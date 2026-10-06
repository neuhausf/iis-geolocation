package store

import (
	"strings"
	"testing"

	"github.com/neuhausf/iis-geolocation/internal/geo"
	"github.com/neuhausf/iis-geolocation/internal/iislog"
)

const log1 = `#Fields: date time c-ip sc-status
2026-08-30 22:10:00 51.154.8.227 200
2026-08-30 23:10:00 51.154.8.227 404
2026-08-31 00:10:00 43.166.237.57 200
2026-08-31 00:20:00 10.0.0.1 200
`

func TestStore(t *testing.T) {
	g, err := geo.Open()
	if err != nil {
		t.Fatal(err)
	}
	s := New(g)
	s.LoadReader("a.log", strings.NewReader(log1), int64(len(log1)), iislog.Options{})
	fi := s.LoadReader("kopie.log", strings.NewReader(log1), int64(len(log1)), iislog.Options{})
	if !fi.Dupe {
		t.Fatal("Duplikat nicht erkannt")
	}
	res, err := s.Run(Query{ExcludePrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Hits != 3 || res.IPs != 2 || len(res.Countries) != 2 || res.Countries[0].CC != "CH" {
		t.Fatalf("unerwartet: hits=%d ips=%d countries=%+v", res.Hits, res.IPs, res.Countries)
	}
	res, _ = s.Run(Query{From: "2026-08-30T23:00", To: "2026-08-31", Status: []int{2}})
	if res.Hits != 2 || res.IPs != 2 {
		t.Fatalf("Filter: hits=%d ips=%d", res.Hits, res.IPs)
	}
	res, _ = s.Run(Query{Country: "CH"})
	if res.Hits != 2 || len(res.IPRows) != 1 || res.IPRows[0].First != "2026-08-30 22:10:00" {
		t.Fatalf("Land: %+v", res.IPRows)
	}
}
