package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/exploded/pec/internal/analysis"
	"github.com/exploded/pec/internal/pe"
	"github.com/exploded/pec/internal/store/db"
)

// SaveMeta is what the caller knows that the analysis does not.
type SaveMeta struct {
	FileSHA    string
	SourceName string
	Version    string
	Notes      string
	PecOn      *bool // nil = unknown
}

// EnsureFile records an uploaded file (idempotent).
func (s *Store) EnsureFile(ctx context.Context, sha, kind, name string, size int64) error {
	return s.Q.UpsertFile(ctx, db.UpsertFileParams{
		Sha256: sha, Kind: kind, Name: name, Size: size, UploadedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

// SaveSession stores a fitted guide-log session and returns its id.
func (s *Store) SaveSession(ctx context.Context, res *analysis.SessionResult, m SaveMeta) (int64, error) {
	f := res.Fit
	sess := res.Session
	h1 := f.Curve.Fundamental()
	sigma := f.PeriodSigma
	if f.PeriodFixed || sigma != sigma || sigma > 1e6 { // NaN or Inf
		sigma = 0
	}
	guiding := int64(0)
	if res.Guiding.Corrections > 0 && !res.Guiding.IsGA {
		guiding = 1
	}
	p := db.InsertRunParams{
		CreatedAt: time.Now().UTC().Format(time.RFC3339), Kind: "analyse",
		FileSha256: m.FileSHA, SourceName: m.SourceName,
		SessionIndex: int64(sess.Index), SessionBegins: sess.Begins.Format(time.RFC3339),
		Equipment: sess.Profile,
		RaHours:   sess.RAHours, DecDeg: sess.DecDeg, HourAngle: sess.HourAngle, AltDeg: sess.AltDeg, PierSide: sess.PierSide,
		PixelScale: sess.PixelScale, ExposureMs: int64(sess.ExposureMS),
		SampleCount: int64(f.N), CadenceS: f.Cadence.Median, SpanS: f.Span, Cycles: f.Cycles, DriftArcsecMin: f.Drift,
		PeriodS: f.Period, PeriodSigmaS: sigma, PeriodFixed: b2i(f.PeriodFixed),
		HarmonicsJson: HarmonicsJSON(f.Curve), Amp1Arcsec: h1.Amp, Phase1Deg: h1.PhaseDeg,
		PeriodicRms: f.ModelRMS, ResidualRms: f.ResidualRMS, PeakToPeak: f.PeakToPeak,
		GuidingActive: guiding, PecOn: nullBool(m.PecOn), RaSign: res.Params.RASign,
		OptionsJson: mustJSON(res.Params), WarningsJson: mustJSON(res.Warnings),
		ToolVersion: m.Version, Notes: m.Notes,
	}
	r, err := s.Q.InsertRun(ctx, p)
	if err != nil {
		return 0, fmt.Errorf("saving run: %w", err)
	}
	return r.LastInsertId()
}

// SaveTable stores a decomposed TCS table as a run of kind "table".
func (s *Store) SaveTable(ctx context.Context, res *analysis.TableResult, m SaveMeta) (int64, error) {
	h1 := res.Curve.Fundamental()
	cfg := res.Config
	p := db.InsertRunParams{
		CreatedAt: time.Now().UTC().Format(time.RFC3339), Kind: "table",
		FileSha256: m.FileSHA, SourceName: m.SourceName,
		SampleCount: int64(res.Stats.Entries), PeriodS: 0, PeriodFixed: 1,
		HarmonicsJson: HarmonicsJSON(res.Curve), Amp1Arcsec: h1.Amp, Phase1Deg: h1.PhaseDeg,
		PeriodicRms: res.Curve.RMS(), ResidualRms: res.ResidRMS, PeakToPeak: float64(res.Stats.P2P) * cfg.ArcsecPerTick,
		PecOn: nullBool(m.PecOn), RaSign: 1,
		OptionsJson:  mustJSON(map[string]any{"harmonics": res.Harmonics, "arcsec_per_tick": cfg.ArcsecPerTick}),
		WarningsJson: "[]", ToolVersion: m.Version, Notes: m.Notes,
	}
	r, err := s.Q.InsertRun(ctx, p)
	if err != nil {
		return 0, fmt.Errorf("saving table run: %w", err)
	}
	return r.LastInsertId()
}

// SaveAnchor stores a PEC index reading and returns its id.
func (s *Store) SaveAnchor(ctx context.Context, a analysis.Anchor, source, note string) (int64, error) {
	if source == "" {
		source = "typed"
	}
	sigma := a.SigmaS
	if sigma <= 0 {
		sigma = analysis.DefaultAnchorSigma
	}
	r, err := s.Q.InsertAnchor(ctx, db.InsertAnchorParams{
		CreatedAt: time.Now().UTC().Format(time.RFC3339), PecIndex: int64(a.Index),
		At: a.At.Format(time.RFC3339Nano), SigmaS: sigma, Source: source,
		PeriodS: a.Period, PeriodSigmaS: a.PeriodSigma, Readings: 1, Note: note,
	})
	if err != nil {
		return 0, fmt.Errorf("saving anchor: %w", err)
	}
	return r.LastInsertId()
}

// AnchorFromRow converts a stored anchor back to the analysis type.
func AnchorFromRow(r db.Anchor) (analysis.Anchor, error) {
	at, err := time.Parse(time.RFC3339Nano, r.At)
	if err != nil {
		return analysis.Anchor{}, fmt.Errorf("anchor %d: bad time %q", r.ID, r.At)
	}
	return analysis.Anchor{
		ID: r.ID, Index: int(r.PecIndex), At: at, SigmaS: r.SigmaS,
		Period: r.PeriodS, PeriodSigma: r.PeriodSigmaS,
	}, nil
}

// FitSaveMeta is what the caller knows about a fit that the fit does not.
type FitSaveMeta struct {
	FileSHA    string
	SourceName string
	PhaseRef   string // human-readable
	Version    string
	Notes      string
	RunID      int64 // 0 = none
	AnchorID   int64 // 0 = none
}

// SaveFit stores a generated table with its provenance and returns its id.
func (s *Store) SaveFit(ctx context.Context, r *analysis.FitResult, meta analysis.Meta, m FitSaveMeta) (int64, error) {
	h1 := r.Correction.Fundamental()
	res, err := s.Q.InsertFit(ctx, db.InsertFitParams{
		CreatedAt: time.Now().UTC().Format(time.RFC3339), Mode: r.Mode,
		RunID: nullID(m.RunID), AnchorID: nullID(m.AnchorID),
		FileSha256: m.FileSHA, SourceName: m.SourceName, PhaseRef: m.PhaseRef,
		PeriodS: r.Period, Harmonics: int64(r.Params.Harmonics), Inverted: b2i(r.Params.Invert),
		Amp1Arcsec: h1.Amp, P2pTicks: int64(r.Stats.P2P), QuantRms: r.QuantRMS, PhaseErrDeg: r.PhaseErr.TotalDeg,
		TableText: r.Table.String(), MetaJson: mustJSON(meta), WarningsJson: mustJSON(r.Warnings),
		ToolVersion: m.Version, Notes: m.Notes,
	})
	if err != nil {
		return 0, fmt.Errorf("saving fit: %w", err)
	}
	return res.LastInsertId()
}

func nullID(id int64) sql.NullInt64 {
	if id == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: id, Valid: true}
}

// HarmonicsJSON encodes a curve for the harmonics_json column.
func HarmonicsJSON(c pe.Curve) string {
	type h struct {
		K        int     `json:"k"`
		A        float64 `json:"a"`
		B        float64 `json:"b"`
		Amp      float64 `json:"amp"`
		PhaseDeg float64 `json:"phase_deg"`
		Sigma    float64 `json:"sigma"`
	}
	out := make([]h, len(c.Harmonics))
	for i, x := range c.Harmonics {
		out[i] = h{x.K, x.A, x.B, x.Amp, x.PhaseDeg, x.AmpSigma}
	}
	return mustJSON(out)
}

// ParseHarmonics decodes harmonics_json.
func ParseHarmonics(s string) (pe.Curve, error) {
	var in []struct {
		K     int     `json:"k"`
		A     float64 `json:"a"`
		B     float64 `json:"b"`
		Sigma float64 `json:"sigma"`
	}
	if err := json.Unmarshal([]byte(s), &in); err != nil {
		return pe.Curve{}, fmt.Errorf("harmonics json: %w", err)
	}
	c := pe.Curve{Harmonics: make([]pe.Harmonic, len(in))}
	for i, h := range in {
		c.Harmonics[i] = pe.NewHarmonic(h.K, h.A, h.B, h.Sigma, h.Sigma)
	}
	return c, nil
}

// ParseParams decodes options_json for an analyse run.
func ParseParams(s string) (analysis.Params, error) {
	p := analysis.DefaultParams()
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return p, fmt.Errorf("options json: %w", err)
	}
	return p, nil
}

// PecOn decodes the nullable column.
func PecOn(v sql.NullInt64) *bool {
	if !v.Valid {
		return nil
	}
	b := v.Int64 != 0
	return &b
}

func nullBool(b *bool) sql.NullInt64 {
	if b == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: b2i(*b), Valid: true}
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}
