# Research notes: EcoFlow PowerOcean DC Fit

How [`ecoflowd`](../../README.md) came about: which ways into the PowerOcean DC Fit exist,
which of them deliver readings, and what the frames and registers mean. `ecoflowd` rests
on what is written here; the notes are kept as the record of how it was established and
as the starting point for the open questions below.

> **Disclaimer:** These notes are mostly based on community reverse engineering, not on
> official EcoFlow documentation. EcoFlow does not officially support or confirm the
> Modbus registers described here. Use at your own risk, especially when writing to
> registers.

## Contents

| File | Description |
|---|---|
| [`api-status.md`](api-status.md) | Overview: cloud REST API vs. app MQTT channel vs. local Modbus TCP, known problems (e.g. error 1006), unlocking, the write tests |
| [`modbus-registers.md`](modbus-registers.md) | Register map (SOC, battery, PV, grid, energy counters, control registers) with decoding examples |
| [`tools.md`](tools.md) | The measuring tools: [`scripts/ecoflow-api.sh`](../../scripts/ecoflow-api.sh) for all four cloud paths, [`scripts/ecoflow-frames.py`](../../scripts/ecoflow-frames.py) for the live frames |
| [`../mqtt-output.md`](../mqtt-output.md) | Why `ecoflowd` publishes two JSON telegrams, and the measurements behind the format |

## `modbusread`

The registers in `modbus-registers.md` are checked on the device with
[`modbusread`](https://github.com/womat/modbusread), a universal, read-only Modbus TCP/RTU
reader – address, register and type in, value out, raw words always shown, addresses
never converted. It knows nothing about EcoFlow on purpose, and therefore has its own
repo:

```
go install github.com/womat/modbusread@latest
```

Up to v0.6.0 it lived here as `cmd/modbusread`; those releases and
`go install github.com/womat/ecoflow/cmd/modbusread@v0.6.0` keep working, newer versions
come from [womat/modbusread](https://github.com/womat/modbusread/releases).

## Summary

- A specific, officially documented REST API for the DC Fit does not exist.
- Four paths were examined; **exactly one delivers readings continuously today**, and that
  is the unofficial MQTT channel of the consumer app.
- The generic EcoFlow Developer/Open API (cloud) returns error 1006 "not allowed" for the
  PowerOcean family – a model blocklist, not a bug. The **DC Fit (SN prefix `HC31`) is
  affected, confirmed on the device**: `device/list` does list it with code 0, but
  `device/quota/all` refuses the readings with 1006.
- The **consumer portal** (REST, session token) answers, but only hands out the state last
  pushed to the cloud – in one measurement it stood still for over an hour.
- The **app's MQTT channel** (protobuf, reverse-engineered) delivers minute values on its
  own and per-second values with the stream switch. `scripts/ecoflow-api.sh` and the
  service `cmd/ecoflowd` run on it.
- **Local Modbus TCP** (port 502) would be the stable path, but is still locked on the
  device (`connection refused`): it has to be unlocked by the installer via the EcoFlow
  Pro app, and the register layout is not officially documented but community-derived.
- For details see the linked files, and the comparison in `api-status.md`.

## Open points

- Confirmation of the register mapping on the DC Fit. The current source treats it as the
  normal case and knows only *one* model-dependent special case, and that one concerns the
  Plus
- Which of the two register readings is right: `modbus-registers.md` puts the
  contradictory addresses of both sources side by side, every row is a single test on the
  device (e.g. system SOC at 40527 vs. 42082)
- Confirmation on the device that unlocking really goes through *Control Mode →
  "Modbus control"* in the Pro app (checklist in `api-status.md`)
- Which values `product_category`/`product_number` (40002/40003) return on the DC Fit –
  the reference integration does not know them
- Whether the fast stream ends after the last switch for reasons of time or because the
  MQTT connection dropped. **About 25 seconds** of run-on were measured; but the same
  measurement also reconnected, so it does not separate the two
- The remaining fields of `cmd_id` 110. A night with PV = 0 split them into three groups
  (PV-bound, battery discharge, settings) and established fields 45/47 as discharge power;
  still unclear are, among others, 2, 3, 5, 18, 19, 24, 28, 32 and 48. `cmd_id` 1, 108,
  109, 111 and 136 are decoded
- What `dcdc` (field 2 of the energy report) measures. The name comes from a third-party
  source (`dcdc_pwr`); it is **not** part of the energy balance and follows the battery
  with the same sign, in the captures at 63–103 % of its value, without a fixed ratio.
  Until that is clear, `ecoflowd` does not publish it
- Scheduled tasks: how the app enables, disables, changes, creates and deletes them is
  captured (`96/125`; the task list comes as `96/127` and is pushed as `96/10`, see
  `api-status.md`, section 3). Write tests with replayed app frames showed that the device
  also accepts enable, disable, create and delete from outside the app, and that it stores
  overlapping tasks, which only the app refuses. Switched that way, tasks **take effect**:
  an app task enabled from the Mac and a task created from the Mac on the 30-minute grid both
  blocked the discharge. A task with off-grid times (00:51–01:02) was stored but not
  executed. Decided on 3 Oct 2026: `ecoflowd --block` switches one app task on and off
  ([Discharge block](../../README.md#discharge-block---block)), tested on the device the same evening (sixth
  test in `api-status.md`). Still open: what the device does at midnight with a 00:00–24:00
  task; a raw `96/10` for the test data; the natural end of a task; and whether off-grid
  minutes are really why the fourth test's task did not run
- Why the portal's daily yield is off in both directions **during the day**. After sunset
  portal and device agree to within 0.058 %, so they measure the same thing; the portal
  just updates in jumps. In practice: take daily values from the device, not from the
  portal


## Sources

The source lists for the individual findings are at the end of each file; these are the
main ones.

- https://developer.ecoflow.com
- https://github.com/Feberdin/ecoflow-powerocean-ha
- https://github.com/MaxGrmm/ecoflow-poweroceanplus-modbus
- https://github.com/MaxGrmm/EF-PowerOcean-TcpModbus
- https://github.com/windmark/EF-PowerOcean-TcpModbus
- https://docs.evcc.io/en/meters/ecoflow-powerocean-modbus
- https://github.com/shuette42/ecoflow-energy-ha
- https://www.photovoltaikforum.com/thread/247994-ecoflow-powerocean-modbus-protokoll/
