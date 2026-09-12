// pec measures the periodic error of a telescope mount from PHD2 guide
// logs, fits a correction curve, and writes a Bisque TCS PEC table to paste
// in by hand. It never talks to the mount.
//
// Subcommands:
//
//	pec serve   [-addr 127.0.0.1:8990] [-db pec.db] [-data ./data] [-tz Local]
//	pec version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"time"

	_ "time/tzdata"

	"github.com/exploded/pec/internal/web"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:8990", "listen address (keep it on loopback)")
	dataDir := fs.String("data", "data", "directory for uploaded files")
	tz := fs.String("tz", "Local", "time zone the PHD2 logs were written in (IANA name or Local)")
	_ = fs.String("db", "pec.db", "SQLite database path (milestone 2)")
	_ = fs.Parse(os.Args[2:])

	switch cmd {
	case "serve":
		if err := serve(*addr, *dataDir, *tz); err != nil {
			fmt.Fprintln(os.Stderr, "pec:", err)
			os.Exit(1)
		}
	case "version":
		fmt.Println("pec", version())
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: pec <serve|version> [flags]")
	os.Exit(2)
}

func serve(addr, dataDir, tz string) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	loc := time.Local
	if tz != "" && tz != "Local" {
		var err error
		if loc, err = time.LoadLocation(tz); err != nil {
			return fmt.Errorf("time zone %q: %w", tz, err)
		}
	}
	srv, err := web.New(web.Options{DataDir: dataDir, Loc: loc, Version: version()}, logger)
	if err != nil {
		return err
	}
	hs := &http.Server{Addr: addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	errc := make(chan error, 1)
	go func() {
		logger.Info("pec listening", "url", "http://"+addr+"/", "version", version(), "tz", loc.String())
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return hs.Shutdown(shctx)
}

// version reports the VCS revision baked in by the Go toolchain, so no
// ldflags are needed: "abc1234 (2026-09-12)" or "devel".
func version() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "devel"
	}
	var rev, t, dirty string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			t = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "+dirty"
			}
		}
	}
	if rev == "" {
		return "devel"
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if len(t) >= 10 {
		t = " (" + t[:10] + ")"
	}
	return rev + dirty + t
}
