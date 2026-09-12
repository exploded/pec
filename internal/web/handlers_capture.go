package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/exploded/pec/internal/mks"
	"github.com/exploded/pec/internal/report"
	"github.com/exploded/pec/internal/store"
	"github.com/exploded/pec/internal/store/db"
)

// maxCapture caps USB capture uploads. Ten minutes of the mount link is
// about 50 MB; an hour is a few hundred.
const maxCapture = 1 << 30

type captureRow struct {
	db.Capture
	When    string
	Started string
}

type captureView struct {
	Captures  []captureRow
	Body      template.HTML
	Title     string
	Level     string
	FileSHA   string
	FileName  string
	HasAnchor bool
}

func (s *Server) captureRows(ctx context.Context) ([]captureRow, error) {
	rows, err := s.st.Q.ListCaptures(ctx, 100)
	if err != nil {
		return nil, err
	}
	out := make([]captureRow, len(rows))
	for i, c := range rows {
		out[i] = captureRow{Capture: c}
		if t, err := time.Parse(time.RFC3339, c.CreatedAt); err == nil {
			out[i].When = t.In(s.opt.Loc).Format("2006-01-02 15:04")
		}
		if t, err := time.Parse(time.RFC3339Nano, c.StartedAt); err == nil {
			out[i].Started = t.In(s.opt.Loc).Format("2006-01-02 15:04:05")
		}
	}
	return out, nil
}

func (s *Server) capturePage(w http.ResponseWriter, r *http.Request) {
	rows, err := s.captureRows(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	s.page(w, r, "capture", "", pageData{Title: "USB capture", Nav: "capture", Data: captureView{Captures: rows}})
}

// analyseCapture decodes a stored capture file.
func (s *Server) analyseCapture(sha string) (*mks.Capture, *mks.Result, error) {
	f, err := os.Open(s.filePath(sha))
	if err != nil {
		return nil, nil, errors.New("the stored capture file is missing")
	}
	defer f.Close()
	c, err := mks.Decode(f)
	if err != nil {
		return nil, nil, err
	}
	res, err := mks.Analyse(c, mks.Options{Entries: s.opt.TCS.Entries})
	if err != nil {
		return nil, nil, err
	}
	return c, res, nil
}

// captureUpload stores the file, decodes it and shows the result with a
// Save button. Nothing is saved to the database yet.
func (s *Server) captureUpload(w http.ResponseWriter, r *http.Request) {
	base := pageData{Nav: "capture"}
	sha, name, err := s.saveUploadStream(r, "capture", "pcap", maxCapture)
	if err != nil {
		s.problem(w, r, "capture", base, err.Error())
		return
	}
	c, res, err := s.analyseCapture(sha)
	if err != nil {
		s.problem(w, r, "capture", base, err.Error())
		return
	}
	d := report.BuildCapture(c, res, name, s.opt.Loc)
	body, err := report.Body(d)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, http.StatusOK, "capture", "capture/_result", pageData{Nav: "capture",
		Data: captureView{Body: body, Title: d.Title, Level: res.Level, FileSHA: sha, FileName: name, HasAnchor: res.HasAnchor}})
}

// captureSave re-decodes the stored file and saves the anchor and the
// capture record.
func (s *Server) captureSave(w http.ResponseWriter, r *http.Request) {
	base := pageData{Nav: "capture"}
	_ = r.ParseForm()
	sha := r.FormValue("file")
	if !validSHA(sha) {
		s.problem(w, r, "capture", base, "upload a capture first")
		return
	}
	_, res, err := s.analyseCapture(sha)
	if err != nil {
		s.problem(w, r, "capture", base, err.Error())
		return
	}
	aid, _, err := s.st.SaveCapture(r.Context(), res, store.CaptureSaveMeta{FileSHA: sha, SourceName: r.FormValue("name"), Note: strings.TrimSpace(r.FormValue("note"))})
	if err != nil {
		s.fail(w, err)
		return
	}
	if aid != 0 {
		redirect(w, r, fmt.Sprintf("/fit?anchor=%d", aid))
		return
	}
	redirect(w, r, "/capture")
}

func (s *Server) captureDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad capture id", http.StatusNotFound)
		return
	}
	if err := s.st.Q.DeleteCapture(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, "/capture")
}

// saveUploadStream is saveUpload for large files: the upload is streamed
// to disk while being hashed, then renamed to its content address.
func (s *Server) saveUploadStream(r *http.Request, field, kind string, limit int64) (sha, name string, err error) {
	r.Body = http.MaxBytesReader(nil, r.Body, limit)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return "", "", fmt.Errorf("upload too large or malformed: %w", err)
	}
	f, hdr, err := r.FormFile(field)
	if err != nil {
		return "", "", errors.New("choose a file first")
	}
	defer f.Close()
	dir := filepath.Join(s.opt.DataDir, "files")
	tmp, err := os.CreateTemp(dir, "upload-*")
	if err != nil {
		return "", "", err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), f)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", "", fmt.Errorf("saving upload: %w", err)
	}
	if n == 0 {
		return "", "", errors.New("the file is empty")
	}
	sha = hex.EncodeToString(h.Sum(nil))
	name = filepath.Base(hdr.Filename)
	path := s.filePath(sha)
	if _, err := os.Stat(path); err != nil {
		if err := os.Rename(tmp.Name(), path); err != nil {
			return "", "", fmt.Errorf("saving upload: %w", err)
		}
	}
	if err := s.st.EnsureFile(r.Context(), sha, kind, name, n); err != nil {
		return "", "", err
	}
	return sha, name, nil
}
