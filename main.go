// billkaat — a local, read-only AWS health check.
// Your credentials and findings never leave this machine.
package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"

	"github.com/billkaat/billkaat/internal/buildinfo"
	"github.com/billkaat/billkaat/internal/engine"
	"github.com/billkaat/billkaat/internal/server"
	"github.com/billkaat/billkaat/internal/store"

	// Register the check sets.
	_ "github.com/billkaat/billkaat/internal/checks/free"
	_ "github.com/billkaat/billkaat/internal/checks/pro"
)

//go:embed all:web
var webFS embed.FS

func main() {
	var (
		addr    = flag.String("addr", "127.0.0.1:4141", "listen address (keep it on localhost)")
		dbPath  = flag.String("db", "billkaat.db", "path to the SQLite database")
		workers = flag.Int("workers", 4, "checks to run in parallel")
		demo    = flag.Bool("demo", false, "seed a fake completed scan (no AWS needed) and start")
	)
	flag.Parse()

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer st.Close()

	eng := &engine.Engine{Store: st, Workers: *workers}

	if *demo {
		if id, err := engine.SeedDemo(st); err != nil {
			log.Fatalf("seed demo: %v", err)
		} else {
			fmt.Printf("seeded demo scan #%d\n", id)
		}
	}

	web, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}

	srv := &server.Server{Store: st, Engine: eng, Web: web}

	fmt.Printf(`
  billkaat %s (%s edition)
  ─────────────────────────────────────────────
  →  http://%s
  data stays on this machine — nothing is sent anywhere.
  read-only AWS access; see iam-policy.json for the exact permissions.

`, buildinfo.Version, buildinfo.Edition, *addr)

	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
