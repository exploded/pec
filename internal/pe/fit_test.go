package pe

import (
	"math"
	"strings"
	"testing"
)

// truth shared by the synthetic cases.
var (
	truthPeriod = 149.6
	truthCurve  = Curve{Harmonics: []Harmonic{
		HarmonicFromPolar(1, 0.68, 30),
		HarmonicFromPolar(2, 0.36, -100),
		HarmonicFromPolar(3, 0.19, 75),
	}}
)

func baseSynth() SynthOptions {
	return SynthOptions{
		Period: truthPeriod, Curve: truthCurve, Offset: 1.2, Drift: 0.30,
		Cadence: 2, Jitter: 0.05, Duration: 900, Noise: 0.02, Seed: 1,
	}
}

func baseOpts() FitOptions {
	o := DefaultFitOptions()
	o.Harmonics = 3
	return o
}

type tol struct {
	ampRel   float64   // relative amplitude tolerance (0 = use ampAbs)
	ampAbs   float64   // absolute amplitude tolerance
	phaseDeg []float64 // per harmonic
	period   float64
	drift    float64
}

func checkHarmonics(t *testing.T, got *Result, tl tol) {
	t.Helper()
	for i, want := range truthCurve.Harmonics {
		g := got.Curve.Harmonics[i]
		lim := tl.ampAbs
		if tl.ampRel > 0 {
			lim = tl.ampRel * want.Amp
		}
		if math.Abs(g.Amp-want.Amp) > lim {
			t.Errorf("k=%d amplitude %.4f, want %.4f (tol %.4f)", want.K, g.Amp, want.Amp, lim)
		}
		if dp := math.Abs(WrapDeg(g.PhaseDeg - want.PhaseDeg)); dp > tl.phaseDeg[i] {
			t.Errorf("k=%d phase %.2f, want %.2f (tol %.1f deg)", want.K, g.PhaseDeg, want.PhaseDeg, tl.phaseDeg[i])
		}
	}
	if tl.period > 0 && math.Abs(got.Period-truthPeriod) > tl.period {
		t.Errorf("period %.3f, want %.3f (tol %.2f)", got.Period, truthPeriod, tl.period)
	}
	if tl.drift > 0 && math.Abs(got.Drift-0.30) > tl.drift {
		t.Errorf("drift %.4f, want 0.30 (tol %.3f)", got.Drift, tl.drift)
	}
}

func hasWarning(r *Result, substr string) bool {
	for _, w := range r.Warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func TestFitCleanScan(t *testing.T) {
	s, br := Synth(baseSynth())
	r, err := Fit(s, br, baseOpts())
	if err != nil {
		t.Fatal(err)
	}
	checkHarmonics(t, r, tol{ampRel: 0.02, phaseDeg: []float64{2, 2, 2}, period: 0.2, drift: 0.01})
	if len(r.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", r.Warnings)
	}
	if r.ResidualRMS < 0.014 || r.ResidualRMS > 0.03 {
		t.Errorf("residual RMS %.4f outside noise band", r.ResidualRMS)
	}
	if r.Cycles < 5.9 || r.Cycles > 6.1 {
		t.Errorf("cycles %.2f", r.Cycles)
	}
}

func TestFitCleanFixed(t *testing.T) {
	s, br := Synth(baseSynth())
	o := baseOpts()
	o.Period = truthPeriod
	r, err := Fit(s, br, o)
	if err != nil {
		t.Fatal(err)
	}
	if !r.PeriodFixed || len(r.Periodogram) != 0 {
		t.Error("period should be fixed with no periodogram")
	}
	checkHarmonics(t, r, tol{ampRel: 0.02, phaseDeg: []float64{2, 2, 2}, drift: 0.01})
}

func TestFitRealisticNoise(t *testing.T) {
	so := baseSynth()
	so.Cadence, so.Duration, so.Noise = 2.5, 1200, 0.35
	s, br := Synth(so)
	r, err := Fit(s, br, baseOpts())
	if err != nil {
		t.Fatal(err)
	}
	checkHarmonics(t, r, tol{ampAbs: 0.09, phaseDeg: []float64{8, 15, 30}, period: 1.5, drift: 0.05})
	sigmaExpect := 0.35 * math.Sqrt(2/float64(r.N))
	if got := r.Curve.Harmonics[0].AmpSigma; got < 0.5*sigmaExpect || got > 1.5*sigmaExpect {
		t.Errorf("AmpSigma %.4f, expected about %.4f", got, sigmaExpect)
	}
	if r.PeriodSigma > 3 {
		t.Errorf("PeriodSigma %.2f s too large", r.PeriodSigma)
	}
}

func TestFitGuidingAssistantLike(t *testing.T) {
	so := baseSynth()
	so.Cadence, so.Duration, so.Noise = 10.5, 390, 0.30
	s, br := Synth(so)
	r, err := Fit(s, br, baseOpts())
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(r, "cycles") {
		t.Errorf("want a short-run warning, got %v", r.Warnings)
	}
	if !hasWarning(r, "poorly determined") {
		t.Errorf("want a period warning, got %v", r.Warnings)
	}
	if math.Abs(r.Period-truthPeriod) > 8 {
		t.Errorf("period %.1f", r.Period)
	}
	if g := r.Curve.Harmonics[0].Amp; math.Abs(g-0.68) > 0.25*0.68 {
		t.Errorf("amp1 %.3f", g)
	}
}

func TestFitDithers(t *testing.T) {
	so := baseSynth()
	so.Cadence, so.Duration = 3, 1200
	so.Jumps = []Jump{{300, 1.5}, {600, -2.0}, {900, 0.8}}
	s, br := Synth(so)
	r, err := Fit(s, br, baseOpts())
	if err != nil {
		t.Fatal(err)
	}
	checkHarmonics(t, r, tol{ampRel: 0.02, phaseDeg: []float64{2, 2, 2}, period: 0.2, drift: 0.01})
	if len(r.Offsets) != 4 {
		t.Fatalf("segments %d, want 4", len(r.Offsets))
	}
	cum := []float64{0, 1.5, -0.5, 0.3}
	for i, c := range cum {
		if d := r.Offsets[i] - r.Offsets[0]; math.Abs(d-c) > 0.02 {
			t.Errorf("offset[%d]-offset[0] = %.3f, want %.3f", i, d, c)
		}
	}
	if r.Dropped < 27 || r.Dropped > 33 {
		t.Errorf("dropped %d, want about 30", r.Dropped)
	}
}

func TestFitQuadraticDrift(t *testing.T) {
	so := baseSynth()
	so.Cadence, so.Duration, so.Curvature = 3, 1500, 0.002
	s, br := Synth(so)
	o := baseOpts()
	o.PolyOrder = 2
	r2, err := Fit(s, br, o)
	if err != nil {
		t.Fatal(err)
	}
	checkHarmonics(t, r2, tol{ampRel: 0.02, phaseDeg: []float64{2, 2, 2}, period: 0.2})
	if math.Abs(r2.Curvature-0.002) > 0.0005 {
		t.Errorf("curvature %.5f, want 0.002", r2.Curvature)
	}
	o.PolyOrder = 1
	r1, err := Fit(s, br, o)
	if err != nil {
		t.Fatal(err)
	}
	e1 := math.Abs(r1.Curve.Harmonics[0].Amp - 0.68)
	e2 := math.Abs(r2.Curve.Harmonics[0].Amp - 0.68)
	if e1 < e2 {
		t.Errorf("order 1 error %.4f should exceed order 2 error %.4f", e1, e2)
	}
}

func TestFitScanEdge(t *testing.T) {
	so := baseSynth()
	so.Period = 139
	s, br := Synth(so)
	r, err := Fit(s, br, baseOpts())
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(r, "edge") {
		t.Errorf("want edge warning, got %v", r.Warnings)
	}
}

func TestFitNyquistClamp(t *testing.T) {
	so := baseSynth()
	so.Cadence = 15
	s, br := Synth(so)
	o := baseOpts()
	o.Harmonics = 6
	o.Period = truthPeriod
	r, err := Fit(s, br, o)
	if err != nil {
		t.Fatal(err)
	}
	if r.HarmonicsUsed != 4 {
		t.Errorf("HarmonicsUsed %d, want 4", r.HarmonicsUsed)
	}
	if !hasWarning(r, "supports at most") {
		t.Errorf("want clamp warning, got %v", r.Warnings)
	}
}

func TestFitNoSignal(t *testing.T) {
	so := baseSynth()
	so.Curve = Curve{}
	so.Noise = 0.35
	s, br := Synth(so)
	r, err := Fit(s, br, baseOpts())
	if err != nil {
		t.Fatal(err)
	}
	h := r.Curve.Harmonics[0]
	if h.Amp > 3*h.AmpSigma {
		t.Errorf("amp1 %.3f exceeds 3 sigma (%.3f) with no signal", h.Amp, h.AmpSigma)
	}
	if !(r.PeriodSigma > 2) {
		t.Errorf("PeriodSigma %.2f should be large with no signal", r.PeriodSigma)
	}
}

func TestSegmentize(t *testing.T) {
	var s []Sample
	for i := 0; i < 100; i++ {
		s = append(s, Sample{T: float64(i), V: 0})
	}
	// Two adjacent breaks (SET LOCK POSITION then DITHER) must not make an empty segment.
	kept, segs, dropped := Segmentize(s, []float64{49.5, 49.5, 50}, SegmentOptions{ExcludeAfter: 5, MinSamples: 3})
	if len(segs) != 2 {
		t.Fatalf("segments %d, want 2: %v", len(segs), segs)
	}
	if segs[0] != (Segment{0, 50}) {
		t.Errorf("seg0 %v", segs[0])
	}
	// The break at 49.5 falls between samples 49 and 50, so samples 50..55
	// (within 5 s of the later break at 50) are excluded.
	if dropped != 6 {
		t.Errorf("dropped %d, want 6", dropped)
	}
	if kept[segs[1].Start].T != 56 {
		t.Errorf("seg1 starts at %v, want 56", kept[segs[1].Start].T)
	}
	// Short trailing segment is dropped.
	_, segs, dropped = Segmentize(s, []float64{97}, SegmentOptions{ExcludeAfter: 0, MinSamples: 5})
	if len(segs) != 1 || dropped != 2 {
		t.Errorf("segs %v dropped %d", segs, dropped)
	}
}

func TestFold(t *testing.T) {
	c := Curve{Harmonics: []Harmonic{HarmonicFromPolar(1, 1, 0)}}
	var s []Sample
	for tt := 0.0; tt < 1000; tt += 0.5 {
		s = append(s, Sample{T: tt, V: c.At(Phase(tt, 100, 0))})
	}
	bins := Fold(s, 100, 0, 10)
	if bins[0].N == 0 || math.Abs(bins[0].Mean-c.At(0.05)) > 0.02 {
		t.Errorf("bin0 %+v", bins[0])
	}
	// Bin 5 spans phase 0.5-0.6; the mean of cos over it is sin(1.2 pi)/(0.2 pi) = -0.936.
	if math.Abs(bins[5].Mean+0.936) > 0.02 {
		t.Errorf("bin5 mean %v, want about -0.936", bins[5].Mean)
	}
}
