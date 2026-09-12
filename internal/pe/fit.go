package pe

import (
	"errors"
	"fmt"
	"math"
)

// FitOptions controls a periodic-error fit.
type FitOptions struct {
	// Period > 0 fixes the worm period and skips the scan.
	Period float64
	Scan   PeriodScan
	// Harmonics is the requested number of terms (default 6). It is clamped
	// to what the sampling cadence can support.
	Harmonics int
	// PolyOrder is the drift polynomial order: 1 (default) or 2.
	PolyOrder int
	// PhaseOrigin t0, seconds in the samples' time base. Phases in the
	// returned Curve are referenced to it. Fit in index mode sets it so that
	// the curve comes out in PEC-table phase.
	PhaseOrigin float64
	Segments    SegmentOptions
}

// DefaultFitOptions returns the brief's defaults.
func DefaultFitOptions() FitOptions {
	return FitOptions{
		Scan:      DefaultPeriodScan(),
		Harmonics: 6,
		PolyOrder: 1,
		Segments:  DefaultSegmentOptions(),
	}
}

// Result is a fitted periodic-error model.
type Result struct {
	Period, PeriodSigma, PeriodHalfWidth float64
	PeriodFixed                          bool
	Periodogram                          []PeriodogramPoint // empty when the period was fixed

	Curve     Curve // harmonics in arcsec, phase referenced to PhaseOrigin
	PolyOrder int
	Drift     float64   // arcsec/min (mean slope)
	Curvature float64   // arcsec/min^2, 0 for order 1
	Offsets   []float64 // per segment, arcsec
	Segments  []Segment // ranges into Kept
	Kept      []Sample  // samples used, sorted by time
	Dropped   int       // samples excluded by segmentation
	N         int       // len(Kept)
	Span      float64   // seconds from first to last kept sample
	Cycles    float64   // Span / Period
	Cadence   CadenceStats

	Detrended []Sample // Kept minus offsets and drift: what gets folded
	Residuals []Sample // Kept minus the full model

	DetrendedRMS, DetrendedP2P float64 // periodic + noise, before removing the curve
	ModelRMS                   float64 // RMS of the fitted curve: what an ideal PEC removes
	ResidualRMS, ResidualP2P   float64 // what no PEC can fix
	CurveMin, CurveMax         float64 // fitted curve extremes on a 1250-point grid
	PeakToPeak                 float64

	HarmonicsUsed int
	PhaseOrigin   float64
	Warnings      []string
}

// Fit segments the series, finds (or takes) the worm period, and fits the
// joint drift + harmonic model. Errors mean no usable result; Warnings on
// the Result flag a usable but marginal one.
func Fit(s []Sample, breaks []float64, o FitOptions) (*Result, error) {
	if o.Harmonics < 1 {
		return nil, errors.New("fit: harmonics must be at least 1")
	}
	if o.PolyOrder < 1 || o.PolyOrder > 2 {
		return nil, errors.New("fit: polynomial order must be 1 or 2")
	}
	kept, segs, dropped := Segmentize(s, breaks, o.Segments)
	if len(kept) == 0 {
		return nil, errors.New("fit: no samples survive segmentation")
	}
	r := &Result{
		PolyOrder: o.PolyOrder, Segments: segs, Kept: kept, Dropped: dropped,
		N: len(kept), PhaseOrigin: o.PhaseOrigin,
	}
	r.Span = kept[len(kept)-1].T - kept[0].T
	r.Cadence = Cadence(kept, segs)
	if dropped > 0 && float64(dropped) > 0.25*float64(len(s)) {
		r.warnf("%d of %d samples (%.0f%%) excluded around dithers and short segments", dropped, len(s), 100*float64(dropped)/float64(len(s)))
	}

	// Nyquist clamp, using the nominal period (fixed, or the scan midpoint)
	// so the scan itself runs with the harmonic count the final fit will use.
	K := o.Harmonics
	nominal := o.Period
	if nominal <= 0 {
		nominal = (o.Scan.Min + o.Scan.Max) / 2
	}
	K = r.clampHarmonics(K, nominal)

	// Period.
	if o.Period > 0 {
		r.Period = o.Period
		r.PeriodFixed = true
	} else {
		pr, err := Periodogram(kept, segs, o.PolyOrder, K, o.PhaseOrigin, o.Scan)
		if err != nil {
			return nil, fmt.Errorf("fit: %w", err)
		}
		r.Period = pr.Best
		r.PeriodSigma = pr.Sigma
		r.PeriodHalfWidth = pr.HalfWidth
		r.Periodogram = pr.Points
		if pr.AtEdge {
			r.warnf("best period %.1f s is at the edge of the scan range %.0f-%.0f s; widen the range", pr.Best, o.Scan.Min, o.Scan.Max)
		}
		if math.IsInf(pr.Sigma, 0) || pr.Sigma > 2 {
			r.warnf("period is poorly determined (sigma %s); pin the period from a longer run or the watch anchor", fmtSigma(pr.Sigma))
		}
	}
	r.Cycles = r.Span / r.Period
	if r.Cycles < 3 {
		r.warnf("run spans only %.1f worm cycles (want at least 3): period and phases are weakly constrained", r.Cycles)
	}

	// Re-check the clamp at the fitted period (only bites if the scan moved
	// far from the nominal period); warnings were already issued above.
	if r.Cadence.Median > 0 {
		if kmax := int(math.Floor(r.Period / (2 * r.Cadence.Median))); kmax >= 1 && K > kmax {
			r.warnf("fitted period %.1f s supports only %d harmonics at this cadence; reduced from %d", r.Period, kmax, K)
			K = kmax
		}
	}
	r.HarmonicsUsed = K

	d := newDesign(kept, segs, o.PolyOrder, K, r.Period, o.PhaseOrigin)
	if d.m < d.n+5 {
		return nil, fmt.Errorf("fit: %d samples cannot constrain %d parameters", d.m, d.n)
	}
	X, y := d.matrix(kept)
	b, rss, rinv, err := LstSq(X, d.m, d.n, y)
	if err != nil {
		return nil, fmt.Errorf("fit: %w", err)
	}
	sigma2 := rss / float64(d.m-d.n)
	cov := Covariance(rinv, d.n, sigma2)

	r.Curve = d.harmonics(b, cov)
	r.Drift = d.driftPerMin(b)
	r.Curvature = d.curvaturePerMin2(b)
	r.Offsets = append([]float64(nil), b[:len(segs)]...)

	// Degeneracy between drift and the fundamental.
	if o.PolyOrder >= 1 {
		for _, j := range []int{d.colHarm, d.colHarm + 1} {
			if c := math.Abs(corr(cov, d.n, d.colPoly, j)); c > 0.9 {
				r.warnf("drift and the fundamental are nearly degenerate on this run (correlation %.2f); a longer run separates them", c)
				break
			}
		}
	}

	r.Detrended = make([]Sample, 0, len(kept))
	r.Residuals = make([]Sample, 0, len(kept))
	det := make([]float64, 0, len(kept))
	res := make([]float64, 0, len(kept))
	for si, sg := range segs {
		for i := sg.Start; i < sg.End; i++ {
			t := kept[i].T
			trend := d.trend(b, si, t)
			dv := kept[i].V - trend
			rv := dv - r.Curve.At(Phase(t, r.Period, o.PhaseOrigin))
			r.Detrended = append(r.Detrended, Sample{T: t, V: dv})
			r.Residuals = append(r.Residuals, Sample{T: t, V: rv})
			det = append(det, dv)
			res = append(res, rv)
		}
	}
	r.DetrendedRMS = RMS(det)
	_, _, r.DetrendedP2P = PeakToPeak(det)
	r.ResidualRMS = RMS(res)
	_, _, r.ResidualP2P = PeakToPeak(res)
	r.ModelRMS = r.Curve.RMS()
	r.CurveMin, r.CurveMax, r.PeakToPeak = r.Curve.PeakToPeak(1250)
	return r, nil
}

// clampHarmonics limits K to what the cadence can resolve at period p
// (Nyquist: at least two samples per shortest harmonic cycle) and warns when
// the sampling is marginal (fewer than three).
func (r *Result) clampHarmonics(K int, p float64) int {
	if r.Cadence.Median <= 0 {
		return K
	}
	kmax := int(math.Floor(p / (2 * r.Cadence.Median)))
	if kmax < 1 {
		kmax = 1
	}
	if K > kmax {
		r.warnf("cadence %.1f s supports at most %d harmonics at a %.1f s period; reduced from %d", r.Cadence.Median, kmax, p, K)
		return kmax
	}
	if float64(K) > p/(3*r.Cadence.Median) {
		r.warnf("cadence %.1f s is marginal for %d harmonics at %.1f s (fewer than 3 samples per shortest cycle); prefer 2-3 s guide exposures", r.Cadence.Median, K, p)
	}
	return K
}

func (r *Result) warnf(format string, args ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}

func fmtSigma(s float64) string {
	if math.IsInf(s, 0) {
		return "unbounded"
	}
	return fmt.Sprintf("%.1f s", s)
}
