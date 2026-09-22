# API-Status – EcoFlow PowerOcean DC Fit

Stand der Recherche: September 2026.

## Kurzfassung

Es gibt **keine** offiziell dokumentierte, spezifische REST-API für den DC Fit.
Zwei Wege existieren, beide mit Einschränkungen:

| Weg                        | Typ                          | Status                                                                                                                                                 |
|----------------------------|------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------|
| EcoFlow Developer/Open API | Cloud, REST, HMAC-signiert   | Listet den DC Fit, verweigert aber die Messwerte: Fehler 1006 „not allowed" – **am Gerät bestätigt**, gilt ebenso für viele PowerOcean-/Plus-Varianten |
| Lokales Modbus TCP         | Modbus (kein REST), Port 502 | Funktioniert, aber inoffiziell, muss vom Installateur freigeschaltet werden, kein offizielles Register-Mapping                                         |

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
  oben durchgemessen – **es gibt keinen weiteren Cloud-Weg, der noch offen wäre.**
- Relevante Leseendpunkte: `/iot-open/sign/device/list` (Geräte des Kontos),
  `/iot-open/sign/device/quota/all?sn=…` (alle Werte eines Geräts),
  `/iot-open/sign/certification` (MQTT-Zugangsdaten: `certificateAccount`,
  `certificatePassword`, `url` = `mqtt-e.ecoflow.com`, `port` = 8883, MQTTS).
- MQTT-Topics je Gerät: `/open/<certificateAccount>/<SN>/quota` und `.../status`
  (Gerät → App) sowie `.../get`, `.../set` mit ihren `_reply`-Gegenstücken (App → Gerät). `.../set` bleibt bewusst
  außerhalb von `scripts/ecoflow-api.sh`;
  `.../get` ist über das Kommando `request` erreichbar, dessen Topic-Suffix fest
  verdrahtet ist.
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

### Der Weg, der trotzdem funktioniert: das Endkunden-Portal

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

Die Erklärung, die dazu passt: **Der REST-Endpunkt pollt das Gerät nicht.** Er gibt
heraus, was zuletzt in die Cloud gepusht wurde. Gepusht wird aber offenbar nur, solange
ein Client aktiv nachfragt – ist keiner da, versiegt der Strom und der Cloud-Stand friert
ein. Ein `status`-Aufruf sieht dann aus wie ein Live-Wert und ist ein Standbild.

Fremdbelege für denselben Mechanismus bei anderen EcoFlow-Geräten:

- `jensfr1/ha-ecoflow-ocean2` hält dafür ein eigenes Intervall von 60 s vor, mit dem
  Kommentar „Ohne diesen regelmaessigen Weckruf sendet das Geraet keine Telemetrie,
  solange keine EcoFlow-App geoeffnet ist."
- `shuette42/ecoflow-energy-ha` schickt nach jedem Verbindungsaufbau und danach alle
  20–30 s eine Anfrage nach; für den PowerOcean **Plus** ist dort zusätzlich vermerkt, dass
  manche Geräte nur direkt nach einem Abo antworten.
- Die openHAB-Anbindung berichtet dasselbe für den STREAM Micro über die offizielle API:
  Updates nur bei geöffneter App, sonst alle 13–16 Minuten.

**Einschränkung:** Das ist eine Beobachtung an einem Gerät plus Fremdbelege für andere
Modelle, keine bewiesene Ursache. Ob die Drosselung im Gerät oder in der Cloud sitzt, ist
von außen nicht zu unterscheiden. Nachmessbar ist es mit `scripts/ecoflow-api.sh live`
(siehe unten) in einem Terminal und `status` in einem zweiten.

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
180 Netzschutzparameter. Es bleiben der lokale Modbus-Weg (Abschnitt 2) und – mit
allen Nachteilen – die inoffizielle App-Cloud (Abschnitt 2b).

### Der MQTT-Kanal der App

Der Kanal, den die App benutzt – und der einzige Cloud-Kanal, auf dem für dieses Gerät
tatsächlich Nachrichten ankommen. Abonnierbar mit `scripts/ecoflow-api.sh live <SN>`.

**Broker:** `mqtt-e.ecoflow.com:8883` (MQTTS), Host und Port kommen aus der
Certification-Antwort; Benutzername und Passwort sind `certificateAccount` und
`certificatePassword` daraus – nicht die Kontodaten, nicht das Token.

**Client-ID:** `ANDROID_<32 Hex-Zeichen, Großbuchstaben>_<userId>`. Das ist keine
Kosmetik: Der Broker weist Client-IDs ab, die nicht so aussehen, und er weist eine bereits
gesehene ID nach dem Verbindungsabbruch erneut ab. Deshalb baut `live` für **jede**
Verbindung eine neue.

**Topics** (Wildcards meiden – die ACL lehnt sie ab, siehe oben):

| Topic                                           | Richtung  | Inhalt                          |
|-------------------------------------------------|-----------|---------------------------------|
| `/app/device/property/<SN>`                     | subscribe | Telemetrie-Push, **Protobuf**   |
| `/app/<userId>/<SN>/thing/property/get`         | publish   | Anfrage/Weckruf                 |
| `/app/<userId>/<SN>/thing/property/get_reply`   | subscribe | Antwort darauf                  |
| `/app/device/status/<SN>`                       | subscribe | online/offline                  |
| `/app/<userId>/<SN>/thing/property/set`         | publish   | **schreibend – hier tabu**      |

**Der Weckruf**, den `live` alle `ECOFLOW_LIVE_INTERVAL` Sekunden (Default 30) auf das
`get`-Topic schickt:

```json
{"from":"Android","id":"<Millisekunden>","moduleType":0,
 "operateType":"latestQuotas","params":{},"version":"1.0"}
```

**Was `live` bewusst nicht tut:** Die App aktiviert ihren schnellen Stream (~3 s) über
einen Protobuf-Frame `EnergyStreamSwitch` auf dem `.../set`-Topic. Das Skript publiziert
grundsätzlich nur auf `get`-Topics, damit kein Schreibpfad existiert, der versehentlich
das Gerät verstellen könnte – dieselbe Regel, nach der `modbusread` keine `Write*`-Methode
aufruft. Der Preis: `live` bekommt vermutlich nur den Takt seiner eigenen Anfragen.

**Format:** Der Push ist beim PowerOcean **Protobuf**, nicht JSON – `jq` hilft dort nicht.
`live` gibt deshalb jede Nachricht als Hex aus und schreibt den Text nur dann zusätzlich
hin, wenn die Nutzlast vollständig druckbar ist. Ob `get_reply` beim DC Fit JSON
(`data.quotaMap`) oder Protobuf liefert, ist offen – das ist die erste Frage, die eine
Messung beantworten muss.

**Vorbehalt Plus vs. DC Fit.** Die Protobuf-Feldnummern unterscheiden sich zwischen den
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

**1. Der Weckruf wird nicht beantwortet.** In zweieinhalb Minuten kam auf
`.../thing/property/get_reply` keine einzige Nachricht. Der Push läuft trotzdem – das Abo
allein scheint zu genügen. Ob der Weckruf überflüssig ist oder ob er den Push erst
auslöst, ist damit nicht entschieden.

**2. Der REST-Endpunkt wird davon nicht frisch.** `status` lieferte während der ganzen
Zeit unverändert `measured : 2026-09-22T07:13:28Z`, während die MQTT-Frames bereits
`08:19Z` trugen – über eine Stunde Unterschied. **Der Umweg über `provider-service` ist
also keine Live-Quelle, auch nicht mit laufendem Zuhörer.** Wer aktuelle Werte will, muss
die Frames auswerten.

**3. Die Nutzlast ist XOR-verschleiert.** Jedes Byte der Nutzlast ist mit dem niederwertigen
Byte der Sequenznummer (Header-Feld 14) verodert. Aufgefallen ist das daran, dass zwei
Frames mit benachbarten Sequenznummern sich in *jedem* Byte um dasselbe Bitmuster
unterscheiden. Ohne diesen Schritt ist die Nutzlast kein gültiges Protobuf.

**Rahmenaufbau** (Feldnummern des äußeren Headers, bestätigt):

| Feld | Bedeutung                                |
|------|------------------------------------------|
| 1    | Nutzlast (XOR-verschleiert, s.o.)        |
| 8    | `cmd_func` – bei allen Frames hier 96    |
| 9    | `cmd_id` – unterscheidet die Berichte    |
| 14   | Sequenznummer, zugleich der XOR-Schlüssel |

**Beobachtete `cmd_id` bei `cmd_func 96`:** 1, **34**, 108, 109, 110, 111, 136.

**Der Energiestrom liegt auf `cmd_id 34`** – nicht auf 33, wie die Fremdquelle für den DC
Fit angibt. Die Nutzlast enthält eine eingebettete Nachricht mit dieser Belegung:

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
eine Frame alle `ECOFLOW_FAST_INTERVAL` Sekunden (Default 10). Es ist das **einzige**
Kommando des Skripts, das auf ein `set`-Topic publiziert, es trägt keine Parameter, und
es ist absichtlich ein eigenes Kommando – damit der Schreibzugriff nie als Nebenwirkung
einer Werteabfrage passiert.

Ausgewertet wird das von `scripts/ecoflow-frames.py`, das die Ausgabe von `live` auf
stdin nimmt. Der schnellere ~3-Sekunden-Takt, den die App über `.../set` freischaltet,
ist damit weiterhin nicht erreicht – für einen Minutentakt braucht es ihn aber auch nicht.

## 2. Lokales Modbus TCP

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

## 2a. Zugang zur EcoFlow Pro App

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

## 2b. Inoffizielle App-Cloud ("Enhanced Mode")

Dritter Weg, der die 1006-Sperre umgeht: Anmeldung mit den normalen
EcoFlow-Kontozugangsdaten statt mit API-Keys, danach Push der Messwerte über WSS/MQTT (~2–4 s statt ~30 s Polling).
Genau das nutzen die Home-Assistant-Integrationen für die
gesperrten Modelle. **Community-Weg ohne jede Zusage von EcoFlow**: kann jederzeit
brechen, und die Kontozugangsdaten liegen im Klartext in der Konfiguration.
(Quelle: https://github.com/shuette42/ecoflow-energy-ha)

Dieses Repo geht genau diesen Weg, aber nur bis zur Hälfte: `scripts/ecoflow-api.sh live`
abonniert den Kanal und hält ihn wach, dekodiert die Protobuf-Frames aber nicht. Der
Aufbau ist oben unter „Der MQTT-Kanal der App" beschrieben.

## 2c. Checkliste für den Installateurstermin

Die Modbus-Freischaltung kann nur ein Installateur mit Pro-App-Zugang vornehmen (siehe
2a). Ein solcher Termin wiederholt sich nicht schnell – deshalb hier abhakbar, was dabei
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
- [ ] Ändert dieser Modus etwas am internen Scheduling der Anlage?
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

## 2d. Was andere Integrationen können (und was nicht)

- **OpenHAB-Binding `org.openhab.binding.ecoflow`:** rein cloudbasiert über die
  Developer-API und unterstützt nur Delta 2, Delta 2 Max und PowerStream – **kein
  PowerOcean**. Für den DC Fit also kein Weg, weder lokal noch cloudseitig.
  Ein brauchbarer Hinweis steht trotzdem in dessen README: Ein Developer-Account lässt
  sich *nicht* mehrfach parallel verwenden, das stört die Event-Updates. Wer
  `scripts/ecoflow-api.sh` neben einer anderen Integration laufen lässt, sollte das
  wissen.
- **evcc** nutzt für den PowerOcean ausschließlich den **lokalen Modbus-Weg**
  (Meter-Template `ecoflow-powerocean-modbus`) – ein weiteres Indiz, dass der Cloud-Weg
  für diese Gerätefamilie nicht praktikabel ist. Die dort verwendeten Registeradressen
  bestätigen die aktuelle Karte in `modbus-registers.md`.

## 3. "Offene API" in Shop-Beschreibungen

Verkaufsseiten für das DC-Fit-Set werben mit einer "offenen API-Schnittstelle"
zur Anbindung an EMS wie Solar Manager Connect 2 oder Loxone. Vermutlich ist
damit dieselbe lokale Modbus-Schnittstelle gemeint, nicht ein separates
REST-Interface – eine explizite Bestätigung dafür liegt aber nicht vor.

## Offene Fragen / weiter zu klären

- [ ] Gilt das Modbus-Register-Mapping (PowerOcean Plus) 1:1 für DC Fit, oder
  gibt es ein eigenes `InverterModel`-Mapping mit abweichenden Adressen?
  → `models.py` im Repo `MaxGrmm/EF-PowerOcean-TcpModbus` noch nicht geprüft.
- [x] Genauer Menüpfad zum Modbus-Schalter in der EcoFlow Pro App → laut
  `MaxGrmm/EF-PowerOcean-TcpModbus`: Wechselrichter auswählen, Control Mode auf **„Modbus control"** umstellen. Also ein
  Betriebsmodus-Wechsel, kein
  versteckter Schalter. Am Gerät noch zu bestätigen (siehe 2c)
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
- [ ] Ist der Weckruf damit überhaupt nötig, oder genügt das Abo allein? Naheliegend nach
  dem Befund oben, aber ungeprüft – dafür bräuchte es einen Lauf ganz ohne Weckruf
- [x] Wie sieht der Befehl für den schnellen Takt wirklich aus? → **Mitgelesen** auf dem
  `set`-Topic, während die App lief (22.09.2026). Bytes und Feldbelegung siehe oben;
  umgesetzt als Kommando `fast`
- [ ] Hält der schnelle Takt auch durch? Der Frame wird alle 10 s wiederholt, weil die
  App das ebenso tut – ob das nötig ist und ob der Stream von selbst wieder ausgeht,
  ist ungeprüft
- [ ] Was tragen die übrigen `cmd_id` (1, 108, 109, 110, 111, 136)? Nach den Namen der
  Fremdquelle EMS-Heartbeat, Batterie- und DCDC-Berichte – ungeprüft

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
