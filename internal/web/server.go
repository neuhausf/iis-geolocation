// Package web stellt die lokale Browser-Oberfläche bereit.
package web

import (
	"compress/gzip"
	"crypto/rand"
	"embed"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/neuhausf/iis-geolocation/internal/iislog"
	"github.com/neuhausf/iis-geolocation/internal/store"
)

//go:embed static
var staticFS embed.FS

// Server ist die HTTP-Oberfläche.
type Server struct {
	Store   *store.Store
	Version string
	Quit    func()
	// DefaultXFF ist die Vorgabe für "X-Forwarded-For bevorzugen".
	DefaultXFF bool

	token string
	port  int
}

// Listen öffnet einen Port auf 127.0.0.1 (bevorzugt port, sonst zufällig).
func Listen(port int) (net.Listener, error) {
	if port > 0 {
		if l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			return l, nil
		}
	}
	return net.Listen("tcp", "127.0.0.1:0")
}

// URL liefert die Adresse inkl. Zugriffsschlüssel.
func (s *Server) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/", s.port)
}

// Init legt Zugriffsschlüssel und Port fest (vor URL/Serve aufrufen).
func (s *Server) Init(l net.Listener) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	s.token = hex.EncodeToString(b)
	s.port = l.Addr().(*net.TCPAddr).Port
}

// Serve beantwortet Anfragen auf l.
func (s *Server) Serve(l net.Listener) error {
	if s.token == "" {
		s.Init(l)
	}

	mux := http.NewServeMux()
	sub, _ := fs.Sub(staticFS, "static")
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		data, err := fs.ReadFile(sub, p)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if p == "index.html" {
			data = []byte(strings.ReplaceAll(string(data), "{{TOKEN}}", s.token))
			data = []byte(strings.ReplaceAll(string(data), "{{VERSION}}", s.Version))
		}
		ct := mime.TypeByExtension(path.Ext(p))
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("/api/info", s.api(s.handleInfo))
	mux.HandleFunc("/api/status", s.api(s.handleStatus))
	mux.HandleFunc("/api/query", s.api(s.handleQuery))
	mux.HandleFunc("/api/export", s.api(s.handleExport))
	mux.HandleFunc("/api/load-path", s.api(s.handleLoadPath))
	mux.HandleFunc("/api/upload", s.api(s.handleUpload))
	mux.HandleFunc("/api/reset", s.api(s.handleReset))
	mux.HandleFunc("/api/quit", s.api(s.handleQuit))

	srv := &http.Server{Handler: s.guard(mux), ReadHeaderTimeout: 30 * time.Second}
	return srv.Serve(l)
}

// guard lässt nur lokale Host-Header zu (Schutz gegen DNS-Rebinding).
func (s *Server) guard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if hst, _, err := net.SplitHostPort(host); err == nil {
			host = hst
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" && host != "[::1]" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) api(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t := r.Header.Get("X-Token")
		if t == "" {
			t = r.URL.Query().Get("t")
		}
		if t != s.token {
			http.Error(w, "Ungültiger Zugriffsschlüssel – bitte die Seite über die EXE neu öffnen.", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	g := s.Store.Geo()
	writeJSON(w, map[string]any{
		"version":    s.Version,
		"dbs":        g.Infos,
		"hasCity":    g.HasCity(),
		"defaultXff": s.DefaultXFF,
		"localTz":    tzLabel(),
	})
}

func tzLabel() string {
	name, off := time.Now().Zone()
	return fmt.Sprintf("%s, UTC%+d", name, off/3600)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"progress": s.Store.Progress(),
		"files":    s.Store.Files(),
	})
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	var q store.Query
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	res, err := s.Store.Run(q)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, res)
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	var q store.Query
	if err := json.Unmarshal([]byte(r.URL.Query().Get("q")), &q); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	q.Limit = -1
	res, err := s.Store.Run(q)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	kind := r.URL.Query().Get("type")
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="iis-geo-%s-%s.csv"`, kind, time.Now().Format("20060102-1504")))
	WriteCSV(w, kind, res)
}

// WriteCSV schreibt eine Ergebnisliste als Excel-freundliches CSV (UTF-8 mit BOM, Semikolon).
func WriteCSV(w io.Writer, kind string, res *store.Result) {
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	itoa := func(n int64) string { return strconv.FormatInt(n, 10) }
	pct := func(n, total int) string {
		if total == 0 {
			return "0"
		}
		return strings.Replace(strconv.FormatFloat(float64(n)*100/float64(total), 'f', 2, 64), ".", ",", 1)
	}
	switch kind {
	case "isps":
		_ = cw.Write([]string{"ASN", "ISP / Organisation", "Land", "IP-Adressen", "Anteil IPs %", "Anfragen"})
		for _, x := range res.ISPs {
			_ = cw.Write([]string{strconv.Itoa(int(x.ASN)), x.Org, x.CC, strconv.Itoa(x.IPs), pct(x.IPs, res.IPs), itoa(x.Hits)})
		}
	case "regions":
		_ = cw.Write([]string{"Land", "Region", "IP-Adressen", "Anteil IPs %", "Anfragen"})
		for _, x := range res.Regions {
			_ = cw.Write([]string{x.CC, x.Region, strconv.Itoa(x.IPs), pct(x.IPs, res.IPs), itoa(x.Hits)})
		}
	case "ips":
		_ = cw.Write([]string{"IP-Adresse", "Land (Code)", "Land", "Region", "Stadt", "ASN", "ISP / Organisation", "Anfragen", "Erste Anfrage", "Letzte Anfrage", "User-Agent (erster)"})
		for _, x := range res.IPRows {
			country := x.Country
			if x.Private {
				country = "Privat / intern"
			}
			asn := ""
			if x.ASN != 0 {
				asn = strconv.Itoa(int(x.ASN))
			}
			_ = cw.Write([]string{x.IP, x.CC, country, x.Region, x.City, asn, x.Org, itoa(x.Hits), x.First, x.Last, x.UA})
		}
	default:
		_ = cw.Write([]string{"Rang", "Land (Code)", "Land", "IP-Adressen", "Anteil IPs %", "Anfragen"})
		for i, x := range res.Countries {
			_ = cw.Write([]string{strconv.Itoa(i + 1), x.CC, x.Name, strconv.Itoa(x.IPs), pct(x.IPs, res.IPs), itoa(x.Hits)})
		}
	}
	cw.Flush()
}

func (s *Server) handleLoadPath(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Paths []string `json:"paths"`
		XFF   bool     `json:"xff"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var srcs []iislog.Source
	for _, p := range req.Paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		x, err := iislog.Expand(p)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		srcs = append(srcs, x...)
	}
	if len(srcs) == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("keine Logdateien gefunden (*.log, *.gz, *.zip)"))
		return
	}
	go s.Store.LoadSources(srcs, iislog.Options{PreferXFF: req.XFF})
	writeJSON(w, map[string]any{"queued": len(srcs)})
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST erwartet", http.StatusMethodNotAllowed)
		return
	}
	opt := iislog.Options{PreferXFF: r.URL.Query().Get("xff") == "1"}
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var results []store.FileInfo
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		name := part.FileName()
		if name == "" {
			part.Close()
			continue
		}
		size, _ := strconv.ParseInt(r.URL.Query().Get("size_"+part.FormName()), 10, 64)
		results = append(results, s.loadUpload(name, size, part, opt)...)
		part.Close()
	}
	writeJSON(w, map[string]any{"files": results})
}

func (s *Server) loadUpload(name string, size int64, r io.Reader, opt iislog.Options) []store.FileInfo {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		// ZIP braucht wahlfreien Zugriff -> temporär speichern
		f, err := os.CreateTemp("", "iis-geo-*.zip")
		if err != nil {
			return []store.FileInfo{{Name: name, Error: err.Error()}}
		}
		defer os.Remove(f.Name())
		_, err = io.Copy(f, r)
		f.Close()
		if err != nil {
			return []store.FileInfo{{Name: name, Error: err.Error()}}
		}
		srcs, err := iislog.Expand(f.Name())
		if err != nil {
			return []store.FileInfo{{Name: name, Error: err.Error()}}
		}
		for i := range srcs {
			srcs[i].Name = name + " → " + srcs[i].Name[strings.Index(srcs[i].Name, "→ ")+len("→ "):]
		}
		return s.Store.LoadSources(srcs, opt)
	case strings.HasSuffix(lower, ".gz"):
		gz, err := gzipReader(r)
		if err != nil {
			return []store.FileInfo{{Name: name, Error: err.Error()}}
		}
		defer gz.Close()
		return []store.FileInfo{s.Store.LoadReader(name, gz, size, opt)}
	default:
		return []store.FileInfo{s.Store.LoadReader(name, r, size, opt)}
	}
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	s.Store.Reset()
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]bool{"ok": true})
	if s.Quit != nil {
		go func() { time.Sleep(300 * time.Millisecond); s.Quit() }()
	}
}

func gzipReader(r io.Reader) (io.ReadCloser, error) {
	return gzip.NewReader(r)
}
