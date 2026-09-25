# API status – EcoFlow PowerOcean DC Fit

State of research: September 2026.

## Summary

There is **no** officially documented, specific REST API for the DC Fit.
Four paths were examined; exactly one delivers readings continuously today:

| Path                           | Type                       | Status                                                                                                                                                  |
|--------------------------------|----------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------|
| EcoFlow Developer/Open API     | Cloud, REST, HMAC-signed   | **Unusable.** Lists the DC Fit, but refuses the readings: error 1006 "not allowed" – confirmed on the device. The MQTT channel stays silent as well    |
| Consumer portal                | Cloud, REST, session token | **Delivers data, but not current data.** Hands out the state last pushed to the cloud; in one measurement it stood still for over an hour             |
| The app's MQTT channel         | Cloud, MQTT, protobuf      | **The only path on which readings flow continuously.** Every minute on its own, every second with the stream switch. Unofficial, reverse-engineered    |
| Local Modbus TCP               | Modbus, port 502           | **Still locked on the device** (`connection refused`). Would be the stable path, but needs unlocking by an installer                                  |

In practice this means: whoever wants values today takes the app MQTT channel —
`scripts/ecoflow-api.sh` for measuring, `cmd/ecoflowd` for continuous operation. Whoever
wants reliability pursues the Modbus unlocking.

This file covers the path *to the device*. How `ecoflowd` passes the values it obtains on
to the local broker — two JSON telegrams, and why in this form — is in
[`mqtt-output.md`](./mqtt-output.md).

## 1. EcoFlow Developer/Open API (cloud)

- Endpoint: developer.ecoflow.com
- REST-based, requests HMAC-signed
- In principle covers the entire EcoFlow device fleet
- Access: developer account on developer-eu.ecoflow.com (EU), generate accessKey and
  secretKey there. Hosts: `api-e.ecoflow.com` (EU), `api-a.ecoflow.com` (US).
- Signature: HMAC-SHA256 of the string
  `<business-params, ASCII-sorted>&accessKey=…&nonce=…&timestamp=…` with the
  secretKey, as hex in the header `sign`; plus the headers `accessKey`, `nonce`
  (6 random digits) and `timestamp` (milliseconds).
- Nested bodies are *flattened* for the signature: objects dotted,
  arrays indexed – `deviceInfo.id=1&deviceList[0].id=1&ids[0]=1&name=demo1`.
  The docs provide a test vector for this, which `scripts/ecoflow-api.sh selftest`
  checks against (source: developer-eu.ecoflow.com, "HTTP access steps").
- The Content-Type decides where the server takes the parameters from:
  `application/json;charset=UTF-8` → request body, otherwise → query string.
- The **raw** values are signed; URL encoding of the query string only happens
  afterwards (shown in the official Java demo client, `HttpUtil.getHttpUriRequest`).
  For serial numbers and quota names without special characters this has no effect.
- The official demo client knows five endpoints in total: `certification`,
  `device/list`, `POST device/quota`, `PUT device/quota` (writing, deliberately
  not used here) and `GET device/quota/all`. All the reading ones among them are
  measured below – **there is no further cloud path that would still be open.**
- Relevant read endpoints: `/iot-open/sign/device/list` (devices on the account),
  `/iot-open/sign/device/quota/all?sn=…` (all values of a device),
  `/iot-open/sign/certification` (MQTT credentials: `certificateAccount`,
  `certificatePassword`, `url` = `mqtt-e.ecoflow.com`, `port` = 8883, MQTTS).
- MQTT topics per device: `/open/<certificateAccount>/<SN>/quota` and `.../status` (device
  → app) as well as `.../get`, `.../set` with their `_reply` counterparts (app → device).
  On **this** channel `.../set` stays outside `scripts/ecoflow-api.sh`; `.../get` is
  reachable through the command `request`, whose topic suffix is hard-wired. (On the *app*
  channel `fast` does publish to `set` – see "The command for the fast rate".)
- Callable fully signed with [`scripts/ecoflow-api.sh`](./scripts/ecoflow-api.sh).

### Error 1006 is a model blocklist

**Known problem:** For the PowerOcean family the API frequently returns error code **1006
"not allowed"**; the Open API's MQTT topic then delivers no data either. This is not a
misconfiguration but a device-side blocklist – the integration
`shuette42/ecoflow-energy-ha` calls it "an EcoFlow API limitation, not a configuration
problem" and lists the affected SN prefixes:

| Device group      | Blocked SN prefixes (error 1006)                                     |
|-------------------|----------------------------------------------------------------------|
| PowerOcean        | `J327`, `J32D`, `J32E`                                               |
| PowerOcean DC Fit | `HC31` (measured here ourselves, not listed in the community lists)  |
| PowerOcean Plus   | `R371`, `R372`, `R374`, `HJ3C`                                       |
| Stream / STREAM   | `BK01`, `BK21`, `ES21`, `ES22`                                       |
| Other             | `HZ31`, `S02F` (Solar Tracker), `AC71` (WAVE 3)                      |

As *reachable* via the Developer API, the same source lists the PowerOcean prefixes
`HJ31`, `HJ32`, `HJ35`, `HJ36`, `HJ37`, `J32B`, `J329`.

**The DC Fit (`HC31`) is blocked – measured on the device, September 2026.** The block
only takes effect when fetching data, though, not when listing. Measured with
`scripts/ecoflow-api.sh` on our own account:

| Call                                                                                                         | Response                                                                                 |
|--------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------|
| `GET /iot-open/sign/device/list`                                                                             | `"code": "0"`, device listed with `"online": 1` and `"productName": "PowerOcean DC Fit"` |
| `GET /iot-open/sign/device/quota/all?sn=…`                                                                   | `"code": "1006"`, `"current device is not allowed to get device info"`                   |
| `POST /iot-open/sign/device/quota` (selected values, the path described in the official PowerOcean docs)     | likewise **1006**                                                                        |

**The block depends on the device, not on the endpoint.** That the signature is built
correctly is shown independently of this: `scripts/ecoflow-api.sh selftest` reproduces the
test vector from EcoFlow's own docs. The path described in EcoFlow's own
PowerOcean docs (`POST /iot-open/sign/device/quota` with
`{"sn": …, "params": {"quotas": ["bpSoc"]}}`) is also rejected with 1006. The signature is
demonstrably correct here – a faulty signature would produce a signature error,
not a rejection on the merits. Telling detail: the examples in these docs use
SNs with the prefix `HJ31`, i.e. one of those listed as reachable.

**So listed does not mean readable.** Whoever only tests `device/list` wrongly takes the
cloud path to be open; the block only shows on the second call. The prefix `HC31`
therefore belongs on the 1006 blocklist above, even though none of the community sources
lists it.

Side finding: an SN that is *not* bound to the account answers `quota/all` with
`"code": "8512"` / `"no permission to do it"` – a different error from 1006 and a
useful test of whether the owner binding is in place at all.

**It is not a registration or mapping problem.** The obvious suspicion that the
system is not assigned to the developer account is refuted: `device/list` returns, with
the same keys, our own SN including `online` and `productName` – which establishes the
binding. The API distinguishes the cases cleanly (8512 "does not belong to you" vs. 1006
"this device does not hand out data"), and the message text of 1006 speaks about the
device, not about permission. That it is not a client problem either is shown by the
reproduced signature test vector and the comparison with the official demo client.

### Official field names (for comparison with the Modbus registers)

Even though the endpoints are blocked for the DC Fit: EcoFlow's PowerOcean docs
(developer-eu.ecoflow.com, document "PP2") name the quantities the device knows. This is
the only *official* source for these semantics and thus the best cross-check for the
community-derived Modbus registers in `modbus-registers.md`:

| Field                                  | Type  | Meaning                                                     |
|----------------------------------------|-------|-------------------------------------------------------------|
| `bpSoc`                                | float | Battery SOC                                                 |
| `bpPwr`                                | float | Battery power                                               |
| `mpptPwr`                              | float | MPPT/PV power                                               |
| `sysLoadPwr`                           | float | Load power                                                  |
| `sysGridPwr`                           | float | Grid power                                                  |
| `pcsAPhase`, `pcsBPhase`, `pcsCPhase`  | json  | per phase: `vol`, `amp`, `actPwr`, `reactPwr`, `apparentPwr` |
| `mpptHeartBeat`                        | json  | list `mpptPv`, each with `vol`, `amp`, `pwr`                |
| `evPwr`, `chargingStatus`, `errorCode` | –     | PowerPulse (wallbox)                                        |

Sign convention according to the docs' examples: negative `actPwr`/`sysGridPwr`
mean feed-in, negative `bpPwr` discharging. **Caution:** For `sysGridPwr` at the
portal endpoint the opposite holds on the DC Fit (see "Signs: measured, not
assumed"). The docs' statement is therefore not to be adopted unchecked for this field.

Also documented: `POST /iot-open/sign/device/quota/data` for historical values (period at
most one week, e.g. `code: JT303_Dashboard_Overview_Summary_Week`), and the MQTT topics
`/open/${certificateAccount}/${sn}/quota` as well as `.../status` – the latter with
`params.status` (0 = offline, 1 = online).

### MQTT path of the Open API (DC Fit, September 2026)

The MQTT path behaves **differently from the REST path** – the block does not take effect
here when connecting:

| Step                                        | Result                                                                          |
|---------------------------------------------|---------------------------------------------------------------------------------|
| `/iot-open/sign/certification`              | Code 0, returns `certificateAccount`, password, `mqtt-e.ecoflow.com`, port 8883 |
| CONNECT (MQTTS, port 8883)                  | `CONNACK (0)` – authentication accepted                                         |
| SUBSCRIBE to `/open/<acct>/<SN>/#`          | **rejected** ("All subscription requests were denied")                          |
| SUBSCRIBE to `/open/<acct>/<SN>/quota`      | **granted** (`SUBACK`, Granted QoS 0)                                           |
| SUBSCRIBE to `/open/<acct>/<SN>/status`     | **granted**                                                                     |
| SUBSCRIBE to `/open/<acct>/<SN>/get_reply`  | **rejected** (`SUBACK` 128 = 0x80)                                              |
| PUBLISH to `/open/<acct>/<SN>/get`          | **rejected** (`PUBACK` RC 135 = 0x87 "Not authorized")                          |

Important for your own tests: **wildcards are rejected by the ACL, exact topics are not.**
A test with `#` therefore produces a false negative – exactly the fallacy that hastily turns
"denied" into "MQTT is blocked".

**On the granted `quota` topic, however, no messages arrived** (observed over
several minutes; the connection demonstrably stayed healthy throughout – `PINGREQ`/`PINGRESP`
went through). So the block acts *silently* here: connection and subscription are accepted,
nothing is published. This matches other users' reports that the Open API's MQTT topic
also delivers no data for 1006 models.

Limitation: this is a negative finding from a short observation. A push could
theoretically depend on conditions (time of day, load changes, firmware). Whoever
checks it again: let it run longer and deliberately make the system move (e.g. switch on
consumers) – do not test with `#`, see the wildcard note above.

Re-measurable with `scripts/ecoflow-api.sh -v mqtt <SN>`; verbose mode shows CONNACK,
SUBACK and the keepalive packets, and every incoming message is logged with a
timestamp.

**In short:** the account may connect and subscribe to exactly two topics, on
which nothing is published for this device. It may not make requests. Of the six
documented topics, two silent ones remain – consistent with the 1006 on all
REST data endpoints.

Important for interpreting the publish test: under MQTT 3.1.1 the broker acknowledges a
publish even when the ACL discards it – "no answer" would not have been interpretable
there. Only **MQTT v5 with QoS 1** returns a reason code in the PUBACK and thereby
separates "the broker did not pass it on" (0x87) from "the device did not answer". That is
exactly how `scripts/ecoflow-api.sh request` measures, and the answer was 0x87: the
request never left the broker.

- Sources: https://github.com/Feberdin/ecoflow-powerocean-ha (README),
  https://github.com/shuette42/ecoflow-energy-ha (prefix lists)

**Conclusion:** For the PowerOcean family including the **DC Fit**, the official cloud API
cannot be used to read readings – neither via REST (1006) nor via MQTT (two subscribable
but silent topics; requests forbidden by the ACL).

## 2. The consumer portal (REST)

`user-portal.ecoflow.com` shows a complete dashboard for the same device – SOC,
solar, house, grid and battery power, daily/monthly/yearly yields. Observed on our own device
(September 2026): for this the portal calls

```
GET https://api-e.ecoflow.com/provider-service/user/device/detail?sn=<SN>   → HTTP 200
```

**The same host as the Developer API, but a different service** (`provider-service`
instead of `iot-open`) and different authentication: the portal's session token (`S1_JWT`
in local storage) instead of the HMAC-signed API keys. The 1006 block evidently does not
apply there – what is blocked is the *Developer API*, not the owner's access to data.

Fetchable with `scripts/ecoflow-api.sh portal <SN>`, token via `ECOFLOW_PORTAL_TOKEN`.

**Two headers, nothing more is needed** (tried on the device, September 2026):
`Authorization: Bearer <token>` and **`product-type: 85`** – the product ID that the
portal itself carries as `productKey` in the URL. If it is missing, the endpoint answers with
`code 0` and **without** `data`; that looks like an empty account, but is only the missing
header. The web interface additionally sends signature headers (`x-appid`, `x-nonce`,
`x-sign`, `x-timestamp`); whether you send them along changes nothing about the answer.

### What the endpoint delivers

The top level carries **exactly the field names of the official PowerOcean docs** – that is,
what the Developer API refuses for this device with 1006:

| Field                                                       | Example value | Meaning                                                  |
|-------------------------------------------------------------|---------------|----------------------------------------------------------|
| `bpSoc`                                                     | 51            | Battery SOC in %                                         |
| `bpPwr`                                                     | -204.28       | Battery power                                            |
| `sysLoadPwr`                                                | -204.28       | House load                                               |
| `sysGridPwr`                                                | 0.0           | Grid power – **positive = feed-in** (see below)          |
| `mpptPwr`                                                   | 0.0           | PV power                                                 |
| `online`                                                    | 1             | Device status                                            |
| `todayElectricityGeneration` … `totalElectricityGeneration` |               | Daily/monthly/yearly/total yield                         |

Below that lies a `quota` object with the firmware's raw blocks (prefix `DC303_`, which
presumably stands for the DC Fit model):

| Block                                                                                    | Fields | Content                                                                                                                                                         |
|------------------------------------------------------------------------------------------|--------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `DC303_EMS_HEARTBEAT`                                                                    | 69     | SOC limits, meter values per phase (`meterAVoltage`, `meterACurrent`, …), daily energies, error codes/masks, `workingMode`, `sysWorkSta`, MPPT voltage window    |
| `DC303_DCDC_STA_HEARTBEAT`                                                               | 54     | DCDC status                                                                                                                                                     |
| `DC303_DCDC_CHANGE_HEARTBEAT`                                                            | 26     | DCDC changes                                                                                                                                                    |
| `DC303_ENERGY_STREAM_REPORT`                                                             | 9      | `bpSoc`, `bpPwr`, `pvPwr`, `gridPwr`, `loadPwr`, `dcdcPwr`, `heatingPower`, `timestamp`                                                                         |
| `DC303_ERROR_CHANGE_HEARTBEAT`                                                           | 4      | Error states                                                                                                                                                    |
| `DC303_BMS_HEARTBEAT`, `DC303_BP_CHANGE_HEARTBEAT`, `DC303_ECOLOGY_DEV_BIND_LIST_REPORT` | 1–2    | Battery and binding info                                                                                                                                        |

That is **more than Modbus exposes** (see `modbus-registers.md`, "Known gaps"),
and `DC303_ENERGY_STREAM_REPORT` carries its own timestamp – so it is also suitable for
recording, not just for a momentary value.

#### The timestamp stands still when nobody is looking

This very timestamp is what `scripts/ecoflow-api.sh status` prints as `measured`
– and it is the **firmware's time of measurement, not the time of the query**. Observed on
the device (22 September 2026): three `status` calls in quick succession returned
exactly the same answer three times, `measured` stayed at `2026-09-22T06:18:28Z`, while
the system was demonstrably running. After opening the app or the portal the value
moved again.

Only one thing is established by this: **the REST endpoint does not poll the device.** It hands
out what was last pushed to the cloud. A `status` call therefore looks like a
live value and is a still image.

**The obvious explanation was wrong.** It went: pushing only happens while a
client is actively *asking*; without a wake-up call the stream dries up. Third-party evidence
spoke for this — `jensfr1/ha-ecoflow-ocean2` keeps a 60-second interval with the comment "Ohne
diesen regelmaessigen Weckruf sendet das Geraet keine Telemetrie, solange keine
EcoFlow-App geoeffnet ist" ("without this regular wake-up call the device sends no
telemetry as long as no EcoFlow app is open"); `shuette42/ecoflow-energy-ha` asks every
20–30 s; the openHAB binding reports updates for the STREAM Micro only while the app is open.

Measured on our own device, this does **not** hold here: 23 minutes without a single
publish, minute values throughout (see "Measured on the DC Fit"). It is enough **to be
subscribed** — nobody has to ask.

What causes the freezing thus remains open. It is conceivable that the device only pushes
while *some* subscription exists, and the portal session is one; or that the
REST endpoint reads from a different store than the MQTT channel. From the outside this cannot
be told apart.

**For practical purposes it makes no difference:** the REST endpoint is no good as a live
source — not even while a subscription is running. This was cross-checked: `measured`
stood still while the MQTT frames were already an hour further on.

#### Signs: measured, not assumed

The signs are **not uniform**, and the portal's interface shows absolute values
anyway. Determined on the device (September 2026):

| Field        | positive means                                     |
|--------------|----------------------------------------------------|
| `bpPwr`      | battery **charging** (negative = discharging)      |
| `sysGridPwr` | **feed-in** (negative = import)                    |
| `sysLoadPwr` | is reported negative while the house is consuming  |

Decided by the energy balance of a sunny measurement: 3378 W PV splits into
514 W house, 609 W into the battery and 2255 W to the grid – that only adds up if the positive
grid figure *leaves* the house. **The portal endpoint thus contradicts the official
field description**, which suggests negative = feed-in for `actPwr`/`sysGridPwr`; for the
DC Fit the measured direction applies.

`scripts/ecoflow-api.sh status` therefore prints absolute values and writes the direction
next to them as a word.

The token can be obtained in two ways: from the logged-in browser (*Local Storage → `S1_JWT`*)
or via the login endpoint of the consumer app:

```
POST https://api-e.ecoflow.com/auth/login
{"email": "…", "password": "<base64>", "scene": "IOT_APP", "userType": "ECOFLOW"}
→ data.token, data.user.userId
```

That is exactly what `scripts/ecoflow-api.sh login [e-mail]` does: it prints only the token
to stdout, so it can be captured directly –
`export ECOFLOW_PORTAL_TOKEN="$(scripts/ecoflow-api.sh login)"`, then `status <SN>`.

The password is only transmitted **base64-encoded, not hashed** – encoding, not
encryption. Whoever does not want that takes the browser token: a smaller secret, it expires by
itself. Source for the flow: `shuette42/ecoflow-energy-ha`, `enhanced_auth.py`.

The same source also says what else this token is good for: it opens the app's **MQTT
channel**, on which, unlike the Open API channel, data actually flows. Two doors
lead there.

**The simple one (used by this repo):**

```
GET /iot-auth/app/certification?userId=<userId>
Authorization: Bearer <token>
lang: en_US
→ data.certificateAccount, data.certificatePassword, data.url, data.port, data.protocol
```

Plain-text JSON, no decryption needed. Fetchable with
`scripts/ecoflow-api.sh app-cert`; the `userId` comes from `ECOFLOW_USER_ID` (the
`login` command prints it ready to export).

**The encrypted one:** `/iot-auth/enterprise-development/user/certification` – which the
portal calls itself when loading – returns the same fields **AES-encrypted**, but
without `userId`. More precisely than it stood here before:

| Parameter  | Value                                                             |
|------------|-------------------------------------------------------------------|
| Method     | AES-256 in **CFB128** mode – *not* CBC                            |
| Key        | `SHA256(token)` as **raw 32 bytes** (not hex, not truncated)      |
| IV         | ASCII constant `ojsajkqjwk1w2dfg` from the portal JS              |
| Encoding   | `data` is a Base64 string, not an object                          |
| Padding    | PKCS7 – the leftover bytes remain after decryption                |

Reproducible with the openssl CLI:

```bash
KEY=$(printf %s "$TOKEN" | openssl dgst -sha256 -binary | xxd -p -c64)
printf %s "$CIPHERTEXT_B64" |
  openssl enc -d -aes-256-cfb -K "$KEY" -iv 6f6a73616a6b716a776b317732646667 -a -A -nopad
```

**Deliberately not implemented.** The plain-text path delivers the same credentials, and a
crypto path in a shell script is code that can silently produce wrong bytes.
It is here in case EcoFlow closes the simple door. It also remains unconfirmed whether
the `enterprise-development` path answers a pure consumer token at all –
the path name suggests a Pro/installer context.

**Assessment:** this is an internal interface of the web interface, neither documented
nor promised by EcoFlow, and the token expires. As a permanent data source it is no
good – local Modbus remains the stable path. As a cross-check when verifying the
Modbus registers, on the other hand, it is excellent: the same quantities, from EcoFlow's own
display.

According to `MaxGrmm/EF-PowerOcean-TcpModbus`, the same endpoint delivers considerably
more than the dashboard shows – cell voltages, SOH, per-phase active/reactive/apparent
power, around 180 grid protection parameters. **Unchecked:** the project named is a Modbus
integration, and whether the statement refers to this REST endpoint or to registers is not
clear from it.

**What this path is good for:** as a cross-check and for counter readings, not for continuous
readings – for those, the app MQTT channel (section 3). The stable path would remain Modbus
(section 4), once it is unlocked.

## 3. The app's MQTT channel

The channel the app uses – and the only cloud channel on which messages actually
arrive for this device. Subscribable with `scripts/ecoflow-api.sh live <SN>`.

**Broker:** `mqtt-e.ecoflow.com:8883` (MQTTS); host and port come from the
certification response; user name and password are `certificateAccount` and
`certificatePassword` from it – not the account credentials, not the token.

**Client ID:** `ANDROID_<32 hex characters, upper case>_<userId>`. This is not
cosmetic: the broker rejects client IDs that do not look like this, and it rejects an ID it has
already seen again after a disconnect. That is why `live` builds a new one for **every**
connection.

**Topics** (avoid wildcards – the ACL rejects them; for the fallacy that results
from this, see section 1, "MQTT path of the Open API"):

| Topic                                           | Direction | Content                         |
|-------------------------------------------------|-----------|---------------------------------|
| `/app/device/property/<SN>`                     | subscribe | telemetry push, **protobuf**    |
| `/app/<userId>/<SN>/thing/property/get`         | publish   | request/wake-up call            |
| `/app/<userId>/<SN>/thing/property/get_reply`   | subscribe | answer to it                    |
| `/app/device/status/<SN>`                       | subscribe | online/offline                  |
| `/app/<userId>/<SN>/thing/property/set`         | publish   | **writing** – only `fast`       |

**The wake-up call** that `live` sends to the `get` topic every `ECOFLOW_LIVE_INTERVAL`
seconds (default 30):

```json
{"from":"Android","id":"<milliseconds>","moduleType":0,
 "operateType":"latestQuotas","params":{},"version":"1.0"}
```

**What `live` deliberately does not do:** the app activates its fast stream (~2–3 s) via
a protobuf frame `EnergyStreamSwitch` on the `.../set` topic. `live` only publishes
to `get` topics – whoever queries values should not write unnoticed in the process. The switch
is sent only by the command `fast` (see "The command for the fast rate"), and by the
Go service `cmd/ecoflowd` only with the flag `--fast`.

The price for `live` is the device's minute rate – **not** the rate of its own
requests: those, as measured further below, make no difference to the data flow.

**Format:** on the PowerOcean the push is **protobuf**, not JSON – `jq` does not help there.
`live` therefore prints every message as hex and additionally writes out the text only
if the payload is entirely printable. On `get_reply`, **nothing at all** arrived on the DC Fit
(see "Measured on the DC Fit"), so the question of JSON or protobuf
does not even arise there.

**Plus vs. DC Fit caveat.** (On the ID: the energy stream exists on **33** *and*
**34** – the fast and the minute report, see "Measured on the DC Fit". Third-party sources
name only one of the two.) The protobuf field numbers differ between the
models. For the JT-S1 PowerOcean, `cmd_func 96 / cmd_id 33` carries the order
`sys_load_pwr, sys_grid_pwr, mppt_pwr, bp_pwr, bp_soc`; the DC Fit definition in
`foxthefox/ioBroker.ecoflow-mqtt` (device type `poweroceanfit`) assigns the same ID
`grid_pwr, dcdc_pwr, bp_pwr, pv_pwr, timestamp, timezone, bp_soc, load_pwr, …`.
Whoever takes the wrong table here gets plausible numbers under the wrong names. This
is the same caveat as for the register mapping.

**Origin:** endpoint path, client ID form, topics and wake-up payload were taken
from third-party reverse engineering. They were checked on the device on 22 September 2026 –
see the next section. Sources: `shuette42/ecoflow-energy-ha`,
`tolwi/hassio-ecoflow-cloud`, `jensfr1/ha-ecoflow-ocean2`,
`foxthefox/ioBroker.ecoflow-mqtt`.

### Measured on the DC Fit (22 September 2026)

The channel **works**. `app-cert` answers with `code 0` and returns
`mqtt-e.ecoflow.com:8883` along with credentials; the broker accepts the client ID of the form
`ANDROID_<hex>_<userId>`, all three topics are subscribed, and on
`/app/device/property/<SN>` **frames arrive every few seconds**. This makes it the
only cloud path on which readings actually flow for this model.

Three findings from the same measurement:

**1. The wake-up call is not answered – and it is superfluous.** In two and a half minutes
not a single message arrived on `.../thing/property/get_reply`; the push ran anyway.
Re-measured on 22 September 2026 with `ECOFLOW_LIVE_INTERVAL=0`, that is **without a
single publish**, with the app and the portal closed:

| | |
|---|---|
| Duration | 22.8 minutes (10:28–10:51 UTC) |
| `96/34` minute report | 47 frames, every minute without gaps, spacing 58–61 s |
| decoded readings | 23, one per minute, without dropouts |
| `254/32` hourly history | kept coming, about every 10 minutes |
| `96/33`, `96/3`, `96/137` | **zero** |

**The subscription alone keeps the device talking.** The minute rate needs no
wake-up call, no stream switch, no publish at all – a purely reading client
is enough. The loop that `live` brings along was thus taken over from third-party projects and
has no effect for this model; `ECOFLOW_LIVE_INTERVAL=0` switches it off.

**2. The REST endpoint does not get fresh from this.** Throughout the whole time `status`
returned an unchanged `measured : 2026-09-22T07:13:28Z`, while the MQTT frames already
carried `08:19Z` – over an hour's difference. **The detour through `provider-service` is
therefore not a live source, not even with a listener running.** Whoever wants current
values has to evaluate the frames.

**3. The payload is XOR-obfuscated.** Every byte of the payload is **exclusive-ORed** (XOR,
not OR) with the low byte of the sequence number (header field 14). It was noticed
because two
frames with adjacent sequence numbers differ in *every* byte by the same bit pattern.
Without this step the payload is not valid protobuf.

**Frame structure** (field numbers of the outer header, confirmed):

| Field | Meaning                                  |
|-------|------------------------------------------|
| 1     | payload (XOR-obfuscated, see above)      |
| 8     | `cmd_func` – 96 for all frames here      |
| 9     | `cmd_id` – distinguishes the reports     |
| 14    | sequence number, also the XOR key        |

**Observed `cmd_id` for `cmd_func 96` in slow mode:** 1, **34**, 108, 109, 110,
111, 136. With an active stream switch, **33**, **3** and **137** are added, and
`cmd_func 254 / cmd_id 32` becomes frequent — see the sections further below.

**The energy stream exists twice, on `cmd_id 34` and `cmd_id 33`** – with the same
field assignment, but a different rate and different packaging:

| ID      | Rate          | Timestamp       | Payload                         | Condition                    |
|---------|---------------|-----------------|---------------------------------|------------------------------|
| **34**  | exactly every minute | rounded to the minute | fields wrapped in a message | always comes                 |
| **33**  | every 2–3 s   | accurate to the second | fields directly in the payload | with the stream switch; without it considerably rarer, see below |

So whoever only measures with `live` sees exclusively 34 and takes 33 to be nonexistent –
and whoever follows the third-party source that only names 33 finds nothing at all without
the switch.

**While the fast stream is running, 34 is superfluous:** across a series of measurements
*every* minute report had a per-second report with the same timestamp. So it contributes
nothing, but looks like a standstill because its timestamp is rounded to the
minute. `ecoflow-frames.py` therefore suppresses it as long as a 33 arrived within the last
90 seconds – and shows it again as soon as the fast stream dries up.

Independently of this, the device sends some frames **twice**, for both IDs. Two
identical readings are one reading, so a line identical to the
previous one is suppressed. Two *different* values in the same second remain – those
occur and are not a repetition.

The field assignment is the same in both cases:

| Field | Type    | Meaning                                |
|-------|---------|----------------------------------------|
| 1     | float   | grid power (positive = feed-in)        |
| 2     | float   | DCDC power – role unclear, see below   |
| 3     | float   | battery power (positive = charging)    |
| 4     | float   | PV power                               |
| 5     | uint32  | timestamp (Unix seconds, UTC)          |
| 6     | sint32  | time zone                              |
| 7     | uint32  | SoC in %                               |
| 8     | float   | house load (reported negative)         |

**How this is established:** via the energy balance. In every single frame
`PV = battery + house + grid` holds to two decimal places – for example 968.59 W PV =
530.0 W battery + 351.6 W house + 87.0 W grid. A wrong field assignment would not
hit that. The signs match those of the portal endpoint (see "Signs:
measured, not assumed").

**Rate:** the device reports the energy stream **exactly every minute** and sends every frame
**twice**. The device timestamp lies on the full minute (`08:30:00Z`,
`08:31:00Z`, `08:32:00Z`).

The rate does **not** depend on the wake-up call: a run with `ECOFLOW_LIVE_INTERVAL=5` still
reported every minute. The app unlocks the faster rhythm via the `.../set` topic
– see the next section.

#### The command for the fast rate, captured rather than guessed

The `set` topic can be **subscribed to**, and subscribing is reading. Whoever operates the
phone app at the same time sees what it sends – `scripts/ecoflow-api.sh app-mqtt <SN>` does
exactly that. Recorded on 22 September 2026:

```
0a390a0408011001102018602001280138034060486150045801700a800103880101ba0103696f73ca0110<SN as ASCII>
```

| Field  | Value           | Meaning                                         |
|--------|-----------------|-------------------------------------------------|
| 1      | `08 01 10 01`   | payload: two flags, both 1 – **in plain text**  |
| 2      | 32              | `src` – the app                                 |
| 3      | 96              | `dest` – the energy management                  |
| 4, 5   | 1, 1            | `dSrc`, `dDest`                                 |
| 7      | 3               | unknown                                         |
| 8      | 96              | `cmd_func`                                      |
| 9      | **97**          | `cmd_id` – the stream switch                    |
| 10     | 4               | `dataLen`                                       |
| 11     | 1               | `needAck`                                       |
| 14     | small counter   | `seq` – 3, 5, 7, 10 … starting in single digits |
| 16, 17 | 3, 1            | `version`, `payloadVer`                         |
| 23     | `"ios"`         | what the app identifies itself as               |
| 25     | serial number   | as ASCII                                        |

The app repeats this about every three seconds; the device then reports just as often.

**The obfuscation only applies device → app.** What the app sends is unencrypted –
the XOR step is dropped in this direction.

**Why capturing made the difference.** An attempt assembled from the third-party sources
was wrong in four places: obfuscated instead of plain payload, wrongly nested content
(`0a020801` instead of `08011001`), a six-digit instead of a single-digit sequence number,
plus two invented and two missing fields. Only `cmd_func 96` and `cmd_id 97` had been
guessed right. On a write topic that would have been a shot in the dark – there is no
reason to guess something like this when you can measure it.

Implemented in `scripts/ecoflow-api.sh fast <SN>`: the same as `live`, plus this one frame
every `ECOFLOW_FAST_INTERVAL` seconds. It is the **only** command of the script that
publishes to a `set` topic, it carries no parameters, and it is deliberately a command of
its own – so that the write access never happens as a side effect of a value query.

**Measured on the device (22 September 2026):**

- The broker **accepts the publish**: `PUBACK RC:0` under MQTT v5 with QoS 1. So on the
  app channel's `set` topic no ACL block applies – unlike on the Open API channel's `get`
  topic, where the same test returned `0x87`.
- **The repeat interval is decisive.** At 3 seconds (the app's rhythm) the fast
  stream runs: in the capture 131 readings over 241 seconds, on average every 1.9 s.
  At 10 seconds the device fell back to the minute rate; the default is therefore 3.
  (How long the switch keeps working afterwards is **not** said by this – see the open question
  on it at the end of the file.)
- Incidentally, `cmd_func 254 / cmd_id 32` also became visible (about twice per second) as
  well as `96/3`, `96/1` and `96/137` at per-second rates – none of them evaluated.

**The price with the script:** it starts a separate `mosquitto_pub` for each switch, i.e.
a connection setup every 3 seconds – around 28,000 a day. Fine for a measurement,
not for continuous operation; `mosquitto_pub` cannot hold a standing connection from the
command line. That is exactly why there is `cmd/ecoflowd`, which holds one.

#### The hourly history: `cmd_func 254 / cmd_id 32`

By far the most frequent frame in fast mode – about twice per second. It carries
**no momentary values, but the energy balance of the current day, hour by hour.**

Structure: the payload (likewise XOR-obfuscated) contains in field 2 a message with

| Field | Content                                                       |
|-------|---------------------------------------------------------------|
| 1     | timestamp, Unix seconds                                       |
| 2     | which flow – 1, 16, 32, 48, 64, 80                            |
| 3     | **24 concatenated varints**: one value per hour of the day, Wh |

Six frames with the same timestamp make up a complete report:

| Field 2 | Flow                         |
|---------|------------------------------|
| 1       | PV generation                |
| 16      | battery charged              |
| 32      | battery discharged           |
| 48      | grid import                  |
| 64      | grid feed-in                 |
| 80      | house consumption            |

The current hour is still filling up; all later hours are at 0.

**How the assignment is established – two independent ways.** First via the slope: over
four minutes each counter grew exactly with the power of its flow (PV 1013 W against
a measured 1029 W, battery 529 against 544, house 454 against 449, grid 30 against 39). Second
via the **hourly balance**, which adds up to ±1 Wh:

```
PV + battery out + grid import  =  house + battery in + grid feed-in
```

Example from 22 September 2026, hour 7 UTC: 1350 + 0 + 1 = 473 + 798 + 81 (±1 Wh
rounding). The zeros are in the right place too: no PV before dawn, the battery discharges
at night and charges as soon as the sun carries the house.

Fetchable with `scripts/ecoflow-api.sh fast <SN> | python3 scripts/ecoflow-frames.py --hours`.

The sum matches the device itself – field 23 in `96/1` carries the same sum as a
float, to within 5.5 Wh in the same one-second window – and **after sunset also
the portal's `todayElectricityGeneration`**, to within 0.058 %. During the day the two
drift apart because the portal updates in jumps; see the list of open questions.

#### The response to the switch: `96/3` and `96/137`

Both come at per-second rates in fast mode. The switch sets `needAck = 1`, so the
device answers with both.

> **Limitation.** It said here that they do not come **at all** without the switch ("zero
> without it"). That did not hold up under a longer observation: over 264 covered minutes
> without the switch, `96/3` came at 1.29 and `96/137` at 1.08 frames per minute –
> compared with 21.5 and 18.9 respectively with the switch. So the switch **speeds them
> up** seventeenfold; it does not switch them on. The earlier zero came from a short
> control run.
>
> One caveat remains: during the long observation it cannot be ruled out
> that **another client** (the phone app) was talking along at times. A clean
> counter-proof would need a long run with the app demonstrably closed.

**`96/137` has an empty payload** – a pure acknowledgement, without content.

**`96/3` is a component list.** Byte-for-byte identical across 83 frames, so
not a measurement. Each field contains an embedded message whose field 1 carries a
serial number as ASCII:

| Field | Example value      | Component                                  |
|-------|--------------------|--------------------------------------------|
| 1     | `HC31XXXXXXXXXXXX` | the system itself – the queried SN         |
| 2     | `HC31YYYYYYYYYYYY` | PV Storage Converter, 5 kW                 |
| 3     | `HJ3AXXXXXXXXXXXX` | battery, 5 kWh                             |
| 3     | `HJ3AYYYYYYYYYYYY` | battery, 5 kWh                             |

The prefixes are real and the meaningful half – `HC31` is the DC Fit, `HJ3A` the
battery modules; the rest is masked here because this repo is public.

**Established via the portal**, not guessed from the prefixes: `user-portal.ecoflow.com`
lists under *System information → Component information* the same serial numbers with type,
model, firmware version and activation date. Field 2 is the inverter there, the
`HJ3A` entries are the battery modules – in the example system two of 5 kWh each, plus a
5 kW converter. `ecoflow-frames.py --modules` adopts these labels.

Use: serial numbers, number and configuration of the battery without app and without portal.
The frame does **not** deliver firmware versions and activation date, though – those are
still only available in the portal.

This is evaluated by `scripts/ecoflow-frames.py`, which takes the output of `live` on
stdin. The faster ~3-second rate that the app unlocks via `.../set`
is thus not reached for `live` – that is what `fast` or `ecoflowd --fast` are for. For
a minute rate it is not needed anyway.

#### The remaining IDs: `96/1`, `96/108`–`96/111`, `96/136`

From a capture on 22 Sep 2026, 14:21–14:31Z (5 min `fast`, then 6 min just
listening). All six carry an XOR-obfuscated payload like the others. `96/1` comes about nine
times as often with the stream switch; `96/108`–`96/111` and `96/136`, on the other hand, run
**independently of the switch** at their own rate (1–2 frames per minute, measured over
264 covered minutes).

**`96/1` is the system report** and carries the **running daily totals as floats**. That
is the finding that can be established best – the same six flows as the
hourly history, measured in the same one-second window:

| Field | `96/1` (Wh) | Hourly history  | Difference | Flow            |
|-------|-------------|-----------------|------------|-----------------|
| 23    | 15960.50    | 15955           | +5.50      | PV              |
| 24    | 4812.19     | 4809            | +3.19      | battery in      |
| 25    | −1565.22    | 1562            | +3.22      | battery out     |
| 26    | −109.56     | 103             | +6.56      | grid import     |
| 27    | 5867.35     | 5861            | +6.35      | feed-in         |
| 28    | −6955.67    | 6948            | +7.67      | house           |

The hourly history counts in whole Wh per hourly bucket; over 15 buckets the rounding adds
up to the few Wh of difference. **The signs are those of the device side**, as in the
energy report: import and consumption negative.

Further in the same frame, less certain: field 3 = `10698.0` is the total capacity in Wh
(two modules, see `96/108`); fields 13–15 are the three grid voltages (234.7 / 234.3 /
235.3 V), fields 16–18 the currents (3.18 / 3.35 / 3.02 A). Field 12 (2217 W) is in the
order of magnitude of U·I over three phases (2242 W) and of the energy report's grid power
in the same second (2308 W), but matches neither of them – **which measuring point this is
remains open**. Fields 4 and 34 carry 99 and 100 respectively, so presumably the state of
charge in two resolutions.

**`96/108` is the per-battery-module report.** Each frame carries **two** nested
entries – just as many as `96/3` lists battery modules:

| Field | Example   | Interpretation                                     |
|-------|-----------|----------------------------------------------------|
| 2     | 99.90 %   | state of charge of the module                      |
| 4     | 53.51 V   | module voltage – matches `96/111` field 9          |
| 7     | 5345.52   | capacity in Wh; both entries together 10707.6, which fits `96/1` field 3 (10698) |

**`96/111` is the detailed report of one battery module.** Field 16 carries the
serial number as ASCII (`HJ3A…`), field 9 the module voltage (53.43 V), field 14 is
**16 cell voltages in mV** (3342–3344) – their sum, 53.495 V, gives back the module
voltage. Field 5 is nine temperatures (26–27 °C).

**`96/109` is the PV string report.** Two strings side by side: fields 2 and 3 are
voltage and current of one, fields 10 and 11 those of the other. Established via the same
cross-check as `96/1` – here over time instead of via a second source:

| PV per energy report   | `f2·f3 + f10·f11` | Ratio      |
|------------------------|-------------------|------------|
| 2526 W                 | 2476 W            | 0.980      |
| 2414 W                 | 2424 W            | 1.004      |
| 2266 W                 | 2294 W            | 1.012      |
| 839 W                  | 795 W             | 0.947      |
| 897 W                  | 875 W             | 0.976      |

**Mean 1.0004 with a spread of 0.033**, across a tripling of the power.
Fields 4, 5, 12 and 13 are **not** the same voltage, even if they seem to be during the
day. At night they separate clearly:

| | Field 2 (string) | Field 3 | Power | Field 4 | Field 12 |
|---|---|---|---|---|---|
| night, PV = 0 | 4.6–5.6 V | 0.08 A | 0.4 W | 397–426 V | 397–426 V |
| day, PV 578 W | 535.5 V | 0.51 A | 273 W | 534.4 V | 534.6 V |

The string drops to a few volts in the dark, fields 4/5/12/13 stay at around
400 V – that is the **DC link**, which the discharging battery holds up. During the day
both run together because the string then feeds the DC link. Fields 22–25 are
temperatures, fields 7 and 15 sit constantly at around 800 (like the limit in field 21).

The voltage behaves as expected: it **rises** when the power falls
(428 V at 2526 W, 556 V at 897 W) – in low light the operating point moves towards the
open-circuit voltage. This is an independent plausibility check for the interpretation as
string voltage.

**`96/110` falls into three groups.** A night with 57 samples at PV = 0 against 12 in
daylight separates them – what is zero in the dark depends on PV, what works then does not:

| Group | Fields | Finding |
|-------|--------|---------|
| PV-bound | 14, 16, 17, 20, 27, 29, 42, 43 | **exactly zero** in the dark, up to 3300 in daylight |
| Battery discharge | **45 and 47** | 174–354 in the dark, zero in daylight |
| Settings | 21, 23, 25, 26, 37, 38, 44, 46, 50, 53, 54 | constant over the whole capture |

**Field 45 is the battery's discharge power in W.** Paired against the energy report to within
5 seconds (n = 15, only during discharging): mean deviation **−9.3 W with a
spread of 14 W**, on values around 250 W. Field 47 carries the same quantity at a second
measuring point, on average 1.6 W below. When charging both are at zero – so the sign
is not in the value but in the choice of field.

This is less firmly established than `96/109` (there 3 % across a tripling); the spread
comes from the energy report arriving only every minute at night, while the house load
changes in between.

Unassigned remain, among others, fields 2, 3, 5 (voltages at the same
level as `96/109`), 18, 19, 24, 28, 32 and 48. Field 48 comes with a warning:

> In the afternoon capture the battery stood still (SoC 100 %). Then
> grid = PV − house with a nearly constant house, so PV and grid run proportionally.
> `f48/grid` ≈ 1.00 and `f48/PV` ≈ 0.82 were both constant – **not separable in this
> operating state.**
>
> In the following night field 48 then lay between −27 and +49, although PV rose to 1006 W.
> With a power of around 2000 W the day before, that is incompatible. **Field 48 is thus
> presumably not a power at all**; what it is remains open.

**What helps here** is therefore not a longer but a *differently placed*
capture: one in which PV, grid, house and battery move independently. Most
easily in the evening, when PV power falls, the house keeps drawing and the battery
takes over.

> **Correction.** It said here that `96/109` and `96/110` come *six times more often*
> without the stream switch. That was wrong and has been withdrawn. The figure arose
> because a still growing capture file was read twice at different times: the frame count
> came from the later read, the time span from the earlier one. Recalculated over 264
> covered minutes, the rate is **the same**: 2.03 frames per minute with the switch, 2.06
> without.

For the choice between `live` and `fast`, `live` nevertheless remains the tool of choice – not
because of the rate, but because it **sends nothing to the device**. Only for pairing with an
energy report to the second is a short `fast` phase useful.

**`96/136` is a constant.** The same two bytes across all samples: `08 0b`, i.e.
field 1 = 11. Not a reading.

### Context: the community's "Enhanced Mode"

Under this name the same channel runs in the Home Assistant integrations. It bypasses the
1006 block by logging in with the normal EcoFlow account credentials instead of
API keys. That is exactly what the Home Assistant integrations do for the
blocked models. **A community path without any commitment from EcoFlow**: it can break at
any time, and the account credentials sit in plain text in the configuration.
(Source: https://github.com/shuette42/ecoflow-energy-ha)

On the rate of "~2–4 s" named there: it only applies with an active stream switch. Without it
the device reports **every minute** — measured, see "Measured on the DC Fit".

This repo goes the whole way: `scripts/ecoflow-api.sh live` or `fast` fetches the
frames, `scripts/ecoflow-frames.py` unpacks them, and `cmd/ecoflowd` does both in one
service and passes the values on to a local MQTT broker.

## 4. Local Modbus TCP

- Not REST, but the classic Modbus TCP protocol on port 502
- Must be unlocked by the **EcoFlow installer/partner** via the EcoFlow **Pro app**
  – disabled by default (source: https://docs.evcc.io/en/meters/ecoflow-powerocean-modbus)
- No official register mapping from EcoFlow; existing mappings are
  community-reverse-engineered (see `modbus-registers.md`)
- Already in productive use in:
    - Home Assistant integrations (`MaxGrmm/EF-PowerOcean-TcpModbus`,
      `windmark/EF-PowerOcean-TcpModbus`, `harduser-gnk/EF-PowerOcean-TcpModbus` – fork
      with write access)
    - evcc (charging infrastructure software) via its own meter template
      `ecoflow-powerocean-modbus`

## 4a. Access to the EcoFlow Pro app

The **Pro app** (`com.ecoflow.pro`) is the installer app and is only enabled for authorised
distributors and installers; consumers use the normal EcoFlow app.

- **Access:** registration as an installer in the EcoFlow Pro web portal
  (https://pro-portal.ecoflow.com, EU instance https://portal.ecoflow.com/pro/eu),
  activation of the role by EcoFlow or the distributor. A registered but not yet activated
  account simply shows no systems.
- **Commissioning** in three steps (Internet Setup → Home Setting → System Setting);
  the device is captured via QR code/serial number and then bound to the owner
  account. Afterwards the Pro app shows SN, owner account and installation date.
  (Source: https://energy.ecoflow.com/eu/software/EcoFlow-Pro-App)

**Two separate bindings** – this is the most common confusion:

| Binding          | To what                                              | Needed for                    |
|------------------|------------------------------------------------------|-------------------------------|
| System binding   | account of the installer who commissioned it         | Pro app, Modbus unlocking     |
| Owner binding    | the owner's EcoFlow account (consumer app)           | cloud API, normal app         |

A Pro account of your own therefore does **not** show the system automatically: it is tied
to the installer's account. Trying to add it yourself ends with "system already added in
another account" (experience reports in the Photovoltaikforum thread 218848, page 17).
This can only be resolved via the original installer (deleting the system or sharing it
under *Installer/user management → Add user*) or via a support ticket with EcoFlow
(`solutionservice.eu@ecoflow.com`) with SN and proof of purchase.

**Important:** No transfer is needed for the Modbus unlocking – it is enough that *any*
Pro access flips the switch once.

## 4b. Checklist for the installer appointment

The Modbus unlocking can only be done by an installer with Pro app access (see 4a). Such
an appointment does not come around again quickly – hence, to tick off here, what needs to
be clarified during it.

**Pre-test: is anything still missing at all**

A `modbusread <ip> 42082 uint16` before the appointment answers this in a second, and
the error message distinguishes the cases:

| Response             | Meaning                                                                |
|----------------------|------------------------------------------------------------------------|
| `connection refused` | device reachable, nothing listening on port 502 → Modbus is disabled   |
| Timeout              | network/VLAN/firewall problem, not the Modbus switch                   |
| Register value       | already unlocked                                                       |

On the test device (DC Fit, firmware **1.0.6.20**, September 2026): ping answered,
port 502 `connection refused` – so disabled, as documented.

**The actual request**

- [ ] **Enable Modbus TCP on the inverter** – specifically: select the inverter in the Pro
  app and set the **Control Mode to "Modbus control"**. That is how the reference
  integration describes it; please have it confirmed whether the menu is actually
  called that.
- [ ] Does this mode change anything about the system's internal scheduling? (The same
  question is in "Open questions" at the end of the file – answer it there, only ask it
  here at the appointment)
- [ ] Does the setting survive a firmware update?
- [ ] **Read registers 40002 and 40003** (`product_category`, `product_number`)
  as soon as Modbus is running: the reference integration can*not* recognise the DC Fit
  by them because nobody knows the values – a contribution that is missing upstream.

**So that you can get at it afterwards**

- [ ] IP address of the inverter. Assigned via DHCP? Then fix it in the router,
  otherwise every poll will eventually point into the void.
- [ ] Have the port (expected 502) and unit/slave ID (expected 1) confirmed.
- [ ] Is access restricted to a particular network/VLAN?

**For the register question (Plus vs. DC Fit)**

- [ ] Ask for the firmware version and exact model designation/`InverterModel`. The mapping
  in `modbus-registers.md` was determined on the PowerOcean **Plus**, and the firmware
  knows model-dependent `address_overrides`.
- [ ] Has EcoFlow documented a register list to him? Unlikely, but
  the cheapest way to official information.

**Because of a system extension at the same appointment**

- [ ] What exactly is being added (battery module, PV string, PowerPulse/wallbox)? New
  components can occupy additional registers.
- [ ] Does this change the SN, system ID or binding? Then the findings in this
  document need to be updated.

**Independent of the appointment**

- [ ] Ask EcoFlow support (`solutionservice.eu@ecoflow.com`) whether our own SN can be
  enabled for the Developer API. Not very promising for the PowerOcean family, but
  the only lever left on the cloud side.

## 5. What other integrations can (and cannot) do

- **OpenHAB binding `org.openhab.binding.ecoflow`:** purely cloud-based via the
  Developer API and supports only Delta 2, Delta 2 Max and PowerStream – **no
  PowerOcean**. So no path for the DC Fit, neither locally nor on the cloud side.
  There is still a useful hint in its README: a developer account can*not*
  be used several times in parallel; that disrupts the event updates. Whoever runs
  `scripts/ecoflow-api.sh` alongside another integration should
  know this.
- **evcc** uses exclusively the **local Modbus path** for the PowerOcean
  (meter template `ecoflow-powerocean-modbus`) – a further indication that the
  *documented* cloud path is not practicable for this device family. About the
  app MQTT channel from section 3 this says nothing: it works, but is unofficial and
  reverse-engineered, so nothing an integration would build on. The
  register addresses used there confirm the current map in `modbus-registers.md`.

## 6. "Open API" in shop descriptions

Sales pages for the DC Fit set advertise an "open API interface"
for connecting to EMS such as Solar Manager Connect 2 or Loxone. Presumably
this means the same local Modbus interface, not a separate
REST interface – but there is no explicit confirmation of this.

## Open questions / still to be clarified

- [ ] Does the Modbus register mapping (PowerOcean Plus) apply 1:1 to the DC Fit, or
  is there a separate `InverterModel` mapping with different addresses?
  → `models.py` in the repo `MaxGrmm/EF-PowerOcean-TcpModbus` not yet checked.
- [ ] Exact menu path to the Modbus switch in the EcoFlow Pro app. Known from a
  third-party source (`MaxGrmm/EF-PowerOcean-TcpModbus`): select the inverter, switch
  Control Mode to **"Modbus control"** – so a change of operating mode, not a hidden
  switch. **Unconfirmed on the device**, hence no tick; listed as a task in 4b
- [ ] Does the "Modbus control" mode affect the internal scheduling? With purely
  reading access presumably without consequence, but that is not established
- [ ] What does field 2 of the energy report (`dcdc`) measure? The name comes from
  `foxthefox/ioBroker.ecoflow-mqtt` (`dcdc_pwr`). Measured against the captures
  (26 Sep 2026): not part of the energy balance, same sign as the battery, 63–103 %
  of its value without a fixed ratio. Until that is clear, `ecoflowd` does not publish it
  (`mqtt-output.md`, §4)
- [x] Does a missing power field mean "0" or "unknown"? → **"0".** `grid` is missing in
  100 of 191 energy reports, an explicit `0.0` appears in none, and without `grid` the
  balance adds up exactly – the proto3 behaviour of omitting fields with the default value
  (26 Sep 2026, `mqtt-output.md`, §4). It remains open whether for this reason at night,
  at PV = 0, no reports at all get through the decoders
- [x] Does the prefix `HC31` (DC Fit) return error 1006? → **Yes, for `quota/all`**;
  `device/list`, by contrast, lists the device normally (September 2026)
- [x] Do messages arrive on the MQTT topic `/open/<acct>/<SN>/quota`? → **No**,
  subscription is granted, connection stays up, nothing is published (short observation,
  September 2026)
- [x] Does the documented request path via `.../get` help? → **No**, the publish is
  rejected with PUBACK 0x87 "Not authorized", the subscription to `.../get_reply` with
  SUBACK 0x80. This fully measures out the **Open API** MQTT channel. The
  app channel is a different one and still open – see the next three points.
- [x] Does `/iot-auth/app/certification` answer with the consumer token, and does the
  broker allow the client ID form `ANDROID_<hex>_<userId>`? → **Yes, both** (22 Sep 2026);
  frames arrive on `/app/device/property/<SN>`
- [x] Does `.../thing/property/get_reply` deliver JSON on the DC Fit? → **Nothing came at
  all**; the push runs anyway. The values are exclusively in the protobuf frames
- [x] Does `measured` in the REST endpoint become fresh again while `live` is running? →
  **No.** Unchanged over two and a half minutes, while the frames were an hour further on.
  The REST path is thus done with as a live source
- [x] Does a more frequent wake-up call change the reporting rate? → **No.** With
  `ECOFLOW_LIVE_INTERVAL=5` the energy stream still came exactly every minute. The rate
  belongs to the device, not to the one asking
- [x] Is the wake-up call needed at all, or is the subscription alone enough? → **The
  subscription is enough.** 23 minutes without a single publish, minute values throughout.
  See section 3
- [x] What does the command for the fast rate really look like? → **Captured** on the
  `set` topic while the app was running (22 Sep 2026). Bytes and field assignment see above;
  implemented as the command `fast`
- [x] Does the fast rate hold up? → **Yes, with a 3 s repeat**; at 10 s the
  device falls back to the minute rate
- [x] How long does the switch keep working afterwards? → **About 25 seconds**, cleanly
  measured: on 22 Sep 2026 the last switch ended at 14:25:57Z, after that came exactly six
  more `96/33` at a **4-second rate** (14:26:01 to 14:26:21Z), then nothing more over five
  further minutes of listening. The rate is remarkable: while the switch is running, the
  reports come at 1–3-second intervals, in the run-on evenly every 4 s. **The earlier
  figure of "about four minutes" is thus not confirmed.** One difference between the two
  runs is known and could be the explanation: here the MQTT client was **reconnected**
  between the two phases (the broker demands a fresh client ID anyway). Whether the end of
  the stream depended on time or on the disconnect, this measurement does **not** separate
  – for that the same connection would have to stay up and only the switching stop
- [x] Does the device ever switch the fast stream on by itself? → **No.** The control
  with the app and the portal closed showed **not a single** `96/33` over 23 minutes.
  In an earlier run it had appeared without our doing anything; the cause was
  therefore **presumably** another client – only the negative control is established
- [x] What does `cmd_func 254 / cmd_id 32` carry? → **The hourly history of the current day**,
  six flows of 24 hourly values each in Wh. Broken down in section 3, fetchable with
  `ecoflow-frames.py --hours`
- [x] What do `96/3` and `96/137` carry? → `96/3` is a **component list** with four
  serial numbers, `96/137` has an **empty payload** and is the acknowledgement of the
  stream switch. The switch speeds both up about seventeenfold, but does not
  switch them on. See section 3
- [x] What role do the components from field 2 of the list onwards have? → Resolved via
  *System information → Component information* in the portal: field 2 is the
  PV Storage Converter, the `HJ3A` entries are the battery modules
- [x] Why does the daily total of the hourly history differ from the portal's
  `todayElectricityGeneration`? → **It does not differ. It is a question of timing.**

  After sunset on 22 Sep 2026, when the daily value was settled, three
  independent sources agreed:

  | Source                   | Daily yield  |
  |--------------------------|--------------|
  | Portal `yield today`     | 17140 Wh     |
  | Hourly history `254/32`  | 17143 Wh     |
  | `96/1` field 23          | 17149.90 Wh  |

  Spread 9.9 Wh = **0.058 %** – and the portal rounds to 0.01 kWh, i.e. to 10 Wh.
  The spread is thus a rounding digit. The two timestamps were four seconds
  apart (portal `20:13:05Z`, capture from `20:13:09Z`), so the portal was
  **not** frozen. As a check, the daily balance from `96/1` closes to within
  0.04 Wh: PV + battery out + grid import = house + battery in + feed-in.

  During the day, by contrast, they diverged, and **in both directions** –
  in the morning the portal read too high (1.74 against 1.26 kWh), in the afternoon too low
  (14.00 against 15.96 kWh). So the portal measures the same thing, but hands it out
  **in jumps and at its own rate**.

  **What is still open about it** (hence only almost done): pure lag does not explain
  the too-high reading in the morning. A suspicion, not established: the timestamp
  `measured` belongs to the power value; when the energy counter was last updated
  it possibly does not say at all. **In practice this means: whoever wants daily values
  takes those from the device** (`254/32` or `96/1`), not those from the portal
- [x] What do the remaining `cmd_id` (1, 108, 109, 110, 111, 136) carry? → **Broken
  down**, see section 3. In short: `96/1` is the system report and carries the daily
  totals as floats (established against the hourly history to within a few Wh), `96/108`
  the per-battery-module report, `96/111` one module in detail including serial number and
  16 cell voltages, `96/136` a constant, `96/109` the PV string report (two strings,
  voltage times current gives the PV power to within 3 %). Only `96/110` remains open

## Sources

- https://github.com/Feberdin/ecoflow-powerocean-ha
- https://github.com/MaxGrmm/ecoflow-poweroceanplus-modbus
- https://github.com/MaxGrmm/EF-PowerOcean-TcpModbus
- https://github.com/windmark/EF-PowerOcean-TcpModbus
- https://github.com/harduser-gnk/EF-PowerOcean-TcpModbus
- https://docs.evcc.io/en/meters/ecoflow-powerocean-modbus
- https://www.photovoltaikforum.com/thread/247994-ecoflow-powerocean-modbus-protokoll/
- https://www.photovoltaikforum.com/thread/218848-erfahrungen-mit-system-ecoflow-powerocean/?pageNo=17
- https://github.com/shuette42/ecoflow-energy-ha (Enhanced Mode: AES certification,
  client ID construction, keepalive intervals)
- https://github.com/tolwi/hassio-ecoflow-cloud (`app/certification`, client ID form)
- https://github.com/jensfr1/ha-ecoflow-ocean2 (wake-up interval with justification)
- https://github.com/foxthefox/ioBroker.ecoflow-mqtt (device type `poweroceanfit`:
  different protobuf field numbers for the DC Fit)
- https://github.com/evcc-io/evcc – `templates/definition/meter/ecoflow-powerocean-modbus.yaml`
- https://github.com/openhab/openhab-addons – `bundles/org.openhab.binding.ecoflow`
- https://developer.ecoflow.com/us/document/introduction
- https://developer-eu.ecoflow.com
- https://developer-eu.ecoflow.com/us/document/PP2 (official PowerOcean docs:
  endpoints, field names, MQTT topics)
- https://developer-eu.ecoflow.com/us/document/root (signature method incl.
  test vector, flattening rules, generic endpoints, MQTT topics)
- https://energy.ecoflow.com/eu/software/EcoFlow-Pro-App
- https://pro-portal.ecoflow.com
- https://energy.ecoflow.com/eu/support
