package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/exploded/pec/internal/analysis"
	"github.com/exploded/pec/internal/phd2"
	"github.com/exploded/pec/internal/store"
	"github.com/exploded/pec/internal/tcs"
)

// --- Analyse -------------------------------------------------------------

type analyseView struct {
	Params   analysis.Params
	FileSHA  string
	FileName string
	Sessions []analysis.SessionSummary
	Local    []localLog // guide logs found in the PHD2 folder on this PC, newest first
	Live     []liveFile // runs recorded from PHD2's event server, newest first
	PHD2Dir  string
	Runs     []runRow // every analysis so far, newest first
}

// localLog is a guide log in the PHD2 folder, offered so the file dialog
// can be skipped when pec runs on the observatory PC.
type localLog struct {
	Name  string
	Size  string
	When  string
	Today bool
}

// localLogs lists PHD2_GuideLog_*.txt in the PHD2 folder, newest first.
func (s *Server) localLogs(ctx context.Context) []localLog {
	entries, err := os.ReadDir(s.phd2Dir(ctx))
	if err != nil {
		return nil
	}
	type f struct {
		name string
		mod  time.Time
		size int64
	}
	var files []f
	for _, e := range entries {
		if e.IsDir() || !isGuideLogName(e.Name()) {
			continue
		}
		if info, err := e.Info(); err == nil {
			files = append(files, f{e.Name(), info.ModTime(), info.Size()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	if len(files) > 20 {
		files = files[:20]
	}
	loc := s.loc(ctx)
	today := time.Now().In(loc).Format("2006-01-02")
	out := make([]localLog, len(files))
	for i, x := range files {
		mod := x.mod.In(loc)
		out[i] = localLog{Name: x.name, Size: fmt.Sprintf("%.0f KB", float64(x.size)/1024), When: mod.Format("2006-01-02 15:04"), Today: mod.Format("2006-01-02") == today}
	}
	return out
}

// isGuideLogName guards the local path: PHD2's own naming, no separators.
func isGuideLogName(n string) bool {
	return strings.HasPrefix(n, "PHD2_GuideLog_") && strings.HasSuffix(n, ".txt") && n == filepath.Base(n)
}

// analyseView is the Runs page: the pickers plus every run so far.
func (s *Server) analyseView(ctx context.Context) analyseView {
	v := analyseView{Params: analysis.DefaultParams(), Local: s.localLogs(ctx), Live: s.liveFiles(ctx, 20), PHD2Dir: s.phd2Dir(ctx)}
	if rows, err := s.runRows(ctx, 200); err == nil {
		v.Runs = rows
	}
	return v
}

func (s *Server) analysePage(w http.ResponseWriter, r *http.Request) {
	s.page(w, r, "analyse", "", pageData{Title: "Runs", Nav: "analyse", Data: s.analyseView(r.Context())})
}

// analyseLocal takes a guide log straight from the PHD2 folder: it is
// copied into the data directory by content hash exactly as an upload is.
func (s *Server) analyseLocal(w http.ResponseWriter, r *http.Request) {
	base := pageData{Title: "Runs", Nav: "analyse", Data: s.analyseView(r.Context())}
	_ = r.ParseForm()
	name := r.FormValue("name")
	if !isGuideLogName(name) {
		s.problem(w, r, "analyse", base, "pick a guide log from the list")
		return
	}
	dir := s.phd2Dir(r.Context())
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		s.problem(w, r, "analyse", base, "could not read "+name+" from "+dir)
		return
	}
	sha, err := s.storeBytes(r, data, "phd2", name)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.analyseSessions(w, r, sha, name)
}

// loadLog parses a stored guide log or live recording; the two are told
// apart by their first byte.
func (s *Server) loadLog(ctx context.Context, sha string) (*phd2.Log, error) {
	return phd2.LoadFile(s.filePath(sha), s.loc(ctx))
}

// storeBytes files a document by content hash, like saveUpload does for a
// browser upload.
func (s *Server) storeBytes(r *http.Request, data []byte, kind, name string) (string, error) {
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	path := s.filePath(sha)
	if _, err := os.Stat(path); err != nil {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return "", fmt.Errorf("saving file: %w", err)
		}
	}
	return sha, s.st.EnsureFile(r.Context(), sha, kind, name, int64(len(data)))
}

// analyseSessions parses a stored log and renders the session picker.
func (s *Server) analyseSessions(w http.ResponseWriter, r *http.Request, sha, name string) {
	base := pageData{Title: "Runs", Nav: "analyse", Data: s.analyseView(r.Context())}
	l, err := s.loadLog(r.Context(), sha)
	if err != nil {
		s.problem(w, r, "analyse", base, "not a PHD2 guide log or recording: "+err.Error())
		return
	}
	v := s.analyseView(r.Context())
	v.FileSHA, v.FileName, v.Sessions = sha, name, analysis.Summaries(l)
	if len(v.Sessions) == 0 {
		s.problem(w, r, "analyse", base, "the log contains no guiding sessions")
		return
	}
	s.page(w, r, "analyse", "analyse/_sessions", pageData{Title: "Runs", Nav: "analyse", Data: v})
}

func (s *Server) analyseUpload(w http.ResponseWriter, r *http.Request) {
	sha, name, err := s.saveUpload(r, "log", "phd2")
	if err != nil {
		s.problem(w, r, "analyse", pageData{Title: "Runs", Nav: "analyse", Data: s.analyseView(r.Context())}, err.Error())
		return
	}
	s.analyseSessions(w, r, sha, name)
}

// analyseRun fits the chosen session, stores it, and sends the browser to
// the run page.
func (s *Server) analyseRun(w http.ResponseWriter, r *http.Request) {
	base := pageData{Nav: "analyse", Data: analyseView{Params: analysis.DefaultParams()}}
	if err := r.ParseForm(); err != nil {
		s.problem(w, r, "analyse", base, "bad form")
		return
	}
	sha := r.FormValue("file")
	if !validSHA(sha) {
		s.problem(w, r, "analyse", base, "upload a log first")
		return
	}
	p, err := parseParams(r)
	if err != nil {
		s.problem(w, r, "analyse", base, err.Error())
		return
	}
	l, err := s.loadLog(r.Context(), sha)
	if err != nil {
		s.problem(w, r, "analyse", base, err.Error())
		return
	}
	n, _ := strconv.Atoi(r.FormValue("session"))
	sess, err := l.Session(n)
	if err != nil {
		s.problem(w, r, "analyse", base, err.Error())
		return
	}
	res, err := analysis.Session(sess, p)
	if err != nil {
		s.problem(w, r, "analyse", base, err.Error())
		return
	}
	meta := store.SaveMeta{FileSHA: sha, SourceName: r.FormValue("name"), Version: s.opt.Version, Notes: r.FormValue("notes"), PecOn: parsePecOn(r.FormValue("pec_on"))}
	id, err := s.st.SaveSession(r.Context(), res, meta)
	if err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, fmt.Sprintf("/runs/%d", id))
}

func parsePecOn(v string) *bool {
	var b bool
	switch v {
	case "on":
		b = true
	case "off":
		b = false
	default:
		return nil
	}
	return &b
}

func parseParams(r *http.Request) (analysis.Params, error) {
	p := analysis.DefaultParams()
	num := func(field string, dst *float64) error {
		v := r.FormValue(field)
		if v == "" {
			return nil
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("%s: not a number", field)
		}
		*dst = f
		return nil
	}
	var harm, poly, minseg float64 = float64(p.Harmonics), float64(p.PolyOrder), float64(p.MinSegment)
	for _, f := range []struct {
		name string
		dst  *float64
	}{
		{"harmonics", &harm}, {"period", &p.Period}, {"scan_min", &p.ScanMin}, {"scan_max", &p.ScanMax},
		{"scan_step", &p.ScanStep}, {"poly", &poly}, {"exclude", &p.ExcludeAfter}, {"min_segment", &minseg},
		{"ra_sign", &p.RASign},
	} {
		if err := num(f.name, f.dst); err != nil {
			return p, err
		}
	}
	p.Harmonics, p.PolyOrder, p.MinSegment = int(harm), int(poly), int(minseg)
	return p, p.Validate()
}

// --- Table ----------------------------------------------------------------

type tableView struct {
	Harmonics     int
	ArcsecPerTick float64
}

func (s *Server) tablePage(w http.ResponseWriter, r *http.Request) {
	s.page(w, r, "table", "", pageData{Title: "Analyse a TCS table", Nav: "fit",
		Data: tableView{Harmonics: 6, ArcsecPerTick: s.opt.TCS.ArcsecPerTick}})
}

func (s *Server) tableRun(w http.ResponseWriter, r *http.Request) {
	base := pageData{Nav: "fit", Data: tableView{Harmonics: 6, ArcsecPerTick: s.opt.TCS.ArcsecPerTick}}
	sha, name, err := s.saveUpload(r, "table", "tcs")
	if err != nil {
		s.problem(w, r, "table", base, err.Error())
		return
	}
	harm := 6
	if v := r.FormValue("harmonics"); v != "" {
		if harm, err = strconv.Atoi(v); err != nil {
			s.problem(w, r, "table", base, "harmonics: not a number")
			return
		}
	}
	cfg := s.opt.TCS
	if v := r.FormValue("arcsec_per_tick"); v != "" {
		if cfg.ArcsecPerTick, err = strconv.ParseFloat(v, 64); err != nil {
			s.problem(w, r, "table", base, "arcsec per tick: not a number")
			return
		}
	}
	tbl, err := tcs.ReadFile(s.filePath(sha))
	if err != nil {
		s.problem(w, r, "table", base, "not a TCS PEC table: "+errors.Unwrap(err).Error())
		return
	}
	res, err := analysis.Table(tbl, cfg, harm)
	if err != nil {
		s.problem(w, r, "table", base, err.Error())
		return
	}
	id, err := s.st.SaveTable(r.Context(), res, store.SaveMeta{FileSHA: sha, SourceName: name, Version: s.opt.Version, Notes: r.FormValue("notes")})
	if err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, fmt.Sprintf("/runs/%d", id))
}
