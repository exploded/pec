package pe

import (
	"bufio"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestCurvePhaseConvention(t *testing.T) {
	// A single harmonic with phase +90 degrees peaks a quarter cycle in.
	c := Curve{Harmonics: []Harmonic{HarmonicFromPolar(1, 1, 90)}}
	if v := c.At(0.25); math.Abs(v-1) > 1e-12 {
		t.Errorf("At(0.25) = %v, want 1", v)
	}
	if v := c.At(0.75); math.Abs(v+1) > 1e-12 {
		t.Errorf("At(0.75) = %v, want -1", v)
	}
	// Harmonic k with phase phi peaks at phi/(360k).
	c2 := Curve{Harmonics: []Harmonic{HarmonicFromPolar(3, 0.5, 60)}}
	if v := c2.At(60.0 / 360 / 3); math.Abs(v-0.5) > 1e-12 {
		t.Errorf("k=3 peak = %v, want 0.5", v)
	}
	h := NewHarmonic(2, 3, 4, 0, 0)
	if h.Amp != 5 || math.Abs(h.PhaseDeg-53.13010235) > 1e-6 {
		t.Errorf("NewHarmonic(3,4) = amp %v phase %v", h.Amp, h.PhaseDeg)
	}
}

func TestCurveShiftScale(t *testing.T) {
	c := Curve{Harmonics: []Harmonic{
		HarmonicFromPolar(1, 0.68, 30), HarmonicFromPolar(2, 0.36, -100), HarmonicFromPolar(3, 0.19, 75),
	}}
	for _, d := range []float64{0, 0.1, 0.37, -0.25} {
		sh := c.Shift(d)
		for phi := 0.0; phi < 1; phi += 0.05 {
			if math.Abs(sh.At(phi)-c.At(phi+d)) > 1e-12 {
				t.Fatalf("Shift(%v).At(%v) = %v, want %v", d, phi, sh.At(phi), c.At(phi+d))
			}
		}
	}
	inv := c.Scale(-1)
	if math.Abs(inv.At(0.3)+c.At(0.3)) > 1e-12 {
		t.Error("Scale(-1) is not the negation")
	}
	if math.Abs(c.RMS()-math.Sqrt((0.68*0.68+0.36*0.36+0.19*0.19)/2)) > 1e-12 {
		t.Errorf("RMS = %v", c.RMS())
	}
}

func TestDFTSynthetic(t *testing.T) {
	truth := Curve{Harmonics: []Harmonic{HarmonicFromPolar(1, 2, 40), HarmonicFromPolar(2, 0.7, -120)}}
	vals := truth.Sample(1250)
	got := DFT(vals, 4)
	for i, h := range truth.Harmonics {
		g := got.Harmonics[i]
		if math.Abs(g.Amp-h.Amp) > 1e-9 || math.Abs(WrapDeg(g.PhaseDeg-h.PhaseDeg)) > 1e-9 {
			t.Errorf("k=%d: got %v@%v want %v@%v", h.K, g.Amp, g.PhaseDeg, h.Amp, h.PhaseDeg)
		}
	}
	for _, g := range got.Harmonics[2:] {
		if g.Amp > 1e-9 {
			t.Errorf("k=%d: spurious amplitude %v", g.K, g.Amp)
		}
	}
}

// TestDFTReferenceTable reproduces the brief's numbers for the real mount table.
func TestDFTReferenceTable(t *testing.T) {
	f, err := os.Open("../../testdata/PEC_table_TCS_2026-09-12.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var vals []float64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		v, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			t.Fatal(err)
		}
		vals = append(vals, v)
	}
	if len(vals) != 1250 {
		t.Fatalf("read %d values, want 1250", len(vals))
	}
	c := DFT(vals, 6)
	want := []float64{6.067, 3.202, 1.716, 0.160}
	for i, w := range want {
		if got := c.Harmonics[i].Amp; math.Abs(got-w) > 0.002 {
			t.Errorf("k=%d amplitude %.3f ticks, want %.3f", i+1, got, w)
		}
	}
	if got := RMS(vals); math.Abs(got-5.011) > 0.001 {
		t.Errorf("RMS %.3f, want 5.011", got)
	}
	if _, _, p2p := PeakToPeak(vals); p2p != 17 {
		t.Errorf("p2p %v, want 17", p2p)
	}
}
