package pe

import (
	"errors"
	"math"
)

// ErrRankDeficient is returned when the design matrix has (numerically)
// dependent columns, for example a segment with all samples at one time.
var ErrRankDeficient = errors.New("least squares: design matrix is rank deficient")

// LstSq solves min |X b - y| for a row-major X of m rows and n columns
// (m > n) by Householder QR. Householder keeps the condition number linear
// rather than squaring it as the normal equations would, and at the sizes
// this tool sees (m up to a few thousand, n up to about 20) it costs well
// under a millisecond.
//
// It returns the coefficients b, the residual sum of squares, and the
// inverse of the triangular factor R (n x n row-major, upper triangular) so
// that inv(X'X) = Rinv * Rinv', which gives parameter covariances for free.
func LstSq(X []float64, m, n int, y []float64) (b []float64, rss float64, rinv []float64, err error) {
	if m < n || n == 0 {
		return nil, 0, nil, errors.New("least squares: need more rows than columns")
	}
	if len(X) != m*n || len(y) != m {
		return nil, 0, nil, errors.New("least squares: dimension mismatch")
	}
	a := make([]float64, len(X))
	copy(a, X)
	z := make([]float64, m)
	copy(z, y)
	v := make([]float64, m)

	for j := 0; j < n; j++ {
		norm := 0.0
		for i := j; i < m; i++ {
			norm += a[i*n+j] * a[i*n+j]
		}
		norm = math.Sqrt(norm)
		if norm == 0 {
			return nil, 0, nil, ErrRankDeficient
		}
		alpha := -norm
		if a[j*n+j] < 0 {
			alpha = norm
		}
		vnorm2 := 0.0
		for i := j; i < m; i++ {
			v[i] = a[i*n+j]
			if i == j {
				v[i] -= alpha
			}
			vnorm2 += v[i] * v[i]
		}
		if vnorm2 == 0 {
			continue
		}
		for k := j; k < n; k++ {
			dot := 0.0
			for i := j; i < m; i++ {
				dot += v[i] * a[i*n+k]
			}
			f := 2 * dot / vnorm2
			for i := j; i < m; i++ {
				a[i*n+k] -= f * v[i]
			}
		}
		dot := 0.0
		for i := j; i < m; i++ {
			dot += v[i] * z[i]
		}
		f := 2 * dot / vnorm2
		for i := j; i < m; i++ {
			z[i] -= f * v[i]
		}
	}

	maxDiag := 0.0
	for j := 0; j < n; j++ {
		maxDiag = math.Max(maxDiag, math.Abs(a[j*n+j]))
	}
	for j := 0; j < n; j++ {
		if math.Abs(a[j*n+j]) < 1e-10*maxDiag {
			return nil, 0, nil, ErrRankDeficient
		}
	}

	b = make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		s := z[i]
		for k := i + 1; k < n; k++ {
			s -= a[i*n+k] * b[k]
		}
		b[i] = s / a[i*n+i]
	}
	for i := n; i < m; i++ {
		rss += z[i] * z[i]
	}

	// Invert the upper-triangular R column by column: R * c_j = e_j.
	rinv = make([]float64, n*n)
	for j := 0; j < n; j++ {
		for i := j; i >= 0; i-- {
			s := 0.0
			if i == j {
				s = 1
			}
			for k := i + 1; k <= j; k++ {
				s -= a[i*n+k] * rinv[k*n+j]
			}
			rinv[i*n+j] = s / a[i*n+i]
		}
	}
	return b, rss, rinv, nil
}

// Covariance returns sigma2 * Rinv * Rinv' (n x n row-major), the parameter
// covariance matrix for a fit whose LstSq returned rinv and whose residual
// variance estimate is sigma2 = rss/(m-n).
func Covariance(rinv []float64, n int, sigma2 float64) []float64 {
	cov := make([]float64, n*n)
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			s := 0.0
			for k := 0; k < n; k++ {
				s += rinv[i*n+k] * rinv[j*n+k]
			}
			cov[i*n+j] = sigma2 * s
			cov[j*n+i] = cov[i*n+j]
		}
	}
	return cov
}
