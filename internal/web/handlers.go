package web

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/exploded/pec/internal/analysis"
	"github.com/exploded/pec/internal/pe"
	"github.com/exploded/pec/internal/phd2"
	"github.com/exploded/pec/internal/tcs"
)

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	s.page(w, r, "index", "", pageData{Title: "pec", Nav: "home"})
}

// --- Analyse -------------------------------------------------------------

type analyseView struct {
	Params   analysis.Params
	FileSHA  string
	FileName string
	Sessions []analysis.SessionSummary
	Result   *sessionView
}

func (s *Server) analysePage(w http.ResponseWriter, r *http.Request) {
	s.page(w, r, "analyse", "", pageData{Title: "Analyse a guide log", Nav: "analyse",
		Data: analyseView{Params: analysis.DefaultParams()}})
}

func (s *Server) analyseUpload(w http.ResponseWriter, r *http.Request) {
	sha, name, err := s.saveUpload(r, "log")
	if err != nil {
		s.problem(w, r, "analyse", pageData{Title: "Analyse a guide log", Nav: "analyse", Data: analyseView{Params: analysis.DefaultParams()}}, err.Error())
		return
	}
	l, err := phd2.ParseFile(s.filePath(sha), s.opt.Loc)
	if err != nil {
		s.problem(w, r, "analyse", pageData{Title: "Analyse a guide log", Nav: "analyse", Data: analyseView{Params: analysis.DefaultParams()}}, "not a PHD2 guide log: "+err.Error())
		return
	}
	v := analyseView{Params: analysis.DefaultParams(), FileSHA: sha, FileName: name, Sessions: analysis.Summaries(l)}
	if len(v.Sessions) == 0 {
		s.problem(w, r, "analyse", pageData{Title: "Analyse a guide log", Nav: "analyse", Data: v}, "the log contains no guiding sessions")
		return
	}
	s.page(w, r, "analyse", "analyse/_sessions", pageData{Title: "Analyse a guide log", Nav: "analyse", Data: v})
}

func (s *Server) analyseRun(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.problem(w, r, "analyse", pageData{Nav: "analyse"}, "bad form")
		return
	}
	sha := r.FormValue("file")
	if !validSHA(sha) {
		s.problem(w, r, "analyse", pageData{Nav: "analyse"}, "upload a log first")
		return
	}
	p, err := parseParams(r)
	if err != nil {
		s.problem(w, r, "analyse", pageData{Nav: "analyse"}, err.Error())
		return
	}
	l, err := phd2.ParseFile(s.filePath(sha), s.opt.Loc)
	if err != nil {
		s.problem(w, r, "analyse", pageData{Nav: "analyse"}, err.Error())
		return
	}
	n, _ := strconv.Atoi(r.FormValue("session"))
	sess, err := l.Session(n)
	if err != nil {
		s.problem(w, r, "analyse", pageData{Nav: "analyse"}, err.Error())
		return
	}
	res, err := analysis.Session(sess, p)
	if err != nil {
		s.problem(w, r, "analyse", pageData{Nav: "analyse"}, err.Error())
		return
	}
	v := analyseView{Params: p, FileSHA: sha, FileName: r.FormValue("name"), Result: s.sessionView(res, r.FormValue("name"))}
	s.page(w, r, "analyse", "analyse/_result", pageData{Title: "Analysis result", Nav: "analyse", Data: v})
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

// sessionView is the result page's data: numbers already formatted where
// the template would otherwise need arithmetic.
type sessionView struct {
	Res      *analysis.SessionResult
	FileName string
	Cfg      tcs.Config

	PeriodText string
	Harmonics  []harmonicRow
	Warnings   []string
	Segments   int
	Drift      string
}

type harmonicRow struct {
	K        int
	PeriodS  float64
	AmpArc   float64
	AmpTicks float64
	PhaseDeg float64
	SigmaArc float64
}

func (s *Server) sessionView(res *analysis.SessionResult, name string) *sessionView {
	f := res.Fit
	v := &sessionView{Res: res, FileName: name, Cfg: s.opt.TCS, Warnings: res.Warnings, Segments: len(f.Segments)}
	switch {
	case f.PeriodFixed:
		v.PeriodText = fmt.Sprintf("%.2f s (pinned)", f.Period)
	case math.IsInf(f.PeriodSigma, 0):
		v.PeriodText = fmt.Sprintf("%.1f s (uncertainty unbounded)", f.Period)
	default:
		v.PeriodText = fmt.Sprintf("%.1f ± %.1f s", f.Period, f.PeriodSigma)
	}
	v.Harmonics = harmonicRows(f.Curve, f.Period, s.opt.TCS)
	if f.PolyOrder == 2 {
		v.Drift = fmt.Sprintf("%.3f″/min, curvature %.4f″/min²", f.Drift, f.Curvature)
	} else {
		v.Drift = fmt.Sprintf("%.3f″/min", f.Drift)
	}
	return v
}

func harmonicRows(c pe.Curve, period float64, cfg tcs.Config) []harmonicRow {
	rows := make([]harmonicRow, len(c.Harmonics))
	for i, h := range c.Harmonics {
		rows[i] = harmonicRow{
			K: h.K, PeriodS: period / float64(h.K), AmpArc: h.Amp, AmpTicks: h.Amp / cfg.ArcsecPerTick,
			PhaseDeg: h.PhaseDeg, SigmaArc: h.AmpSigma,
		}
	}
	return rows
}

// --- Table ----------------------------------------------------------------

type tableView struct {
	Harmonics     int
	ArcsecPerTick float64
	FileName      string
	Res           *analysis.TableResult
	Rows          []harmonicRow
	NominalPeriod float64
}

func (s *Server) tablePage(w http.ResponseWriter, r *http.Request) {
	s.page(w, r, "table", "", pageData{Title: "Analyse a TCS table", Nav: "table",
		Data: tableView{Harmonics: 6, ArcsecPerTick: s.opt.TCS.ArcsecPerTick, NominalPeriod: 150}})
}

func (s *Server) tableRun(w http.ResponseWriter, r *http.Request) {
	base := tableView{Harmonics: 6, ArcsecPerTick: s.opt.TCS.ArcsecPerTick, NominalPeriod: 150}
	sha, name, err := s.saveUpload(r, "table")
	if err != nil {
		s.problem(w, r, "table", pageData{Nav: "table", Data: base}, err.Error())
		return
	}
	harm := 6
	if v := r.FormValue("harmonics"); v != "" {
		if harm, err = strconv.Atoi(v); err != nil {
			s.problem(w, r, "table", pageData{Nav: "table", Data: base}, "harmonics: not a number")
			return
		}
	}
	cfg := s.opt.TCS
	if v := r.FormValue("arcsec_per_tick"); v != "" {
		if cfg.ArcsecPerTick, err = strconv.ParseFloat(v, 64); err != nil {
			s.problem(w, r, "table", pageData{Nav: "table", Data: base}, "arcsec per tick: not a number")
			return
		}
	}
	tbl, err := tcs.ReadFile(s.filePath(sha))
	if err != nil {
		s.problem(w, r, "table", pageData{Nav: "table", Data: base}, "not a TCS PEC table: "+errors.Unwrap(err).Error())
		return
	}
	res, err := analysis.Table(tbl, cfg, harm)
	if err != nil {
		s.problem(w, r, "table", pageData{Nav: "table", Data: base}, err.Error())
		return
	}
	v := tableView{Harmonics: harm, ArcsecPerTick: cfg.ArcsecPerTick, FileName: name, Res: res, NominalPeriod: 150}
	v.Rows = harmonicRows(res.Curve, 150, cfg)
	s.page(w, r, "table", "table/_result", pageData{Title: "TCS table analysis", Nav: "table", Data: v})
}
