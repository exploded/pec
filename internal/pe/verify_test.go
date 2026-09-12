package pe

import "testing"

// afterWith returns a fit of the truth curve plus a PEC correction curve,
// with the period pinned to the before fit.
func verifyCase(t *testing.T, pec Curve, noise float64) VerifyResult {
	t.Helper()
	so := baseSynth()
	so.Cadence, so.Duration, so.Noise, so.Seed = 2.5, 1200, noise, 7
	sB, brB := Synth(so)
	before, err := Fit(sB, brB, baseOpts())
	if err != nil {
		t.Fatal(err)
	}
	// After: error + correction. Sum the harmonics coefficient-wise.
	sum := Curve{Harmonics: make([]Harmonic, len(truthCurve.Harmonics))}
	for i, h := range truthCurve.Harmonics {
		p, _ := pec.Amp(h.K)
		sum.Harmonics[i] = NewHarmonic(h.K, h.A+p.A, h.B+p.B, 0, 0)
	}
	so.Curve = sum
	so.Seed = 11
	sA, brA := Synth(so)
	o := baseOpts()
	o.Period = before.Period
	after, err := Fit(sA, brA, o)
	if err != nil {
		t.Fatal(err)
	}
	return Verify(before, after, 0)
}

func TestVerifyVerdicts(t *testing.T) {
	cases := []struct {
		name  string
		pec   Curve
		noise float64
		want  Verdict
	}{
		{"helping", truthCurve.Scale(-0.9), 0.05, VerdictHelping},
		{"unchanged", truthCurve.Scale(-0.1), 0.05, VerdictUnchanged},
		{"inverted", truthCurve.Scale(1), 0.05, VerdictInverted},
		{"inverted with seeing noise", truthCurve.Scale(1), 0.35, VerdictInverted},
		{"half cycle", truthCurve.Shift(0.5).Scale(-1), 0.05, VerdictHalfCycle},
		{"helping with seeing noise", truthCurve.Scale(-0.9), 0.35, VerdictHelping},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := verifyCase(t, c.pec, c.noise)
			if v.Verdict != c.want {
				t.Errorf("verdict %v (ratio %.2f ± %.2f, ratio2 %.2f), want %v\n%s", v.Verdict, v.Ratio, v.RatioSigma, v.Ratio2, c.want, v.Detail)
			}
		})
	}
}

func TestVerifyInconclusive(t *testing.T) {
	so := baseSynth()
	so.Curve = Curve{}
	so.Noise = 0.35
	s, br := Synth(so)
	before, err := Fit(s, br, baseOpts())
	if err != nil {
		t.Fatal(err)
	}
	v := Verify(before, before, 0)
	if v.Verdict != VerdictInconclusive {
		t.Errorf("verdict %v, want inconclusive", v.Verdict)
	}
}
