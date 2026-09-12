package pe

import (
	"fmt"
	"math"
)

// Verdict is the plain-language outcome of comparing a PEC-off run with a
// PEC-on run. It is the most valuable output of the tool, so the categories
// are deliberately few and the thresholds deliberately wide.
type Verdict int

const (
	VerdictInconclusive Verdict = iota // too little signal to say
	VerdictHelping                     // amplitude dropped: the curve is doing its job
	VerdictUnchanged                   // roughly the same: right phase but wrong amplitude, or PEC not actually on
	VerdictWorse                       // grew, but not the doubling signature
	VerdictInverted                    // every harmonic roughly doubled: the table has the wrong sign
	VerdictHalfCycle                   // odd harmonics doubled, even ones cancelled: half a cycle out of phase
)

// String is the short headline.
func (v Verdict) String() string {
	switch v {
	case VerdictHelping:
		return "PEC is helping"
	case VerdictUnchanged:
		return "PEC made no difference"
	case VerdictWorse:
		return "PEC made things worse"
	case VerdictInverted:
		return "PEC is inverted"
	case VerdictHalfCycle:
		return "PEC is half a cycle out of phase"
	}
	return "Inconclusive"
}

// Level maps the verdict to a display class: good, warn, bad, info.
func (v Verdict) Level() string {
	switch v {
	case VerdictHelping:
		return "good"
	case VerdictUnchanged:
		return "warn"
	case VerdictWorse, VerdictInverted, VerdictHalfCycle:
		return "bad"
	}
	return "info"
}

// VerifyResult compares two fits at the same period.
type VerifyResult struct {
	Verdict Verdict
	Detail  string // one or two sentences the user can act on

	AmpBefore, AmpAfter   float64 // fundamental, arcsec
	Ratio, RatioSigma     float64
	Amp2Before, Amp2After float64 // second harmonic, arcsec
	Ratio2                float64

	PeriodicRMSBefore, PeriodicRMSAfter float64
	ResidualRMSBefore, ResidualRMSAfter float64
	PeriodBefore, PeriodAfter           float64 // PeriodAfter is what the after fit used (normally pinned to before)
	PeriodAfterFree                     float64 // after's own scan, for information; 0 if not run

	Reasons []string // caveats
}

// Verify classifies before (PEC off) against after (PEC on). The after fit
// should have been made with the period pinned to before's, so the harmonic
// sets are comparable; afterFree is after's own scanned period (0 to skip
// the disagreement check).
func Verify(before, after *Result, afterFree float64) VerifyResult {
	b1, a1 := before.Curve.Fundamental(), after.Curve.Fundamental()
	v := VerifyResult{
		AmpBefore: b1.Amp, AmpAfter: a1.Amp,
		PeriodicRMSBefore: before.ModelRMS, PeriodicRMSAfter: after.ModelRMS,
		ResidualRMSBefore: before.ResidualRMS, ResidualRMSAfter: after.ResidualRMS,
		PeriodBefore: before.Period, PeriodAfter: after.Period, PeriodAfterFree: afterFree,
	}
	if h, ok := before.Curve.Amp(2); ok {
		v.Amp2Before = h.Amp
	}
	if h, ok := after.Curve.Amp(2); ok {
		v.Amp2After = h.Amp
	}
	if v.AmpBefore > 0 {
		v.Ratio = v.AmpAfter / v.AmpBefore
		rb, ra := b1.AmpSigma/b1.Amp, 0.0
		if a1.Amp > 0 {
			ra = a1.AmpSigma / a1.Amp
		}
		v.RatioSigma = v.Ratio * math.Sqrt(rb*rb+ra*ra)
	}
	if v.Amp2Before > 0 {
		v.Ratio2 = v.Amp2After / v.Amp2Before
	}

	if afterFree > 0 {
		tol := math.Max(0.02*before.Period, 3*(before.PeriodSigma+after.PeriodSigma))
		if math.IsInf(tol, 0) || math.IsNaN(tol) {
			tol = 0.02 * before.Period
		}
		if math.Abs(afterFree-before.Period) > tol {
			v.Reasons = append(v.Reasons, fmt.Sprintf("the two runs disagree on the worm period (%.1f s before, %.1f s after on its own); one run may be too short, and the verdict uses the before period", before.Period, afterFree))
		}
	}

	switch {
	case v.AmpBefore <= 0 || v.AmpBefore < 3*b1.AmpSigma:
		v.Verdict = VerdictInconclusive
		v.Detail = fmt.Sprintf("The PEC-off run shows no clear periodic signal (fundamental %.2f″ ± %.2f″), so there is nothing to compare against. Measure PEC off again with a longer, unguided run.", v.AmpBefore, b1.AmpSigma)
	case v.RatioSigma > 0.3:
		v.Verdict = VerdictInconclusive
		v.Detail = fmt.Sprintf("The amplitude ratio %.2f has an uncertainty of ±%.2f, too large to classify. Longer runs at a finer cadence will tighten it.", v.Ratio, v.RatioSigma)
	case v.Ratio < 0.7:
		v.Verdict = VerdictHelping
		v.Detail = fmt.Sprintf("The fundamental dropped from %.2f″ to %.2f″ (ratio %.2f ± %.2f). The table has the right sign and phase; periodic RMS went from %.2f″ to %.2f″.", v.AmpBefore, v.AmpAfter, v.Ratio, v.RatioSigma, v.PeriodicRMSBefore, v.PeriodicRMSAfter)
	case v.Ratio <= 1.3:
		v.Verdict = VerdictUnchanged
		v.Detail = fmt.Sprintf("The fundamental is %.2f″ before and %.2f″ after (ratio %.2f ± %.2f). Either the table amplitude is far too small, or PEC was not actually applied during the after run. Check that Apply PEC was on.", v.AmpBefore, v.AmpAfter, v.Ratio, v.RatioSigma)
	case v.Ratio > 1.5 && v.Amp2Before > 0 && v.Ratio2 > 1.3:
		v.Verdict = VerdictInverted
		v.Detail = fmt.Sprintf("The fundamental grew from %.2f″ to %.2f″ (ratio %.2f ± %.2f) and the second harmonic grew too (%.2f″ to %.2f″). Every harmonic doubling means the table has the wrong sign. Re-fit with Invert and paste the inverted table.", v.AmpBefore, v.AmpAfter, v.Ratio, v.RatioSigma, v.Amp2Before, v.Amp2After)
	case v.Ratio > 1.5 && v.Amp2Before > 0 && v.Ratio2 < 0.7:
		v.Verdict = VerdictHalfCycle
		v.Detail = fmt.Sprintf("The fundamental grew from %.2f″ to %.2f″ (ratio %.2f ± %.2f) while the second harmonic shrank (%.2f″ to %.2f″). Odd harmonics doubling and even ones cancelling means the table is half a worm cycle out of phase, not inverted. Check the phase anchor.", v.AmpBefore, v.AmpAfter, v.Ratio, v.RatioSigma, v.Amp2Before, v.Amp2After)
	default:
		v.Verdict = VerdictWorse
		v.Detail = fmt.Sprintf("The fundamental grew from %.2f″ to %.2f″ (ratio %.2f ± %.2f) without the clean doubling of an inverted table. The phase is probably off by a fraction of a cycle. Check the anchor before trusting the table.", v.AmpBefore, v.AmpAfter, v.Ratio, v.RatioSigma)
	}
	return v
}
