# Modbus-Register – EcoFlow PowerOcean (DC Fit / Plus)

> **Status:** Community-reverse-engineert, von EcoFlow nicht offiziell dokumentiert oder
> unterstützt. Zwei Quellen mit **abweichenden Adressen** liegen vor (siehe
> „Widersprüche der Quellen"); am eigenen Gerät ist noch nichts nachgemessen.

**Testgerät dieser Notizen:** PowerOcean **DC Fit**, Firmware **1.0.6.20**.

**Zur Modellfrage:** Die aktuelle Quelle behandelt den **DC Fit als Normalfall** – im
gesamten Register-Katalog gibt es genau *einen* modellabhängigen Sonderfall, und der gilt
dem **Plus** (`feed_in_power_max`, siehe unten). Die frühere Sorge in diesen Notizen, das
Mapping sei „am Plus ermittelt und für den DC Fit fraglich", hat die Richtung vertauscht.
Bestätigt ist damit trotzdem nichts – nur der Verdacht hat sich gedreht.

## Quellen

- https://github.com/MaxGrmm/EF-PowerOcean-TcpModbus – Home-Assistant-Integration.
  Register-Map als Code in `custom_components/ef_powerocean_tcpmodbus/const.py`
  (`MODBUS_REGISTERS`), Modellkatalog in `models.py`, Protokollnotizen in
  `EcoFlow_PowerOcean_Modbus.md`. **Aktuellere und gegen das EcoFlow-Portal
  gegengeprüfte Quelle** – im Konfliktfall die belastbarere.
- https://github.com/MaxGrmm/ecoflow-poweroceanplus-modbus – ältere Register-Tabellen,
  ermittelt durch Scannen von 40001–44096 an einem PowerOcean **Plus**.
- `evcc-io/evcc`, `templates/definition/meter/ecoflow-powerocean-modbus.yaml` –
  Meter-Template der Ladesoftware evcc. **Unabhängige dritte Quelle**: entstanden
  außerhalb der beiden EcoFlow-Repos und bestätigt deren aktuelle Adressen (siehe
  „Bestätigung durch evcc"). Dazu https://docs.evcc.io/en/meters/ecoflow-powerocean-modbus
  für Freischaltungshinweis und Modbus-Parameter.

Beide GitHub-Repos stehen unter MIT-Lizenz ("free to use, modify, and distribute with
attribution").

## Freischaltung

Muss vom **EcoFlow-Installateur/-Partner** über die EcoFlow **Pro App** (Installateur-App,
nicht die normale Endkunden-App) aktiviert werden – standardmäßig deaktiviert.

Der Pfad laut `EcoFlow_PowerOcean_Modbus.md`:

> Pro App öffnen → **Wechselrichter auswählen** → Control Mode auf **„Modbus control"**
> umstellen.

Es ist also kein versteckter Schalter, sondern ein **Wechsel des Betriebsmodus**. Nicht
bestätigt ist, wie sich dieser Modus auf das interne Scheduling auswirkt – bei rein
lesendem Zugriff sollte er folgenlos sein, belegt ist das nicht.

Der Zugang zur Pro App selbst (Installateur-Rolle, Anlagen- vs. Besitzer-Bindung,
Entbindung von einem fremden Konto) steht in [`api-status.md`](./api-status.md),
Abschnitt „Zugang zur EcoFlow Pro App"; die Fragen für den Termin in Abschnitt 2c. Für die
Freischaltung ist keine Übertragung der Anlage nötig – es genügt, dass ein Pro-Zugang den
Modus umstellt.

## Verbindung

| Parameter | Wert |
|---|---|
| Protokoll | Modbus TCP |
| Port | 502 |
| Unit-/Slave-ID | 1 (das Gerät antwortet praktisch auf jede ID) |
| Funktionscodes | `0x03` lesen; `0x06`/`0x10` schreiben |
| Adressierung | direkte 4xxxx-Adressen, **literal aufs Kabel** |
| Max. Register je Read | 125 (Modbus-Grenze einer Antwort) |

**Zur Adressierung:** Die Referenz-Integration übergibt die 4xxxx-Zahlen unverändert an
pymodbus, sie gehen also genau so auf den Draht. `modbusread` tut dasselbe (siehe
`CLAUDE.md`: Adressen werden nie umgerechnet) – die Tabellen unten sind damit **1:1
verwendbar**. Die frühere Angabe „Register-Nummerierung 1-based" in diesen Notizen ist
dadurch stark in Zweifel gezogen, aber erst am Gerät endgültig zu klären.

## Datentypen und Wortreihenfolge

| Typ | Register | Bemerkung |
|---|---|---|
| `UINT16` | 1 | Prozentwerte, Zähler, Fehlercodes, Enums |
| `UINT32` | 2 | Kapazitäten und Leistungsgrenzen, `(high << 16) \| low` |
| `FLOAT32` | 2 | IEEE 754, **word-swapped** – Leistung, Spannung, Strom, Energie |
| `SERIAL` | 8 | 16 ASCII-Bytes, je Wort High-Byte zuerst |

**Lesen und Schreiben verwenden unterschiedliche Wortreihenfolgen** – laut Quelle
widerspricht sich EcoFlows eigene Doku hier, gemessen wurde am Plus:

| Richtung | Wortreihenfolge |
|---|---|
| Lesen (`0x03`) | **Low-Word zuerst** (Register *N* = untere Hälfte) |
| Schreiben (`0x10`) | **High-Word zuerst** |

Für `modbusread` heißt das beim Lesen: `--word-order low`.

Die Falle beim Schreiben: Wird ein 32-Bit-Wert in *Lese*-Reihenfolge geschrieben, lehnt das
Gerät das nicht ab – es speichert die Worte und interpretiert sie High-Word-first, handelt
also auf einem um Faktor 65536 zu großen Wert. Ein Schreibzugriff kann dadurch „erfolgreich"
aussehen, während etwas völlig anderes passiert.

## Register-Map (aktuelle Quelle: `const.py`)

Die Integration liest in vier Blöcken:

| Block | Start | Worte | Inhalt |
|---|---|---|---|
| Device Info | 40002 | 12 | Produkttyp, Seriennummer, Firmware |
| Live | 40519 | 89 | Leistungen, Limits, Spannungen, Ströme, PV |
| Faults | 42049 | 45 | Fehlerzähler und -codes, SOC je Batterie |
| Energie | 42161 | 100 | Lifetime- und Tageszähler |

### Geräteidentität

| Register | Typ | Beschreibung |
|---|---|---|
| 40002 | UINT16 | `product_category` |
| 40003 | UINT16 | `product_number` |
| 40004 | SERIAL (8) | Seriennummer, 16 ASCII-Bytes |
| 40012 | UINT32 | Firmware-Version |

**Offen und am eigenen Gerät klärbar:** `models.py` kann den **DC Fit nicht** aus diesen
Registern erkennen – belegt sind nur `product_number` 1 (Single-/Three-Phase, je nach
`product_category`), 2 (Single-Phase) und 3 (Plus); im Code steht dazu ausdrücklich
„We are not sure of the product number for the remaining models." Wer 40002/40003 an einem
DC Fit liest, schließt diese Lücke – auch upstream.

### Live-Werte

| Register | Typ | Beschreibung |
|---|---|---|
| 40519 | FLOAT32 | `house_power` – Hausverbrauch |
| 40521 | FLOAT32 | `grid_power` – Netzleistung |
| 40523 | FLOAT32 | `solar_power` – PV-Leistung |
| 40525 | FLOAT32 | `battery_power` – Batterieleistung |
| 40527 | UINT16 | `battery_soc` – **System-SOC in %** |
| 40528 | UINT32 | `inverter_rated_power` |
| 40574 | FLOAT32 | `battery_voltage` |
| 40576 | FLOAT32 | `battery_current` |
| 40578 | FLOAT32 | `battery_temperature` |
| 40580 / 40582 / 40584 | FLOAT32 | Spannung L1 / L2 / L3 |
| 40586 / 40588 / 40590 | FLOAT32 | Strom L1 / L2 / L3 |
| 40592 | FLOAT32 | `inverter_temperature` |
| 40594 | FLOAT32 | `frequency` – Netzfrequenz |
| 40596 / 40598 / 40600 | FLOAT32 | PV-Strang 1 / 2 / 3 Spannung |
| 40602 / 40604 / 40606 | FLOAT32 | PV-Strang 1 / 2 / 3 Strom |

Die Float-Werte tragen physikalische Einheiten direkt; die Skalierungsfaktoren (×10, ×100,
×1000) der älteren Quelle entfallen hier – siehe „Widersprüche der Quellen".

### Fehler und Batterien

| Register | Typ | Beschreibung |
|---|---|---|
| 42049 | UINT16 | `fault_count` |
| 42050 ff. | UINT16 | `fault_1` … `fault_n` |
| 42081 | UINT16 | `battery_count` – **Anzahl Batterien** |
| 42082 ff. | UINT16 | SOC je Batterie (`42081 + n`) |

### Energiezähler

| Register | Typ | Beschreibung |
|---|---|---|
| 42161 / 42163 | FLOAT32 | Netzbezug gesamt / heute |
| 42177 / 42179 | FLOAT32 | Netzeinspeisung gesamt / heute |
| 42225 / 42227 | FLOAT32 | Batterie geladen gesamt / heute |
| 42241 / 42243 | FLOAT32 | Batterie entladen gesamt / heute |
| 42257 / 42259 | FLOAT32 | Solarertrag gesamt / heute |

## Bestätigung durch evcc

Das evcc-Template ist unabhängig von den beiden EcoFlow-Repos entstanden und verwendet
exakt die Adressen der aktuellen Karte – inklusive `float32s`, evccs Bezeichnung für
**word-swapped** Float, sowie Port 502 und Unit-ID 1:

| Register | evcc | Deckt sich mit |
|---|---|---|
| 40521 | Grid power, W | `grid_power` |
| 40523 | Solar power, W | `solar_power` |
| 40525 | Battery power, W | `battery_power` |
| 40527 | Battery state of charge, % (`uint16`) | `battery_soc` |
| 42161 | Grid import total, kWh | `grid_import_total` |
| 42241 | Battery discharged total, kWh | `bat_discharged_total` |
| 42257 | Solar yield total, kWh | `solar_total` |

Damit ist **40527 als System-SOC von zwei unabhängigen Quellen gestützt** – die ältere
Deutung „evtl. Max-SOC-Limit" ist damit unwahrscheinlich geworden (gemessen ist sie
deshalb trotzdem noch nicht).

**Neuer Widerspruch beim Vorzeichen:** evcc kommentiert 40525 mit „positive when
discharging, negative when charging" und dreht den Rohwert per `scale: -1` um. Die ältere
Tabelle in diesen Notizen behauptete das Gegenteil (positiv = laden). Am Gerät zu
entscheiden: bei bekanntem Ladevorgang einmal 40525 lesen.

## Widersprüche der Quellen (= der Messplan)

Die ältere Tabelle und die aktuelle Integration deuten teils dieselben Adressen
unterschiedlich. Beide Quellen stimmen überein bei **40580–40591** (Phasenspannungen und
-ströme) und **40602–40607** (PV-Strangströme). Die Abweichungen:

| Adresse | Ältere Quelle | Aktuelle Quelle |
|---|---|---|
| 40527 | „evtl. Max-SOC-Limit", Wert 100 | `battery_soc` (System-SOC in %) |
| 40574 | PV-Gesamtleistung (×100) | `battery_voltage` |
| 40576 | Batterieleistung (×1000) | `battery_current` |
| 40592 | Netzfrequenz | `inverter_temperature` |
| 40594 | – | `frequency` |
| 40596 | Wirkleistung (×10) | PV-Strang-1-Spannung |
| 40600 | Inverter-Temperatur | PV-Strang-3-Spannung |
| 42081 | Systemstatus (1 = Online) | `battery_count` |
| 42082 | Batterie-SOC | SOC der **ersten** Batterie |

Zwei Beobachtungen dazu, beide **unbestätigt**:

- Bei 42081/42082 können sich beide Deutungen wie eine Bestätigung angefühlt haben: Eine
  Anlage mit *einer* Batterie liefert dort eine 1 – als „online" ebenso plausibel wie als
  „battery_count".
- Die Skalierungsfaktoren der älteren Quelle (×10, ×100, ×1000) wirken wie Korrekturen für
  falsch zugeordnete Adressen. Wenn die aktuelle Karte stimmt, braucht es sie nicht.

**Am Gerät zu entscheiden**, sobald Modbus frei ist – jede Zeile ist ein Einzeltest:

```
modbusread <ip> 40527 uint16                       # 100 → eher Limit; 0..100 plausibel → SOC
modbusread <ip> 40574 float32 --word-order low     # Spannung (~400 V) oder Leistung?
modbusread <ip> 42081 uint16                       # Anzahl Batterien oder Online-Flag?
modbusread <ip> 40525 float32 --word-order low     # Vorzeichen bei bekanntem Ladevorgang
modbusread <ip> 40519 raw --count 100 --out hex    # Live-Block am Stück ansehen
```

Die offiziellen Cloud-Feldnamen in [`api-status.md`](./api-status.md) (`bpSoc`, `bpPwr`,
`mpptPwr`, `sysLoadPwr`, `sysGridPwr` samt Vorzeichenkonvention) sind dabei die
Gegenprobe: Ein Register, dessen Wert nicht zur offiziellen Semantik passt, ist falsch
gedeutet.

## Steuer-/Konfigurationsregister

### Explizit als beschreibbar implementiert (Home-Assistant-Integration)
| Register | Beschreibung | Range |
|---|---|---|
| 40536 | `min_soc_limit` (Entladeuntergrenze) | 0–100 %, Schritt 1 |
| 40541 | `device_led_brightness` | 0–100 %, Schritt 10 |

### Bekannt, aber (noch) nicht als schreibbar exponiert
| Register | Beschreibung |
|---|---|
| 40530 | `system_modes` (UINT32, vermutlich Bitmaske) |
| 40609 | `feed_in_power_max` – **einziger modellabhängiger Fall**: am PowerOcean **Plus** stattdessen 40538 |
| 40546 | `limit_inv_power` |
| 40548 | `limit_inv_max` |
| 40552 | `battery_capacity` |
| 40554 | `battery_discharge_power_limit` |
| 40556 | `battery_charge_power_limit` |

### Modi/Enums (aktuell nur lesend implementiert)
- `grid_mode`, `operating_mode`
- `self_use_mode_ena`, `intelligent_mode_ena`, `battery_saver_mode_ena` (Binärflags)

### Nicht kartierte Konfigurationsregister
Der Bereich unterhalb 40574 enthält mehr Register, als die Integration liest; einige davon
sind **vorzeichenbehaftete** 32-Bit-Werte, für die es dort bisher keinen Decoder gibt.
`modbusread` kann sie mit `int32` lesen.

## Decoding-Snippet (pymodbus)

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
    # Lesereihenfolge: registers[0] = Low-Word, registers[1] = High-Word
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

## Bekannte Lücken – was Modbus nicht liefert

Die Cloud kennt deutlich mehr als Modbus. Betroffen sind unter anderem Zellspannungen,
Temperaturen je Pack, State of Health und Zyklenzahl, DC-Bus- und Isolationswerte,
AFCI-Selbsttest, **phasenweise Wirk-/Blind-/Scheinleistung** (Modbus hat nur Spannung und
Strom), rund 180 Netzschutzparameter, Zeitpläne/Peak-Shaving/VPP, ausführliche Fehlerlisten
sowie Monats- und Jahresenergien.

Für dieses Gerät ist die **offizielle** Cloud-API gesperrt (siehe `api-status.md`).
Erreichbar bleiben diese Werte laut Quelle über das Endkunden-Portal
`user-portal.ecoflow.com`: im Netzwerk-Tab des Browsers der Request `detail?<seriennummer>`.
Inoffiziell, jederzeit änderbar – aber es ist der einzige bekannte Weg an diese Daten.

## Vorsicht bei Schreibzugriffen

`modbusread` schreibt grundsätzlich nicht (siehe `CLAUDE.md`). Wer es anderweitig tut:

- Das Mapping ist reverse-engineert und von EcoFlow nicht bestätigt; Firmware-Updates
  können Adressen und Verhalten ändern.
- **Rücklesen beweist nichts.** Unmittelbar nach einem 32-Bit-Schreibzugriff stehen die
  Worte so da, wie sie gesendet wurden; erst Sekunden später dreht die Firmware sie in
  Lesereihenfolge. Beides belegt nur die Zustellung, nicht die Wirkung. Verlässlich ist
  allein das Verhalten: bewegte Leistung, LED, oder die Pro App.
- Vor produktivem Zugriff auf die Leistungslimit-Register (40554/40556) erst mit
  unkritischen Registern (Min-SOC, LED) testen und das Geräteverhalten beobachten.
- Schreibzugriffe können das interne Scheduling stören – immer nur ein Register auf einmal
  ändern.
