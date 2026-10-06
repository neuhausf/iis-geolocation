// Package geo kapselt die Geolokalisierung (Land, Region/Stadt, ASN/ISP) per MMDB.
package geo

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/neuhausf/iis-geolocation/internal/geodata"
	"github.com/oschwald/maxminddb-golang/v2"
)

// Result einer Abfrage.
type Result struct {
	CountryCode string
	CountryDE   string
	CountryEN   string
	Continent   string
	Region      string
	City        string
	ASN         uint32
	Org         string
}

// DBInfo beschreibt eine geladene Datenbank.
type DBInfo struct {
	Kind   string    `json:"kind"`
	Type   string    `json:"type"`
	Source string    `json:"source"`
	Built  time.Time `json:"built"`
}

// Geo hält die geöffneten Datenbanken.
type Geo struct {
	country, asn, city *maxminddb.Reader
	Infos              []DBInfo
}

type countryRec struct {
	Continent struct {
		Code string `maxminddb:"code"`
	} `maxminddb:"continent"`
	Country struct {
		ISOCode string            `maxminddb:"iso_code"`
		Names   map[string]string `maxminddb:"names"`
	} `maxminddb:"country"`
}

type cityRec struct {
	City struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"city"`
	Subdivisions []struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"subdivisions"`
}

type asnRec struct {
	Number uint32 `maxminddb:"autonomous_system_number"`
	Org    string `maxminddb:"autonomous_system_organization"`
}

// Open lädt die eingebetteten Datenbanken. Liegen passende *.mmdb(.gz)-Dateien in
// einem der Ordner in extraDirs (z.B. neben der EXE), haben diese Vorrang.
func Open(extraDirs ...string) (*Geo, error) {
	g := &Geo{}
	var err error
	if g.country, err = g.load("Land", geodata.Country, extraDirs, "country", "city"); err != nil {
		return nil, err
	}
	if g.asn, err = g.load("ISP/ASN", geodata.ASN, extraDirs, "asn"); err != nil {
		return nil, err
	}
	if g.city, err = g.load("Region/Stadt", geodata.City, extraDirs, "city"); err != nil {
		return nil, err
	}
	return g, nil
}

// HasCity meldet, ob Regionen/Städte verfügbar sind.
func (g *Geo) HasCity() bool { return g.city != nil }

func (g *Geo) load(kind string, embedded []byte, dirs []string, keywords ...string) (*maxminddb.Reader, error) {
	// externe Datei suchen (erstes Schlüsselwort hat Vorrang)
	for _, kw := range keywords {
		for _, dir := range dirs {
			if dir == "" {
				continue
			}
			for _, p := range findDB(dir, kw) {
				data, err := readMaybeGzip(p)
				if err != nil {
					continue
				}
				r, err := maxminddb.OpenBytes(data)
				if err != nil {
					continue
				}
				g.Infos = append(g.Infos, info(kind, r, filepath.Base(p)))
				return r, nil
			}
		}
		// Bei "Land" nur dann auf eine City-Datei ausweichen, wenn nichts eingebettet ist.
		if kind == "Land" && len(embedded) > 0 {
			break
		}
	}
	if len(embedded) == 0 {
		return nil, nil
	}
	data, err := gunzip(embedded)
	if err != nil {
		return nil, fmt.Errorf("eingebettete Datenbank %s: %w", kind, err)
	}
	r, err := maxminddb.OpenBytes(data)
	if err != nil {
		return nil, fmt.Errorf("eingebettete Datenbank %s: %w", kind, err)
	}
	g.Infos = append(g.Infos, info(kind, r, "eingebettet"))
	return r, nil
}

func info(kind string, r *maxminddb.Reader, src string) DBInfo {
	return DBInfo{Kind: kind, Type: r.Metadata.DatabaseType, Source: src, Built: time.Unix(int64(r.Metadata.BuildEpoch), 0).UTC()}
}

// findDB sucht z.B. dbip-city-lite-2026-10.mmdb oder GeoLite2-City.mmdb (neueste zuerst).
func findDB(dir, keyword string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		if e.IsDir() || !(strings.HasSuffix(n, ".mmdb") || strings.HasSuffix(n, ".mmdb.gz")) {
			continue
		}
		if strings.Contains(n, keyword) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

func readMaybeGzip(p string) ([]byte, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(strings.ToLower(p), ".gz") {
		return gunzip(data)
	}
	return data, nil
}

func gunzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

// Lookup geolokalisiert eine Adresse.
func (g *Geo) Lookup(a netip.Addr) Result {
	var res Result
	if g.country != nil {
		var c countryRec
		if err := g.country.Lookup(a).Decode(&c); err == nil {
			res.CountryCode = c.Country.ISOCode
			res.CountryDE = c.Country.Names["de"]
			res.CountryEN = c.Country.Names["en"]
			if v, ok := shortNames[res.CountryCode]; ok {
				res.CountryDE = v
			} else if res.CountryDE == "" {
				res.CountryDE = res.CountryEN
			}
			res.CountryDE = strings.ReplaceAll(res.CountryDE, "ß", "ss")
			res.Continent = c.Continent.Code
		}
	}
	if g.city != nil {
		var c cityRec
		if err := g.city.Lookup(a).Decode(&c); err == nil {
			res.City = name(c.City.Names)
			if len(c.Subdivisions) > 0 {
				res.Region = name(c.Subdivisions[0].Names)
			}
		}
	}
	if g.asn != nil {
		var r asnRec
		if err := g.asn.Lookup(a).Decode(&r); err == nil {
			res.ASN = r.Number
			res.Org = r.Org
		}
	}
	return res
}

func name(m map[string]string) string {
	if v := m["de"]; v != "" {
		return v
	}
	return m["en"]
}

// shortNames: gebräuchliche Kurzformen statt der langen amtlichen Bezeichnungen.
var shortNames = map[string]string{
	"US": "USA", "GB": "Grossbritannien", "KR": "Südkorea", "KP": "Nordkorea", "CN": "China",
	"RU": "Russland", "TW": "Taiwan", "HK": "Hongkong", "MO": "Macao", "IR": "Iran", "SY": "Syrien",
	"VN": "Vietnam", "LA": "Laos", "MD": "Moldau", "TZ": "Tansania", "BO": "Bolivien", "VE": "Venezuela",
	"CD": "Kongo (Dem. Rep.)", "CG": "Kongo", "PS": "Palästina", "VA": "Vatikanstadt", "MK": "Nordmazedonien",
	"CZ": "Tschechien", "AE": "Vereinigte Arabische Emirate", "DO": "Dominikanische Republik",
}
