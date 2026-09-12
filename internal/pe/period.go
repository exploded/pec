package pe

import (
	"errors"
	"fmt"
	"math"
)

// PeriodScan is the trial-period grid for the periodogram.
type PeriodScan struct {
	Min, Max, Step float64
}

// DefaultPeriodScan is the brief's 140-160 s range at 0.1 s steps.
func DefaultPeriodScan() PeriodScan { return PeriodScan{Min: 140, Max: 160, Step: 0.1} }

// PeriodogramPoint is one trial period. Power is the first-harmonic power
// A1^2 + B1^2 in arcsec^2 from the joint fit at that period; RSS is the
// residual sum of squares of that fit, which is the selection criterion.
type PeriodogramPoint struct {
	Period, Power, RSS float64
}

// PeriodResult is the outcome of a scan.
type PeriodResult struct {
	Best      float64 // parabola-refined minimum-RSS period, s
	Sigma     float64 // 1-sigma uncertainty, s (+Inf when the minimum is not curved)
	HalfWidth float64 // half-width at which power falls to half its peak, s
	AtEdge    bool    // the best grid point is the first or last
	Points    []PeriodogramPoint
}

// Periodogram scans trial periods, solving the joint model (segment offsets,
// polynomial of order poly, K harmonics) at each, and picks the period that
// minimises the residual sum of squares. It is a least-squares periodogram
// with nuisance parameters, which is what Lomb-Scargle approximates without
// the offsets and drift. The scan uses the full harmonic count rather than
// the fundamental alone: otherwise the unmodelled higher harmonics dominate
// the residual and bias the minimum.
func Periodogram(s []Sample, segs []Segment, poly, K int, t0 float64, scan PeriodScan) (PeriodResult, error) {
	if scan.Step <= 0 || scan.Max <= scan.Min {
		return PeriodResult{}, fmt.Errorf("period scan: bad range %v", scan)
	}
	if K < 1 {
		K = 1
	}
	steps := int(math.Round((scan.Max-scan.Min)/scan.Step)) + 1
	res := PeriodResult{Points: make([]PeriodogramPoint, 0, steps)}
	best := -1
	for i := 0; i < steps; i++ {
		p := scan.Min + float64(i)*scan.Step
		d := newDesign(s, segs, poly, K, p, t0)
		X, y := d.matrix(s)
		b, rss, _, err := LstSq(X, d.m, d.n, y)
		if err != nil {
			return PeriodResult{}, fmt.Errorf("period %.2f s: %w", p, err)
		}
		h := d.harmonics(b, nil)
		pt := PeriodogramPoint{Period: p, Power: h.Harmonics[0].Amp * h.Harmonics[0].Amp, RSS: rss}
		res.Points = append(res.Points, pt)
		if best < 0 || rss < res.Points[best].RSS {
			best = i
		}
	}
	if best < 0 {
		return PeriodResult{}, errors.New("period scan: no trial periods")
	}
	bp := res.Points[best]
	res.Best = bp.Period
	res.Sigma = math.Inf(1)
	res.HalfWidth = math.Inf(1)
	if best == 0 || best == len(res.Points)-1 {
		res.AtEdge = true
		return res, nil
	}

	// Refine with a parabola through the three points around the minimum.
	h := scan.Step
	rm, r0, rp := res.Points[best-1].RSS, bp.RSS, res.Points[best+1].RSS
	curv := (rp - 2*r0 + rm) / (h * h) // d2 RSS / dP2
	if curv > 0 {
		res.Best = bp.Period - (h/2)*(rp-rm)/(rp-2*r0+rm)
		d := newDesign(s, segs, poly, K, res.Best, t0)
		dof := float64(d.m - d.n)
		if dof > 0 {
			sigma2 := r0 / dof
			// Profile chi-square rises by 1 at one sigma: (curv/sigma2) dP^2 / 2 = 1.
			res.Sigma = math.Sqrt(2 * sigma2 / curv)
		}
	}
	wm, w0, wp := res.Points[best-1].Power, bp.Power, res.Points[best+1].Power
	wcurv := (wp - 2*w0 + wm) / (h * h)
	if wcurv < 0 && w0 > 0 {
		res.HalfWidth = math.Sqrt(w0 / -wcurv)
	}
	return res, nil
}
