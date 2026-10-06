//go:build city

package geodata

import _ "embed"

// City enthält Region/Stadt (nur in der Variante "mit Regionen", Build-Tag "city").
//
//go:embed dbip-city-lite.mmdb.gz
var City []byte
