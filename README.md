# EcoFlow PowerOcean DC Fit – API & Modbus Notizen

Persönliche Recherche-Notizen zu Integrationsmöglichkeiten (REST-API, lokales
Modbus TCP) für den EcoFlow PowerOcean DC Fit.

> **Disclaimer:** Diese Doku basiert größtenteils auf Community-Reverse-Engineering,
> nicht auf offizieller EcoFlow-Dokumentation. EcoFlow unterstützt oder bestätigt
> die hier beschriebenen Modbus-Register nicht offiziell. Nutzung auf eigenes
> Risiko, insbesondere bei Schreibzugriffen auf Register.

## Inhalt

| Datei                                                | Beschreibung                                                                                          |
|------------------------------------------------------|-------------------------------------------------------------------------------------------------------|
| [`api-status.md`](./api-status.md)                   | Überblick: Cloud-REST-API vs. lokales Modbus TCP, bekannte Probleme (z.B. Fehler 1006), Freischaltung |
| [`modbus-registers.md`](./modbus-registers.md)       | Register-Map (SOC, Batterie, PV, Netz, Energiezähler, Steuerregister) inkl. Decoding-Beispielen       |
| [`cmd/modbusread`](./cmd/modbusread)                 | Kleines Go-CLI zum Nachmessen der Register am Gerät (s.u.)                                            |
| [`scripts/ecoflow-api.sh`](./scripts/ecoflow-api.sh) | Shell-Skript für signierte Leseaufrufe gegen die EcoFlow Cloud-API (s.u.)                             |
| [`scripts/ecoflow-frames.py`](./scripts/ecoflow-frames.py) | Packt die Live-Frames aus `ecoflow-api.sh live` aus – der Weg zu aktuellen Messwerten (s.u.)    |

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

| Eingabe                                           | Transport                            |
|---------------------------------------------------|--------------------------------------|
| `192.168.1.50`, `plc.local:1502`                  | Modbus TCP (Port-Default 502)        |
| `/dev/ttyUSB0`, `/dev/tty.usbserial-…`, `COM3`    | Modbus RTU über die serielle Leitung |
| `rtu://…`, `tcp://…`, `rtuovertcp://…`, `udp://…` | explizit, schlägt die Erkennung      |

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

| Maschine                                        | `ARCH`          |
|-------------------------------------------------|-----------------|
| Raspberry Pi 3/4/5 mit 64-bit Raspberry Pi OS   | `linux-arm64`   |
| Raspberry Pi mit 32-bit OS, inkl. Zero und Pi 1 | `linux-arm`     |
| gewöhnlicher Linux-PC/Server, NAS               | `linux-amd64`   |
| Mac mit Apple Silicon                           | `darwin-arm64`  |
| Mac mit Intel                                   | `darwin-amd64`  |
| Windows                                         | `windows-amd64` |

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
scripts/ecoflow-api.sh request <SN> bpSoc         # Werte per MQTT anfordern
export ECOFLOW_PORTAL_TOKEN="$(scripts/ecoflow-api.sh login)"   # Token per Login holen
scripts/ecoflow-api.sh portal <SN>                # Endkunden-Portal statt Developer-API
scripts/ecoflow-api.sh status <SN>                # dieselben Daten als Kurzübersicht
scripts/ecoflow-api.sh portal-get <pfad>          # beliebiger GET gegen die Portal-API
scripts/ecoflow-api.sh app-cert                   # MQTT-Zugangsdaten des App-Kanals
scripts/ecoflow-api.sh live <SN>                  # App-Kanal abonnieren und wachhalten
scripts/ecoflow-api.sh fast <SN>                  # dasselbe mit schnellem Takt (schreibt!)
scripts/ecoflow-api.sh app-mqtt <SN>              # mitlesen, was die App ans Geraet sendet
scripts/ecoflow-api.sh selftest                   # Signatur gegen EcoFlows Testvektor
```

`values` nutzt `POST /iot-open/sign/device/quota`, den in EcoFlows PowerOcean-Doku
beschriebenen Weg für gezielte Größen (`bpSoc`, `bpPwr`, `mpptPwr`, `sysLoadPwr`,
`sysGridPwr`, `pcsAPhase` …). POST ist hier der *Lese*-Endpunkt; das PUT-Gegenstück, das
Werte setzt, kennt das Skript bewusst nicht.

`mqtt` holt sich die Zugangsdaten über `/iot-open/sign/certification` und abonniert
`/open/<certificateAccount>/<SN>/quota` (zweites Argument wechselt das Suffix, z.B.
`status`, `get_reply` oder `#`). Jede Nachricht bekommt einen Zeitstempel – eine stille
Aufzeichnung ist nur dann ein Beleg, wenn man weiß, wann sie still war. Braucht
zusätzlich `jq` und `mosquitto_sub` (`brew install mosquitto` bzw.
`apt install mosquitto-clients`).

`portal` geht einen anderen Weg: Es fragt `provider-service/user/device/detail` ab – den
Endpunkt, den das Endkunden-Portal selbst benutzt. Der antwortet auch für Geräte, die die
Developer-API mit 1006 sperrt, und liefert SOC, Live-Leistungen, Energiezähler sowie die
Rohblöcke der Firmware (69 EMS-Felder, DCDC-Status, Energy-Stream). Authentifiziert wird nicht mit den API-Keys, sondern
mit dem **Session-Token des Portals** in `ECOFLOW_PORTAL_TOKEN`. Der Token läuft ab; bei HTTP 401
neu holen. Nicht ins Repo und möglichst nicht in die Shell-History.

### Live-Werte abrufen

Für `status` und `portal` braucht es **kein** API-Schlüsselpaar – nur den Portal-Token.
Einmalabruf aus einer frischen Shell, Login und Abfrage in einem Kommando:

```console
$ ECOFLOW_PORTAL_TOKEN="$(scripts/ecoflow-api.sh login vorname.nachname@example.com)" \
    scripts/ecoflow-api.sh status HC31XXXXXXXXXXXX
Password (not echoed):
logged in as user 19701254481420…
device   : Mathe (online)
SoC      : 18 %
PV       : 1045 W
grid     : 0 W (idle)
house    : 446 W
battery  : 599 W (charging)

yield    : today 1.90 | month 282.67 | year 4548.79 | total 5236.71 kWh
measured : 2026-09-17T08:39:26Z
```

`measured` ist der Zeitstempel aus dem Energy-Stream-Block der Firmware – der
**Messzeitpunkt, nicht der Abrufzeitpunkt**. Steht er bei mehreren Aufrufen still, ist die
Anzeige ein Standbild: Der Endpunkt gibt heraus, was zuletzt in die Cloud gepusht wurde,
und gepusht wird nur, solange ein Client nachfragt. Genau dafür gibt es `live`.

Die vorangestellte Zuweisung **ohne `export`** gilt nur für dieses eine Kommando: Danach
kennt die Shell die Variable nicht, der Token steht in keiner weiteren Prozessumgebung, und
es gibt nichts aufzuräumen – auch nicht nach einem Fehler oder Ctrl-C. Die Passwortabfrage
kommt aus dem Terminal (`/dev/tty`) und funktioniert deshalb auch in der
Kommandosubstitution: `login` schreibt **nur** den Token nach stdout, alles andere nach
stderr. Die E-Mail darf fehlen, dann wird sie gefragt oder aus `ECOFLOW_EMAIL` genommen.
Braucht `jq` (`brew install jq`).

Für mehrere Abfragen, ohne jedes Mal das Passwort zu tippen, den Token einmal exportieren –
dann aber am Ende **`unset`**, und mit `;` statt `&&`, sonst bleibt er gerade im Fehlerfall
stehen. `export ECOFLOW_PORTAL_TOKEN=` löscht ihn nicht, es setzt ihn leer und lässt ihn
exportiert:

```bash
export ECOFLOW_PORTAL_TOKEN="$(scripts/ecoflow-api.sh login)"
scripts/ecoflow-api.sh status HC31XXXXXXXXXXXX
scripts/ecoflow-api.sh portal HC31XXXXXXXXXXXX
unset ECOFLOW_PORTAL_TOKEN
```

Eine Subshell nimmt den Token beim Verlassen von selbst mit, das erspart das Aufräumen:

```bash
( export ECOFLOW_PORTAL_TOKEN="$(scripts/ecoflow-api.sh login)"
  scripts/ecoflow-api.sh status HC31XXXXXXXXXXXX )
```

Der Token gilt, bis er abläuft; bei HTTP 401 neu einloggen.

`status` rendert die Antwort von `portal` als Übersicht. Das Portal meldet Haus- und
Batterieleistung **negativ**, während seine eigene Oberfläche sie positiv anzeigt. `status`
gibt deshalb den Betrag aus und schreibt die Richtung dazu, statt ein Vorzeichen
durchzureichen, das man erst deuten muss. Kommt statt der Übersicht *„the response carried
no data"*, passt `ECOFLOW_PRODUCT_TYPE` nicht zum Gerät (Default `85` = PowerOcean).

Zwei Wege zum Token:

- **Aus dem Browser:** im eingeloggten Portal unter *Local Storage → `S1_JWT`*.
- **Per `login`:** fragt E-Mail und Passwort ab (Passwort ohne Echo, wahlweise aus
  `ECOFLOW_PASSWORD` – besser nicht, eine exportierte Variable überlebt die Shell, die sie
  gesetzt hat, und landet in Prozessumgebungen).

Zur Abwägung: `login` benutzt den Login-Endpunkt der Endkunden-App, der das Passwort **base64-kodiert, nicht gehasht**
überträgt – Base64 ist Kodierung, keine Verschlüsselung,
geschützt ist allein der TLS-Kanal. Der Browser-Token ist das kleinere Geheimnis und läuft
von selbst ab; das Passwort ist der bequemere Weg. Beides sind inoffizielle Schnittstellen.

### Den Kanal wachhalten: `live`

`status` liefert nur dann frische Zahlen, wenn das Gerät kurz zuvor etwas in die Cloud
geschoben hat – und das tut es offenbar nur, solange jemand nachfragt. Ohne offene App
bleibt `measured` stehen, teils stundenlang. `live` übernimmt die Rolle der App:

```bash
export ECOFLOW_PORTAL_TOKEN="$(scripts/ecoflow-api.sh login)"
export ECOFLOW_USER_ID=19701254481420        # gibt "login" fertig zum Exportieren aus
scripts/ecoflow-api.sh live HC31XXXXXXXXXXXX
```

Es holt sich über `app-cert` die Zugangsdaten des App-MQTT-Kanals, abonniert die drei
Topics des Geräts und schickt alle `ECOFLOW_LIVE_INTERVAL` Sekunden (Default 30) eine
Anfrage hinterher, damit der Strom nicht versiegt. Läuft bis Ctrl-C. Braucht `jq`,
`mosquitto_sub` und `mosquitto_pub`.

Die Ausgabe von `live` ist **roh** – Zeitstempel, Topic, Länge und Nutzlast als Hex, weil
der Push Protobuf ist und nicht JSON:

```console
2026-09-22T10:18:05+0200 /app/device/property/HC31... 74 0a480a26f6ddf1e70b98...
```

### Aktuelle Messwerte: `ecoflow-frames.py`

Zum Lesen gibt es `scripts/ecoflow-frames.py`, das die Frames auspackt. Es braucht nur
`python3`, keine weiteren Pakete:

```console
$ scripts/ecoflow-api.sh live HC31XXXXXXXXXXXX | python3 scripts/ecoflow-frames.py
08:18:00Z  PV     969 W | house    352 W | battery    530 W (charging) | grid     87 W (export) | SoC 60 %
08:19:00Z  PV     976 W | house    349 W | battery    540 W (charging) | grid     87 W (export) | SoC 61 %
```

Das ist der Weg zu aktuellen Werten – **nicht** `status`. Am Gerät gemessen (22. September
2026): Während `live` lief und Frames mit `08:19Z` ankamen, meldete `status` unverändert
`measured : 07:13:28Z`. Der Cloud-Umweg wird also auch von einem laufenden Zuhörer nicht
aufgefrischt.

Der Zeitstempel links ist der des Geräts (UTC), das Gerät meldet minütlich und schickt
jeden Frame doppelt. Wie die Frames aufgebaut sind, warum die Nutzlast XOR-verschleiert
ist und woran die Feldzuordnung hängt, steht in `api-status.md`.

### Schneller Takt: `fast`

Statt minütlich alle paar Sekunden – dafür gibt es `fast` anstelle von `live`:

```bash
scripts/ecoflow-api.sh fast HC31XXXXXXXXXXXX | python3 scripts/ecoflow-frames.py
```

Es tut alles, was `live` tut, und schaltet zusätzlich den schnellen Datenstrom ein.

**Das ist das einzige Kommando im Skript, das auf ein `.../set`-Topic schreibt** – also
auf den Weg, über den sich das Gerät auch verstellen ließe. Deshalb ist es ein eigenes
Kommando: Der Schreibzugriff passiert nie nebenbei, sondern nur, wenn man `fast` tippt.

Was dabei gesendet wird, ist **nicht geraten**. Der Befehl wurde mitgelesen, indem das
`set`-Topic abonniert und dabei die Handy-App bedient wurde (`app-mqtt`, s.u.); das Skript
gibt diese Bytes unverändert wieder und ändert nur die laufende Nummer. Er trägt keine
Parameter. Ein früherer Versuch, ihn aus Fremdquellen zusammenzusetzen, lag an vier
Stellen daneben — siehe `api-status.md`.

`ECOFLOW_FAST_INTERVAL` setzt die Wiederholrate (Default 10 s); die App selbst wiederholt
etwa alle drei Sekunden.

### Mitlesen, was die App sendet: `app-mqtt`

```bash
scripts/ecoflow-api.sh app-mqtt HC31XXXXXXXXXXXX        # Default-Topic: set
```

Abonniert eines der App-Topics unter `/app/<userId>/<SN>/thing/property/` und zeigt, was
dort ankommt. Voreingestellt ist `set` — das Topic, auf das das Skript sonst nichts
schreibt. Wer die Handy-App bedient, während das läuft, sieht ihre Befehle im Original.
Reines Abonnieren, kein Schreibzugriff.

**Vorbehalt:** Die Feldnummern gelten für den **DC Fit**. Beim PowerOcean Plus liegen
dieselben Größen auf anderen Nummern – dort lieferte das Skript plausible Zahlen an den
falschen Namen. Belegt ist die Zuordnung hier über die Energiebilanz: In jedem Frame geht
`PV = Batterie + Haus + Netz` auf zwei Nachkommastellen auf.

**Warum Python und nicht Go:** Das ist eine Zwischenstufe zum Ausprobieren, keine
Festlegung. Python3 liegt auf Mac und Raspberry Pi ohnehin bereit, und der
Protobuf-Rahmen lässt sich mit der Standardbibliothek lesen – für acht Felder kostet eine
Protobuf-Werkzeugkette mehr, als sie bringt. Bewährt sich das Auswerten im Alltag, gehört
es als Go-Werkzeug ins Repo: dann läuft es in der CI mit, ist ohne Gerät testbar wie
`internal/decode`, und die Binaries der Releases decken es mit ab.

`request` und `live` sind die **einzigen** Kommandos, die publizieren, und beide nur auf
ein `get`-Topic. `request` abonniert `.../get_reply`,
schickt die Anfrage an `.../get` und wartet `ECOFLOW_WAIT` Sekunden (Default 15). Das
Suffix ist fest verdrahtet – es gibt kein freies Topic-Argument, das `.../set`-Topic ist
von hier aus also nicht erreichbar. Braucht zusätzlich `mosquitto_pub`.

Die Ausnahme ist `fast` (s.o.): Es publiziert als einziges Kommando auf `.../set`, mit
einem mitgelesenen, parameterlosen Befehl. Dass es ein eigenes Kommando ist und nicht ein
Schalter an `live`, ist Absicht – wer Werte abfragt, soll dabei nicht unbemerkt schreiben.

Exit-Code `0` heißt `code 0` von der API, `2` jeder andere Code. **`2` mit Code 1006**
ist die interessante Antwort: Dann ist das Modell von der Developer-API ausgeschlossen (siehe `api-status.md`) und nur
der lokale Modbus-Weg bleibt. Das Gerät muss an das
eigene EcoFlow-Konto gebunden sein, sonst bleibt die Liste leer.

## Kurzüberblick

- Eine spezifische, offiziell dokumentierte REST-API für den DC Fit existiert nicht.
- Die generische EcoFlow Developer/Open API (Cloud) liefert für die PowerOcean-Familie
  Fehler 1006 "not allowed" – eine Modell-Sperrliste, kein Bug. Der **DC Fit (SN-Präfix `HC31`) ist betroffen, am Gerät
  bestätigt**: `device/list` listet ihn zwar
  mit Code 0, `device/quota/all` verweigert aber die Messwerte mit 1006.
- Praktikabler Weg: **lokales Modbus TCP** (Port 502) – muss vom Installateur über
  die EcoFlow Pro App freigeschaltet werden, Registerbelegung ist nicht offiziell
  dokumentiert, sondern community-ermittelt.
- Details siehe die beiden verlinkten Dateien.

## Offene Punkte

- Bestätigung des Register-Mappings am DC Fit. Die aktuelle Quelle behandelt ihn als
  Normalfall und kennt nur *einen* modellabhängigen Sonderfall, und der gilt dem Plus
- Welche der beiden Register-Deutungen stimmt: `modbus-registers.md` stellt die
  widersprüchlichen Adressen beider Quellen gegenüber, jede Zeile ist ein Einzeltest
  am Gerät (z.B. System-SOC auf 40527 vs. 42082)
- Bestätigung am Gerät, dass die Freischaltung wirklich über *Control Mode →
  „Modbus control"* in der Pro App läuft (Checkliste in `api-status.md`)
- Welche Werte `product_category`/`product_number` (40002/40003) am DC Fit liefern –
  die Referenz-Integration kennt sie nicht
- Ob der Weckruf auf `.../get` überhaupt nötig ist oder das Abo allein genügt –
  gemessen wurde bisher nur mit laufendem Weckruf
- Was die übrigen Frame-Kennungen tragen (`cmd_id` 1, 108–111, 136); der
  Energiestrom auf 34 ist ausgewertet, der Rest nicht
- Ob sich der schnelle ~3-Sekunden-Takt lohnt, den die App über das
  `.../set`-Topic freischaltet – bewusst nicht ausprobiert (kein Schreibpfad)

## Arbeiten an diesem Repo

Jede Änderung – Go-Code wie Notizen – läuft über einen kurzlebigen Feature-Branch
und einen PR nach `main`. `main` bleibt dadurch jederzeit auslieferbar, und die CI
prüft *bevor* etwas ankommt, nicht danach. Das ist wichtig, weil ein Release ein Tag
auf `main` ist (s.u.).

**1. Sauber starten.** Du zweigst gleich von `main` ab; ist der Stand alt, baust du
auf Veraltetem auf und handelst dir beim Merge Konflikte ein.

```bash
git checkout main && git pull
```

**2. Branch anlegen.** Kurz, kleingeschrieben, nach dem *Ziel* der Änderung benannt.

```bash
git checkout -b register-map-dcfit
```

**3. Ändern und lokal prüfen.** Das sind exakt die Prüfungen aus
`.github/workflows/ci.yml` – laufen sie hier durch, wird die CI später kaum rot.

```bash
go build ./... && go vet ./... && go test ./...
gofmt -l ./cmd ./internal      # keine Ausgabe = in Ordnung
```

Bei reinen Doku-Änderungen entfällt das. Dafür gilt: `README.md`, `api-status.md` und
`modbus-registers.md` überschneiden sich absichtlich – ändert sich eine Aussage, die
anderen Stellen und die „Offene Punkte"-Listen mitziehen.

**4. Committen.** Betreffzeile im Imperativ, die das *Ergebnis* nennt; darunter ein
Absatz zum **Warum**. Das Was steht schon im Diff. `git add -p` zeigt jeden Block
einzeln, damit kein vergessener Debug-Ausdruck mitrutscht.

```bash
git add -p && git commit
```

**5. Pushen und PR öffnen.** `--fill` übernimmt Titel und Text aus dem Commit.

```bash
git push -u origin register-map-dcfit
gh pr create --base main --fill
```

**6. CI abwarten.** Sie läuft auf einer frischen Maschine und findet damit die
vergessene Datei und die Abhängigkeit, die es nur lokal gibt. Rot heißt: nachbessern,
erneut committen, pushen – der PR aktualisiert sich von allein.

```bash
gh pr checks --watch
```

**7. Mergen.** Squash macht aus den Zwischenschritten einen lesbaren Commit auf
`main`. Den entfernten Branch löscht GitHub selbst.

```bash
gh pr merge --squash --delete-branch
git checkout main && git pull
```

**Release.** Wenn der Stand auf `main` veröffentlicht werden soll:

```bash
git checkout main && git pull
git tag v0.3.0 && git push origin v0.3.0
```

Nur auf `main` taggen – `release.yml` baut daraus die Binaries und stempelt die
Versionsnummer über `-X main.version` ein. Ein Tag auf einem anderen Branch erzeugte
ein Release, das auf einen Stand zeigt, den es in `main` nie gab.

> Beim Squash-Merge entsteht auf `main` ein *neuer* Commit; der Commit des Branches
> wird nie ein Vorfahre von `main`. `git branch --merged` meldet solche Branches
> deshalb dauerhaft als „nicht gemerged" – verlässlich ist `gh pr list`.

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
