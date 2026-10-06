#!/usr/bin/env bash
# Lädt die aktuellen DB-IP-Lite-Datenbanken (CC BY 4.0) nach internal/geodata/.
# Aufruf: tools/fetch-db.sh [--city]
set -euo pipefail
cd "$(dirname "$0")/../internal/geodata"
kinds="country asn"
[[ "${1:-}" == "--city" ]] && kinds="$kinds city"
for kind in $kinds; do
  ok=""
  # aktueller Monat, sonst Vormonat (neue Monatsdateien erscheinen am Monatsanfang)
  for ym in "$(date -u +%Y-%m)" "$(date -u -d "$(date -u +%Y-%m-15) -1 month" +%Y-%m)"; do
    url="https://download.db-ip.com/free/dbip-${kind}-lite-${ym}.mmdb.gz"
    if curl -fsSL --retry 3 -o "dbip-${kind}-lite.mmdb.gz.tmp" "$url"; then
      mv "dbip-${kind}-lite.mmdb.gz.tmp" "dbip-${kind}-lite.mmdb.gz"
      echo "geladen: $url"
      ok=1
      break
    fi
  done
  [[ -n "$ok" ]] || { echo "Download fehlgeschlagen: $kind" >&2; exit 1; }
done
