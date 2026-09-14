// pec measures the periodic error of a telescope mount from PHD2 guide
// logs, fits a correction curve, and writes a Bisque TCS PEC table to paste
// in by hand. It never talks to the mount.
//
// Run pec.exe (or double-click it): it serves http://127.0.0.1:8990/, keeps
// pec.db and data\ beside the executable, and opens the browser. Running it
// again while it is up just opens the browser to the running copy. Everything
// a user can change is on the Settings page. The only flags are for running a
// second test instance elsewhere: -addr, -db, -data.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"time"

	_ "time/tzdata"

	"github.com/exploded/pec/internal/store"
	"github.com/exploded/pec/internal/web"
)

func main() {
	exeDir := "."
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	addr := flag.String("addr", "127.0.0.1:8990", "developer: listen address (keep it on loopback)")
	dbPath := flag.String("db", filepath.Join(exeDir, "pec.db"), "developer: history database")
	dataDir := flag.String("data", filepath.Join(exeDir, "data"), "developer: directory for uploaded files")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: pec.exe            (serves http://127.0.0.1:8990/ and opens the browser)")
		fmt.Fprintln(os.Stderr, "       pec.exe [-addr host:port] [-db file] [-data dir]   developer test instance")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*addr, *dbPath, *dataDir); err != nil {
		fmt.Fprintln(os.Stderr, "pec:", err)
		os.Exit(1)
	}
}

func run(addr, dbPath, dataDir string) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	url := "http://" + addr + "/"

	// Bind first: a second double-click must never open the running copy's
	// database. If pec already answers on the port, just open the browser.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if pecAnswers(url) {
			fmt.Println("pec is already running at " + url)
			openBrowser(url, logger)
			return nil
		}
		return fmt.Errorf("cannot listen on %s: %w", addr, err)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		ln.Close()
		return fmt.Errorf("database %s: %w", dbPath, err)
	}
	defer st.Close()
	srv, err := web.New(web.Options{DataDir: dataDir, Version: version(), Store: st}, logger)
	if err != nil {
		ln.Close()
		return err
	}
	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	errc := make(chan error, 1)
	go func() {
		logger.Info("pec listening", "url", url, "version", version(), "db", dbPath, "data", dataDir)
		if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()
	openBrowser(url, logger)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return hs.Shutdown(shctx)
}

// pecAnswers reports whether a pec instance is serving at url.
func pecAnswers(url string) bool {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(url + "report.css")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// openBrowser opens url in the default browser on Windows. Elsewhere it
// does nothing: the URL is in the log line.
func openBrowser(url string, logger *slog.Logger) {
	if runtime.GOOS != "windows" {
		return
	}
	if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start(); err != nil {
		logger.Warn("could not open the browser", "err", err, "url", url)
	}
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
