// Package iislog liest IIS-Logdateien im W3C-Format (auch mehrere #Fields-Blöcke pro Datei).
package iislog

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/netip"
	"strings"
)

// Entry ist eine geparste Logzeile. Die Byte-Slices sind nur während des Callbacks gültig.
type Entry struct {
	Unix   int64 // Sekunden seit 1970 (Zeit wie im Log, i.d.R. UTC)
	IP     netip.Addr
	Status int // HTTP-Status, 0 wenn nicht vorhanden
	UA     []byte
}

// Stats zählt, was beim Lesen passiert ist.
type Stats struct {
	Lines    int64 // Datenzeilen (ohne #-Direktiven)
	Parsed   int64 // erfolgreich verwertete Zeilen
	Skipped  int64 // Zeilen ohne gültige IP/Zeit
	MinUnix  int64
	MaxUnix  int64
	BytesIn  int64
	UsedXFF  int64 // Zeilen, bei denen X-Forwarded-For verwendet wurde
	NoFields bool  // Datei hatte keine #Fields-Direktive
}

// Options steuern den Parser.
type Options struct {
	// PreferXFF verwendet die erste IP aus einem X-Forwarded-For-Feld, falls vorhanden.
	PreferXFF bool
}

type layout struct {
	date, time, cip, status, ua, xff int
	n                                int
}

func newLayout(fields []string) layout {
	l := layout{date: -1, time: -1, cip: -1, status: -1, ua: -1, xff: -1, n: len(fields)}
	for i, f := range fields {
		switch strings.ToLower(f) {
		case "date":
			l.date = i
		case "time":
			l.time = i
		case "c-ip":
			l.cip = i
		case "sc-status":
			l.status = i
		case "cs(user-agent)":
			l.ua = i
		case "x-forwarded-for", "cs(x-forwarded-for)", "x_forwarded_for", "originalip", "x-original-for", "true-client-ip", "cs(true-client-ip)":
			if l.xff < 0 {
				l.xff = i
			}
		}
	}
	return l
}

// Default-Layout, falls eine Datei (oder ein Ausschnitt) keine #Fields-Direktive hat.
var defaultFields = strings.Fields("date time s-ip cs-method cs-uri-stem cs-uri-query s-port cs-username c-ip cs(User-Agent) cs(Referer) sc-status sc-substatus sc-win32-status time-taken")

// Parse liest r zeilenweise und ruft fn für jede verwertbare Zeile auf.
// progress (optional) wird regelmässig mit der Anzahl gelesener Bytes aufgerufen.
func Parse(r io.Reader, opt Options, fn func(*Entry), progress func(n int64)) (Stats, error) {
	var st Stats
	br := bufio.NewReaderSize(r, 1<<20)
	lay := newLayout(defaultFields)
	haveFields := false
	headerDate := ""
	var cols [64][]byte
	var e Entry
	var sinceProgress int64

	for {
		line, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// Überlange Zeile: Rest verwerfen.
			for errors.Is(err, bufio.ErrBufferFull) {
				st.BytesIn += int64(len(line))
				line, err = br.ReadSlice('\n')
			}
			st.BytesIn += int64(len(line))
			st.Lines++
			st.Skipped++
			if err != nil {
				break
			}
			continue
		}
		st.BytesIn += int64(len(line))
		sinceProgress += int64(len(line))
		if progress != nil && sinceProgress > 4<<20 {
			progress(sinceProgress)
			sinceProgress = 0
		}
		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 {
			if err != nil {
				break
			}
			continue
		}
		if line[0] == '#' {
			if bytes.HasPrefix(line, []byte("#Fields:")) {
				lay = newLayout(strings.Fields(string(line[len("#Fields:"):])))
				haveFields = true
			} else if bytes.HasPrefix(line, []byte("#Date:")) {
				f := strings.Fields(string(line[len("#Date:"):]))
				if len(f) > 0 {
					headerDate = f[0]
				}
			}
			if err != nil {
				break
			}
			continue
		}
		// UTF-8 BOM am Dateianfang
		line = bytes.TrimPrefix(line, []byte{0xEF, 0xBB, 0xBF})
		st.Lines++

		n := splitSpaces(line, cols[:])
		if n < 2 {
			st.Skipped++
			if err != nil {
				break
			}
			continue
		}
		ok := parseLine(&e, cols[:n], lay, headerDate, opt, &st)
		if ok {
			st.Parsed++
			if st.MinUnix == 0 || e.Unix < st.MinUnix {
				st.MinUnix = e.Unix
			}
			if e.Unix > st.MaxUnix {
				st.MaxUnix = e.Unix
			}
			fn(&e)
		} else {
			st.Skipped++
		}
		if err != nil {
			break
		}
	}
	if progress != nil && sinceProgress > 0 {
		progress(sinceProgress)
	}
	st.NoFields = !haveFields
	return st, nil
}

func splitSpaces(line []byte, out [][]byte) int {
	n := 0
	start := -1
	for i, c := range line {
		if c == ' ' || c == '\t' {
			if start >= 0 {
				if n == len(out) {
					return n
				}
				out[n] = line[start:i]
				n++
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 && n < len(out) {
		out[n] = line[start:]
		n++
	}
	return n
}

func col(cols [][]byte, i int) []byte {
	if i < 0 || i >= len(cols) {
		return nil
	}
	return cols[i]
}

func parseLine(e *Entry, cols [][]byte, lay layout, headerDate string, opt Options, st *Stats) bool {
	date := col(cols, lay.date)
	if date == nil {
		date = []byte(headerDate)
	}
	t, ok := parseDateTime(date, col(cols, lay.time))
	if !ok {
		return false
	}
	e.Unix = t

	var ip netip.Addr
	if opt.PreferXFF && lay.xff >= 0 {
		if a, ok := parseXFF(col(cols, lay.xff)); ok {
			ip = a
			st.UsedXFF++
		}
	}
	if !ip.IsValid() {
		a, ok := parseIP(col(cols, lay.cip))
		if !ok {
			return false
		}
		ip = a
	}
	e.IP = ip

	e.Status = 0
	if s := col(cols, lay.status); len(s) == 3 {
		e.Status = int(s[0]-'0')*100 + int(s[1]-'0')*10 + int(s[2]-'0')
		if e.Status < 100 || e.Status > 999 {
			e.Status = 0
		}
	}
	e.UA = col(cols, lay.ua)
	return true
}

func parseIP(b []byte) (netip.Addr, bool) {
	if len(b) == 0 || (len(b) == 1 && b[0] == '-') {
		return netip.Addr{}, false
	}
	s := string(b)
	a, err := netip.ParseAddr(s)
	if err != nil {
		// "1.2.3.4:5678" oder "[::1]:5678"
		if ap, err2 := netip.ParseAddrPort(s); err2 == nil {
			a = ap.Addr()
		} else if i := strings.IndexByte(s, '%'); i > 0 { // Zone-ID
			if a, err = netip.ParseAddr(s[:i]); err != nil {
				return netip.Addr{}, false
			}
		} else {
			return netip.Addr{}, false
		}
	}
	return a.WithZone("").Unmap(), true
}

// parseXFF nimmt die erste (Client-)Adresse aus "a,+b" / "a, b" / "a:port".
func parseXFF(b []byte) (netip.Addr, bool) {
	if len(b) == 0 || (len(b) == 1 && b[0] == '-') {
		return netip.Addr{}, false
	}
	if i := bytes.IndexByte(b, ','); i >= 0 {
		b = b[:i]
	}
	b = bytes.Trim(b, "+ \"")
	return parseIP(b)
}

// parseDateTime parst "2026-08-30" und "23:59:27" ohne time.Parse (schnell).
func parseDateTime(d, t []byte) (int64, bool) {
	if len(d) != 10 || d[4] != '-' || d[7] != '-' {
		return 0, false
	}
	y, ok1 := atoi(d[0:4])
	m, ok2 := atoi(d[5:7])
	day, ok3 := atoi(d[8:10])
	if !ok1 || !ok2 || !ok3 || m < 1 || m > 12 || day < 1 || day > 31 {
		return 0, false
	}
	var hh, mm, ss int
	if len(t) >= 8 && t[2] == ':' && t[5] == ':' {
		var ok4, ok5, ok6 bool
		hh, ok4 = atoi(t[0:2])
		mm, ok5 = atoi(t[3:5])
		ss, ok6 = atoi(t[6:8])
		if !ok4 || !ok5 || !ok6 || hh > 23 || mm > 59 || ss > 60 {
			return 0, false
		}
	} else if t != nil {
		return 0, false
	}
	return daysFromCivil(y, m, day)*86400 + int64(hh*3600+mm*60+ss), true
}

func atoi(b []byte) (int, bool) {
	n := 0
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// daysFromCivil: Tage seit 1970-01-01 (Algorithmus von Howard Hinnant).
func daysFromCivil(y, m, d int) int64 {
	if m <= 2 {
		y--
	}
	era := y / 400
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return int64(era*146097 + doe - 719468)
}
