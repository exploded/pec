package web

import (
	"io"
	"log/slog"
	"math"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/exploded/pec/internal/phd2live/phd2test"
	"github.com/exploded/pec/internal/store"
)

// waitPage polls a path until the body contains want.
func waitPage(t *testing.T, ts *httptest.Server, path, want string, d time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(d)
	var body string
	for time.Now().Before(deadline) {
		_, body = get(t, ts, path)
		if strings.Contains(body, want) {
			return body
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s never showed %q; last body:\n%s", path, want, body)
	return body
}

func TestTonightLiveFlow(t *testing.T) {
	fake := phd2test.New(t)
	loc, _ := time.LoadLocation("Australia/Melbourne")
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "pec.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s, err := New(Options{DataDir: dir, Loc: loc, Version: "test", Store: st, PHD2Server: fake.Addr()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	if !fake.WaitConn(3 * time.Second) {
		t.Fatal("recorder did not connect")
	}
	waitPage(t, ts, "/tonight/live", "Connected to PHD2 2.6.14dev1", 3*time.Second)
	if _, page := get(t, ts, "/settings"); !strings.Contains(page, "connected to PHD2 2.6.14dev1") {
		t.Errorf("settings page does not show the PHD2 connection")
	}

	// A Guiding Assistant run: three guided frames, then output off for 500 s.
	base := 5000.0
	fake.Send(phd2test.Event("StartGuiding", base, nil))
	waitPage(t, ts, "/tonight/live", "Run started", 3*time.Second)
	frame := 0
	step := func(pulse int) {
		frame++
		tt := float64(frame) * 2.5
		fake.Send(phd2test.GuideStep(base+tt, frame, tt, 0.4*math.Sin(2*math.Pi*tt/149.6), pulse))
	}
	for i := 0; i < 3; i++ {
		step(100)
	}
	fake.Send(phd2test.Event("GuideParamChange", base+8, map[string]any{"Name": "MountGuidingEnabled", "Value": false}))
	for i := 0; i < 200; i++ {
		step(0)
	}
	page := waitPage(t, ts, "/tonight/live", "Period so far", 6*time.Second)
	if !strings.Contains(page, "Guiding Assistant style") || !strings.Contains(page, "Enough for a fit") {
		t.Errorf("live card during the run: %s", page)
	}
	m := regexp.MustCompile(`Period so far</div><div class="mono">([0-9.]+) s`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no period on the card: %s", page)
	}
	if p, _ := strconv.ParseFloat(m[1], 64); p < 149 || p > 150.2 {
		t.Errorf("live period %s, want about 149.6", m[1])
	}
	if !strings.Contains(page, "declination unknown") {
		t.Errorf("without NINA or a slew the position warning should show: %s", page)
	}

	fake.Send(phd2test.Event("GuidingStopped", base+600, nil))
	page = waitPage(t, ts, "/tonight/live", "Save as PEC off", 5*time.Second)
	sha := regexp.MustCompile(`name="file" value="([0-9a-f]{64})"`).FindStringSubmatch(page)
	if sha == nil {
		t.Fatalf("no recording sha on the card: %s", page)
	}
	if _, runs := get(t, ts, "/analyse"); !strings.Contains(runs, `hx-post="/analyse/live"`) || !strings.Contains(runs, "PHD2 live") {
		t.Errorf("Runs page does not list the recording: %s", runs)
	}
	if _, start := get(t, ts, "/"); !strings.Contains(start, "Save the recorded run") {
		t.Errorf("Start page should point at the unsaved recording: %s", start)
	}

	// Save it the short way: the card's form posts /analyse.
	resp, body := postForm(t, ts, "/analyse", url.Values{"file": {sha[1]}, "session": {"1"}, "name": {"PHD2 live test"}, "pec_on": {"off"}})
	run := resp.Header.Get("HX-Redirect")
	if resp.StatusCode != 200 || !strings.HasPrefix(run, "/runs/") {
		t.Fatalf("save: %d %s %s", resp.StatusCode, run, body)
	}
	if _, rp := get(t, ts, run); !strings.Contains(rp, "With PEC off") || !strings.Contains(rp, "PHD2 live test") {
		t.Errorf("run page: %s", rp)
	}
	if _, page = get(t, ts, "/tonight/live"); strings.Contains(page, "Save as PEC") {
		t.Errorf("saved recording still offered: %s", page)
	}
	// The long way round still works, and a saved recording cannot be discarded.
	if resp, body := postForm(t, ts, "/analyse/live", url.Values{"file": {sha[1]}}); resp.StatusCode != 200 || !strings.Contains(body, "Guiding Assistant run") {
		t.Errorf("analyse/live: %d %s", resp.StatusCode, body)
	}
	if resp, body := postForm(t, ts, "/tonight/discard", url.Values{"file": {sha[1]}}); resp.StatusCode != 422 || !strings.Contains(body, "saved as a run") {
		t.Errorf("discard of a saved recording: %d %s", resp.StatusCode, body)
	}

	// A second, short run is discarded.
	fake.Send(phd2test.Event("StartGuiding", base+1000, nil))
	waitPage(t, ts, "/tonight/live", "Run started", 3*time.Second)
	for i := 0; i < 5; i++ {
		step(0)
	}
	fake.Send(phd2test.Event("GuidingStopped", base+1100, nil))
	page = waitPage(t, ts, "/tonight/live", "Save as PEC off", 5*time.Second)
	sha2 := regexp.MustCompile(`name="file" value="([0-9a-f]{64})"`).FindStringSubmatch(page)
	if sha2 == nil || sha2[1] == sha[1] {
		t.Fatalf("second recording: %v", sha2)
	}
	resp, body = postForm(t, ts, "/tonight/discard", url.Values{"file": {sha2[1]}})
	if resp.StatusCode != 200 || strings.Contains(body, "Save as PEC") {
		t.Errorf("discard: %d %s", resp.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(dir, "files", sha2[1])); !os.IsNotExist(err) {
		t.Errorf("discarded file still there: %v", err)
	}
}

func TestTonightFilter(t *testing.T) {
	f := newFakeNINA(t)
	ts, _ := serverWithNINA(t, f)
	_, page := get(t, ts, "/tonight")
	if !strings.Contains(page, "Select L filter") || !strings.Contains(page, "tracking (Siderial)") || !strings.Contains(page, "cooler on") {
		t.Fatalf("equipment line: %s", page)
	}
	resp, body := postForm(t, ts, "/tonight/filter", nil)
	if resp.StatusCode != 200 || len(f.Changes) != 1 || f.Changes[0] != "0" || !strings.Contains(body, "Luminance") || strings.Contains(body, "Select L filter") {
		t.Errorf("select L: %d changes %v body %s", resp.StatusCode, f.Changes, body)
	}
	f.mu.Lock()
	f.Filters, f.Filter = []string{"R", "G"}, 0
	f.mu.Unlock()
	if resp, body := postForm(t, ts, "/tonight/filter", nil); resp.StatusCode != 422 || !strings.Contains(body, "starting with L") {
		t.Errorf("no L: %d %s", resp.StatusCode, body)
	}
	f.mu.Lock()
	f.Filters = nil
	f.mu.Unlock()
	if resp, body := postForm(t, ts, "/tonight/filter", nil); resp.StatusCode != 422 || !strings.Contains(body, "no filter wheel") {
		t.Errorf("no wheel: %d %s", resp.StatusCode, body)
	}
}
