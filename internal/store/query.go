package store

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Query beschreibt die Filter einer Auswertung.
type Query struct {
	From           string `json:"from"` // "2006-01-02T15:04" (in TZ), leer = offen
	To             string `json:"to"`   // exklusiv
	TZ             string `json:"tz"`   // "utc" (Standard) oder "local"
	Country        string `json:"country"`
	ASN            uint32 `json:"asn"`
	Region         string `json:"region"`
	Status         []int  `json:"status"` // Statusklassen 1..5 (0 = unbekannt); leer = alle
	ExcludePrivate bool   `json:"excludePrivate"`
	Limit          int    `json:"limit"` // max. IP-Zeilen (0 = 1000, <0 = alle)
}

// CountryRow ist eine Zeile der Länderliste.
type CountryRow struct {
	CC        string `json:"cc"`
	Name      string `json:"name"`
	Continent string `json:"continent"`
	IPs       int    `json:"ips"`
	Hits      int64  `json:"hits"`
}

// ISPRow ist eine Zeile der ISP/ASN-Liste.
type ISPRow struct {
	ASN  uint32 `json:"asn"`
	Org  string `json:"org"`
	CC   string `json:"cc"` // häufigstes Land
	IPs  int    `json:"ips"`
	Hits int64  `json:"hits"`
}

// RegionRow ist eine Zeile der Regionenliste.
type RegionRow struct {
	CC     string `json:"cc"`
	Region string `json:"region"`
	IPs    int    `json:"ips"`
	Hits   int64  `json:"hits"`
}

// IPRow ist eine Zeile der IP-Liste.
type IPRow struct {
	IP      string `json:"ip"`
	CC      string `json:"cc"`
	Country string `json:"country"`
	Region  string `json:"region"`
	City    string `json:"city"`
	ASN     uint32 `json:"asn"`
	Org     string `json:"org"`
	Hits    int64  `json:"hits"`
	First   string `json:"first"`
	Last    string `json:"last"`
	UA      string `json:"ua"`
	Private bool   `json:"private,omitempty"`
}

// Point ist ein Punkt der Zeitachse.
type Point struct {
	T    string `json:"t"`
	IPs  int    `json:"ips"`
	Hits int64  `json:"hits"`
}

// Result einer Auswertung.
type Result struct {
	Hits      int64         `json:"hits"`
	IPs       int           `json:"ips"`
	Countries []CountryRow  `json:"countries"`
	ISPs      []ISPRow      `json:"isps"`
	Regions   []RegionRow   `json:"regions"`
	IPRows    []IPRow       `json:"ipRows"`
	IPTotal   int           `json:"ipTotal"`
	Unit      string        `json:"unit"` // "hour" oder "day"
	Timeline  []Point       `json:"timeline"`
	Status    map[int]int64 `json:"status"`
	DataFrom  string        `json:"dataFrom"` // gesamter Datenbereich, Format wie Query.From
	DataTo    string        `json:"dataTo"`
	HasData   bool          `json:"hasData"`
	TZName    string        `json:"tzName"`
	Version   int64         `json:"version"`
}

const inputLayout = "2006-01-02T15:04"

// Location liefert die Zeitzone zu "utc"/"local".
func Location(tz string) *time.Location {
	if strings.EqualFold(tz, "local") {
		return time.Local
	}
	return time.UTC
}

func parseInput(v string, loc *time.Location) (int64, bool, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false, nil
	}
	for _, l := range []string{inputLayout, "2006-01-02T15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(l, v, loc); err == nil {
			return t.Unix(), true, nil
		}
	}
	return 0, false, fmt.Errorf("ungültiges Datum: %q", v)
}

type agg struct {
	ips  int
	hits int64
}

// Run führt eine Auswertung aus.
func (s *Store) Run(q Query) (*Result, error) {
	loc := Location(q.TZ)
	from, hasFrom, err := parseInput(q.From, loc)
	if err != nil {
		return nil, err
	}
	to, hasTo, err := parseInput(q.To, loc)
	if err != nil {
		return nil, err
	}
	// Nur Datum als "bis" => ganzer Tag inklusive
	if hasTo && len(strings.TrimSpace(q.To)) == 10 {
		to = time.Unix(to, 0).In(loc).AddDate(0, 0, 1).Unix()
	}
	statusOK := [6]bool{true, true, true, true, true, true}
	if len(q.Status) > 0 {
		statusOK = [6]bool{}
		for _, c := range q.Status {
			if c >= 0 && c <= 5 {
				statusOK[c] = true
			}
		}
	}
	country := strings.ToUpper(strings.TrimSpace(q.Country))

	s.mu.RLock()
	defer s.mu.RUnlock()

	res := &Result{Status: map[int]int64{}, Version: s.version, TZName: tzName(loc)}
	if s.minUnix != 0 || s.maxUnix != 0 {
		res.HasData = true
		res.DataFrom = time.Unix(floorDiv(s.minUnix, 3600)*3600, 0).In(loc).Format(inputLayout)
		res.DataTo = time.Unix((floorDiv(s.maxUnix, 3600)+1)*3600, 0).In(loc).Format(inputLayout)
	}

	// IP-Filter vorab (Land, ASN, Region, privat)
	ipOK := make([]bool, len(s.ips))
	for i := range s.ips {
		r := &s.ips[i]
		ok := true
		if q.ExcludePrivate && r.private {
			ok = false
		}
		if ok && country != "" {
			if country == "-" {
				ok = r.cc == "" && !r.private
			} else if country == "PRIVATE" {
				ok = r.private
			} else {
				ok = r.cc == country
			}
		}
		if ok && q.ASN != 0 {
			ok = r.asn == q.ASN
		}
		if ok && q.Region != "" {
			ok = r.region == q.Region
		}
		ipOK[i] = ok
	}

	type ipAgg struct {
		hits          int64
		firstH, lastH int32
	}
	per := make(map[uint32]*ipAgg)
	hourHits := make(map[int32]int64)
	hourIPs := make(map[uint64]struct{})
	statusHits := map[int]int64{}

	for k, n := range s.buckets {
		if !ipOK[k.ip] {
			continue
		}
		t := int64(k.hour) * 3600
		if (hasFrom && t < from) || (hasTo && t >= to) {
			continue
		}
		// Statusverteilung ohne Statusfilter, damit die Chips Zahlen zeigen
		statusHits[int(k.sc)] += int64(n)
		if !statusOK[k.sc] {
			continue
		}
		a := per[k.ip]
		if a == nil {
			a = &ipAgg{firstH: k.hour, lastH: k.hour}
			per[k.ip] = a
		}
		a.hits += int64(n)
		if k.hour < a.firstH {
			a.firstH = k.hour
		}
		if k.hour > a.lastH {
			a.lastH = k.hour
		}
		hourHits[k.hour] += int64(n)
		hourIPs[uint64(uint32(k.hour))<<32|uint64(k.ip)] = struct{}{}
		res.Hits += int64(n)
	}
	res.Status = statusHits
	res.IPs = len(per)

	// Länder, ISPs, Regionen
	cAgg := map[string]*CountryRow{}
	type ispKey struct {
		asn uint32
		org string
	}
	iAgg := map[ispKey]*ISPRow{}
	iCC := map[ispKey]map[string]int{}
	rAgg := map[[2]string]*RegionRow{}
	for id, a := range per {
		r := &s.ips[id]
		cc, name := r.cc, r.country
		if r.private {
			cc, name = "PRIVATE", "Privat / intern"
		} else if cc == "" {
			cc, name = "-", "Unbekannt"
		}
		c := cAgg[cc]
		if c == nil {
			c = &CountryRow{CC: cc, Name: name, Continent: r.continent}
			cAgg[cc] = c
		}
		c.IPs++
		c.Hits += a.hits

		if !r.private {
			ik := ispKey{r.asn, r.org}
			x := iAgg[ik]
			if x == nil {
				x = &ISPRow{ASN: r.asn, Org: r.org}
				if x.Org == "" {
					x.Org = "Unbekannt"
				}
				iAgg[ik] = x
				iCC[ik] = map[string]int{}
			}
			x.IPs++
			x.Hits += a.hits
			iCC[ik][r.cc]++

			if r.region != "" {
				rk := [2]string{r.cc, r.region}
				y := rAgg[rk]
				if y == nil {
					y = &RegionRow{CC: r.cc, Region: r.region}
					rAgg[rk] = y
				}
				y.IPs++
				y.Hits += a.hits
			}
		}
	}
	for _, c := range cAgg {
		res.Countries = append(res.Countries, *c)
	}
	sort.Slice(res.Countries, func(i, j int) bool {
		a, b := res.Countries[i], res.Countries[j]
		if a.IPs != b.IPs {
			return a.IPs > b.IPs
		}
		if a.Hits != b.Hits {
			return a.Hits > b.Hits
		}
		return a.CC < b.CC
	})
	for k, x := range iAgg {
		best, bn := "", -1
		for cc, n := range iCC[k] {
			if n > bn || (n == bn && cc < best) {
				best, bn = cc, n
			}
		}
		x.CC = best
		res.ISPs = append(res.ISPs, *x)
	}
	sort.Slice(res.ISPs, func(i, j int) bool {
		a, b := res.ISPs[i], res.ISPs[j]
		if a.IPs != b.IPs {
			return a.IPs > b.IPs
		}
		if a.Hits != b.Hits {
			return a.Hits > b.Hits
		}
		return a.Org < b.Org
	})
	for _, y := range rAgg {
		res.Regions = append(res.Regions, *y)
	}
	sort.Slice(res.Regions, func(i, j int) bool {
		a, b := res.Regions[i], res.Regions[j]
		if a.IPs != b.IPs {
			return a.IPs > b.IPs
		}
		if a.Hits != b.Hits {
			return a.Hits > b.Hits
		}
		return a.CC+a.Region < b.CC+b.Region
	})

	// IP-Liste
	ids := make([]uint32, 0, len(per))
	for id := range per {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := per[ids[i]].hits, per[ids[j]].hits
		if a != b {
			return a > b
		}
		return s.ips[ids[i]].addr.Less(s.ips[ids[j]].addr)
	})
	res.IPTotal = len(ids)
	limit := q.Limit
	if limit == 0 {
		limit = 1000
	}
	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}
	const tf = "2006-01-02 15:04:05"
	for _, id := range ids {
		r, a := &s.ips[id], per[id]
		// Exakte Zeit, wenn die erste/letzte Anfrage der IP im Filter liegt; sonst Stundengenau.
		first := "≈ " + time.Unix(int64(a.firstH)*3600, 0).In(loc).Format("2006-01-02 15:04")
		if floorDiv(r.first, 3600) == int64(a.firstH) {
			first = time.Unix(r.first, 0).In(loc).Format(tf)
		}
		last := "≈ " + time.Unix(int64(a.lastH)*3600+3599, 0).In(loc).Format("2006-01-02 15:04")
		if floorDiv(r.last, 3600) == int64(a.lastH) {
			last = time.Unix(r.last, 0).In(loc).Format(tf)
		}
		res.IPRows = append(res.IPRows, IPRow{
			IP: r.addr.String(), CC: r.cc, Country: r.country, Region: r.region, City: r.city,
			ASN: r.asn, Org: r.org, Hits: a.hits, UA: r.ua, Private: r.private,
			First: first, Last: last,
		})
	}

	res.Unit, res.Timeline = timeline(hourHits, hourIPs, loc)
	return res, nil
}

func tzName(loc *time.Location) string {
	if loc == time.UTC {
		return "UTC"
	}
	name, off := time.Now().In(loc).Zone()
	return fmt.Sprintf("Lokalzeit (%s, UTC%+d)", name, off/3600)
}

// timeline baut eine lückenlose Zeitreihe: stündlich bis 3 Tage, sonst täglich.
func timeline(hourHits map[int32]int64, hourIPs map[uint64]struct{}, loc *time.Location) (string, []Point) {
	if len(hourHits) == 0 {
		return "day", nil
	}
	minH, maxH := int32(1<<31-1), int32(-1<<31)
	for h := range hourHits {
		if h < minH {
			minH = h
		}
		if h > maxH {
			maxH = h
		}
	}
	if maxH-minH < 72 {
		idx := map[int32]int{}
		var pts []Point
		for h := minH; h <= maxH; h++ {
			idx[h] = len(pts)
			pts = append(pts, Point{T: time.Unix(int64(h)*3600, 0).In(loc).Format("2006-01-02 15:04")})
		}
		for h, n := range hourHits {
			pts[idx[h]].Hits += n
		}
		for k := range hourIPs {
			pts[idx[int32(uint32(k>>32))]].IPs++
		}
		return "hour", pts
	}
	dayOf := func(h int32) time.Time {
		t := time.Unix(int64(h)*3600, 0).In(loc)
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	}
	start, end := dayOf(minH), dayOf(maxH)
	idx := map[string]int{}
	var pts []Point
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		k := d.Format("2006-01-02")
		idx[k] = len(pts)
		pts = append(pts, Point{T: k})
	}
	hourDay := map[int32]int{}
	di := func(h int32) int {
		if v, ok := hourDay[h]; ok {
			return v
		}
		v := idx[dayOf(h).Format("2006-01-02")]
		hourDay[h] = v
		return v
	}
	for h, n := range hourHits {
		pts[di(h)].Hits += n
	}
	seen := map[uint64]struct{}{}
	for k := range hourIPs {
		d := di(int32(uint32(k >> 32)))
		key := uint64(d)<<32 | (k & 0xffffffff)
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			pts[d].IPs++
		}
	}
	return "day", pts
}
