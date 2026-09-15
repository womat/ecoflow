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

## `modbusread`

Ein universeller, **rein lesender** Modbus-TCP-Reader – Adresse, Register und Typ rein,
Wert raus. Kein EcoFlow-Wissen im Tool, keine eingebaute Register-Map; damit auch für
andere Modbus-TCP-Geräte brauchbar. Gedacht, um die community-ermittelten Register aus
`modbus-registers.md` am eigenen Gerät zu überprüfen.

```
go build ./cmd/modbusread

modbusread <host[:port]> <address> <type> [flags]
```

Adresse dezimal (`42082`) oder hex (`0xA462`), Typ `raw`, `uint16`, `int16`, `uint32`,
`int32`, `float32`, `float64` oder `string`.

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

## Kurzüberblick

- Eine spezifische, offiziell dokumentierte REST-API für den DC Fit existiert nicht.
- Die generische EcoFlow Developer/Open API (Cloud) liefert für die PowerOcean-Familie
  häufig Fehler 1006 "not allowed".
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
- Genauer Freischalt-Pfad in der EcoFlow Pro App

## Quellen

- https://developer.ecoflow.com
- https://github.com/Feberdin/ecoflow-powerocean-ha
- https://github.com/MaxGrmm/ecoflow-poweroceanplus-modbus
- https://github.com/MaxGrmm/EF-PowerOcean-TcpModbus
- https://github.com/windmark/EF-PowerOcean-TcpModbus
- https://docs.evcc.io/en/meters/ecoflow-powerocean-modbus
- https://www.photovoltaikforum.com/thread/247994-ecoflow-powerocean-modbus-protokoll/

## Lizenz

MIT – siehe [`LICENSE`](./LICENSE). Beachte, dass Teile der Register-Informationen
aus MIT-lizenzierten Drittquellen (s.o.) übernommen wurden; die jeweiligen
Original-Links sind in `modbus-registers.md` angegeben.
