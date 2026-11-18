// «Эгида» — система управления задачами и этапами работы отделов.
package main

import (
	_ "embed"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
)

const (
	appName    = "Эгида"
	appVersion = "1.0.0"
	appAuthor  = "Горшков Сергей Владимирович"
	appSite    = "https://nookbat.ru/"
	appID      = "ru.nookbat.egida"
)

//go:embed build/icon.png
var iconPNG []byte

var appIcon = fyne.NewStaticResource("icon.png", iconPNG)

func dataDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "Egida")
}

var logPath string

func setupLog() string {
	if logPath != "" {
		return logPath
	}
	dir := dataDir()
	if os.MkdirAll(dir, 0o755) != nil {
		return ""
	}
	p := filepath.Join(dir, "egida.log")
	if st, err := os.Stat(p); err == nil && st.Size() > 5<<20 {
		os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return ""
	}
	log.SetOutput(io.MultiWriter(f, os.Stderr))
	logPath = p
	return p
}

func main() {
	args := platformStartup(os.Args[1:])
	fs := flag.NewFlagSet("egida", flag.ExitOnError)
	serve := fs.Bool("serve", false, "run the server without a window")
	db := fs.String("db", filepath.Join(dataDir(), "egida.db"), "SQLite database file (with --serve)")
	pg := fs.String("pg", "", "PostgreSQL URL, e.g. postgres://user:pass@host:5432/egida (with --serve)")
	listen := fs.String("listen", fmt.Sprintf(":%d", 8080), "address of the HTTP API (with --serve)")
	name := fs.String("name", "", "server name shown in LAN discovery")
	admin := fs.String("admin", "", "login:password of the first administrator (with --serve, empty database)")
	version := fs.Bool("version", false, "print the version")
	fs.Parse(args)
	if *version {
		fmt.Println(appName, appVersion)
		return
	}
	setupLog()
	log.Printf("%s %s start, log %s", appName, appVersion, logPath)
	if *serve {
		os.Exit(runHeadless(*db, *pg, *listen, *name, *admin))
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic: %v", r)
			reportStartFailure(fmt.Sprint(r))
			os.Exit(2)
		}
	}()
	a := app.NewWithID(appID)
	a.SetIcon(appIcon)
	started := false
	a.Lifecycle().SetOnStarted(func() { started = true })
	u := newUI(a)
	u.win.ShowAndRun()
	u.shutdown()
	if !started {
		reportStartFailure(nil)
		os.Exit(1)
	}
	log.Print("exit")
}
