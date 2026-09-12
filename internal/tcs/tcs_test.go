package tcs

import (
	"bytes"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

const fixture = "../../testdata/PEC_table_TCS_2026-09-12.txt"

func TestReadWriteRoundTrip(t *testing.T) {
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	tbl, err := Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(tbl.Values) != 1250 {
		t.Fatalf("entries %d", len(tbl.Values))
	}
	// Strip the three hand-written comment lines; the rest must be byte-identical.
	var want []byte
	for _, line := range bytes.SplitAfter(raw, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("#")) {
			continue
		}
		want = append(want, line...)
	}
	var got bytes.Buffer
	if err := Write(&got, tbl); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Errorf("Write output differs from fixture\n got: %q\nwant: %q", head(got.Bytes()), head(want))
	}
	s := tbl.Stats()
	if s.Min != -8 || s.Max != 9 || s.P2P != 17 || math.Abs(s.RMS-5.011) > 0.001 {
		t.Errorf("stats %+v", s)
	}
}

func head(b []byte) []byte {
	if len(b) > 60 {
		return b[:60]
	}
	return b
}

func TestReadValidation(t *testing.T) {
	good := func(n int) string {
		var sb strings.Builder
		for i := 0; i < n; i++ {
			sb.WriteString(strings.Repeat(" ", 2) + itoa(i) + "\t" + itoa(i%3-1) + "\n")
		}
		return sb.String()
	}
	cases := []struct {
		name    string
		in      string
		wantErr string
	}{
		{"lf only, mixed whitespace", "0 1\n1\t\t2\n" + tail(2, 125), ""},
		{"comments and blanks", "# hi\n\n" + good(125), ""},
		{"1249 entries", good(1249), "multiple of 125"},
		{"non-contiguous", "0 1\n2 1\n" + tail(3, 125), "contiguous"},
		{"float value", "0 1.5\n" + tail(1, 125), "integer ticks"},
		{"three fields", "0 1 2\n" + tail(1, 125), "2 fields"},
		{"empty", "", "multiple of 125"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Read(strings.NewReader(c.in))
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, c.wantErr)
			}
		})
	}
}

func tail(from, to int) string {
	var sb strings.Builder
	for i := from; i < to; i++ {
		sb.WriteString(itoa(i) + " 0\n")
	}
	return sb.String()
}

func itoa(i int) string { return strconv.Itoa(i) }

func TestFromArcsec(t *testing.T) {
	cfg := DefaultConfig()
	vals := make([]float64, 1250)
	for i := range vals {
		vals[i] = 1.0 * math.Sin(2*math.Pi*float64(i)/1250)
	}
	tbl, q := FromArcsec(vals, cfg)
	if tbl.Values[0] != 0 || tbl.Values[312] != 9 {
		t.Errorf("values[0]=%d values[312]=%d", tbl.Values[0], tbl.Values[312])
	}
	if q < 0.025 || q > 0.04 {
		t.Errorf("quantisation RMS %.4f, want about 0.032", q)
	}
	neg := tbl.Negate()
	if neg.Values[312] != -9 {
		t.Errorf("negate %d", neg.Values[312])
	}
}
