// Package tcs reads and writes Bisque TCS periodic-error-correction tables:
// the text the TCS window's Copy button puts on the clipboard, and the text
// its Paste button expects back.
package tcs

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
)

const (
	// DefaultArcsecPerTick is the encoder resolution derived from the TCS
	// graph legend (min -0.9" at -8 ticks, max +1.0" at +9 ticks).
	DefaultArcsecPerTick = 0.1125
	// DefaultEntries is 125 x PEC ratio 10 for the Paramount ME.
	DefaultEntries = 1250
	// Ratio is the base entry count; a table must be a multiple of it.
	Ratio = 125
)

// Config carries the mount-specific constants. They are values, not
// constants buried in code, because the brief asks for them to be
// configurable.
type Config struct {
	ArcsecPerTick float64
	Entries       int
}

// DefaultConfig returns the Paramount ME values.
func DefaultConfig() Config {
	return Config{ArcsecPerTick: DefaultArcsecPerTick, Entries: DefaultEntries}
}

// Table is a PEC table: Values[i] is the correction in encoder ticks at
// index i, where index 0..N-1 maps linearly onto one worm revolution.
type Table struct {
	Values []int
}

// Read parses a table. It accepts what the TCS Copy button produces plus
// '#' comment lines, blank lines, any whitespace separation, and CR/LF line
// endings. It requires exactly two integer fields per row, contiguous
// indices from 0, and a length that is a positive multiple of Ratio.
func Read(r io.Reader) (*Table, error) {
	sc := bufio.NewScanner(r)
	var vals []int
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			return nil, fmt.Errorf("line %d: want 2 fields, got %d", lineNo, len(f))
		}
		idx, err := strconv.Atoi(f[0])
		if err != nil {
			return nil, fmt.Errorf("line %d: bad index %q", lineNo, f[0])
		}
		if idx != len(vals) {
			return nil, fmt.Errorf("line %d: index %d, want %d (indices must be contiguous from 0)", lineNo, idx, len(vals))
		}
		v, err := strconv.Atoi(f[1])
		if err != nil {
			return nil, fmt.Errorf("line %d: bad value %q (values are integer ticks)", lineNo, f[1])
		}
		vals = append(vals, v)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading table: %w", err)
	}
	if len(vals) == 0 || len(vals)%Ratio != 0 {
		return nil, fmt.Errorf("table has %d entries; want a positive multiple of %d", len(vals), Ratio)
	}
	return &Table{Values: vals}, nil
}

// ReadFile reads a table from disk.
func ReadFile(path string) (*Table, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	t, err := Read(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return t, nil
}

// Write emits the table in the TCS Copy-button format: right-aligned
// width-4 index, a tab, the value, LF (the mount's own copy on disk is
// LF-terminated). No header or comments, so the file is paste-clean. The
// format is fixed on every platform so output diffs cleanly against a
// recorded table.
func Write(w io.Writer, t *Table) error {
	bw := bufio.NewWriter(w)
	for i, v := range t.Values {
		if _, err := fmt.Fprintf(bw, "%4d\t%d\n", i, v); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// WriteFile writes a table to disk.
func WriteFile(path string, t *Table) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := Write(f, t); err != nil {
		f.Close()
		return fmt.Errorf("%s: %w", path, err)
	}
	return f.Close()
}

// String renders the table exactly as Write would.
func (t *Table) String() string {
	var sb strings.Builder
	_ = Write(&sb, t)
	return sb.String()
}

// Arcsec converts ticks to arcseconds.
func (t *Table) Arcsec(cfg Config) []float64 {
	out := make([]float64, len(t.Values))
	for i, v := range t.Values {
		out[i] = float64(v) * cfg.ArcsecPerTick
	}
	return out
}

// Negate returns the inverted table.
func (t *Table) Negate() *Table {
	out := &Table{Values: make([]int, len(t.Values))}
	for i, v := range t.Values {
		out.Values[i] = -v
	}
	return out
}

// Stats summarises a table in ticks. RMS is about zero, matching the
// brief's reference figure.
type Stats struct {
	Entries       int
	Min, Max, P2P int
	RMS           float64
	Mean          float64
}

// Stats computes the summary.
func (t *Table) Stats() Stats {
	s := Stats{Entries: len(t.Values)}
	if len(t.Values) == 0 {
		return s
	}
	s.Min, s.Max = t.Values[0], t.Values[0]
	sum, sum2 := 0.0, 0.0
	for _, v := range t.Values {
		if v < s.Min {
			s.Min = v
		}
		if v > s.Max {
			s.Max = v
		}
		sum += float64(v)
		sum2 += float64(v) * float64(v)
	}
	s.P2P = s.Max - s.Min
	n := float64(len(t.Values))
	s.RMS = math.Sqrt(sum2 / n)
	s.Mean = sum / n
	return s
}

// FromArcsec rounds each value to the nearest tick and reports the RMS error
// in arcsec introduced by that rounding (expect about 0.1125/sqrt(12) =
// 0.032" for a smooth curve).
func FromArcsec(vals []float64, cfg Config) (*Table, float64) {
	t := &Table{Values: make([]int, len(vals))}
	sum2 := 0.0
	for i, v := range vals {
		ticks := v / cfg.ArcsecPerTick
		r := math.Round(ticks)
		t.Values[i] = int(r)
		e := (ticks - r) * cfg.ArcsecPerTick
		sum2 += e * e
	}
	if len(vals) == 0 {
		return t, 0
	}
	return t, math.Sqrt(sum2 / float64(len(vals)))
}
