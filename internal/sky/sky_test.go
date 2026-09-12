package sky

import (
	"math"
	"testing"
	"time"
)

func TestGMST(t *testing.T) {
	// J2000.0: 2000-01-01 12:00 UT, GMST 18.697374558 h.
	got := GMST(time.Date(2000, 1, 1, 12, 0, 0, 0, time.UTC))
	if math.Abs(got-18.697374558) > 1e-6 {
		t.Fatalf("GMST at J2000 = %.6f", got)
	}
	// Meeus example 12.a: 1987 April 10 0h UT, GMST 13h 10m 46.3668s.
	got = GMST(time.Date(1987, 4, 10, 0, 0, 0, 0, time.UTC))
	want := 13 + 10/60.0 + 46.3668/3600
	if math.Abs(got-want) > 0.5/3600 {
		t.Fatalf("GMST 1987-04-10 = %.6f want %.6f", got, want)
	}
}

func TestAltitude(t *testing.T) {
	// On the meridian the altitude is 90 - |lat - dec|.
	if a := Altitude(0, 0, -37.8); math.Abs(a-52.2) > 1e-9 {
		t.Errorf("meridian altitude %.3f", a)
	}
	// Six hours off the meridian on the equator, seen from the equator: horizon.
	if a := Altitude(6, 0, 0); math.Abs(a) > 1e-9 {
		t.Errorf("horizon altitude %.3f", a)
	}
}

func TestHourAngleWrap(t *testing.T) {
	if h := HourAngle(1, 23); math.Abs(h-2) > 1e-9 {
		t.Errorf("wrap east: %.3f", h)
	}
	if h := HourAngle(23, 1); math.Abs(h+2) > 1e-9 {
		t.Errorf("wrap west: %.3f", h)
	}
}

func TestMoon(t *testing.T) {
	// Meeus example 47.a: 1992 April 12 0h TD, apparent lambda 133.167,
	// beta -3.229. The truncated series is good to a few tenths of a degree.
	at := time.Date(1992, 4, 12, 0, 0, 0, 0, time.UTC)
	m := MoonAt(at, Site{})
	T := (JD(at) - 2451545.0) / 36525
	ra, dec := eclToEq(133.167, -3.229, T)
	if sep := Separation(m.RAHours, m.DecDeg, ra, dec); sep > 0.5 {
		t.Fatalf("moon %.3f h %.2f deg is %.2f deg from Meeus", m.RAHours, m.DecDeg, sep)
	}
	// 1992-04-12 was two days after first quarter: about 70 % lit.
	if m.Illum < 0.6 || m.Illum > 0.85 {
		t.Errorf("illumination %.2f", m.Illum)
	}
}

func TestPlan(t *testing.T) {
	// Pick an instant and a site, then check the recommendation obeys the window.
	site := Site{LatDeg: -37.8, LonDeg: 145.0}
	at := time.Date(2026, 9, 12, 21, 5, 0, 0, time.FixedZone("AEST", 10*3600))
	w := DefaultWindow()
	p := PlanAt(at, site, w)
	var rec *Candidate
	for i := range p.Candidates {
		if p.Candidates[i].Recommend {
			rec = &p.Candidates[i]
		}
	}
	if rec == nil {
		t.Fatal("no recommendation")
	}
	if rec.Status != StatusGood || rec.HAHours < w.MinHA || rec.HAHours > w.MaxHA || rec.AltDeg < w.MinAlt {
		t.Fatalf("recommended %s: %+v", rec.Name, rec)
	}
	// Every candidate's status must match its geometry.
	for _, c := range p.Candidates {
		switch {
		case c.HAHours > w.MaxHA && c.Status != StatusPast:
			t.Errorf("%s past meridian window but %s", c.Name, c.Status)
		case c.HAHours < w.MinHA && c.Status != StatusSoon && c.Status != StatusUnder:
			t.Errorf("%s east of window but %s", c.Name, c.Status)
		}
	}
	// Melbourne, 21:05 AEST on 2026-09-12: Sadalmelik (Dec -0.3) has just
	// entered the window and beats Deneb Algedi (Dec -16) and Sadalsuud
	// (Dec -5.6), which stay in it longer.
	if rec.Name != "Sadalmelik" {
		t.Errorf("recommended %s, want Sadalmelik", rec.Name)
	}
	// The ideal RA sits mid-window.
	if h := HourAngle(p.LST, p.IdealRA); math.Abs(h-(w.MinHA+w.MaxHA)/2) > 1e-9 {
		t.Errorf("ideal RA hour angle %.3f", h)
	}
}

func TestFormat(t *testing.T) {
	if s := FormatHA(-1.5); s != "-1h 30m" {
		t.Errorf("FormatHA %q", s)
	}
	if s := FormatHA(0.999); s != "+1h 00m" {
		t.Errorf("FormatHA %q", s)
	}
	if s := FormatRA(23.999); s != "00h 00m" {
		t.Errorf("FormatRA %q", s)
	}
	if s := FormatRA(21.5259); s != "21h 32m" {
		t.Errorf("FormatRA %q", s)
	}
}
