package iislog

import (
	"strings"
	"testing"
	"time"
)

const sample = `#Software: Microsoft Internet Information Services 10.0
#Version: 1.0
#Date: 2026-08-31 00:00:04
#Fields: date time s-ip cs-method cs-uri-stem cs-uri-query s-port cs-username c-ip cs(User-Agent) cs(Referer) sc-status sc-substatus sc-win32-status sc-bytes cs-bytes time-taken crypt-protocol crypt-cipher crypt-hash crypt-keyexchange
2026-08-30 23:59:27 172.18.102.130 POST /webservice/v2/NaregService.svc - 443 - 159.144.156.69 - - 200 0 0 3930 1632 606 400 6610 800d ae06
2026-08-30 23:59:35 172.18.102.130 GET /Person/Summary/28943 textCategory=22002 443 - 43.166.237.57 Mozilla/5.0+(iPhone;+CPU+iPhone+OS+13_2_3+like+Mac+OS+X) https://www.nareg.ch/ 404 0 0 7975 697 127 400 6610 800d ae06
2026-08-31 00:00:02 172.18.102.130 GET / - 443 - - - - 200 0 0 1 1 1 400 6610 800d ae06
#Fields: time c-ip sc-status X-Forwarded-For
00:00:05 10.0.0.1 301 51.154.8.227,+10.0.0.9
00:00:06 [2001:db8::1]:5555 200 -
`

func TestParse(t *testing.T) {
	var got []Entry
	st, err := Parse(strings.NewReader(sample), Options{PreferXFF: true}, func(e *Entry) { got = append(got, *e) }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Lines != 5 || st.Parsed != 4 || st.Skipped != 1 || st.UsedXFF != 1 {
		t.Fatalf("stats: %+v", st)
	}
	want := []struct {
		ip     string
		ts     string
		status int
	}{
		{"159.144.156.69", "2026-08-30T23:59:27Z", 200},
		{"43.166.237.57", "2026-08-30T23:59:35Z", 404},
		{"51.154.8.227", "2026-08-31T00:00:05Z", 301}, // Datum aus #Date, IP aus X-Forwarded-For
		{"2001:db8::1", "2026-08-31T00:00:06Z", 200},
	}
	for i, w := range want {
		e := got[i]
		ts := time.Unix(e.Unix, 0).UTC().Format(time.RFC3339)
		if e.IP.String() != w.ip || ts != w.ts || e.Status != w.status {
			t.Errorf("%d: got %s %s %d, want %+v", i, e.IP, ts, e.Status, w)
		}
	}
}

func TestDaysFromCivil(t *testing.T) {
	for _, d := range []string{"1970-01-01", "2000-02-29", "2026-08-31", "2099-12-31"} {
		tm, _ := time.Parse("2006-01-02", d)
		y, m, dd := tm.Date()
		if got := daysFromCivil(y, int(m), dd) * 86400; got != tm.Unix() {
			t.Errorf("%s: %d != %d", d, got, tm.Unix())
		}
	}
}
