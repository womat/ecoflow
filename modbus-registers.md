# Modbus-Register – EcoFlow PowerOcean (DC Fit / Plus)

> **Status:** Community-reverse-engineert, von EcoFlow nicht offiziell dokumentiert oder unterstützt.
> Getestet wurde dieses Mapping primär auf dem **PowerOcean Plus**. Für den **DC Fit** liegt
> keine explizite Bestätigung vor – die Registeradressen können abweichen, da die
> Firmware laut Quellcode zwischen `InverterModel`-Varianten unterscheidet
> (`address_overrides`).

## Quellen

- https://github.com/MaxGrmm/ecoflow-poweroceanplus-modbus (README, Register-Tabellen, Decoding-Beispiele)
- https://github.com/MaxGrmm/EF-PowerOcean-TcpModbus (Home-Assistant-Integration, `const.py`)
- https://docs.evcc.io/en/meters/ecoflow-powerocean-modbus (Freischaltungshinweis, Modbus-Parameter)

Beide GitHub-Repos stehen unter MIT-Lizenz ("free to use, modify, and distribute with attribution").

## Verbindung

| Parameter | Wert |
|---|---|
| Protokoll | Modbus TCP |
| Port | 502 |
| Slave-ID | 1 (Gerät antwortet auf IDs 1–250) |
| Float-Encoding | 32-bit IEEE754, **word-swapped** |
| Register-Nummerierung | 1-based |

## Freischaltung

Muss vom **EcoFlow-Installateur/-Partner** über die EcoFlow **Pro App** (Installateur-App,
nicht die normale Endkunden-App) aktiviert werden – standardmäßig deaktiviert.
Genauer Menüpfad nicht öffentlich dokumentiert; vermutlich im Bereich
„Inbetriebnahme → Optionale Einrichtung“ o.ä.

## Status & Batterie-SOC

| Register | Typ | Beschreibung |
|---|---|---|
| 42081 | UINT16 | Systemstatus (1 = Online, 0 = Offline) |
| 42082 | UINT16 | Batterie-SOC in % |

## Live-Messwerte (Floats, 2 Register, word-swapped)

### Solar
| Register | Einheit | Scale | Beschreibung |
|---|---|---|---|
| 40574/40575 | W | ×100 | PV-Gesamtleistung |
| 40602/40603 | A | ×1 | PV-Strang 1 Strom |
| 40604/40605 | A | ×1 | PV-Strang 2 Strom |
| 40606/40607 | A | ×1 | PV-Strang 3 Strom |

### Batterie
| Register | Einheit | Scale | Beschreibung |
|---|---|---|---|
| 40576/40577 | W | ×1000 | Batterieleistung (positiv = laden, negativ = entladen) |
| 40578/40579 | °C | ×1 | Batterietemperatur |
| 42227/42228 | kWh | ×1 | Verbleibende Batterieenergie |

### AC-Netz
| Register | Einheit | Scale | Beschreibung |
|---|---|---|---|
| 40580–40585 | V | ×1 | Spannung L1/L2/L3 |
| 40586–40591 | A | ×1 | Strom L1/L2/L3 |
| 40592/40593 | Hz | ×1 | Netzfrequenz |
| 40596/40597 | W | ×10 | Wirkleistung (positiv = Einspeisung, negativ = Bezug) |
| 40598/40599 | VA | ×10 | Scheinleistung |

### Inverter
| Register | Einheit | Scale | Beschreibung |
|---|---|---|---|
| 40600/40601 | °C | ×1 | Inverter-Temperatur |

## Energiezähler – heute (reset um Mitternacht)

| Register | Einheit | Beschreibung |
|---|---|---|
| 42163/42164 | kWh | Netzbezug heute |
| 42179/42180 | kWh | Netzeinspeisung heute |
| 42195/42196 | kWh | PV-Strang 1 Ertrag heute |
| 42211/42212 | kWh | PV-Strang 2 Ertrag heute |
| 42243/42244 | kWh | Batterie geladen heute |
| 42145/42146 | kWh | Batterie entladen heute |

## Energiezähler – Lifetime

| Register | Einheit | Beschreibung |
|---|---|---|
| 42113/42114 | kWh | Batterie-Nettoenergie |
| 42161/42162 | Wh | Netzbezug Lifetime |
| 42177/42178 | Wh | Netzeinspeisung Lifetime |
| 42193/42194 | kWh | PV-Strang 1 Gesamtertrag |
| 42209/42210 | kWh | PV-Strang 2 Gesamtertrag |
| 42225/42226 | kWh | Batterie geladen (Lifetime) |
| 42241/42242 | kWh | Batterie entladen (Lifetime) |
| 42257/42258 | kWh | Gesamtsystemenergie |

## Steuer-/Konfigurationsregister

### Explizit als beschreibbar implementiert (Home-Assistant-Integration)
| Register | Beschreibung | Range |
|---|---|---|
| 40536 | Minimum-SOC-Limit (Entladeuntergrenze) | 0–100 %, Schritt 1 |
| 40541 | LED-Helligkeit | 0–100 %, Schritt 10 |

### Bekannt, aber (noch) nicht als schreibbar exponiert
| Register | Beschreibung |
|---|---|
| 40530 | `system_modes` (UINT32, vermutlich Bitmaske) |
| 40538 / 40609 | `feed_in_power_max` (adressabhängig vom Inverter-Modell) |
| 40546 | `limit_inv_power` |
| 40548 | `limit_inv_max` |
| 40554 | `battery_discharge_power_limit` |
| 40556 | `battery_charge_power_limit` |

### Modi/Enums (aktuell nur lesend implementiert)
- `grid_mode`
- `operating_mode`
- `self_use_mode_ena`, `intelligent_mode_ena`, `battery_saver_mode_ena` (Binärflags)

### Unbekannte Konfigurationsregister (nicht schreiben ohne Verständnis der Wirkung!)
| Register | Beobachteter Wert | Notiz |
|---|---|---|
| 40527 | 100 | evtl. Max-SOC-Limit (%) |
| 40528 | 15000 | evtl. Leistungslimit (W×10?) |
| 40615–40618 | 10000/6000 | unbekannt |
| 40625–40628 | 800/10000 | unbekannt |

## Decoding-Snippets

```python
import struct
from pymodbus.client import ModbusTcpClient

client = ModbusTcpClient("192.168.x.x", port=502, timeout=3)
client.connect()

def read_uint16(addr):
    r = client.read_holding_registers(addr, count=1, slave=1)
    return r.registers[0] if not r.isError() else None

def read_float(addr, scale=1):
    r = client.read_holding_registers(addr, count=2, slave=1)
    if r.isError():
        return None
    # word-swapped: registers[1] = high word, registers[0] = low word
    raw = struct.pack('>HH', r.registers[1], r.registers[0])
    return round(struct.unpack('>f', raw)[0] * scale, 3)

def read_serial(addr=40004, count=8):
    r = client.read_holding_registers(addr, count=count, slave=1)
    chars = []
    for val in r.registers:
        chars.append(chr((val >> 8) & 0xFF))
        chars.append(chr(val & 0xFF))
    return ''.join(c for c in chars if 32 <= ord(c) <= 126)
```

## Bekannte Lücken

Folgende Werte sind laut Community-Doku **nicht** über Modbus TCP verfügbar,
nur über die EcoFlow Cloud-API:

- State of Health (`bpSoh`)
- Batteriespannung (`bpVol`)
- Min/Max-Zelltemperatur
- Batteriestrom (`bpAmp`)
- Einzelne MPPT-Spannungen/-Leistungen
- Phasenweise Blindleistung

## Vorsicht bei Schreibzugriffen

Dieses Mapping ist reverse-engineert und nicht von EcoFlow bestätigt. Vor
produktivem Schreibzugriff auf Leistungslimit-Register (40554/40556) erst mit
unkritischen Registern (Min-SOC, LED) testen und Geräteverhalten beobachten.
Firmware-Updates können Adressen/Verhalten ändern.
