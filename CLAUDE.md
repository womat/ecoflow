# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Was dieses Repo ist

Recherche-Notizen und die Werkzeuge, mit denen sie entstanden sind:

1. **Recherche-Notizen** (Markdown, Deutsch) zu den Integrationsmöglichkeiten des
   **EcoFlow PowerOcean DC Fit** – Cloud-Wege vs. lokales Modbus TCP, Register-Map.
2. **`scripts/ecoflow-api.sh`** und **`scripts/ecoflow-frames.py`** – das Messwerkzeug am
   Cloud-Kanal und der Frame-Dekoder. Damit sind praktisch alle Befunde dieser Notizen
   entstanden.
3. **`cmd/ecoflowd`** – der Dienst für den Dauerbetrieb auf demselben Kanal: liest die
   Messwerte und publiziert sie auf einen lokalen MQTT-Broker.
4. **`modbusread`** – ein Go-CLI, mit dem die Angaben aus diesen Notizen am Gerät
   überprüft werden. Bewusst **universell**: es enthält kein EcoFlow-Wissen, keine
   eingebaute Register-Map und keine gerätespezifischen Meldungen. Spricht Modbus TCP
   und Modbus RTU (seriell); der Transport ergibt sich aus dem Ziel-Argument
   (`cmd/modbusread/target.go`), nicht aus einem Flag.

## Kommandos

```
go build ./...                 # baut cmd/modbusread und cmd/ecoflowd
go test ./...                  # alles, läuft ohne Hardware
go test -run TestParseAddr ./internal/decode/   # einzelner Test
go vet ./...
gofmt -l ./cmd ./internal      # keine Ausgabe = in Ordnung; die CI scheitert daran
scripts/ecoflow-api.sh selftest # Signatur und Stream-Schalter-Frame, ohne Netz
```

Die Integrationstests starten den Modbus-Server aus `github.com/simonvetter/modbus`
auf einem freien Port und lesen dagegen – kein Gerät nötig. `internal/frames` und
`cmd/ecoflowd` testen gegen anonymisierte Mitschnitte vom echten Gerät in
`internal/frames/testdata/`; die `.golden`-Dateien dort sind die Ausgabe von
`scripts/ecoflow-frames.py` über dieselben Mitschnitte und werden von
`cmd/ecoflowd/ecoflowd_test.go` gelesen — sie halten die Go- und die Python-Fassung
zeilengleich. **Beide Richtungen sind geprüft:** `go test` misst die Go-Fassung an den
`.golden`-Dateien, ein CI-Schritt erzeugt sie mit dem Python-Decoder neu und vergleicht.

`damaged.txt` ist kein Mitschnitt, sondern von Hand gebaut: vier absichtlich kaputte
Frames, an denen der Python-Decoder früher mitten im Strom mit einem Traceback endete.
Seine `.golden`-Datei ist **leer** und sagt damit zweierlei — keine der beiden Fassungen
meldet daraus einen Messwert, und keine bleibt daran stehen. Beim Hinzufügen neuer
Fehlerfälle gehört der Frame dorthin.

Wer eine der beiden Fassungen ändert, erzeugt die `.golden`-Dateien neu:

```
python3 scripts/ecoflow-frames.py < internal/frames/testdata/fast.txt \
  > internal/frames/testdata/fast.golden
```

## Branches & Releases

Ein Dauerbranch: **`main`**. Arbeit läuft in kurzlebigen Feature-Branches, die per PR
nach `main` gehen — CI (`gofmt`, `vet`, `build`, `test -race`) muss grün sein. Kein
`develop`: `go install …@latest` löst auf den neuesten Semver-Tag auf, nicht auf einen
Branch, ein zweiter Dauerbranch würde also nur ein Merge-Ritual ohne Gegenwert erzeugen.

Ein Release ist ein Tag `vX.Y.Z` auf `main`; der Release-Workflow baut daraus die
Binaries und stempelt die Tag-Nummer über `-X main.version` ein. Nicht auf anderen
Branches taggen — sonst zeigt ein Release auf einen Stand, der nie in `main` war.

## Sprache

**`README.md` auf Englisch, die Recherche-Notizen auf Deutsch, Code und
Programmausgaben auf Englisch.**

- `README.md` ist englisch, weil `ecoflowd` und `modbusread` auch ohne Deutschkenntnisse
  einsetzbar sein sollen – die README ist der Einstieg für genau diese Leute. Änderungen
  daran ebenfalls auf Englisch.
- `api-status.md`, `modbus-registers.md`, `mqtt-ausgabe.md` und diese Datei bleiben
  deutsch: Das ist die Recherche, sie ist umfangreich und entstand auf Deutsch; eine
  Übersetzung wäre eine zweite Fassung, die dem Stand hinterherläuft. Die README weist
  darauf hin. Neue oder geänderte Abschnitte dort auf Deutsch (technische Bezeichner wie
  `feed_in_power_max` bleiben englisch).
- Go-Kommentare, `--help`-Text und Fehlermeldungen auf Englisch, weil die Werkzeuge
  universell einsetzbar sein sollen.

## Struktur & Zusammenhang der Dateien

- `cmd/modbusread/` – CLI: Flags, Lesen mit Chunking/Fehlerisolierung, Ausgabe, Polling
- `cmd/ecoflowd/` – Dienst für den Dauerbetrieb am Cloud-Kanal: Flags,
  Verbindungsschleife mit Rücknahme, und die Ausgabeseite – er publiziert die Messwerte
  auf einen lokalen MQTT-Broker, als zwei JSON-Telegramme (`<topic>/state`,
  `<topic>/energy`) mit Seriennummer und Messzeit im Payload; kein Retain, kein
  Verfügbarkeits-Topic, kein Heartbeat. Ins Telegramm kommt nur, was geklärt ist —
  `dcdc` fehlt deshalb bewusst. Nichts davon liegt auf der Platte; die Sitzung lebt im Prozess. Anders als
  `modbusread` bewusst gerätespezifisch; das Wissen dazu liegt in `internal/frames` und
  `internal/ecoflow`
- `internal/decode/` – reine Funktionen über `[]uint16` (Typen, Word-/Byte-Order,
  Adress-Parsing). Hier liegt die Logik, die bei Fehlern *falsche Zahlen* statt
  Abstürze liefert – deshalb netzwerkfrei und vollständig testbar gehalten
- `internal/frames/` – reine Funktionen über `[]byte`: die Protobuf-Frames des
  App-MQTT-Kanals (Rahmen, XOR-Verschleierung, Energieberichte, Stundenhistorie,
  Komponentenliste, Bau des Stream-Schalters). Aus demselben Grund netzwerkfrei wie
  `internal/decode`. **Hier wohnt das EcoFlow-Wissen**, damit `modbusread` universell
  bleibt. Die Tests laufen gegen anonymisierte Mitschnitte vom echten Gerät in
  `testdata/` und prüfen die beiden Rechenidentitäten, die die Feldzuordnung belegt
  haben. Die `.golden`-Dateien liegen zwar hier, gelesen werden sie aber von
  `cmd/ecoflowd` – dort wohnt die Formatierung, die sie festhalten
- `internal/ecoflow/` – der Weg *hinein*: Login, Certification, Client-ID, Topics.
  Gegenstück zu `internal/frames`, das nur deutet, was schon da ist; die beiden kennen
  einander nicht. Getestet gegen einen `httptest`-Server, nicht gegen die echte Cloud
- `scripts/ecoflow-api.sh` – das Messwerkzeug am Cloud-Kanal und die Fassung, mit der
  alle Befunde entstanden sind: Developer-API, Portal-REST, App-MQTT, Mitlesen des
  `set`-Topics. Bleibt neben `ecoflowd` bestehen – für die nächste unbekannte Kennung
  greift man wieder dazu
- `scripts/ecoflow-frames.py` – packt die Frames aus, die `ecoflow-api.sh live|fast`
  liefert; `--hours` und `--modules` können mehr als der Go-Dienst. Erzeugt die
  `.golden`-Dateien
- `contrib/` – Betriebsbeiwerk, das nicht gebaut wird: die systemd-Vorlage für `ecoflowd`
- `.github/workflows/` – `ci.yml` (gofmt, vet, build, test -race) und `release.yml`
  (beide Binaries für sechs Plattformen, Tag `vX.Y.Z` auf `main`)
- `ecoflow-open-demo/` – EcoFlows offizieller Java-Demo-Client, nur zum Nachlesen
  heruntergeladen. Per `.gitignore` bewusst **nicht** versioniert; nicht „aufräumen"
- `README.md` – Einstieg (englisch), Disclaimer, Summary, Quellenliste, Open points
- `api-status.md` – die *Entscheidungsebene*: Cloud-REST (EcoFlow Developer/Open API,
  HMAC-signiert, liefert für PowerOcean oft Fehler 1006) vs. lokales **Modbus TCP**
  (Port 502, Freischaltung nur durch Installateur via EcoFlow **Pro App**)
- `mqtt-ausgabe.md` – die *Ausgabeseite*: wie `ecoflowd` auf den lokalen Broker
  publiziert und warum so — zwei JSON-Telegramme statt, wie bis v0.4.x, ein Topic je
  Wert. Enthält die Begründungen (Messzeit im Payload statt Heartbeat und Last Will,
  camelCase, fehlendes Feld = 0 wegen proto3, `dcdc` erst nach Klärung) und die
  Messungen, auf denen sie stehen
- `modbus-registers.md` – die *Detailebene*: Register-Map, Encoding-Konventionen,
  Python-Decoding-Snippets (pymodbus), bekannte Lücken

Die vier Markdown-Dateien überschneiden sich bewusst: README verlinkt alle drei,
`api-status.md` verweist für das Mapping auf `modbus-registers.md` und für die
Ausgabeseite auf `mqtt-ausgabe.md`. Bei inhaltlichen Änderungen (z.B. Fehler 1006 gelöst,
Freischaltpfad gefunden) **alle betroffenen Stellen mitziehen**, inkl. der „Offene
Fragen“-Checkliste in `api-status.md` und „Open points“ in der README – dort auf
Englisch.

Dasselbe gilt seit dem Cloud-Kanal für die Werkzeuge: Wer an `scripts/ecoflow-api.sh`,
`scripts/ecoflow-frames.py` oder `cmd/ecoflowd` etwas ändert, zieht die zugehörigen
README-Abschnitte und den `--help`-Text mit. **Das ist mehrfach unterblieben** — eine
Prüfung fand rund 60 Stellen, an denen die Doku beschrieb, was einmal galt: darunter
die Zusage, das Skript könne „nichts am Gerät ändern", und eine Erklärung fürs
Einfrieren des REST-Zeitstempels, die die eigene spätere Messung widerlegt hatte.
Beim Nachziehen zählt der Code, nicht die ältere Prosa.

## Konventionen im Code

- **Read-only ist eine harte Eigenschaft, kein Default:** `modbusread` ruft keine
  `Write*`-Methode der Library auf. Das Mapping ist unbestätigt (siehe unten), ein
  Tool ohne Schreibpfad kann nicht versehentlich schreiben. Nicht aufweichen.
- **Der eine Schreibpfad im Repo, und wie er eingehegt ist:** Auf dem App-MQTT-Kanal
  gibt es genau einen – den `EnergyStreamSwitch` auf `.../set`, der den schnellen
  Datenstrom einschaltet. Er ist an vier Bedingungen gebunden, und die sind zusammen
  die Regel: Er hängt an einem **eigenen Kommando** (`ecoflow-api.sh fast`) bzw. einem
  **eigenen Flag** (`ecoflowd --fast`), passiert also nie als Nebenwirkung des Lesens;
  er trägt **keine Parameter**; seine Bytes sind am Draht **mitgelesen** und werden
  unverändert wiedergegeben, nur die Sequenznummer variiert; und er ist als Einziger
  dort. Ein zweiter Schreibpfad wäre eine eigene Entscheidung, keine Erweiterung
  dieser. Warum das so streng ist: Ein aus Fremdquellen zusammengesetzter Versuch lag
  an vier Stellen daneben – auf einem Topic, über das sich das Gerät verstellen lässt.
- **Adressen werden nie umgerechnet** – was getippt wird, geht so auf den Draht
  (0-based). Viele Quellen dokumentieren 1-based; das Umrechnen bleibt bewusst beim
  Menschen, damit das Tool keine Annahme versteckt.
- **Word-/Byte-Order rechnet das Tool selbst**, der Client läuft fest auf
  `BIG_ENDIAN, HIGH_WORD_FIRST` und liest nur `ReadRegisters`. So sind die Rohwords
  immer für die Ausgabe da und die Dekodierung bleibt pur testbar.
- **Flags, die nicht wirken können, werden abgelehnt statt ignoriert** – die
  Serial-Parameter an einem TCP-Ziel sind ein Fehler. Eine stillschweigend wirkungslose
  Baudrate schickt Menschen auf Fehlersuche an der Hardware.
- **Rohwords stehen immer in der Ausgabe**, auch wenn ein Wert dekodiert wurde – beim
  Reverse-Engineering ist der Rohwert wichtiger als die Deutung.
- **Adress-Parsing nutzt bewusst nicht `strconv.ParseUint(s, 0, …)`** (Basis 0 läse
  `042` als Oktal 34).

## Inhaltliche Konventionen (Doku)

- **Herkunft kennzeichnen:** Praktisch alles hier ist Community-Reverse-Engineering,
  nicht offizielle EcoFlow-Doku. Neue Behauptungen mit Quell-URL belegen (die
  Quellenlisten am Dateiende pflegen) und bestätigtes Wissen von Vermutungen
  sprachlich trennen („vermutlich“, „nicht bestätigt“).
- **Plus vs. DC Fit:** Die Modellfrage ist offen, **die Richtung hat sich aber gedreht**:
  Die aktuelle Quelle behandelt den DC Fit als Normalfall und kennt genau *einen*
  modellabhängigen Sonderfall, und der gilt dem Plus. Die frühere Sorge, das Mapping sei
  „am Plus ermittelt und für den DC Fit fraglich", ist damit überholt — bestätigt ist
  deswegen nichts, nur der Verdacht ist ein anderer. Den Vorbehalt bei Register-Aussagen
  nicht wegkürzen, aber auch nicht in der alten Fassung konservieren; die aktuelle steht
  in `modbus-registers.md`.

  **Bei den Protobuf-Feldnummern des Cloud-Kanals gilt er dagegen scharf und in der
  ursprünglichen Richtung:** `cmd_func 96 / cmd_id 33` belegt beim DC Fit andere Felder
  als beim Plus, gemessen. Wer dort die falsche Tabelle nimmt, bekommt plausible Zahlen
  an falschen Namen.
- **Register-Tabellen:** Die Adressen stehen so da, wie sie auf den Draht gehen – die
  Referenz-Integration übergibt die 4xxxx-Zahlen unverändert an pymodbus, `modbusread`
  ebenso. **Ob sie 1-based oder 0-based gemeint sind, ist ungeklärt**; die frühere
  Angabe „1-based" in diesen Notizen ist inzwischen stark in Zweifel gezogen und erst am
  Gerät zu entscheiden. Genau deshalb nicht umrechnen und nichts dazuschreiben, was nur
  eine der beiden Deutungen stützt. Floats belegen 2 Register, 32-bit IEEE754
  **word-swapped** (High-Word im zweiten Register). Neue Einträge im bestehenden
  Tabellenformat (Register | Einheit | Scale | Beschreibung) mit explizitem Scale-Faktor
  ergänzen.
- **Schreibregister:** Die Trennung in „explizit beschreibbar“ / „bekannt, nicht
  exponiert“ / „unbekannt“ beibehalten und die Warnung vor Schreibzugriffen
  (Leistungslimits 40554/40556) nicht entfernen.
