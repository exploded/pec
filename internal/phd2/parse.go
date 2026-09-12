package phd2

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

const timeLayout = "2006-01-02 15:04:05"

type state int

const (
	stTop state = iota
	stCalib
	stHeader
	stRows
)

// ParseFile parses a guide log from disk. Timestamps in the log carry no
// zone; loc says which one they were written in.
func ParseFile(path string, loc *time.Location) (*Log, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	l, err := Parse(f, path, loc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return l, nil
}

// Parse parses a guide log. It tolerates repeated "PHD2 version"
// preambles, calibration blocks (skipped), sessions without a "Guiding
// Ends", DROP rows, and INFO lines anywhere.
func Parse(r io.Reader, path string, loc *time.Location) (*Log, error) {
	if loc == nil {
		loc = time.Local
	}
	l := &Log{Path: path}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	st := stTop
	var cur *Session
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimRight(sc.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "PHD2 version"):
			l.Versions = append(l.Versions, line)
			st = stTop
			cur = nil
		case strings.HasPrefix(line, "Calibration Begins at"):
			l.Calibrations++
			st = stCalib
			cur = nil
		case strings.HasPrefix(line, "Guiding Begins at"):
			t, err := time.ParseInLocation(timeLayout, strings.TrimSpace(strings.TrimPrefix(line, "Guiding Begins at")), loc)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNo, err)
			}
			cur = &Session{Index: len(l.Sessions) + 1, Begins: t, Header: map[string]string{}}
			l.Sessions = append(l.Sessions, cur)
			st = stHeader
		case strings.HasPrefix(line, "Guiding Ends at"):
			if cur != nil {
				t, err := time.ParseInLocation(timeLayout, strings.TrimSpace(strings.TrimPrefix(line, "Guiding Ends at")), loc)
				if err != nil {
					return nil, fmt.Errorf("line %d: %w", lineNo, err)
				}
				cur.Ends = t
			}
			cur = nil
			st = stTop
		case trimmed == "":
			// ignore
		case st == stCalib:
			// skip calibration body
		case strings.HasPrefix(line, "INFO:"):
			if cur != nil {
				cur.Events = append(cur.Events, parseInfo(cur, strings.TrimSpace(strings.TrimPrefix(line, "INFO:"))))
			}
		case st == stHeader:
			if strings.HasPrefix(line, "Frame,Time,") {
				st = stRows
				continue
			}
			parseHeaderLine(cur, trimmed)
		case st == stRows:
			if line[0] >= '0' && line[0] <= '9' {
				if err := parseRow(cur, line); err != nil {
					return nil, fmt.Errorf("line %d: %w", lineNo, err)
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading log: %w", err)
	}
	return l, nil
}

// parseHeaderLine splits "k = v, k = v" into the session header map and
// pulls out the fields the tool uses.
func parseHeaderLine(s *Session, line string) {
	// Pairs from this line only: keys such as "Dec" recur on other lines
	// ("Norm rates ... Dec = 7.5\"/s"), so field extraction must not go
	// through the first-wins session map.
	kv := map[string]string{}
	for _, part := range strings.Split(line, ", ") {
		k, v, ok := strings.Cut(part, " = ")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if _, seen := kv[k]; !seen {
			kv[k] = v
		}
		if _, seen := s.Header[k]; !seen {
			s.Header[k] = v
		}
	}
	switch {
	case strings.HasPrefix(line, "Equipment Profile"):
		s.Profile = kv["Equipment Profile"]
	case strings.HasPrefix(line, "Camera"):
		s.Camera = kv["Camera"]
	case strings.HasPrefix(line, "Mount"):
		s.Mount = kv["Mount"]
	case strings.HasPrefix(line, "Pixel scale"):
		s.PixelScale = leadingFloat(kv["Pixel scale"])
		s.Binning = int(leadingFloat(kv["Binning"]))
		s.FocalLength = leadingFloat(kv["Focal length"])
	case strings.HasPrefix(line, "Exposure"):
		s.ExposureMS = int(leadingFloat(kv["Exposure"]))
	case strings.HasPrefix(line, "RA = "):
		s.RAHours = leadingFloat(kv["RA"])
		s.DecDeg = leadingFloat(kv["Dec"])
		s.HourAngle = leadingFloat(kv["Hour angle"])
		s.PierSide = kv["Pier side"]
		s.AltDeg = leadingFloat(kv["Alt"])
		s.AzDeg = leadingFloat(kv["Az"])
	}
}

// leadingFloat parses the first whitespace-separated token as a float.
func leadingFloat(v string) float64 {
	f := strings.Fields(v)
	if len(f) == 0 {
		return 0
	}
	x, _ := strconv.ParseFloat(f[0], 64)
	return x
}

func parseInfo(s *Session, text string) Event {
	e := Event{Kind: EventOther, After: len(s.Samples) - 1, Text: text}
	if e.After >= 0 {
		e.Offset = s.Samples[e.After].Offset
	}
	switch {
	case strings.HasPrefix(text, "DITHER by "):
		e.Kind = EventDither
		// "DITHER by -2.494, 0.318, new lock pos = 111.353, 46.274"
		rest := strings.TrimPrefix(text, "DITHER by ")
		nums := floatsIn(rest)
		if len(nums) >= 4 {
			e.Dx, e.Dy, e.LockX, e.LockY = nums[0], nums[1], nums[2], nums[3]
		}
	case strings.HasPrefix(text, "SET LOCK POSITION"):
		e.Kind = EventSetLockPos
		nums := floatsIn(text)
		if len(nums) >= 2 {
			e.LockX, e.LockY = nums[0], nums[1]
		}
	case strings.HasPrefix(text, "SETTLING STATE CHANGE"):
		if strings.Contains(text, "complete") {
			e.Kind = EventSettlingDone
		} else {
			e.Kind = EventSettlingStart
		}
	case strings.HasPrefix(text, "Guiding parameter change"):
		e.Kind = EventParamChange
		_, kv, _ := strings.Cut(text, ", ")
		k, v, ok := strings.Cut(kv, "=")
		if ok {
			e.Key = strings.TrimSpace(k)
			e.Value = strings.TrimSpace(v)
		}
	case strings.HasPrefix(text, "GA Result"):
		e.Kind = EventGAResult
	}
	return e
}

// floatsIn extracts every parseable float token from text, ignoring words.
func floatsIn(text string) []float64 {
	var out []float64
	for _, tok := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ' ' || r == '=' }) {
		if f, err := strconv.ParseFloat(tok, 64); err == nil {
			out = append(out, f)
		}
	}
	return out
}

func parseRow(s *Session, line string) error {
	rd := csv.NewReader(strings.NewReader(line))
	rd.FieldsPerRecord = -1
	rd.LazyQuotes = true
	f, err := rd.Read()
	if err != nil {
		return fmt.Errorf("bad row: %w", err)
	}
	if len(f) < 3 {
		return fmt.Errorf("bad row: %d fields", len(f))
	}
	frame, _ := strconv.Atoi(f[0])
	offset, err := strconv.ParseFloat(f[1], 64)
	if err != nil {
		return fmt.Errorf("bad time %q", f[1])
	}
	if f[2] == "DROP" {
		e := Event{Kind: EventDrop, After: len(s.Samples) - 1, Offset: offset, Frame: frame}
		if len(f) > 18 {
			e.Text = f[18]
		}
		s.Events = append(s.Events, e)
		return nil
	}
	if len(f) < 18 {
		return fmt.Errorf("row has %d fields, want 18", len(f))
	}
	smp := Sample{
		Frame:        frame,
		Offset:       offset,
		At:           s.Begins.Add(time.Duration(offset * float64(time.Second))),
		Dx:           num(f[3]),
		Dy:           num(f[4]),
		RARaw:        num(f[5]),
		DecRaw:       num(f[6]),
		RAGuide:      num(f[7]),
		DecGuide:     num(f[8]),
		RADuration:   int(num(f[9])),
		RADirection:  f[10],
		DecDuration:  int(num(f[11])),
		DecDirection: f[12],
		StarMass:     num(f[15]),
		SNR:          num(f[16]),
		ErrorCode:    int(num(f[17])),
	}
	s.Samples = append(s.Samples, smp)
	return nil
}

func num(v string) float64 {
	if v == "" {
		return 0
	}
	x, _ := strconv.ParseFloat(v, 64)
	return x
}
