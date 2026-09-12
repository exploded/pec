package sky

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNINASite(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.profile")
	os.WriteFile(old, []byte(`<?xml version="1.0"?><Profile><Name>Old rig</Name><AstrometrySettings><Latitude>-33.5</Latitude><Longitude>151.2</Longitude></AstrometrySettings></Profile>`), 0o644)
	cur := filepath.Join(dir, "cur.profile")
	os.WriteFile(cur, []byte(`{"Name":"Observatory","AstrometrySettings":{"Latitude":-37.8136,"Longitude":144.9631,"Elevation":31}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)
	past := time.Now().Add(-time.Hour)
	os.Chtimes(old, past, past)

	s, name, err := NINASite(dir)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Observatory" || s.LatDeg != -37.8136 || s.LonDeg != 144.9631 {
		t.Fatalf("got %q %+v", name, s)
	}

	// The XML fallback works on its own.
	s, name, err = parseNINAProfile([]byte(`<Profile><Name>Old rig</Name><Latitude>-33.5</Latitude><Longitude>151.2</Longitude></Profile>`))
	if err != nil || name != "Old rig" || s.LatDeg != -33.5 {
		t.Fatalf("xml: %v %q %+v", err, name, s)
	}
	if _, _, err := parseNINAProfile([]byte(`{"AstrometrySettings":{"Latitude":0,"Longitude":0}}`)); err == nil {
		t.Error("0,0 should be rejected")
	}
	if _, _, err := NINASite(filepath.Join(dir, "missing")); err == nil {
		t.Error("missing dir should fail")
	}
}
