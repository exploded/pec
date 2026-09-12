// Package report turns analysis results into a presentable page: a
// plain-language verdict, a few numbers, charts drawn as inline SVG, and the
// tables behind them. The same ReportData renders inside the live UI and as
// a self-contained HTML file with the CSS inlined.
package report

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"math"
	"time"

	"github.com/exploded/pec/internal/analysis"
	"github.com/exploded/pec/internal/mks"
	"github.com/exploded/pec/internal/pe"
	"github.com/exploded/pec/internal/tcs"
)

//go:embed report.css
var css string

//go:embed body.tmpl
var bodyTmpl string

//go:embed page.tmpl
var pageTmpl string

var tmpl = template.Must(template.New("report").Parse(bodyTmpl + pageTmpl))

// CSS returns the shared stylesheet (tokens, chart classes, verdict bands).
func CSS() string { return css }

// Kind identifies what the report describes.
type Kind string

const (
	KindAnalyse Kind = "analyse"
	KindTable   Kind = "table"
	KindVerify  Kind = "verify"
	KindFit     Kind = "fit"
	KindCapture Kind = "capture"
)

// Stat is a headline tile or a key/value row.
type Stat struct{ K, V, Small string }

// LegendItem names a series colour. Class is the swatch class for the
// colour slot (html/template strips var() from inline styles).
type LegendItem struct{ Label, Color string }

// Class maps the colour slot to its legend swatch class.
func (l LegendItem) Class() string {
	switch l.Color {
	case colMeasured:
		return "s1"
	case colFitted:
		return "s2"
	case colResidual:
		return "s3"
	}
	return ""
}

// Chart is one card: title, note, legend and the SVG.
type Chart struct {
	Title, Note string
	Legend      []LegendItem
	SVG         template.HTML
}

// HarmonicRow is one line of the harmonic table, pre-formatted.
type HarmonicRow struct {
	K                                int
	Period, Amp, Ticks, Phase, Sigma string
	Before, After, Ratio             string
}

// Verdict is the plain-language band at the top of a report.
type Verdict struct{ Level, Headline, Detail string }

// ReportData is everything a report page needs.
type ReportData struct {
	Kind      Kind
	Title     string
	Subtitle  string
	Generated time.Time
	Version   string

	Verdict       Verdict
	Warnings      []string
	Stats         []Stat
	Charts        []Chart
	Harmonics     []HarmonicRow
	HarmonicsNote string
	Compare       bool // harmonic table shows before/after/ratio columns
	RowsTitle     string
	RowsHead      []string
	Rows          [][]string
	Source        []Stat
	Notes         []string

	CSS template.CSS
}

// Body renders the report body for embedding in a page that already has the
// stylesheet.
func Body(d ReportData) (template.HTML, error) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "report-body", d); err != nil {
		return "", fmt.Errorf("report body: %w", err)
	}
	return template.HTML(buf.String()), nil
}

// Render produces the self-contained HTML file.
func Render(d ReportData) ([]byte, error) {
	d.CSS = template.CSS(css)
	if d.Generated.IsZero() {
		d.Generated = time.Now()
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page", d); err != nil {
		return nil, fmt.Errorf("report page: %w", err)
	}
	return buf.Bytes(), nil
}

const (
	colMeasured = "var(--series-1)"
	colFitted   = "var(--series-2)"
	colResidual = "var(--series-3)"
)

func arc(v float64) string  { return fmt.Sprintf("%.2f″", v) }
func arc3(v float64) string { return fmt.Sprintf("%.3f″", v) }

// BuildAnalyse describes a fitted guide-log session.
func BuildAnalyse(res *analysis.SessionResult, cfg tcs.Config, sourceName string) ReportData {
	f := res.Fit
	s := res.Session
	d := ReportData{
		Kind:     KindAnalyse,
		Title:    fmt.Sprintf("Periodic error · session %d · %s", s.Index, s.Begins.Format("2006-01-02 15:04")),
		Subtitle: sourceName,
		Warnings: res.Warnings,
	}

	// Verdict: a summary sentence, warn-level if the run is weak.
	level := "info"
	if f.Cycles < 3 || math.IsInf(f.PeriodSigma, 0) || (!f.PeriodFixed && f.PeriodSigma > 2) {
		level = "warn"
	}
	periodTxt := fmt.Sprintf("%.1f s", f.Period)
	switch {
	case f.PeriodFixed:
		periodTxt += " (pinned)"
	case math.IsInf(f.PeriodSigma, 0):
		periodTxt += " (uncertainty unbounded)"
	default:
		periodTxt = fmt.Sprintf("%.1f ± %.1f s", f.Period, f.PeriodSigma)
	}
	h1 := f.Curve.Fundamental()
	d.Verdict = Verdict{
		Level:    level,
		Headline: fmt.Sprintf("Periodic error %.2f″ peak-to-peak at %s", f.PeakToPeak, periodTxt),
		Detail: fmt.Sprintf("The fundamental is %.2f″ (%.1f ticks). An ideal correction would take the RMS from %.2f″ down to %.2f″; the rest is seeing and drift that no PEC table can remove. Drift %.2f″/min.",
			h1.Amp, h1.Amp/cfg.ArcsecPerTick, f.DetrendedRMS, f.ResidualRMS, f.Drift),
	}
	if level == "warn" {
		d.Verdict.Detail += " This run is short or coarsely sampled, so treat the period and phases as provisional."
	}

	d.Stats = []Stat{
		{"Worm period", periodTxt, scanNote(res.Params, f)},
		{"Peak-to-peak", arc(f.PeakToPeak), fmt.Sprintf("%.1f ticks · fitted curve", f.PeakToPeak/cfg.ArcsecPerTick)},
		{"Periodic RMS", arc(f.ModelRMS), "what an ideal PEC removes"},
		{"Residual RMS", arc(f.ResidualRMS), fmt.Sprintf("left after the curve · p2p %.2f″", f.ResidualP2P)},
		{"Drift", driftText(f), "polar alignment / refraction"},
		{"Run", fmt.Sprintf("%.1f cycles", f.Cycles), fmt.Sprintf("%d samples, %d segment%s, cadence %.1f s", f.N, len(f.Segments), plural(len(f.Segments)), f.Cadence.Median)},
	}

	d.Charts = append(d.Charts, foldChart(f, "Folded at the worm period", colMeasured, colFitted))
	d.Charts = append(d.Charts, residualChart(f))
	d.Charts = append(d.Charts, harmonicBars([]pe.Curve{f.Curve}, []string{colMeasured}, nil, "Harmonic amplitudes"))
	if len(f.Periodogram) > 0 {
		d.Charts = append(d.Charts, periodogramChart(f))
	}
	d.Harmonics = harmonicRows(f.Curve, f.Period, cfg)
	d.HarmonicsNote = fmt.Sprintf("phase relative to session start; %d fitted", f.HarmonicsUsed)
	d.Rows = [][]string{
		{"Detrended samples (periodic + seeing)", arc(f.DetrendedRMS), arc(f.DetrendedP2P)},
		{"Fitted periodic curve", arc(f.ModelRMS), arc(f.PeakToPeak)},
		{"Residual after ideal PEC", arc(f.ResidualRMS), arc(f.ResidualP2P)},
	}
	d.RowsTitle = "Before and after"
	d.RowsHead = []string{"", "RMS", "Peak-to-peak"}
	d.Source = sessionSource(res, sourceName)
	return d
}

func scanNote(p analysis.Params, f *pe.Result) string {
	if f.PeriodFixed {
		return "period supplied, not fitted"
	}
	if math.IsInf(f.PeriodHalfWidth, 0) {
		return fmt.Sprintf("scan %.0f–%.0f s", p.ScanMin, p.ScanMax)
	}
	return fmt.Sprintf("scan %.0f–%.0f s · half-power width ±%.1f s", p.ScanMin, p.ScanMax, f.PeriodHalfWidth)
}

func driftText(f *pe.Result) string {
	if f.PolyOrder == 2 {
		return fmt.Sprintf("%.2f″/min", f.Drift)
	}
	return fmt.Sprintf("%.2f″/min", f.Drift)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func sessionSource(res *analysis.SessionResult, name string) []Stat {
	s := res.Session
	p := res.Params
	out := []Stat{
		{K: "File", V: name},
		{K: "Session", V: fmt.Sprintf("%d, begins %s", s.Index, s.Begins.Format("2006-01-02 15:04:05 MST"))},
		{K: "Target", V: fmt.Sprintf("RA %.2f h · Dec %.1f° · HA %.2f h · alt %.1f° · pier %s", s.RAHours, s.DecDeg, s.HourAngle, s.AltDeg, s.PierSide)},
		{K: "Capture", V: fmt.Sprintf("%.2f″/px · bin %d · exposure %d ms · %s", s.PixelScale, s.Binning, s.ExposureMS, s.Camera)},
		{K: "Rows used", V: fmt.Sprintf("%d of %d", res.Used, len(s.Samples)) + guidingNote(res)},
		{K: "Options", V: fmt.Sprintf("%d harmonics · drift order %d · exclude %.0f s after dithers · min segment %d · RA sign %+.0f", p.Harmonics, p.PolyOrder, p.ExcludeAfter, p.MinSegment, p.RASign)},
	}
	if len(res.Fit.Offsets) > 1 {
		v := ""
		for i, o := range res.Fit.Offsets {
			if i > 0 {
				v += ", "
			}
			v += fmt.Sprintf("%.2f″", o)
		}
		out = append(out, Stat{K: "Segment offsets", V: v})
	}
	return out
}

func guidingNote(res *analysis.SessionResult) string {
	switch {
	case res.Guiding.IsGA:
		return " · Guiding Assistant run"
	case res.Guiding.Corrections > 0:
		return fmt.Sprintf(" · %d rows with RA pulses", res.Guiding.Corrections)
	}
	return ""
}

func harmonicRows(c pe.Curve, period float64, cfg tcs.Config) []HarmonicRow {
	rows := make([]HarmonicRow, len(c.Harmonics))
	for i, h := range c.Harmonics {
		sigma := "–"
		if h.AmpSigma > 0 {
			sigma = arc3(h.AmpSigma)
		}
		rows[i] = HarmonicRow{
			K: h.K, Period: fmt.Sprintf("%.1f s", period/float64(h.K)),
			Amp: arc3(h.Amp), Ticks: fmt.Sprintf("%.2f", h.Amp/cfg.ArcsecPerTick),
			Phase: fmt.Sprintf("%.1f°", h.PhaseDeg), Sigma: sigma,
		}
	}
	return rows
}

// foldChart draws the detrended samples binned by phase with the fitted
// curve over them.
func foldChart(f *pe.Result, title, colPts, colCurve string) Chart {
	// About four samples per bin, between 12 and 50 bins, so whiskers mean
	// something on a short run and the curve is not hidden on a long one.
	bins := min(50, max(12, len(f.Detrended)/4))
	fold := pe.Fold(f.Detrended, f.Period, f.PhaseOrigin, bins)
	var pnts []XY
	var errs []float64
	for _, b := range fold {
		if b.N == 0 {
			continue
		}
		pnts = append(pnts, XY{b.Phase, b.Mean})
		errs = append(errs, b.SD)
	}
	curve := make([]XY, 0, 201)
	for i := 0; i <= 200; i++ {
		phi := float64(i) / 200
		curve = append(curve, XY{phi, f.Curve.At(phi)})
	}
	svg := RenderXY([]Series{
		{Label: "", Color: colPts, Pts: pnts, ErrY: errs, NoLine: true},
		{Label: "fit", Color: colCurve, Pts: curve},
	}, Axes{
		H: 300, XMin: 0, XMax: 1, XTicks: 4, YTicks: 4, ZeroLine: true,
		YLabel: "RA error, arcsec (drift removed)", XLabel: "worm phase",
		XFmt: func(v float64) string { return fmt.Sprintf("%.2f", v) },
		YFmt: func(v float64) string { return fmt.Sprintf("%.1f", v) },
	})
	return Chart{
		Title:  title,
		Note:   fmt.Sprintf("Bin means of the detrended samples (%d bins, whiskers ±1 SD) with the %d-harmonic fit. Phase 0 is the session start.", bins, len(f.Curve.Harmonics)),
		Legend: []LegendItem{{"measured (bin mean ± SD)", colPts}, {"fitted curve", colCurve}},
		SVG:    template.HTML(svg),
	}
}

func residualChart(f *pe.Result) Chart {
	pts := make([]XY, len(f.Residuals))
	for i, s := range f.Residuals {
		pts[i] = XY{s.T / 60, s.V}
	}
	var vl []VLine
	for i, sg := range f.Segments {
		if i == 0 {
			continue
		}
		vl = append(vl, VLine{X: f.Kept[sg.Start].T / 60, Label: "dither"})
	}
	xmax := 1.0
	if len(pts) > 0 {
		xmax = math.Ceil(pts[len(pts)-1].X)
	}
	// Integer-minute ticks: every minute up to 12, then every 5 or 10.
	xt := int(xmax)
	switch {
	case xmax > 60:
		xmax = math.Ceil(xmax/10) * 10
		xt = int(xmax / 10)
	case xmax > 12:
		xmax = math.Ceil(xmax/5) * 5
		xt = int(xmax / 5)
	}
	svg := RenderXY([]Series{{Label: "", Color: colResidual, Pts: pts, NoLine: true}}, Axes{
		H: 220, XMin: 0, XMax: xmax, XTicks: xt, YTicks: 4, ZeroLine: true, VLines: vl,
		YLabel: "residual after the fitted curve, arcsec", XLabel: "minutes",
		XFmt: func(v float64) string { return fmt.Sprintf("%.0f", v) },
		YFmt: func(v float64) string { return fmt.Sprintf("%.1f", v) },
	})
	return Chart{
		Title: "Residual over time",
		Note:  fmt.Sprintf("What is left after drift, segment offsets and the periodic curve are removed: RMS %.2f″. Structure here is not periodic error.", f.ResidualRMS),
		SVG:   template.HTML(svg),
	}
}

func harmonicBars(curves []pe.Curve, colors []string, labels []string, title string) Chart {
	n := 0
	for _, c := range curves {
		n = max(n, len(c.Harmonics))
	}
	groups := make([]BarGroup, n)
	hasSigma := false
	for k := 1; k <= n; k++ {
		g := BarGroup{Label: fmt.Sprintf("k=%d", k)}
		for _, c := range curves {
			h, ok := c.Amp(k)
			if !ok {
				g.Values = append(g.Values, 0)
				g.Sigma = append(g.Sigma, 0)
				continue
			}
			g.Values = append(g.Values, h.Amp)
			g.Sigma = append(g.Sigma, h.AmpSigma)
			hasSigma = hasSigma || h.AmpSigma > 0
		}
		groups[k-1] = g
	}
	svg := RenderBars(groups, colors, Axes{H: 240, YTicks: 4, YLabel: "amplitude, arcsec",
		YFmt: func(v float64) string { return fmt.Sprintf("%.2f", v) }})
	ch := Chart{Title: title, SVG: template.HTML(svg)}
	if hasSigma {
		ch.Note = "Whiskers are ±1σ from the fit covariance."
	}
	for i, l := range labels {
		if i < len(colors) {
			ch.Legend = append(ch.Legend, LegendItem{l, colors[i]})
		}
	}
	return ch
}

func periodogramChart(f *pe.Result) Chart {
	// Residual RMS of the joint fit at each trial period: the minimum is the
	// chosen period and the width of the dip is how well it is determined.
	pts := make([]XY, len(f.Periodogram))
	ymin, ymax := math.Inf(1), math.Inf(-1)
	for i, p := range f.Periodogram {
		pts[i] = XY{p.Period, math.Sqrt(p.RSS / float64(max(f.N, 1)))}
		ymin = math.Min(ymin, pts[i].Y)
		ymax = math.Max(ymax, pts[i].Y)
	}
	pad := math.Max((ymax-ymin)*0.15, 0.005)
	svg := RenderXY([]Series{{Label: "", Color: colMeasured, Pts: pts, Thin: true}}, Axes{
		H: 200, XMin: pts[0].X, XMax: pts[len(pts)-1].X, YMin: ymin - pad, YMax: ymax + pad, XTicks: 4, YTicks: 2,
		VLines: []VLine{{X: f.Period, Label: fmt.Sprintf("best %.1f s", f.Period)}},
		YLabel: "fit residual RMS, arcsec", XLabel: "trial period, s", NoDirectLabs: true,
		XFmt: func(v float64) string { return fmt.Sprintf("%.0f", v) },
		YFmt: func(v float64) string { return fmt.Sprintf("%.3f", v) },
	})
	note := "Residual RMS of the full fit at each trial period; the best period is the minimum, and a shallow dip means a poorly determined period."
	if !math.IsInf(f.PeriodSigma, 0) {
		note += fmt.Sprintf(" 1σ ±%.1f s.", f.PeriodSigma)
	}
	return Chart{Title: "Period scan", Note: note, SVG: template.HTML(svg)}
}

// BuildTable describes a stored TCS table.
func BuildTable(res *analysis.TableResult, sourceName string, nominalPeriod float64) ReportData {
	st := res.Stats
	cfg := res.Config
	h1 := res.Curve.Fundamental()
	d := ReportData{
		Kind:     KindTable,
		Title:    "PEC table · " + sourceName,
		Subtitle: fmt.Sprintf("%d entries, %.4f″ per tick", st.Entries, cfg.ArcsecPerTick),
	}
	d.Verdict = Verdict{
		Level:    "info",
		Headline: fmt.Sprintf("Stored correction %.2f″ peak-to-peak, RMS %.2f″", float64(st.P2P)*cfg.ArcsecPerTick, st.RMS*cfg.ArcsecPerTick),
		Detail: fmt.Sprintf("The fundamental is %.3f″ (%.2f ticks) at phase %.1f° from index 0; the first %d harmonics carry %.2f″ RMS and only %.3f″ RMS is left over (noise and rounding).",
			h1.Amp, h1.Amp/cfg.ArcsecPerTick, h1.PhaseDeg, res.Harmonics, res.Curve.RMS(), res.ResidRMS),
	}
	d.Stats = []Stat{
		{"Peak-to-peak", fmt.Sprintf("%d ticks", st.P2P), fmt.Sprintf("%.2f″ · min %d / max %d", float64(st.P2P)*cfg.ArcsecPerTick, st.Min, st.Max)},
		{"RMS", fmt.Sprintf("%.2f ticks", st.RMS), fmt.Sprintf("%.2f″ about zero", st.RMS*cfg.ArcsecPerTick)},
		{"Harmonic RMS", arc(res.Curve.RMS()), fmt.Sprintf("first %d harmonics", res.Harmonics)},
		{"Left over", arc3(res.ResidRMS), "table minus its harmonics"},
	}
	n := len(res.Arcsec)
	raw := make([]XY, n)
	model := make([]XY, n)
	for i := range res.Arcsec {
		raw[i] = XY{float64(i), res.Arcsec[i]}
		model[i] = XY{float64(i), res.Model[i]}
	}
	svg := RenderXY([]Series{
		{Label: "table", Color: colMeasured, Pts: raw, Thin: true},
		{Label: "harmonics", Color: colFitted, Pts: model},
	}, Axes{
		H: 300, XMin: 0, XMax: float64(n), XTicks: 5, YTicks: 4, ZeroLine: true, NoDirectLabs: true,
		YLabel: "correction, arcsec", XLabel: "table index",
		XFmt: func(v float64) string { return fmt.Sprintf("%.0f", v) },
		YFmt: func(v float64) string { return fmt.Sprintf("%.1f", v) },
	})
	d.Charts = []Chart{
		{Title: "Stored table and its harmonic reconstruction", Note: "Raw ticks converted to arcsec, with the harmonic sum overlaid.",
			Legend: []LegendItem{{"stored table", colMeasured}, {"harmonic reconstruction", colFitted}}, SVG: template.HTML(svg)},
		harmonicBars([]pe.Curve{res.Curve}, []string{colMeasured}, nil, "Harmonic amplitudes"),
	}
	d.Harmonics = harmonicRows(res.Curve, nominalPeriod, cfg)
	d.HarmonicsNote = fmt.Sprintf("phase relative to index 0; periods assume a %.0f s worm", nominalPeriod)
	d.Source = []Stat{{K: "File", V: sourceName}, {K: "Entries", V: fmt.Sprintf("%d", st.Entries)}, {K: "Arcsec per tick", V: fmt.Sprintf("%.4f", cfg.ArcsecPerTick)}}
	return d
}

// VerifyInput is one side of a comparison.
type VerifyInput struct {
	Label string // e.g. "PEC off · 2026-09-11 21:01"
	Res   *analysis.SessionResult
}

// BuildVerify describes a PEC-off versus PEC-on comparison.
func BuildVerify(v pe.VerifyResult, before, after VerifyInput, cfg tcs.Config) ReportData {
	d := ReportData{
		Kind:     KindVerify,
		Title:    "Verify PEC",
		Subtitle: before.Label + "  →  " + after.Label,
		Verdict:  Verdict{Level: v.Verdict.Level(), Headline: v.Verdict.String(), Detail: v.Detail},
		Warnings: v.Reasons,
	}
	// A guided run has its periodic error suppressed by PHD2 whatever the
	// mount's PEC is doing, so comparing it proves nothing about the table.
	for _, side := range []struct {
		name string
		res  *analysis.SessionResult
	}{{"before", before.Res}, {"after", after.Res}} {
		if side.res.Guiding.Corrections > 0 && !side.res.Guiding.IsGA {
			d.Warnings = append(d.Warnings, fmt.Sprintf("the %s run was guided (%d RA pulses): PHD2 was suppressing the periodic error, so this comparison does not test the PEC table. Use Guiding Assistant runs for both sides.", side.name, side.res.Guiding.Corrections))
			if d.Verdict.Level == "good" {
				d.Verdict.Level = "warn"
			}
		}
	}
	d.Stats = []Stat{
		{"Fundamental before", arc(v.AmpBefore), fmt.Sprintf("%.1f ticks", v.AmpBefore/cfg.ArcsecPerTick)},
		{"Fundamental after", arc(v.AmpAfter), fmt.Sprintf("%.1f ticks", v.AmpAfter/cfg.ArcsecPerTick)},
		{"Ratio after / before", fmt.Sprintf("%.2f ± %.2f", v.Ratio, v.RatioSigma), "below 0.7 helping · above 1.5 inverted or out of phase"},
		{"Periodic RMS", fmt.Sprintf("%.2f″ → %.2f″", v.PeriodicRMSBefore, v.PeriodicRMSAfter), "all harmonics"},
		{"Residual RMS", fmt.Sprintf("%.2f″ → %.2f″", v.ResidualRMSBefore, v.ResidualRMSAfter), "non-periodic part; should not change"},
		{"Period used", fmt.Sprintf("%.1f s", v.PeriodBefore), periodAfterNote(v)},
	}
	d.Charts = []Chart{
		harmonicBars([]pe.Curve{before.Res.Fit.Curve, after.Res.Fit.Curve}, []string{colMeasured, colFitted}, []string{"before (PEC off)", "after (PEC on)"}, "Harmonic amplitudes, before and after"),
		foldChart(before.Res.Fit, "Before: "+before.Label, colMeasured, colMeasured),
		foldChart(after.Res.Fit, "After: "+after.Label, colFitted, colFitted),
	}
	d.Compare = true
	n := max(len(before.Res.Fit.Curve.Harmonics), len(after.Res.Fit.Curve.Harmonics))
	for k := 1; k <= n; k++ {
		hb, _ := before.Res.Fit.Curve.Amp(k)
		ha, _ := after.Res.Fit.Curve.Amp(k)
		ratio := "–"
		if hb.Amp > 0 {
			ratio = fmt.Sprintf("%.2f", ha.Amp/hb.Amp)
		}
		d.Harmonics = append(d.Harmonics, HarmonicRow{
			K: k, Period: fmt.Sprintf("%.1f s", v.PeriodBefore/float64(k)),
			Before: arc3(hb.Amp), After: arc3(ha.Amp), Ratio: ratio,
		})
	}
	d.HarmonicsNote = "after fitted with the period pinned to before"
	d.Source = append(sessionSource(before.Res, "before: "+before.Label), sessionSource(after.Res, "after: "+after.Label)...)
	d.Notes = []string{"Phases are relative to each session's own start and are not comparable between the two runs. The verdict uses amplitudes only."}
	return d
}

func periodAfterNote(v pe.VerifyResult) string {
	if v.PeriodAfterFree > 0 {
		return fmt.Sprintf("after on its own scan: %.1f s", v.PeriodAfterFree)
	}
	return "after fitted at before's period"
}

// BuildFit describes a generated table: what was written, where its phase
// came from, and how far it may be out.
func BuildFit(r *analysis.FitResult, sourceName string, loc *time.Location) ReportData {
	cfg := r.Params.Config
	st := r.Stats
	h1 := r.Correction.Fundamental()
	period := r.Period
	if period <= 0 {
		period = 150
	}
	d := ReportData{Kind: KindFit, Subtitle: sourceName, Warnings: r.Warnings}
	p2pArc := float64(st.P2P) * cfg.ArcsecPerTick

	var detail string
	switch r.Mode {
	case analysis.ModeTCS:
		d.Title = "PEC table from the TCS recording"
		detail = fmt.Sprintf("The recording was smoothed to its first %d harmonics, removing %.3f″ RMS of noise; rounding to whole ticks adds %.3f″ RMS. The phase is the mount's own, so no anchor was needed.",
			r.Params.Harmonics, r.NoiseRemoved, r.QuantRMS)
	default:
		d.Title = "PEC table from a guide-log run"
		b := r.PhaseErr
		detail = fmt.Sprintf("Fitted with the period pinned at %.2f s and the phase set by anchor index %d. The phase-error budget is ±%.1f° (anchor ±%.1f°, period ±%.1f°), which would leave %.0f%% of the fundamental behind. Rounding to whole ticks adds %.3f″ RMS.",
			r.Period, r.Anchor.Index, b.TotalDeg, b.AnchorDeg, b.PeriodDeg, 100*b.ResidualFraction, r.QuantRMS)
	}
	if r.Params.Invert {
		detail += " The table is inverted (negated) as requested."
	}
	head := fmt.Sprintf("Table ready: %d ticks peak-to-peak (%.2f″), fundamental %.2f″", st.P2P, p2pArc, h1.Amp)
	if r.Level == "bad" {
		head = "Do not paste this table"
		detail = "See the warnings. The numbers are shown so they can be checked, but this table should not go into the mount. " + detail
	}
	d.Verdict = Verdict{Level: r.Level, Headline: head, Detail: detail}

	d.Stats = []Stat{
		{"Peak-to-peak", fmt.Sprintf("%d ticks", st.P2P), fmt.Sprintf("%.2f″ · min %d / max %d", p2pArc, st.Min, st.Max)},
		{"Fundamental", arc(h1.Amp), fmt.Sprintf("%.2f ticks · phase %.1f° from index 0", h1.Amp/cfg.ArcsecPerTick, h1.PhaseDeg)},
		{"Quantisation", arc3(r.QuantRMS), "RMS from rounding to whole ticks"},
	}
	if r.Mode == analysis.ModeTCS {
		d.Stats = append(d.Stats, Stat{"Noise removed", arc3(r.NoiseRemoved), fmt.Sprintf("recording minus its %d harmonics", r.Params.Harmonics)})
	} else {
		b := r.PhaseErr
		d.Stats = append(d.Stats,
			Stat{"Phase-error budget", fmt.Sprintf("±%.1f°", b.TotalDeg), fmt.Sprintf("anchor ±%.1f° · period ±%.1f° · leaves %.0f%% of the fundamental", b.AnchorDeg, b.PeriodDeg, 100*b.ResidualFraction)},
			Stat{"Period", fitPeriodText(r), "source: " + r.PeriodSource},
			Stat{"Anchor", fmt.Sprintf("index %d", r.Anchor.Index), anchorText(r, loc)},
		)
	}
	d.Stats = append(d.Stats, Stat{"Entries", fmt.Sprintf("%d", cfg.Entries), fmt.Sprintf("%.4f″ per tick", cfg.ArcsecPerTick)})

	if r.Mode == analysis.ModeIndex {
		d.Charts = append(d.Charts, indexFoldChart(r))
	} else {
		d.Charts = append(d.Charts, recordingChart(r))
	}
	d.Charts = append(d.Charts, tableChart(r),
		harmonicBars([]pe.Curve{r.Correction}, []string{colFitted}, nil, "Harmonic amplitudes of the correction"))
	d.Harmonics = harmonicRows(r.Correction, period, cfg)
	d.HarmonicsNote = "correction curve; phase relative to index 0"
	if r.Period <= 0 {
		d.HarmonicsNote += fmt.Sprintf("; periods assume a %.0f s worm", period)
	}
	d.Source = fitSource(r, sourceName, loc)
	d.Notes = fitNotes(r)
	return d
}

func fitPeriodText(r *analysis.FitResult) string {
	if r.PeriodSigma > 0 {
		return fmt.Sprintf("%.2f ± %.2f s", r.Period, r.PeriodSigma)
	}
	return fmt.Sprintf("%.2f s", r.Period)
}

func anchorText(r *analysis.FitResult, loc *time.Location) string {
	a := r.Anchor
	sigma := a.SigmaS
	if sigma <= 0 {
		sigma = analysis.DefaultAnchorSigma
	}
	if a.At.IsZero() {
		return fmt.Sprintf("±%.0f s · %.0f s into the run", sigma, r.AnchorOffset)
	}
	return fmt.Sprintf("%s ±%.0f s · %.0f s into the run", a.At.In(loc).Format("2006-01-02 15:04:05"), sigma, r.AnchorOffset)
}

// indexFoldChart places the detrended samples at the table index the mount
// was showing, with the fitted error curve.
func indexFoldChart(r *analysis.FitResult) Chart {
	f := r.Fit
	n := float64(r.Params.Config.Entries)
	bins := min(50, max(12, len(f.Detrended)/4))
	fold := pe.Fold(f.Detrended, r.Period, r.PhaseOrigin, bins)
	var pnts []XY
	var errs []float64
	for _, b := range fold {
		if b.N == 0 {
			continue
		}
		pnts = append(pnts, XY{b.Phase * n, b.Mean})
		errs = append(errs, b.SD)
	}
	curve := make([]XY, 0, 201)
	for i := 0; i <= 200; i++ {
		phi := float64(i) / 200
		curve = append(curve, XY{phi * n, r.Error.At(phi)})
	}
	svg := RenderXY([]Series{
		{Label: "", Color: colMeasured, Pts: pnts, ErrY: errs, NoLine: true},
		{Label: "fit", Color: colFitted, Pts: curve},
	}, Axes{
		H: 300, XMin: 0, XMax: n, XTicks: 5, YTicks: 4, ZeroLine: true,
		YLabel: "RA error, arcsec (drift removed)", XLabel: "table index",
		XFmt: func(v float64) string { return fmt.Sprintf("%.0f", v) },
		YFmt: func(v float64) string { return fmt.Sprintf("%.1f", v) },
	})
	return Chart{
		Title:  "Measured error in table phase",
		Note:   fmt.Sprintf("Bin means of the detrended samples (%d bins, whiskers ±1 SD) placed at the PEC index the mount was showing, from the anchor and the pinned period, with the fitted error curve. The written correction is the negative of this curve.", bins),
		Legend: []LegendItem{{"measured (bin mean ± SD)", colMeasured}, {"fitted error", colFitted}},
		SVG:    template.HTML(svg),
	}
}

// recordingChart shows the TCS recording against its smoothed curve.
func recordingChart(r *analysis.FitResult) Chart {
	n := len(r.Raw)
	raw := make([]XY, n)
	model := r.Error.Sample(n)
	sm := make([]XY, n)
	for i := range raw {
		raw[i] = XY{float64(i), r.Raw[i]}
		sm[i] = XY{float64(i), model[i]}
	}
	svg := RenderXY([]Series{
		{Label: "recording", Color: colMeasured, Pts: raw, Thin: true},
		{Label: "smoothed", Color: colFitted, Pts: sm},
	}, Axes{
		H: 300, XMin: 0, XMax: float64(n), XTicks: 5, YTicks: 4, ZeroLine: true, NoDirectLabs: true,
		YLabel: "correction, arcsec", XLabel: "table index",
		XFmt: func(v float64) string { return fmt.Sprintf("%.0f", v) },
		YFmt: func(v float64) string { return fmt.Sprintf("%.1f", v) },
	})
	note := fmt.Sprintf("The TCS recording in arcsec with its %d-harmonic smoothing; %.3f″ RMS of noise is removed.", r.Params.Harmonics, r.NoiseRemoved)
	if r.Params.Invert {
		note += " The written table is the smoothed curve negated."
	}
	return Chart{
		Title:  "Recorded table and its smoothed curve",
		Note:   note,
		Legend: []LegendItem{{"TCS recording", colMeasured}, {"smoothed curve", colFitted}},
		SVG:    template.HTML(svg),
	}
}

// tableChart shows the written table over the unrounded curve.
func tableChart(r *analysis.FitResult) Chart {
	cfg := r.Params.Config
	n := len(r.Table.Values)
	smooth := make([]XY, n)
	ticks := make([]XY, n)
	for i := range smooth {
		smooth[i] = XY{float64(i), r.Values[i]}
		ticks[i] = XY{float64(i), float64(r.Table.Values[i]) * cfg.ArcsecPerTick}
	}
	svg := RenderXY([]Series{
		{Label: "curve", Color: colMeasured, Pts: smooth, Thin: true},
		{Label: "table", Color: colFitted, Pts: ticks},
	}, Axes{
		H: 260, XMin: 0, XMax: float64(n), XTicks: 5, YTicks: 4, ZeroLine: true, NoDirectLabs: true,
		YLabel: "correction, arcsec", XLabel: "table index",
		XFmt: func(v float64) string { return fmt.Sprintf("%.0f", v) },
		YFmt: func(v float64) string { return fmt.Sprintf("%.1f", v) },
	})
	return Chart{
		Title:  "Correction written to the table",
		Note:   fmt.Sprintf("The table as written, in whole ticks of %.4f″, over the unrounded curve. The difference is the %.3f″ RMS quantisation.", cfg.ArcsecPerTick, r.QuantRMS),
		Legend: []LegendItem{{"curve before rounding", colMeasured}, {"written table", colFitted}},
		SVG:    template.HTML(svg),
	}
}

func fitSource(r *analysis.FitResult, name string, loc *time.Location) []Stat {
	out := []Stat{{K: "File", V: name}}
	inv := "not inverted"
	if r.Params.Invert {
		inv = "inverted"
	}
	switch r.Mode {
	case analysis.ModeTCS:
		out = append(out,
			Stat{K: "Mode", V: "TCS recording: the table is the recording smoothed; phase and units are the mount's own"},
			Stat{K: "Options", V: fmt.Sprintf("%d harmonics · %s · %.4f″ per tick", r.Params.Harmonics, inv, r.Params.Config.ArcsecPerTick)},
			Stat{K: "Sign", V: "assumes the TCS records a correction with the same sign as the table it plays back"},
		)
	default:
		out = append(out, Stat{K: "Mode", V: "guide-log run with an anchor: sample times mapped onto the PEC index from the anchor and the pinned period"})
		if r.Session != nil {
			s := r.Session.Session
			out = append(out, Stat{K: "Run", V: fmt.Sprintf("session %d, begins %s · exposure %d ms · %s", s.Index, s.Begins.In(loc).Format("2006-01-02 15:04:05 MST"), s.ExposureMS, guidingWord(r.Session))})
		}
		out = append(out,
			Stat{K: "Anchor", V: fmt.Sprintf("index %d · %s", r.Anchor.Index, anchorText(r, loc))},
			Stat{K: "Period", V: fmt.Sprintf("%s · %s", fitPeriodText(r), r.PeriodSource)},
			Stat{K: "Options", V: fmt.Sprintf("%d harmonics · %s · exposure-midpoint shift %s · %.4f″ per tick", r.Params.Harmonics, inv, onOff(r.Params.ExposureShift), r.Params.Config.ArcsecPerTick)},
		)
		raSign := 1.0
		if r.Session != nil {
			raSign = r.Session.Params.RASign
		}
		out = append(out, Stat{K: "Sign", V: fmt.Sprintf("table = −(measured error); error = %+.0f × PHD2 RARawDistance × pixel scale", raSign)})
	}
	return out
}

func guidingWord(res *analysis.SessionResult) string {
	switch {
	case res.Guiding.IsGA:
		return "Guiding Assistant run"
	case res.Guiding.Corrections > 0:
		return fmt.Sprintf("guided (%d RA pulses)", res.Guiding.Corrections)
	}
	return "no corrections sent"
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// BuildCapture describes a decoded USB capture: the worm period from the
// encoder rate and the PEC index anchor.
func BuildCapture(c *mks.Capture, r *mks.Result, sourceName string, loc *time.Location) ReportData {
	d := ReportData{Kind: KindCapture, Title: "USB capture · " + sourceName,
		Subtitle: fmt.Sprintf("%s to %s", r.Start.In(loc).Format("2006-01-02 15:04:05"), r.End.In(loc).Format("15:04:05")),
		Warnings: r.Warnings}
	ts := func(t time.Time) string { return t.In(loc).Format("15:04:05.0") }
	if r.HasAnchor {
		d.Verdict = Verdict{Level: r.Level,
			Headline: fmt.Sprintf("Anchor: index %d at %s ± %.2f s · worm period %.3f ± %.4f s", r.AnchorIndex, ts(r.AnchorAt), r.AnchorSigma, r.Period, r.PeriodSigma),
			Detail: fmt.Sprintf("The HA encoder advanced at %.3f counts/s over %.0f s of tracking with %.1f counts of scatter, which at %.0f counts per worm turn gives the period. %d PEC index readings sit %.2f steps from encoder/16 with a spread of %.2f steps, so the index is known to a fraction of a step at any instant of the capture.",
				r.EncoderRate, r.TrackTo.Sub(r.TrackFrom).Seconds(), r.EncoderRMS, r.CountsPerTurn, r.IndexReadings, r.IndexOffset, r.IndexSpread),
		}
	} else {
		d.Verdict = Verdict{Level: "bad", Headline: "No anchor from this capture",
			Detail: fmt.Sprintf("The worm period is still measured, %.3f ± %.3f s from the encoder rate, but without PEC index readings the phase is unknown. Keep the TCS window on the Periodic Error Correction tab while capturing.", r.Period, r.PeriodSigma)}
		if r.Period == 0 {
			d.Verdict.Detail = "Nothing usable was decoded; see the warnings."
		}
	}
	teeth := fmt.Sprintf("%.1f teeth at sidereal rate", r.TeethEstimate)
	if math.Abs(r.TeethEstimate-576) > 1.5 {
		teeth += " · 576 expected; ProTrack or a non-sidereal rate shifts it"
	}
	d.Stats = []Stat{
		{"Worm period", fmt.Sprintf("%.3f s", r.Period), fmt.Sprintf("± %.4f s · %s", r.PeriodSigma, teeth)},
		{"Encoder rate", fmt.Sprintf("%.3f /s", r.EncoderRate), fmt.Sprintf("%d readings · scatter %.1f counts · %d outliers", r.EncoderReadings, r.EncoderRMS, r.EncoderOutliers)},
	}
	if r.HasAnchor {
		d.Stats = append(d.Stats,
			Stat{"Anchor", fmt.Sprintf("index %d", r.AnchorIndex), fmt.Sprintf("%s ± %.2f s", r.AnchorAt.In(loc).Format("2006-01-02 15:04:05.00"), r.AnchorSigma)},
			Stat{"Index readings", fmt.Sprintf("%d", r.IndexReadings), fmt.Sprintf("offset %.2f steps · spread %.2f", r.IndexOffset, r.IndexSpread)})
	} else {
		d.Stats = append(d.Stats, Stat{"Index readings", "0", "TCS window not on the PEC tab"})
	}
	track := "never"
	if !r.TrackFrom.IsZero() {
		track = fmt.Sprintf("%s to %s", ts(r.TrackFrom), ts(r.TrackTo))
	}
	d.Stats = append(d.Stats,
		Stat{"Tracking", track, fmt.Sprintf("status changes: %s", statusList(r.StatusChanges))},
		Stat{"Capture", fmt.Sprintf("%.0f s", r.Span), fmt.Sprintf("%d frames · %d junk bytes · %d unpaired", r.Frames, r.Junk, r.Unpaired)})

	// Encoder residual chart with status changes marked.
	var pts []XY
	for _, e := range c.Encoder() {
		if e.At.Before(r.TrackFrom) || e.At.After(r.TrackTo) {
			continue
		}
		pts = append(pts, XY{e.At.Sub(r.Start).Minutes(), float64(e.Value) - r.EncoderAt(e.At)})
	}
	var vl []VLine
	for _, s := range r.StatusChanges {
		// Label only the change into tracking; the others come in a
		// cluster during homing and their labels would overlap.
		label := ""
		if s.Value&mks.StatusTracking != 0 {
			label = "tracking"
		}
		vl = append(vl, VLine{X: s.At.Sub(r.Start).Minutes(), Label: label})
	}
	if len(pts) > 0 {
		xmax := math.Ceil(r.Span / 60)
		svg := RenderXY([]Series{{Label: "", Color: colResidual, Pts: pts, NoLine: true}}, Axes{
			H: 220, XMin: 0, XMax: xmax, XTicks: int(math.Min(xmax, 10)), YTicks: 4, ZeroLine: true, VLines: vl,
			YLabel: "encoder minus fitted line, counts", XLabel: "minutes",
			XFmt: func(v float64) string { return fmt.Sprintf("%.0f", v) },
			YFmt: func(v float64) string { return fmt.Sprintf("%.0f", v) },
		})
		d.Charts = append(d.Charts, Chart{Title: "Encoder against the fitted rate",
			Note: "Each HA encoder reading minus the straight line fitted through them. A steady rate is a flat band a few counts wide; a slope change or a step means the rate changed.",
			SVG:  template.HTML(svg)})
	}
	if r.IndexReadings > 0 {
		var ipts []XY
		n := r.CountsPerTurn / mks.CountsPerIndex
		for _, ix := range c.Index() {
			if ix.At.Before(r.TrackFrom) || ix.At.After(r.TrackTo) {
				continue
			}
			pred := math.Mod(r.EncoderAt(ix.At)/mks.CountsPerIndex, n)
			dev := math.Mod(float64(ix.Value)-pred+1.5*n, n) - n/2
			ipts = append(ipts, XY{ix.At.Sub(r.Start).Minutes(), dev - r.IndexOffset})
		}
		xmax := math.Ceil(r.Span / 60)
		svg := RenderXY([]Series{{Label: "", Color: colMeasured, Pts: ipts, NoLine: true}}, Axes{
			H: 200, XMin: 0, XMax: xmax, XTicks: int(math.Min(xmax, 10)), YMin: -2, YMax: 2, YTicks: 4, ZeroLine: true,
			YLabel: "index reading minus encoder relation, steps", XLabel: "minutes",
			XFmt: func(v float64) string { return fmt.Sprintf("%.0f", v) },
			YFmt: func(v float64) string { return fmt.Sprintf("%.1f", v) },
		})
		d.Charts = append(d.Charts, Chart{Title: "PEC index readings against the encoder",
			Note: fmt.Sprintf("Each index the TCS window polled, minus what the encoder relation (encoder/16 + %.2f, modulo %.0f) predicts. Readings within one step of zero confirm the relation; the anchor's timing error comes from this spread.", r.IndexOffset, n),
			SVG:  template.HTML(svg)})
	}
	d.Source = []Stat{
		{K: "File", V: sourceName},
		{K: "Window", V: fmt.Sprintf("%s to %s (capture PC clock) · bus %d device %d", r.Start.In(loc).Format("2006-01-02 15:04:05 MST"), r.End.In(loc).Format("15:04:05"), c.Bus, c.Device)},
		{K: "Encoder fit", V: fmt.Sprintf("%.4f ± %.4f counts/s · residual RMS %.2f counts · %.0f counts per worm turn (%d per index step)", r.EncoderRate, r.EncoderRateSig, r.EncoderRMS, r.CountsPerTurn, mks.CountsPerIndex)},
	}
	d.Notes = append(r.Notes, "The 16-counts-per-step relation and the tracking status bit were inferred from one capture on 2026-09-12; a capture with the PEC tab showing throughout confirms them. Nothing was sent to the mount: this is a passive USB capture.")
	return d
}

func statusList(ch []mks.Reading) string {
	if len(ch) == 0 {
		return "none seen"
	}
	s := ""
	for i, c := range ch {
		if i > 0 {
			s += " → "
		}
		s += fmt.Sprintf("%d", c.Value)
	}
	return s
}

func fitNotes(r *analysis.FitResult) []string {
	switch r.Mode {
	case analysis.ModeTCS:
		return []string{"Assumes the TCS records a correction with the same sign as the table it plays back. Verify with a PEC-on Guiding Assistant run: if the error doubles, re-fit with invert."}
	}
	return []string{"Assumes tracking ran uninterrupted between the anchor and the run, and that the RA sign is right. Verify with a PEC-on Guiding Assistant run before trusting it: if the error doubles, re-fit with invert; if it is unchanged, the phase is wrong."}
}
