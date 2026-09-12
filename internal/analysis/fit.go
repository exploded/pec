package analysis

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/exploded/pec/internal/pe"
	"github.com/exploded/pec/internal/tcs"
)

// Phase-reference modes. A table is only ever written with one of them.
const (
	ModeTCS   = "tcs"   // smooth a table the TCS recorded itself
	ModeIndex = "index" // map PHD2 sample times onto the index from an anchor
)

// DefaultAnchorSigma is the timing uncertainty assumed for a typed anchor:
// about a human reaction time.
const DefaultAnchorSigma = 2.0

// Anchor is a PEC index reading at a wall-clock instant.
type Anchor struct {
	ID     int64 // store id for provenance; 0 if none
	Index  int
	At     time.Time
	SigmaS float64 // timing uncertainty, seconds; 0 means DefaultAnchorSigma
	// Period and PeriodSigma come from a watch fit of the index counter
	// (milestone 4); 0 when the anchor was typed.
	Period, PeriodSigma float64
}

func (a Anchor) sigma() float64 {
	if a.SigmaS > 0 {
		return a.SigmaS
	}
	return DefaultAnchorSigma
}

// FitParams are the user's choices for writing a table.
type FitParams struct {
	Harmonics int
	Invert    bool
	// Period > 0 pins the worm period (index mode). 0 uses the anchor's
	// watch period if it has one, else the run's fitted period.
	Period      float64
	PeriodSigma float64 // uncertainty of a pinned period; 0 = unknown
	// ExposureShift moves each PHD2 frame time back by half the exposure,
	// to the instant the centroid represents. Default on.
	ExposureShift bool
	// MeasuredWithPEC records what the mount was doing during the run; true
	// makes the result a residual, not a replacement table, and is flagged.
	MeasuredWithPEC *bool
	Config          tcs.Config
}

// DefaultFitParams returns the defaults.
func DefaultFitParams() FitParams {
	return FitParams{Harmonics: 6, ExposureShift: true, Config: tcs.DefaultConfig()}
}

// Validate checks ranges.
func (p FitParams) Validate() error {
	switch {
	case p.Harmonics < 1 || p.Harmonics > 12:
		return errors.New("harmonics must be between 1 and 12")
	case p.Period < 0 || p.PeriodSigma < 0:
		return errors.New("period and its uncertainty cannot be negative")
	case p.Config.ArcsecPerTick <= 0:
		return errors.New("arcsec per tick must be positive")
	case p.Config.Entries <= 0 || p.Config.Entries%tcs.Ratio != 0:
		return fmt.Errorf("entries must be a positive multiple of %d", tcs.Ratio)
	}
	return nil
}

// PhaseError is the budget for how far the written table's phase may be
// from the mount's, in degrees of the fundamental.
type PhaseError struct {
	AnchorDeg float64 // from the anchor's timing uncertainty
	PeriodDeg float64 // from the period uncertainty, at the sample farthest from the anchor
	TotalDeg  float64 // quadrature sum
	// ResidualFraction is the fraction of the fundamental left behind by a
	// phase error of TotalDeg: 2 sin(TotalDeg/2).
	ResidualFraction float64
	FarS             float64 // largest |t - anchor| over the run, seconds
	PeriodKnown      bool
}

// FitResult is a table ready to paste, with everything needed to judge it.
type FitResult struct {
	Mode   string
	Params FitParams

	Table  *tcs.Table
	Stats  tcs.Stats
	Values []float64 // Correction sampled at every index, arcsec, before rounding

	// Correction is what the table encodes: the curve in arcsec against
	// index phase (index 0 at phase 0). Error is the measured curve it was
	// derived from: in index mode the mount's error, so Correction = -Error
	// (times -1 again if inverted); in tcs mode the smoothed recording,
	// so Correction = Error unless inverted.
	Correction, Error pe.Curve

	Raw          []float64 // tcs mode: the recorded table in arcsec
	NoiseRemoved float64   // tcs mode: RMS of recording minus smoothed curve
	QuantRMS     float64   // RMS error from rounding to whole ticks

	Period, PeriodSigma float64
	PeriodSource        string // "run" | "pinned" | "anchor" | "nominal"
	Anchor              *Anchor
	AnchorOffset        float64 // index mode: anchor instant in the samples' time base
	PhaseOrigin         float64 // index mode: t0 handed to the fitter
	PhaseErr            PhaseError

	Fit      *pe.Result     // index mode: the pinned-period fit in index phase
	Session  *SessionResult // index mode: the run it came from
	Warnings []string
	Level    string // "good" | "warn" | "bad"
}

func (r *FitResult) warn(level, msg string) {
	r.Warnings = append(r.Warnings, msg)
	if level == "bad" || (level == "warn" && r.Level != "bad") {
		r.Level = level
	}
}

// FitTable smooths a table the TCS recorded itself. The recording is
// already in the mount's phase and units, so no external phase reference is
// involved. The assumption, to be confirmed on the sky, is that the TCS
// records a correction with the same sign convention as the table it plays
// back; Invert is there for when it does not.
func FitTable(t *tcs.Table, p FitParams) (*FitResult, error) {
	p.Config.Entries = len(t.Values)
	if err := p.Validate(); err != nil {
		return nil, err
	}
	cfg := p.Config
	raw := t.Arcsec(cfg)
	smoothed := pe.DFT(raw, p.Harmonics)
	r := &FitResult{Mode: ModeTCS, Params: p, Error: smoothed, Raw: raw, Level: "good", PeriodSource: "nominal", Period: p.Period}
	r.Correction = smoothed
	if p.Invert {
		r.Correction = smoothed.Scale(-1)
	}
	r.finish(cfg)
	model := smoothed.Sample(len(raw))
	resid := make([]float64, len(raw))
	for i := range raw {
		resid[i] = raw[i] - model[i]
	}
	r.NoiseRemoved = pe.RMS(resid)
	if r.Stats.P2P == 0 {
		r.warn("bad", "the recorded table is flat: nothing to write")
	}
	return r, nil
}

// IndexInput is what FitIndex needs from a run and an anchor, in the
// samples' own time base (seconds from the session start).
type IndexInput struct {
	Samples []pe.Sample
	Breaks  []float64
	// AnchorOffset is the anchor instant in the samples' time base, already
	// corrected for the exposure midpoint.
	AnchorOffset float64
	Index        int
	AnchorSigma  float64
	Period       float64 // the period to pin; must be > 0
	PeriodSigma  float64 // 0 = unknown
	PeriodSource string
	Fit          pe.FitOptions // harmonics, drift order and segmentation from the run
}

// FitIndex fits the run with the period pinned and the phase origin placed
// where the mount's index passes zero, so the fitted curve is in table
// phase by construction:
//
//	index(t) = (Index + (t - AnchorOffset) N / P) mod N
//	t0 = AnchorOffset - Index P / N          (index(t0) = 0)
//
// The correction is the negated error.
func FitIndex(in IndexInput, p FitParams) (*FitResult, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if in.Period <= 0 {
		return nil, errors.New("index mode needs a worm period")
	}
	cfg := p.Config
	if in.Index < 0 || in.Index >= cfg.Entries {
		return nil, fmt.Errorf("anchor index %d is outside 0..%d", in.Index, cfg.Entries-1)
	}
	n := float64(cfg.Entries)
	t0 := in.AnchorOffset - float64(in.Index)*in.Period/n
	o := in.Fit
	o.Period = in.Period
	o.PhaseOrigin = t0
	o.Harmonics = p.Harmonics
	if o.PolyOrder == 0 {
		o.PolyOrder = 1
	}
	if o.Segments.MinSamples == 0 {
		o.Segments = pe.DefaultSegmentOptions()
	}
	fit, err := pe.Fit(in.Samples, in.Breaks, o)
	if err != nil {
		return nil, err
	}
	r := &FitResult{
		Mode: ModeIndex, Params: p, Fit: fit, Error: fit.Curve, Level: "good",
		Period: in.Period, PeriodSigma: in.PeriodSigma, PeriodSource: in.PeriodSource,
		AnchorOffset: in.AnchorOffset, PhaseOrigin: t0,
		Anchor: &Anchor{Index: in.Index, SigmaS: in.AnchorSigma},
	}
	r.Correction = fit.Curve.Scale(-1)
	if p.Invert {
		r.Correction = r.Correction.Scale(-1)
	}
	r.finish(cfg)
	for _, w := range fit.Warnings {
		r.warn("warn", w)
	}

	// Phase-error budget.
	b := &r.PhaseErr
	b.AnchorDeg = 360 * r.Anchor.sigma() / in.Period
	for _, s := range fit.Kept {
		b.FarS = math.Max(b.FarS, math.Abs(s.T-in.AnchorOffset))
	}
	b.PeriodKnown = in.PeriodSigma > 0 && !math.IsInf(in.PeriodSigma, 0)
	if b.PeriodKnown {
		b.PeriodDeg = 360 * b.FarS / in.Period * (in.PeriodSigma / in.Period)
	}
	b.TotalDeg = math.Hypot(b.AnchorDeg, b.PeriodDeg)
	b.ResidualFraction = 2 * math.Sin(b.TotalDeg*math.Pi/360)
	switch {
	case !b.PeriodKnown:
		r.warn("warn", "the period's uncertainty is unknown, so the phase-error budget covers the anchor only; pin a period with a known uncertainty or use a watch anchor")
	case b.PeriodDeg > 10:
		r.warn("warn", fmt.Sprintf("period uncertainty ±%.2f s puts the phase ±%.0f° out at the sample farthest from the anchor (%.0f min away); measure the period from a longer run or a watch anchor", in.PeriodSigma, b.PeriodDeg, b.FarS/60))
	}
	if b.FarS > 3600 {
		r.warn("warn", fmt.Sprintf("the anchor is %.0f min from part of the run; tracking must have run uninterrupted the whole time for the index mapping to hold", b.FarS/60))
	}
	if b.TotalDeg > 20 {
		r.warn("bad", fmt.Sprintf("phase-error budget ±%.0f° would leave %.0f%% of the fundamental behind; this table is not worth pasting", b.TotalDeg, 100*b.ResidualFraction))
	}
	h1 := fit.Curve.Fundamental()
	if h1.AmpSigma > 0 && h1.Amp < 3*h1.AmpSigma {
		r.warn("bad", "the fundamental is not detected above noise; the table would be noise")
	}
	return r, nil
}

// FitSession is FitIndex for a stored run: samples and options come from
// the run, the anchor offset from the anchor and the session start, and the
// period from the parameters, the anchor or the run in that order.
func FitSession(res *SessionResult, a Anchor, p FitParams) (*FitResult, error) {
	s := res.Session
	samples, _ := sessionSamples(s, res.Params)
	in := IndexInput{
		Samples: samples, Breaks: s.Breaks(), Index: a.Index, AnchorSigma: a.sigma(),
		AnchorOffset: a.At.Sub(s.Begins).Seconds(),
		Fit:          res.Params.fitOptions(),
	}
	if p.ExposureShift {
		// The centroid represents the exposure midpoint, half an exposure
		// before PHD2's frame timestamp; the anchor moves later by as much
		// in the frame time base.
		in.AnchorOffset += float64(s.ExposureMS) / 2000
	}
	switch {
	case p.Period > 0:
		in.Period, in.PeriodSigma, in.PeriodSource = p.Period, p.PeriodSigma, "pinned"
	case a.Period > 0:
		in.Period, in.PeriodSigma, in.PeriodSource = a.Period, a.PeriodSigma, "anchor"
	default:
		in.Period, in.PeriodSource = res.Fit.Period, "run"
		if s := res.Fit.PeriodSigma; !res.Fit.PeriodFixed && !math.IsInf(s, 0) && !math.IsNaN(s) {
			in.PeriodSigma = s
		}
	}
	r, err := FitIndex(in, p)
	if err != nil {
		return nil, err
	}
	r.Session = res
	r.Anchor = &a
	if res.Guiding.Corrections > 0 && !res.Guiding.IsGA {
		r.warn("bad", fmt.Sprintf("this run was guided (%d RA pulses): PHD2 was cancelling the periodic error, so the curve is not the mount's error; fit from a Guiding Assistant run", res.Guiding.Corrections))
	}
	switch {
	case p.MeasuredWithPEC == nil:
		r.warn("warn", "whether PEC was on during this run is not recorded; if it was, this table corrects only the residual")
	case *p.MeasuredWithPEC:
		r.warn("bad", "this run was made with PEC on, so the curve is the residual after the stored table; this output corrects that residual only and must not replace the stored table")
	}
	return r, nil
}

// finish samples the correction, rounds it and fills the table statistics.
func (r *FitResult) finish(cfg tcs.Config) {
	r.Values = r.Correction.Sample(cfg.Entries)
	r.Table, r.QuantRMS = tcs.FromArcsec(r.Values, cfg)
	r.Stats = r.Table.Stats()
}

// IndexOf maps a sample time to the table index under the anchor mapping.
func (r *FitResult) IndexOf(t float64) float64 {
	n := float64(r.Params.Config.Entries)
	idx := math.Mod((t-r.PhaseOrigin)/r.Period*n, n)
	if idx < 0 {
		idx += n
	}
	return idx
}
