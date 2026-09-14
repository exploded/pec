package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"time"

	"github.com/exploded/pec/internal/analysis"
	"github.com/exploded/pec/internal/pe"
	"github.com/exploded/pec/internal/phd2"
	"github.com/exploded/pec/internal/report"
	"github.com/exploded/pec/internal/store"
	"github.com/exploded/pec/internal/store/db"
	"github.com/exploded/pec/internal/tcs"
)

// runRow is one line of the runs list.
type runRow struct {
	db.Run
	When    string
	Begins  string
	PecText string
	Kind    string
}

func (s *Server) runRows(ctx context.Context, limit int64) ([]runRow, error) {
	runs, err := s.st.Q.ListRuns(ctx, limit)
	if err != nil {
		return nil, err
	}
	loc := s.loc(ctx)
	rows := make([]runRow, len(runs))
	for i, r := range runs {
		rows[i] = runRowIn(loc, r)
	}
	return rows, nil
}

// runRowIn formats a run for the UI zone.
func runRowIn(loc *time.Location, r db.Run) runRow {
	row := runRow{Run: r, Kind: r.Kind, PecText: "?"}
	if t, err := time.Parse(time.RFC3339, r.CreatedAt); err == nil {
		row.When = t.In(loc).Format("2006-01-02 15:04")
	}
	if t, err := time.Parse(time.RFC3339, r.SessionBegins); err == nil {
		row.Begins = t.In(loc).Format("2006-01-02 15:04")
	}
	if v := store.PecOn(r.PecOn); v != nil {
		if *v {
			row.PecText = "on"
		} else {
			row.PecText = "off"
		}
	}
	return row
}

// --- Run detail -------------------------------------------------------------

type runView struct {
	Run     runRow
	Body    template.HTML
	Notes   string
	PecOn   string // "on" | "off" | ""
	Cfg     tcs.Config
	IsTable bool
}

func (s *Server) loadRun(r *http.Request) (db.Run, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return db.Run{}, errors.New("bad run id")
	}
	run, err := s.st.Q.GetRun(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Run{}, errors.New("no such run")
	}
	return run, err
}

// refitSession re-runs the stored analysis from the stored file so charts
// and comparisons come from the same samples. override tweaks the stored
// parameters (Verify pins the period).
func (s *Server) refitSession(ctx context.Context, run db.Run, override func(*analysis.Params)) (*analysis.SessionResult, error) {
	if run.Kind != "analyse" {
		return nil, errors.New("not a guide-log run")
	}
	p, err := store.ParseParams(run.OptionsJson)
	if err != nil {
		return nil, err
	}
	if override != nil {
		override(&p)
	}
	l, err := phd2.ParseFile(s.filePath(run.FileSha256), s.loc(ctx))
	if err != nil {
		return nil, fmt.Errorf("the stored log file is missing or unreadable: %w", err)
	}
	sess, err := l.Session(int(run.SessionIndex))
	if err != nil {
		return nil, err
	}
	return analysis.Session(sess, p)
}

func (s *Server) buildRun(ctx context.Context, run db.Run) (report.ReportData, error) {
	switch run.Kind {
	case "analyse":
		res, err := s.refitSession(ctx, run, nil)
		if err != nil {
			return report.ReportData{}, err
		}
		return report.BuildAnalyse(res, s.opt.TCS, run.SourceName, store.PecOn(run.PecOn)), nil
	case "table":
		tbl, err := tcs.ReadFile(s.filePath(run.FileSha256))
		if err != nil {
			return report.ReportData{}, fmt.Errorf("the stored table file is missing or unreadable: %w", err)
		}
		var opt struct {
			Harmonics     int     `json:"harmonics"`
			ArcsecPerTick float64 `json:"arcsec_per_tick"`
		}
		opt.Harmonics, opt.ArcsecPerTick = 6, s.opt.TCS.ArcsecPerTick
		_ = jsonInto(run.OptionsJson, &opt)
		cfg := tcs.Config{ArcsecPerTick: opt.ArcsecPerTick, Entries: len(tbl.Values)}
		res, err := analysis.Table(tbl, cfg, opt.Harmonics)
		if err != nil {
			return report.ReportData{}, err
		}
		return report.BuildTable(res, run.SourceName, 150), nil
	}
	return report.ReportData{}, fmt.Errorf("unknown run kind %q", run.Kind)
}

func (s *Server) runPage(w http.ResponseWriter, r *http.Request) {
	run, err := s.loadRun(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	loc := s.loc(r.Context())
	d, err := s.buildRun(r.Context(), run)
	if err != nil {
		s.render(w, http.StatusUnprocessableEntity, "run", "base", pageData{Title: "Run", Nav: "analyse", Error: err.Error(), Data: runView{Run: runRowIn(loc, run)}})
		return
	}
	body, err := report.Body(d)
	if err != nil {
		s.fail(w, err)
		return
	}
	v := runView{Run: runRowIn(loc, run), Body: body, Notes: run.Notes, Cfg: s.opt.TCS, IsTable: run.Kind == "table"}
	if p := store.PecOn(run.PecOn); p != nil {
		if *p {
			v.PecOn = "on"
		} else {
			v.PecOn = "off"
		}
	}
	s.page(w, r, "run", "", pageData{Title: d.Title, Nav: "analyse", Data: v})
}

func (s *Server) runReport(w http.ResponseWriter, r *http.Request) {
	run, err := s.loadRun(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	d, err := s.buildRun(r.Context(), run)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	d.Version = s.opt.Version
	d.Generated = time.Now().In(s.loc(r.Context()))
	out, err := report.Render(d)
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="pec-run-%d.html"`, run.ID))
	_, _ = w.Write(out)
}

func (s *Server) runDelete(w http.ResponseWriter, r *http.Request) {
	run, err := s.loadRun(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err := s.st.Q.DeleteRun(r.Context(), run.ID); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, "/analyse")
}

func (s *Server) runNotes(w http.ResponseWriter, r *http.Request) {
	run, err := s.loadRun(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	_ = r.ParseForm()
	pec := sql.NullInt64{}
	if b := parsePecOn(r.FormValue("pec_on")); b != nil {
		pec = sql.NullInt64{Int64: map[bool]int64{true: 1, false: 0}[*b], Valid: true}
	}
	if err := s.st.Q.UpdateRunNotes(r.Context(), db.UpdateRunNotesParams{Notes: r.FormValue("notes"), PecOn: pec, ID: run.ID}); err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("HX-Trigger", `{"showToast": {"msg": "Saved", "type": "success"}}`)
	w.WriteHeader(http.StatusNoContent)
}

// --- Verify -----------------------------------------------------------------

type verifyView struct {
	Runs   []runRow
	Before int64
	After  int64
	Body   template.HTML
	Title  string
}

func (s *Server) verifyPage(w http.ResponseWriter, r *http.Request) {
	runs, err := s.st.Q.ListAnalyseRuns(r.Context(), 200)
	if err != nil {
		s.fail(w, err)
		return
	}
	v := verifyView{}
	loc := s.loc(r.Context())
	for _, run := range runs {
		v.Runs = append(v.Runs, runRowIn(loc, run))
	}
	v.Before, _ = strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	v.After, _ = strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	s.page(w, r, "verify", "", pageData{Title: "Verify PEC", Nav: "verify", Data: v})
}

func (s *Server) verifyRun(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	bid, _ := strconv.ParseInt(r.FormValue("before"), 10, 64)
	aid, _ := strconv.ParseInt(r.FormValue("after"), 10, 64)
	base := pageData{Nav: "verify"}
	if bid == 0 || aid == 0 {
		s.problem(w, r, "verify", base, "pick a before run and an after run")
		return
	}
	if bid == aid {
		s.problem(w, r, "verify", base, "before and after are the same run; pick two different runs")
		return
	}
	rb, err := s.st.Q.GetRun(r.Context(), bid)
	if err != nil {
		s.problem(w, r, "verify", base, "before run not found")
		return
	}
	ra, err := s.st.Q.GetRun(r.Context(), aid)
	if err != nil {
		s.problem(w, r, "verify", base, "after run not found")
		return
	}
	before, err := s.refitSession(r.Context(), rb, nil)
	if err != nil {
		s.problem(w, r, "verify", base, "before: "+err.Error())
		return
	}
	afterFree, err := s.refitSession(r.Context(), ra, nil)
	if err != nil {
		s.problem(w, r, "verify", base, "after: "+err.Error())
		return
	}
	after, err := s.refitSession(r.Context(), ra, func(p *analysis.Params) { p.Period = before.Fit.Period })
	if err != nil {
		s.problem(w, r, "verify", base, "after: "+err.Error())
		return
	}
	free := 0.0
	if !afterFree.Fit.PeriodFixed {
		free = afterFree.Fit.Period
	}
	v := pe.Verify(before.Fit, after.Fit, free)
	loc := s.loc(r.Context())
	d := report.BuildVerify(v,
		report.VerifyInput{Label: runLabel(runRowIn(loc, rb)), Res: before},
		report.VerifyInput{Label: runLabel(runRowIn(loc, ra)), Res: after}, s.opt.TCS)
	body, err := report.Body(d)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.page(w, r, "verify", "verify/_result", pageData{Title: "Verify PEC", Nav: "verify",
		Data: verifyView{Body: body, Title: d.Subtitle, Before: bid, After: aid}})
}

func runLabel(r runRow) string {
	return fmt.Sprintf("run %d · %s session %d · PEC %s", r.ID, r.Begins, r.SessionIndex, r.PecText)
}
