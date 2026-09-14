package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/exploded/pec/internal/store"
)

func TestTargetFlow(t *testing.T) {
	ts := newTestServer(t)

	// No site yet: the page asks for one.
	_, page := get(t, ts, "/target")
	if !strings.Contains(page, "Enter the site first") {
		t.Fatalf("empty page: %s", page)
	}

	// A bad latitude is refused.
	resp, body := postForm(t, ts, "/target", url.Values{"lat": {"95"}, "lon": {"145"}})
	if resp.StatusCode != 422 || !strings.Contains(body, "latitude") {
		t.Fatalf("bad lat: %d %s", resp.StatusCode, body)
	}

	// A good site at a fixed instant gives a recommendation and is remembered.
	resp, body = postForm(t, ts, "/target", url.Values{"lat": {"-37.8"}, "lon": {"145"}, "at": {"2026-09-12T21:00"}})
	if resp.StatusCode != 200 || !strings.Contains(body, "Slew to") || !strings.Contains(body, "Planned for") {
		t.Fatalf("plan: %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "37.8000 S, 145.0000 E") {
		t.Errorf("site text missing: %s", body)
	}
	_, page = get(t, ts, "/target")
	if !strings.Contains(page, `value="-37.8"`) || !strings.Contains(page, `value="145"`) {
		t.Errorf("site not remembered: %s", page)
	}
	if !strings.Contains(page, `hx-trigger="every 60s"`) {
		t.Errorf("live page should refresh")
	}

	// The htmx refresh returns just the result fragment.
	req, _ := http.NewRequest("GET", ts.URL+"/target?lat=-37.8&lon=145", nil)
	resp, body = doReq(t, ts, req)
	if resp.StatusCode != 200 || strings.Contains(body, "<html") || !strings.Contains(body, "Sidereal time") {
		t.Errorf("fragment: %d %s", resp.StatusCode, body)
	}
}

func TestAnalyseLocal(t *testing.T) {
	loc, _ := time.LoadLocation("Australia/Melbourne")
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "pec.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	phd := filepath.Join(dir, "PHD2")
	os.MkdirAll(phd, 0o755)
	data, _ := os.ReadFile("../../testdata/guidelog_excerpt.txt")
	os.WriteFile(filepath.Join(phd, "PHD2_GuideLog_2026-09-11_115345.txt"), data, 0o644)
	os.WriteFile(filepath.Join(phd, "PHD2_DebugLog_2026-09-11_115345.txt"), []byte("x"), 0o644)
	s, err := New(Options{DataDir: dir, Loc: loc, Version: "test", Store: st, PHD2Dir: phd}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	_, page := get(t, ts, "/analyse")
	if !strings.Contains(page, `value="PHD2_GuideLog_2026-09-11_115345.txt"`) || strings.Contains(page, "DebugLog") {
		t.Fatalf("local list: %s", page)
	}
	resp, body := postForm(t, ts, "/analyse/local", url.Values{"name": {"PHD2_GuideLog_2026-09-11_115345.txt"}})
	if resp.StatusCode != 200 || !strings.Contains(body, "Guiding Assistant run") || !strings.Contains(body, "PHD2_GuideLog_2026-09-11_115345.txt") {
		t.Fatalf("local sessions: %d %s", resp.StatusCode, body)
	}
	for _, bad := range []string{"../pec.db", "PHD2_GuideLog_missing.txt", "notes.txt"} {
		if resp, _ := postForm(t, ts, "/analyse/local", url.Values{"name": {bad}}); resp.StatusCode != 422 {
			t.Errorf("%q: %d", bad, resp.StatusCode)
		}
	}
}

func TestTonightPage(t *testing.T) {
	ts := newTestServer(t)
	resp, body := get(t, ts, "/tonight")
	if resp.StatusCode != 200 || !strings.Contains(body, "ProTrack off") || strings.Count(body, "data-step=") != 19 {
		t.Fatalf("tonight: %d", resp.StatusCode)
	}
	if !strings.Contains(body, `hx-trigger="every 5s"`) || !strings.Contains(body, "live feed is off") || !strings.Contains(body, "Advanced API is not set up") {
		t.Errorf("tonight without connections: %s", body)
	}
}

func TestTargetNINA(t *testing.T) {
	loc, _ := time.LoadLocation("Australia/Melbourne")
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "pec.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	nina := filepath.Join(dir, "Profiles")
	os.MkdirAll(nina, 0o755)
	os.WriteFile(filepath.Join(nina, "a.profile"), []byte(`{"Name":"Obs","AstrometrySettings":{"Latitude":-33.86,"Longitude":151.21}}`), 0o644)
	s, err := New(Options{DataDir: dir, Loc: loc, Version: "test", Store: st, NINADir: nina}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	resp, _ := postForm(t, ts, "/target/nina", nil)
	if resp.StatusCode != 200 || resp.Header.Get("HX-Redirect") != "/target" {
		t.Fatalf("nina: %d %v", resp.StatusCode, resp.Header)
	}
	_, page := get(t, ts, "/target")
	if !strings.Contains(page, `value="-33.86"`) || !strings.Contains(page, `value="151.21"`) {
		t.Errorf("site not saved from NINA: %s", page)
	}

	// Without a profile directory the button reports the problem.
	s.opt.NINADir = filepath.Join(dir, "nowhere")
	resp, body := postForm(t, ts, "/target/nina", nil)
	if resp.StatusCode != 422 || !strings.Contains(body, "NINA") {
		t.Errorf("missing profiles: %d %s", resp.StatusCode, body)
	}
}
