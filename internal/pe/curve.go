package pe

import "math"

// Harmonic is one term of a periodic-error curve, in arcsec.
//
// Phase convention (the only definition in the code base):
//
//	A cos(k theta) + B sin(k theta) = R cos(k theta - phi)
//	R = sqrt(A^2 + B^2),  phi = atan2(B, A) in (-180, 180] degrees
//
// where theta = 2 pi (t - t0)/P for measured data and theta = 2 pi i/N for a
// table of N entries (t0 is index 0). Harmonic k therefore peaks at
// t = t0 + phi P/(360 k), i.e. at fundamental phase phi/(360 k) modulo 1/k.
type Harmonic struct {
	K             int
	A, B          float64 // cosine and sine coefficients, arcsec
	Amp           float64 // R
	PhaseDeg      float64 // phi
	AmpSigma      float64 // sqrt((sigA^2 + sigB^2)/2); 0 when unknown
	PhaseSigmaDeg float64 // AmpSigma/Amp in degrees; 0 when unknown
}

// NewHarmonic fills the derived fields from A, B and their sigmas.
func NewHarmonic(k int, a, b, sigA, sigB float64) Harmonic {
	h := Harmonic{K: k, A: a, B: b}
	h.Amp = math.Hypot(a, b)
	h.PhaseDeg = math.Atan2(b, a) * 180 / math.Pi
	h.AmpSigma = math.Sqrt((sigA*sigA + sigB*sigB) / 2)
	if h.Amp > 0 {
		h.PhaseSigmaDeg = h.AmpSigma / h.Amp * 180 / math.Pi
	}
	return h
}

// Curve is a sum of harmonics of one fundamental cycle. Phase phi in [0, 1)
// spans one worm revolution.
type Curve struct {
	Harmonics []Harmonic
}

// At evaluates the curve at fundamental phase phi.
func (c Curve) At(phi float64) float64 {
	v := 0.0
	for _, h := range c.Harmonics {
		th := 2 * math.Pi * float64(h.K) * phi
		v += h.A*math.Cos(th) + h.B*math.Sin(th)
	}
	return v
}

// Sample evaluates the curve at n evenly spaced phases i/n.
func (c Curve) Sample(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = c.At(float64(i) / float64(n))
	}
	return out
}

// PeakToPeak evaluates the curve on an n-point grid and returns its extremes.
func (c Curve) PeakToPeak(n int) (min, max, p2p float64) {
	return PeakToPeak(c.Sample(n))
}

// RMS is the root-mean-square of the curve over one cycle: sqrt(sum Amp^2/2).
func (c Curve) RMS() float64 {
	s := 0.0
	for _, h := range c.Harmonics {
		s += h.Amp * h.Amp / 2
	}
	return math.Sqrt(s)
}

// Scale multiplies every coefficient by f (use -1 to invert, or
// 1/ArcsecPerTick to convert to encoder ticks). Sigmas scale by |f|.
func (c Curve) Scale(f float64) Curve {
	out := Curve{Harmonics: make([]Harmonic, len(c.Harmonics))}
	for i, h := range c.Harmonics {
		af := math.Abs(f)
		out.Harmonics[i] = NewHarmonic(h.K, h.A*f, h.B*f, h.AmpSigma*af, h.AmpSigma*af)
	}
	return out
}

// Shift returns c' with c'(phi) = c(phi + d).
func (c Curve) Shift(d float64) Curve {
	out := Curve{Harmonics: make([]Harmonic, len(c.Harmonics))}
	for i, h := range c.Harmonics {
		w := 2 * math.Pi * float64(h.K) * d
		a := h.A*math.Cos(w) + h.B*math.Sin(w)
		b := h.B*math.Cos(w) - h.A*math.Sin(w)
		out.Harmonics[i] = NewHarmonic(h.K, a, b, h.AmpSigma, h.AmpSigma)
	}
	return out
}

// Amp returns harmonic k, if present.
func (c Curve) Amp(k int) (Harmonic, bool) {
	for _, h := range c.Harmonics {
		if h.K == k {
			return h, true
		}
	}
	return Harmonic{}, false
}

// Fundamental returns harmonic 1, or a zero Harmonic if the curve is empty.
func (c Curve) Fundamental() Harmonic {
	h, _ := c.Amp(1)
	return h
}

// DFT computes the first `harmonics` Fourier terms of one evenly spaced
// period of values (for example a 1250-entry TCS table):
//
//	A_k = (2/N) sum v_i cos(2 pi k i/N),  B_k = (2/N) sum v_i sin(2 pi k i/N)
//
// On the real mount table this reproduces the brief's reference numbers.
func DFT(values []float64, harmonics int) Curve {
	n := float64(len(values))
	c := Curve{Harmonics: make([]Harmonic, harmonics)}
	for k := 1; k <= harmonics; k++ {
		var a, b float64
		for i, v := range values {
			th := 2 * math.Pi * float64(k) * float64(i) / n
			a += v * math.Cos(th)
			b += v * math.Sin(th)
		}
		c.Harmonics[k-1] = NewHarmonic(k, 2*a/n, 2*b/n, 0, 0)
	}
	return c
}

// RMS is the root-mean-square of values about zero.
func RMS(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	s := 0.0
	for _, v := range values {
		s += v * v
	}
	return math.Sqrt(s / float64(len(values)))
}

// PeakToPeak returns the minimum, maximum and their difference.
func PeakToPeak(values []float64) (min, max, p2p float64) {
	if len(values) == 0 {
		return 0, 0, 0
	}
	min, max = values[0], values[0]
	for _, v := range values[1:] {
		min = math.Min(min, v)
		max = math.Max(max, v)
	}
	return min, max, max - min
}

// WrapDeg maps an angle difference to (-180, 180].
func WrapDeg(d float64) float64 {
	d = math.Mod(d+180, 360)
	if d < 0 {
		d += 360
	}
	return d - 180
}
