# The output format of `ecoflowd`

How `cmd/ecoflowd` puts its readings on the local broker, and why in this form.

> **Provenance:** The statements about `ecoflowd` come from the code in this repo. The
> comparison pattern comes from a capture on the broker of an existing home automation
> setup (23 Sep 2026, about five minutes) and from its Node-RED flow. The statements about
> missing fields and about `dcdc` were measured on the captures in
> `internal/frames/testdata/` (26 Sep 2026); where something is inferred rather than
> measured, it says so.

## Summary

Since **v0.5.0**, `ecoflowd` publishes **two JSON telegrams**: `<topic>/state` per
measurement and `<topic>/energy` with the day's totals, both with serial number and time
of measurement in the payload, not retained. Up to v0.4.x it was one topic per value with
a bare number, plus an availability topic with last will; that is gone without
replacement, and the break was deliberate.

The reason is not taste: `internal/frames.Energy` is a composite – several readings and
one time of measurement from *one* frame. The old format took it apart and then built
three mechanisms to make up for the loss (heartbeat, availability topic, change
detection). With the time of measurement in the payload, none of them is needed any more.

## 1. What `ecoflowd` sends

```
ecoflow/state   {"sn":"HC31XXXXXXXXXXXX","timestamp":"2026-09-22T09:13:16Z","pv":975,"house":-459,"battery":515,"grid":0,"soc":63}
ecoflow/energy  {"sn":"HC31XXXXXXXXXXXX","timestamp":"2026-09-22T09:13:18Z","pv":3301,"house":3206,"batteryIn":1545,"batteryOut":1562,"gridIn":62,"gridOut":175}
```

The values are decoded from `internal/frames/testdata/fast.txt`; the tests in
`cmd/ecoflowd/telegram_test.go` check exactly these. The field tables are in the README,
section "To the local broker".

- `state` goes out on every new measurement: every minute without `--fast`, every two to
  three seconds with `--fast`. There is no change detection any more – an unchanged
  measurement with a new time of measurement is news: the device is still there.
- `energy` goes out as soon as the six parts of the hourly history (254/32) with the same
  timestamp are together (`cmd/ecoflowd/serve.go`, `dailyTotals`).
- `timestamp` is always the **device's time of measurement**, for `energy` that of the
  hourly history. The day's totals apply to the **UTC day**; in Austria they jump to 0 at
  01:00 (CET) or 02:00 (CEST).
- Watts and watt-hours as measured, signs as the device delivers them – positive `grid` is
  export, positive `battery` is charging. Converting is left to the human, the same rule
  as for the addresses in `modbusread`.

### The earlier format (up to v0.4.x)

For reference, in case an old consumer turns up:

```
ecoflow/HC31XXXXXXXXXXXX/pv        0
ecoflow/HC31XXXXXXXXXXXX/house     -293
…
ecoflow/HC31XXXXXXXXXXXX/measured  2026-09-23T20:39:00Z
ecoflow/HC31XXXXXXXXXXXX/energy/…  (six daily totals)
ecoflow/HC31XXXXXXXXXXXX/status    online        ← retained, last will
```

It published on change, plus once a minute even when unchanged. The retained `status`
stays on the broker after the move until you delete it (README, "Moving from v0.4.x").

## 2. The comparison pattern

A home automation setup that grew over time, on the same broker, sends **one flat JSON
object per device** to `myhome/<device>/summary`, regardless of changes:

```
myhome/inverter/summary   {"E":39836000,"P":240,"PC":242.39,"PR":-34,"F":49.98,…}
myhome/heatpump/summary   {"Timestamp":"2026-09-23T20:36:23.168Z","E":26440945,"P":0,…}
myhome/wallbox/summary    {"timeStamp":"…","counter":34274662,"counterUnit":"Wh",
                           "gauge":0.98,"gaugeUnit":"W"}
```

No availability topic, no last will; the receivers check the timestamp themselves.

The weather branch of the same broker carries **both** forms side by side – a `summary`
with the complete JSON *and* fanned-out single values. So the forms do not exclude each
other; `ecoflowd` only offers the one, because a second way in would be a second one to
keep up to date.

**There is no uniform key style there** (as of 25 Sep 2026, from the keys the flow reads):
PascalCase (`Timestamp`, `State`, `Power`), abbreviations (`E`, `P`, `SOC`), camelCase
(`timeStamp`, `unitCounter`), lowercase (`out1`), and twice mixed within one object
(Smartfox: `BoilerE` next to `grid`). So there is no house style `ecoflowd` could follow –
see §4.

## 3. The assumptions behind the old format – and what became of them

### 3.1 "evcc and Home Assistant need one scalar per topic" – **refuted**

evcc's MQTT plugin has a `jq:` expression, Home Assistant has `value_template:
"{{ value_json.pv }}"`. JSON costs one line of configuration there, nothing more.

### 3.2 "A heartbeat is needed" – **obsolete with the timestamp**

The old reasoning was: *"Without a heartbeat a consumer cannot tell 'unchanged' from
'gone'."* That is exactly what a time of measurement in the payload provides, and better:
it is the **device's** clock, not the service's send time.

### 3.3 "A retained reading outlives what it describes" – **only without a timestamp**

If the message carries its time of measurement, a newly connected receiver sees the state
**and** its age. It still stays at `retain: false` – to match the comparison pattern and
because Home Assistant warns against retained values together with `expire_after`, no
longer for the old reason.

### 3.4 "An availability topic reports the outage" – **does not hold up**

The only known consumer had to build its own age watchdog because it could not trust the
retained `status`: if `ecoflowd` hangs without losing the connection, `online` stays and
the last will does not fire. A hanging service can only be noticed by the receiver. Home
Assistant manages with `expire_after`, evcc with `timeout`, without an availability topic.

## 4. The decisions in detail

### Two telegrams, not one

Momentary values and daily totals come from different frames (96/33 or 96/34, and 254/32)
with different timestamps. Packed into one object, that would produce exactly the lie a
composite telegram is meant to avoid: fields of different ages under one timestamp.

### The serial number in the payload, the topic free

There it survives forwarding to InfluxDB or into a queue where the topic gets lost, and a
freely chosen topic fits into any naming scheme that grew over time. **What you give up
for it:** `ecoflow/+/soc` across several devices. Several devices each need their own
`--topic` (the systemd unit reads `/etc/ecoflowd/<SN>.env` for that).

### Topics lowercase, `/state` and `/energy` fixed

MQTT is case-sensitive; a subscription that differs in one letter gets no error, but
nothing. The fixed parts are therefore lowercase. `ecoflowd` takes `--topic` unchanged –
what is typed goes out as it is.

### Keys in camelCase

JSON itself prescribes no style (RFC 8259, ECMA-404). The most-cited guidelines settle on
camelCase: Google's JSON Style Guide, the Microsoft REST API Guidelines, JSON:API.
snake_case is common in Python-adjacent ecosystems (Home Assistant, zigbee2mqtt);
practically nobody recommends PascalCase for JSON. For the receivers it makes no
difference – `value_json.batteryIn` reads like `value_json.battery_in`. Abbreviations are
treated as words: `sn`, `soc`, `pv`. Only `batteryIn`, `batteryOut`, `gridIn`, `gridOut`
are two words.

### A missing field is 0 – measured, not assumed

Up to v0.4.x this section voiced the worry that a missing field would silently be read as
0 and could not be told apart from a measured 0; JSON could leave the field out instead.
The captures answer that differently:

| | `slow.txt` | `fast.txt` | together |
|---|---|---|---|
| energy reports | 47 | 144 | 191 |
| of which without `grid` | 13 | 87 | **100** |
| power field explicitly `0.0` on the wire | 0 | 0 | **0** |
| balance `PV − battery − │house│ − 0` without `grid` | 0.000 W | 0.000 W | **exactly 0** |
| balance with `grid` | ≤ 0.001 W | 0.000 W | |

Fields other than `grid` are never missing. That is the behaviour of **proto3**: a field
that has its default value – 0 for numbers – is not transmitted, and the receiver reads
the absence as that default. Only fields marked `optional` reveal whether they were set.
**That EcoFlow's schema is proto3 without `optional` is inferred, not proven**; what is
proven is the behaviour.

"Missing" here therefore means "measured 0". Leaving it out would be wrong: `grid` would
then be missing from every second telegram, and in the most common state at that (neither
import nor export), and a receiver would get `null` instead of 0. Recorded in
`internal/frames` as `TestAbsentMeansZero`, which fires if a firmware changes the
behaviour.

### `dcdc` only once it is understood

Only what is understood goes into the telegram. `dcdc` (field 2) is not: the name comes
from a third-party source (`dcdc_pwr`, `foxthefox/ioBroker.ecoflow-mqtt`), the field is
not part of the energy balance and follows the battery with the same sign, in the captures
at 63–103 % of its value, without a fixed ratio. Values above 100 % rule out that it is
simply the battery power minus converter losses. Both decoders still read the field;
`ecoflowd` only publishes it once its role is established (`TestDCDCIsNotPublished` holds
that in place).

### What fell away along the way

The `last` map, the heartbeat interval, the staleness watchdog, `setOnlineLocked`,
`announce`, `check`, the last will and both 30-second tickers. What remains: a frame
arrives, build the object, publish.

## 5. What the format costs

- **No immediate notice on a crash.** Without a last will, a receiver notices the
  service's death only after its own deadline instead of within seconds.
- **No selective subscription.** Whoever only wants the state of charge gets everything.
  Irrelevant with seven fields.
- **JSON parsing in every consumer.** One line of `jq` or `value_template`.
- **A break for existing consumers.** Whoever was attached to `ecoflow/<SN>/+` has to
  switch; by the versioning rule in `0.x` a minor step with a note on the break, hence
  v0.5.0.

## 6. Open points

- **Do energy reports arrive at night?** By the same proto3 rule, with PV = 0 the PV field
  would be missing too, and both decoders discard a frame without PV
  (`internal/frames/energy.go`, `scripts/ecoflow-frames.py`). The captures are daytime
  recordings only (PV ≥ 948 W). To be checked with a night-time `ecoflow-api.sh live`; if
  the answer is "no", the PV check has to change – in both versions and with new
  `.golden` files.
- **How often does `energy` come with `--fast`?** The hourly history then arrives
  considerably more often. Not counted; if it becomes too much, "only send when the totals
  changed" would be a decision of its own.
- **What does `dcdc` measure?** See §4.

## Sources

- https://www.rfc-editor.org/rfc/rfc8259 – JSON, no rule on naming style
- https://google.github.io/styleguide/jsoncstyleguide.xml – Google's JSON Style Guide, camelCase
- https://github.com/microsoft/api-guidelines – Microsoft REST API Guidelines, camelCase
- https://jsonapi.org/recommendations/ – JSON:API, camelCase for member names
- https://protobuf.dev/programming-guides/proto3/#default – default values in proto3
- https://protobuf.dev/programming-guides/field_presence/ – when a field is transmitted
- https://docs.evcc.io/en/docs/devices/plugins – `jq` in the MQTT plugin
- https://github.com/evcc-io/evcc/pull/943 – introduction of `jq` parsing
- https://www.home-assistant.io/integrations/sensor.mqtt/ – `value_template`,
  `expire_after`, `availability_topic`
- https://www.zigbee2mqtt.io/guide/usage/mqtt_topics_and_messages.html – one JSON object
  per device
- https://tasmota.github.io/docs/MQTT/ – `tele/<topic>/SENSOR` as a composite telegram
- https://sparkplug.eclipse.org/ – composite payload with a timestamp per metric
