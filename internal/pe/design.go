package pe

import "math"

// design describes the joint least-squares model. This is the only place the
// column layout is defined; everything else indexes through it.
//
// For samples i with segment s(i), polynomial order p in {1,2}, K harmonics,
// period P and phase origin t0:
//
//	tau_i   = (t_i - tMid)/tHalf                 centred and scaled to [-1, 1]
//	theta_i = 2 pi (t_i - t0) / P
//	X = [ 1{s(i)=1} ... 1{s(i)=S} | tau_i (tau_i^2) | cos theta_i sin theta_i ... cos K theta_i sin K theta_i ]
//	b = [ c_1 ... c_S | d_1 (d_2) | A_1 B_1 ... A_K B_K ]
//
// There is no global intercept: the per-segment constants absorb it together
// with the lock-position jumps. The polynomial is centred so it cannot leak
// into the harmonics through poor conditioning; the harmonic columns use the
// un-centred time so phases are referenced to t0.
type design struct {
	segs        []Segment
	poly, K     int
	period, t0  float64
	tMid, tHalf float64
	m, n        int
	colPoly     int // first polynomial column
	colHarm     int // first harmonic column (cos of k=1)
}

func newDesign(s []Sample, segs []Segment, poly, K int, period, t0 float64) design {
	tMin, tMax := math.Inf(1), math.Inf(-1)
	m := 0
	for _, sg := range segs {
		for i := sg.Start; i < sg.End; i++ {
			tMin = math.Min(tMin, s[i].T)
			tMax = math.Max(tMax, s[i].T)
			m++
		}
	}
	tHalf := (tMax - tMin) / 2
	if tHalf <= 0 || math.IsInf(tHalf, 0) {
		tHalf = 1
	}
	d := design{
		segs: segs, poly: poly, K: K, period: period, t0: t0,
		tMid: (tMin + tMax) / 2, tHalf: tHalf, m: m,
	}
	d.colPoly = len(segs)
	d.colHarm = d.colPoly + poly
	d.n = d.colHarm + 2*K
	return d
}

// matrix builds X (row-major m x n) and the observation vector.
func (d design) matrix(s []Sample) (X, y []float64) {
	X = make([]float64, d.m*d.n)
	y = make([]float64, d.m)
	row := 0
	for si, sg := range d.segs {
		for i := sg.Start; i < sg.End; i++ {
			d.fillRow(X[row*d.n:(row+1)*d.n], si, s[i].T)
			y[row] = s[i].V
			row++
		}
	}
	return X, y
}

func (d design) fillRow(r []float64, seg int, t float64) {
	r[seg] = 1
	tau := (t - d.tMid) / d.tHalf
	if d.poly >= 1 {
		r[d.colPoly] = tau
	}
	if d.poly >= 2 {
		r[d.colPoly+1] = tau * tau
	}
	theta := 2 * math.Pi * (t - d.t0) / d.period
	for k := 1; k <= d.K; k++ {
		r[d.colHarm+2*(k-1)] = math.Cos(float64(k) * theta)
		r[d.colHarm+2*(k-1)+1] = math.Sin(float64(k) * theta)
	}
}

// trend evaluates the segment offset plus polynomial part of the model.
func (d design) trend(b []float64, seg int, t float64) float64 {
	tau := (t - d.tMid) / d.tHalf
	v := b[seg]
	if d.poly >= 1 {
		v += b[d.colPoly] * tau
	}
	if d.poly >= 2 {
		v += b[d.colPoly+1] * tau * tau
	}
	return v
}

// driftPerMin converts the tau-scaled slope to arcsec/min at the run midpoint.
func (d design) driftPerMin(b []float64) float64 {
	if d.poly < 1 {
		return 0
	}
	return 60 * b[d.colPoly] / d.tHalf
}

// curvaturePerMin2 converts the tau^2 coefficient to the quadratic
// coefficient of drift in minutes, arcsec/min^2.
func (d design) curvaturePerMin2(b []float64) float64 {
	if d.poly < 2 {
		return 0
	}
	return b[d.colPoly+1] * (60 / d.tHalf) * (60 / d.tHalf)
}

// harmonics extracts the fitted Curve, with sigmas from cov when given.
func (d design) harmonics(b, cov []float64) Curve {
	c := Curve{Harmonics: make([]Harmonic, d.K)}
	for k := 1; k <= d.K; k++ {
		ia := d.colHarm + 2*(k-1)
		ib := ia + 1
		var sa, sb float64
		if cov != nil {
			sa = math.Sqrt(math.Max(cov[ia*d.n+ia], 0))
			sb = math.Sqrt(math.Max(cov[ib*d.n+ib], 0))
		}
		c.Harmonics[k-1] = NewHarmonic(k, b[ia], b[ib], sa, sb)
	}
	return c
}

// corr returns the correlation between parameters i and j from cov.
func corr(cov []float64, n, i, j int) float64 {
	den := math.Sqrt(cov[i*n+i] * cov[j*n+j])
	if den == 0 {
		return 0
	}
	return cov[i*n+j] / den
}
