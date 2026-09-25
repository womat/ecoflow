# Das Ausgabeformat von `ecoflowd`

Wie `cmd/ecoflowd` seine Messwerte auf den lokalen Broker legt und warum in dieser Form.

> **Herkunft:** Die Angaben über `ecoflowd` stammen aus dem Code dieses Repos. Das
> Vergleichsmuster stammt aus einem Mitschnitt am Broker einer bestehenden
> Hausautomatisierung (23.09.2026, rund fünf Minuten) und aus deren Node-RED-Flow. Die
> Aussagen über fehlende Felder und über `dcdc` sind an den Mitschnitten in
> `internal/frames/testdata/` gemessen (26.09.2026); wo etwas gefolgert statt gemessen ist,
> steht das dabei.

## Kurzfassung

Seit **v0.5.0** publiziert `ecoflowd` **zwei JSON-Telegramme**: `<topic>/state` je Messung
und `<topic>/energy` mit den Tagessummen, beide mit Seriennummer und Messzeit im Payload,
nicht retained. Bis v0.4.x war es ein Topic je Wert mit blanker Zahl, dazu ein
Verfügbarkeits-Topic mit Last Will; das ist ersatzlos entfallen, der Bruch war bewusst.

Der Grund ist nicht Geschmack: `internal/frames.Energy` ist ein Sammelobjekt – mehrere
Messwerte und eine Messzeit aus *einem* Frame. Das alte Format nahm es auseinander und
baute anschließend drei Mechanismen, um den Verlust auszugleichen (Heartbeat,
Verfügbarkeits-Topic, Änderungserkennung). Mit der Messzeit im Payload ist keiner davon
mehr nötig.

## 1. Was `ecoflowd` sendet

```
ecoflow/state   {"sn":"HC31XXXXXXXXXXXX","timestamp":"2026-09-22T09:13:16Z","pv":975,"house":-459,"battery":515,"grid":0,"soc":63}
ecoflow/energy  {"sn":"HC31XXXXXXXXXXXX","timestamp":"2026-09-22T09:13:18Z","pv":3301,"house":3206,"batteryIn":1545,"batteryOut":1562,"gridIn":62,"gridOut":175}
```

Die Werte sind aus `internal/frames/testdata/fast.txt` dekodiert; die Tests in
`cmd/ecoflowd/telegram_test.go` prüfen genau diese. Die Feldtabellen stehen in der README,
Abschnitt „An den lokalen Broker“.

- `state` geht bei jeder neuen Messung hinaus: ohne `--fast` minütlich, mit `--fast` alle
  zwei bis drei Sekunden. Es gibt keine Änderungserkennung mehr – eine unveränderte Messung
  mit neuer Messzeit ist eine Nachricht: Das Gerät ist noch da.
- `energy` geht hinaus, sobald die sechs Teile der Stundenhistorie (254/32) mit gleichem
  Zeitstempel beisammen sind (`cmd/ecoflowd/serve.go`, `dailyTotals`).
- `timestamp` ist immer die **Messzeit des Geräts**, bei `energy` die der Stundenhistorie.
  Die Tagessummen gelten für den **UTC-Tag**; in Österreich springen sie um 01:00 (MEZ)
  bzw. 02:00 (MESZ) auf 0.
- Watt und Wattstunden wie gemessen, Vorzeichen wie das Gerät sie liefert – positives
  `grid` ist Einspeisung, positives `battery` ist Laden. Umrechnen bleibt beim Menschen,
  dieselbe Regel wie bei den Adressen in `modbusread`.

### Das frühere Format (bis v0.4.x)

Zum Nachschlagen, falls ein alter Verbraucher auftaucht:

```
ecoflow/HC31XXXXXXXXXXXX/pv        0
ecoflow/HC31XXXXXXXXXXXX/house     -293
…
ecoflow/HC31XXXXXXXXXXXX/measured  2026-09-23T20:39:00Z
ecoflow/HC31XXXXXXXXXXXX/energy/…  (sechs Tagessummen)
ecoflow/HC31XXXXXXXXXXXX/status    online        ← retained, Last Will
```

Publiziert wurde bei Änderung, dazu einmal pro Minute auch unverändert. Das retained
`status` bleibt nach dem Umstieg am Broker stehen, bis man es löscht (README, „Umstieg von
v0.4.x“).

## 2. Das Vergleichsmuster

Eine gewachsene Hausautomatisierung am selben Broker sendet **ein flaches JSON-Objekt je
Gerät** auf `myhome/<gerät>/summary`, unabhängig von Änderungen:

```
myhome/inverter/summary   {"E":39836000,"P":240,"PC":242.39,"PR":-34,"F":49.98,…}
myhome/heatpump/summary   {"Timestamp":"2026-09-23T20:36:23.168Z","E":26440945,"P":0,…}
myhome/wallbox/summary    {"timeStamp":"…","counter":34274662,"counterUnit":"Wh",
                           "gauge":0.98,"gaugeUnit":"W"}
```

Kein Verfügbarkeits-Topic, kein Last Will; die Empfänger prüfen den Zeitstempel selbst.

Der Wetter-Zweig desselben Brokers führt **beide** Formen parallel – ein `summary` mit dem
vollständigen JSON *und* aufgefächerte Einzelwerte. Die Formen schließen einander also
nicht aus; `ecoflowd` bietet nur die eine an, weil ein zweiter Zugang ein zweiter wäre, den
man nachziehen muss.

**Einen einheitlichen Schlüsselstil gibt es dort nicht** (Stand 25.09.2026, aus den
Schlüsseln, die der Flow liest): PascalCase (`Timestamp`, `State`, `Power`), Kürzel (`E`,
`P`, `SOC`), camelCase (`timeStamp`, `unitCounter`), Kleinschreibung (`out1`), und zweimal
gemischt in einem Objekt (Smartfox: `BoilerE` neben `grid`). Ein Hausstil, dem `ecoflowd`
folgen könnte, existiert also nicht – siehe §4.

## 3. Die Annahmen hinter dem alten Format – und was aus ihnen wurde

### 3.1 „evcc und Home Assistant brauchen je Topic einen Skalar" – **widerlegt**

evcc kennt im MQTT-Plugin einen `jq:`-Ausdruck, Home Assistant `value_template:
"{{ value_json.pv }}"`. JSON kostet dort eine Konfigurationszeile, nicht mehr.

### 3.2 „Ein Heartbeat ist nötig" – **entfällt mit dem Zeitstempel**

Die alte Begründung lautete: *„Without a heartbeat a consumer cannot tell 'unchanged' from
'gone'."* Genau das leistet eine Messzeit im Payload, und zwar besser: Sie ist die Uhr des
**Geräts**, nicht die Sendezeit des Dienstes.

### 3.3 „Ein retained Messwert überlebt das, was er beschreibt" – **nur ohne Zeitstempel**

Trägt die Nachricht ihre Messzeit, sieht ein neu verbundener Empfänger den Stand **und**
sein Alter. Es bleibt trotzdem bei `retain: false` – aus Gleichlauf mit dem
Vergleichsmuster und weil Home Assistant vor retained Werten zusammen mit `expire_after`
warnt, nicht mehr aus dem alten Grund.

### 3.4 „Ein Verfügbarkeits-Topic meldet den Ausfall" – **trägt nicht**

Der einzige bekannte Verbraucher hat einen eigenen Alterswächter bauen müssen, weil er dem
retained `status` nicht trauen konnte: Hängt `ecoflowd`, ohne die Verbindung zu verlieren,
bleibt `online` stehen und der Last Will feuert nicht. Einen hängenden Dienst kann nur der
Empfänger bemerken. Home Assistant kommt mit `expire_after`, evcc mit `timeout` ohne
Verfügbarkeits-Topic aus.

## 4. Die Entscheidungen im Einzelnen

### Zwei Telegramme, nicht eines

Momentanwerte und Tagessummen stammen aus verschiedenen Frames (96/33 bzw. 96/34 und
254/32) mit verschiedenen Zeitstempeln. In ein Objekt gepackt, entstünde genau die Lüge,
die ein Sammeltelegramm vermeiden soll: Felder verschiedenen Alters unter einem
Zeitstempel.

### Die Seriennummer im Payload, das Topic frei

Dort überlebt sie eine Weiterleitung nach InfluxDB oder in eine Warteschlange, bei der das
Topic verloren geht, und ein frei wählbares Topic fügt sich in jedes gewachsene
Namensschema. **Was man dafür aufgibt:** `ecoflow/+/soc` über mehrere Geräte. Mehrere
Geräte brauchen je ein eigenes `--topic` (die systemd-Unit liest dafür
`/etc/ecoflowd/<SN>.env`).

### Topics klein, `/state` und `/energy` fest

MQTT unterscheidet Groß- und Kleinschreibung; ein Abo, das sich in einem Buchstaben
unterscheidet, bekommt keinen Fehler, sondern nichts. Die festen Teile sind deshalb klein.
`--topic` übernimmt `ecoflowd` unverändert – was getippt wird, geht so hinaus.

### Schlüssel in camelCase

JSON selbst schreibt keinen Stil vor (RFC 8259, ECMA-404). Die meistzitierten Leitfäden
legen sich auf camelCase fest: Googles JSON Style Guide, die Microsoft REST API Guidelines,
JSON:API. snake_case ist in Python-nahen Ökosystemen verbreitet (Home Assistant,
zigbee2mqtt), PascalCase empfiehlt für JSON praktisch niemand. Für die Empfänger ist es
gleich – `value_json.batteryIn` liest sich wie `value_json.battery_in`. Abkürzungen werden
wie Wörter behandelt: `sn`, `soc`, `pv`. Zweiwortig sind nur `batteryIn`, `batteryOut`,
`gridIn`, `gridOut`.

### Ein fehlendes Feld ist 0 – gemessen, nicht angenommen

Bis v0.4.x stand hier die Sorge, ein fehlendes Feld werde stillschweigend als 0 gelesen
und sei von einer gemessenen 0 nicht zu unterscheiden; JSON könne das Feld stattdessen
weglassen. Die Mitschnitte beantworten das anders:

| | `slow.txt` | `fast.txt` | zusammen |
|---|---|---|---|
| Energieberichte | 47 | 144 | 191 |
| davon ohne `grid` | 13 | 87 | **100** |
| Leistungsfeld explizit `0.0` am Draht | 0 | 0 | **0** |
| Bilanz `PV − battery − │house│ − 0` ohne `grid` | 0,000 W | 0,000 W | **exakt 0** |
| Bilanz mit `grid` | ≤ 0,001 W | 0,000 W | |

Andere Felder als `grid` fehlen nie. Das ist das Verhalten von **proto3**: Ein Feld, das
seinen Standardwert hat – bei Zahlen 0 –, wird nicht übertragen, und der Empfänger liest
das Fehlen als diesen Standardwert. Nur Felder mit `optional` verraten, ob sie gesetzt
waren. **Dass EcoFlows Schema proto3 ohne `optional` ist, ist gefolgert, nicht belegt**;
belegt ist das Verhalten.

„Fehlt“ heißt hier also „gemessen 0“. Weglassen wäre falsch: `grid` fehlte dann in jedem
zweiten Telegramm, und zwar im häufigsten Zustand (weder Bezug noch Einspeisung), und ein
Empfänger bekäme `null` statt 0. Festgehalten in `internal/frames` als
`TestAbsentMeansZero`, der anschlägt, wenn eine Firmware das Verhalten ändert.

### `dcdc` erst nach Klärung

Ins Telegramm kommt nur, was verstanden ist. `dcdc` (Feld 2) ist es nicht: Der Name stammt
aus einer Fremdquelle (`dcdc_pwr`, `foxthefox/ioBroker.ecoflow-mqtt`), das Feld ist nicht
Teil der Energiebilanz und folgt der Batterie mit gleichem Vorzeichen, in den Mitschnitten
mit 63–103 % ihres Werts, ohne festes Verhältnis. Werte über 100 % schließen aus, dass es
schlicht die Batterieleistung abzüglich Wandlerverlust ist. Beide Dekoder lesen das Feld
weiter; `ecoflowd` publiziert es erst, wenn seine Rolle belegt ist
(`TestDCDCIsNotPublished` hält das fest).

### Was dabei zusammengefallen ist

`last`-Map, Heartbeat-Takt, Stale-Wächter, `setOnlineLocked`, `announce`, `check`, der Last
Will und beide 30-Sekunden-Ticker. Übrig ist: Frame kommt, Objekt bauen, publizieren.

## 5. Was das Format kostet

- **Keine Sofortmeldung bei Absturz.** Ohne Last Will merkt ein Empfänger den Tod des
  Dienstes erst nach seiner eigenen Frist statt in Sekunden.
- **Kein selektives Abonnement.** Wer nur den Ladestand will, bekommt alles. Bei sieben
  Feldern belanglos.
- **JSON-Parsen bei jedem Konsumenten.** Eine Zeile `jq` bzw. `value_template`.
- **Bruch für bestehende Verbraucher.** Wer an `ecoflow/<SN>/+` hing, muss umstellen; nach
  der Versionsregel in `0.x` ein Minor-Schritt mit Bruch-Vermerk, daher v0.5.0.

## 6. Offene Punkte

- **Kommen nachts Energieberichte an?** Nach derselben proto3-Regel fehlte bei PV = 0 auch
  das PV-Feld, und beide Dekoder verwerfen einen Frame ohne PV
  (`internal/frames/energy.go`, `scripts/ecoflow-frames.py`). Die Mitschnitte sind reine
  Tagaufnahmen (PV ≥ 948 W). Zu prüfen mit einem nächtlichen `ecoflow-api.sh live`; fällt
  die Antwort „nein“ aus, ist die PV-Prüfung zu ändern – in beiden Fassungen und mit neuen
  `.golden`-Dateien.
- **Wie oft kommt `energy` mit `--fast`?** Die Stundenhistorie kommt dann deutlich
  häufiger. Nicht gezählt; wird es zu viel, wäre „nur bei geänderten Summen senden“ eine
  eigene Entscheidung.
- **Was misst `dcdc`?** Siehe §4.

## Quellen

- https://www.rfc-editor.org/rfc/rfc8259 – JSON, ohne Vorgabe zur Schreibweise
- https://google.github.io/styleguide/jsoncstyleguide.xml – Googles JSON Style Guide, camelCase
- https://github.com/microsoft/api-guidelines – Microsoft REST API Guidelines, camelCase
- https://jsonapi.org/recommendations/ – JSON:API, camelCase für Member-Namen
- https://protobuf.dev/programming-guides/proto3/#default – Standardwerte in proto3
- https://protobuf.dev/programming-guides/field_presence/ – wann ein Feld übertragen wird
- https://docs.evcc.io/en/docs/devices/plugins – `jq` im MQTT-Plugin
- https://github.com/evcc-io/evcc/pull/943 – Einbau des `jq`-Parsens
- https://www.home-assistant.io/integrations/sensor.mqtt/ – `value_template`,
  `expire_after`, `availability_topic`
- https://www.zigbee2mqtt.io/guide/usage/mqtt_topics_and_messages.html – ein JSON-Objekt
  je Gerät
- https://tasmota.github.io/docs/MQTT/ – `tele/<topic>/SENSOR` als Sammeltelegramm
- https://sparkplug.eclipse.org/ – Sammel-Payload mit Zeitstempel je Metrik
