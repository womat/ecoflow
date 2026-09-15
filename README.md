# EcoFlow PowerOcean DC Fit – API & Modbus Notizen

Persönliche Recherche-Notizen zu Integrationsmöglichkeiten (REST-API, lokales
Modbus TCP) für den EcoFlow PowerOcean DC Fit.

> **Disclaimer:** Diese Doku basiert größtenteils auf Community-Reverse-Engineering,
> nicht auf offizieller EcoFlow-Dokumentation. EcoFlow unterstützt oder bestätigt
> die hier beschriebenen Modbus-Register nicht offiziell. Nutzung auf eigenes
> Risiko, insbesondere bei Schreibzugriffen auf Register.

## Inhalt

| Datei | Beschreibung |
|---|---|
| [`api-status.md`](./api-status.md) | Überblick: Cloud-REST-API vs. lokales Modbus TCP, bekannte Probleme (z.B. Fehler 1006), Freischaltung |
| [`modbus-registers.md`](./modbus-registers.md) | Register-Map (SOC, Batterie, PV, Netz, Energiezähler, Steuerregister) inkl. Decoding-Beispielen |
| [`cmd/modbusread`](./cmd/modbusread) | Kleines Go-CLI zum Nachmessen der Register am Gerät (s.u.) |
| [`scripts/ecoflow-api.sh`](./scripts/ecoflow-api.sh) | Shell-Skript für signierte Leseaufrufe gegen die EcoFlow Cloud-API (s.u.) |

## `modbusread`

Ein universeller, **rein lesender** Modbus-Reader – Adresse, Register und Typ rein,
Wert raus. Kein EcoFlow-Wissen im Tool, keine eingebaute Register-Map; damit auch für
beliebige andere Modbus-Geräte brauchbar. Gedacht, um die community-ermittelten Register
aus `modbus-registers.md` am eigenen Gerät zu überprüfen.

```
go build ./cmd/modbusread

modbusread <ziel> <address> <type> [flags]
```

Adresse dezimal (`42082`) oder hex (`0xA462`), Typ `raw`, `uint16`, `int16`, `uint32`,
`int32`, `float32`, `float64` oder `string`.

Das **Ziel** entscheidet über den Transport, ohne zusätzliches Flag:

| Eingabe | Transport |
|---|---|
| `192.168.1.50`, `plc.local:1502` | Modbus TCP (Port-Default 502) |
| `/dev/ttyUSB0`, `/dev/tty.usbserial-…`, `COM3` | Modbus RTU über die serielle Leitung |
| `rtu://…`, `tcp://…`, `rtuovertcp://…`, `udp://…` | explizit, schlägt die Erkennung |

```console
$ modbusread 192.168.1.50 42082 uint16
addr     raw                  value
42082    0x0064               100

# PV-Gesamtleistung: Float mit vertauschten Words (EcoFlow-Konvention)
$ modbusread 192.168.1.50 40574 float32 --word-order low

# Bereich dumpen, um unbekannte Register zu finden
$ modbusread 192.168.1.50 40520 raw --count 120 --out hex

# Herausfinden, welches Register auf eine Änderung in der App reagiert
$ modbusread 192.168.1.50 40520 raw --count 120 --interval 1s --on-change
```

Wichtig: **Adressen werden nicht umgerechnet** – sie gehen so auf den Draht, wie sie
getippt werden (0-based). Die Tabellen in `modbus-registers.md` sind als 1-based
bezeichnet; ob das stimmt, ist offen (siehe „Offene Punkte“).

Bei einem seriellen Ziel kommen die Leitungsparameter dazu – `--baud` (19200),
`--databits` (8), `--parity` (none) und `--stopbits`. Letzteres folgt bei `0` der
Modbus-Regel: zwei Stoppbits ohne Parität, eines mit. Sie müssen exakt zum Gerät passen,
sonst kommt Datenmüll oder gar nichts. Am Bus hängen typischerweise mehrere Geräte, dort
ist `--unit` kein Formalismus mehr:

```console
$ modbusread /dev/ttyUSB0 40069 uint16 --baud 9600 --parity even --unit 3
```

An einem TCP-Ziel werden diese Flags **abgelehnt** statt ignoriert – eine Baudrate, die
stillschweigend wirkungslos bleibt, schickt einen sonst auf Fehlersuche an der Verkabelung.

Weitere Flags: `--unit`, `--fc holding|input`, `--byte-order`, `--timeout`, `--json`,
`--samples`. `modbusread --help` zeigt alles.

### Installation

Mit vorhandener Go-Toolchain direkt aus dem Repo:

```
go install github.com/womat/ecoflow/cmd/modbusread@latest
```

Für Maschinen **ohne Go** – etwa den Raspberry Pi neben der Anlage – liegen fertige
Binaries unter [Releases](https://github.com/womat/ecoflow/releases). Sie sind statisch
gelinkt (`CGO_ENABLED=0`), es ist also nichts zu installieren: entpacken und ausführen.

```bash
VERSION=v0.1.0
ARCH=linux-arm64   # siehe Tabelle unten

curl -LO "https://github.com/womat/ecoflow/releases/download/$VERSION/modbusread-$VERSION-$ARCH.tar.gz"
tar -xzf "modbusread-$VERSION-$ARCH.tar.gz"
./modbusread --version
```

| Maschine | `ARCH` |
|---|---|
| Raspberry Pi 3/4/5 mit 64-bit Raspberry Pi OS | `linux-arm64` |
| Raspberry Pi mit 32-bit OS, inkl. Zero und Pi 1 | `linux-arm` |
| gewöhnlicher Linux-PC/Server, NAS | `linux-amd64` |
| Mac mit Apple Silicon | `darwin-arm64` |
| Mac mit Intel | `darwin-amd64` |
| Windows | `windows-amd64` |

Das 32-bit-Archiv ist mit `GOARM=6` gebaut und läuft deshalb auch auf den älteren
ARMv6-Modellen. Prüfen lässt sich der Download gegen die `checksums.txt` desselben
Release:

```bash
curl -LO "https://github.com/womat/ecoflow/releases/download/$VERSION/checksums.txt"
sha256sum -c checksums.txt --ignore-missing
```

`modbusread --version` meldet Commit und Go-Version aus den Build-Infos, die Go beim
`go build` selbst einstempelt; Release-Binaries tragen die Tag-Nummer.

Ein Release entsteht aus einem Tag `vX.Y.Z` auf `main`; der Workflow baut alle Ziele und
hängt sie samt Checksummen an das GitHub-Release.

## `scripts/ecoflow-api.sh`

Gegenstück zu `modbusread` für den Cloud-Weg: ein kleines Shell-Skript, das die
HMAC-Signatur der EcoFlow Developer/Open API baut und die Antwort roh ausgibt.
Ebenfalls **rein lesend** – es kennt nur GET-Endpunkte. Braucht `bash`, `curl` und
`openssl`; `jq` wird benutzt, wenn es da ist.

Zugangsdaten kommen aus der Umgebung, nie aus dem Repo:

```bash
export ECOFLOW_ACCESS_KEY='…'   # developer-eu.ecoflow.com → Security
export ECOFLOW_SECRET_KEY='…'
# ECOFLOW_HOST setzt den Host, Default https://api-e.ecoflow.com (EU)

scripts/ecoflow-api.sh devices                    # Geräte des Kontos
scripts/ecoflow-api.sh quota <SN>                  # alle Werte eines Geräts
scripts/ecoflow-api.sh -v get /iot-open/sign/device/list   # beliebiger GET, mit Debug
scripts/ecoflow-api.sh values <SN> bpSoc bpPwr     # gezielte Werte (POST-Endpunkt)
scripts/ecoflow-api.sh cert                       # MQTT-Zugangsdaten des Kontos
scripts/ecoflow-api.sh mqtt <SN>                  # Topic abonnieren (Ctrl-C beendet)
scripts/ecoflow-api.sh selftest                   # Signaturaufbau prüfen, ohne Keys
```

`values` nutzt `POST /iot-open/sign/device/quota`, den in EcoFlows PowerOcean-Doku
beschriebenen Weg für gezielte Größen (`bpSoc`, `bpPwr`, `mpptPwr`, `sysLoadPwr`,
`sysGridPwr`, `pcsAPhase` …). POST ist hier der *Lese*-Endpunkt; das PUT-Gegenstück, das
Werte setzt, kennt das Skript bewusst nicht.

`mqtt` holt sich die Zugangsdaten über `/iot-open/sign/certification` und abonniert
`/open/<certificateAccount>/<SN>/quota` (zweites Argument wechselt das Suffix, z.B.
`status` oder `#`). Braucht zusätzlich `jq` und `mosquitto_sub`
(`brew install mosquitto` bzw. `apt install mosquitto-clients`). Auch hier wird nur
abonniert, nie publiziert.

Exit-Code `0` heißt `code 0` von der API, `2` jeder andere Code. **`2` mit Code 1006**
ist die interessante Antwort: Dann ist das Modell von der Developer-API ausgeschlossen
(siehe `api-status.md`) und nur der lokale Modbus-Weg bleibt. Das Gerät muss an das
eigene EcoFlow-Konto gebunden sein, sonst bleibt die Liste leer.

## Kurzüberblick

- Eine spezifische, offiziell dokumentierte REST-API für den DC Fit existiert nicht.
- Die generische EcoFlow Developer/Open API (Cloud) liefert für die PowerOcean-Familie
  Fehler 1006 "not allowed" – eine Modell-Sperrliste, kein Bug. Der **DC Fit
  (SN-Präfix `HC31`) ist betroffen, am Gerät bestätigt**: `device/list` listet ihn zwar
  mit Code 0, `device/quota/all` verweigert aber die Messwerte mit 1006.
- Praktikabler Weg: **lokales Modbus TCP** (Port 502) – muss vom Installateur über
  die EcoFlow Pro App freigeschaltet werden, Registerbelegung ist nicht offiziell
  dokumentiert, sondern community-ermittelt.
- Details siehe die beiden verlinkten Dateien.

## Offene Punkte

- Bestätigung, ob das Register-Mapping (ermittelt am PowerOcean Plus) 1:1 für
  den DC Fit gilt, oder ob es eigene Adress-Overrides gibt
- Ob die Register in `modbus-registers.md` 1-based oder 0-based zu lesen sind: die
  Tabelle sagt 1-based, das Python-Snippet daneben verwendet die Zahlen literal
  (pymodbus adressiert 0-based). Mit `modbusread` am Gerät klärbar: SOC einmal auf
  42081 und einmal auf 42082 lesen
- Genauer Menüpfad zum Modbus-Schalter in der EcoFlow Pro App (der Zugangsweg zur
  App selbst ist geklärt, siehe `api-status.md`)
- Ob der MQTT-Weg der Open API für den DC Fit Daten liefert oder dieselbe
  1006-Sperre greift

## Quellen

- https://developer.ecoflow.com
- https://github.com/Feberdin/ecoflow-powerocean-ha
- https://github.com/MaxGrmm/ecoflow-poweroceanplus-modbus
- https://github.com/MaxGrmm/EF-PowerOcean-TcpModbus
- https://github.com/windmark/EF-PowerOcean-TcpModbus
- https://docs.evcc.io/en/meters/ecoflow-powerocean-modbus
- https://github.com/shuette42/ecoflow-energy-ha
- https://www.photovoltaikforum.com/thread/247994-ecoflow-powerocean-modbus-protokoll/

## Lizenz

MIT – siehe [`LICENSE`](./LICENSE). Beachte, dass Teile der Register-Informationen
aus MIT-lizenzierten Drittquellen (s.o.) übernommen wurden; die jeweiligen
Original-Links sind in `modbus-registers.md` angegeben.
