package web

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/exploded/pec/internal/store"
)

func TestSettingsRoundTrip(t *testing.T) {
	ts := newTestServer(t)
	_, page := get(t, ts, "/settings")
	if !strings.Contains(page, `name="phd2_dir"`) || !strings.Contains(page, "Save settings") {
		t.Fatalf("settings page: %s", page)
	}
	resp, body := postForm(t, ts, "/settings", url.Values{
		"phd2_dir": {`C:\logs`}, "nina_dir": {`C:\nina`}, "tz": {"Australia/Melbourne"}, "lat": {"-37.8"}, "lon": {"145"},
	})
	if resp.StatusCode != 200 || !strings.Contains(body, "Saved.") {
		t.Fatalf("save: %d %s", resp.StatusCode, body)
	}
	_, page = get(t, ts, "/settings")
	for _, want := range []string{`value="C:\logs"`, `value="C:\nina"`, `value="Australia/Melbourne"`, `value="-37.8"`, `value="145"`, "using Australia/Melbourne", "not found on this PC"} {
		if !strings.Contains(page, want) {
			t.Errorf("settings page lacks %q", want)
		}
	}
	// The site is shared with the Point page and the Start page.
	_, page = get(t, ts, "/target")
	if !strings.Contains(page, `value="-37.8"`) {
		t.Error("target page does not show the site saved under settings")
	}
	_, page = get(t, ts, "/")
	if !strings.Contains(page, "37.8000 S, 145.0000 E") {
		t.Error("start page does not show the site")
	}

	resp, body = postForm(t, ts, "/settings", url.Values{"tz": {"Mars/Olympus"}})
	if resp.StatusCode != 422 || !strings.Contains(body, "time zone") {
		t.Errorf("bad tz: %d %s", resp.StatusCode, body)
	}
	resp, body = postForm(t, ts, "/settings", url.Values{"lat": {"95"}, "lon": {"145"}})
	if resp.StatusCode != 422 || !strings.Contains(body, "latitude") {
		t.Errorf("bad lat: %d %s", resp.StatusCode, body)
	}
	// Blank folders and zone go back to the defaults.
	resp, _ = postForm(t, ts, "/settings", url.Values{"phd2_dir": {""}, "nina_dir": {""}, "tz": {""}})
	if resp.StatusCode != 200 {
		t.Errorf("clear: %d", resp.StatusCode)
	}
	_, page = get(t, ts, "/settings")
	if strings.Contains(page, `value="C:\logs"`) || !strings.Contains(page, "using Australia/Melbourne") {
		t.Errorf("clear did not reset the folder or the zone fell back wrongly: %s", page)
	}
}

// A folder saved under Settings wins over the Options default.
func TestSettingsOverrideOptions(t *testing.T) {
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
	s, err := New(Options{DataDir: dir, Loc: loc, Version: "test", Store: st, PHD2Dir: filepath.Join(dir, "nowhere")}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	_, page := get(t, ts, "/analyse")
	if strings.Contains(page, "PHD2_GuideLog_2026-09-11_115345.txt") {
		t.Fatal("the default folder should have no logs")
	}
	if resp, _ := postForm(t, ts, "/settings", url.Values{"phd2_dir": {phd}}); resp.StatusCode != 200 {
		t.Fatalf("save: %d", resp.StatusCode)
	}
	_, page = get(t, ts, "/analyse")
	if !strings.Contains(page, `value="PHD2_GuideLog_2026-09-11_115345.txt"`) {
		t.Errorf("settings folder not used: %s", page)
	}
}

func TestStartPage(t *testing.T) {
	ts := newTestServer(t)
	_, page := get(t, ts, "/")
	if strings.Count(page, "not yet") < 3 || !strings.Contains(page, "Next:") || !strings.Contains(page, "Set the site") || !strings.Contains(page, "Check my PEC") {
		t.Fatalf("empty start page: %s", page)
	}
	postForm(t, ts, "/target", url.Values{"lat": {"-37.8"}, "lon": {"145"}})
	_, page = get(t, ts, "/")
	if !strings.Contains(page, "Record a night") {
		t.Errorf("after the site the next step should be Record: %s", page)
	}
	analyseSession(t, ts, "2", url.Values{"pec_on": {"off"}})
	_, page = get(t, ts, "/")
	if !strings.Contains(page, "1 PEC off") || !strings.Contains(page, "needs a PEC-off run and a PEC-on run") {
		t.Errorf("after a PEC-off run: %s", page)
	}
	analyseSession(t, ts, "2", url.Values{"pec_on": {"on"}})
	_, page = get(t, ts, "/")
	if !strings.Contains(page, "1 PEC on") || !strings.Contains(page, "ready: compare") || !strings.Contains(page, "Verify the two runs") {
		t.Errorf("after a PEC-on run: %s", page)
	}
	// A table plus a tcs-mode fit completes the sequence.
	resp, _ := upload(t, ts, "/table", "table", "../../testdata/PEC_table_TCS_2026-09-12.txt", nil)
	tid := strings.TrimPrefix(resp.Header.Get("HX-Redirect"), "/runs/")
	resp, body := postForm(t, ts, "/fit", url.Values{"mode": {"tcs"}, "table": {tid}, "harmonics": {"6"}})
	if !strings.HasPrefix(resp.Header.Get("HX-Redirect"), "/fits/") {
		t.Fatalf("fit: %d %s", resp.StatusCode, body)
	}
	_, page = get(t, ts, "/")
	for _, want := range []string{"ticks peak-to-peak", "paste it into the TCS", "the TCS table is on file"} {
		if !strings.Contains(page, want) {
			t.Errorf("start page after a fit lacks %q", want)
		}
	}
}

func TestSettingsLiveConnections(t *testing.T) {
	ts := newTestServer(t)
	_, page := get(t, ts, "/settings")
	if !strings.Contains(page, "Live connections") || !strings.Contains(page, `name="phd2_server"`) || strings.Count(page, ">off ·") != 2 {
		t.Fatalf("settings page: %s", page)
	}
	if resp, body := postForm(t, ts, "/settings", url.Values{"phd2_server": {"notaport"}}); resp.StatusCode != 422 || !strings.Contains(body, "PHD2 server") {
		t.Errorf("bad phd2 server: %d %s", resp.StatusCode, body)
	}
	if resp, body := postForm(t, ts, "/settings", url.Values{"phd2_server": {"127.0.0.1:x"}}); resp.StatusCode != 422 || !strings.Contains(body, "PHD2 server") {
		t.Errorf("bad phd2 port: %d %s", resp.StatusCode, body)
	}
	if resp, body := postForm(t, ts, "/settings", url.Values{"nina_api": {"ftp://x"}}); resp.StatusCode != 422 || !strings.Contains(body, "NINA API") {
		t.Errorf("bad nina api: %d %s", resp.StatusCode, body)
	}
	if resp, _ := postForm(t, ts, "/settings", url.Values{"phd2_server": {"127.0.0.1:1"}, "nina_api": {"http://127.0.0.1:1/"}}); resp.StatusCode != 200 {
		t.Errorf("save: %d", resp.StatusCode)
	}
	_, page = get(t, ts, "/settings")
	if !strings.Contains(page, `value="127.0.0.1:1"`) || !strings.Contains(page, `value="http://127.0.0.1:1/"`) || strings.Contains(page, ">off ·") {
		t.Errorf("saved connections not shown: %s", page)
	}
	if _, start := get(t, ts, "/"); !strings.Contains(start, "PHD2 feed") {
		t.Errorf("start page lacks the feed state: %s", start)
	}
	if resp, _ := postForm(t, ts, "/settings", url.Values{"phd2_server": {""}, "nina_api": {""}}); resp.StatusCode != 200 {
		t.Errorf("clear: %d", resp.StatusCode)
	}
	if _, page = get(t, ts, "/settings"); strings.Count(page, ">off ·") != 2 {
		t.Errorf("clear did not turn the connections off: %s", page)
	}
}
