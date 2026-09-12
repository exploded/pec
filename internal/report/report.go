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
	K                                     int
	Period, Amp, Ticks, Phase, Sigma      string
	Before, After, Ratio                  string
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

	Verdict   Verdict
	Warnings  []string
	Stats     []Stat
	Charts    []Chart
	Harmonics []HarmonicRow
	HarmonicsNote string
	Compare   bool // harmonic table shows before/after/ratio columns
	RowsTitle string
	RowsHead  []string
	Rows      [][]string
	Source    []Stat
	Notes     []string

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
		Title: title,
		Note:  fmt.Sprintf("Bin means of the detrended samples (%d bins, whiskers ±1 SD) with the %d-harmonic fit. Phase 0 is the session start.", bins, len(f.Curve.Harmonics)),
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
