// iis-geo: IIS-Logs einlesen, IP-Adressen geolokalisieren und nach Land auswerten.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"text/tabwriter"
	_ "time/tzdata"

	"github.com/neuhausf/iis-geolocation/internal/geo"
	"github.com/neuhausf/iis-geolocation/internal/iislog"
	"github.com/neuhausf/iis-geolocation/internal/store"
	"github.com/neuhausf/iis-geolocation/internal/web"
)

var version = "dev"

func main() {
	fl := flag.NewFlagSet("iis-geo", flag.ExitOnError)
	port := fl.Int("port", 8077, "bevorzugter lokaler Port der Oberfläche (belegt => zufällig)")
	noBrowser := fl.Bool("no-browser", false, "Browser nicht automatisch öffnen")
	xff := fl.Bool("xff", true, "IP aus X-Forwarded-For bevorzugen, falls im Log vorhanden")
	cli := fl.Bool("cli", false, "ohne Oberfläche: Auswertung in der Konsole ausgeben")
	from := fl.String("from", "", "CLI: Beginn, z.B. 2026-08-01 oder 2026-08-01T06:00")
	to := fl.String("to", "", "CLI: Ende (exklusiv; reines Datum = ganzer Tag inklusive)")
	tz := fl.String("tz", "utc", "CLI: Zeitzone für -from/-to: utc oder local")
	country := fl.String("country", "", "CLI: nur dieses Land (ISO-Code, z.B. CH)")
	noPrivate := fl.Bool("no-private", true, "CLI: private/interne IP-Adressen ignorieren")
	csvOut := fl.String("csv", "", "CLI: Ergebnis zusätzlich als CSV speichern (Länder)")
	ipsOut := fl.String("csv-ips", "", "CLI: IP-Liste als CSV speichern")
	top := fl.Int("top", 30, "CLI: Anzahl Länder in der Ausgabe")
	showVersion := fl.Bool("version", false, "Version anzeigen")
	fl.Usage = func() {
		fmt.Fprintf(os.Stderr, "iis-geo %s – IIS-Logs geolokalisieren\n\n", version)
		fmt.Fprintf(os.Stderr, "Aufruf:\n  iis-geo.exe [Optionen] [Dateien/Ordner/Muster ...]\n\n")
		fmt.Fprintf(os.Stderr, "Ohne -cli startet eine Oberfläche im Browser. Dateien können auch per\nDrag & Drop auf die EXE gezogen werden.\n\nOptionen:\n")
		fl.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nBeispiel:\n  iis-geo.exe -cli -from 2026-08-01 -to 2026-08-31 -csv laender.csv D:\\Logs\\W3SVC1\n")
	}
	_ = fl.Parse(os.Args[1:])
	if *showVersion {
		fmt.Println(version)
		return
	}

	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	wd, _ := os.Getwd()
	g, err := geo.Open(exeDir, wd)
	if err != nil {
		fatal(err)
	}
	st := store.New(g)

	var srcs []iislog.Source
	for _, a := range fl.Args() {
		x, err := iislog.Expand(a)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warnung: %v\n", err)
			continue
		}
		srcs = append(srcs, x...)
	}
	opt := iislog.Options{PreferXFF: *xff}

	if *cli {
		runCLI(st, srcs, opt, store.Query{From: *from, To: *to, TZ: *tz, Country: *country, ExcludePrivate: *noPrivate, Limit: -1}, *top, *csvOut, *ipsOut)
		return
	}

	l, err := web.Listen(*port)
	if err != nil {
		fatal(err)
	}
	srv := &web.Server{Store: st, Version: version, DefaultXFF: *xff, Quit: func() {
		fmt.Println("Beendet.")
		os.Exit(0)
	}}
	srv.Init(l)
	go func() {
		if err := srv.Serve(l); err != nil {
			fatal(err)
		}
	}()
	url := srv.URL()
	fmt.Printf("iis-geo %s\n\n", version)
	for _, d := range g.Infos {
		fmt.Printf("  Datenbank %-13s %s (%s, Stand %s)\n", d.Kind+":", d.Type, d.Source, d.Built.Format("2006-01-02"))
	}
	fmt.Printf("\nOberfläche: %s\n", url)
	fmt.Println("Dieses Fenster offen lassen. Schliessen (oder \"Beenden\" in der Oberfläche) beendet das Programm.")
	if len(srcs) > 0 {
		fmt.Printf("\nLade %d Datei(en) ...\n", len(srcs))
		go st.LoadSources(srcs, opt)
	}
	if !*noBrowser {
		openBrowser(url)
	}
	select {}
}

func runCLI(st *store.Store, srcs []iislog.Source, opt iislog.Options, q store.Query, top int, csvOut, ipsOut string) {
	if len(srcs) == 0 {
		fatal(fmt.Errorf("keine Logdateien angegeben"))
	}
	for _, fi := range st.LoadSources(srcs, opt) {
		if fi.Error != "" {
			fmt.Fprintf(os.Stderr, "%s: %s\n", fi.Name, fi.Error)
		} else {
			fmt.Fprintf(os.Stderr, "%s: %d Zeilen\n", fi.Name, fi.Parsed)
		}
	}
	res, err := st.Run(q)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("\nZeitraum Daten: %s – %s (%s)\n", res.DataFrom, res.DataTo, res.TZName)
	fmt.Printf("Anfragen: %d   Eindeutige IP-Adressen: %d   Länder: %d\n\n", res.Hits, res.IPs, len(res.Countries))
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "Rang\tCode\tLand\tIPs\tAnteil\tAnfragen\t")
	for i, c := range res.Countries {
		if i >= top {
			break
		}
		share := 0.0
		if res.IPs > 0 {
			share = float64(c.IPs) * 100 / float64(res.IPs)
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%d\t%.1f %%\t%d\t\n", i+1, c.CC, c.Name, c.IPs, share, c.Hits)
	}
	tw.Flush()
	write := func(path, kind string) {
		if path == "" {
			return
		}
		f, err := os.Create(path)
		if err != nil {
			fatal(err)
		}
		web.WriteCSV(f, kind, res)
		if err := f.Close(); err != nil {
			fatal(err)
		}
		fmt.Fprintf(os.Stderr, "gespeichert: %s\n", path)
	}
	write(csvOut, "countries")
	write(ipsOut, "ips")
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "Fehler: %v\n", err)
	if runtime.GOOS == "windows" {
		fmt.Fprintln(os.Stderr, "\n[Enter] zum Schliessen")
		_, _ = fmt.Scanln()
	}
	os.Exit(1)
}
