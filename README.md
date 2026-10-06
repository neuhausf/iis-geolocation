# iis-geo – IIS-Logs nach Herkunftsland auswerten

Eine einzelne Windows-EXE, ohne Installation: IIS-Logdateien einlesen (auch viele gleichzeitig),
IP-Adressen offline geolokalisieren (Land, ISP/Provider, optional Region/Stadt) und die Länder nach
Anzahl eindeutiger IP-Adressen sortieren, mit Weltkarte, Zeitverlauf und Zeitraumfilter.

## Download

**[⬇ iis-geo.exe](https://github.com/neuhausf/iis-geolocation/releases/latest/download/iis-geo.exe)**: Land + ISP (rund 17 MB, empfohlen)
**[⬇ iis-geo-regionen.exe](https://github.com/neuhausf/iis-geolocation/releases/latest/download/iis-geo-regionen.exe)**: zusätzlich Region (Kanton/Bundesland) und Stadt (rund 80 MB)

Alle Versionen findest du unter [Releases](https://github.com/neuhausf/iis-geolocation/releases). Die EXE wird jeden Monat
automatisch mit der aktuellen Geo-Datenbank neu gebaut.

> Die EXE ist nicht signiert. Beim ersten Start meldet Windows SmartScreen sie deshalb eventuell:
> „Weitere Informationen“ → „Trotzdem ausführen“.

## Bedienung

1. `iis-geo.exe` per Doppelklick starten. Ein Konsolenfenster geht auf und die Oberfläche öffnet sich im Browser
   (`http://127.0.0.1:8077/`).
2. Logs laden, auf einem dieser Wege:
   - `*.log`-Dateien ins Browserfenster ziehen oder per Klick auswählen (mehrere gleichzeitig),
   - oder einen Ordner, eine Datei oder ein Muster eingeben, z.B. `C:\inetpub\logs\LogFiles\W3SVC1` oder
     `D:\Logs\u_ex2608*.log`. Ordner werden mit allen Unterordnern gelesen. Bei grossen Logmengen ist das am schnellsten.
   - oder Dateien/Ordner direkt **auf die EXE ziehen**.
   - `.gz` und `.zip` werden automatisch entpackt. Wird dieselbe Datei ein zweites Mal geladen, erkennt das Programm das und überspringt sie.
3. Auswerten:
   - **Zeitraum** über *Von/Bis* (stundengenau) oder die Schnellwahl (Alles, 24 h, 7 Tage, 30 Tage)
   - **Zeitzone**: UTC (so schreibt IIS die Zeiten) oder Lokalzeit
   - **HTTP-Status** (2xx/3xx/4xx/5xx), z.B. nur 4xx, um Scanner zu finden
   - **Interne IPs** (10.x, 172.16–31.x, 192.168.x, …) ein- oder ausblenden
   - Ein Klick auf ein Land (Balken, Karte oder Tabelle) filtert alle Ansichten auf dieses Land. Gleiches gilt für ISP und Region.
4. Ergebnisse: Länder-Rangliste, Weltkarte, Zeitverlauf (eindeutige IPs bzw. Anfragen pro Tag/Stunde),
   ISP-Liste, Regionen und IP-Liste (mit erster/letzter Anfrage und User-Agent).
   Jede Liste lässt sich als **CSV** speichern (Excel-tauglich: Semikolon, UTF-8).
5. Zum Beenden das Konsolenfenster schliessen oder in der Oberfläche auf *Beenden* klicken.

Alles läuft lokal. Es werden keine Daten ins Internet gesendet, und die Geo-Datenbank steckt in der EXE.

### Kommandozeile (ohne Oberfläche)

```bat
iis-geo.exe -cli -from 2026-08-01 -to 2026-08-31 -csv laender.csv -csv-ips ips.csv D:\Logs\W3SVC1
```

| Option | Bedeutung |
|---|---|
| `-cli` | Auswertung in der Konsole statt im Browser |
| `-from`, `-to` | Zeitraum, `2026-08-01` oder `2026-08-01T06:00`; `-to` ist exklusiv, ein reines Datum zählt als ganzer Tag |
| `-tz utc\|local` | Zeitzone für `-from`/`-to` (Standard: `utc`) |
| `-country CH` | nur ein Land |
| `-no-private=false` | interne IPs mitzählen |
| `-csv datei.csv` / `-csv-ips datei.csv` | Länderliste bzw. IP-Liste speichern |
| `-top 30` | Anzahl Länder in der Ausgabe |
| `-xff=false` | `X-Forwarded-For` ignorieren und immer `c-ip` verwenden |
| `-port 8077`, `-no-browser` | Optionen für die Oberfläche |

## Hinweise zum Logformat

- Gelesen wird das W3C-Format von IIS. Massgebend ist die `#Fields:`-Zeile, das Programm kommt also mit beliebiger
  Feldauswahl und auch mit mehreren `#Fields`-Blöcken pro Datei zurecht. Benötigt werden `c-ip` und `time` (`date` ist optional;
  fehlt es, gilt das Datum aus `#Date:`).
- Steht ein Reverse-Proxy oder Load-Balancer vor dem IIS, ist `c-ip` dessen Adresse. Ist im IIS ein
  benutzerdefiniertes Feld `X-Forwarded-For` geloggt, wird standardmässig die erste Adresse daraus verwendet.
- „Eindeutige IP-Adressen“ zählt jede Adresse im gewählten Filter einmal. „Anfragen“ zählt die Logzeilen.

## Geo-Datenbank

[IP Geolocation by DB-IP](https://db-ip.com) (Lite-Datenbanken, Lizenz [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/)).
Auf Länderebene ist die Genauigkeit sehr hoch, Regionen und Städte sind nur Näherungswerte.

Wer eine neuere oder andere Datenbank verwenden möchte, legt eine `*.mmdb`- oder `*.mmdb.gz`-Datei neben die EXE.
Sie hat dann Vorrang vor der eingebetteten. Erkannt werden Dateinamen mit `country`, `asn` oder `city`, z.B.
`dbip-city-lite-2026-10.mmdb.gz` oder MaxMind `GeoLite2-City.mmdb`. So bekommt auch die kleine `iis-geo.exe` Regionen.

## Selbst bauen

Voraussetzung ist Go ≥ 1.26.

```bash
tools/fetch-db.sh --city          # Windows: powershell -File tools\fetch-db.ps1 -City
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o iis-geo.exe .
GOOS=windows GOARCH=amd64 go build -trimpath -tags city -ldflags "-s -w" -o iis-geo-regionen.exe .
```

Der Workflow `.github/workflows/build.yml` baut bei jedem Push, testet die EXE auf einem Windows-Runner und
veröffentlicht für den Standard-Branch das Release `latest`, für Tags `v*` ein eigenes Release. Zusätzlich baut er monatlich neu.

Die Weltkarte (`internal/web/static/world.json`) stammt aus Natural Earth (via `world-atlas`) und wird mit `tools/gen-world.js` erzeugt.
