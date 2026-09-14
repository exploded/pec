package web

import (
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/exploded/pec/internal/sky"
	"github.com/exploded/pec/internal/store"
)

// serverWithNINA builds a server whose NINA is the fake, with the site set.
func serverWithNINA(t *testing.T, f *fakeNINA) (*httptest.Server, *Server) {
	t.Helper()
	loc, _ := time.LoadLocation("Australia/Melbourne")
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "pec.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s, err := New(Options{DataDir: dir, Loc: loc, Version: "test", Store: st, NINAAPI: f.URL}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	if resp, body := postForm(t, ts, "/target", url.Values{"lat": {"-37.8"}, "lon": {"145"}}); resp.StatusCode != 200 {
		t.Fatalf("site: %d %s", resp.StatusCode, body)
	}
	return ts, s
}

// starsNow finds a catalogue star well up right now and one below the
// slew floor, from the test site.
func starsNow(t *testing.T) (up, low string) {
	t.Helper()
	p := sky.PlanAt(time.Now(), sky.Site{LatDeg: -37.8, LonDeg: 145}, sky.DefaultWindow())
	for _, c := range p.Candidates {
		if up == "" && c.AltDeg > 30 {
			up = c.Name
		}
		if low == "" && c.AltDeg < 10 {
			low = c.Name
		}
	}
	if up == "" || low == "" {
		t.Skipf("no suitable stars right now (up %q, low %q)", up, low)
	}
	return up, low
}

func TestTargetSlew(t *testing.T) {
	f := newFakeNINA(t)
	ts, _ := serverWithNINA(t, f)
	up, low := starsNow(t)
	slew := func(star string, confirm bool) (int, string) {
		form := url.Values{"star": {star}}
		if confirm {
			form.Set("confirm", "on")
		}
		resp, body := postForm(t, ts, "/target/slew", form)
		return resp.StatusCode, body
	}

	// The card shows the mount and, with a pick, the button.
	_, page := get(t, ts, "/target?at=2026-09-12T21:00")
	for _, want := range []string{"Mount, through NINA", "Telescope Simulator", "Slew the mount to Sadalmelik", "cannot check the roof", `id="slew-dialog"`} {
		if !strings.Contains(page, want) {
			t.Errorf("target page lacks %q", want)
		}
	}

	// Refusals, each with its reason.
	if code, body := slew(up, false); code != 422 || !strings.Contains(body, "confirmation") {
		t.Errorf("no confirm: %d %s", code, body)
	}
	if code, body := slew("Nowhere", true); code != 422 || !strings.Contains(body, "does not know") {
		t.Errorf("unknown star: %d %s", code, body)
	}
	if code, body := slew(low, true); code != 422 || !strings.Contains(body, "only") {
		t.Errorf("low star: %d %s", code, body)
	}
	cases := []struct {
		name string
		set  func()
		want string
	}{
		{"not connected", func() { f.Connected = false }, "no mount connected"},
		{"parked", func() { f.AtPark = true }, "parked"},
		{"not tracking", func() { f.Tracking = false }, "not tracking"},
		{"slewing", func() { f.Slewing = true }, "slewing"},
		{"roof closed", func() { f.HasDome = true; f.RoofOpen = false }, "roof reads closed"},
		{"unsafe", func() { f.HasSafety = true; f.Safe = false }, "unsafe"},
	}
	for _, c := range cases {
		f.mu.Lock()
		f.Connected, f.AtPark, f.Tracking, f.Slewing, f.HasDome, f.HasSafety = true, false, true, false, false, false
		c.set()
		f.mu.Unlock()
		if code, body := slew(up, true); code != 422 || !strings.Contains(body, c.want) {
			t.Errorf("%s: %d %s", c.name, code, body)
		}
		if len(f.Slews) != 0 {
			t.Fatalf("%s: a refused slew reached NINA", c.name)
		}
	}
	f.mu.Lock()
	f.Connected, f.AtPark, f.Tracking, f.Slewing, f.HasDome, f.RoofOpen, f.HasSafety, f.Safe = true, false, true, false, true, true, true, true
	f.SlewFails = true
	f.mu.Unlock()
	if code, body := slew(up, true); code != 422 || !strings.Contains(body, "did not complete") {
		t.Errorf("failed slew: %d %s", code, body)
	}

	// Success: the star's J2000 degrees go to NINA, the readback matches,
	// and the target is remembered for the recorder.
	f.mu.Lock()
	f.SlewFails = false
	f.mu.Unlock()
	code, body := slew(up, true)
	if code != 200 || !strings.Contains(body, "from "+up) || !strings.Contains(body, "Last slew from here: "+up) {
		t.Fatalf("slew: %d %s", code, body)
	}
	star, _ := findStar(up)
	q := f.Slews[len(f.Slews)-1]
	var ra, dec float64
	if _, err := parseFloats(q.Get("ra"), q.Get("dec"), &ra, &dec); err != nil || q.Get("waitForResult") != "true" {
		t.Errorf("slew query %v", q)
	}
	if ra < star.RAHours*15-0.001 || ra > star.RAHours*15+0.001 || dec < star.DecDeg-0.001 || dec > star.DecDeg+0.001 {
		t.Errorf("slew went to %v %v, want %v %v", ra, dec, star.RAHours*15, star.DecDeg)
	}
	if !strings.Contains(body, "Mount reports 0.0 arcmin from "+up) {
		t.Errorf("readback: %s", body)
	}
	_, page = get(t, ts, "/target")
	if !strings.Contains(page, "Last slew from here: "+up) {
		t.Errorf("last slew not shown: %s", page)
	}
}

// TestTargetNINADown: an absent NINA is a one-line note, not a stall.
func TestTargetNINADown(t *testing.T) {
	f := newFakeNINA(t)
	ts, _ := serverWithNINA(t, f)
	f.Close()
	start := time.Now()
	_, page := get(t, ts, "/target?at=2026-09-12T21:00")
	if !strings.Contains(page, "not answering") || strings.Contains(page, `class="danger" data-star=`) {
		t.Errorf("down page: %s", page)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("page took %v with NINA down", time.Since(start))
	}
	if code, body := postForm(t, ts, "/target/slew", url.Values{"star": {"Altair"}, "confirm": {"on"}}); code.StatusCode != 422 {
		t.Errorf("slew with NINA down: %d %s", code.StatusCode, body)
	}
}

func parseFloats(a, b string, x, y *float64) (int, error) {
	n, err := fmt.Sscan(a, x)
	if err != nil {
		return n, err
	}
	return fmt.Sscan(b, y)
}
