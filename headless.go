package main

import (
	"log"
	"os"
	"strings"

	"github.com/Serge-Nook/aegis/internal/core"
	"github.com/Serge-Nook/aegis/internal/remote"
)

// runHeadless serves the HTTP API for the other workplaces without a window
// (a dedicated server for 50+ users, or PostgreSQL behind an API).
func runHeadless(db, pg, listen, name, admin string) int {
	kind, dsn := core.KindSQLite, db
	if pg != "" {
		kind, dsn = core.KindPostgres, pg
	}
	svc, err := core.Open(kind, dsn)
	if err != nil {
		log.Printf("open database: %v", err)
		return 1
	}
	defer svc.Close()
	if need, err := svc.NeedsSetup(); err == nil && need {
		login, pass, ok := strings.Cut(admin, ":")
		if !ok {
			log.Print("the database has no users: start once with --admin login:password")
			return 1
		}
		if err := svc.CreateFirstAdmin(login, "Администратор", pass); err != nil {
			log.Printf("create administrator: %v", err)
			return 1
		}
		log.Printf("administrator %q created", login)
	}
	if name == "" {
		name, _ = os.Hostname()
	}
	log.Printf("serving %s database on %s", kind, listen)
	if err := remote.NewServer(svc, name, appVersion).ListenAndServe(listen); err != nil {
		log.Printf("server: %v", err)
		return 1
	}
	return 0
}
