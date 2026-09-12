package pe

import (
	"math"
	"math/rand/v2"
)

// Jump is a lock-position step: Delta arcsec added to every sample after T.
type Jump struct {
	T, Delta float64
}

// SynthOptions describes a synthetic guide-log series with known truth,
// used by the round-trip tests and available for demos.
type SynthOptions struct {
	Period    float64
	Curve     Curve   // true harmonics (arcsec, phase per the Harmonic convention, t0 = 0)
	Offset    float64 // arcsec
	Drift     float64 // arcsec/min
	Curvature float64 // arcsec/min^2
	Cadence   float64 // s
	Jitter    float64 // +/- uniform seconds added to each sample time
	Duration  float64 // s
	Noise     float64 // Gaussian sigma, arcsec
	Jumps     []Jump
	Seed      uint64
}

// Synth generates samples and returns the jump times as breaks.
func Synth(o SynthOptions) (samples []Sample, breaks []float64) {
	rng := rand.New(rand.NewPCG(o.Seed, o.Seed^0x9e3779b97f4a7c15))
	for t := 0.0; t <= o.Duration; t += o.Cadence {
		tt := t + (2*rng.Float64()-1)*o.Jitter
		if tt < 0 {
			tt = 0
		}
		min := tt / 60
		v := o.Offset + o.Drift*min + o.Curvature*min*min
		v += o.Curve.At(Phase(tt, o.Period, 0))
		if o.Noise > 0 {
			v += rng.NormFloat64() * o.Noise
		}
		for _, j := range o.Jumps {
			if tt > j.T {
				v += j.Delta
			}
		}
		samples = append(samples, Sample{T: tt, V: v})
	}
	for _, j := range o.Jumps {
		breaks = append(breaks, j.T)
	}
	return samples, breaks
}

// HarmonicFromPolar builds a Harmonic from amplitude and phase in degrees.
func HarmonicFromPolar(k int, amp, phaseDeg float64) Harmonic {
	rad := phaseDeg * math.Pi / 180
	return NewHarmonic(k, amp*math.Cos(rad), amp*math.Sin(rad), 0, 0)
}
