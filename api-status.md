# API-Status – EcoFlow PowerOcean DC Fit

Stand der Recherche: September 2026.

## Kurzfassung

Es gibt **keine** offiziell dokumentierte, spezifische REST-API für den DC Fit.
Vier Wege wurden untersucht; genau einer liefert heute laufend Messwerte:

| Weg                            | Typ                        | Status                                                                                                                                             |
|--------------------------------|----------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------|
| EcoFlow Developer/Open API     | Cloud, REST, HMAC-signiert | **Unbrauchbar.** Listet den DC Fit, verweigert aber die Messwerte: Fehler 1006 „not allowed" – am Gerät bestätigt. Auch der MQTT-Kanal bleibt stumm |
| Endkunden-Portal               | Cloud, REST, Session-Token | **Liefert Daten, aber keine aktuellen.** Gibt den zuletzt in die Cloud gepushten Stand heraus; der stand in einer Messung über eine Stunde still     |
| MQTT-Kanal der App             | Cloud, MQTT, Protobuf      | **Der einzige Weg, auf dem laufend Messwerte fließen.** Minütlich von selbst, im Sekundentakt mit dem Stream-Schalter. Inoffiziell, rückentwickelt   |
| Lokales Modbus TCP             | Modbus, Port 502           | **Am Gerät noch gesperrt** (`connection refused`). Wäre der stabile Weg, braucht aber die Freischaltung durch einen Installateur                     |

Praktisch heißt das: Wer heute Werte will, nimmt den App-MQTT-Kanal — `scripts/ecoflow-api.sh`
zum Messen, `cmd/ecoflowd` für den Dauerbetrieb. Wer Verlässlichkeit will, betreibt die
Modbus-Freischaltung.

## 1. EcoFlow Developer/Open API (Cloud)

- Endpunkt: developer.ecoflow.com
- REST-basiert, Requests HMAC-signiert
- Deckt grundsätzlich die gesamte EcoFlow-Geräteflotte ab
- Zugang: Developer-Account auf developer-eu.ecoflow.com (EU), dort accessKey und
  secretKey erzeugen. Hosts: `api-e.ecoflow.com` (EU), `api-a.ecoflow.com` (US).
- Signatur: HMAC-SHA256 des Strings
  `<business-params, ASCII-sortiert>&accessKey=…&nonce=…&timestamp=…` mit dem
  secretKey, als Hex in den Header `sign`; dazu die Header `accessKey`, `nonce`
  (6-stellig zufällig) und `timestamp` (Millisekunden).
- Verschachtelte Bodies werden für die Signatur *geflacht*: Objekte gepunktet,
  Arrays indiziert – `deviceInfo.id=1&deviceList[0].id=1&ids[0]=1&name=demo1`.
  Die Doku liefert dafür einen Testvektor, gegen den `scripts/ecoflow-api.sh selftest`
  prüft (Quelle: developer-eu.ecoflow.com, „HTTP access steps").
- Content-Type entscheidet, woher der Server die Parameter nimmt:
  `application/json;charset=UTF-8` → Request-Body, sonst → Query-String.
- Signiert werden die **rohen** Werte; URL-Encoding des Query-Strings passiert erst
  danach (belegt im offiziellen Java-Demo-Client, `HttpUtil.getHttpUriRequest`).
  Für Seriennummern und Quota-Namen ohne Sonderzeichen ist das folgenlos.
- Der offizielle Demo-Client kennt insgesamt fünf Endpunkte: `certification`,
  `device/list`, `POST device/quota`, `PUT device/quota` (schreibend, hier
  bewusst nicht verwendet) und `GET device/quota/all`. Alle lesenden davon sind
  unten durchgemessen – **es gibt keinen weiteren Cloud-Weg, der noch offen wäre.**
- Relevante Leseendpunkte: `/iot-open/sign/device/list` (Geräte des Kontos),
  `/iot-open/sign/device/quota/all?sn=…` (alle Werte eines Geräts),
  `/iot-open/sign/certification` (MQTT-Zugangsdaten: `certificateAccount`,
  `certificatePassword`, `url` = `mqtt-e.ecoflow.com`, `port` = 8883, MQTTS).
- MQTT-Topics je Gerät: `/open/<certificateAccount>/<SN>/quota` und `.../status`
  (Gerät → App) sowie `.../get`, `.../set` mit ihren `_reply`-Gegenstücken (App → Gerät).
  Auf **diesem** Kanal bleibt `.../set` außerhalb von `scripts/ecoflow-api.sh`;
  `.../get` ist über das Kommando `request` erreichbar, dessen Topic-Suffix fest
  verdrahtet ist. (Auf dem *App*-Kanal publiziert `fast` sehr wohl auf `set` – siehe
  „Der Befehl für den schnellen Takt".)
- Fertig signiert aufrufbar mit [`scripts/ecoflow-api.sh`](./scripts/ecoflow-api.sh).

### Fehler 1006 ist eine Modell-Sperrliste

**Bekanntes Problem:** Für die PowerOcean-Familie liefert die API häufig Fehlercode **1006 "not allowed"**; auch das
MQTT-Topic der Open API liefert dann keine Daten.
Das ist keine Fehlkonfiguration, sondern eine geräteseitige Sperrliste – die
Integration `shuette42/ecoflow-energy-ha` nennt es „an EcoFlow API limitation, not a
configuration problem" und führt die betroffenen SN-Präfixe:

| Gerätegruppe      | Gesperrte SN-Präfixe (Fehler 1006)                                   |
|-------------------|----------------------------------------------------------------------|
| PowerOcean        | `J327`, `J32D`, `J32E`                                               |
| PowerOcean DC Fit | `HC31` (hier selbst gemessen, in den Community-Listen nicht geführt) |
| PowerOcean Plus   | `R371`, `R372`, `R374`, `HJ3C`                                       |
| Stream / STREAM   | `BK01`, `BK21`, `ES21`, `ES22`                                       |
| Sonstige          | `HZ31`, `S02F` (Solar Tracker), `AC71` (WAVE 3)                      |

Als über die Developer-API *erreichbar* führt dieselbe Quelle die PowerOcean-Präfixe
`HJ31`, `HJ32`, `HJ35`, `HJ36`, `HJ37`, `J32B`, `J329`.

**Der DC Fit (`HC31`) ist gesperrt – am Gerät gemessen, September 2026.** Die Sperre
greift aber erst beim Datenabruf, nicht beim Auflisten. Gemessen mit
`scripts/ecoflow-api.sh` am eigenen Konto:

| Aufruf                                                                                                       | Antwort                                                                                  |
|--------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------|
| `GET /iot-open/sign/device/list`                                                                             | `"code": "0"`, Gerät gelistet mit `"online": 1` und `"productName": "PowerOcean DC Fit"` |
| `GET /iot-open/sign/device/quota/all?sn=…`                                                                   | `"code": "1006"`, `"current device is not allowed to get device info"`                   |
| `POST /iot-open/sign/device/quota` (gezielte Werte, der in der offiziellen PowerOcean-Doku beschriebene Weg) | ebenfalls **1006**                                                                       |

**Die Sperre hängt am Gerät, nicht am Endpunkt.** Dass die Signatur korrekt gebildet
wird, ist unabhängig davon belegt: `scripts/ecoflow-api.sh selftest` reproduziert den
Testvektor aus EcoFlows eigener Doku. Auch der in EcoFlows eigener
PowerOcean-Doku beschriebene Weg (`POST /iot-open/sign/device/quota` mit
`{"sn": …, "params": {"quotas": ["bpSoc"]}}`) wird mit 1006 abgelehnt. Die Signatur ist
dabei nachweislich korrekt – eine fehlerhafte Signatur würde einen Signaturfehler
liefern, keine inhaltliche Ablehnung. Bezeichnend: Die Beispiele in dieser Doku verwenden
SNs mit dem Präfix `HJ31`, also eines der als erreichbar geführten.

**Gelistet heißt also nicht lesbar.** Wer nur `device/list` testet, hält den Cloud-Weg
fälschlich für offen; die Sperre zeigt sich erst beim zweiten Aufruf. Das Präfix `HC31`
gehört damit auf die 1006-Sperrliste oben, auch wenn keine der Community-Quellen es führt.

Nebenbefund: Eine SN, die *nicht* an das Konto gebunden ist, beantwortet `quota/all` mit
`"code": "8512"` / `"no permission to do it"` – ein anderer Fehler als 1006 und ein
brauchbarer Test, ob die Besitzer-Bindung überhaupt steht.

**Es ist kein Registrierungs- oder Mapping-Problem.** Die naheliegende Vermutung, die
Anlage sei dem Developer-Account nicht zugeordnet, ist widerlegt: `device/list` gibt mit
denselben Keys die eigene SN samt `online` und `productName` zurück – die Bindung ist
damit belegt. Die API unterscheidet die Fälle sauber (8512 „gehört dir nicht" vs. 1006
„dieses Gerät gibt keine Daten heraus"), und der Meldungstext von 1006 spricht über das
Gerät, nicht über die Berechtigung. Dass es auch kein Client-Problem ist, zeigen der
reproduzierte Signatur-Testvektor und der Abgleich mit dem offiziellen Demo-Client.

### Offizielle Feldnamen (für den Abgleich mit den Modbus-Registern)

Auch wenn die Endpunkte für den DC Fit gesperrt sind: EcoFlows PowerOcean-Doku (developer-eu.ecoflow.com, Dokument
„PP2") benennt die Größen, die das Gerät kennt.
Das ist die einzige *offizielle* Quelle für diese Semantik und damit die beste
Gegenprobe für die community-ermittelten Modbus-Register in `modbus-registers.md`:

| Feld                                   | Typ   | Bedeutung                                                   |
|----------------------------------------|-------|-------------------------------------------------------------|
| `bpSoc`                                | float | Batterie-SOC                                                |
| `bpPwr`                                | float | Batterieleistung                                            |
| `mpptPwr`                              | float | MPPT-/PV-Leistung                                           |
| `sysLoadPwr`                           | float | Lastleistung                                                |
| `sysGridPwr`                           | float | Netzleistung                                                |
| `pcsAPhase`, `pcsBPhase`, `pcsCPhase`  | json  | je Phase: `vol`, `amp`, `actPwr`, `reactPwr`, `apparentPwr` |
| `mpptHeartBeat`                        | json  | Liste `mpptPv` mit je `vol`, `amp`, `pwr`                   |
| `evPwr`, `chargingStatus`, `errorCode` | –     | PowerPulse (Wallbox)                                        |

Vorzeichen-Konvention laut den Beispielen der Doku: negative `actPwr`/`sysGridPwr`
bedeuten Einspeisung, negative `bpPwr` Entladung. **Achtung:** Für `sysGridPwr` am
Portal-Endpunkt gilt am DC Fit das Gegenteil (siehe „Vorzeichen: gemessen, nicht
angenommen"). Die Dokuangabe ist für dieses Feld also nicht ungeprüft zu übernehmen.

Ebenfalls dokumentiert: `POST /iot-open/sign/device/quota/data` für historische Werte (Zeitraum höchstens eine Woche,
z.B. `code: JT303_Dashboard_Overview_Summary_Week`), und
die MQTT-Topics `/open/${certificateAccount}/${sn}/quota` sowie `.../status` – Letzteres
mit `params.status` (0 = offline, 1 = online).

### MQTT-Weg der Open API (DC Fit, September 2026)

Der MQTT-Pfad verhält sich **anders als der REST-Pfad** – die Sperre greift hier nicht
beim Verbindungsaufbau:

| Schritt                                     | Ergebnis                                                                        |
|---------------------------------------------|---------------------------------------------------------------------------------|
| `/iot-open/sign/certification`              | Code 0, liefert `certificateAccount`, Passwort, `mqtt-e.ecoflow.com`, Port 8883 |
| CONNECT (MQTTS, Port 8883)                  | `CONNACK (0)` – Authentifizierung akzeptiert                                    |
| SUBSCRIBE auf `/open/<acct>/<SN>/#`         | **abgelehnt** („All subscription requests were denied")                         |
| SUBSCRIBE auf `/open/<acct>/<SN>/quota`     | **gewährt** (`SUBACK`, Granted QoS 0)                                           |
| SUBSCRIBE auf `/open/<acct>/<SN>/status`    | **gewährt**                                                                     |
| SUBSCRIBE auf `/open/<acct>/<SN>/get_reply` | **abgelehnt** (`SUBACK` 128 = 0x80)                                             |
| PUBLISH auf `/open/<acct>/<SN>/get`         | **abgelehnt** (`PUBACK` RC 135 = 0x87 „Not authorized")                         |

Wichtig für eigene Tests: **Wildcards werden von der ACL abgelehnt, exakte Topics nicht.**
Ein Test mit `#` erzeugt also ein falsches Negativ – genau der Fehlschluss, der aus
„denied" voreilig „MQTT ist gesperrt" macht.

**Auf dem gewährten `quota`-Topic kamen jedoch keine Nachrichten an** (beobachtet über
mehrere Minuten; die Verbindung blieb dabei nachweislich gesund – `PINGREQ`/`PINGRESP`
liefen durch). Die Sperre wirkt hier also *still*: Verbindung und Abo werden akzeptiert,
publiziert wird nichts. Das deckt sich mit den Berichten anderer Nutzer, dass auch das
MQTT-Topic der Open API bei 1006-Modellen keine Daten liefert.

Einschränkung: Das ist ein Negativbefund aus einer kurzen Beobachtung. Ein Push könnte
theoretisch an Bedingungen hängen (Tageszeit, Lastwechsel, Firmware). Wer es erneut
prüft: länger laufen lassen und dabei die Anlage bewusst bewegen (z.B. Verbraucher
zuschalten) – nicht mit `#` testen, siehe Wildcard-Hinweis oben.

Nachmessbar mit `scripts/ecoflow-api.sh -v mqtt <SN>`; der Verbose-Modus zeigt CONNACK,
SUBACK und die Keepalive-Pakete, und jede eintreffende Nachricht wird mit Zeitstempel
protokolliert.

**Zusammengefasst:** Das Konto darf sich verbinden und genau zwei Topics abonnieren, auf
denen für dieses Gerät nichts publiziert wird. Anfragen darf es nicht. Von den sechs
dokumentierten Topics bleiben zwei stumme übrig – konsistent zum 1006 auf allen
REST-Datenendpunkten.

Wichtig für die Interpretation des Publish-Tests: Unter MQTT 3.1.1 bestätigt der Broker
einen Publish auch dann, wenn die ACL ihn verwirft – „keine Antwort" wäre dort nicht
deutbar gewesen. Erst **MQTT v5 mit QoS 1** liefert im PUBACK einen Reason Code und
trennt damit „der Broker hat es nicht weitergereicht" (0x87) von „das Gerät hat nicht
geantwortet". Genau so misst `scripts/ecoflow-api.sh request`, und die Antwort war 0x87:
Die Anfrage hat den Broker nie verlassen.

- Quellen: https://github.com/Feberdin/ecoflow-powerocean-ha (README),
  https://github.com/shuette42/ecoflow-energy-ha (Präfixlisten)

**Fazit:** Für die PowerOcean-Familie inklusive **DC Fit** ist die offizielle Cloud-API
zum Auslesen von Messwerten nicht nutzbar – weder über REST (1006) noch über MQTT (zwei abonnierbare, aber stumme
Topics; Anfragen per ACL verboten).

## 2. Das Endkunden-Portal (REST)

`user-portal.ecoflow.com` zeigt für dasselbe Gerät ein vollständiges Dashboard – SOC,
Solar-, Haus-, Netz- und Batterieleistung, Tages-/Monats-/Jahreserträge. Am eigenen Gerät
beobachtet (September 2026): Das Portal ruft dafür auf

```
GET https://api-e.ecoflow.com/provider-service/user/device/detail?sn=<SN>   → HTTP 200
```

**Derselbe Host wie die Developer-API, aber ein anderer Dienst** (`provider-service` statt
`iot-open`) und eine andere Authentifizierung: das Session-Token des Portals (`S1_JWT` im Local Storage) statt der
HMAC-signierten API-Keys. Die 1006-Sperre gilt dort
offensichtlich nicht – gesperrt ist die *Developer-API*, nicht der Datenzugang des
Besitzers.

Abrufbar mit `scripts/ecoflow-api.sh portal <SN>`, Token über `ECOFLOW_PORTAL_TOKEN`.

**Zwei Header, mehr braucht es nicht** (am Gerät ausprobiert, September 2026):
`Authorization: Bearer <token>` und **`product-type: 85`** – die Produktkennung, die das
Portal selbst als `productKey` in der URL führt. Fehlt sie, antwortet der Endpunkt mit
`code 0` und **ohne** `data`; das sieht wie ein leeres Konto aus, ist aber nur der fehlende
Header. Die Weboberfläche schickt zusätzlich Signaturheader (`x-appid`, `x-nonce`,
`x-sign`, `x-timestamp`); ob man sie mitsendet, ändert an der Antwort nichts.

### Was der Endpunkt liefert

Die Kopfebene trägt **genau die Feldnamen der offiziellen PowerOcean-Doku** – also das,
was die Developer-API für dieses Gerät mit 1006 verweigert:

| Feld                                                        | Beispielwert | Bedeutung                                              |
|-------------------------------------------------------------|--------------|--------------------------------------------------------|
| `bpSoc`                                                     | 51           | Batterie-SOC in %                                      |
| `bpPwr`                                                     | -204.28      | Batterieleistung                                       |
| `sysLoadPwr`                                                | -204.28      | Hauslast                                               |
| `sysGridPwr`                                                | 0.0          | Netzleistung – **positiv = Einspeisung** (siehe unten) |
| `mpptPwr`                                                   | 0.0          | PV-Leistung                                            |
| `online`                                                    | 1            | Gerätestatus                                           |
| `todayElectricityGeneration` … `totalElectricityGeneration` |              | Tages-/Monats-/Jahres-/Gesamtertrag                    |

Darunter liegt ein `quota`-Objekt mit den Rohblöcken der Firmware (Präfix `DC303_`, was
für das DC-Fit-Modell stehen dürfte):

| Block                                                                                    | Felder | Inhalt                                                                                                                                                          |
|------------------------------------------------------------------------------------------|--------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `DC303_EMS_HEARTBEAT`                                                                    | 69     | SOC-Grenzen, Zählerwerte je Phase (`meterAVoltage`, `meterACurrent`, …), Tagesenergien, Fehlercodes/-masken, `workingMode`, `sysWorkSta`, MPPT-Spannungsfenster |
| `DC303_DCDC_STA_HEARTBEAT`                                                               | 54     | DCDC-Status                                                                                                                                                     |
| `DC303_DCDC_CHANGE_HEARTBEAT`                                                            | 26     | DCDC-Änderungen                                                                                                                                                 |
| `DC303_ENERGY_STREAM_REPORT`                                                             | 9      | `bpSoc`, `bpPwr`, `pvPwr`, `gridPwr`, `loadPwr`, `dcdcPwr`, `heatingPower`, `timestamp`                                                                         |
| `DC303_ERROR_CHANGE_HEARTBEAT`                                                           | 4      | Fehlerzustände                                                                                                                                                  |
| `DC303_BMS_HEARTBEAT`, `DC303_BP_CHANGE_HEARTBEAT`, `DC303_ECOLOGY_DEV_BIND_LIST_REPORT` | 1–2    | Batterie- und Bindungsinfos                                                                                                                                     |

Das ist **mehr, als Modbus exponiert** (siehe `modbus-registers.md`, „Bekannte Lücken"),
und `DC303_ENERGY_STREAM_REPORT` trägt einen eigenen Zeitstempel – es taugt also auch zum
Mitschreiben, nicht nur für einen Momentanwert.

#### Der Zeitstempel steht still, wenn niemand hinsieht

Genau dieser Zeitstempel ist es, den `scripts/ecoflow-api.sh status` als `measured`
ausgibt – und er ist der **Messzeitpunkt der Firmware, nicht der Abrufzeitpunkt**. Am
Gerät beobachtet (22. September 2026): Drei `status`-Aufrufe kurz hintereinander lieferten
dreimal exakt dieselbe Antwort, `measured` blieb auf `2026-09-22T06:18:28Z` stehen, während
die Anlage nachweislich lief. Nach dem Öffnen der App bzw. des Portals wanderte der Wert
wieder.

Belegt ist daraus nur eines: **Der REST-Endpunkt pollt das Gerät nicht.** Er gibt heraus,
was zuletzt in die Cloud gepusht wurde. Ein `status`-Aufruf sieht deshalb aus wie ein
Live-Wert und ist ein Standbild.

**Die naheliegende Erklärung war falsch.** Sie lautete: Gepusht werde nur, solange ein
Client aktiv *nachfragt*; ohne Weckruf versiege der Strom. Dafür sprachen Fremdbelege —
`jensfr1/ha-ecoflow-ocean2` hält ein 60-Sekunden-Intervall vor mit dem Kommentar „Ohne
diesen regelmaessigen Weckruf sendet das Geraet keine Telemetrie, solange keine
EcoFlow-App geoeffnet ist"; `shuette42/ecoflow-energy-ha` fragt alle 20–30 s nach; die
openHAB-Anbindung berichtet für den STREAM Micro Updates nur bei geöffneter App.

Am eigenen Gerät nachgemessen gilt das hier **nicht**: 23 Minuten ohne einen einzigen
Publish, durchgehend Minutenwerte (siehe „Am DC Fit gemessen"). Es genügt, **abonniert zu
sein** — nachfragen muss niemand.

Was das Einfrieren verursacht, bleibt damit offen. Denkbar ist, dass das Gerät nur pusht,
solange *irgendein* Abo besteht, und die Portal-Sitzung eines ist; oder dass der
REST-Endpunkt aus einem anderen Vorrat liest als der MQTT-Kanal. Von außen ist das nicht
zu unterscheiden.

**Für die Praxis ist es gleich:** Der REST-Endpunkt taugt nicht als Live-Quelle — auch
nicht, während ein Abo läuft. Das wurde gegengeprüft: `measured` blieb stehen, während die
MQTT-Frames bereits eine Stunde weiter waren.

#### Vorzeichen: gemessen, nicht angenommen

Die Vorzeichen sind **nicht einheitlich**, und die Oberfläche des Portals zeigt ohnehin
Beträge an. Am Gerät bestimmt (September 2026):

| Feld         | positiv bedeutet                                   |
|--------------|----------------------------------------------------|
| `bpPwr`      | Batterie **lädt** (negativ = entlädt)              |
| `sysGridPwr` | **Einspeisung** (negativ = Bezug)                  |
| `sysLoadPwr` | wird negativ gemeldet, während das Haus verbraucht |

Entschieden über die Energiebilanz einer sonnigen Messung: 3378 W PV verteilen sich auf
514 W Haus, 609 W in die Batterie und 2255 W ans Netz – das geht nur auf, wenn die positive
Netzzahl das Haus *verlässt*. **Damit widerspricht der Portal-Endpunkt der offiziellen
Feldbeschreibung**, die für `actPwr`/`sysGridPwr` negativ = Einspeisung nahelegt; für den
DC Fit gilt die gemessene Richtung.

`scripts/ecoflow-api.sh status` gibt deshalb Beträge aus und schreibt die Richtung als Wort
dazu.

Das Token gibt es auf zwei Wegen: aus dem eingeloggten Browser (*Local Storage → `S1_JWT`*)
oder über den Login-Endpunkt der Endkunden-App:

```
POST https://api-e.ecoflow.com/auth/login
{"email": "…", "password": "<base64>", "scene": "IOT_APP", "userType": "ECOFLOW"}
→ data.token, data.user.userId
```

Genau das macht `scripts/ecoflow-api.sh login [E-Mail]`: Es gibt ausschließlich den Token
auf stdout aus, sodass er sich direkt einfangen lässt –
`export ECOFLOW_PORTAL_TOKEN="$(scripts/ecoflow-api.sh login)"`, danach `status <SN>`.

Das Passwort wird dabei nur **base64-kodiert, nicht gehasht** übertragen – Kodierung, keine
Verschlüsselung. Wer das nicht will, nimmt den Browser-Token: kleineres Geheimnis, läuft von
selbst ab. Quelle für den Ablauf: `shuette42/ecoflow-energy-ha`, `enhanced_auth.py`.

Dort steht auch, wofür dieses Token sonst noch taugt: Es öffnet den **MQTT-Kanal der
App**, auf dem im Unterschied zum Open-API-Kanal tatsächlich Daten fließen. Dorthin
führen zwei Türen.

**Die einfache (von diesem Repo benutzt):**

```
GET /iot-auth/app/certification?userId=<userId>
Authorization: Bearer <token>
lang: en_US
→ data.certificateAccount, data.certificatePassword, data.url, data.port, data.protocol
```

Klartext-JSON, keine Entschlüsselung nötig. Abrufbar mit
`scripts/ecoflow-api.sh app-cert`, die `userId` kommt aus `ECOFLOW_USER_ID` (das
`login`-Kommando gibt sie fertig zum Exportieren aus).

**Die verschlüsselte:** `/iot-auth/enterprise-development/user/certification` – den das
Portal beim Laden selbst aufruft – liefert dieselben Felder **AES-verschlüsselt**, dafür
ohne `userId`. Genauer, als es hier bisher stand:

| Parameter  | Wert                                                              |
|------------|-------------------------------------------------------------------|
| Verfahren  | AES-256 im Modus **CFB128** – *nicht* CBC                         |
| Schlüssel  | `SHA256(token)` als **rohe 32 Byte** (nicht hex, nicht gekürzt)   |
| IV         | ASCII-Konstante `ojsajkqjwk1w2dfg` aus dem Portal-JS               |
| Kodierung  | `data` ist ein Base64-String, kein Objekt                         |
| Padding    | PKCS7 – die Restbytes bleiben nach dem Entschlüsseln übrig         |

Mit dem openssl-CLI nachvollziehbar:

```bash
KEY=$(printf %s "$TOKEN" | openssl dgst -sha256 -binary | xxd -p -c64)
printf %s "$CIPHERTEXT_B64" |
  openssl enc -d -aes-256-cfb -K "$KEY" -iv 6f6a73616a6b716a776b317732646667 -a -A -nopad
```

**Bewusst nicht implementiert.** Der Klartextweg liefert dieselben Zugangsdaten, und ein
Krypto-Pfad in einem Shell-Skript ist Code, der schweigend falsche Bytes produzieren kann.
Er steht hier, falls EcoFlow die einfache Tür schließt. Unbestätigt bleibt außerdem, ob
der `enterprise-development`-Pfad mit einem reinen Endkunden-Token überhaupt antwortet –
der Pfadname legt einen Pro-/Installateurskontext nahe.

**Einordnung:** Das ist eine interne Schnittstelle der Weboberfläche, von EcoFlow weder
dokumentiert noch zugesagt, und das Token läuft ab. Als dauerhafte Datenquelle taugt das
nicht – lokales Modbus bleibt der stabile Weg. Als Gegenprobe beim Verifizieren der
Modbus-Register ist es dagegen ausgezeichnet: dieselben Größen, aus EcoFlows eigener
Anzeige.

Laut `MaxGrmm/EF-PowerOcean-TcpModbus` liefert derselbe Endpunkt noch deutlich mehr als
das Dashboard zeigt – Zellspannungen, SOH, phasenweise Wirk-/Blind-/Scheinleistung, rund
180 Netzschutzparameter. **Ungeprüft:** Das genannte Projekt ist eine Modbus-Integration,
und ob sich die Angabe auf diesen REST-Endpunkt oder auf Register bezieht, geht daraus
nicht hervor.

**Wofür dieser Weg taugt:** als Gegenprobe und für Zählerstände, nicht für laufende
Messwerte – dafür der App-MQTT-Kanal (Kapitel 3). Der stabile Weg bliebe Modbus
(Kapitel 4), sobald er freigeschaltet ist.

## 3. Der MQTT-Kanal der App

Der Kanal, den die App benutzt – und der einzige Cloud-Kanal, auf dem für dieses Gerät
tatsächlich Nachrichten ankommen. Abonnierbar mit `scripts/ecoflow-api.sh live <SN>`.

**Broker:** `mqtt-e.ecoflow.com:8883` (MQTTS), Host und Port kommen aus der
Certification-Antwort; Benutzername und Passwort sind `certificateAccount` und
`certificatePassword` daraus – nicht die Kontodaten, nicht das Token.

**Client-ID:** `ANDROID_<32 Hex-Zeichen, Großbuchstaben>_<userId>`. Das ist keine
Kosmetik: Der Broker weist Client-IDs ab, die nicht so aussehen, und er weist eine bereits
gesehene ID nach dem Verbindungsabbruch erneut ab. Deshalb baut `live` für **jede**
Verbindung eine neue.

**Topics** (Wildcards meiden – die ACL lehnt sie ab; zum Fehlschluss, der daraus
entsteht, siehe Kapitel 1, „MQTT-Weg der Open API"):

| Topic                                           | Richtung  | Inhalt                          |
|-------------------------------------------------|-----------|---------------------------------|
| `/app/device/property/<SN>`                     | subscribe | Telemetrie-Push, **Protobuf**   |
| `/app/<userId>/<SN>/thing/property/get`         | publish   | Anfrage/Weckruf                 |
| `/app/<userId>/<SN>/thing/property/get_reply`   | subscribe | Antwort darauf                  |
| `/app/device/status/<SN>`                       | subscribe | online/offline                  |
| `/app/<userId>/<SN>/thing/property/set`         | publish   | **schreibend** – nur `fast`     |

**Der Weckruf**, den `live` alle `ECOFLOW_LIVE_INTERVAL` Sekunden (Default 30) auf das
`get`-Topic schickt:

```json
{"from":"Android","id":"<Millisekunden>","moduleType":0,
 "operateType":"latestQuotas","params":{},"version":"1.0"}
```

**Was `live` bewusst nicht tut:** Die App aktiviert ihren schnellen Stream (~2–3 s) über
einen Protobuf-Frame `EnergyStreamSwitch` auf dem `.../set`-Topic. `live` publiziert nur
auf `get`-Topics – wer Werte abfragt, soll dabei nicht unbemerkt schreiben. Den Schalter
schickt allein das Kommando `fast` (siehe „Der Befehl für den schnellen Takt"), und der
Go-Dienst `cmd/ecoflowd` nur mit dem Flag `--fast`.

Der Preis für `live` ist der Minutentakt des Geräts – **nicht** der Takt seiner eigenen
Anfragen: Die sind, wie weiter unten gemessen, für den Datenfluss ohne Belang.

**Format:** Der Push ist beim PowerOcean **Protobuf**, nicht JSON – `jq` hilft dort nicht.
`live` gibt deshalb jede Nachricht als Hex aus und schreibt den Text nur dann zusätzlich
hin, wenn die Nutzlast vollständig druckbar ist. Auf `get_reply` kam am DC Fit
**überhaupt nichts** an (siehe „Am DC Fit gemessen"), die Frage nach JSON oder Protobuf
stellt sich dort also gar nicht.

**Vorbehalt Plus vs. DC Fit.** (Zur Kennung: Den Energiestrom gibt es auf **33** *und*
**34** – der schnelle und der minütliche Bericht, siehe „Am DC Fit gemessen". Fremdquellen
nennen nur eine von beiden.) Die Protobuf-Feldnummern unterscheiden sich zwischen den
Modellen. Für den JT-S1-PowerOcean trägt `cmd_func 96 / cmd_id 33` die Reihenfolge
`sys_load_pwr, sys_grid_pwr, mppt_pwr, bp_pwr, bp_soc`; die DC-Fit-Definition bei
`foxthefox/ioBroker.ecoflow-mqtt` (Gerätetyp `poweroceanfit`) belegt dieselbe Kennung
mit `grid_pwr, dcdc_pwr, bp_pwr, pv_pwr, timestamp, timezone, bp_soc, load_pwr, …`.
Wer hier die falsche Tabelle nimmt, bekommt plausible Zahlen an den falschen Namen. Das
ist derselbe Vorbehalt wie beim Register-Mapping.

**Herkunft:** Endpunktpfad, Client-ID-Form, Topics und Weckruf-Payload waren Übernahmen
aus fremdem Reverse-Engineering. Am Gerät nachgeprüft wurden sie am 22. September 2026 –
siehe den nächsten Abschnitt. Quellen: `shuette42/ecoflow-energy-ha`,
`tolwi/hassio-ecoflow-cloud`, `jensfr1/ha-ecoflow-ocean2`,
`foxthefox/ioBroker.ecoflow-mqtt`.

### Am DC Fit gemessen (22. September 2026)

Der Kanal **trägt**. `app-cert` antwortet mit `code 0` und liefert
`mqtt-e.ecoflow.com:8883` samt Zugangsdaten; der Broker akzeptiert die Client-ID der Form
`ANDROID_<hex>_<userId>`, alle drei Topics werden abonniert, und auf
`/app/device/property/<SN>` treffen **alle paar Sekunden Frames ein**. Damit ist dies der
einzige Cloud-Weg, auf dem für dieses Modell tatsächlich Messwerte fließen.

Drei Befunde aus derselben Messung:

**1. Der Weckruf wird nicht beantwortet – und er ist überflüssig.** In zweieinhalb Minuten
kam auf `.../thing/property/get_reply` keine einzige Nachricht, der Push lief trotzdem.
Nachgemessen am 22. September 2026 mit `ECOFLOW_LIVE_INTERVAL=0`, also **ohne einen
einzigen Publish**, bei geschlossener App und geschlossenem Portal:

| | |
|---|---|
| Dauer | 22,8 Minuten (10:28–10:51 UTC) |
| `96/34` Minutenbericht | 47 Frames, lückenlos jede Minute, Abstand 58–61 s |
| dekodierte Messwerte | 23, einer je Minute, ohne Aussetzer |
| `254/32` Stundenhistorie | kam weiter, rund alle 10 Minuten |
| `96/33`, `96/3`, `96/137` | **null** |

**Das Abo allein hält das Gerät am Reden.** Für den Minutentakt braucht es keinen
Weckruf, keinen Stream-Schalter, überhaupt keinen Publish – ein rein lesender Client
genügt. Die Schleife, die `live` mitbringt, ist damit von fremden Projekten übernommen und
für dieses Modell ohne Wirkung; `ECOFLOW_LIVE_INTERVAL=0` schaltet sie ab.

**2. Der REST-Endpunkt wird davon nicht frisch.** `status` lieferte während der ganzen
Zeit unverändert `measured : 2026-09-22T07:13:28Z`, während die MQTT-Frames bereits
`08:19Z` trugen – über eine Stunde Unterschied. **Der Umweg über `provider-service` ist
also keine Live-Quelle, auch nicht mit laufendem Zuhörer.** Wer aktuelle Werte will, muss
die Frames auswerten.

**3. Die Nutzlast ist XOR-verschleiert.** Jedes Byte der Nutzlast ist mit dem niederwertigen
Byte der Sequenznummer (Header-Feld 14) **exklusiv-verodert** (XOR, nicht OR). Aufgefallen
ist das daran, dass zwei
Frames mit benachbarten Sequenznummern sich in *jedem* Byte um dasselbe Bitmuster
unterscheiden. Ohne diesen Schritt ist die Nutzlast kein gültiges Protobuf.

**Rahmenaufbau** (Feldnummern des äußeren Headers, bestätigt):

| Feld | Bedeutung                                |
|------|------------------------------------------|
| 1    | Nutzlast (XOR-verschleiert, s.o.)        |
| 8    | `cmd_func` – bei allen Frames hier 96    |
| 9    | `cmd_id` – unterscheidet die Berichte    |
| 14   | Sequenznummer, zugleich der XOR-Schlüssel |

**Beobachtete `cmd_id` bei `cmd_func 96` im langsamen Betrieb:** 1, **34**, 108, 109, 110,
111, 136. Mit aktivem Stream-Schalter kommen **33**, **3** und **137** dazu, und
`cmd_func 254 / cmd_id 32` wird häufig — siehe die Abschnitte weiter unten.

**Den Energiestrom gibt es zweimal, auf `cmd_id 34` und `cmd_id 33`** – mit derselben
Feldbelegung, aber unterschiedlichem Takt und unterschiedlicher Verpackung:

| Kennung | Takt          | Zeitstempel     | Nutzlast                        | Bedingung                    |
|---------|---------------|-----------------|---------------------------------|------------------------------|
| **34**  | genau minütlich | auf die Minute gerundet | Felder in eine Nachricht eingepackt | kommt immer                  |
| **33**  | alle 2–3 s    | sekundengenau   | Felder direkt in der Nutzlast   | nur bei aktivem Stream-Schalter |

Wer also nur mit `live` misst, sieht ausschließlich 34 und hält 33 für nicht vorhanden –
und wer der Fremdquelle folgt, die nur 33 nennt, findet ohne den Schalter gar nichts.

**Bei laufendem schnellen Strom ist 34 überflüssig:** Über eine Messreihe hinweg hatte
*jeder* Minutenbericht einen Sekundenbericht mit demselben Zeitstempel. Er trägt also
nichts bei, sieht aber wie ein Stillstand aus, weil sein Zeitstempel auf die Minute
gerundet ist. `ecoflow-frames.py` unterdrückt ihn deshalb, solange innerhalb der letzten
90 Sekunden ein 33er kam – und zeigt ihn wieder, sobald der schnelle Strom versiegt.

Unabhängig davon schickt das Gerät manche Frames **zweimal**, bei beiden Kennungen. Zwei
gleiche Messwerte sind ein Messwert, deshalb wird eine Zeile unterdrückt, die mit der
vorigen identisch ist. Zwei *verschiedene* Werte in derselben Sekunde bleiben stehen – die
kommen vor und sind keine Wiederholung.

Die Feldbelegung ist in beiden Fällen dieselbe:

| Feld | Typ     | Bedeutung                              |
|------|---------|----------------------------------------|
| 1    | float   | Netzleistung (positiv = Einspeisung)   |
| 2    | float   | DCDC-Leistung                          |
| 3    | float   | Batterieleistung (positiv = Laden)     |
| 4    | float   | PV-Leistung                            |
| 5    | uint32  | Zeitstempel (Unix-Sekunden, UTC)       |
| 6    | sint32  | Zeitzone                               |
| 7    | uint32  | SoC in %                               |
| 8    | float   | Hauslast (wird negativ gemeldet)       |

**Wie das belegt ist:** über die Energiebilanz. In jedem einzelnen Frame gilt
`PV = Batterie + Haus + Netz` auf zwei Nachkommastellen genau – etwa 968,59 W PV =
530,0 W Batterie + 351,6 W Haus + 87,0 W Netz. Eine falsche Feldzuordnung würde das nicht
treffen. Die Vorzeichen decken sich mit denen des Portal-Endpunkts (siehe „Vorzeichen:
gemessen, nicht angenommen").

**Takt:** Das Gerät meldet den Energiestrom **genau minütlich** und schickt jeden Frame
**doppelt**. Der Gerätezeitstempel liegt dabei auf der vollen Minute (`08:30:00Z`,
`08:31:00Z`, `08:32:00Z`).

Der Takt hängt **nicht** am Weckruf: Ein Lauf mit `ECOFLOW_LIVE_INTERVAL=5` meldete
weiterhin minütlich. Den schnelleren Rhythmus schaltet die App über das `.../set`-Topic
frei – siehe den nächsten Abschnitt.

#### Der Befehl für den schnellen Takt, mitgelesen statt geraten

Das `set`-Topic lässt sich **abonnieren**, und Abonnieren ist lesend. Wer dabei die
Handy-App bedient, sieht, was sie sendet – `scripts/ecoflow-api.sh app-mqtt <SN>` tut
genau das. Am 22. September 2026 aufgezeichnet:

```
0a390a0408011001102018602001280138034060486150045801700a800103880101ba0103696f73ca0110<SN als ASCII>
```

| Feld | Wert            | Bedeutung                                       |
|------|-----------------|-------------------------------------------------|
| 1    | `08 01 10 01`   | Nutzlast: zwei Flags, beide 1 – **im Klartext** |
| 2    | 32              | `src` – die App                                 |
| 3    | 96              | `dest` – die Energieverwaltung                  |
| 4, 5 | 1, 1            | `dSrc`, `dDest`                                 |
| 7    | 3               | unbekannt                                       |
| 8    | 96              | `cmd_func`                                      |
| 9    | **97**          | `cmd_id` – der Stream-Schalter                  |
| 10   | 4               | `dataLen`                                       |
| 11   | 1               | `needAck`                                       |
| 14   | kleiner Zähler  | `seq` – 3, 5, 7, 10 … einstellig beginnend      |
| 16, 17 | 3, 1          | `version`, `payloadVer`                         |
| 23   | `"ios"`         | womit die App sich meldet                       |
| 25   | Seriennummer    | als ASCII                                       |

Die App wiederholt das etwa alle drei Sekunden; das Gerät meldet dann ebenso oft.

**Die Verschleierung gilt nur Gerät → App.** Was die App sendet, ist unverschlüsselt –
der XOR-Schritt entfällt in dieser Richtung.

**Warum das Mitlesen den Unterschied machte.** Ein aus den Fremdquellen zusammengesetzter
Versuch lag an vier Stellen daneben: verschleierte statt klare Nutzlast, falsch verschachtelter
Inhalt (`0a020801` statt `08011001`), eine sechsstellige statt einer einstelligen
Sequenznummer, dazu zwei erfundene und zwei fehlende Felder. Richtig geraten waren allein
`cmd_func 96` und `cmd_id 97`. Auf einem Schreib-Topic wäre das ein Schuss ins Dunkle
gewesen – es gibt keinen Grund, so etwas zu raten, wenn man es messen kann.

Umgesetzt in `scripts/ecoflow-api.sh fast <SN>`: dasselbe wie `live`, zusätzlich dieser
eine Frame alle `ECOFLOW_FAST_INTERVAL` Sekunden. Es ist das **einzige** Kommando des
Skripts, das auf ein `set`-Topic publiziert, es trägt keine Parameter, und es ist
absichtlich ein eigenes Kommando – damit der Schreibzugriff nie als Nebenwirkung einer
Werteabfrage passiert.

**Am Gerät gemessen (22. September 2026):**

- Der Broker **nimmt den Publish an**: `PUBACK RC:0` unter MQTT v5 mit QoS 1. Auf dem
  `set`-Topic des App-Kanals greift also keine ACL-Sperre – anders als auf dem `get`-Topic
  des Open-API-Kanals, wo derselbe Test `0x87` lieferte.
- **Der Wiederholabstand entscheidet.** Mit 3 Sekunden (dem Rhythmus der App) läuft der
  schnelle Strom: im Mitschnitt 131 Messwerte über 241 Sekunden, im Schnitt alle 1,9 s.
  Mit 10 Sekunden fiel das Gerät auf den Minutentakt zurück; Default ist deshalb 3.
  (Wie lange der Schalter nachwirkt, ist damit **nicht** gesagt – siehe die offene Frage
  dazu am Dateiende.)
- Nebenbei sichtbar wurde noch `cmd_func 254 / cmd_id 32` (rund zweimal pro Sekunde) sowie
  `96/3`, `96/1` und `96/137` im Sekundenbereich – alle nicht ausgewertet.

**Preis beim Skript:** Es startet für jeden Schalter einen eigenen `mosquitto_pub`, also
alle 3 Sekunden einen Verbindungsaufbau – rund 28.000 am Tag. Für eine Messung in Ordnung,
für Dauerbetrieb nicht; eine stehende Verbindung kann `mosquitto_pub` von der
Kommandozeile aus nicht. Genau deshalb gibt es `cmd/ecoflowd`, das eine hält.

#### Die Stundenhistorie: `cmd_func 254 / cmd_id 32`

Der mit Abstand häufigste Frame im schnellen Betrieb – rund zweimal pro Sekunde. Er trägt
**keine Momentanwerte, sondern die Energiebilanz des laufenden Tages, stundenweise.**

Aufbau: Die Nutzlast (ebenfalls XOR-verschleiert) enthält in Feld 2 eine Nachricht mit

| Feld | Inhalt                                                        |
|------|---------------------------------------------------------------|
| 1    | Zeitstempel, Unix-Sekunden                                    |
| 2    | welcher Fluss – 1, 16, 32, 48, 64, 80                         |
| 3    | **24 aneinandergereihte Varints**: ein Wert je Tagesstunde, Wh |

Sechs Frames mit demselben Zeitstempel ergeben eine vollständige Meldung:

| Feld 2 | Fluss                        |
|--------|------------------------------|
| 1      | PV-Erzeugung                 |
| 16     | Batterie geladen             |
| 32     | Batterie entladen            |
| 48     | Netzbezug                    |
| 64     | Netzeinspeisung              |
| 80     | Hausverbrauch                |

Die laufende Stunde füllt sich noch; alle späteren Stunden stehen auf 0.

**Wie die Zuordnung belegt ist – zwei unabhängige Wege.** Erstens über die Steigung: Über
vier Minuten wuchs jeder Zähler genau mit der Leistung seines Flusses (PV 1013 W gegen
gemessene 1029 W, Batterie 529 gegen 544, Haus 454 gegen 449, Netz 30 gegen 39). Zweitens
über die **Stundenbilanz**, die auf ±1 Wh aufgeht:

```
PV + Batterie raus + Netzbezug  =  Haus + Batterie rein + Netzeinspeisung
```

Beispiel vom 22. September 2026, Stunde 7 UTC: 1350 + 0 + 1 = 473 + 798 + 81 (±1 Wh
Rundung). Auch die Nullen sitzen richtig: kein PV vor der Dämmerung, Batterie entlädt
nachts und lädt, sobald die Sonne das Haus trägt.

Abrufbar mit `scripts/ecoflow-api.sh fast <SN> | python3 scripts/ecoflow-frames.py --hours`.

**Offen:** Die Tagessumme deckt sich nicht mit `todayElectricityGeneration` des Portals –
siehe die Liste der offenen Fragen. Mit dem Gerät selbst deckt sie sich dagegen: Feld 23
in `96/1` trägt dieselbe Summe als Float, im selben Sekundenfenster auf 5,5 Wh genau.

#### Die Antwort auf den Schalter: `96/3` und `96/137`

Beide kommen im schnellen Betrieb im Sekundentakt – und im langsamen **gar nicht**. Die
Gegenprobe ist eindeutig: 83 bzw. 75 Frames bei aktivem Schalter, null ohne ihn. Der
Schalter setzt `needAck = 1`, das Gerät antwortet also mit beidem.

**`96/137` hat eine leere Nutzlast** – eine reine Bestätigung, ohne Inhalt.

**`96/3` ist eine Komponentenliste.** Über 83 Frames hinweg byteweise identisch, also
keine Messung. Jedes Feld enthält eine eingebettete Nachricht, deren Feld 1 eine
Seriennummer als ASCII trägt:

| Feld | Beispielwert       | Komponente                          |
|------|--------------------|-------------------------------------|
| 1    | `HC31XXXXXXXXXXXX` | das System selbst – die abgefragte SN |
| 2    | `HC31YYYYYYYYYYYY` | PV Storage Converter, 5 kW          |
| 3    | `HJ3AXXXXXXXXXXXX` | Batterie, 5 kWh                     |
| 3    | `HJ3AYYYYYYYYYYYY` | Batterie, 5 kWh                     |

Die Präfixe sind echt und die aussagekräftige Hälfte – `HC31` ist der DC Fit, `HJ3A` die
Batteriemodule; der Rest ist hier maskiert, weil dieses Repo öffentlich ist.

**Belegt über das Portal**, nicht über die Präfixe geraten: `user-portal.ecoflow.com`
führt unter *System information → Component information* dieselben Seriennummern mit Typ,
Modell, Firmware-Stand und Aktivierungsdatum. Feld 2 ist dort der Wechselrichter, die
`HJ3A`-Einträge sind die Batteriemodule – beim Beispielsystem zwei à 5 kWh, dazu ein
5-kW-Konverter. `ecoflow-frames.py --modules` übernimmt diese Bezeichnungen.

Nutzen: Seriennummern, Anzahl und Bestückung der Batterie ohne App und ohne Portal.
Firmware-Stände und Aktivierungsdatum liefert der Frame allerdings **nicht** – die gibt es
weiterhin nur im Portal.

Ausgewertet wird das von `scripts/ecoflow-frames.py`, das die Ausgabe von `live` auf
stdin nimmt. Der schnellere ~3-Sekunden-Takt, den die App über `.../set` freischaltet,
ist damit für `live` nicht erreicht – dafür gibt es `fast` bzw. `ecoflowd --fast`. Für
einen Minutentakt braucht es ihn ohnehin nicht.

#### Die übrigen Kennungen: `96/1`, `96/108`–`96/111`, `96/136`

Aus einem Mitschnitt vom 22.09.2026, 14:21–14:31Z (5 min `fast`, danach 6 min nur
zuhören). Alle sechs tragen eine XOR-verschleierte Nutzlast wie die übrigen und kommen
**nur bei aktivem Stream-Schalter** in dichter Folge; ohne ihn bleiben sie selten.

**`96/1` ist der Systembericht** und trägt die **laufenden Tagessummen als Float**. Das
ist der Befund, der sich am besten belegen lässt – dieselben sechs Flüsse wie die
Stundenhistorie, im selben Sekundenfenster gemessen:

| Feld | `96/1` (Wh) | Stundenhistorie | Differenz | Fluss           |
|------|-------------|-----------------|-----------|-----------------|
| 23   | 15960,50    | 15955           | +5,50     | PV              |
| 24   | 4812,19     | 4809            | +3,19     | Batterie hinein |
| 25   | −1565,22    | 1562            | +3,22     | Batterie heraus |
| 26   | −109,56     | 103             | +6,56     | Netzbezug       |
| 27   | 5867,35     | 5861            | +6,35     | Einspeisung     |
| 28   | −6955,67    | 6948            | +7,67     | Haus            |

Die Stundenhistorie zählt in ganzen Wh je Stundenkübel; über 15 Kübel summiert sich die
Rundung auf die paar Wh Unterschied. **Die Vorzeichen sind die der Geräteseite**, wie im
Energiebericht: Bezug und Verbrauch negativ.

Weiter im selben Frame, weniger sicher: Feld 3 = `10698,0` ist die Gesamtkapazität in Wh
(zwei Module, siehe `96/108`); Feld 13–15 sind die drei Netzspannungen (234,7 / 234,3 /
235,3 V), Feld 16–18 die Ströme (3,18 / 3,35 / 3,02 A). Feld 12 (2217 W) liegt in der
Größenordnung von U·I über drei Phasen (2242 W) und der Netzleistung des Energieberichts
derselben Sekunde (2308 W), stimmt aber mit keiner von beiden überein – **welcher
Messpunkt das ist, ist offen**. Feld 4 und 34 tragen 99 bzw. 100, der Ladestand also
vermutlich in zwei Auflösungen.

**`96/108` ist der Bericht je Batteriemodul.** Jeder Frame trägt **zwei** verschachtelte
Einträge – genauso viele, wie `96/3` Batteriemodule auflistet:

| Feld | Beispiel  | Deutung                                            |
|------|-----------|----------------------------------------------------|
| 2    | 99,90 %   | Ladestand des Moduls                               |
| 4    | 53,51 V   | Modulspannung – deckt sich mit `96/111` Feld 9     |
| 7    | 5345,52   | Kapazität in Wh; beide Einträge zusammen 10707,6, was zu `96/1` Feld 3 (10698) passt |

**`96/111` ist der Bericht eines Batteriemoduls im Detail.** Feld 16 trägt die
Seriennummer als ASCII (`HJ3A…`), Feld 9 die Modulspannung (53,43 V), Feld 14 sind
**16 Zellspannungen in mV** (3342–3344) – ihre Summe, 53,495 V, ergibt die Modulspannung
zurück. Feld 5 sind neun Temperaturen (26–27 °C).

**`96/109` ist der PV-Strangbericht.** Zwei Stränge nebeneinander: Feld 2 und 3 sind
Spannung und Strom des einen, Feld 10 und 11 die des anderen. Belegt über dieselbe
Gegenprobe wie `96/1` – hier über die Zeit statt über eine zweite Quelle:

| PV laut Energiebericht | `f2·f3 + f10·f11` | Verhältnis |
|------------------------|-------------------|------------|
| 2526 W                 | 2476 W            | 0,980      |
| 2414 W                 | 2424 W            | 1,004      |
| 2266 W                 | 2294 W            | 1,012      |
| 839 W                  | 795 W             | 0,947      |
| 897 W                  | 875 W             | 0,976      |

**Mittel 1,0004 bei einer Streuung von 0,033**, über eine Verdreifachung der Leistung.
Die Felder 4, 5, 7 bzw. 12, 13, 15 liegen auf derselben Spannungsebene (weitere
Messpunkte am selben Strang, nicht zugeordnet), Feld 22–25 sind Temperaturen.

Die Spannung verhält sich dabei wie erwartet: Sie **steigt**, wenn die Leistung fällt
(428 V bei 2526 W, 556 V bei 897 W) – der Arbeitspunkt wandert bei wenig Licht Richtung
Leerlaufspannung. Das ist eine unabhängige Plausibilitätsprobe für die Deutung als
Strangspannung.

**`96/110` liegt auf derselben Ebene und ist nicht zugeordnet.** Die Felder 2, 3 und 5
tragen dieselben Strangspannungen; 15, 18, 19, 24, 30 und 42 stehen über den ganzen
Mitschnitt **konstant** und sind damit Einstellungen, keine Messwerte. Feld 48 läuft mit
der Leistung mit, lässt sich aber nicht festnageln – und zwar aus einem Grund, der
festgehalten gehört:

> Im Mitschnitt stand die Batterie still (SoC 100 %). Dann gilt Netz = PV − Haus bei
> nahezu konstantem Haus, PV und Netz laufen also proportional. `f48/Netz` ≈ 1,00 und
> `f48/PV` ≈ 0,82 sind beide konstant – **die beiden Größen sind in dieser Betriebslage
> nicht trennbar.** Mehr Proben derselben Lage ändern daran nichts.

**Was hier weiterhilft**, ist deshalb nicht ein längerer, sondern ein *anders gelegter*
Mitschnitt: einer, in dem PV, Netz, Haus und Batterie sich unabhängig bewegen. Am
einfachsten am Abend, wenn die PV-Leistung fällt, das Haus weiter zieht und die Batterie
übernimmt. Dazu passt ein gemessener Nebenbefund: **`96/109` und `96/110` kommen ohne
Stream-Schalter sechsmal häufiger** (29 Frames in 348 s gegen 5 in 300 s) – der schnelle
Energiestrom verdrängt sie. Für diese Frage ist `live` also die bessere Quelle als `fast`,
und schreibt obendrein nichts ans Gerät. Nur zum Paaren mit einem Energiebericht auf die
Sekunde genau ist eine kurze `fast`-Phase nützlich.

**`96/136` ist eine Konstante.** Über alle Proben dieselben zwei Byte: `08 0b`, also
Feld 1 = 11. Kein Messwert.

### Einordnung: der „Enhanced Mode" der Community

Unter diesem Namen läuft derselbe Kanal in den Home-Assistant-Integrationen. Er umgeht die
1006-Sperre, indem er sich mit den normalen EcoFlow-Kontozugangsdaten anmeldet statt mit
API-Keys. Genau das tun die Home-Assistant-Integrationen für die
gesperrten Modelle. **Community-Weg ohne jede Zusage von EcoFlow**: kann jederzeit
brechen, und die Kontozugangsdaten liegen im Klartext in der Konfiguration.
(Quelle: https://github.com/shuette42/ecoflow-energy-ha)

Zum dort genannten Takt „~2–4 s": Der gilt nur mit aktivem Stream-Schalter. Ohne ihn
meldet das Gerät **minütlich** — gemessen, siehe „Am DC Fit gemessen".

Dieses Repo geht den Weg vollständig: `scripts/ecoflow-api.sh live` bzw. `fast` holt die
Frames, `scripts/ecoflow-frames.py` packt sie aus, und `cmd/ecoflowd` tut beides in einem
Dienst und reicht die Werte an einen lokalen MQTT-Broker weiter.

## 4. Lokales Modbus TCP

- Kein REST, sondern klassisches Modbus-TCP-Protokoll auf Port 502
- Muss vom **EcoFlow-Installateur/-Partner** über die EcoFlow **Pro App**
  freigeschaltet werden – standardmäßig deaktiviert (Quelle: https://docs.evcc.io/en/meters/ecoflow-powerocean-modbus)
- Kein offizielles Register-Mapping von EcoFlow; existierende Mappings sind
  Community-reverse-engineered (siehe `modbus-registers.md`)
- Bereits produktiv genutzt in:
    - Home Assistant Integrationen (`MaxGrmm/EF-PowerOcean-TcpModbus`,
      `windmark/EF-PowerOcean-TcpModbus`, `harduser-gnk/EF-PowerOcean-TcpModbus` – Fork mit Schreibzugriff)
    - evcc (Ladeinfrastruktur-Software) über eigenes Meter-Template
      `ecoflow-powerocean-modbus`

## 4a. Zugang zur EcoFlow Pro App

Die **Pro App** (`com.ecoflow.pro`) ist die Installateur-App und nur für autorisierte
Distributoren und Installateure freigeschaltet; Endkunden nutzen die normale EcoFlow-App.

- **Zugang:** Registrierung als Installateur im EcoFlow Pro Web Portal (https://pro-portal.ecoflow.com,
  EU-Instanz https://portal.ecoflow.com/pro/eu),
  Freischaltung der Rolle durch EcoFlow bzw. den Distributor. Ein registrierter, aber
  noch nicht freigeschalteter Account zeigt schlicht keine Anlagen.
- **Inbetriebnahme** in drei Schritten (Internet Setup → Home Setting → System Setting);
  das Gerät wird per QR-Code/Seriennummer erfasst und anschließend an das Besitzer-Konto
  gebunden. Danach zeigt die Pro App SN, Besitzer-Konto und Installationsdatum.
  (Quelle: https://energy.ecoflow.com/eu/software/EcoFlow-Pro-App)

**Zwei getrennte Bindungen** – das ist die häufigste Verwechslung:

| Bindung          | Woran                                                | Wofür nötig                   |
|------------------|------------------------------------------------------|-------------------------------|
| Anlagen-Bindung  | Konto des Installateurs, der in Betrieb genommen hat | Pro App, Modbus-Freischaltung |
| Besitzer-Bindung | EcoFlow-Konto des Eigentümers (Endkunden-App)        | Cloud-API, normale App        |

Ein eigener Pro-Account zeigt die Anlage deshalb **nicht** automatisch: Sie hängt am
Konto des Installateurs. Der Versuch, sie selbst hinzuzufügen, endet mit „System bereits
in einem anderen Konto hinzugefügt" (Erfahrungsberichte im Photovoltaikforum-Thread
218848, Seite 17). Auflösen lässt sich das nur über den ursprünglichen Installateur (Anlage löschen bzw. unter
*Installateur-/Benutzerverwaltung → Benutzer hinzufügen*
freigeben) oder über ein Support-Ticket bei EcoFlow (`solutionservice.eu@ecoflow.com`)
mit SN und Kaufbeleg.

**Wichtig:** Für die Modbus-Freischaltung ist keine Übertragung nötig – es genügt, dass *irgendein* Pro-Zugang den
Schalter einmalig umlegt.

## 4b. Checkliste für den Installateurstermin

Die Modbus-Freischaltung kann nur ein Installateur mit Pro-App-Zugang vornehmen (siehe
4a). Ein solcher Termin wiederholt sich nicht schnell – deshalb hier abhakbar, was dabei
zu klären ist.

**Vorab-Test, ob überhaupt noch etwas fehlt**

Ein `modbusread <ip> 42082 uint16` vor dem Termin beantwortet das in einer Sekunde, und
die Fehlermeldung unterscheidet die Fälle:

| Antwort              | Bedeutung                                                              |
|----------------------|------------------------------------------------------------------------|
| `connection refused` | Gerät erreichbar, auf Port 502 lauscht nichts → Modbus ist deaktiviert |
| Timeout              | Netz-/VLAN-/Firewall-Problem, nicht der Modbus-Schalter                |
| Registerwert         | schon freigeschaltet                                                   |

Am Testgerät (DC Fit, Firmware **1.0.6.20**, September 2026): Ping beantwortet,
Port 502 `connection refused` – also deaktiviert, wie dokumentiert.

**Das eigentliche Anliegen**

- [ ] **Modbus TCP am Wechselrichter aktivieren** – konkret: Wechselrichter in der Pro
  App auswählen und den **Control Mode auf „Modbus control"** stellen. So beschreibt
  es die Referenz-Integration; bitte bestätigen lassen, ob das Menü tatsächlich so
  heißt.
- [ ] Ändert dieser Modus etwas am internen Scheduling der Anlage? (Dieselbe Frage steht
  in „Offene Fragen" am Dateiende – dort beantworten, hier nur beim Termin erfragen)
- [ ] Überlebt die Einstellung ein Firmware-Update?
- [ ] **Register 40002 und 40003 auslesen** (`product_category`, `product_number`),
  sobald Modbus läuft: Die Referenz-Integration kann den DC Fit daran *nicht*
  erkennen, weil niemand die Werte kennt – ein Beitrag, der upstream fehlt.

**Damit man danach auch drankommt**

- [ ] IP-Adresse des Wechselrichters. Per DHCP vergeben? Dann im Router fest zuordnen,
  sonst zeigt jeder Poll irgendwann ins Leere.
- [ ] Port (Erwartung 502) und Unit-/Slave-ID (Erwartung 1) bestätigen lassen.
- [ ] Ist der Zugriff auf ein bestimmtes Netz/VLAN beschränkt?

**Für die Register-Frage (Plus vs. DC Fit)**

- [ ] Firmware-Version und genaue Modellbezeichnung/`InverterModel` erfragen. Das Mapping
  in `modbus-registers.md` wurde am PowerOcean **Plus** ermittelt, und die Firmware
  kennt modellabhängige `address_overrides`.
- [ ] Hat EcoFlow ihm gegenüber eine Registerliste dokumentiert? Unwahrscheinlich, aber
  der billigste Weg an offizielle Angaben.

**Wegen einer Anlagenerweiterung am selben Termin**

- [ ] Was kommt genau dazu (Batteriemodul, PV-String, PowerPulse/Wallbox)? Neue
  Komponenten können zusätzliche Register belegen.
- [ ] Ändern sich dadurch SN, Anlagen-ID oder Bindung? Dann sind die Befunde in diesem
  Dokument nachzuziehen.

**Unabhängig vom Termin**

- [ ] EcoFlow-Support (`solutionservice.eu@ecoflow.com`) fragen, ob die eigene SN für die
  Developer-API freigeschaltet werden kann. Für die PowerOcean-Familie wenig
  aussichtsreich, aber der einzige verbliebene Hebel auf der Cloud-Seite.

## 5. Was andere Integrationen können (und was nicht)

- **OpenHAB-Binding `org.openhab.binding.ecoflow`:** rein cloudbasiert über die
  Developer-API und unterstützt nur Delta 2, Delta 2 Max und PowerStream – **kein
  PowerOcean**. Für den DC Fit also kein Weg, weder lokal noch cloudseitig.
  Ein brauchbarer Hinweis steht trotzdem in dessen README: Ein Developer-Account lässt
  sich *nicht* mehrfach parallel verwenden, das stört die Event-Updates. Wer
  `scripts/ecoflow-api.sh` neben einer anderen Integration laufen lässt, sollte das
  wissen.
- **evcc** nutzt für den PowerOcean ausschließlich den **lokalen Modbus-Weg**
  (Meter-Template `ecoflow-powerocean-modbus`) – ein weiteres Indiz, dass der
  *dokumentierte* Cloud-Weg für diese Gerätefamilie nicht praktikabel ist. Für den
  App-MQTT-Kanal aus Abschnitt 3 sagt das nichts: Der trägt, ist aber inoffiziell und
  rückentwickelt, also nichts, worauf eine Integration bauen würde. Die dort verwendeten
  Registeradressen bestätigen die aktuelle Karte in `modbus-registers.md`.

## 6. "Offene API" in Shop-Beschreibungen

Verkaufsseiten für das DC-Fit-Set werben mit einer "offenen API-Schnittstelle"
zur Anbindung an EMS wie Solar Manager Connect 2 oder Loxone. Vermutlich ist
damit dieselbe lokale Modbus-Schnittstelle gemeint, nicht ein separates
REST-Interface – eine explizite Bestätigung dafür liegt aber nicht vor.

## Offene Fragen / weiter zu klären

- [ ] Gilt das Modbus-Register-Mapping (PowerOcean Plus) 1:1 für DC Fit, oder
  gibt es ein eigenes `InverterModel`-Mapping mit abweichenden Adressen?
  → `models.py` im Repo `MaxGrmm/EF-PowerOcean-TcpModbus` noch nicht geprüft.
- [ ] Genauer Menüpfad zum Modbus-Schalter in der EcoFlow Pro App. Aus fremder Quelle
  (`MaxGrmm/EF-PowerOcean-TcpModbus`) bekannt: Wechselrichter auswählen, Control Mode auf
  **„Modbus control"** umstellen – also ein Betriebsmodus-Wechsel, kein versteckter
  Schalter. **Am Gerät unbestätigt**, deshalb kein Haken; steht als Aufgabe in 4b
- [ ] Wirkt sich der Modus „Modbus control" auf das interne Scheduling aus? Bei rein
  lesendem Zugriff vermutlich folgenlos, belegt ist das nicht
- [x] Liefert das Präfix `HC31` (DC Fit) Fehler 1006? → **Ja, bei `quota/all`**;
  `device/list` listet das Gerät dagegen normal (September 2026)
- [x] Kommen auf dem MQTT-Topic `/open/<acct>/<SN>/quota` Nachrichten an? → **Nein**,
  Abo wird gewährt, Verbindung bleibt stehen, es wird nichts publiziert (kurze Beobachtung, September 2026)
- [x] Hilft der dokumentierte Anfrage-Weg über `.../get`? → **Nein**, der Publish wird
  mit PUBACK 0x87 „Not authorized" abgelehnt, das Abo auf `.../get_reply` mit
  SUBACK 0x80. Damit ist der **Open-API**-MQTT-Kanal vollständig ausgemessen. Der
  App-Kanal ist ein anderer und noch offen – siehe die nächsten drei Punkte.
- [x] Antwortet `/iot-auth/app/certification` mit dem Endkunden-Token, und lässt der
  Broker die Client-ID-Form `ANDROID_<hex>_<userId>` zu? → **Ja, beides** (22.09.2026);
  Frames treffen auf `/app/device/property/<SN>` ein
- [x] Liefert `.../thing/property/get_reply` beim DC Fit JSON? → **Es kam gar nichts**;
  der Push läuft trotzdem. Die Werte stecken ausschließlich in den Protobuf-Frames
- [x] Wird `measured` im REST-Endpunkt wieder frisch, solange `live` läuft? → **Nein.**
  Über zweieinhalb Minuten unverändert, während die Frames eine Stunde weiter waren.
  Der REST-Weg ist damit als Live-Quelle erledigt
- [x] Ändert ein häufigerer Weckruf den Meldetakt? → **Nein.** Mit
  `ECOFLOW_LIVE_INTERVAL=5` kam der Energiestrom weiterhin genau minütlich. Der Takt
  gehört dem Gerät, nicht dem Frager
- [x] Ist der Weckruf überhaupt nötig, oder genügt das Abo allein? → **Das Abo genügt.**
  23 Minuten ohne einen einzigen Publish, durchgehend Minutenwerte. Siehe Abschnitt 3
- [x] Wie sieht der Befehl für den schnellen Takt wirklich aus? → **Mitgelesen** auf dem
  `set`-Topic, während die App lief (22.09.2026). Bytes und Feldbelegung siehe oben;
  umgesetzt als Kommando `fast`
- [x] Hält der schnelle Takt durch? → **Ja, bei 3 s Wiederholung**; bei 10 s fällt das
  Gerät auf den Minutentakt zurück
- [x] Wie lange wirkt der Schalter nach? → **Rund 25 Sekunden**, sauber gemessen: Am
  22.09.2026 endete der letzte Schalter um 14:25:57Z, danach kamen noch genau sechs
  `96/33` im **4-Sekunden-Takt** (14:26:01 bis 14:26:21Z), dann nichts mehr über
  fünf weitere Minuten Zuhören. Bemerkenswert ist der Takt: Während der Schalter läuft,
  kommen die Berichte im 1–3-Sekunden-Abstand, im Nachlauf gleichmäßig alle 4 s.
  **Die frühere Angabe „rund vier Minuten" ist damit nicht bestätigt.** Ein Unterschied
  zwischen beiden Läufen ist bekannt und könnte die Erklärung sein: Hier wurde der
  MQTT-Client zwischen beiden Phasen **neu verbunden** (der Broker verlangt ohnehin eine
  frische Client-ID). Ob das Ende des Stroms an der Zeit hing oder am Verbindungsabbruch,
  trennt diese Messung **nicht** – dafür müsste dieselbe Verbindung stehen bleiben und
  nur das Schalten aufhören
- [x] Schaltet das Gerät den schnellen Strom je von selbst ein? → **Nein.** Die Kontrolle
  mit geschlossener App und geschlossenem Portal zeigte über 23 Minuten **keinen einzigen**
  `96/33`. In einem früheren Lauf war er ohne unser Zutun aufgetaucht; die Ursache war
  demnach **vermutlich** ein anderer Client – belegt ist nur die Negativkontrolle
- [x] Was trägt `cmd_func 254 / cmd_id 32`? → **Die Stundenhistorie des laufenden Tages**,
  sechs Flüsse à 24 Stundenwerte in Wh. Aufgeschlüsselt in Abschnitt 3, abrufbar mit
  `ecoflow-frames.py --hours`
- [x] Was tragen `96/3` und `96/137`? → `96/3` ist eine **Komponentenliste** mit vier
  Seriennummern, `96/137` hat eine **leere Nutzlast** und ist die Bestätigung auf den
  Stream-Schalter. Beide erscheinen nur bei aktivem Schalter. Siehe Abschnitt 3
- [x] Welche Rolle haben die Komponenten ab Feld 2 der Liste? → Über
  *System information → Component information* im Portal aufgelöst: Feld 2 ist der
  PV Storage Converter, die `HJ3A`-Einträge sind die Batteriemodule
- [ ] Warum weicht die Tagessumme der Stundenhistorie vom `todayElectricityGeneration`
  des Portals ab? **Ungeklärt, aber eingegrenzt.** Zwei Messungen am 22.09.2026:

  | Zeitpunkt | Portal | Gerät | Portal ist |
  |-----------|--------|-------|------------|
  | 07:13Z    | 1,74 kWh | 1,26 kWh | **höher** |
  | 14:21Z    | 14,00 kWh | 15,96 kWh | **niedriger** |

  Das Vorzeichen dreht sich im Lauf desselben Tages. Damit scheiden zwei naheliegende
  Erklärungen aus: **Nachlauf** kann einen steigenden Zähler nie zu hoch ablesen lassen,
  und ein **fester Wandlungsverlust** (14,00/15,96 = 88 %, für DC→AC plausibel) müsste
  in dieselbe Richtung wirken.

  Auf der Geräteseite ist der Wert dagegen doppelt belegt: Die Stundenhistorie
  (`254/32`) und das Summenfeld 23 in `96/1` stimmen im selben Sekundenfenster auf
  5,5 Wh überein. Der Unterschied liegt also nicht am Auspacken.

  Was als Nächstes weiterhilft: **nicht** zwei Stichproben, sondern dieselbe Größe über
  einen Tag mehrfach parallel abgefragt. Vorsicht bei der Zeitachse – im Mitschnitt vom
  Vormittag lag der Portal-Zeitstempel (`07:13:28Z`) genau zwei Stunden hinter der
  Gerätezeit derselben Aufnahme (`09:13:18Z`); das ist der bekannte Einfrier-Effekt,
  keine Zeitzone: Am Nachmittag stimmten Gerätezeit und UTC auf die Sekunde
- [x] Was tragen die übrigen `cmd_id` (1, 108, 109, 110, 111, 136)? → **Aufgeschlüsselt**,
  siehe Abschnitt 3. Kurz: `96/1` ist der Systembericht und trägt die Tagessummen als
  Float (gegen die Stundenhistorie auf wenige Wh belegt), `96/108` den Bericht je
  Batteriemodul, `96/111` ein Modul im Detail samt Seriennummer und 16 Zellspannungen,
  `96/136` eine Konstante, `96/109` den PV-Strangbericht (zwei Stränge, Spannung mal
  Strom ergibt die PV-Leistung auf 3 % genau). Offen bleibt allein `96/110`

## Quellenübersicht

- https://github.com/Feberdin/ecoflow-powerocean-ha
- https://github.com/MaxGrmm/ecoflow-poweroceanplus-modbus
- https://github.com/MaxGrmm/EF-PowerOcean-TcpModbus
- https://github.com/windmark/EF-PowerOcean-TcpModbus
- https://github.com/harduser-gnk/EF-PowerOcean-TcpModbus
- https://docs.evcc.io/en/meters/ecoflow-powerocean-modbus
- https://www.photovoltaikforum.com/thread/247994-ecoflow-powerocean-modbus-protokoll/
- https://www.photovoltaikforum.com/thread/218848-erfahrungen-mit-system-ecoflow-powerocean/?pageNo=17
- https://github.com/shuette42/ecoflow-energy-ha (Enhanced Mode: AES-Certification,
  Client-ID-Bau, Keepalive-Intervalle)
- https://github.com/tolwi/hassio-ecoflow-cloud (`app/certification`, Client-ID-Form)
- https://github.com/jensfr1/ha-ecoflow-ocean2 (Weckruf-Intervall mit Begründung)
- https://github.com/foxthefox/ioBroker.ecoflow-mqtt (Gerätetyp `poweroceanfit`:
  abweichende Protobuf-Feldnummern für den DC Fit)
- https://github.com/evcc-io/evcc – `templates/definition/meter/ecoflow-powerocean-modbus.yaml`
- https://github.com/openhab/openhab-addons – `bundles/org.openhab.binding.ecoflow`
- https://developer.ecoflow.com/us/document/introduction
- https://developer-eu.ecoflow.com
- https://developer-eu.ecoflow.com/us/document/PP2 (offizielle PowerOcean-Doku:
  Endpunkte, Feldnamen, MQTT-Topics)
- https://developer-eu.ecoflow.com/us/document/root (Signaturverfahren inkl.
  Testvektor, Flattening-Regeln, generische Endpunkte, MQTT-Topics)
- https://energy.ecoflow.com/eu/software/EcoFlow-Pro-App
- https://pro-portal.ecoflow.com
- https://energy.ecoflow.com/eu/support
