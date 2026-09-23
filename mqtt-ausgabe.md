# Das Ausgabeformat von `ecoflowd`

Wie `cmd/ecoflowd` seine Messwerte auf den lokalen Broker legt, warum es heute so ist –
und warum ein Sammeltelegramm die bessere Form wäre.

> **Herkunft:** Die Angaben über `ecoflowd` stammen aus dem Code dieses Repos, die über
> das Vergleichsmuster aus einem Mitschnitt am Broker einer bestehenden
> Hausautomatisierung (23.09.2026, rund fünf Minuten). Die Einschätzung am Ende ist ein
> Urteil, kein Messergebnis: **gebaut und erprobt ist das empfohlene Format nicht.**

## Kurzfassung

`ecoflowd` publiziert **ein Topic je Wert mit blanker Zahl**. Die verbreitetere Bauform –
und die bessere für diese Datenquelle – ist **ein JSON-Telegramm je Messung**, mit
Seriennummer und Messzeit im Payload.

Der Grund ist nicht Geschmack: `internal/frames.Energy` ist bereits ein Sammelobjekt –
sechs Messwerte und eine Messzeit aus *einem* Frame. Die Ausgabeseite nimmt es
auseinander und baut anschließend drei Mechanismen, um den Verlust auszugleichen
(Heartbeat, Verfügbarkeits-Topic, Änderungserkennung). Alle drei entfallen, sobald die
Messzeit im Payload steht.

**Geändert ist bislang nichts.** Diese Datei hält die Analyse fest, damit die Entscheidung
nicht beim nächsten Mal von vorn geführt werden muss.

## 1. Was `ecoflowd` heute sendet

Ein Topic je Wert, Nutzlast eine blanke Zahl als Text, kein JSON
(`cmd/ecoflowd/publish.go:224-230`):

```
ecoflow/HC31XXXXXXXXXXXX/pv        0
ecoflow/HC31XXXXXXXXXXXX/house     -293
ecoflow/HC31XXXXXXXXXXXX/battery   -293
ecoflow/HC31XXXXXXXXXXXX/grid      0
ecoflow/HC31XXXXXXXXXXXX/dcdc      -163
ecoflow/HC31XXXXXXXXXXXX/soc       71
ecoflow/HC31XXXXXXXXXXXX/measured  2026-09-23T20:39:00Z
ecoflow/HC31XXXXXXXXXXXX/status    online        ← retained, Last Will
```

Dazu die Tagessummen unter `/energy/` (`pv`, `house`, `battery_in`, `battery_out`,
`grid_in`, `grid_out`). Messwerte QoS 0 und nicht retained, die Verfügbarkeit retained.
Publiziert wird bei Änderung, dazu einmal pro Minute auch unverändert
(`publish.go:34-39`, `:277-284`).

## 2. Das Vergleichsmuster

Eine gewachsene Hausautomatisierung am selben Broker sendet **ein flaches JSON-Objekt je
Gerät**, alle fünf Sekunden, unabhängig von Änderungen:

```
myhome/inverter/summary   {"E":39836000,"P":240,"PC":242.39,"PR":-34,"F":49.98,…}
myhome/heatpump/summary   {"Timestamp":"2026-09-23T20:36:23.168Z","E":26440945,"P":0,…}
myhome/wallbox/summary    {"timeStamp":"…","counter":34274662,"counterUnit":"Wh",
                           "gauge":0.98,"gaugeUnit":"W"}
```

Kein Verfügbarkeits-Topic, kein Last Will; die Empfänger prüfen den Zeitstempel selbst.

Bemerkenswert ist der Wetter-Zweig desselben Brokers: Er führt **beide** Formen parallel –
ein `summary` mit dem vollständigen JSON *und* aufgefächerte Einzelwerte als blanke Zahlen
(`…/temperature` → `9.03`). Die Formen schließen einander also nicht aus; die Frage ist,
welche der Hauptzugang ist.

## 3. Die drei Annahmen hinter dem heutigen Format – und was aus ihnen wurde

### 3.1 „evcc und Home Assistant brauchen je Topic einen Skalar" – **widerlegt**

evcc kennt im MQTT-Plugin einen `jq:`-Ausdruck, Home Assistant `value_template:
"{{ value_json.pv }}"`. JSON kostet dort eine Konfigurationszeile, nicht mehr. Das war das
tragende Argument für die Auffächerung; es trägt nicht.

### 3.2 „Ein Heartbeat ist nötig" – **entfällt mit dem Zeitstempel**

Die Begründung steht wörtlich im Code (`publish.go:34-39`): *„Without a heartbeat a
consumer cannot tell 'unchanged' from 'gone'."* Genau das leistet eine Messzeit im Payload,
und zwar besser: Sie ist die Uhr des **Geräts**, nicht die Sendezeit des Dienstes.

Hinzu kommt: Bei einem Sammelobjekt wäre die Änderungserkennung ohnehin wirkungslos, weil
sich die Messzeit bei jeder Messung ändert und damit das Objekt immer.

### 3.3 „Ein retained Messwert überlebt das, was er beschreibt" – **nur ohne Zeitstempel**

Die Begründung gegen `retain` (README, „Die Messwerte sind nicht retained…“) trifft auf
eine blanke Zahl zu: Nach
einem Ausfall liest ein Verbraucher den letzten Stand für immer weiter. Trägt die Nachricht
ihre Messzeit, sieht ein neu verbundener Empfänger den Stand **und** sein Alter.

Die Empfehlung bleibt trotzdem `retain: false` – aus Gleichlauf mit dem Vergleichsmuster,
nicht mehr aus dem alten Grund.

## 4. Empfehlung: ein Telegramm je Messung

```
--topic myhome/ecoflow   →   myhome/ecoflow/state
                             myhome/ecoflow/energy
```

**Momentanwerte**, ein Telegramm je eingegangener Messung:

```json
{"sn":"HC31XXXXXXXXXXXX","measured":"2026-09-23T20:39:00Z",
 "pv":0,"house":-293,"battery":-293,"grid":0,"dcdc":-163,"soc":71}
```

**Tagessummen**, eigenes Telegramm:

```json
{"sn":"HC31XXXXXXXXXXXX","date":"2026-09-23",
 "pv":…,"house":10887,"battery_in":…,"battery_out":4106,"grid_in":…,"grid_out":…}
```

Watt und Wattstunden wie gemessen, Vorzeichen wie das Gerät sie liefert – positives `grid`
ist Einspeisung, positives `battery` ist Laden. Das bleibt so: Das Umrechnen gehört zum
Menschen, dieselbe Regel wie bei den Adressen in `modbusread`.

### Warum die Werte zusammengehören

Sie wurden zusammen gemessen. `internal/frames.Energy` trägt sie als ein Struct aus einem
Frame; `publish.go:224-230` zerlegt es in sechs Nachrichten und legt die Messzeit als
siebte daneben. Ein Verbraucher kann heute `pv` von 09:13 mit `soc` von 09:12 kombinieren,
und nichts sagt ihm das.

### Warum die Tagessummen ein eigenes Telegramm brauchen

Sie stammen aus einem anderen Frame (254/32) und werden erst weitergegeben, wenn alle sechs
Flussanteile mit gleichem Zeitstempel beisammen sind (`cmd/ecoflowd/serve.go:224-228`).
In dasselbe Objekt gepackt, entstünde genau die Lüge, die ein Sammeltelegramm vermeiden
soll: Felder verschiedenen Alters unter einem Zeitstempel.

Dass sie eigenständig behandelt gehören, zeigt auch der Betrieb: In 150 Sekunden Mitschnitt
kamen **zwei** der sechs Summen (`house` 10887 Wh, `battery_out` 4106 Wh) – die anderen
vier ändern sich nachts nicht und schweigen deshalb. Wer den Tagesstand will, muss heute
sechs Topics einzeln einsammeln und darauf bauen, jedes irgendwann gesehen zu haben.

### Warum die Seriennummer in den Payload gehört

Heute steht sie im Topic, begründet damit, sie sei „der einzige Name, den ein Gerät hat"
(README, Abschnitt „An den lokalen Broker“). Das trägt nicht mehr, sobald sie im Payload
steht – dort ist sie ja,
und zwar an der Stelle, die eine Weiterleitung überlebt. Wer die Nachricht nach InfluxDB
oder in eine Warteschlange reicht, verliert das Topic und mit ihm die Zuordnung.

Dazu kommt: Ein erzwungenes Seriennummer-Segment ist ein Fremdkörper in jedem gewachsenen
Namensschema. Ein frei wählbares Topic fügt sich ein.

**Was man dafür aufgibt:** `ecoflow/+/soc` über mehrere Geräte hinweg. Ersatz ist
`<prefix>/+/state` mit einem Filter auf `sn` – eine Zeile mehr beim Empfänger.

### Kein Verfügbarkeits-Topic

Der Beleg dagegen kommt aus dem Betrieb: Der einzige bekannte Konsument hat einen eigenen
Alterswächter bauen müssen, weil er dem retained `status` nicht trauen konnte. Hängt
`ecoflowd`, ohne die Verbindung zu verlieren, bleibt `online` stehen und der Last Will
feuert nicht (siehe „Offene Punkte"). Ein Signal, dem sein Empfänger nicht traut, trägt
nichts.

Home Assistant kommt mit `expire_after` ohne `availability_topic` aus. Was ein Last Will
kann und eine Messzeit nicht – sofort melden statt nach Ablauf einer Frist –, gewinnt
niemand, der ohnehin einen Alterswächter betreibt.

### Was dabei zusammenfällt

Mit der Messzeit im Payload entfallen `last`-Map, Heartbeat-Takt, Stale-Wächter,
`setOnlineLocked`, `announce`, `check`, der Last Will und beide 30-Sekunden-Ticker. Übrig
bleibt: Frame kommt, Objekt bauen, publizieren.

**Der Kernsatz dieser Analyse:** Die halbe Komplexität der Ausgabeseite existiert nur,
weil die Messzeit nicht im Payload steht.

## 5. Was das neue Format kostet

Nicht verschweigen:

- **Keine Sofortmeldung bei Absturz.** Ohne Last Will merkt ein Empfänger den Tod des
  Dienstes erst nach seiner eigenen Frist statt in Sekunden.
- **Kein selektives Abonnement.** Wer nur den Ladestand will, bekommt alles. Bei acht
  Feldern belanglos, bei einem schwachen Client nicht.
- **JSON-Parsen bei jedem Konsumenten.** Eine Zeile `jq` bzw. `value_template` – überall.

## 6. Was ein Umbau kostet

Die bekannten Verbraucher hängen an `ecoflow/<SN>/+`, und die README nennt die Einzeltopics
in den Beispielen für evcc und Home Assistant. **Additiv** wäre der Schritt verträglich,
ein **Wegfall** der Einzeltopics ein Major-Schritt nach der Versionsregel (README,
Abschnitt „Arbeiten an diesem Repo“).

## 7. Offene Punkte

- **Kommen unvollständige Frames real vor?** `internal/frames/energy.go:57-77` prüft **nur
  PV** darauf, ob das Feld wirklich da war – mit der Begründung, ein als lauter Nullen
  gelesener Frame sehe nachts wie eine echte Messung aus. Für House, Battery, Grid, DCDC
  und SoC liefert `float32At()` stillschweigend eine 0, wenn das Feld fehlt.

  Das ist **kein Formatproblem**, es besteht heute schon: `publish.go:224-230` sendet diese
  0 als blanke Zahl, und die ist von einer gemessenen 0 nicht zu unterscheiden. Nur kann
  das heutige Format den Unterschied gar nicht ausdrücken – Schweigen heißt dort bereits
  „unverändert". JSON kann ein Feld weglassen.

  Zu messen mit `scripts/ecoflow-frames.py` über einen längeren Mitschnitt: bei welchen
  Feldern fehlt tatsächlich etwas? Bis dahin ist „fehlende Felder weglassen statt als 0
  senden" eine begründete Absicht und **kein belegter Bedarf**.

- **Das Verfügbarkeitssignal kann einfrieren.** Hängt `ecoflowd`, ohne die MQTT-Verbindung
  zu verlieren, bleibt das retained `online` stehen und der Last Will feuert nicht. Der
  Stale-Wächter (`publish.go:32`, drei Minuten) greift nur, solange der Prozess noch läuft.
  Gefunden beim Bau eines Verbrauchers, am eigenen Code nicht nachgestellt.

## Quellen

- https://docs.evcc.io/en/docs/devices/plugins – `jq` im MQTT-Plugin
- https://github.com/evcc-io/evcc/pull/943 – Einbau des `jq`-Parsens
- https://www.home-assistant.io/integrations/sensor.mqtt/ – `value_template`,
  `expire_after`, `availability_topic`
- https://www.zigbee2mqtt.io/guide/usage/mqtt_topics_and_messages.html – ein JSON-Objekt
  je Gerät
- https://tasmota.github.io/docs/MQTT/ – `tele/<topic>/SENSOR` als Sammeltelegramm
- https://sparkplug.eclipse.org/ – Sammel-Payload mit Zeitstempel je Metrik
