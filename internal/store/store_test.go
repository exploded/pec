package store

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/exploded/pec/internal/analysis"
	"github.com/exploded/pec/internal/phd2"
)

func TestStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "pec.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	loc, _ := time.LoadLocation("Australia/Melbourne")
	l, err := phd2.ParseFile("../../testdata/guidelog_excerpt.txt", loc)
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := l.Session(2)
	p := analysis.DefaultParams()
	p.Harmonics = 3
	res, err := analysis.Session(sess, p)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureFile(ctx, "abc", "phd2", "log.txt", 10); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureFile(ctx, "abc", "phd2", "log2.txt", 10); err != nil {
		t.Fatal("upsert should be idempotent:", err)
	}
	id, err := s.SaveSession(ctx, res, SaveMeta{FileSHA: "abc", SourceName: "log.txt", Version: "t"})
	if err != nil {
		t.Fatal(err)
	}
	on := true
	id2, err := s.SaveSession(ctx, res, SaveMeta{FileSHA: "abc", SourceName: "log.txt", Version: "t", PecOn: &on, Notes: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if id2 != id+1 {
		t.Errorf("ids %d %d", id, id2)
	}

	r, err := s.Q.GetRun(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if PecOn(r.PecOn) != nil {
		t.Error("pec_on should be NULL")
	}
	if r.SessionIndex != 2 || r.PierSide != "East" || math.Abs(r.Amp1Arcsec-res.Fit.Curve.Fundamental().Amp) > 1e-9 {
		t.Errorf("run %+v", r)
	}
	c, err := ParseHarmonics(r.HarmonicsJson)
	if err != nil || len(c.Harmonics) != 3 || math.Abs(c.Harmonics[0].PhaseDeg-res.Fit.Curve.Harmonics[0].PhaseDeg) > 1e-9 {
		t.Errorf("harmonics round trip: %v %+v", err, c)
	}
	pp, err := ParseParams(r.OptionsJson)
	if err != nil || pp.Harmonics != 3 || pp.ScanMax != 160 {
		t.Errorf("params round trip: %v %+v", err, pp)
	}
	r2, _ := s.Q.GetRun(ctx, id2)
	if v := PecOn(r2.PecOn); v == nil || !*v || r2.Notes != "second" {
		t.Errorf("pec_on/notes round trip %+v", r2)
	}

	list, err := s.Q.ListRuns(ctx, 10)
	if err != nil || len(list) != 2 || list[0].ID != id2 {
		t.Errorf("list %v %d", err, len(list))
	}
	if err := s.Q.DeleteRun(ctx, id); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.Q.ListRuns(ctx, 10); len(list) != 1 {
		t.Errorf("after delete %d", len(list))
	}
	// Re-open applies the schema idempotently.
	s2, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()
}
