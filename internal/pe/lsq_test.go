package pe

import (
	"errors"
	"math"
	"testing"
)

func TestLstSq(t *testing.T) {
	cases := []struct {
		name    string
		X       []float64
		m, n    int
		y       []float64
		want    []float64
		wantRSS float64
	}{
		{
			// Exact line y = 1 + 2x through three points.
			name: "exact line", X: []float64{1, 0, 1, 1, 1, 2}, m: 3, n: 2,
			y: []float64{1, 3, 5}, want: []float64{1, 2}, wantRSS: 0,
		},
		{
			// Classic textbook: points (0,1),(1,2),(2,4) -> slope 1.5, intercept 5/6, rss 1/6.
			name: "overdetermined", X: []float64{1, 0, 1, 1, 1, 2}, m: 3, n: 2,
			y: []float64{1, 2, 4}, want: []float64{5.0 / 6, 1.5}, wantRSS: 1.0 / 6,
		},
		{
			// Negative leading entry exercises the reflector sign choice.
			name: "negative pivot", X: []float64{-1, 0, 1, 1, 1, 2, 1, 3}, m: 4, n: 2,
			y: []float64{-2, 3, 4, 5}, want: []float64{2, 1}, wantRSS: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, rss, rinv, err := LstSq(c.X, c.m, c.n, c.y)
			if err != nil {
				t.Fatal(err)
			}
			for i := range c.want {
				if math.Abs(b[i]-c.want[i]) > 1e-9 {
					t.Errorf("b[%d] = %v, want %v", i, b[i], c.want[i])
				}
			}
			if math.Abs(rss-c.wantRSS) > 1e-9 {
				t.Errorf("rss = %v, want %v", rss, c.wantRSS)
			}
			// Rinv Rinv' must equal inv(X'X).
			cov := Covariance(rinv, c.n, 1)
			xtx := make([]float64, c.n*c.n)
			for i := 0; i < c.n; i++ {
				for j := 0; j < c.n; j++ {
					for r := 0; r < c.m; r++ {
						xtx[i*c.n+j] += c.X[r*c.n+i] * c.X[r*c.n+j]
					}
				}
			}
			for i := 0; i < c.n; i++ {
				for j := 0; j < c.n; j++ {
					s := 0.0
					for k := 0; k < c.n; k++ {
						s += xtx[i*c.n+k] * cov[k*c.n+j]
					}
					want := 0.0
					if i == j {
						want = 1
					}
					if math.Abs(s-want) > 1e-9 {
						t.Errorf("(X'X)(cov)[%d,%d] = %v, want %v", i, j, s, want)
					}
				}
			}
		})
	}
}

func TestLstSqRankDeficient(t *testing.T) {
	// Second column is twice the first.
	X := []float64{1, 2, 2, 4, 3, 6}
	_, _, _, err := LstSq(X, 3, 2, []float64{1, 2, 3})
	if !errors.Is(err, ErrRankDeficient) {
		t.Fatalf("err = %v, want ErrRankDeficient", err)
	}
}
