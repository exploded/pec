package web

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/exploded/pec/internal/analysis"
	"github.com/exploded/pec/internal/mks"
)

func TestCaptureFlow(t *testing.T) {
	ts := newTestServer(t)
	// A synthetic capture overlapping session 2 of the fixture log
	// (2026-09-11 21:01:45 Melbourne = 11:01:45 UTC).
	start := time.Date(2026, 9, 11, 11, 0, 0, 0, time.UTC)
	data := mks.Synth(mks.SynthOptions{Start: start, Duration: 600, Rate: 133.6, Encoder0: 1000, IndexOffset: 72.5, WithIndex: true})
	path := filepath.Join(t.TempDir(), "night.pcapng")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	resp, body := upload(t, ts, "/capture", "capture", path, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("decode: %d %s", resp.StatusCode, body)
	}
	for _, want := range []string{"Anchor: index", "worm period 149.7", "Save anchor", "Encoder against the fitted rate", "PEC index readings against the encoder", `name="file" value="`} {
		if !strings.Contains(body, want) {
			t.Errorf("result lacks %q", want)
		}
	}
	sha := strings.SplitN(strings.SplitN(body, `name="file" value="`, 2)[1], `"`, 2)[0]
	resp, body = postForm(t, ts, "/capture/save", url.Values{"file": {sha}, "name": {"night.pcapng"}, "note": {"first capture"}})
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("HX-Redirect"), "/fit?anchor=") {
		t.Fatalf("save: %d %q %s", resp.StatusCode, resp.Header.Get("HX-Redirect"), body)
	}
	_, page := get(t, ts, "/anchor")
	if !strings.Contains(page, "capture") || !strings.Contains(page, "149.7") || !strings.Contains(page, "first capture") {
		t.Error("anchor list lacks the capture anchor with its period")
	}
	_, page = get(t, ts, "/capture")
	if !strings.Contains(page, "Saved captures") || !strings.Contains(page, "night.pcapng") {
		t.Error("capture page lacks the saved capture")
	}

	// Fit from the fixture session with the capture anchor: the period
	// comes from the anchor, with its tiny uncertainty.
	runURL := analyseSession(t, ts, "2", url.Values{"pec_on": {"off"}})
	form := url.Values{"mode": {"index"}, "run": {strings.TrimPrefix(runURL, "/runs/")}, "anchor": {"1"}, "harmonics": {"3"}, "exposure_shift": {"1"}}
	resp, _ = postForm(t, ts, "/fit", form)
	fitURL := resp.Header.Get("HX-Redirect")
	if fitURL == "" {
		t.Fatal("fit did not save")
	}
	_, mj := get(t, ts, fitURL+"/pec_table.meta.json")
	var m analysis.Meta
	if err := json.Unmarshal([]byte(mj), &m); err != nil {
		t.Fatal(err)
	}
	if m.Period.Source != "anchor" || m.Period.SigmaS > 0.01 || m.PhaseError == nil || m.PhaseError.TotalDeg > 2 {
		t.Errorf("fit should use the capture period: %+v %+v", m.Period, m.PhaseError)
	}

	// Not a capture.
	resp, body = upload(t, ts, "/capture", "capture", "../../testdata/guidelog_excerpt.txt", nil)
	if resp.StatusCode != 422 || !strings.Contains(body, "not a pcap") {
		t.Errorf("bad file: %d %s", resp.StatusCode, body)
	}
	// Delete the capture record; the anchor stays.
	resp, _ = postForm(t, ts, "/captures/1/delete", nil)
	if resp.StatusCode != 200 {
		t.Errorf("delete %d", resp.StatusCode)
	}
	_, page = get(t, ts, "/anchor")
	if !strings.Contains(page, "first capture") {
		t.Error("anchor should survive capture deletion")
	}
}
