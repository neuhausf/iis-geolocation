// Package geodata bettet die DB-IP-Lite-Datenbanken (CC BY 4.0, https://db-ip.com) in die EXE ein.
// Die *.mmdb.gz-Dateien werden vor dem Build mit tools/fetch-db.sh bzw. tools/fetch-db.ps1 geladen.
package geodata

import _ "embed"

//go:embed dbip-country-lite.mmdb.gz
var Country []byte

//go:embed dbip-asn-lite.mmdb.gz
var ASN []byte
