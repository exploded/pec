package mks

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// StatusTracking is the status-word bit that was set while the mount was
// tracking in the reference capture (0x1200 tracking, 0x0300 slewing,
// 0x0200 idle). Inferred, not documented.
const StatusTracking = 0x1000

// CountsPerIndex is the encoder counts per PEC index step: the encoder
// advanced 19,976 counts per 149.59 s worm turn, which is 16 per step of a
// 1250-entry table. Inferred from the reference capture.
const CountsPerIndex = 16

// SiderealDay is the sidereal day in seconds, for the tooth-count check.
const SiderealDay = 86164.0905

// Options for Analyse.
type Options struct {
	Entries int     // PEC table entries (1250)
	Settle  float64 // seconds after tracking starts to ignore (default 3)
}

// Result is what a capture says about the worm: its period from the
// encoder rate, and the PEC index at a known instant.
type Result struct {
	Start, End time.Time
	Span       float64 // s
	Frames     int
	Junk       int
	Unpaired   int

	StatusChanges []Reading // every change of the HA status word
	TrackFrom     time.Time // tracking interval used for the fit
	TrackTo       time.Time

	EncoderReadings int
	EncoderRate     float64 // counts/s
	EncoderRateSig  float64
	EncoderRMS      float64 // fit residual, counts
	EncoderOutliers int
	fitT0           time.Time
	fitA0           float64 // fitted encoder value at fitT0

	CountsPerTurn float64
	Period        float64 // s
	PeriodSigma   float64
	TeethEstimate float64 // SiderealDay / Period; 576 for a Paramount ME at sidereal rate

	IndexReadings int
	IndexOffset   float64 // steps: index = (encoder/16 + IndexOffset) mod Entries
	IndexSpread   float64 // largest deviation of a reading from that relation, steps
	HasAnchor     bool
	AnchorIndex   int
	AnchorAt      time.Time
	AnchorSigma   float64 // s

	Notes    []string // informational, do not affect Level
	Warnings []string
	Level    string // good | warn | bad
}

func (r *Result) warn(level, msg string) {
	r.Warnings = append(r.Warnings, msg)
	if level == "bad" || (level == "warn" && r.Level != "bad") {
		r.Level = level
	}
}

// EncoderAt evaluates the fitted encoder line.
func (r *Result) EncoderAt(t time.Time) float64 {
	return r.fitA0 + r.EncoderRate*t.Sub(r.fitT0).Seconds()
}

// IndexAt evaluates the index relation at t (fractional steps).
func (r *Result) IndexAt(t time.Time) float64 {
	n := r.CountsPerTurn / CountsPerIndex
	x := math.Mod(r.EncoderAt(t)/CountsPerIndex+r.IndexOffset, n)
	if x < 0 {
		x += n
	}
	return x
}

// Analyse fits the encoder rate over the tracking interval and ties the
// PEC index readings to it.
func Analyse(c *Capture, o Options) (*Result, error) {
	if o.Entries <= 0 {
		o.Entries = 1250
	}
	if o.Settle <= 0 {
		o.Settle = 3
	}
	r := &Result{Start: c.Start, End: c.End, Span: c.End.Sub(c.Start).Seconds(), Frames: c.Frames, Junk: c.Junk, Unpaired: c.Unpaired,
		CountsPerTurn: CountsPerIndex * float64(o.Entries), Level: "good"}

	// Tracking interval: the longest stretch with the tracking bit set.
	st := c.Status()
	var last int64 = -1
	for _, s := range st {
		if s.Value != last {
			r.StatusChanges = append(r.StatusChanges, s)
			last = s.Value
		}
	}
	var bestFrom, bestTo time.Time
	for i, s := range r.StatusChanges {
		if s.Value&StatusTracking == 0 {
			continue
		}
		to := c.End
		if i+1 < len(r.StatusChanges) {
			to = r.StatusChanges[i+1].At
		}
		if to.Sub(s.At) > bestTo.Sub(bestFrom) {
			bestFrom, bestTo = s.At, to
		}
	}
	switch {
	case len(st) == 0:
		r.warn("warn", "no status readings; assuming the mount was tracking for the whole capture")
		bestFrom, bestTo = c.Start, c.End
	case bestFrom.IsZero():
		return r, fmt.Errorf("the mount was never tracking during this capture (status %s)", statusWords(r.StatusChanges))
	}
	if n := len(r.StatusChanges); n > 1 {
		if bestFrom.After(c.Start.Add(time.Second)) {
			r.Notes = append(r.Notes, fmt.Sprintf("tracking started %.0f s into the capture (homing or a slew before it); the %.0f s of tracking are used.", bestFrom.Sub(c.Start).Seconds(), bestTo.Sub(bestFrom).Seconds()))
		}
		if bestTo.Before(c.End.Add(-time.Second)) {
			r.warn("warn", fmt.Sprintf("tracking stopped %.0f s before the end of the capture; an anchor is only valid while tracking continues", c.End.Sub(bestTo).Seconds()))
		}
	}
	r.TrackFrom, r.TrackTo = bestFrom, bestTo
	from := bestFrom.Add(time.Duration(o.Settle * float64(time.Second)))

	// Encoder fit.
	var enc []Reading
	for _, e := range c.Encoder() {
		if !e.At.Before(from) && !e.At.After(bestTo) {
			enc = append(enc, e)
		}
	}
	if len(enc) < 10 {
		return r, fmt.Errorf("only %d encoder readings while tracking; need a longer capture", len(enc))
	}
	r.fitT0 = enc[0].At
	fit := func(rs []Reading) (a0, rate, sig, rms float64) {
		n := float64(len(rs))
		var st, sv, stt, stv float64
		for _, e := range rs {
			t := e.At.Sub(r.fitT0).Seconds()
			st += t
			sv += float64(e.Value)
			stt += t * t
			stv += t * float64(e.Value)
		}
		d := n*stt - st*st
		rate = (n*stv - st*sv) / d
		a0 = (sv - rate*st) / n
		var ss float64
		for _, e := range rs {
			t := e.At.Sub(r.fitT0).Seconds()
			res := float64(e.Value) - (a0 + rate*t)
			ss += res * res
		}
		rms = math.Sqrt(ss / math.Max(n-2, 1))
		sig = rms * math.Sqrt(n/d)
		return
	}
	a0, rate, sig, rms := fit(enc)
	// One pass of outlier rejection at 50 counts (the reference capture's
	// worst residual was 13).
	kept := enc[:0:0]
	for _, e := range enc {
		if math.Abs(float64(e.Value)-(a0+rate*e.At.Sub(r.fitT0).Seconds())) <= 50 {
			kept = append(kept, e)
		}
	}
	r.EncoderOutliers = len(enc) - len(kept)
	if len(kept) >= 10 {
		a0, rate, sig, rms = fit(kept)
		enc = kept
	}
	r.EncoderReadings = len(enc)
	r.fitA0, r.EncoderRate, r.EncoderRateSig, r.EncoderRMS = a0, rate, sig, rms
	if rate <= 0 {
		return r, fmt.Errorf("the HA encoder is not advancing (%.2f counts/s); was the mount tracking?", rate)
	}
	r.Period = r.CountsPerTurn / rate
	r.PeriodSigma = r.Period * sig / rate
	r.TeethEstimate = SiderealDay / r.Period
	if span := enc[len(enc)-1].At.Sub(enc[0].At).Seconds(); span < 120 {
		r.warn("warn", fmt.Sprintf("only %.0f s of tracking; the period is from a short baseline", span))
	}
	if rms > 5 {
		r.warn("warn", fmt.Sprintf("encoder scatter %.1f counts about a straight line; the rate may not have been steady", rms))
	}

	// Index readings against the encoder line.
	n := float64(o.Entries)
	var devs []float64
	for _, ix := range c.Index() {
		if ix.At.Before(from) || ix.At.After(bestTo) {
			continue
		}
		pred := math.Mod(r.EncoderAt(ix.At)/CountsPerIndex, n)
		d := math.Mod(float64(ix.Value)-pred+1.5*n, n) - n/2
		devs = append(devs, d)
	}
	r.IndexReadings = len(devs)
	if len(devs) == 0 {
		r.warn("bad", "no PEC index readings: keep the Bisque TCS window on its Periodic Error Correction tab during the capture so TheSkyX polls the index")
		return r, nil
	}
	sorted := append([]float64(nil), devs...)
	sort.Float64s(sorted)
	r.IndexOffset = sorted[len(sorted)/2]
	for _, d := range devs {
		r.IndexSpread = math.Max(r.IndexSpread, math.Abs(d-r.IndexOffset))
	}
	if r.IndexSpread > 2 {
		r.warn("bad", fmt.Sprintf("index readings disagree with the encoder by up to %.1f steps; the 16-counts-per-step relation may not hold", r.IndexSpread))
		return r, nil
	}
	if len(devs) < 5 {
		r.warn("warn", fmt.Sprintf("only %d index readings; the anchor rests on few samples", len(devs)))
	}

	// Anchor at the last index reading, moved to the instant the relation
	// gives a whole index step.
	at := c.Index()[len(c.Index())-1].At
	if at.After(bestTo) {
		at = bestTo
	}
	idx := r.IndexAt(at)
	k := math.Round(idx)
	at = at.Add(time.Duration((k - idx) * CountsPerIndex / rate * float64(time.Second)))
	r.AnchorIndex = int(math.Mod(k+n, n))
	r.AnchorAt = at
	r.HasAnchor = true
	r.AnchorSigma = math.Max(0.05, math.Hypot(r.IndexSpread*CountsPerIndex/rate, rms/rate))
	return r, nil
}

func statusWords(ch []Reading) string {
	s := ""
	for i, c := range ch {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("%d", c.Value)
	}
	return s
}
