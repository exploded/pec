package phd2

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"time"
)

// SessionInfoEvent names the record pec appends to a live recording.
const SessionInfoEvent = "pec.SessionInfo"

// SessionInfo is pec's own line in a live recording: what PHD2 answered
// when the run started and where the mount was pointing. The event stream
// has no header, so this carries what a guide-log header would.
type SessionInfo struct {
	Event              string  `json:"Event"`
	Timestamp          float64 `json:"Timestamp"`
	PixelScale         float64 `json:"PixelScale"`         // arcsec/px; 0 = PHD2 did not say
	ExposureMS         int     `json:"ExposureMS"`         // 0 = unknown
	RAHours            float64 `json:"RAHours"`            // J2000
	DecDeg             float64 `json:"DecDeg"`             // J2000
	PositionSource     string  `json:"PositionSource"`     // nina | target | none
	GuideOutputEnabled *bool   `json:"GuideOutputEnabled"` // nil when PHD2 did not answer
	Profile            string  `json:"Profile,omitempty"`
	Camera             string  `json:"Camera,omitempty"`
	Mount              string  `json:"Mount,omitempty"`
	Target             string  `json:"Target,omitempty"`
	MidRun             bool    `json:"MidRun,omitempty"` // pec connected while the run was already going
	Warning            string  `json:"Warning,omitempty"`
}

// rawEvent holds every field pec reads from any PHD2 event. encoding/json
// matches names case-insensitively, so dx and dy land in Dx and Dy.
type rawEvent struct {
	Event     string
	Timestamp float64

	PHDVersion, PHDSubver string // Version
	State                 string // AppState

	Frame                                                            int     // GuideStep, StarLost
	Time                                                             float64 // seconds since guiding started
	Mount                                                            string
	Dx, Dy                                                           float64 // GuideStep, GuidingDithered
	RADistanceRaw, DECDistanceRaw, RADistanceGuide, DECDistanceGuide float64
	RADuration, DECDuration                                          int
	RADirection, DECDirection                                        string
	StarMass, SNR                                                    float64
	ErrorCode                                                        int
	Status                                                           json.RawMessage // StarLost: text; SettleDone: number

	X, Y  float64         // LockPositionSet, StarSelected
	Name  string          // GuideParamChange
	Value json.RawMessage // GuideParamChange: bool, number or string
	Msg   string          // Alert
}

// ParseEvents reads a live recording: PHD2 event-server lines as received,
// plus pec's own SessionInfo line. Each StartGuiding..GuidingStopped becomes
// a Session. Timestamps are absolute, so loc only chooses how they display.
// A line that does not decode is skipped: a recording still being written
// ends mid-line.
func ParseEvents(r io.Reader, name string, loc *time.Location) (*Log, error) {
	if loc == nil {
		loc = time.Local
	}
	p := eventsParser{l: &Log{Path: name}, loc: loc}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev rawEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		p.decoded++
		p.handle(line, ev)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading recording: %w", err)
	}
	if p.decoded == 0 {
		return nil, errors.New("not a PHD2 recording: no events")
	}
	p.finish(0) // the run is still going: leave Ends zero
	return p.l, nil
}

// LoadFile parses a guide log or a live recording, told apart by the first
// non-blank byte: a recording is JSON lines, a guide log is text.
func LoadFile(path string, loc *time.Location) (*Log, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	br := bufio.NewReader(f)
	events := false
	for {
		b, err := br.ReadByte()
		if err != nil {
			break
		}
		if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
			continue
		}
		events = b == '{'
		_ = br.UnreadByte()
		break
	}
	var l *Log
	if events {
		l, err = ParseEvents(br, path, loc)
	} else {
		l, err = Parse(br, path, loc)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return l, nil
}

// IsEventsStream reports whether data starts like a live recording.
func IsEventsStream(head []byte) bool {
	head = bytes.TrimSpace(head)
	return len(head) > 0 && head[0] == '{'
}

type eventsParser struct {
	l       *Log
	loc     *time.Location
	decoded int

	cur            *Session
	startTS        float64 // timestamp of the event that opened cur; Begins fallback
	sawOutputParam bool    // a MountGuidingEnabled change was seen in cur
}

func (p *eventsParser) open(ts float64, midRun bool) {
	p.cur = &Session{Index: len(p.l.Sessions) + 1, Live: true, Header: map[string]string{}}
	if midRun {
		p.cur.Header["Joined"] = "run already in progress when pec connected"
	}
	p.startTS = ts
	p.sawOutputParam = false
	p.l.Sessions = append(p.l.Sessions, p.cur)
}

// finish closes the open session. endTS 0 means the recording ended
// without a GuidingStopped, so Ends stays zero.
func (p *eventsParser) finish(endTS float64) {
	s := p.cur
	if s == nil {
		return
	}
	if s.Begins.IsZero() {
		s.Begins = unixTime(p.startTS, p.loc)
	}
	for i := range s.Samples {
		s.Samples[i].At = s.Begins.Add(time.Duration(s.Samples[i].Offset * float64(time.Second)))
	}
	if endTS > 0 {
		s.Ends = unixTime(endTS, p.loc)
	}
	p.cur = nil
}

// event positions a new event after the last sample, as parseInfo does.
func (p *eventsParser) event(kind EventKind, text string) Event {
	e := Event{Kind: kind, After: len(p.cur.Samples) - 1, Text: text}
	if e.After >= 0 {
		e.Offset = p.cur.Samples[e.After].Offset
	}
	return e
}

func (p *eventsParser) add(e Event) {
	p.cur.Events = append(p.cur.Events, e)
}

func (p *eventsParser) handle(line []byte, ev rawEvent) {
	switch ev.Event {
	case "Version":
		p.l.Versions = append(p.l.Versions, "PHD2 version "+ev.PHDVersion+ev.PHDSubver)
	case "StartGuiding":
		// PHD2 re-sends StartGuiding to a client that connects mid-run, so
		// one with samples already in hand means a new run.
		if p.cur != nil && len(p.cur.Samples) > 0 {
			p.finish(ev.Timestamp)
		}
		if p.cur == nil {
			p.open(ev.Timestamp, false)
		}
	case "AppState":
		if ev.State == "Guiding" && p.cur == nil {
			p.open(ev.Timestamp, true)
		}
	case "GuideStep":
		if p.cur == nil {
			p.open(ev.Timestamp-ev.Time, true)
		}
		s := p.cur
		if s.Begins.IsZero() {
			// Time counts from the real start of guiding, whatever pec saw.
			s.Begins = unixTime(ev.Timestamp-ev.Time, p.loc)
		}
		if s.Mount == "" {
			s.Mount = ev.Mount
		}
		s.Samples = append(s.Samples, Sample{
			Frame: ev.Frame, Offset: ev.Time,
			Dx: ev.Dx, Dy: ev.Dy,
			RARaw: ev.RADistanceRaw, DecRaw: ev.DECDistanceRaw,
			RAGuide: ev.RADistanceGuide, DecGuide: ev.DECDistanceGuide,
			RADuration: ev.RADuration, RADirection: ev.RADirection,
			DecDuration: ev.DECDuration, DecDirection: ev.DECDirection,
			StarMass: ev.StarMass, SNR: ev.SNR, ErrorCode: ev.ErrorCode,
		})
	case "StarLost":
		if p.cur == nil {
			return
		}
		e := p.event(EventDrop, rawString(ev.Status))
		if e.Text == "" {
			e.Text = "star lost"
		}
		e.Offset, e.Frame = ev.Time, ev.Frame
		p.add(e)
	case "GuidingDithered":
		if p.cur == nil {
			return
		}
		e := p.event(EventDither, fmt.Sprintf("DITHER by %.3f, %.3f", ev.Dx, ev.Dy))
		e.Dx, e.Dy = ev.Dx, ev.Dy
		p.add(e)
	case "LockPositionSet":
		if p.cur == nil {
			return
		}
		e := p.event(EventSetLockPos, fmt.Sprintf("SET LOCK POSITION, new lock pos = %.3f, %.3f", ev.X, ev.Y))
		e.LockX, e.LockY = ev.X, ev.Y
		p.add(e)
	case "SettleBegin":
		if p.cur != nil {
			p.add(p.event(EventSettlingStart, "SETTLING STATE CHANGE, Settling started"))
		}
	case "SettleDone":
		if p.cur != nil {
			p.add(p.event(EventSettlingDone, "SETTLING STATE CHANGE, Settling complete"))
		}
	case "GuideParamChange":
		if p.cur == nil {
			return
		}
		e := p.event(EventParamChange, "Guiding parameter change, "+ev.Name+" = "+rawString(ev.Value))
		e.Key, e.Value = ev.Name, rawString(ev.Value)
		if e.Key == "MountGuidingEnabled" {
			p.sawOutputParam = true
		}
		p.add(e)
	case "Alert":
		if p.cur != nil {
			p.add(p.event(EventOther, ev.Msg))
		}
	case "Paused", "Resumed":
		if p.cur != nil {
			p.add(p.event(EventOther, ev.Event))
		}
	case "GuidingStopped":
		p.finish(ev.Timestamp)
	case SessionInfoEvent:
		var info SessionInfo
		if err := json.Unmarshal(line, &info); err != nil {
			return
		}
		if p.cur == nil {
			p.open(info.Timestamp, info.MidRun)
		}
		p.applyInfo(info)
	}
}

// applyInfo fills the header fields a guide log would have carried.
func (p *eventsParser) applyInfo(info SessionInfo) {
	s := p.cur
	if info.PixelScale > 0 {
		s.PixelScale = info.PixelScale
	}
	if info.ExposureMS > 0 {
		s.ExposureMS = info.ExposureMS
	}
	s.RAHours, s.DecDeg = info.RAHours, info.DecDeg
	if info.Profile != "" {
		s.Profile = info.Profile
	}
	if info.Camera != "" {
		s.Camera = info.Camera
	}
	if info.Mount != "" && s.Mount == "" {
		s.Mount = info.Mount
	}
	if info.Target != "" {
		s.Header["Target"] = info.Target
	}
	if info.PositionSource != "" {
		s.Header["Position source"] = info.PositionSource
	}
	if info.Warning != "" {
		s.Header["Warning"] = info.Warning
	}
	if info.MidRun {
		s.Header["Joined"] = "run already in progress when pec connected"
	}
	// Output already off when pec asked: the run was unguided from the
	// first sample, which no GuideParamChange will say.
	if info.GuideOutputEnabled != nil && !*info.GuideOutputEnabled && !p.sawOutputParam {
		p.add(Event{Kind: EventParamChange, After: -1, Key: "MountGuidingEnabled", Value: "false",
			Text: "Guiding parameter change, MountGuidingEnabled = false (already off when pec asked)"})
		p.sawOutputParam = true
	}
}

// rawString renders a JSON scalar as the text a guide log would show.
func rawString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	}
	return string(raw)
}

func unixTime(ts float64, loc *time.Location) time.Time {
	sec, frac := math.Modf(ts)
	return time.Unix(int64(sec), int64(math.Round(frac*1e9))).In(loc)
}
