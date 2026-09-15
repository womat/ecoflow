# API-Status – EcoFlow PowerOcean DC Fit

Stand der Recherche: September 2026.

## Kurzfassung

Es gibt **keine** offiziell dokumentierte, spezifische REST-API für den DC Fit.
Zwei Wege existieren, beide mit Einschränkungen:

| Weg | Typ | Status |
|---|---|---|
| EcoFlow Developer/Open API | Cloud, REST, HMAC-signiert | Existiert generisch für alle EcoFlow-Geräte, liefert für PowerOcean-Familie teils Fehler 1006 „not allowed" |
| Lokales Modbus TCP | Modbus (kein REST), Port 502 | Funktioniert, aber inoffiziell, muss vom Installateur freigeschaltet werden, kein offizielles Register-Mapping |

## 1. EcoFlow Developer/Open API (Cloud)

- Endpunkt: developer.ecoflow.com
- REST-basiert, Requests HMAC-signiert
- Deckt grundsätzlich die gesamte EcoFlow-Geräteflotte ab
- **Bekanntes Problem:** Für PowerOcean (Plus, vermutlich auch DC Fit, da gleiche
  Steuerplattform) liefert die API häufig Fehlercode **1006 "not allowed"**.
  Auch das MQTT-Topic der Open API liefert in diesen Fällen keine Daten.
- Quelle: https://github.com/Feberdin/ecoflow-powerocean-ha (README)

**Fazit:** Für PowerOcean-Geräte in der Praxis meist nicht nutzbar, obwohl die
API-Infrastruktur grundsätzlich existiert.

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

## 3. "Offene API" in Shop-Beschreibungen

Verkaufsseiten für das DC-Fit-Set werben mit einer "offenen API-Schnittstelle"
zur Anbindung an EMS wie Solar Manager Connect 2 oder Loxone. Vermutlich ist
damit dieselbe lokale Modbus-Schnittstelle gemeint, nicht ein separates
REST-Interface – eine explizite Bestätigung dafür liegt aber nicht vor.

## Offene Fragen / weiter zu klären

- [ ] Gilt das Modbus-Register-Mapping (PowerOcean Plus) 1:1 für DC Fit, oder
      gibt es ein eigenes `InverterModel`-Mapping mit abweichenden Adressen?
      → `models.py` im Repo `MaxGrmm/EF-PowerOcean-TcpModbus` noch nicht geprüft.
- [ ] Genauer Menüpfad zur Modbus-Freischaltung in der EcoFlow Pro App
      (nicht öffentlich dokumentiert)
- [ ] Ob Fehler 1006 bei der Cloud-API mittlerweile (nach Firmware-Updates)
      behoben wurde

## Quellenübersicht

- https://github.com/Feberdin/ecoflow-powerocean-ha
- https://github.com/MaxGrmm/ecoflow-poweroceanplus-modbus
- https://github.com/MaxGrmm/EF-PowerOcean-TcpModbus
- https://github.com/windmark/EF-PowerOcean-TcpModbus
- https://github.com/harduser-gnk/EF-PowerOcean-TcpModbus
- https://docs.evcc.io/en/meters/ecoflow-powerocean-modbus
- https://www.photovoltaikforum.com/thread/247994-ecoflow-powerocean-modbus-protokoll/
