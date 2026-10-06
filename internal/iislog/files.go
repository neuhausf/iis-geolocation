package iislog

import (
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Source ist eine einzelne lesbare Logquelle (Datei, Datei in ZIP, ...).
type Source struct {
	Name string // Anzeigename
	Size int64  // (unkomprimierte) Grösse, falls bekannt; sonst komprimiert
	Open func() (io.ReadCloser, error)
}

// IsLogName meldet, ob ein Dateiname nach einem (ggf. gepackten) Log aussieht.
func IsLogName(name string) bool {
	n := strings.ToLower(name)
	return strings.HasSuffix(n, ".log") || strings.HasSuffix(n, ".log.gz") || strings.HasSuffix(n, ".gz") ||
		strings.HasSuffix(n, ".zip") || strings.HasSuffix(n, ".txt")
}

// Expand löst Dateien, Ordner (rekursiv) und Platzhalter (*.log) zu Quellen auf.
func Expand(pattern string) ([]Source, error) {
	pattern = strings.Trim(strings.TrimSpace(pattern), "\"")
	if pattern == "" {
		return nil, nil
	}
	var paths []string
	if strings.ContainsAny(pattern, "*?[") {
		m, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		if len(m) == 0 {
			return nil, fmt.Errorf("keine Dateien gefunden für %q", pattern)
		}
		paths = m
	} else {
		paths = []string{pattern}
	}
	var out []Source
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if fi.IsDir() {
			err := filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil // unlesbare Ordner überspringen
				}
				if !d.IsDir() && IsLogName(d.Name()) {
					s, err := fileSources(path)
					if err == nil {
						out = append(out, s...)
					}
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			continue
		}
		s, err := fileSources(p)
		if err != nil {
			return nil, err
		}
		out = append(out, s...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func fileSources(path string) ([]Source, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".zip") {
		return zipSources(path)
	}
	src := Source{
		Name: path,
		Size: fi.Size(),
		Open: func() (io.ReadCloser, error) {
			f, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			if strings.HasSuffix(lower, ".gz") {
				return newGzip(f)
			}
			return f, nil
		},
	}
	return []Source{src}, nil
}

func zipSources(path string) ([]Source, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var out []Source
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !IsLogName(f.Name) || strings.HasSuffix(strings.ToLower(f.Name), ".zip") {
			continue
		}
		name := f.Name
		size := int64(f.UncompressedSize64)
		out = append(out, Source{
			Name: path + " → " + name,
			Size: size,
			Open: func() (io.ReadCloser, error) {
				zr, err := zip.OpenReader(path)
				if err != nil {
					return nil, err
				}
				for _, zf := range zr.File {
					if zf.Name == name {
						rc, err := zf.Open()
						if err != nil {
							zr.Close()
							return nil, err
						}
						var r io.ReadCloser = multiCloser{rc, rc, zr}
						if strings.HasSuffix(strings.ToLower(name), ".gz") {
							return newGzip(r)
						}
						return r, nil
					}
				}
				zr.Close()
				return nil, fmt.Errorf("%s nicht in %s gefunden", name, path)
			},
		})
	}
	return out, nil
}

type multiCloser struct {
	io.Reader
	a, b io.Closer
}

func (m multiCloser) Close() error {
	err := m.a.Close()
	if err2 := m.b.Close(); err == nil {
		err = err2
	}
	return err
}

func newGzip(f io.ReadCloser) (io.ReadCloser, error) {
	gz, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return multiCloser{gz, gz, f}, nil
}
