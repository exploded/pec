package report

import (
	"strings"
	"testing"
	"time"

	"github.com/exploded/pec/internal/analysis"
	"github.com/exploded/pec/internal/pe"
	"github.com/exploded/pec/internal/phd2"
	"github.com/exploded/pec/internal/tcs"
)

func fixtureSession(t *testing.T, n int) *analysis.SessionResult {
	t.Helper()
	loc, _ := time.LoadLocation("Australia/Melbourne")
	l, err := phd2.ParseFile("../../testdata/guidelog_excerpt.txt", loc)
	if err != nil {
		t.Fatal(err)
	}
	s, err := l.Session(n)
	if err != nil {
		t.Fatal(err)
	}
	p := analysis.DefaultParams()
	p.Harmonics = 3
	res, err := analysis.Session(s, p)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestAnalyseReport(t *testing.T) {
	res := fixtureSession(t, 2)
	d := BuildAnalyse(res, tcs.DefaultConfig(), "log.txt")
	d.Version = "test"
	html, err := Render(d)
	if err != nil {
		t.Fatal(err)
	}
	out := string(html)
	for _, want := range []string{"<svg", "Periodic error", "verdict warn", "Folded at the worm period", "Residual over time", "Period scan", "--series-1", `data-theme="dark"`, "prefers-color-scheme: dark"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	if strings.Contains(out, "<script") || strings.Contains(out, "http://") || strings.Contains(out, "https://") {
		t.Error("report must be self-contained: no scripts or external references")
	}
	if len(d.Charts) != 4 {
		t.Errorf("charts %d, want 4", len(d.Charts))
	}
	// Dithered session gets dither markers on the residual chart.
	res4 := fixtureSession(t, 4)
	d4 := BuildAnalyse(res4, tcs.DefaultConfig(), "log.txt")
	if !strings.Contains(string(d4.Charts[1].SVG), "dither") {
		t.Error("residual chart should mark dithers")
	}
}

func TestTableReport(t *testing.T) {
	tbl, err := tcs.ReadFile("../../testdata/PEC_table_TCS_2026-09-12.txt")
	if err != nil {
		t.Fatal(err)
	}
	res, err := analysis.Table(tbl, tcs.DefaultConfig(), 6)
	if err != nil {
		t.Fatal(err)
	}
	d := BuildTable(res, "table.txt", 150)
	html, err := Render(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "17 ticks") || !strings.Contains(string(html), "0.683″") {
		t.Error("table report lacks the reference numbers")
	}
}

func TestVerifyReport(t *testing.T) {
	before := fixtureSession(t, 2)
	after := fixtureSession(t, 2)
	v := pe.Verify(before.Fit, after.Fit, 0)
	d := BuildVerify(v, VerifyInput{"PEC off", before}, VerifyInput{"PEC on", after}, tcs.DefaultConfig())
	html, err := Render(d)
	if err != nil {
		t.Fatal(err)
	}
	out := string(html)
	if !strings.Contains(out, v.Verdict.String()) || !strings.Contains(out, "verdict "+v.Verdict.Level()) {
		t.Errorf("verify report lacks the verdict band: %s", v.Verdict)
	}
	if body, err := Body(d); err != nil || !strings.Contains(string(body), "Harmonic amplitudes, before and after") {
		t.Errorf("Body: %v", err)
	}
}

func TestSVGBasics(t *testing.T) {
	svg := RenderXY([]Series{{Label: "a", Color: "var(--series-1)", Pts: []XY{{0, -1}, {1, 1}}}}, Axes{XMin: 0, XMax: 1, ZeroLine: true})
	if !strings.HasPrefix(svg, "<svg") || !strings.HasSuffix(svg, "</svg>") || !strings.Contains(svg, "<path") {
		t.Errorf("bad svg: %s", svg)
	}
	bars := RenderBars([]BarGroup{{Label: "k=1", Values: []float64{0.5, 0.2}, Sigma: []float64{0.05, 0.05}}}, []string{"a", "b"}, Axes{})
	if strings.Count(bars, "<rect") != 2 {
		t.Errorf("bars: %s", bars)
	}
	if niceCeil(0.73) != 0.8 || niceCeil(2.1) != 2.5 || niceCeil(11) != 15 {
		t.Errorf("niceCeil %v %v %v", niceCeil(0.73), niceCeil(2.1), niceCeil(11))
	}
}
