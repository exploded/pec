package web

import (
	"bytes"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/exploded/pec/internal/store"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	loc, _ := time.LoadLocation("Australia/Melbourne")
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "pec.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s, err := New(Options{DataDir: dir, Loc: loc, Version: "test", Store: st}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func doReq(t *testing.T, ts *httptest.Server, req *http.Request) (*http.Response, string) {
	t.Helper()
	req.Header.Set("HX-Request", "true")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func upload(t *testing.T, ts *httptest.Server, path, field, file string, extra map[string]string) (*http.Response, string) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile(field, filepath.Base(file))
	_, _ = fw.Write(data)
	for k, v := range extra {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return doReq(t, ts, req)
}

func postForm(t *testing.T, ts *httptest.Server, path string, form url.Values) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return doReq(t, ts, req)
}

func get(t *testing.T, ts *httptest.Server, path string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+path, nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestPagesRender(t *testing.T) {
	ts := newTestServer(t)
	for _, p := range []string{"/", "/analyse", "/table", "/verify", "/anchor", "/fit", "/report.css", "/static/css/app.css", "/static/js/htmx.min.js"} {
		resp, _ := get(t, ts, p)
		if resp.StatusCode != 200 {
			t.Errorf("%s: status %d", p, resp.StatusCode)
		}
	}
}

// analyseSession uploads the fixture and analyses session n, returning the run URL.
func analyseSession(t *testing.T, ts *httptest.Server, n string, extra url.Values) string {
	t.Helper()
	resp, body := upload(t, ts, "/analyse/upload", "log", "../../testdata/guidelog_excerpt.txt", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("upload status %d: %s", resp.StatusCode, body)
	}
	sha := regexp.MustCompile(`name="file" value="([0-9a-f]{64})"`).FindStringSubmatch(body)
	if sha == nil {
		t.Fatal("no file sha in fragment")
	}
	form := url.Values{"file": {sha[1]}, "name": {"log.txt"}, "session": {n}, "harmonics": {"3"}}
	for k, v := range extra {
		form[k] = v
	}
	resp, body = postForm(t, ts, "/analyse", form)
	if resp.StatusCode != 200 || resp.Header.Get("HX-Redirect") == "" {
		t.Fatalf("analyse status %d redirect %q: %s", resp.StatusCode, resp.Header.Get("HX-Redirect"), body)
	}
	return resp.Header.Get("HX-Redirect")
}

func TestAnalyseFlow(t *testing.T) {
	ts := newTestServer(t)
	_, body := upload(t, ts, "/analyse/upload", "log", "../../testdata/guidelog_excerpt.txt", nil)
	if !strings.Contains(body, "Guiding Assistant run") || strings.Count(body, "<tr class=") != 4 {
		t.Fatalf("sessions fragment unexpected:\n%s", body)
	}
	runURL := analyseSession(t, ts, "2", url.Values{"pec_on": {"off"}, "notes": {"first light"}})
	resp, page := get(t, ts, runURL)
	if resp.StatusCode != 200 {
		t.Fatalf("run page %d: %s", resp.StatusCode, page)
	}
	for _, want := range []string{"<svg", "Periodic error", "worm cycles", "Folded at the worm period", "first light", `value="off" selected`, "Download report"} {
		if !strings.Contains(page, want) {
			t.Errorf("run page lacks %q", want)
		}
	}
	resp, rep := get(t, ts, runURL+"/report.html")
	if resp.StatusCode != 200 || !strings.HasPrefix(rep, "<!DOCTYPE html>") || !strings.Contains(rep, "<style>") || strings.Contains(rep, "/report.css") {
		t.Errorf("standalone report: status %d", resp.StatusCode)
	}
	_, index := get(t, ts, "/")
	if !strings.Contains(index, "first light") || !strings.Contains(index, "guide log") {
		t.Error("index lacks the run")
	}

	// Notes update.
	resp, _ = postForm(t, ts, runURL+"/notes", url.Values{"notes": {"edited"}, "pec_on": {"on"}})
	if resp.StatusCode != 204 {
		t.Errorf("notes status %d", resp.StatusCode)
	}
	_, page = get(t, ts, runURL)
	if !strings.Contains(page, `value="edited"`) || !strings.Contains(page, `value="on" selected`) {
		t.Error("notes not updated")
	}

	// Validation error path returns 422 with the problem fragment.
	sha := regexp.MustCompile(`[0-9a-f]{64}`).FindString(body)
	resp, b := postForm(t, ts, "/analyse", url.Values{"file": {sha}, "session": {"2"}, "harmonics": {"40"}})
	if resp.StatusCode != 422 || !strings.Contains(b, "harmonics must be") {
		t.Errorf("validation: status %d body %s", resp.StatusCode, b)
	}

	// Delete.
	resp, _ = postForm(t, ts, runURL+"/delete", nil)
	if resp.StatusCode != 200 || resp.Header.Get("HX-Redirect") != "/" {
		t.Errorf("delete: %d", resp.StatusCode)
	}
	resp, _ = get(t, ts, runURL)
	if resp.StatusCode != 404 {
		t.Errorf("deleted run still served: %d", resp.StatusCode)
	}
}

func TestTableFlow(t *testing.T) {
	ts := newTestServer(t)
	resp, body := upload(t, ts, "/table", "table", "../../testdata/PEC_table_TCS_2026-09-12.txt", map[string]string{"harmonics": "4"})
	if resp.StatusCode != 200 || resp.Header.Get("HX-Redirect") == "" {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	_, page := get(t, ts, resp.Header.Get("HX-Redirect"))
	for _, want := range []string{"17 ticks", "5.01 ticks", "0.683″", "0.360″", "0.193″", "harmonic reconstruction"} {
		if !strings.Contains(page, want) {
			t.Errorf("table page lacks %q", want)
		}
	}
	resp, body = upload(t, ts, "/table", "table", "../../testdata/guidelog_excerpt.txt", nil)
	if resp.StatusCode != 422 || !strings.Contains(body, "not a TCS PEC table") {
		t.Errorf("bad table: status %d body %s", resp.StatusCode, body)
	}
}

func TestVerifyFlow(t *testing.T) {
	ts := newTestServer(t)
	u1 := analyseSession(t, ts, "2", url.Values{"pec_on": {"off"}})
	u2 := analyseSession(t, ts, "2", url.Values{"pec_on": {"on"}})
	id := func(u string) string { return strings.TrimPrefix(u, "/runs/") }
	_, page := get(t, ts, "/verify")
	if strings.Count(page, `name="before"`) != 2 {
		t.Errorf("verify page should list two runs")
	}
	resp, body := postForm(t, ts, "/verify", url.Values{"before": {id(u1)}, "after": {id(u2)}})
	if resp.StatusCode != 200 {
		t.Fatalf("verify %d: %s", resp.StatusCode, body)
	}
	// Same data both sides: ratio 1, "no difference".
	for _, want := range []string{"PEC made no difference", "verdict warn", "Harmonic amplitudes, before and after"} {
		if !strings.Contains(body, want) {
			t.Errorf("verify result lacks %q", want)
		}
	}
	resp, body = postForm(t, ts, "/verify", url.Values{"before": {id(u1)}, "after": {id(u1)}})
	if resp.StatusCode != 422 || !strings.Contains(body, "same run") {
		t.Errorf("same-run check: %d %s", resp.StatusCode, body)
	}
}
