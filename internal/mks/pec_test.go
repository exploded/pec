package mks

import (
	"bytes"
	"math"
	"os"
	"testing"
	"time"

	"github.com/exploded/pec/internal/pe"
	"github.com/exploded/pec/internal/tcs"
)

// binTable averages a table into nb bins, as the report does.
func binTable(t *tcs.Table, nb int) []float64 {
	per := len(t.Values) / nb
	out := make([]float64, nb)
	for i := range out {
		var s float64
		for j := 0; j < per; j++ {
			s += float64(t.Values[i*per+j])
		}
		out[i] = s / float64(per)
	}
	return out
}

func TestSynthPEC(t *testing.T) {
	tab, err := tcs.ReadFile("../../testdata/PEC_table_TCS_2026-09-12.txt")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 12, 19, 0, 0, 0, time.UTC)
	data := Synth(SynthOptions{Start: start, Duration: 1000, Rate: 133.698, Encoder0: 40000, IndexOffset: 72.7, WithIndex: true,
		PECTable: tab.Values, PECFrom: 420})
	c, err := Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Analyse(c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.PECOn) != 1 {
		t.Fatalf("PECOn %v", r.PECOn)
	}
	if from := r.PECOn[0].From.Sub(start).Seconds(); math.Abs(from-420) > 60 {
		t.Errorf("PEC on from %.0f s, want about 420", from)
	}
	if math.Abs(r.EncoderRate-133.698) > 0.005 || r.EncoderRMS > 1 || r.QuietReadings < 200 {
		t.Errorf("rate %.4f rms %.2f quiet %d", r.EncoderRate, r.EncoderRMS, r.QuietReadings)
	}
	if !r.HasAnchor || r.IndexSpread > 1.2 {
		t.Errorf("anchor %v spread %.2f", r.HasAnchor, r.IndexSpread)
	}
	if r.Fold == nil {
		t.Fatal("no fold")
	}
	applied := make([]float64, r.FoldBins)
	for i := range applied {
		applied[i] = -r.Fold[i]
	}
	ha := pe.DFT(applied, 3).Fundamental()
	hs := pe.DFT(binTable(tab, r.FoldBins), 3).Fundamental()
	diff := math.Mod(ha.PhaseDeg-hs.PhaseDeg+540, 360) - 180
	if math.Abs(ha.Amp-hs.Amp) > 0.5 || math.Abs(diff) > 6 {
		t.Errorf("applied %.2f @ %.1f, table %.2f @ %.1f (diff %.1f)", ha.Amp, ha.PhaseDeg, hs.Amp, hs.PhaseDeg, diff)
	}
}

// TestNightCapture checks the 2026-09-12 measuring-night capture when it
// is present: PEC was switched on 11 minutes in, and the encoder then
// carried minus the stored table.
func TestNightCapture(t *testing.T) {
	f, err := os.Open("../../.local/2026-09-12/file2.pcapng")
	if err != nil {
		t.Skip("night capture not present")
	}
	defer f.Close()
	c, err := Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Analyse(c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Australia/Melbourne")
	if len(r.PECOn) != 1 {
		t.Fatalf("PECOn %v", r.PECOn)
	}
	want := time.Date(2026, 9, 12, 19, 19, 0, 0, loc)
	if d := r.PECOn[0].From.Sub(want).Abs(); d > 90*time.Second {
		t.Errorf("PEC on from %s, want about %s", r.PECOn[0].From.In(loc), want)
	}
	if math.Abs(r.EncoderRate-133.698) > 0.002 || r.EncoderRMS > 1 || math.Abs(r.Period-149.591) > 0.003 {
		t.Errorf("rate %.4f rms %.2f period %.4f", r.EncoderRate, r.EncoderRMS, r.Period)
	}
	if !r.HasAnchor || r.IndexSpread > 1 || math.Abs(r.IndexOffset-72.7) > 0.5 {
		t.Errorf("anchor %v spread %.2f offset %.2f", r.HasAnchor, r.IndexSpread, r.IndexOffset)
	}
	tab, err := tcs.ReadFile("../../testdata/PEC_table_TCS_2026-09-12.txt")
	if err != nil {
		t.Fatal(err)
	}
	applied := make([]float64, r.FoldBins)
	for i := range applied {
		applied[i] = -r.Fold[i]
	}
	ha := pe.DFT(applied, 3).Fundamental()
	hs := pe.DFT(binTable(tab, r.FoldBins), 3).Fundamental()
	diff := math.Mod(ha.PhaseDeg-hs.PhaseDeg+540, 360) - 180
	if math.Abs(ha.Amp-hs.Amp) > 0.3 || math.Abs(diff) > 4 {
		t.Errorf("applied %.2f @ %.1f, table %.2f @ %.1f (diff %.1f)", ha.Amp, ha.PhaseDeg, hs.Amp, hs.PhaseDeg, diff)
	}
	t.Logf("PEC on %s..%s, rate %.4f rms %.2f, applied %.2f @ %.1f vs table %.2f @ %.1f, anchor %d at %s +- %.2f",
		r.PECOn[0].From.In(loc).Format("15:04:05"), r.PECOn[0].To.In(loc).Format("15:04:05"), r.EncoderRate, r.EncoderRMS, ha.Amp, ha.PhaseDeg, hs.Amp, hs.PhaseDeg, r.AnchorIndex, r.AnchorAt.In(loc).Format("15:04:05.00"), r.AnchorSigma)
}
