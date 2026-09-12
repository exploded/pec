package pe

import "math"

// Phase returns the fundamental phase of time t in [0, 1) for the given
// period and origin.
func Phase(t, period, t0 float64) float64 {
	p := (t - t0) / period
	p -= math.Floor(p)
	return p
}

// Bin is one phase bin of a folded series. Phase is the bin centre.
type Bin struct {
	Phase, Mean, SD float64
	N               int
}

// Fold bins samples by fundamental phase. It is for display only; the fit
// itself uses the unbinned samples.
func Fold(s []Sample, period, t0 float64, bins int) []Bin {
	out := make([]Bin, bins)
	sum := make([]float64, bins)
	sum2 := make([]float64, bins)
	for i := range out {
		out[i].Phase = (float64(i) + 0.5) / float64(bins)
	}
	for _, smp := range s {
		i := int(Phase(smp.T, period, t0) * float64(bins))
		if i >= bins {
			i = bins - 1
		}
		out[i].N++
		sum[i] += smp.V
		sum2[i] += smp.V * smp.V
	}
	for i := range out {
		if out[i].N == 0 {
			continue
		}
		n := float64(out[i].N)
		out[i].Mean = sum[i] / n
		if out[i].N > 1 {
			v := (sum2[i] - sum[i]*sum[i]/n) / (n - 1)
			out[i].SD = math.Sqrt(math.Max(v, 0))
		}
	}
	return out
}
