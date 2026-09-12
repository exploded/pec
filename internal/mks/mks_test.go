package mks

import (
	"bytes"
	"math"
	"os"
	"testing"
	"time"
)

func TestFrameRoundTrip(t *testing.T) {
	cases := []struct {
		axis int
		word int
		data []byte
	}{
		{0, CmdStatus, nil},
		{1, CmdRead32, u16(10)},
		{0, StatusOK, i32(-59)}, // has 0xff bytes
		{0, StatusOK, i32(0)},   // all zeros: stuffing
		{0, CmdWrite32, append(u16(0x0b), i32(-1)...)},
		{0, 44, bytes.Repeat([]byte{0, 0x64, 0x5a}, 20)}, // start and end bytes inside data
	}
	for _, c := range cases {
		raw := encode(c.axis, 0xc6, c.word, c.data)
		var sp splitter
		fr := sp.feed(time.Unix(0, 0), raw[:3])
		fr = append(fr, sp.feed(time.Unix(1, 0), raw[3:])...)
		if len(fr) != 1 || fr[0].Axis != c.axis || fr[0].Word != c.word || !bytes.Equal(fr[0].Data, c.data) || fr[0].Seq != 0xc6 {
			t.Errorf("round trip %+v: got %+v", c, fr)
		}
		if sp.junk != 0 {
			t.Errorf("junk %d", sp.junk)
		}
	}
	// Junk before a frame is skipped, not fatal.
	var sp splitter
	fr := sp.feed(time.Unix(0, 0), append([]byte{1, 2, 0x64, 9}, encode(0, 1, CmdStatus, nil)...))
	if len(fr) != 1 || sp.junk != 4 {
		t.Errorf("resync: frames %d junk %d", len(fr), sp.junk)
	}
}

func TestSyntheticCapture(t *testing.T) {
	t0 := time.Date(2026, 9, 12, 21, 0, 0, 0, time.UTC)
	const rate, offset = 133.6, 72.5
	data := Synth(SynthOptions{Start: t0, Duration: 400, Rate: rate, Encoder0: 1000, IndexOffset: offset, WithIndex: true})
	c, err := Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if c.Junk != 0 || c.Unpaired != 0 || len(c.Encoder()) < 1200 || len(c.Index()) < 350 || len(c.Writes) < 60 || len(c.Series(1, CmdRead32, Reg32Encoder)) < 1200 {
		t.Fatalf("decode: junk %d unpaired %d enc %d idx %d writes %d", c.Junk, c.Unpaired, len(c.Encoder()), len(c.Index()), len(c.Writes))
	}
	r, err := Analyse(c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(r.EncoderRate-rate) > 0.01 || r.EncoderRMS > 1 || r.EncoderOutliers != 0 {
		t.Errorf("rate %.4f rms %.2f outliers %d", r.EncoderRate, r.EncoderRMS, r.EncoderOutliers)
	}
	wantP := 20000 / rate
	if math.Abs(r.Period-wantP) > 0.02 || r.PeriodSigma > 0.01 {
		t.Errorf("period %.3f +- %.4f, want %.3f", r.Period, r.PeriodSigma, wantP)
	}
	if !r.HasAnchor || math.Abs(r.IndexOffset-offset) > 0.6 || r.IndexSpread > 1.1 {
		t.Errorf("index offset %.2f spread %.2f anchor %v", r.IndexOffset, r.IndexSpread, r.HasAnchor)
	}
	// The anchor must agree with the truth at its own instant.
	ts := r.AnchorAt.Sub(t0).Seconds()
	truth := math.Mod((1000+rate*(ts-20))/CountsPerIndex+offset, 1250)
	if d := math.Abs(float64(r.AnchorIndex) - truth); d > 1 {
		t.Errorf("anchor index %d at %.3f s, truth %.2f", r.AnchorIndex, ts, truth)
	}
	if r.AnchorSigma > 0.2 || r.Level != "good" {
		t.Errorf("anchor sigma %.3f level %s warnings %v", r.AnchorSigma, r.Level, r.Warnings)
	}
	if len(r.StatusChanges) != 2 || r.TrackFrom.Sub(t0).Seconds() < 19.9 || len(r.Notes) != 1 {
		t.Errorf("status changes %v notes %v", r.StatusChanges, r.Notes)
	}

	// Without index readings: period still comes out, anchor does not.
	c2, _ := Decode(bytes.NewReader(Synth(SynthOptions{Start: t0, Duration: 300, Rate: rate, Encoder0: 1000, IndexOffset: offset})))
	r2, err := Analyse(c2, Options{})
	if err != nil || r2.HasAnchor || r2.Level != "bad" || math.Abs(r2.Period-wantP) > 0.02 {
		t.Errorf("no-index: err %v anchor %v level %s period %.3f", err, r2.HasAnchor, r2.Level, r2.Period)
	}
}

// TestRealCapture checks the reference capture when it is present (it is
// 47 MB and gitignored).
func TestRealCapture(t *testing.T) {
	f, err := os.Open("../../.local/cpature.pcapng")
	if err != nil {
		t.Skip("reference capture not present")
	}
	defer f.Close()
	c, err := Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if c.Frames != 52881 || c.Junk > 10 || len(c.Encoder()) != 1944 || len(c.Index()) != 2 {
		t.Errorf("frames %d junk %d encoder %d index %d", c.Frames, c.Junk, len(c.Encoder()), len(c.Index()))
	}
	r, err := Analyse(c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(r.EncoderRate-133.535) > 0.01 || r.EncoderRMS > 1 || math.Abs(r.Period-149.77) > 0.02 {
		t.Errorf("rate %.4f rms %.2f period %.3f", r.EncoderRate, r.EncoderRMS, r.Period)
	}
	if r.IndexReadings != 2 || math.Abs(r.IndexOffset-72.7) > 0.6 || !r.HasAnchor {
		t.Errorf("index readings %d offset %.2f anchor %v", r.IndexReadings, r.IndexOffset, r.HasAnchor)
	}
	if len(r.StatusChanges) != 3 || r.StatusChanges[2].Value != 4608 {
		t.Errorf("status %v", r.StatusChanges)
	}
	t.Logf("rate %.4f +- %.4f counts/s, period %.4f +- %.4f s, teeth %.2f, anchor index %d at %s +- %.3f s, warnings %v",
		r.EncoderRate, r.EncoderRateSig, r.Period, r.PeriodSigma, r.TeethEstimate, r.AnchorIndex, r.AnchorAt.Format(time.RFC3339Nano), r.AnchorSigma, r.Warnings)
}
