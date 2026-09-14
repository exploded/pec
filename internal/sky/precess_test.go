package sky

import (
	"math"
	"testing"
	"time"
)

// Meeus, example 21.b: theta Persei (after proper motion) on 2028 Nov 13.19.
func TestPrecessMeeus(t *testing.T) {
	at := time.Date(2028, 11, 13, 4, 33, 36, 0, time.UTC)
	ra, dec := J2000ToDate(41.054063/15, 49.227750, at)
	if d := math.Abs(ra*15 - 41.547214); d > 0.0005 {
		t.Errorf("RA %.6f deg, want 41.547214", ra*15)
	}
	if d := math.Abs(dec - 49.348483); d > 0.0005 {
		t.Errorf("Dec %.6f, want 49.348483", dec)
	}
	ra0, dec0 := DateToJ2000(ra, dec, at)
	if math.Abs(ra0*15-41.054063) > 1e-9 || math.Abs(dec0-49.227750) > 1e-9 {
		t.Errorf("round trip %.9f %.9f", ra0*15, dec0)
	}
}

// Precession over 26 years moves an equatorial star by about 20 arcminutes
// in RA; a wrong sign would show as motion the other way.
func TestPrecessDirection(t *testing.T) {
	at := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	ra, dec := J2000ToDate(0, 0, at)
	if ra < 0.020 || ra > 0.025 || dec < 0.13 || dec > 0.16 {
		t.Errorf("origin precessed to %.4f h, %.4f deg", ra, dec)
	}
	if r, _ := J2000ToDate(23.99, 0, at); r > 1 {
		t.Errorf("RA did not wrap: %v", r)
	}
}
