package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/exploded/pec/internal/analysis"
	"github.com/exploded/pec/internal/phd2"
	"github.com/exploded/pec/internal/report"
	"github.com/exploded/pec/internal/store"
	"github.com/exploded/pec/internal/store/db"
	"github.com/exploded/pec/internal/tcs"
)

// --- Anchors ------------------------------------------------------------------

type anchorRow struct {
	db.Anchor
	AtText string // in the UI zone
	Ago    string
}

type anchorView struct {
	Anchors []anchorRow
	Entries int
	Sigma   float64
}

func (s *Server) anchorRows(ctx context.Context) ([]anchorRow, error) {
	rows, err := s.st.Q.ListAnchors(ctx, 100)
	if err != nil {
		return nil, err
	}
	loc := s.loc(ctx)
	out := make([]anchorRow, len(rows))
	for i, a := range rows {
		out[i] = anchorRow{Anchor: a}
		if t, err := time.Parse(time.RFC3339Nano, a.At); err == nil {
			out[i].AtText = t.In(loc).Format("2006-01-02 15:04:05")
			out[i].Ago = ago(time.Since(t))
		}
	}
	return out, nil
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%.1f h ago", d.Hours())
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}

func (s *Server) anchorData(ctx context.Context) (anchorView, error) {
	rows, err := s.anchorRows(ctx)
	return anchorView{Anchors: rows, Entries: s.opt.TCS.Entries, Sigma: analysis.DefaultAnchorSigma}, err
}

func (s *Server) anchorPage(w http.ResponseWriter, r *http.Request) {
	v, err := s.anchorData(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	s.page(w, r, "anchor", "", pageData{Title: "Anchor the PEC index", Nav: "capture", Data: v})
}

// anchorList answers a mutation with the refreshed list fragment, or a
// redirect for a plain form post.
func (s *Server) anchorList(w http.ResponseWriter, r *http.Request) {
	if !isHTMX(r) {
		http.Redirect(w, r, "/anchor", http.StatusSeeOther)
		return
	}
	v, err := s.anchorData(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, http.StatusOK, "anchor", "anchor/_list", pageData{Nav: "capture", Data: v})
}

// anchorCreate stamps the server clock unless a time was typed.
func (s *Server) anchorCreate(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	base := pageData{Nav: "capture"}
	_ = r.ParseForm()
	idx, err := strconv.Atoi(strings.TrimSpace(r.FormValue("index")))
	if err != nil || idx < 0 || idx >= s.opt.TCS.Entries {
		s.problem(w, r, "anchor", base, fmt.Sprintf("the index must be a whole number from 0 to %d", s.opt.TCS.Entries-1))
		return
	}
	a := analysis.Anchor{Index: idx, At: now, SigmaS: analysis.DefaultAnchorSigma}
	if v := r.FormValue("sigma"); v != "" {
		if a.SigmaS, err = strconv.ParseFloat(v, 64); err != nil || a.SigmaS <= 0 {
			s.problem(w, r, "anchor", base, "timing uncertainty must be a positive number of seconds")
			return
		}
	}
	if v := strings.TrimSpace(r.FormValue("at")); v != "" {
		t, err := parseLocal(v, s.loc(r.Context()))
		if err != nil {
			s.problem(w, r, "anchor", base, "the time must look like 2026-09-12T21:03:04 (local time)")
			return
		}
		a.At = t
	}
	if _, err := s.st.SaveAnchor(r.Context(), a, "typed", strings.TrimSpace(r.FormValue("note"))); err != nil {
		s.fail(w, err)
		return
	}
	s.anchorList(w, r)
}

// parseLocal accepts what a datetime-local input produces, with or without
// seconds, in the UI zone.
func parseLocal(v string, loc *time.Location) (time.Time, error) {
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, v, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("bad time")
}

func (s *Server) anchorDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad anchor id", http.StatusNotFound)
		return
	}
	if err := s.st.Q.DeleteAnchor(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	s.anchorList(w, r)
}

// --- Fit ------------------------------------------------------------------------

type fitRow struct {
	db.Fit
	When     string
	ModeText string
}

type fitView struct {
	Runs    []runRow
	Tables  []runRow
	Anchors []anchorRow
	Fits    []fitRow
	Params  analysis.FitParams
	Table   tableView // the "record the mount's current table" card

	Mode                     string
	RunID, TableID, AnchorID int64

	// Preview / saved page.
	Body  template.HTML
	Title string
	Level string
	Fit   fitRow
}

func fitRowIn(loc *time.Location, f db.Fit) fitRow {
	row := fitRow{Fit: f, ModeText: "guide log + anchor"}
	if f.Mode == analysis.ModeTCS {
		row.ModeText = "TCS recording"
	}
	if t, err := time.Parse(time.RFC3339, f.CreatedAt); err == nil {
		row.When = t.In(loc).Format("2006-01-02 15:04")
	}
	return row
}

func (s *Server) fitData(ctx context.Context) (fitView, error) {
	v := fitView{Params: analysis.DefaultFitParams(), Table: tableView{Harmonics: 6, ArcsecPerTick: s.opt.TCS.ArcsecPerTick}}
	loc := s.loc(ctx)
	runs, err := s.st.Q.ListAnalyseRuns(ctx, 200)
	if err != nil {
		return v, err
	}
	for _, r := range runs {
		v.Runs = append(v.Runs, runRowIn(loc, r))
	}
	tables, err := s.st.Q.ListTableRuns(ctx, 200)
	if err != nil {
		return v, err
	}
	for _, r := range tables {
		v.Tables = append(v.Tables, runRowIn(loc, r))
	}
	if v.Anchors, err = s.anchorRows(ctx); err != nil {
		return v, err
	}
	fits, err := s.st.Q.ListFits(ctx, 200)
	if err != nil {
		return v, err
	}
	for _, f := range fits {
		v.Fits = append(v.Fits, fitRowIn(loc, f))
	}
	return v, nil
}

func (s *Server) fitPage(w http.ResponseWriter, r *http.Request) {
	v, err := s.fitData(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	q := r.URL.Query()
	v.RunID, _ = strconv.ParseInt(q.Get("run"), 10, 64)
	v.TableID, _ = strconv.ParseInt(q.Get("table"), 10, 64)
	v.AnchorID, _ = strconv.ParseInt(q.Get("anchor"), 10, 64)
	switch {
	case v.RunID != 0 || v.AnchorID != 0:
		v.Mode = analysis.ModeIndex
	case v.TableID != 0 || len(v.Tables) > 0:
		v.Mode = analysis.ModeTCS
	default:
		v.Mode = analysis.ModeIndex
	}
	if v.AnchorID == 0 && len(v.Anchors) > 0 {
		v.AnchorID = v.Anchors[0].ID
	}
	s.page(w, r, "fit", "", pageData{Title: "Fit a PEC table", Nav: "fit", Data: v})
}

// fitContext is a computed fit plus the provenance the store needs.
type fitContext struct {
	Res                           *analysis.FitResult
	RunID, AnchorID               int64
	FileSHA, SourceName, PhaseRef string
}

// errNoPhaseRef is the refusal the brief requires.
var errNoPhaseRef = errors.New("a phase reference is required: a table the TCS recorded itself, or a guide-log run with an anchor. The tool will not guess the phase.")

// computeFit reads the form and runs the fit without saving anything.
func (s *Server) computeFit(r *http.Request) (*fitContext, error) {
	if err := r.ParseForm(); err != nil {
		return nil, errors.New("bad form")
	}
	p := analysis.DefaultFitParams()
	p.Config = s.opt.TCS
	if v := r.FormValue("harmonics"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, errors.New("harmonics: not a number")
		}
		p.Harmonics = n
	}
	p.Invert = r.FormValue("invert") != ""
	p.ExposureShift = r.FormValue("exposure_shift") != ""
	for _, f := range []struct {
		name string
		dst  *float64
	}{{"period", &p.Period}, {"period_sigma", &p.PeriodSigma}} {
		if v := r.FormValue(f.name); v != "" {
			x, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return nil, fmt.Errorf("%s: not a number", strings.ReplaceAll(f.name, "_", " "))
			}
			*f.dst = x
		}
	}
	ctx := r.Context()
	switch r.FormValue("mode") {
	case analysis.ModeTCS:
		id, _ := strconv.ParseInt(r.FormValue("table"), 10, 64)
		run, err := s.st.Q.GetRun(ctx, id)
		if err != nil || run.Kind != "table" {
			return nil, errors.New("pick a TCS table run (analyse the recording on the TCS table page first)")
		}
		var opt struct {
			ArcsecPerTick float64 `json:"arcsec_per_tick"`
		}
		if jsonInto(run.OptionsJson, &opt) == nil && opt.ArcsecPerTick > 0 {
			p.Config.ArcsecPerTick = opt.ArcsecPerTick
		}
		tbl, err := tcs.ReadFile(s.filePath(run.FileSha256))
		if err != nil {
			return nil, fmt.Errorf("the stored table file is missing or unreadable: %w", err)
		}
		res, err := analysis.FitTable(tbl, p)
		if err != nil {
			return nil, err
		}
		return &fitContext{Res: res, RunID: run.ID, FileSHA: run.FileSha256, SourceName: run.SourceName, PhaseRef: "tcs: " + run.SourceName}, nil

	case analysis.ModeIndex:
		rid, _ := strconv.ParseInt(r.FormValue("run"), 10, 64)
		run, err := s.st.Q.GetRun(ctx, rid)
		if err != nil || run.Kind != "analyse" {
			return nil, errors.New("pick a guide-log run")
		}
		aid, _ := strconv.ParseInt(r.FormValue("anchor"), 10, 64)
		if aid == 0 {
			return nil, errors.New("pick an anchor: a PEC index reading with its time. Add one on the Anchor page while the TCS window shows the index.")
		}
		arow, err := s.st.Q.GetAnchor(ctx, aid)
		if err != nil {
			return nil, errors.New("that anchor no longer exists")
		}
		a, err := store.AnchorFromRow(arow)
		if err != nil {
			return nil, err
		}
		sres, err := s.refitSession(r.Context(), run, nil)
		if err != nil {
			return nil, err
		}
		p.MeasuredWithPEC = store.PecOn(run.PecOn)
		res, err := analysis.FitSession(sres, a, p)
		if err != nil {
			return nil, err
		}
		return &fitContext{
			Res: res, RunID: run.ID, AnchorID: a.ID, FileSHA: run.FileSha256, SourceName: run.SourceName,
			PhaseRef: fmt.Sprintf("index %d at %s", a.Index, a.At.In(s.loc(r.Context())).Format("2006-01-02 15:04:05")),
		}, nil
	}
	return nil, errNoPhaseRef
}

func (s *Server) fitPreview(w http.ResponseWriter, r *http.Request) {
	base := pageData{Nav: "fit"}
	c, err := s.computeFit(r)
	if err != nil {
		s.problem(w, r, "fit", base, err.Error())
		return
	}
	d := report.BuildFit(c.Res, c.SourceName, s.loc(r.Context()))
	body, err := report.Body(d)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, http.StatusOK, "fit", "fit/_preview", pageData{Nav: "fit", Data: fitView{Body: body, Title: d.Title, Level: c.Res.Level}})
}

func (s *Server) fitSave(w http.ResponseWriter, r *http.Request) {
	base := pageData{Nav: "fit"}
	c, err := s.computeFit(r)
	if err != nil {
		s.problem(w, r, "fit", base, err.Error())
		return
	}
	meta := c.Res.Meta(analysis.MetaInfo{Version: s.opt.Version, Generated: time.Now(), RunID: c.RunID, SourceName: c.SourceName, SourceSHA: c.FileSHA})
	id, err := s.st.SaveFit(r.Context(), c.Res, meta, store.FitSaveMeta{
		FileSHA: c.FileSHA, SourceName: c.SourceName, PhaseRef: c.PhaseRef, Version: s.opt.Version,
		Notes: strings.TrimSpace(r.FormValue("notes")), RunID: c.RunID, AnchorID: c.AnchorID,
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, fmt.Sprintf("/fits/%d", id))
}

// --- Saved fits ---------------------------------------------------------------

func (s *Server) loadFit(r *http.Request) (db.Fit, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return db.Fit{}, errors.New("bad fit id")
	}
	f, err := s.st.Q.GetFit(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Fit{}, errors.New("no such fit")
	}
	return f, err
}

// rebuildFit recomputes a saved fit from its source file and stored meta,
// so the page has charts even after the run or anchor has been deleted.
func (s *Server) rebuildFit(ctx context.Context, f db.Fit) (*analysis.FitResult, error) {
	var m analysis.Meta
	if err := jsonInto(f.MetaJson, &m); err != nil {
		return nil, fmt.Errorf("stored meta: %w", err)
	}
	p := analysis.DefaultFitParams()
	p.Config = s.opt.TCS
	if m.ArcsecPerTick > 0 {
		p.Config.ArcsecPerTick = m.ArcsecPerTick
	}
	if m.Entries > 0 {
		p.Config.Entries = m.Entries
	}
	p.Harmonics = int(f.Harmonics)
	p.Invert = f.Inverted != 0
	path := s.filePath(f.FileSha256)
	switch f.Mode {
	case analysis.ModeTCS:
		tbl, err := tcs.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("the stored table file is missing or unreadable: %w", err)
		}
		return analysis.FitTable(tbl, p)
	case analysis.ModeIndex:
		if m.Session == nil || m.PhaseRef.At == nil {
			return nil, errors.New("stored meta lacks the session or anchor")
		}
		l, err := phd2.ParseFile(path, s.loc(ctx))
		if err != nil {
			return nil, fmt.Errorf("the stored log file is missing or unreadable: %w", err)
		}
		sess, err := l.Session(m.Session.Index)
		if err != nil {
			return nil, err
		}
		ap := analysis.DefaultParams()
		if m.Analysis != nil {
			ap = *m.Analysis
		}
		sres, err := analysis.Session(sess, ap)
		if err != nil {
			return nil, err
		}
		a := analysis.Anchor{ID: m.AnchorID, Index: m.PhaseRef.Index, At: *m.PhaseRef.At, SigmaS: m.PhaseRef.SigmaS}
		p.ExposureShift = m.PhaseRef.ExposureShift
		p.Period, p.PeriodSigma = m.Period.Seconds, m.Period.SigmaS
		p.MeasuredWithPEC = m.MeasuredWithPEC
		res, err := analysis.FitSession(sres, a, p)
		if err != nil {
			return nil, err
		}
		res.PeriodSource = m.Period.Source
		return res, nil
	}
	return nil, fmt.Errorf("unknown fit mode %q", f.Mode)
}

func (s *Server) fitViewPage(w http.ResponseWriter, r *http.Request) {
	f, err := s.loadFit(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	v := fitView{Fit: fitRowIn(s.loc(r.Context()), f)}
	res, err := s.rebuildFit(r.Context(), f)
	if err != nil {
		s.render(w, http.StatusUnprocessableEntity, "fit_view", "base", pageData{Title: "Fit", Nav: "fit", Error: err.Error(), Data: v})
		return
	}
	d := report.BuildFit(res, f.SourceName, s.loc(r.Context()))
	body, err := report.Body(d)
	if err != nil {
		s.fail(w, err)
		return
	}
	v.Body, v.Title, v.Level = body, d.Title, res.Level
	s.page(w, r, "fit_view", "", pageData{Title: d.Title, Nav: "fit", Data: v})
}

func (s *Server) fitTableDownload(w http.ResponseWriter, r *http.Request) {
	f, err := s.loadFit(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="pec_table_fit%d.txt"`, f.ID))
	_, _ = w.Write([]byte(f.TableText))
}

func (s *Server) fitMetaDownload(w http.ResponseWriter, r *http.Request) {
	f, err := s.loadFit(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="pec_table_fit%d.meta.json"`, f.ID))
	_, _ = w.Write(prettyJSON(f.MetaJson))
}

func (s *Server) fitDelete(w http.ResponseWriter, r *http.Request) {
	f, err := s.loadFit(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err := s.st.Q.DeleteFit(r.Context(), f.ID); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, "/fit")
}

func (s *Server) fitNotes(w http.ResponseWriter, r *http.Request) {
	f, err := s.loadFit(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	_ = r.ParseForm()
	if err := s.st.Q.UpdateFitNotes(r.Context(), db.UpdateFitNotesParams{Notes: strings.TrimSpace(r.FormValue("notes")), ID: f.ID}); err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("HX-Trigger", `{"showToast": {"msg": "Saved", "type": "success"}}`)
	w.WriteHeader(http.StatusNoContent)
}
