# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Was dieses Repo ist

Zwei Dinge, die zusammengehören:

1. **Recherche-Notizen** (Markdown, Deutsch) zu den Integrationsmöglichkeiten des
   **EcoFlow PowerOcean DC Fit** – Cloud-API vs. lokales Modbus TCP, Register-Map.
2. **`modbusread`** – ein Go-CLI, mit dem die Angaben aus diesen Notizen am Gerät
   überprüft werden. Bewusst **universell**: es enthält kein EcoFlow-Wissen, keine
   eingebaute Register-Map und keine gerätespezifischen Meldungen.

## Kommandos

```
go build ./...                 # baut cmd/modbusread
go test ./...                  # alles, läuft ohne Hardware
go test -run TestParseAddr ./internal/decode/   # einzelner Test
go vet ./...
```

Die Integrationstests starten den Modbus-Server aus `github.com/simonvetter/modbus`
auf einem freien Port und lesen dagegen – kein Gerät nötig.

## Branches & Releases

Ein Dauerbranch: **`main`**. Arbeit läuft in kurzlebigen Feature-Branches, die per PR
nach `main` gehen — CI (`gofmt`, `vet`, `build`, `test -race`) muss grün sein. Kein
`develop`: `go install …@latest` löst auf den neuesten Semver-Tag auf, nicht auf einen
Branch, ein zweiter Dauerbranch würde also nur ein Merge-Ritual ohne Gegenwert erzeugen.

Ein Release ist ein Tag `vX.Y.Z` auf `main`; der Release-Workflow baut daraus die
Binaries und stempelt die Tag-Nummer über `-X main.version` ein. Nicht auf anderen
Branches taggen — sonst zeigt ein Release auf einen Stand, der nie in `main` war.

## Sprache

**Doku auf Deutsch, Code und Programmausgaben auf Englisch.** Neue oder geänderte
Markdown-Abschnitte ebenfalls auf Deutsch (technische Bezeichner wie
`feed_in_power_max` bleiben englisch); Go-Kommentare, `--help`-Text und
Fehlermeldungen auf Englisch, weil das Tool universell einsetzbar sein soll.

## Struktur & Zusammenhang der Dateien

- `cmd/modbusread/` – CLI: Flags, Lesen mit Chunking/Fehlerisolierung, Ausgabe, Polling
- `internal/decode/` – reine Funktionen über `[]uint16` (Typen, Word-/Byte-Order,
  Adress-Parsing). Hier liegt die Logik, die bei Fehlern *falsche Zahlen* statt
  Abstürze liefert – deshalb netzwerkfrei und vollständig testbar gehalten
- `README.md` – Einstieg, Disclaimer, Kurzüberblick, Quellenliste, offene Punkte
- `api-status.md` – die *Entscheidungsebene*: Cloud-REST (EcoFlow Developer/Open API,
  HMAC-signiert, liefert für PowerOcean oft Fehler 1006) vs. lokales **Modbus TCP**
  (Port 502, Freischaltung nur durch Installateur via EcoFlow **Pro App**)
- `modbus-registers.md` – die *Detailebene*: Register-Map, Encoding-Konventionen,
  Python-Decoding-Snippets (pymodbus), bekannte Lücken

Die drei Dateien überschneiden sich bewusst: README verlinkt beide, `api-status.md`
verweist für das Mapping auf `modbus-registers.md`. Bei inhaltlichen Änderungen
(z.B. Fehler 1006 gelöst, Freischaltpfad gefunden) **alle betroffenen Stellen
mitziehen**, inkl. der „Offene Fragen“/„Offene Punkte“-Checklisten in README und
`api-status.md`.

## Konventionen im Code

- **Read-only ist eine harte Eigenschaft, kein Default:** `modbusread` ruft keine
  `Write*`-Methode der Library auf. Das Mapping ist unbestätigt (siehe unten), ein
  Tool ohne Schreibpfad kann nicht versehentlich schreiben. Nicht aufweichen.
- **Adressen werden nie umgerechnet** – was getippt wird, geht so auf den Draht
  (0-based). Viele Quellen dokumentieren 1-based; das Umrechnen bleibt bewusst beim
  Menschen, damit das Tool keine Annahme versteckt.
- **Word-/Byte-Order rechnet das Tool selbst**, der Client läuft fest auf
  `BIG_ENDIAN, HIGH_WORD_FIRST` und liest nur `ReadRegisters`. So sind die Rohwords
  immer für die Ausgabe da und die Dekodierung bleibt pur testbar.
- **Rohwords stehen immer in der Ausgabe**, auch wenn ein Wert dekodiert wurde – beim
  Reverse-Engineering ist der Rohwert wichtiger als die Deutung.
- **Adress-Parsing nutzt bewusst nicht `strconv.ParseUint(s, 0, …)`** (Basis 0 läse
  `042` als Oktal 34).

## Inhaltliche Konventionen (Doku)

- **Herkunft kennzeichnen:** Praktisch alles hier ist Community-Reverse-Engineering,
  nicht offizielle EcoFlow-Doku. Neue Behauptungen mit Quell-URL belegen (die
  Quellenlisten am Dateiende pflegen) und bestätigtes Wissen von Vermutungen
  sprachlich trennen („vermutlich“, „nicht bestätigt“).
- **Plus vs. DC Fit:** Das Register-Mapping wurde am PowerOcean **Plus** ermittelt.
  Ob es 1:1 für den **DC Fit** gilt, ist offen (Firmware kennt `InverterModel`-
  spezifische `address_overrides`). Diesen Vorbehalt bei Register-Aussagen nicht
  wegkürzen.
- **Register-Tabellen:** Adressen sind 1-based; Floats belegen 2 Register,
  32-bit IEEE754 **word-swapped** (High-Word im zweiten Register). Neue Einträge
  im bestehenden Tabellenformat (Register | Einheit | Scale | Beschreibung) mit
  explizitem Scale-Faktor ergänzen.
- **Schreibregister:** Die Trennung in „explizit beschreibbar“ / „bekannt, nicht
  exponiert“ / „unbekannt“ beibehalten und die Warnung vor Schreibzugriffen
  (Leistungslimits 40554/40556) nicht entfernen.
