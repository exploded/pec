package analysis

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/exploded/pec/internal/pe"
	"github.com/exploded/pec/internal/phd2"
	"github.com/exploded/pec/internal/tcs"
)

const fixtureTable = "../../testdata/PEC_table_TCS_2026-09-12.txt"

func TestFitTableRoundTrip(t *testing.T) {
	tbl, err := tcs.ReadFile(fixtureTable)
	if err != nil {
		t.Fatal(err)
	}
	p := DefaultFitParams()
	r, err := FitTable(tbl, p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode != ModeTCS || len(r.Table.Values) != 1250 || r.Level != "good" {
		t.Fatalf("mode %s entries %d level %s", r.Mode, len(r.Table.Values), r.Level)
	}
	// Written text reads back.
	var buf bytes.Buffer
	if err := tcs.Write(&buf, r.Table); err != nil {
		t.Fatal(err)
	}
	if strings.Count(buf.String(), "\n") != 1250 {
		t.Errorf("wrote %d lines", strings.Count(buf.String(), "\n"))
	}
	back, err := tcs.Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	// The output's harmonics match the input's within 1 % for k = 1..3.
	in := pe.DFT(tbl.Arcsec(p.Config), 3)
	out := pe.DFT(back.Arcsec(p.Config), 3)
	for k := 0; k < 3; k++ {
		hi, ho := in.Harmonics[k], out.Harmonics[k]
		if math.Abs(ho.Amp-hi.Amp) > 0.01*hi.Amp {
			t.Errorf("k=%d amp in %.4f out %.4f", k+1, hi.Amp, ho.Amp)
		}
		if math.Abs(pe.WrapDeg(ho.PhaseDeg-hi.PhaseDeg)) > 1 {
			t.Errorf("k=%d phase in %.2f out %.2f", k+1, hi.PhaseDeg, ho.PhaseDeg)
		}
	}
	if r.QuantRMS < 0.02 || r.QuantRMS > 0.04 {
		t.Errorf("quantisation RMS %.4f, want about 0.032", r.QuantRMS)
	}
	if r.NoiseRemoved <= 0 || r.NoiseRemoved > 0.1 {
		t.Errorf("noise removed %.4f", r.NoiseRemoved)
	}
	if r.Stats.P2P < 15 || r.Stats.P2P > 19 {
		t.Errorf("p2p %d ticks, input 17", r.Stats.P2P)
	}

	// Invert is the exact negation.
	p.Invert = true
	ri, err := FitTable(tbl, p)
	if err != nil {
		t.Fatal(err)
	}
	for i := range r.Table.Values {
		if ri.Table.Values[i] != -r.Table.Values[i] {
			t.Fatalf("index %d: %d vs inverted %d", i, r.Table.Values[i], ri.Table.Values[i])
		}
	}
}

func TestFitIndexSynthetic(t *testing.T) {
	const (
		period = 149.6
		anchT  = 100.0 // anchor instant in the samples' time base
		anchI  = 300   // index showing at that instant
	)
	truth := pe.Curve{Harmonics: []pe.Harmonic{
		pe.HarmonicFromPolar(1, 0.68, 30), pe.HarmonicFromPolar(2, 0.36, -100), pe.HarmonicFromPolar(3, 0.19, 75),
	}}
	samples, breaks := pe.Synth(pe.SynthOptions{
		Period: period, Curve: truth, Offset: 1.2, Drift: 0.3, Cadence: 2, Jitter: 0.05,
		Duration: 900, Noise: 0.02, Seed: 1,
	})
	p := DefaultFitParams()
	p.Harmonics = 3
	in := IndexInput{
		Samples: samples, Breaks: breaks, AnchorOffset: anchT, Index: anchI, AnchorSigma: 2,
		Period: period, PeriodSigma: 0.2, PeriodSource: "pinned", Fit: pe.DefaultFitOptions(),
	}
	r, err := FitIndex(in, p)
	if err != nil {
		t.Fatal(err)
	}
	cfg := p.Config
	// Expected: the mount shows index i at t_i = anchT + (i - anchI) P/N,
	// where the true error is truth(t_i/P); the correction is its negative.
	worst := 0
	for i := range r.Table.Values {
		ti := anchT + float64(i-anchI)*period/float64(cfg.Entries)
		want := int(math.Round(-truth.At(pe.Phase(ti, period, 0)) / cfg.ArcsecPerTick))
		if d := abs(r.Table.Values[i] - want); d > worst {
			worst = d
		}
	}
	if worst > 1 {
		t.Errorf("table differs from -truth by up to %d ticks", worst)
	}
	if r.Stats.P2P < 15 {
		t.Errorf("p2p %d ticks; truth is about 17", r.Stats.P2P)
	}
	// The mapping helper agrees with the anchor.
	if idx := r.IndexOf(anchT); math.Abs(idx-anchI) > 1e-6 {
		t.Errorf("IndexOf(anchor) = %.4f, want %d", idx, anchI)
	}
	// Budget: 2 s of 149.6 s is 4.81 degrees; period term 0.2/149.6 over
	// 800 s is 2.57 degrees.
	b := r.PhaseErr
	if math.Abs(b.AnchorDeg-4.81) > 0.02 || math.Abs(b.FarS-800) > 1 || math.Abs(b.PeriodDeg-2.57) > 0.05 {
		t.Errorf("budget %+v", b)
	}
	if b.TotalDeg < 5.4 || b.TotalDeg > 5.5 || b.ResidualFraction < 0.09 || b.ResidualFraction > 0.1 {
		t.Errorf("total %.2f residual %.3f", b.TotalDeg, b.ResidualFraction)
	}
	if r.Level != "good" {
		t.Errorf("level %s warnings %v", r.Level, r.Warnings)
	}

	// Invert is the exact negation.
	p.Invert = true
	ri, err := FitIndex(in, p)
	if err != nil {
		t.Fatal(err)
	}
	for i := range r.Table.Values {
		if ri.Table.Values[i] != -r.Table.Values[i] {
			t.Fatalf("index %d: %d vs inverted %d", i, r.Table.Values[i], ri.Table.Values[i])
		}
	}

	// Unknown period sigma is flagged; a large one condemns the table.
	in.PeriodSigma = 0
	ru, _ := FitIndex(in, DefaultFitParams())
	if ru.Level != "warn" || !hasWarning(ru.Warnings, "uncertainty is unknown") {
		t.Errorf("unknown sigma: %s %v", ru.Level, ru.Warnings)
	}
	in.PeriodSigma = 3
	rb, _ := FitIndex(in, DefaultFitParams())
	if rb.Level != "bad" || !hasWarning(rb.Warnings, "not worth pasting") {
		t.Errorf("bad sigma: %s %v", rb.Level, rb.Warnings)
	}
}

func TestFitIndexRefusals(t *testing.T) {
	samples, breaks := pe.Synth(pe.SynthOptions{Period: 150, Cadence: 2, Duration: 600, Seed: 2})
	base := IndexInput{Samples: samples, Breaks: breaks, Period: 150, Fit: pe.DefaultFitOptions()}
	cases := []struct {
		name string
		mod  func(*IndexInput, *FitParams)
		want string
	}{
		{"no period", func(in *IndexInput, _ *FitParams) { in.Period = 0 }, "needs a worm period"},
		{"index too big", func(in *IndexInput, _ *FitParams) { in.Index = 1250 }, "outside 0..1249"},
		{"harmonics", func(_ *IndexInput, p *FitParams) { p.Harmonics = 0 }, "harmonics must be"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in, p := base, DefaultFitParams()
			c.mod(&in, &p)
			_, err := FitIndex(in, p)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err %v, want %q", err, c.want)
			}
		})
	}
}

func TestFitSessionAndMeta(t *testing.T) {
	loc, _ := time.LoadLocation("Australia/Melbourne")
	l, err := phd2.ParseFile("../../testdata/guidelog_excerpt.txt", loc)
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := l.Session(2)
	ap := DefaultParams()
	ap.Harmonics = 3
	res, err := Session(sess, ap)
	if err != nil {
		t.Fatal(err)
	}
	a := Anchor{ID: 7, Index: 512, At: sess.Begins.Add(90 * time.Second)}
	p := DefaultFitParams()
	p.Harmonics = 3
	r, err := FitSession(res, a, p)
	if err != nil {
		t.Fatal(err)
	}
	// Exposure shift: 10 s frames put the anchor 5 s later in frame time.
	if math.Abs(r.AnchorOffset-95) > 1e-9 {
		t.Errorf("anchor offset %.3f, want 95", r.AnchorOffset)
	}
	if r.PeriodSource != "run" || r.Period != res.Fit.Period {
		t.Errorf("period %s %.2f", r.PeriodSource, r.Period)
	}
	if !hasWarning(r.Warnings, "not recorded") {
		t.Errorf("expected the PEC-unknown warning: %v", r.Warnings)
	}
	on := true
	p.MeasuredWithPEC = &on
	rp, _ := FitSession(res, a, p)
	if rp.Level != "bad" || !hasWarning(rp.Warnings, "must not replace") {
		t.Errorf("PEC-on run should be flagged bad: %v", rp.Warnings)
	}

	m := r.Meta(MetaInfo{Version: "t", RunID: 3, SourceName: "log.txt", SourceSHA: "abc"})
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var back Meta
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Mode != "index" || back.PhaseRef.Index != 512 || back.AnchorID != 7 || back.RunID != 3 ||
		back.Session == nil || back.Session.Index != 2 || back.PhaseError == nil || len(back.Curve) != 3 ||
		!strings.Contains(back.Sign, "-(measured error)") || back.Period.Source != "run" || back.PhaseRef.At == nil {
		t.Errorf("meta round trip:\n%s", b)
	}
	if !back.PhaseRef.At.Equal(a.At) {
		t.Errorf("anchor time %s vs %s", back.PhaseRef.At, a.At)
	}
}

func hasWarning(ws []string, sub string) bool {
	for _, w := range ws {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
