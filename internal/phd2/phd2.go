// Package phd2 parses PHD2 guide logs into sessions of guide-star samples
// and events. It knows nothing about periodic error; it only turns the log
// text into numbers with their timing and the header metadata.
package phd2

import (
	"math"
	"sort"
	"time"
)

// RASign is the single home of the PHD2 RA sign convention. The tool
// defines the RA error as
//
//	err_arcsec = sign * RARawDistance_px * PixelScale
//
// with sign defaulting to RASign. Whether a positive RARawDistance is east
// or west is not documented, so +1 is an assumption that the analysis form
// can override. No other sign for this quantity exists in the code base.
const RASign = 1.0

// Log is one PHD2 guide log file.
type Log struct {
	Path         string
	Versions     []string // each "PHD2 version ..." preamble line, in order
	Calibrations int      // calibration blocks seen and skipped
	Sessions     []*Session
}

// Session is one "Guiding Begins" ... "Guiding Ends" block.
type Session struct {
	Index  int       // 1-based position in the file
	Begins time.Time // parsed in the caller's location
	Ends   time.Time // zero if the file ended without "Guiding Ends"

	Profile     string
	Camera      string
	Mount       string
	PixelScale  float64 // arcsec/px
	Binning     int
	FocalLength float64 // mm
	ExposureMS  int

	RAHours, DecDeg, HourAngle, AltDeg, AzDeg float64
	PierSide                                  string

	Header  map[string]string // every "k = v" pair in the header block, first wins
	Samples []Sample          // valid rows only; DROP rows become EventDrop
	Events  []Event

	// Live marks a session recorded from PHD2's event server rather than
	// parsed from a guide log. The stream carries no "GA Result" lines, so
	// guide output being off is the whole evidence of a Guiding Assistant run.
	Live bool
}

// Sample is one guide frame.
type Sample struct {
	Frame  int
	Offset float64   // PHD2 "Time": seconds since Begins
	At     time.Time // Begins + Offset

	Dx, Dy            float64 // px
	RARaw, DecRaw     float64 // px, before any correction
	RAGuide, DecGuide float64 // px, after the guide algorithm

	RADuration, DecDuration   int // ms; non-zero means PHD2 pulsed the mount
	RADirection, DecDirection string

	StarMass, SNR float64
	ErrorCode     int
}

// EventKind classifies INFO lines and dropped frames.
type EventKind int

const (
	EventOther         EventKind = iota // any other INFO line, kept verbatim
	EventDither                         // "INFO: DITHER by dx, dy, new lock pos = X, Y"
	EventSetLockPos                     // "INFO: SET LOCK POSITION, new lock pos = X, Y"
	EventSettlingStart                  // "INFO: SETTLING STATE CHANGE, Settling started"
	EventSettlingDone                   // "INFO: SETTLING STATE CHANGE, Settling complete"
	EventParamChange                    // "INFO: Guiding parameter change, K = V"
	EventGAResult                       // "INFO: GA Result - ..."
	EventDrop                           // a "DROP" data row
)

// String names the kind.
func (k EventKind) String() string {
	switch k {
	case EventDither:
		return "dither"
	case EventSetLockPos:
		return "set lock position"
	case EventSettlingStart:
		return "settling started"
	case EventSettlingDone:
		return "settling complete"
	case EventParamChange:
		return "parameter change"
	case EventGAResult:
		return "guiding assistant result"
	case EventDrop:
		return "dropped frame"
	}
	return "info"
}

// Event is an INFO line or a dropped frame, positioned in the sample
// sequence.
type Event struct {
	Kind   EventKind
	After  int     // index into Samples of the last sample before this line; -1 if none
	Offset float64 // Samples[After].Offset (0 if After < 0); for EventDrop the row's own Time
	Frame  int     // EventDrop only
	Text   string  // the line with "INFO: " stripped, or the DROP reason

	Key, Value   string  // EventParamChange
	Dx, Dy       float64 // EventDither
	LockX, LockY float64 // EventDither, EventSetLockPos
}

// Session returns the 1-based session n.
func (l *Log) Session(n int) (*Session, error) {
	if n < 1 || n > len(l.Sessions) {
		return nil, &SessionError{N: n, Count: len(l.Sessions)}
	}
	return l.Sessions[n-1], nil
}

// SessionError reports an out-of-range session number.
type SessionError struct{ N, Count int }

func (e *SessionError) Error() string {
	if e.Count == 0 {
		return "log has no guiding sessions"
	}
	return "session " + itoa(e.N) + " out of range 1-" + itoa(e.Count)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}

// Breaks returns the times (sample offsets) at which the lock position
// moved: dithers and explicit lock-position changes. A SET LOCK POSITION
// and DITHER pair at the same point yields one break.
func (s *Session) Breaks() []float64 {
	var out []float64
	for _, e := range s.Events {
		if e.Kind != EventDither && e.Kind != EventSetLockPos {
			continue
		}
		if len(out) > 0 && out[len(out)-1] == e.Offset {
			continue
		}
		out = append(out, e.Offset)
	}
	sort.Float64s(out)
	return out
}

// Dithers counts dither events.
func (s *Session) Dithers() int {
	n := 0
	for _, e := range s.Events {
		if e.Kind == EventDither {
			n++
		}
	}
	return n
}

// Drops counts dropped frames.
func (s *Session) Drops() int {
	n := 0
	for _, e := range s.Events {
		if e.Kind == EventDrop {
			n++
		}
	}
	return n
}

// RAArcsec converts each sample's raw RA distance to arcsec with the given
// sign (normally RASign).
func (s *Session) RAArcsec(sign float64) []float64 {
	out := make([]float64, len(s.Samples))
	for i, smp := range s.Samples {
		out[i] = sign * smp.RARaw * s.PixelScale
	}
	return out
}

// Cadence returns the median, minimum and maximum interval between
// consecutive samples, in seconds.
func (s *Session) Cadence() (median, min, max float64) {
	if len(s.Samples) < 2 {
		return 0, 0, 0
	}
	diffs := make([]float64, 0, len(s.Samples)-1)
	for i := 1; i < len(s.Samples); i++ {
		diffs = append(diffs, s.Samples[i].Offset-s.Samples[i-1].Offset)
	}
	sort.Float64s(diffs)
	n := len(diffs)
	if n%2 == 1 {
		median = diffs[n/2]
	} else {
		median = (diffs[n/2-1] + diffs[n/2]) / 2
	}
	return median, diffs[0], diffs[n-1]
}

// Span is the time from the first to the last sample, in seconds.
func (s *Session) Span() float64 {
	if len(s.Samples) < 2 {
		return 0
	}
	return s.Samples[len(s.Samples)-1].Offset - s.Samples[0].Offset
}

// Duration is Ends - Begins, or the sample span if Ends is unknown.
func (s *Session) Duration() time.Duration {
	if !s.Ends.IsZero() {
		return s.Ends.Sub(s.Begins)
	}
	return time.Duration(math.Round(s.Span() * float64(time.Second)))
}

// GuidingState describes whether PHD2 was sending corrections.
type GuidingState struct {
	Disabled       bool    // "MountGuidingEnabled = false" was seen at some point
	DisabledAfter  int     // Samples index after which it appeared; -1 when it preceded the first sample (or never, if !Disabled)
	ReenabledAfter int     // index after which it went back to true; -1 never
	Corrections    int     // samples with a non-zero RA duration
	FracZeroRA     float64 // fraction of samples with RADuration == 0
	IsGA           bool    // guide output was off and a GA Result was logged (or the session is Live)
}

// Guiding inspects the events and samples.
func (s *Session) Guiding() GuidingState {
	g := GuidingState{DisabledAfter: -1, ReenabledAfter: -1}
	ga := false
	for _, e := range s.Events {
		switch e.Kind {
		case EventParamChange:
			if e.Key == "MountGuidingEnabled" {
				if e.Value == "false" && !g.Disabled {
					g.Disabled = true
					g.DisabledAfter = e.After
				} else if e.Value == "true" && g.Disabled && g.ReenabledAfter < 0 {
					g.ReenabledAfter = e.After
				}
			}
		case EventGAResult:
			ga = true
		}
	}
	g.IsGA = g.Disabled && (ga || s.Live)
	zero := 0
	for _, smp := range s.Samples {
		if smp.RADuration != 0 {
			g.Corrections++
		} else {
			zero++
		}
	}
	if len(s.Samples) > 0 {
		g.FracZeroRA = float64(zero) / float64(len(s.Samples))
	}
	return g
}

// MeasurementSamples is the one policy function deciding which rows count
// as an unguided periodic-error measurement. If guiding was disabled during
// the session it returns the rows strictly after the disable event (and up
// to the re-enable event); otherwise all rows. The warnings explain what
// was skipped and whether corrections were still being sent.
func (s *Session) MeasurementSamples() (rows []Sample, warnings []string) {
	g := s.Guiding()
	rows = s.Samples
	if g.Disabled {
		end := len(s.Samples)
		if g.ReenabledAfter > g.DisabledAfter {
			end = g.ReenabledAfter + 1
		}
		rows = s.Samples[g.DisabledAfter+1 : end]
		if skipped := g.DisabledAfter + 1; skipped > 0 {
			warnings = append(warnings, itoa(skipped)+" samples before guiding was disabled were skipped")
		}
		if trailing := len(s.Samples) - end; trailing > 0 {
			warnings = append(warnings, itoa(trailing)+" samples after guiding was re-enabled were skipped")
		}
	}
	corr := 0
	for _, r := range rows {
		if r.RADuration != 0 {
			corr++
		}
	}
	if corr > 0 {
		warnings = append(warnings, itoa(corr)+" of "+itoa(len(rows))+" used samples carry RA corrections (PHD2 was guiding, which suppresses the periodic error being measured)")
	}
	return rows, warnings
}
