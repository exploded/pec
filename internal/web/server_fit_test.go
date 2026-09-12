package web

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/exploded/pec/internal/analysis"
	"github.com/exploded/pec/internal/tcs"
)

func TestAnchorFlow(t *testing.T) {
	ts := newTestServer(t)
	resp, body := postForm(t, ts, "/anchor", url.Values{"index": {"512"}, "note": {"from the TCS window"}})
	if resp.StatusCode != 200 || !strings.Contains(body, ">512<") || !strings.Contains(body, "from the TCS window") {
		t.Fatalf("anchor create %d: %s", resp.StatusCode, body)
	}
	resp, body = postForm(t, ts, "/anchor", url.Values{"index": {"1250"}})
	if resp.StatusCode != 422 || !strings.Contains(body, "0 to 1249") {
		t.Errorf("out of range: %d %s", resp.StatusCode, body)
	}
	resp, body = postForm(t, ts, "/anchor", url.Values{"index": {"7"}, "at": {"2026-09-12T21:03:04"}, "sigma": {"1"}})
	if resp.StatusCode != 200 || !strings.Contains(body, "2026-09-12 21:03:04") || !strings.Contains(body, "1.0 s") {
		t.Errorf("typed time: %d %s", resp.StatusCode, body)
	}
	_, page := get(t, ts, "/anchor")
	if strings.Count(page, "/delete") != 2 {
		t.Errorf("anchor page should list two anchors")
	}
	resp, body = postForm(t, ts, "/anchors/1/delete", nil)
	if resp.StatusCode != 200 || strings.Contains(body, ">512<") || !strings.Contains(body, ">7<") {
		t.Errorf("delete: %d %s", resp.StatusCode, body)
	}
}

func TestFitTCSFlow(t *testing.T) {
	ts := newTestServer(t)
	resp, _ := upload(t, ts, "/table", "table", "../../testdata/PEC_table_TCS_2026-09-12.txt", nil)
	tid := strings.TrimPrefix(resp.Header.Get("HX-Redirect"), "/runs/")

	// Refusal without a phase reference, and nothing saved.
	resp, body := postForm(t, ts, "/fit/preview", url.Values{"harmonics": {"6"}})
	if resp.StatusCode != 422 || !strings.Contains(body, "phase reference is required") {
		t.Fatalf("no phase ref: %d %s", resp.StatusCode, body)
	}
	resp, body = postForm(t, ts, "/fit", url.Values{"harmonics": {"6"}})
	if resp.StatusCode != 422 {
		t.Fatalf("save without phase ref: %d %s", resp.StatusCode, body)
	}
	_, page := get(t, ts, "/fit")
	if strings.Contains(page, "Saved tables") {
		t.Error("a refused fit was saved")
	}

	form := url.Values{"mode": {"tcs"}, "table": {tid}, "harmonics": {"6"}}
	resp, body = postForm(t, ts, "/fit/preview", form)
	if resp.StatusCode != 200 {
		t.Fatalf("preview: %d %s", resp.StatusCode, body)
	}
	for _, want := range []string{"Table ready", "Save table", "verdict good", "smoothed curve", "<svg"} {
		if !strings.Contains(body, want) {
			t.Errorf("preview lacks %q", want)
		}
	}
	form.Set("notes", "first table")
	resp, body = postForm(t, ts, "/fit", form)
	fitURL := resp.Header.Get("HX-Redirect")
	if resp.StatusCode != 200 || !strings.HasPrefix(fitURL, "/fits/") {
		t.Fatalf("save: %d %q %s", resp.StatusCode, fitURL, body)
	}
	_, page = get(t, ts, fitURL)
	for _, want := range []string{"Download table", "Download meta", "first table", "pec_table.txt", "Copy to clipboard", "<svg"} {
		if !strings.Contains(page, want) {
			t.Errorf("fit page lacks %q", want)
		}
	}
	resp, txt := get(t, ts, fitURL+"/pec_table.txt")
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), "pec_table_fit") {
		t.Fatalf("table download: %d", resp.StatusCode)
	}
	tbl, err := tcs.Read(strings.NewReader(txt))
	if err != nil || len(tbl.Values) != 1250 {
		t.Fatalf("downloaded table: %v", err)
	}
	if !strings.HasPrefix(txt, "   0\t") || strings.Contains(txt, "\r") {
		t.Errorf("table text format: %q", txt[:20])
	}
	resp, mj := get(t, ts, fitURL+"/pec_table.meta.json")
	var m analysis.Meta
	if resp.StatusCode != 200 || json.Unmarshal([]byte(mj), &m) != nil {
		t.Fatalf("meta download: %d %s", resp.StatusCode, mj)
	}
	if m.Mode != "tcs" || m.Entries != 1250 || m.Table.P2P != tbl.Stats().P2P || m.Source.Name == "" || m.Tool != "pec" || m.Inverted {
		t.Errorf("meta: %+v", m)
	}

	// Invert is the exact negation of the saved table.
	form.Set("invert", "1")
	resp, _ = postForm(t, ts, "/fit", form)
	fit2 := resp.Header.Get("HX-Redirect")
	_, txt2 := get(t, ts, fit2+"/pec_table.txt")
	tbl2, err := tcs.Read(strings.NewReader(txt2))
	if err != nil {
		t.Fatal(err)
	}
	for i := range tbl.Values {
		if tbl2.Values[i] != -tbl.Values[i] {
			t.Fatalf("index %d: %d vs inverted %d", i, tbl.Values[i], tbl2.Values[i])
		}
	}
	_, page = get(t, ts, "/fit")
	if !strings.Contains(page, `href="`+fitURL+`"`) || !strings.Contains(page, `href="`+fit2+`"`) {
		t.Error("fit page does not list both saved tables")
	}
	resp, _ = postForm(t, ts, fit2+"/delete", nil)
	if resp.StatusCode != 200 || resp.Header.Get("HX-Redirect") != "/fit" {
		t.Errorf("delete: %d", resp.StatusCode)
	}
	if resp, _ = get(t, ts, fit2); resp.StatusCode != 404 {
		t.Errorf("deleted fit still served: %d", resp.StatusCode)
	}
}

func TestFitIndexFlow(t *testing.T) {
	ts := newTestServer(t)
	runURL := analyseSession(t, ts, "2", url.Values{"pec_on": {"off"}})
	rid := strings.TrimPrefix(runURL, "/runs/")
	if resp, _ := postForm(t, ts, "/anchor", url.Values{"index": {"512"}}); resp.StatusCode != 200 {
		t.Fatal("anchor")
	}
	form := url.Values{"mode": {"index"}, "run": {rid}, "anchor": {"1"}, "harmonics": {"3"}, "exposure_shift": {"1"}}
	resp, body := postForm(t, ts, "/fit/preview", form)
	if resp.StatusCode != 200 {
		t.Fatalf("preview: %d %s", resp.StatusCode, body)
	}
	for _, want := range []string{"Phase-error budget", "index 512", "Measured error in table phase", "tracking must have run uninterrupted"} {
		if !strings.Contains(body, want) {
			t.Errorf("preview lacks %q", want)
		}
	}
	resp, _ = postForm(t, ts, "/fit", form)
	fitURL := resp.Header.Get("HX-Redirect")
	if fitURL == "" {
		t.Fatal("save did not redirect")
	}
	_, mj := get(t, ts, fitURL+"/pec_table.meta.json")
	var m analysis.Meta
	if err := json.Unmarshal([]byte(mj), &m); err != nil {
		t.Fatal(err)
	}
	if m.Mode != "index" || m.PhaseRef.Index != 512 || m.PhaseRef.At == nil || m.Session == nil || m.Session.Index != 2 ||
		m.AnchorID != 1 || m.PhaseError == nil || m.Analysis == nil || m.MeasuredWithPEC == nil || *m.MeasuredWithPEC {
		t.Errorf("meta: %s", mj)
	}
	// The saved page rebuilds the charts from the stored file, even after
	// the run and the anchor are gone.
	postForm(t, ts, runURL+"/delete", nil)
	postForm(t, ts, "/anchors/1/delete", nil)
	resp, page := get(t, ts, fitURL)
	if resp.StatusCode != 200 || !strings.Contains(page, "Measured error in table phase") || !strings.Contains(page, "<svg") {
		t.Errorf("saved page after deletions: %d", resp.StatusCode)
	}
	resp, body = postForm(t, ts, "/fit/preview", form)
	if resp.StatusCode != 422 {
		t.Errorf("preview with a deleted run should fail: %d %s", resp.StatusCode, body)
	}
}
