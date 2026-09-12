// Package pe measures periodic error: it segments a guide-star error series
// at lock-position jumps, scans for the worm period, and fits drift plus a
// harmonic series to the samples in one joint least-squares problem.
//
// Nothing in this package knows about PHD2, TCS tables or files. It works on
// (time, arcsec) samples and returns a Curve of harmonics that the rest of
// the tool shares between measured data and stored PEC tables.
package pe

import (
	"math"
	"sort"
)

// Sample is one guide-star error measurement: T seconds from an arbitrary
// origin (the session start), V the RA error in arcsec with the sign
// convention already applied.
type Sample struct {
	T, V float64
}

// Segment is a half-open range [Start, End) into the kept sample slice.
// Samples within a segment share one unknown offset in the fit.
type Segment struct {
	Start, End int
}

// Len returns the number of samples in the segment.
func (s Segment) Len() int { return s.End - s.Start }

// SegmentOptions controls how the series is cut at breaks.
type SegmentOptions struct {
	// ExcludeAfter drops samples within this many seconds after a break,
	// where the guider is settling on the new lock position.
	ExcludeAfter float64
	// MinSamples drops segments shorter than this; a segment with only a
	// handful of samples cannot constrain its own offset.
	MinSamples int
}

// DefaultSegmentOptions returns the defaults from the brief: 30 s exclusion,
// 10-sample minimum.
func DefaultSegmentOptions() SegmentOptions {
	return SegmentOptions{ExcludeAfter: 30, MinSamples: 10}
}

// Segmentize sorts samples by time, cuts the series at each break (a break at
// time b separates samples with T <= b from those with T > b), drops samples
// inside the post-break exclusion window, drops segments shorter than
// MinSamples, and returns the surviving samples with their segment ranges.
// Adjacent or duplicate breaks never produce empty segments. dropped counts
// every sample removed for either reason.
func Segmentize(s []Sample, breaks []float64, o SegmentOptions) (kept []Sample, segs []Segment, dropped int) {
	sorted := make([]Sample, len(s))
	copy(sorted, s)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].T < sorted[j].T })

	bs := make([]float64, len(breaks))
	copy(bs, breaks)
	sort.Float64s(bs)
	dedup := bs[:0]
	for _, b := range bs {
		if len(dedup) == 0 || b != dedup[len(dedup)-1] {
			dedup = append(dedup, b)
		}
	}
	bs = dedup

	type group struct {
		id      int
		samples []Sample
	}
	var groups []*group
	for _, smp := range sorted {
		// id = number of breaks strictly before T.
		id := sort.Search(len(bs), func(i int) bool { return bs[i] >= smp.T })
		if id > 0 {
			b := bs[id-1]
			if smp.T > b && smp.T <= b+o.ExcludeAfter {
				dropped++
				continue
			}
		}
		if len(groups) == 0 || groups[len(groups)-1].id != id {
			groups = append(groups, &group{id: id})
		}
		g := groups[len(groups)-1]
		g.samples = append(g.samples, smp)
	}

	for _, g := range groups {
		if len(g.samples) < o.MinSamples {
			dropped += len(g.samples)
			continue
		}
		start := len(kept)
		kept = append(kept, g.samples...)
		segs = append(segs, Segment{Start: start, End: len(kept)})
	}
	return kept, segs, dropped
}

// CadenceStats summarises the interval between consecutive samples.
type CadenceStats struct {
	Median, Min, Max, Mean float64
}

// Cadence computes interval statistics within each segment, so the gaps at
// breaks do not inflate the maximum. With nil segs the whole slice is one
// segment. Samples must be sorted by T.
func Cadence(s []Sample, segs []Segment) CadenceStats {
	if segs == nil {
		segs = []Segment{{0, len(s)}}
	}
	var diffs []float64
	for _, sg := range segs {
		for i := sg.Start + 1; i < sg.End; i++ {
			diffs = append(diffs, s[i].T-s[i-1].T)
		}
	}
	if len(diffs) == 0 {
		return CadenceStats{}
	}
	sort.Float64s(diffs)
	sum := 0.0
	for _, d := range diffs {
		sum += d
	}
	return CadenceStats{
		Median: median(diffs),
		Min:    diffs[0],
		Max:    diffs[len(diffs)-1],
		Mean:   sum / float64(len(diffs)),
	}
}

// median of an already sorted slice.
func median(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return math.NaN()
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}
