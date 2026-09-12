package analysis

import (
	"fmt"
	"time"
)

// Meta is the provenance written beside a table as pec_table.meta.json. It
// says where every number came from, so a table found on disk a year later
// can be trusted or discarded.
type Meta struct {
	Tool      string    `json:"tool"`
	Version   string    `json:"version"`
	Generated time.Time `json:"generated"`
	Mode      string    `json:"mode"` // "tcs" | "index"

	Source   MetaSource     `json:"source"`
	Session  *MetaSession   `json:"session,omitempty"`
	PhaseRef MetaPhaseRef   `json:"phase_ref"`
	Period   MetaPeriod     `json:"period"`
	Curve    []MetaHarmonic `json:"harmonics"` // the correction curve, arcsec, index phase

	RASign   float64 `json:"ra_sign"`
	Inverted bool    `json:"inverted"`
	// Sign spells out the convention the table was written with.
	Sign string `json:"sign"`

	Entries       int       `json:"entries"`
	ArcsecPerTick float64   `json:"arcsec_per_tick"`
	Table         MetaTable `json:"table"`

	QuantisationRMS float64 `json:"quantisation_rms_arcsec"`
	NoiseRemovedRMS float64 `json:"noise_removed_rms_arcsec,omitempty"`

	PhaseError *MetaPhaseError `json:"phase_error,omitempty"`
	Warnings   []string        `json:"warnings"`
	RunID      int64           `json:"run_id,omitempty"`
	AnchorID   int64           `json:"anchor_id,omitempty"`

	// Analysis and MeasuredWithPEC let the fit be rebuilt from the source
	// file alone, after the run it came from has been deleted.
	Analysis        *Params `json:"analysis_params,omitempty"`
	MeasuredWithPEC *bool   `json:"measured_with_pec,omitempty"`
}

// MetaSource is the file the table was derived from.
type MetaSource struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// MetaSession is the guide-log session (index mode).
type MetaSession struct {
	Index      int       `json:"index"`
	Begins     time.Time `json:"begins"`
	ExposureMS int       `json:"exposure_ms"`
	PixelScale float64   `json:"pixel_scale"`
	DecFactor  float64   `json:"dec_factor"` // 1/cos(Dec) applied to sky errors
	Samples    int       `json:"samples_used"`
	Target     string    `json:"target"`
}

// MetaPhaseRef is the phase reference.
type MetaPhaseRef struct {
	Mode   string     `json:"mode"`
	Index  int        `json:"index,omitempty"`
	At     *time.Time `json:"at,omitempty"`
	SigmaS float64    `json:"sigma_s,omitempty"`
	// AnchorOffsetS is the anchor instant in seconds from the session start,
	// after the exposure-midpoint shift.
	AnchorOffsetS float64 `json:"anchor_offset_s,omitempty"`
	ExposureShift bool    `json:"exposure_shift,omitempty"`
}

// MetaPeriod is the worm period the table was fitted at.
type MetaPeriod struct {
	Seconds float64 `json:"seconds"`
	SigmaS  float64 `json:"sigma_s,omitempty"`
	Source  string  `json:"source"` // "run" | "pinned" | "anchor" | "nominal"
}

// MetaHarmonic is one term of the correction curve.
type MetaHarmonic struct {
	K        int     `json:"k"`
	A        float64 `json:"a"`
	B        float64 `json:"b"`
	Amp      float64 `json:"amp"`
	PhaseDeg float64 `json:"phase_deg"`
	SigmaAmp float64 `json:"sigma_amp,omitempty"`
}

// MetaTable summarises the written table in ticks.
type MetaTable struct {
	Min int     `json:"min"`
	Max int     `json:"max"`
	P2P int     `json:"p2p"`
	RMS float64 `json:"rms"`
}

// MetaPhaseError is the phase-error budget (index mode).
type MetaPhaseError struct {
	AnchorDeg        float64 `json:"anchor_deg"`
	PeriodDeg        float64 `json:"period_deg"`
	TotalDeg         float64 `json:"total_deg"`
	ResidualFraction float64 `json:"residual_fraction"`
	FarS             float64 `json:"farthest_sample_s"`
}

// MetaInfo is what the caller knows that the fit does not.
type MetaInfo struct {
	Version    string
	Generated  time.Time
	RunID      int64
	SourceName string
	SourceSHA  string
}

// Meta builds the provenance record.
func (r *FitResult) Meta(info MetaInfo) Meta {
	m := Meta{
		Tool: "pec", Version: info.Version, Generated: info.Generated, Mode: r.Mode,
		Source:   MetaSource{Name: info.SourceName, SHA256: info.SourceSHA},
		PhaseRef: MetaPhaseRef{Mode: r.Mode},
		Period:   MetaPeriod{Seconds: r.Period, SigmaS: r.PeriodSigma, Source: r.PeriodSource},
		RASign:   1, Inverted: r.Params.Invert,
		Entries: r.Params.Config.Entries, ArcsecPerTick: r.Params.Config.ArcsecPerTick,
		Table:           MetaTable{Min: r.Stats.Min, Max: r.Stats.Max, P2P: r.Stats.P2P, RMS: r.Stats.RMS},
		QuantisationRMS: r.QuantRMS, NoiseRemovedRMS: r.NoiseRemoved,
		Warnings: append([]string{}, r.Warnings...),
		RunID:    info.RunID,
	}
	if m.Generated.IsZero() {
		m.Generated = time.Now()
	}
	for _, h := range r.Correction.Harmonics {
		m.Curve = append(m.Curve, MetaHarmonic{K: h.K, A: h.A, B: h.B, Amp: h.Amp, PhaseDeg: h.PhaseDeg, SigmaAmp: h.AmpSigma})
	}
	inv := "not inverted"
	if r.Params.Invert {
		inv = "then negated (inverted)"
	}
	switch r.Mode {
	case ModeTCS:
		m.Sign = "table = smoothed recording, " + inv
	case ModeIndex:
		if r.Session != nil {
			m.RASign = r.Session.Params.RASign
		}
		m.Sign = fmt.Sprintf("table = -(measured error), error = ra_sign(%+.0f) x PHD2 RARawDistance x pixel scale / cos(Dec), %s", m.RASign, inv)
		if r.Anchor != nil {
			m.PhaseRef.Index = r.Anchor.Index
			m.PhaseRef.SigmaS = r.Anchor.sigma()
			m.AnchorID = r.Anchor.ID
			if !r.Anchor.At.IsZero() {
				at := r.Anchor.At
				m.PhaseRef.At = &at
			}
		}
		m.PhaseRef.AnchorOffsetS = r.AnchorOffset
		m.PhaseRef.ExposureShift = r.Params.ExposureShift
		m.PhaseError = &MetaPhaseError{
			AnchorDeg: r.PhaseErr.AnchorDeg, PeriodDeg: r.PhaseErr.PeriodDeg, TotalDeg: r.PhaseErr.TotalDeg,
			ResidualFraction: r.PhaseErr.ResidualFraction, FarS: r.PhaseErr.FarS,
		}
		m.MeasuredWithPEC = r.Params.MeasuredWithPEC
		if r.Session != nil {
			s := r.Session.Session
			ap := r.Session.Params
			m.Analysis = &ap
			m.Session = &MetaSession{
				Index: s.Index, Begins: s.Begins, ExposureMS: s.ExposureMS, PixelScale: s.PixelScale,
				DecFactor: r.Session.DecFactor, Samples: r.Fit.N,
				Target: fmt.Sprintf("RA %.2f h, Dec %.1f deg, HA %.2f h, alt %.1f deg, pier %s", s.RAHours, s.DecDeg, s.HourAngle, s.AltDeg, s.PierSide),
			}
		}
	}
	return m
}
