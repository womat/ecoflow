# API-Status – EcoFlow PowerOcean DC Fit

Stand der Recherche: September 2026.

## Kurzfassung

Es gibt **keine** offiziell dokumentierte, spezifische REST-API für den DC Fit.
Zwei Wege existieren, beide mit Einschränkungen:

| Weg | Typ | Status |
|---|---|---|
| EcoFlow Developer/Open API | Cloud, REST, HMAC-signiert | Listet den DC Fit, verweigert aber die Messwerte: Fehler 1006 „not allowed" – **am Gerät bestätigt**, gilt ebenso für viele PowerOcean-/Plus-Varianten |
| Lokales Modbus TCP | Modbus (kein REST), Port 502 | Funktioniert, aber inoffiziell, muss vom Installateur freigeschaltet werden, kein offizielles Register-Mapping |

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
  (Gerät → App) sowie `.../get`, `.../set` mit ihren `_reply`-Gegenstücken
  (App → Gerät). `.../set` bleibt bewusst außerhalb von `scripts/ecoflow-api.sh`;
  `.../get` ist über das Kommando `request` erreichbar, dessen Topic-Suffix fest
  verdrahtet ist.
- Fertig signiert aufrufbar mit [`scripts/ecoflow-api.sh`](./scripts/ecoflow-api.sh).

### Fehler 1006 ist eine Modell-Sperrliste

**Bekanntes Problem:** Für die PowerOcean-Familie liefert die API häufig Fehlercode
**1006 "not allowed"**; auch das MQTT-Topic der Open API liefert dann keine Daten.
Das ist keine Fehlkonfiguration, sondern eine geräteseitige Sperrliste – die
Integration `shuette42/ecoflow-energy-ha` nennt es „an EcoFlow API limitation, not a
configuration problem" und führt die betroffenen SN-Präfixe:

| Gerätegruppe | Gesperrte SN-Präfixe (Fehler 1006) |
|---|---|
| PowerOcean | `J327`, `J32D`, `J32E` |
| PowerOcean DC Fit | `HC31` (hier selbst gemessen, in den Community-Listen nicht geführt) |
| PowerOcean Plus | `R371`, `R372`, `R374`, `HJ3C` |
| Stream / STREAM | `BK01`, `BK21`, `ES21`, `ES22` |
| Sonstige | `HZ31`, `S02F` (Solar Tracker), `AC71` (WAVE 3) |

Als über die Developer-API *erreichbar* führt dieselbe Quelle die PowerOcean-Präfixe
`HJ31`, `HJ32`, `HJ35`, `HJ36`, `HJ37`, `J32B`, `J329`.

**Der DC Fit (`HC31`) ist gesperrt – am Gerät gemessen, September 2026.** Die Sperre
greift aber erst beim Datenabruf, nicht beim Auflisten. Gemessen mit
`scripts/ecoflow-api.sh` am eigenen Konto:

| Aufruf | Antwort |
|---|---|
| `GET /iot-open/sign/device/list` | `"code": "0"`, Gerät gelistet mit `"online": 1` und `"productName": "PowerOcean DC Fit"` |
| `GET /iot-open/sign/device/quota/all?sn=…` | `"code": "1006"`, `"current device is not allowed to get device info"` |
| `POST /iot-open/sign/device/quota` (gezielte Werte, der in der offiziellen PowerOcean-Doku beschriebene Weg) | ebenfalls **1006** |

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

Auch wenn die Endpunkte für den DC Fit gesperrt sind: EcoFlows PowerOcean-Doku
(developer-eu.ecoflow.com, Dokument „PP2") benennt die Größen, die das Gerät kennt.
Das ist die einzige *offizielle* Quelle für diese Semantik und damit die beste
Gegenprobe für die community-ermittelten Modbus-Register in `modbus-registers.md`:

| Feld | Typ | Bedeutung |
|---|---|---|
| `bpSoc` | float | Batterie-SOC |
| `bpPwr` | float | Batterieleistung |
| `mpptPwr` | float | MPPT-/PV-Leistung |
| `sysLoadPwr` | float | Lastleistung |
| `sysGridPwr` | float | Netzleistung |
| `pcsAPhase`, `pcsBPhase`, `pcsCPhase` | json | je Phase: `vol`, `amp`, `actPwr`, `reactPwr`, `apparentPwr` |
| `mpptHeartBeat` | json | Liste `mpptPv` mit je `vol`, `amp`, `pwr` |
| `evPwr`, `chargingStatus`, `errorCode` | – | PowerPulse (Wallbox) |

Vorzeichen-Konvention aus den Beispielen: negative `actPwr`/`sysGridPwr` bedeuten
Einspeisung, negative `bpPwr` Entladung.

Ebenfalls dokumentiert: `POST /iot-open/sign/device/quota/data` für historische Werte
(Zeitraum höchstens eine Woche, z.B. `code: JT303_Dashboard_Overview_Summary_Week`), und
die MQTT-Topics `/open/${certificateAccount}/${sn}/quota` sowie `.../status` – Letzteres
mit `params.status` (0 = offline, 1 = online).

### MQTT-Weg der Open API (DC Fit, September 2026)

Der MQTT-Pfad verhält sich **anders als der REST-Pfad** – die Sperre greift hier nicht
beim Verbindungsaufbau:

| Schritt | Ergebnis |
|---|---|
| `/iot-open/sign/certification` | Code 0, liefert `certificateAccount`, Passwort, `mqtt-e.ecoflow.com`, Port 8883 |
| CONNECT (MQTTS, Port 8883) | `CONNACK (0)` – Authentifizierung akzeptiert |
| SUBSCRIBE auf `/open/<acct>/<SN>/#` | **abgelehnt** („All subscription requests were denied") |
| SUBSCRIBE auf `/open/<acct>/<SN>/quota` | **gewährt** (`SUBACK`, Granted QoS 0) |
| SUBSCRIBE auf `/open/<acct>/<SN>/status` | **gewährt** |
| SUBSCRIBE auf `/open/<acct>/<SN>/get_reply` | **abgelehnt** (`SUBACK` 128 = 0x80) |
| PUBLISH auf `/open/<acct>/<SN>/get` | **abgelehnt** (`PUBACK` RC 135 = 0x87 „Not authorized") |

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
zum Auslesen von Messwerten nicht nutzbar – weder über REST (1006) noch über MQTT
(zwei abonnierbare, aber stumme Topics; Anfragen per ACL verboten). Es bleiben der lokale Modbus-Weg (Abschnitt 2) und – mit
allen Nachteilen – die inoffizielle App-Cloud (Abschnitt 2b).

## 2. Lokales Modbus TCP

- Kein REST, sondern klassisches Modbus-TCP-Protokoll auf Port 502
- Muss vom **EcoFlow-Installateur/-Partner** über die EcoFlow **Pro App**
  freigeschaltet werden – standardmäßig deaktiviert
  (Quelle: https://docs.evcc.io/en/meters/ecoflow-powerocean-modbus)
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

- **Zugang:** Registrierung als Installateur im EcoFlow Pro Web Portal
  (https://pro-portal.ecoflow.com, EU-Instanz https://portal.ecoflow.com/pro/eu),
  Freischaltung der Rolle durch EcoFlow bzw. den Distributor. Ein registrierter, aber
  noch nicht freigeschalteter Account zeigt schlicht keine Anlagen.
- **Inbetriebnahme** in drei Schritten (Internet Setup → Home Setting → System Setting);
  das Gerät wird per QR-Code/Seriennummer erfasst und anschließend an das Besitzer-Konto
  gebunden. Danach zeigt die Pro App SN, Besitzer-Konto und Installationsdatum.
  (Quelle: https://energy.ecoflow.com/eu/software/EcoFlow-Pro-App)

**Zwei getrennte Bindungen** – das ist die häufigste Verwechslung:

| Bindung | Woran | Wofür nötig |
|---|---|---|
| Anlagen-Bindung | Konto des Installateurs, der in Betrieb genommen hat | Pro App, Modbus-Freischaltung |
| Besitzer-Bindung | EcoFlow-Konto des Eigentümers (Endkunden-App) | Cloud-API, normale App |

Ein eigener Pro-Account zeigt die Anlage deshalb **nicht** automatisch: Sie hängt am
Konto des Installateurs. Der Versuch, sie selbst hinzuzufügen, endet mit „System bereits
in einem anderen Konto hinzugefügt" (Erfahrungsberichte im Photovoltaikforum-Thread
218848, Seite 17). Auflösen lässt sich das nur über den ursprünglichen Installateur
(Anlage löschen bzw. unter *Installateur-/Benutzerverwaltung → Benutzer hinzufügen*
freigeben) oder über ein Support-Ticket bei EcoFlow (`solutionservice.eu@ecoflow.com`)
mit SN und Kaufbeleg.

**Wichtig:** Für die Modbus-Freischaltung ist keine Übertragung nötig – es genügt, dass
*irgendein* Pro-Zugang den Schalter einmalig umlegt.

## 2b. Inoffizielle App-Cloud ("Enhanced Mode")

Dritter Weg, der die 1006-Sperre umgeht: Anmeldung mit den normalen
EcoFlow-Kontozugangsdaten statt mit API-Keys, danach Push der Messwerte über WSS/MQTT
(~2–4 s statt ~30 s Polling). Genau das nutzen die Home-Assistant-Integrationen für die
gesperrten Modelle. **Community-Weg ohne jede Zusage von EcoFlow**: kann jederzeit
brechen, und die Kontozugangsdaten liegen im Klartext in der Konfiguration.
(Quelle: https://github.com/shuette42/ecoflow-energy-ha)

## 2c. Checkliste für den Installateurstermin

Die Modbus-Freischaltung kann nur ein Installateur mit Pro-App-Zugang vornehmen (siehe
2a). Ein solcher Termin wiederholt sich nicht schnell – deshalb hier abhakbar, was dabei
zu klären ist.

**Vorab-Test, ob überhaupt noch etwas fehlt**

Ein `modbusread <ip> 42082 uint16` vor dem Termin beantwortet das in einer Sekunde, und
die Fehlermeldung unterscheidet die Fälle:

| Antwort | Bedeutung |
|---|---|
| `connection refused` | Gerät erreichbar, auf Port 502 lauscht nichts → Modbus ist deaktiviert |
| Timeout | Netz-/VLAN-/Firewall-Problem, nicht der Modbus-Schalter |
| Registerwert | schon freigeschaltet |

Am Testgerät (DC Fit, Firmware **1.0.6.20**, September 2026): Ping beantwortet,
Port 502 `connection refused` – also deaktiviert, wie dokumentiert.

**Das eigentliche Anliegen**

- [ ] **Modbus TCP am Wechselrichter aktivieren.**
- [ ] Menüpfad bzw. Bezeichnung des Schalters notieren – das schließt die letzte offene
      Frage unten.
- [ ] Überlebt die Einstellung ein Firmware-Update?

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

## 3. "Offene API" in Shop-Beschreibungen

Verkaufsseiten für das DC-Fit-Set werben mit einer "offenen API-Schnittstelle"
zur Anbindung an EMS wie Solar Manager Connect 2 oder Loxone. Vermutlich ist
damit dieselbe lokale Modbus-Schnittstelle gemeint, nicht ein separates
REST-Interface – eine explizite Bestätigung dafür liegt aber nicht vor.

## Offene Fragen / weiter zu klären

- [ ] Gilt das Modbus-Register-Mapping (PowerOcean Plus) 1:1 für DC Fit, oder
      gibt es ein eigenes `InverterModel`-Mapping mit abweichenden Adressen?
      → `models.py` im Repo `MaxGrmm/EF-PowerOcean-TcpModbus` noch nicht geprüft.
- [ ] Genauer Menüpfad zum Modbus-Schalter in der EcoFlow Pro App: in welchem der
      drei Inbetriebnahme-Schritte bzw. Gerätemenüs liegt er, und ist er nachträglich
      erreichbar? (Der Zugangsweg zur Pro App selbst ist geklärt, siehe 2a; die Fragen
      für den Installateurstermin stehen in 2c.)
- [x] Liefert das Präfix `HC31` (DC Fit) Fehler 1006? → **Ja, bei `quota/all`**;
      `device/list` listet das Gerät dagegen normal (September 2026)
- [x] Kommen auf dem MQTT-Topic `/open/<acct>/<SN>/quota` Nachrichten an? → **Nein**,
      Abo wird gewährt, Verbindung bleibt stehen, es wird nichts publiziert
      (kurze Beobachtung, September 2026)
- [x] Hilft der dokumentierte Anfrage-Weg über `.../get`? → **Nein**, der Publish wird
      mit PUBACK 0x87 „Not authorized" abgelehnt, das Abo auf `.../get_reply` mit
      SUBACK 0x80. Damit ist MQTT vollständig ausgemessen.

## Quellenübersicht

- https://github.com/Feberdin/ecoflow-powerocean-ha
- https://github.com/MaxGrmm/ecoflow-poweroceanplus-modbus
- https://github.com/MaxGrmm/EF-PowerOcean-TcpModbus
- https://github.com/windmark/EF-PowerOcean-TcpModbus
- https://github.com/harduser-gnk/EF-PowerOcean-TcpModbus
- https://docs.evcc.io/en/meters/ecoflow-powerocean-modbus
- https://www.photovoltaikforum.com/thread/247994-ecoflow-powerocean-modbus-protokoll/
- https://www.photovoltaikforum.com/thread/218848-erfahrungen-mit-system-ecoflow-powerocean/?pageNo=17
- https://github.com/shuette42/ecoflow-energy-ha
- https://developer.ecoflow.com/us/document/introduction
- https://developer-eu.ecoflow.com
- https://developer-eu.ecoflow.com/us/document/PP2 (offizielle PowerOcean-Doku:
  Endpunkte, Feldnamen, MQTT-Topics)
- https://developer-eu.ecoflow.com/us/document/root (Signaturverfahren inkl.
  Testvektor, Flattening-Regeln, generische Endpunkte, MQTT-Topics)
- https://energy.ecoflow.com/eu/software/EcoFlow-Pro-App
- https://pro-portal.ecoflow.com
- https://energy.ecoflow.com/eu/support
