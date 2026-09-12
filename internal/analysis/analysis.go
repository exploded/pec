// Package analysis joins the parsers to the fitter: it turns a PHD2 session
// or a TCS table into a fitted periodic-error result with the provenance
// the UI, reports and store need. It holds the policy decisions (which
// rows count, what to warn about) so the web layer stays thin.
package analysis

import (
	"errors"
	"fmt"
	"time"

	"github.com/exploded/pec/internal/pe"
	"github.com/exploded/pec/internal/phd2"
	"github.com/exploded/pec/internal/tcs"
)

// Params are the user-settable analysis options.
type Params struct {
	Harmonics    int
	Period       float64 // > 0 pins the period; 0 scans
	ScanMin      float64
	ScanMax      float64
	ScanStep     float64
	PolyOrder    int
	ExcludeAfter float64 // seconds excluded after a dither
	MinSegment   int
	RASign       float64
	PhaseOrigin  float64 // seconds from session start; 0 unless fitting in index phase
}

// DefaultParams returns the brief's defaults.
func DefaultParams() Params {
	scan := pe.DefaultPeriodScan()
	seg := pe.DefaultSegmentOptions()
	return Params{
		Harmonics: 6, ScanMin: scan.Min, ScanMax: scan.Max, ScanStep: scan.Step,
		PolyOrder: 1, ExcludeAfter: seg.ExcludeAfter, MinSegment: seg.MinSamples,
		RASign: phd2.RASign,
	}
}

// Validate checks ranges and returns a user-facing error.
func (p Params) Validate() error {
	switch {
	case p.Harmonics < 1 || p.Harmonics > 12:
		return errors.New("harmonics must be between 1 and 12")
	case p.Period < 0:
		return errors.New("period cannot be negative")
	case p.Period == 0 && (p.ScanMin <= 0 || p.ScanMax <= p.ScanMin):
		return errors.New("period scan range must have max greater than min")
	case p.Period == 0 && (p.ScanStep <= 0 || p.ScanStep > (p.ScanMax-p.ScanMin)/4):
		return errors.New("period scan step must be positive and give at least five trial periods")
	case p.PolyOrder != 1 && p.PolyOrder != 2:
		return errors.New("drift polynomial order must be 1 or 2")
	case p.ExcludeAfter < 0:
		return errors.New("dither exclusion cannot be negative")
	case p.MinSegment < 3:
		return errors.New("minimum segment length must be at least 3 samples")
	case p.RASign != 1 && p.RASign != -1:
		return errors.New("RA sign must be +1 or -1")
	}
	return nil
}

func (p Params) fitOptions() pe.FitOptions {
	return pe.FitOptions{
		Period:      p.Period,
		Scan:        pe.PeriodScan{Min: p.ScanMin, Max: p.ScanMax, Step: p.ScanStep},
		Harmonics:   p.Harmonics,
		PolyOrder:   p.PolyOrder,
		PhaseOrigin: p.PhaseOrigin,
		Segments:    pe.SegmentOptions{ExcludeAfter: p.ExcludeAfter, MinSamples: p.MinSegment},
	}
}

// SessionResult is a fitted PHD2 session.
type SessionResult struct {
	Session  *phd2.Session
	Params   Params
	Fit      *pe.Result
	Used     int      // samples handed to the fitter
	Guiding  phd2.GuidingState
	Warnings []string // policy warnings (guiding active, skipped rows) followed by fit warnings
}

// Session fits one session.
func Session(s *phd2.Session, p Params) (*SessionResult, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if s.PixelScale <= 0 {
		return nil, errors.New("session header has no pixel scale")
	}
	rows, warnings := s.MeasurementSamples()
	if len(rows) < 10 {
		return nil, fmt.Errorf("only %d usable samples in this session", len(rows))
	}
	samples := make([]pe.Sample, len(rows))
	for i, r := range rows {
		samples[i] = pe.Sample{T: r.Offset, V: p.RASign * r.RARaw * s.PixelScale}
	}
	fit, err := pe.Fit(samples, s.Breaks(), p.fitOptions())
	if err != nil {
		return nil, err
	}
	res := &SessionResult{
		Session: s, Params: p, Fit: fit, Used: len(rows), Guiding: s.Guiding(),
		Warnings: append(warnings, fit.Warnings...),
	}
	return res, nil
}

// TableResult is the harmonic breakdown of a stored PEC table.
type TableResult struct {
	Table     *tcs.Table
	Config    tcs.Config
	Stats     tcs.Stats
	Curve     pe.Curve  // arcsec
	Arcsec    []float64 // the table converted
	Model     []float64 // the K-harmonic reconstruction, arcsec
	ResidRMS  float64   // arcsec left after removing the harmonics
	Harmonics int
}

// Table analyses a stored table.
func Table(t *tcs.Table, cfg tcs.Config, harmonics int) (*TableResult, error) {
	if harmonics < 1 || harmonics > 12 {
		return nil, errors.New("harmonics must be between 1 and 12")
	}
	if cfg.ArcsecPerTick <= 0 {
		return nil, errors.New("arcsec per tick must be positive")
	}
	arc := t.Arcsec(cfg)
	c := pe.DFT(arc, harmonics)
	model := c.Sample(len(arc))
	resid := make([]float64, len(arc))
	for i := range arc {
		resid[i] = arc[i] - model[i]
	}
	return &TableResult{
		Table: t, Config: cfg, Stats: t.Stats(), Curve: c, Arcsec: arc, Model: model,
		ResidRMS: pe.RMS(resid), Harmonics: harmonics,
	}, nil
}

// SessionSummary is one row of the session picker.
type SessionSummary struct {
	Index      int
	Begins     time.Time
	Duration   time.Duration
	Samples    int
	Span       float64 // s
	Cadence    float64 // s, median
	RAHours    float64
	DecDeg     float64
	AltDeg     float64
	PierSide   string
	Guiding    phd2.GuidingState
	Dithers    int
	Drops      int
	Note       string // "Guiding Assistant", "guiding off after frame N", ...
	Usable     bool
	UsableRows int
}

// Summaries describes every session in a log for the picker.
func Summaries(l *phd2.Log) []SessionSummary {
	out := make([]SessionSummary, 0, len(l.Sessions))
	for _, s := range l.Sessions {
		g := s.Guiding()
		med, _, _ := s.Cadence()
		rows, _ := s.MeasurementSamples()
		sm := SessionSummary{
			Index: s.Index, Begins: s.Begins, Duration: s.Duration(), Samples: len(s.Samples),
			Span: s.Span(), Cadence: med, RAHours: s.RAHours, DecDeg: s.DecDeg, AltDeg: s.AltDeg,
			PierSide: s.PierSide, Guiding: g, Dithers: s.Dithers(), Drops: s.Drops(),
			UsableRows: len(rows), Usable: len(rows) >= 10,
		}
		switch {
		case g.IsGA:
			sm.Note = "Guiding Assistant run"
		case g.DisabledAfter >= 0:
			sm.Note = fmt.Sprintf("guiding off after frame %d", g.DisabledAfter+1)
		case g.Corrections > 0:
			sm.Note = "guided"
		default:
			sm.Note = "no corrections sent"
		}
		out = append(out, sm)
	}
	return out
}
