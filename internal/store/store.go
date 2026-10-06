// Package store sammelt geparste Logzeilen (aggregiert pro IP, Stunde und Statusklasse).
package store

import (
	"bufio"
	"crypto/sha1"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/neuhausf/iis-geolocation/internal/geo"
	"github.com/neuhausf/iis-geolocation/internal/iislog"
)

type ipRec struct {
	addr        netip.Addr
	cc          string
	country     string
	continent   string
	region      string
	city        string
	asn         uint32
	org         string
	ua          string
	first, last int64
	private     bool
}

type bkey struct {
	ip   uint32
	hour int32 // Unix-Stunde (UTC bzw. Zeit wie im Log)
	sc   uint8 // Statusklasse 1..5, 0 = unbekannt
}

// FileInfo beschreibt eine geladene Datei.
type FileInfo struct {
	Name    string `json:"name"`
	Lines   int64  `json:"lines"`
	Parsed  int64  `json:"parsed"`
	Skipped int64  `json:"skipped"`
	UsedXFF int64  `json:"usedXff"`
	From    int64  `json:"from"`
	To      int64  `json:"to"`
	Error   string `json:"error,omitempty"`
	Dupe    bool   `json:"dupe,omitempty"`
}

// Progress ist der Fortschritt des laufenden Ladevorgangs.
type Progress struct {
	Active     bool   `json:"active"`
	File       string `json:"file"`
	FilesDone  int    `json:"filesDone"`
	FilesTotal int    `json:"filesTotal"`
	BytesDone  int64  `json:"bytesDone"`
	BytesTotal int64  `json:"bytesTotal"`
	Lines      int64  `json:"lines"`
	Message    string `json:"message,omitempty"`
}

// Store ist threadsicher.
type Store struct {
	geo *geo.Geo

	mu      sync.RWMutex
	ips     []ipRec
	idx     map[netip.Addr]uint32
	buckets map[bkey]uint32
	files   []FileInfo
	keys    map[string]bool
	intern  map[string]string
	minUnix int64
	maxUnix int64
	version int64

	pmu  sync.Mutex
	prog Progress
	// loadMu serialisiert Ladevorgänge.
	loadMu sync.Mutex
}

// New erzeugt einen leeren Store.
func New(g *geo.Geo) *Store {
	s := &Store{geo: g}
	s.resetLocked()
	return s
}

func (s *Store) resetLocked() {
	s.ips = nil
	s.idx = make(map[netip.Addr]uint32)
	s.buckets = make(map[bkey]uint32)
	s.files = nil
	s.keys = make(map[string]bool)
	s.intern = make(map[string]string)
	s.minUnix, s.maxUnix = 0, 0
	s.version++
}

// Reset verwirft alle Daten.
func (s *Store) Reset() {
	s.loadMu.Lock()
	defer s.loadMu.Unlock()
	s.mu.Lock()
	s.resetLocked()
	s.mu.Unlock()
}

// Geo liefert die verwendete Geo-Datenbank.
func (s *Store) Geo() *geo.Geo { return s.geo }

// Progress liefert den aktuellen Ladefortschritt.
func (s *Store) Progress() Progress {
	s.pmu.Lock()
	defer s.pmu.Unlock()
	return s.prog
}

func (s *Store) setProgress(f func(p *Progress)) {
	s.pmu.Lock()
	f(&s.prog)
	s.pmu.Unlock()
}

// Files liefert die geladenen Dateien.
func (s *Store) Files() []FileInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]FileInfo(nil), s.files...)
}

func (s *Store) in(v string) string {
	if v == "" {
		return ""
	}
	if x, ok := s.intern[v]; ok {
		return x
	}
	s.intern[v] = v
	return v
}

func (s *Store) add(e *iislog.Entry) {
	id, ok := s.idx[e.IP]
	if !ok {
		r := ipRec{addr: e.IP, first: e.Unix, last: e.Unix, private: isPrivate(e.IP)}
		if !r.private {
			g := s.geo.Lookup(e.IP)
			r.cc, r.country, r.continent = s.in(g.CountryCode), s.in(g.CountryDE), s.in(g.Continent)
			r.region, r.city, r.asn, r.org = s.in(g.Region), s.in(g.City), g.ASN, s.in(g.Org)
		}
		if len(e.UA) > 0 && !(len(e.UA) == 1 && e.UA[0] == '-') {
			ua := e.UA
			if len(ua) > 300 {
				ua = ua[:300]
			}
			r.ua = s.in(strings.ReplaceAll(string(ua), "+", " "))
		}
		id = uint32(len(s.ips))
		s.ips = append(s.ips, r)
		s.idx[e.IP] = id
	} else {
		r := &s.ips[id]
		if e.Unix < r.first {
			r.first = e.Unix
		}
		if e.Unix > r.last {
			r.last = e.Unix
		}
	}
	sc := uint8(0)
	if e.Status >= 100 && e.Status < 600 {
		sc = uint8(e.Status / 100)
	}
	s.buckets[bkey{ip: id, hour: int32(floorDiv(e.Unix, 3600)), sc: sc}]++
	if s.minUnix == 0 || e.Unix < s.minUnix {
		s.minUnix = e.Unix
	}
	if e.Unix > s.maxUnix {
		s.maxUnix = e.Unix
	}
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

func isPrivate(a netip.Addr) bool {
	return a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsUnspecified() || a.IsMulticast() || cgnat.Contains(a)
}

// LoadSources liest die Quellen nacheinander ein.
func (s *Store) LoadSources(srcs []iislog.Source, opt iislog.Options) []FileInfo {
	s.loadMu.Lock()
	defer s.loadMu.Unlock()
	var total int64
	for _, src := range srcs {
		total += src.Size
	}
	s.setProgress(func(p *Progress) {
		*p = Progress{Active: true, FilesTotal: len(srcs), BytesTotal: total}
	})
	defer s.setProgress(func(p *Progress) { p.Active = false; p.File = "" })

	var out []FileInfo
	for i, src := range srcs {
		s.setProgress(func(p *Progress) { p.File = src.Name; p.FilesDone = i })
		fi := s.loadOne(src, opt)
		out = append(out, fi)
	}
	s.setProgress(func(p *Progress) { p.FilesDone = len(srcs) })
	return out
}

func (s *Store) loadOne(src iislog.Source, opt iislog.Options) FileInfo {
	fi := FileInfo{Name: src.Name}
	rc, err := src.Open()
	if err != nil {
		fi.Error = err.Error()
		s.appendFile(fi, "")
		return fi
	}
	defer rc.Close()
	// Fingerabdruck (Anfang des Inhalts + Grösse), damit dieselbe Datei nicht doppelt
	// gezählt wird – egal ob per Upload, Pfad oder aus einem ZIP geladen.
	br := bufio.NewReaderSize(rc, 1<<20)
	head, _ := br.Peek(64 << 10)
	sum := sha1.Sum(head)
	key := fmt.Sprintf("%x|%d", sum, src.Size)
	s.mu.RLock()
	dupe := s.keys[key]
	s.mu.RUnlock()
	if dupe {
		fi.Dupe = true
		fi.Error = "bereits geladen – übersprungen"
		s.setProgress(func(p *Progress) { p.BytesDone += src.Size })
		s.appendFile(fi, "")
		return fi
	}
	fi, err = s.parseInto(src.Name, br, opt)
	if err != nil {
		fi.Error = err.Error()
	}
	s.appendFile(fi, key)
	return fi
}

// LoadReader liest einen einzelnen Datenstrom (z.B. Upload) ein. Der Aufrufer
// muss LoadSources/LoadReader nicht parallel aufrufen; das wird hier serialisiert.
func (s *Store) LoadReader(name string, r io.Reader, size int64, opt iislog.Options) FileInfo {
	return s.LoadSources([]iislog.Source{{
		Name: name, Size: size,
		Open: func() (io.ReadCloser, error) { return io.NopCloser(r), nil },
	}}, opt)[0]
}

func (s *Store) parseInto(name string, r io.Reader, opt iislog.Options) (FileInfo, error) {
	fi := FileInfo{Name: name}
	st, err := iislog.Parse(r, opt, func(e *iislog.Entry) {
		s.mu.Lock()
		s.add(e)
		s.mu.Unlock()
	}, func(n int64) {
		s.setProgress(func(p *Progress) { p.BytesDone += n })
	})
	fi.Lines, fi.Parsed, fi.Skipped, fi.UsedXFF = st.Lines, st.Parsed, st.Skipped, st.UsedXFF
	fi.From, fi.To = st.MinUnix, st.MaxUnix
	if st.Parsed == 0 && err == nil {
		if st.Lines == 0 {
			err = fmt.Errorf("keine Logzeilen gefunden")
		} else {
			err = fmt.Errorf("keine Zeile konnte gelesen werden (Format?)")
		}
	}
	s.setProgress(func(p *Progress) { p.Lines += st.Parsed })
	return fi, err
}

func (s *Store) appendFile(fi FileInfo, key string) {
	s.mu.Lock()
	s.files = append(s.files, fi)
	if key != "" && fi.Parsed > 0 {
		s.keys[key] = true
	}
	s.version++
	s.mu.Unlock()
}

// DataRange liefert die Zeitspanne aller geladenen Daten.
func (s *Store) DataRange() (time.Time, time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.minUnix == 0 && s.maxUnix == 0 {
		return time.Time{}, time.Time{}, false
	}
	return time.Unix(s.minUnix, 0).UTC(), time.Unix(s.maxUnix, 0).UTC(), true
}
