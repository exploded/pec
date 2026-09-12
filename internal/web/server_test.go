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
	"regexp"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	loc, _ := time.LoadLocation("Australia/Melbourne")
	s, err := New(Options{DataDir: t.TempDir(), Loc: loc, Version: "test"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func upload(t *testing.T, ts *httptest.Server, path, field, file string, extra map[string]string) (int, string) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile(field, "upload.txt")
	_, _ = fw.Write(data)
	for k, v := range extra {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("HX-Request", "true")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestPagesRender(t *testing.T) {
	ts := newTestServer(t)
	for _, p := range []string{"/", "/analyse", "/table", "/static/css/app.css", "/static/js/htmx.min.js"} {
		resp, err := ts.Client().Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("%s: status %d", p, resp.StatusCode)
		}
	}
}

func TestAnalyseFlow(t *testing.T) {
	ts := newTestServer(t)
	status, body := upload(t, ts, "/analyse/upload", "log", "../../testdata/guidelog_excerpt.txt", nil)
	if status != 200 {
		t.Fatalf("upload status %d: %s", status, body)
	}
	if !strings.Contains(body, "Guiding Assistant run") || strings.Count(body, "<tr class=") != 4 {
		t.Fatalf("sessions fragment unexpected:\n%s", body)
	}
	sha := regexp.MustCompile(`name="file" value="([0-9a-f]{64})"`).FindStringSubmatch(body)
	if sha == nil {
		t.Fatal("no file sha in fragment")
	}
	form := url.Values{"file": {sha[1]}, "name": {"log.txt"}, "session": {"2"}, "harmonics": {"3"}}
	req, _ := http.NewRequest("POST", ts.URL+"/analyse", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	body = string(b)
	if resp.StatusCode != 200 {
		t.Fatalf("analyse status %d: %s", resp.StatusCode, body)
	}
	for _, want := range []string{"Worm period", "worm cycles", "samples before guiding was disabled", "Harmonics"} {
		if !strings.Contains(body, want) {
			t.Errorf("result lacks %q", want)
		}
	}

	// Validation error path returns 422 with the problem fragment.
	form.Set("harmonics", "40")
	req, _ = http.NewRequest("POST", ts.URL+"/analyse", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	resp, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 422 || !strings.Contains(string(b), "harmonics must be") {
		t.Errorf("validation: status %d body %s", resp.StatusCode, b)
	}
}

func TestTableFlow(t *testing.T) {
	ts := newTestServer(t)
	status, body := upload(t, ts, "/table", "table", "../../testdata/PEC_table_TCS_2026-09-12.txt", map[string]string{"harmonics": "4"})
	if status != 200 {
		t.Fatalf("status %d: %s", status, body)
	}
	for _, want := range []string{"17 ticks", "5.01 ticks", "0.683", "0.360", "0.193"} {
		if !strings.Contains(body, want) {
			t.Errorf("table result lacks %q\n%s", want, body)
		}
	}
	status, body = upload(t, ts, "/table", "table", "../../testdata/guidelog_excerpt.txt", nil)
	if status != 422 || !strings.Contains(body, "not a TCS PEC table") {
		t.Errorf("bad table: status %d body %s", status, body)
	}
}
