// Package web is the pec user interface: a local HTTP server rendering
// html/template pages with htmx 4 for the interactive parts. All assets are
// embedded so the deployable is the single binary.
package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/exploded/pec/internal/tcs"
)

// all: is required because fragment files start with "_", which a plain
// directory embed would skip.
//
//go:embed all:templates
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Options configures the server.
type Options struct {
	DataDir string         // uploaded files live under DataDir/files
	Loc     *time.Location // zone the PHD2 log timestamps were written in
	Version string
	TCS     tcs.Config
}

// Server holds parsed templates and the request handlers.
type Server struct {
	opt   Options
	pages map[string]*template.Template
	log   *slog.Logger
}

// New loads the templates and prepares the data directory.
func New(opt Options, logger *slog.Logger) (*Server, error) {
	if opt.Loc == nil {
		opt.Loc = time.Local
	}
	if opt.TCS.ArcsecPerTick == 0 {
		opt.TCS = tcs.DefaultConfig()
	}
	if err := os.MkdirAll(filepath.Join(opt.DataDir, "files"), 0o755); err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}
	pages, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	return &Server{opt: opt, pages: pages, log: logger}, nil
}

// Handler builds the router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServerFS(static))))
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /analyse", s.analysePage)
	mux.HandleFunc("POST /analyse/upload", s.analyseUpload)
	mux.HandleFunc("POST /analyse", s.analyseRun)
	mux.HandleFunc("GET /table", s.tablePage)
	mux.HandleFunc("POST /table", s.tableRun)

	cop := http.NewCrossOriginProtection()
	return cop.Handler(s.logging(mux))
}

func cacheStatic(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		h.ServeHTTP(w, r)
	})
}

func (s *Server) logging(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h.ServeHTTP(w, r)
		if !strings.HasPrefix(r.URL.Path, "/static/") {
			s.log.Info("http", "method", r.Method, "path", r.URL.Path, "ms", time.Since(start).Milliseconds())
		}
	})
}

// loadTemplates parses layouts and partials into a base, then clones the
// base per page so each page's "content" block is isolated.
func loadTemplates() (map[string]*template.Template, error) {
	funcs := template.FuncMap{
		"f0":  func(v float64) string { return fmt.Sprintf("%.0f", v) },
		"f1":  func(v float64) string { return fmt.Sprintf("%.1f", v) },
		"f2":  func(v float64) string { return fmt.Sprintf("%.2f", v) },
		"f3":  func(v float64) string { return fmt.Sprintf("%.3f", v) },
		"pct": func(v float64) string { return fmt.Sprintf("%.0f%%", 100*v) },
		"dur": func(d time.Duration) string { return d.Round(time.Second).String() },
		"ts":  func(t time.Time) string { return t.Format("2006-01-02 15:04:05") },
		"lower": strings.ToLower,
		// unit conversions between arcsec and encoder ticks
		"ticks": func(arcsec float64, cfg tcs.Config) float64 { return arcsec / cfg.ArcsecPerTick },
		"arc":   func(ticks int, cfg tcs.Config) float64 { return float64(ticks) * cfg.ArcsecPerTick },
		"arcf":  func(ticks float64, cfg tcs.Config) float64 { return ticks * cfg.ArcsecPerTick },
	}
	base := template.New("").Funcs(funcs)
	base, err := base.ParseFS(templateFS, "templates/layouts/*.html", "templates/partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("templates: %w", err)
	}
	const dir = "templates/pages"
	entries, err := fs.ReadDir(templateFS, dir)
	if err != nil {
		return nil, fmt.Errorf("templates: %w", err)
	}
	var frags []string
	var pageFiles []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		if strings.HasPrefix(e.Name(), "_") {
			frags = append(frags, dir+"/"+e.Name())
		} else {
			pageFiles = append(pageFiles, e.Name())
		}
	}
	pages := map[string]*template.Template{}
	for _, f := range pageFiles {
		clone, err := base.Clone()
		if err != nil {
			return nil, err
		}
		files := append([]string{dir + "/" + f}, frags...)
		if clone, err = clone.ParseFS(templateFS, files...); err != nil {
			return nil, fmt.Errorf("templates: %s: %w", f, err)
		}
		pages[strings.TrimSuffix(f, ".html")] = clone
	}
	return pages, nil
}

// pageData is the envelope every template receives.
type pageData struct {
	Title   string
	Version string
	Nav     string // active nav item
	Error   string
	Data    any
}

// render executes the named template of a page into a buffer first so a
// template error yields a real 500 rather than half a page.
func (s *Server) render(w http.ResponseWriter, status int, page, name string, d pageData) {
	tmpl, ok := s.pages[page]
	if !ok {
		s.fail(w, fmt.Errorf("no template %q", page))
		return
	}
	d.Version = s.opt.Version
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, d); err != nil {
		s.fail(w, fmt.Errorf("render %s/%s: %w", page, name, err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// page renders a full page or, for an htmx request, just the fragment.
func (s *Server) page(w http.ResponseWriter, r *http.Request, page, fragment string, d pageData) {
	name := "base"
	if isHTMX(r) && fragment != "" {
		name = fragment
	}
	s.render(w, http.StatusOK, page, name, d)
}

// problem renders an error fragment with 422 so htmx routes it to the
// form's error slot, or a full page with the message for plain requests.
func (s *Server) problem(w http.ResponseWriter, r *http.Request, page string, d pageData, msg string) {
	d.Error = msg
	if isHTMX(r) {
		s.render(w, http.StatusUnprocessableEntity, page, "problem", d)
		return
	}
	s.render(w, http.StatusUnprocessableEntity, page, "base", d)
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	s.log.Error("request failed", "err", err)
	http.Error(w, "internal error: "+err.Error(), http.StatusInternalServerError)
}

func isHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

// maxUpload caps guide-log uploads; a night of PHD2 logging is well under 1 MB.
const maxUpload = 32 << 20

// saveUpload reads the form file, stores it by content hash under the data
// directory, and returns the hash and original name.
func (s *Server) saveUpload(r *http.Request, field string) (sha, name string, err error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxUpload)
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		return "", "", fmt.Errorf("upload too large or malformed: %w", err)
	}
	f, hdr, err := r.FormFile(field)
	if err != nil {
		return "", "", errors.New("choose a file first")
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return "", "", err
	}
	if len(data) == 0 {
		return "", "", errors.New("the file is empty")
	}
	sum := sha256.Sum256(data)
	sha = hex.EncodeToString(sum[:])
	path := s.filePath(sha)
	if _, err := os.Stat(path); err != nil {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return "", "", fmt.Errorf("saving upload: %w", err)
		}
	}
	return sha, filepath.Base(hdr.Filename), nil
}

func (s *Server) filePath(sha string) string {
	return filepath.Join(s.opt.DataDir, "files", sha)
}

// validSHA guards path construction from form input.
func validSHA(sha string) bool {
	if len(sha) != 64 {
		return false
	}
	_, err := hex.DecodeString(sha)
	return err == nil
}
