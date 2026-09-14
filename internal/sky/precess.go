package sky

import (
	"math"
	"time"
)

// precessAngles returns the equatorial precession angles zeta, z and theta
// (radians) from J2000 to the mean equinox of t (Meeus, Astronomical
// Algorithms, 21.3), accurate to well under an arcsecond for decades.
func precessAngles(t time.Time) (zeta, z, theta float64) {
	T := (JD(t) - 2451545.0) / 36525
	T2, T3 := T*T, T*T*T
	arcsec := math.Pi / 180 / 3600
	zeta = (2306.2181*T + 0.30188*T2 + 0.017998*T3) * arcsec
	z = (2306.2181*T + 1.09468*T2 + 0.018203*T3) * arcsec
	theta = (2004.3109*T - 0.42665*T2 - 0.041833*T3) * arcsec
	return
}

// J2000ToDate precesses mean J2000 coordinates (hours, degrees) to the mean
// equinox of date (Meeus 21.4). Proper motion is ignored: a few arcseconds
// for the brightest stars, nothing against a guide field.
func J2000ToDate(raHours, decDeg float64, t time.Time) (float64, float64) {
	zeta, z, theta := precessAngles(t)
	a0 := raHours * 15 * math.Pi / 180
	d0 := decDeg * math.Pi / 180
	A := math.Cos(d0) * math.Sin(a0+zeta)
	B := math.Cos(theta)*math.Cos(d0)*math.Cos(a0+zeta) - math.Sin(theta)*math.Sin(d0)
	C := math.Sin(theta)*math.Cos(d0)*math.Cos(a0+zeta) + math.Cos(theta)*math.Sin(d0)
	ra := math.Atan2(A, B) + z
	dec := math.Asin(C)
	return wrapHours(ra * 180 / math.Pi / 15), dec * 180 / math.Pi
}

// DateToJ2000 is the inverse of J2000ToDate: mean coordinates of date t
// back to J2000.
func DateToJ2000(raHours, decDeg float64, t time.Time) (float64, float64) {
	zeta, z, theta := precessAngles(t)
	a := raHours * 15 * math.Pi / 180
	d := decDeg * math.Pi / 180
	A := math.Cos(d) * math.Sin(a-z)
	B := math.Cos(theta)*math.Cos(d)*math.Cos(a-z) + math.Sin(theta)*math.Sin(d)
	C := -math.Sin(theta)*math.Cos(d)*math.Cos(a-z) + math.Cos(theta)*math.Sin(d)
	ra := math.Atan2(A, B) - zeta
	dec := math.Asin(C)
	return wrapHours(ra * 180 / math.Pi / 15), dec * 180 / math.Pi
}

func wrapHours(h float64) float64 {
	h = math.Mod(h, 24)
	if h < 0 {
		h += 24
	}
	return h
}
