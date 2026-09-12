package phd2

import (
	"math"
	"testing"
	"time"
)

func loadFixture(t *testing.T) *Log {
	t.Helper()
	loc, err := time.LoadLocation("Australia/Melbourne")
	if err != nil {
		t.Fatal(err)
	}
	l, err := ParseFile("../../testdata/guidelog_excerpt.txt", loc)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestParseStructure(t *testing.T) {
	l := loadFixture(t)
	if len(l.Versions) != 3 {
		t.Errorf("preambles %d, want 3", len(l.Versions))
	}
	if l.Calibrations != 1 {
		t.Errorf("calibrations %d, want 1", l.Calibrations)
	}
	if len(l.Sessions) != 4 {
		t.Fatalf("sessions %d, want 4", len(l.Sessions))
	}
	want := []struct {
		begins  string
		samples int
		pier    string
	}{
		{"2026-09-11 20:52:33", 16, "West"},
		{"2026-09-11 21:01:45", 40, "East"},
		{"2026-09-11 21:08:57", 20, "East"},
		{"2026-09-11 21:31:50", 240, "East"},
	}
	for i, w := range want {
		s := l.Sessions[i]
		if got := s.Begins.Format("2006-01-02 15:04:05"); got != w.begins {
			t.Errorf("session %d begins %s, want %s", i+1, got, w.begins)
		}
		if len(s.Samples) != w.samples {
			t.Errorf("session %d samples %d, want %d", i+1, len(s.Samples), w.samples)
		}
		if s.PierSide != w.pier {
			t.Errorf("session %d pier %q, want %q", i+1, s.PierSide, w.pier)
		}
		if s.Index != i+1 {
			t.Errorf("session index %d", s.Index)
		}
		if s.Ends.IsZero() {
			t.Errorf("session %d has no Ends", i+1)
		}
	}
	if _, err := l.Session(5); err == nil {
		t.Error("Session(5) should fail")
	}
}

func TestParseHeader(t *testing.T) {
	l := loadFixture(t)
	s := l.Sessions[1]
	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"pixel scale", s.PixelScale, 1.43},
		{"binning", float64(s.Binning), 2},
		{"focal length", s.FocalLength, 1157},
		{"exposure", float64(s.ExposureMS), 10000},
		{"RA", s.RAHours, 17.00},
		{"Dec", s.DecDeg, -40.4},
		{"HA", s.HourAngle, 3.08},
		{"Alt", s.AltDeg, 54.5},
		{"Az", s.AzDeg, 250.9},
	}
	for _, c := range checks {
		if math.Abs(c.got-c.want) > 1e-9 {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if s.Profile != "AT12IN" {
		t.Errorf("profile %q", s.Profile)
	}
	if s.Camera != "ZWO ASI220MM Mini (ASCOM)" {
		t.Errorf("camera %q", s.Camera)
	}
	if s.Header["Hysteresis"] != "0.100" {
		t.Errorf("header map missing Hysteresis: %q", s.Header["Hysteresis"])
	}
}

func TestSamplesAndTiming(t *testing.T) {
	l := loadFixture(t)
	s := l.Sessions[1]
	first := s.Samples[0]
	if first.Frame != 1 || math.Abs(first.Offset-11.426) > 1e-9 || math.Abs(first.RARaw-0.195) > 1e-9 {
		t.Errorf("first sample %+v", first)
	}
	if got := first.At.Sub(s.Begins).Seconds(); math.Abs(got-11.426) > 1e-6 {
		t.Errorf("At offset %v", got)
	}
	// Session 1, frame 9 (index 8) carried a real 104 ms west pulse.
	s1 := l.Sessions[0]
	if s1.Samples[8].RADuration != 104 || s1.Samples[8].RADirection != "W" {
		t.Errorf("frame 9 %+v", s1.Samples[8])
	}
	ra := s.RAArcsec(RASign)
	if math.Abs(ra[0]-0.195*1.43) > 1e-9 {
		t.Errorf("RAArcsec %v", ra[0])
	}
	med, _, _ := s.Cadence()
	if med < 10 || med > 11 {
		t.Errorf("cadence %v", med)
	}
}

func TestGuidingAssistantSession(t *testing.T) {
	l := loadFixture(t)
	s := l.Sessions[1]
	g := s.Guiding()
	if g.DisabledAfter != 2 {
		t.Errorf("DisabledAfter %d, want 2", g.DisabledAfter)
	}
	if g.ReenabledAfter != 39 {
		t.Errorf("ReenabledAfter %d, want 39", g.ReenabledAfter)
	}
	if !g.IsGA {
		t.Error("IsGA false")
	}
	rows, warnings := s.MeasurementSamples()
	if len(rows) != 37 || rows[0].Frame != 4 {
		t.Errorf("measurement rows %d starting at frame %d", len(rows), rows[0].Frame)
	}
	if len(warnings) != 1 {
		t.Errorf("warnings %v", warnings)
	}
	for _, r := range rows {
		if r.RADuration != 0 {
			t.Errorf("frame %d has RA correction in GA run", r.Frame)
		}
	}
	// A guided session reports corrections.
	_, w := l.Sessions[3].MeasurementSamples()
	if len(w) != 1 {
		t.Errorf("guided session warnings %v", w)
	}
}

func TestEvents(t *testing.T) {
	l := loadFixture(t)
	s3 := l.Sessions[2]
	if b := s3.Breaks(); len(b) != 1 {
		t.Errorf("session 3 breaks %v, want one (SET LOCK + DITHER pair collapsed)", b)
	}
	if s3.Dithers() != 1 {
		t.Errorf("session 3 dithers %d", s3.Dithers())
	}
	s4 := l.Sessions[3]
	if b := s4.Breaks(); len(b) != 4 {
		t.Errorf("session 4 breaks %v, want 4", b)
	}
	if s4.Drops() != 1 {
		t.Errorf("session 4 drops %d", s4.Drops())
	}
	var drop *Event
	for i := range s4.Events {
		if s4.Events[i].Kind == EventDrop {
			drop = &s4.Events[i]
		}
	}
	if drop == nil || drop.Frame != 234 || drop.Text != "Star lost - mass changed" || math.Abs(drop.Offset-2571.242) > 1e-9 {
		t.Errorf("drop event %+v", drop)
	}
	var dither *Event
	for i := range s3.Events {
		if s3.Events[i].Kind == EventDither {
			dither = &s3.Events[i]
		}
	}
	if dither == nil || math.Abs(dither.Dx+2.494) > 1e-9 || math.Abs(dither.LockX-111.353) > 1e-9 {
		t.Errorf("dither event %+v", dither)
	}
	// The break time is the offset of the last sample before the event.
	if dither.After < 0 || s3.Samples[dither.After].Offset != dither.Offset {
		t.Errorf("dither After/Offset %+v", dither)
	}
}
