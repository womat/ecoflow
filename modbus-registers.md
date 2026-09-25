# Modbus registers – EcoFlow PowerOcean (DC Fit / Plus)

> **Status:** Community reverse-engineered, not officially documented or supported by
> EcoFlow. Two sources with **differing addresses** are available (see
> "Contradictions between the sources"); nothing has been measured on our own device yet.

**Test device for these notes:** PowerOcean **DC Fit**, firmware **1.0.6.20**.

**On the model question:** The current source treats the **DC Fit as the normal case** –
in the entire register catalogue there is exactly *one* model-dependent special case, and
that one concerns the **Plus** (`feed_in_power_max`, see below). The earlier worry in
these notes that the mapping was "determined on the Plus and questionable for the DC Fit"
had the direction reversed. Nothing is confirmed by this, though – only the suspicion has
turned around.

## Sources

- https://github.com/MaxGrmm/EF-PowerOcean-TcpModbus – Home Assistant integration.
  Register map as code in `custom_components/ef_powerocean_tcpmodbus/const.py`
  (`MODBUS_REGISTERS`), model catalogue in `models.py`, protocol notes in
  `EcoFlow_PowerOcean_Modbus.md`. **The more recent source, cross-checked against the
  EcoFlow portal** – the more reliable one in case of conflict.
- https://github.com/MaxGrmm/ecoflow-poweroceanplus-modbus – older register tables,
  determined by scanning 40001–44096 on a PowerOcean **Plus**.
- `evcc-io/evcc`, `templates/definition/meter/ecoflow-powerocean-modbus.yaml` – meter
  template of the charging software evcc. **Independent third source**: written outside
  the two EcoFlow repos and confirms their current addresses (see "Confirmation by
  evcc"). Plus https://docs.evcc.io/en/meters/ecoflow-powerocean-modbus for the unlocking
  note and Modbus parameters.

Both GitHub repos are under the MIT license ("free to use, modify, and distribute with
attribution").

## Unlocking

Has to be activated by the **EcoFlow installer/partner** via the EcoFlow **Pro app** (the
installer app, not the normal consumer app) – disabled by default.

The path according to `EcoFlow_PowerOcean_Modbus.md`:

> Open the Pro app → **select the inverter** → switch Control Mode to **"Modbus
> control"**.

So it is not a hidden switch but a **change of operating mode**. It is not confirmed how
this mode affects the internal scheduling – with purely reading access it should have no
consequences, but that is not proven.

Access to the Pro app itself (installer role, system vs. owner binding, unbinding from
someone else's account) is covered in [`api-status.md`](./api-status.md), section 4a
"Access to the EcoFlow Pro app"; the questions for the appointment in section 4b.
Unlocking does not require transferring the system – it is enough for a Pro account to
switch the mode.

## Connection

| Parameter | Value |
|---|---|
| Protocol | Modbus TCP |
| Port | 502 |
| Unit/slave ID | 1 (the device answers to practically any ID) |
| Function codes | `0x03` read; `0x06`/`0x10` write |
| Addressing | direct 4xxxx addresses, **literally on the wire** |
| Max. registers per read | 125 (Modbus limit of one response) |

**On addressing:** The reference integration passes the 4xxxx numbers unchanged to
pymodbus, so they go on the wire exactly like that. `modbusread` does the same (see
[`README.md`](./README.md): addresses are never converted) – the tables below can thus be
**used 1:1**. The earlier claim "register numbering 1-based" in these notes is thereby put
in serious doubt, but can only be settled for good on the device.

## Data types and word order

| Type | Registers | Remark |
|---|---|---|
| `UINT16` | 1 | percentages, counters, fault codes, enums |
| `UINT32` | 2 | capacities and power limits, `(high << 16) \| low` |
| `FLOAT32` | 2 | IEEE 754, **word-swapped** – power, voltage, current, energy |
| `SERIAL` | 8 | 16 ASCII bytes, high byte first in each word |

**Reading and writing use different word orders** – according to the source, EcoFlow's
own documentation contradicts itself here; it was measured on the Plus:

| Direction | Word order |
|---|---|
| Read (`0x03`) | **low word first** (register *N* = lower half) |
| Write (`0x10`) | **high word first** |

For `modbusread` this means, when reading: `--word-order low`.

The trap when writing: if a 32-bit value is written in *read* order, the device does not
reject it – it stores the words and interprets them high-word-first, so it acts on a value
65536 times too large. A write can therefore look "successful" while something entirely
different happens.

## Register map (current source: `const.py`)

The integration reads in four blocks:

| Block | Start | Words | Content |
|---|---|---|---|
| Device info | 40002 | 12 | product type, serial number, firmware |
| Live | 40519 | 89 | power, limits, voltages, currents, PV |
| Faults | 42049 | 45 | fault counters and codes, SOC per battery |
| Energy | 42161 | 100 | lifetime and daily counters |

### Device identity

| Register | Type | Description |
|---|---|---|
| 40002 | UINT16 | `product_category` |
| 40003 | UINT16 | `product_number` |
| 40004 | SERIAL (8) | serial number, 16 ASCII bytes |
| 40012 | UINT32 | firmware version |

**Open and resolvable on our own device:** `models.py` **cannot** recognise the DC Fit
from these registers – only `product_number` 1 (single/three-phase, depending on
`product_category`), 2 (single-phase) and 3 (Plus) are known; the code says explicitly
"We are not sure of the product number for the remaining models." Whoever reads
40002/40003 on a DC Fit closes this gap – upstream too.

### Live values

| Register | Type | Description |
|---|---|---|
| 40519 | FLOAT32 | `house_power` – house consumption |
| 40521 | FLOAT32 | `grid_power` – grid power |
| 40523 | FLOAT32 | `solar_power` – PV power |
| 40525 | FLOAT32 | `battery_power` – battery power |
| 40527 | UINT16 | `battery_soc` – **system SOC in %** |
| 40528 | UINT32 | `inverter_rated_power` |
| 40574 | FLOAT32 | `battery_voltage` |
| 40576 | FLOAT32 | `battery_current` |
| 40578 | FLOAT32 | `battery_temperature` |
| 40580 / 40582 / 40584 | FLOAT32 | voltage L1 / L2 / L3 |
| 40586 / 40588 / 40590 | FLOAT32 | current L1 / L2 / L3 |
| 40592 | FLOAT32 | `inverter_temperature` |
| 40594 | FLOAT32 | `frequency` – grid frequency |
| 40596 / 40598 / 40600 | FLOAT32 | PV string 1 / 2 / 3 voltage |
| 40602 / 40604 / 40606 | FLOAT32 | PV string 1 / 2 / 3 current |

The float values carry physical units directly; the scaling factors (×10, ×100, ×1000) of
the older source do not apply here – see "Contradictions between the sources".

### Faults and batteries

| Register | Type | Description |
|---|---|---|
| 42049 | UINT16 | `fault_count` |
| 42050 ff. | UINT16 | `fault_1` … `fault_n` |
| 42081 | UINT16 | `battery_count` – **number of batteries** |
| 42082 ff. | UINT16 | SOC per battery (`42081 + n`) |

### Energy counters

| Register | Type | Description |
|---|---|---|
| 42161 / 42163 | FLOAT32 | grid import total / today |
| 42177 / 42179 | FLOAT32 | grid export total / today |
| 42225 / 42227 | FLOAT32 | battery charged total / today |
| 42241 / 42243 | FLOAT32 | battery discharged total / today |
| 42257 / 42259 | FLOAT32 | solar yield total / today |

## Confirmation by evcc

The evcc template was written independently of the two EcoFlow repos and uses exactly the
addresses of the current map – including `float32s`, evcc's name for a **word-swapped**
float, as well as port 502 and unit ID 1:

| Register | evcc | Matches |
|---|---|---|
| 40521 | Grid power, W | `grid_power` |
| 40523 | Solar power, W | `solar_power` |
| 40525 | Battery power, W | `battery_power` |
| 40527 | Battery state of charge, % (`uint16`) | `battery_soc` |
| 42161 | Grid import total, kWh | `grid_import_total` |
| 42241 | Battery discharged total, kWh | `bat_discharged_total` |
| 42257 | Solar yield total, kWh | `solar_total` |

This means **40527 as system SOC is backed by two independent sources** – the older
reading "possibly max SOC limit" has thereby become unlikely (it still has not been
measured, though).

**A new contradiction about the sign:** evcc comments 40525 with "positive when
discharging, negative when charging" and flips the raw value with `scale: -1`. The older
table in these notes claimed the opposite (positive = charging). To be decided on the
device: read 40525 once during a known charging phase.

## Contradictions between the sources (= the measurement plan)

The older table and the current integration partly interpret the same addresses
differently. Both sources agree on **40580–40591** (phase voltages and currents) and
**40602–40607** (PV string currents). The differences:

| Address | Older source | Current source |
|---|---|---|
| 40527 | "possibly max SOC limit", value 100 | `battery_soc` (system SOC in %) |
| 40574 | total PV power (×100) | `battery_voltage` |
| 40576 | battery power (×1000) | `battery_current` |
| 40592 | grid frequency | `inverter_temperature` |
| 40594 | – | `frequency` |
| 40596 | active power (×10) | PV string 1 voltage |
| 40600 | inverter temperature | PV string 3 voltage |
| 42081 | system status (1 = online) | `battery_count` |
| 42082 | battery SOC | SOC of the **first** battery |

Two observations on this, both **unconfirmed**:

- At 42081/42082 both readings may have felt like a confirmation: a system with *one*
  battery returns a 1 there – as plausible for "online" as for "battery_count".
- The scaling factors of the older source (×10, ×100, ×1000) look like corrections for
  wrongly assigned addresses. If the current map is right, they are not needed.

**To be decided on the device** as soon as Modbus is unlocked – every row is a single
test:

```
modbusread <ip> 40527 uint16                       # 100 → rather a limit; 0..100 plausible → SOC
modbusread <ip> 40574 float32 --word-order low     # voltage (~400 V) or power?
modbusread <ip> 42081 uint16                       # number of batteries or online flag?
modbusread <ip> 40525 float32 --word-order low     # sign during a known charging phase
modbusread <ip> 40519 raw --count 100 --out hex    # look at the live block in one piece
```

The official cloud field names in [`api-status.md`](./api-status.md) (`bpSoc`, `bpPwr`,
`mpptPwr`, `sysLoadPwr`, `sysGridPwr`) serve as the cross-check for the **magnitude**: a
register whose value does not fit the official semantics is misinterpreted.

**For the signs it is no use.** On the DC Fit, `sysGridPwr` behaves opposite to what the
docs say — positive there means **export**, measured via the energy balance
`PV = battery + house + grid`. Whoever takes the official convention as the yardstick
without checking discards a correct interpretation. What is measured on the cloud channel:
battery positive = charging, grid positive = export, house is reported negative. Whether
the Modbus registers follow the same direction is **not** established by this — but it is
the cross-check that is ready to hand.

## Control/configuration registers

### Explicitly implemented as writable (Home Assistant integration)
| Register | Description | Range |
|---|---|---|
| 40536 | `min_soc_limit` (lower discharge limit) | 0–100 %, step 1 |
| 40541 | `device_led_brightness` | 0–100 %, step 10 |

### Known, but not (yet) exposed as writable
| Register | Description |
|---|---|
| 40530 | `system_modes` (UINT32, presumably a bitmask) |
| 40609 | `feed_in_power_max` – **the only model-dependent case**: on the PowerOcean **Plus** 40538 instead |
| 40546 | `limit_inv_power` |
| 40548 | `limit_inv_max` |
| 40552 | `battery_capacity` |
| 40554 | `battery_discharge_power_limit` |
| 40556 | `battery_charge_power_limit` |

### Modes/enums (currently implemented read-only)
- `grid_mode`, `operating_mode`
- `self_use_mode_ena`, `intelligent_mode_ena`, `battery_saver_mode_ena` (binary flags)

### Unmapped configuration registers
The block 40519–40607 is read in one piece but not fully interpreted: below 40574 there
are more registers than the integration **decodes**; some of them are **signed** 32-bit
values for which there is no decoder there. `modbusread` can read them with `int32`.

## Decoding snippet (pymodbus)

```python
import struct
from pymodbus.client import ModbusTcpClient

client = ModbusTcpClient("192.168.x.x", port=502, timeout=3)
client.connect()

def read_uint16(addr):
    r = client.read_holding_registers(addr, count=1, slave=1)
    return r.registers[0] if not r.isError() else None

def read_float(addr):
    r = client.read_holding_registers(addr, count=2, slave=1)
    if r.isError():
        return None
    # read order: registers[0] = low word, registers[1] = high word
    raw = struct.pack('>HH', r.registers[1], r.registers[0])
    return round(struct.unpack('>f', raw)[0], 3)

def read_serial(addr=40004, count=8):
    r = client.read_holding_registers(addr, count=count, slave=1)
    chars = []
    for val in r.registers:
        chars.append(chr((val >> 8) & 0xFF))
        chars.append(chr(val & 0xFF))
    return ''.join(c for c in chars if 32 <= ord(c) <= 126)
```

## Known gaps – what Modbus does not deliver

The cloud knows considerably more than Modbus. Affected are, among others, cell voltages,
temperatures per pack, state of health and cycle count, DC bus and insulation values, the
AFCI self-test, **per-phase active/reactive/apparent power** (Modbus only has voltage and
current), about 180 grid protection parameters, schedules/peak shaving/VPP, detailed fault
lists, and monthly and yearly energies.

For this device the **official** cloud API is blocked (see `api-status.md`). These values
remain reachable via two unofficial paths, both scripted by now rather than read off the
browser:

- **Consumer portal**, `provider-service/user/device/detail?sn=<SN>` —
  `scripts/ecoflow-api.sh portal|status`. **Not a live source:** the endpoint hands out the
  state last pushed to the cloud, which in one measurement stood still for over an hour.
- **App MQTT channel** — `scripts/ecoflow-api.sh live|fast`, unpacked by
  `scripts/ecoflow-frames.py` or by `cmd/ecoflowd`. Delivers readings continuously, plus
  the day's hourly balance and the serial numbers of the installed components.

Both are unofficial and can change at any time.

## Caution with write access

`modbusread` never writes – there is no code path in this program that issues a Modbus
write command. (The repo's second binary, `ecoflowd`, has a write path, but to the cloud
and only behind the `--fast` flag; it does not touch Modbus.) If you write by other means:

- The mapping is reverse-engineered and not confirmed by EcoFlow; firmware updates can
  change addresses and behaviour.
- **Reading back proves nothing.** Immediately after a 32-bit write the words stand as
  they were sent; only seconds later does the firmware turn them into read order. Both
  only prove delivery, not effect. The only reliable evidence is behaviour: power that
  moves, the LED, or the Pro app.
- Before productive access to the power limit registers (40554/40556), test with
  non-critical registers first (min SOC, LED) and watch the device's behaviour.
- Writes can disturb the internal scheduling – only ever change one register at a time.
