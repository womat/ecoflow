# EcoFlow PowerOcean DC Fit – API & Modbus Notizen

Persönliche Recherche-Notizen zu Integrationsmöglichkeiten (REST-API, lokales
Modbus TCP) für den EcoFlow PowerOcean DC Fit.

> **Disclaimer:** Diese Doku basiert größtenteils auf Community-Reverse-Engineering,
> nicht auf offizieller EcoFlow-Dokumentation. EcoFlow unterstützt oder bestätigt
> die hier beschriebenen Modbus-Register nicht offiziell. Nutzung auf eigenes
> Risiko, insbesondere bei Schreibzugriffen auf Register.

## Inhalt

| Datei | Beschreibung |
|---|---|
| [`api-status.md`](./api-status.md) | Überblick: Cloud-REST-API vs. lokales Modbus TCP, bekannte Probleme (z.B. Fehler 1006), Freischaltung |
| [`modbus-registers.md`](./modbus-registers.md) | Register-Map (SOC, Batterie, PV, Netz, Energiezähler, Steuerregister) inkl. Decoding-Beispielen |

## Kurzüberblick

- Eine spezifische, offiziell dokumentierte REST-API für den DC Fit existiert nicht.
- Die generische EcoFlow Developer/Open API (Cloud) liefert für die PowerOcean-Familie
  häufig Fehler 1006 "not allowed".
- Praktikabler Weg: **lokales Modbus TCP** (Port 502) – muss vom Installateur über
  die EcoFlow Pro App freigeschaltet werden, Registerbelegung ist nicht offiziell
  dokumentiert, sondern community-ermittelt.
- Details siehe die beiden verlinkten Dateien.

## Offene Punkte

- Bestätigung, ob das Register-Mapping (ermittelt am PowerOcean Plus) 1:1 für
  den DC Fit gilt, oder ob es eigene Adress-Overrides gibt
- Genauer Freischalt-Pfad in der EcoFlow Pro App

## Quellen

- https://developer.ecoflow.com
- https://github.com/Feberdin/ecoflow-powerocean-ha
- https://github.com/MaxGrmm/ecoflow-poweroceanplus-modbus
- https://github.com/MaxGrmm/EF-PowerOcean-TcpModbus
- https://github.com/windmark/EF-PowerOcean-TcpModbus
- https://docs.evcc.io/en/meters/ecoflow-powerocean-modbus
- https://www.photovoltaikforum.com/thread/247994-ecoflow-powerocean-modbus-protokoll/

## Lizenz

MIT – siehe [`LICENSE`](./LICENSE). Beachte, dass Teile der Register-Informationen
aus MIT-lizenzierten Drittquellen (s.o.) übernommen wurden; die jeweiligen
Original-Links sind in `modbus-registers.md` angegeben.
