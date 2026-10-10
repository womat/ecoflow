# ecoflowd – Deutsche Kurzfassung

🇬🇧 [Full documentation in English](README.md)

**ecoflowd bringt die Live-Werte einer EcoFlow PowerOcean (DC Fit) auf deinen lokalen
MQTT-Broker – für evcc, Home Assistant, Node-RED oder alles andere, das MQTT spricht.**

ecoflowd meldet sich an wie die EcoFlow-App, abonniert den Cloud-Kanal des Geräts und
veröffentlicht, was ankommt, als zwei JSON-Telegramme:

- **`ecoflow/state`** jede Minute: PV, Hausverbrauch, Batterie, Netz (W) und Ladestand (%),
- **`ecoflow/energy`** die Tagessummen (Wh).

Auf Wunsch schaltet es den Sekundentakt ein (`--fast`) oder hält die Batterie auf Anfrage
vom Entladen ab, z. B. während das Auto lädt (`--block`). Ohne diese beiden Optionen
sendet es nichts an das Gerät. Mit `--listen` zeigt eine Web-Seite Ladestand und Richtung
des Akkus, PV, Haus und Netz – nur lesend, siehe [Web page](README.md#web-page---listen).

> **Inoffiziell.** EcoFlow dokumentiert diesen Kanal nicht: Login, Frame-Aufbau und
> Feldnummern sind gemessen und können sich jederzeit ändern. Wie sie ermittelt wurden,
> steht in den [Forschungsnotizen](docs/research/README.md).

## In vier Schritten

1. **Seriennummer** des Geräts aus der App holen (beginnt beim DC Fit mit `HC31`) und die
   Zugangsdaten des **App-Kontos** bereithalten – kein Entwickler-Schlüssel nötig.
2. **Installieren** – entweder:
   - **Docker:** `docker-compose.yaml` und `.env.example` aus dem Repo laden, `.env`
     ausfüllen, `chmod 600 .env`, `docker compose up -d`. Das Image
     `ghcr.io/womat/ecoflowd` läuft auf jedem Raspberry Pi.
   - **systemd:** Archiv für deinen Rechner von den
     [Releases](https://github.com/womat/ecoflow/releases/latest) laden (`linux_arm64` für
     einen Pi mit 64-Bit-System, `linux_armv7` für 32 Bit, `linux_armv6` für Pi 1 und Zero).
     Programm und Unit liegen im Archiv; die Zugangsdaten kommen nach `/etc/ecoflowd/env`.
3. **Broker eintragen:** `--broker tcp://…:1883`, bei Docker `ECOFLOWD_BROKER` in `.env`.
4. **Einbinden:** in evcc oder Home Assistant auf `ecoflow/state` hören – mit `timeout`
   bzw. `expire_after`, damit ein eingefrorener Wert auffällt.

**`.env` bzw. `/etc/ecoflowd/env` enthält das Passwort des EcoFlow-Kontos** und gehört
nur root bzw. dir (`chmod 600`).

Die genauen Befehle stehen unter [Quick start: Docker](README.md#quick-start-docker) und
[Quick start: systemd](README.md#quick-start-systemd), das Telegrammformat und Beispiele
für evcc und Home Assistant unter [MQTT output](README.md#mqtt-output).

## Lizenz

MIT, siehe [`LICENSE`](LICENSE). Das fertige Programm enthält den Eclipse-Paho-MQTT-Client
(EPL-2.0 oder EDL-1.0), siehe [Third-party licenses](README.md#third-party-licenses).
