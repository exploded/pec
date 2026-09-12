package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

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
}

func (s *Server) analysePage(w http.ResponseWriter, r *http.Request) {
	s.page(w, r, "analyse", "", pageData{Title: "Analyse a guide log", Nav: "analyse",
		Data: analyseView{Params: analysis.DefaultParams()}})
}

func (s *Server) analyseUpload(w http.ResponseWriter, r *http.Request) {
	base := pageData{Title: "Analyse a guide log", Nav: "analyse", Data: analyseView{Params: analysis.DefaultParams()}}
	sha, name, err := s.saveUpload(r, "log", "phd2")
	if err != nil {
		s.problem(w, r, "analyse", base, err.Error())
		return
	}
	l, err := phd2.ParseFile(s.filePath(sha), s.opt.Loc)
	if err != nil {
		s.problem(w, r, "analyse", base, "not a PHD2 guide log: "+err.Error())
		return
	}
	v := analyseView{Params: analysis.DefaultParams(), FileSHA: sha, FileName: name, Sessions: analysis.Summaries(l)}
	if len(v.Sessions) == 0 {
		s.problem(w, r, "analyse", base, "the log contains no guiding sessions")
		return
	}
	s.page(w, r, "analyse", "analyse/_sessions", pageData{Title: "Analyse a guide log", Nav: "analyse", Data: v})
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
	l, err := phd2.ParseFile(s.filePath(sha), s.opt.Loc)
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
	s.page(w, r, "table", "", pageData{Title: "Analyse a TCS table", Nav: "table",
		Data: tableView{Harmonics: 6, ArcsecPerTick: s.opt.TCS.ArcsecPerTick}})
}

func (s *Server) tableRun(w http.ResponseWriter, r *http.Request) {
	base := pageData{Nav: "table", Data: tableView{Harmonics: 6, ArcsecPerTick: s.opt.TCS.ArcsecPerTick}}
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
