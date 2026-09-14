package phd2

import (
	"os"
	"strings"
	"testing"
	"time"
)

const fixtureBegins = 1789383900 // the Begins the fixture encodes (steps carry Timestamp = Begins + Time + 0.12)

func melbourne(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Australia/Melbourne")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestParseEvents(t *testing.T) {
	l, err := LoadFile("../../testdata/phd2_events_excerpt.jsonl", melbourne(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Versions) != 1 || l.Versions[0] != "PHD2 version 2.6.14dev1" {
		t.Errorf("versions %v", l.Versions)
	}
	if len(l.Sessions) != 1 {
		t.Fatalf("sessions %d, want 1", len(l.Sessions))
	}
	s := l.Sessions[0]
	if !s.Live || s.Index != 1 {
		t.Errorf("Live %v Index %d", s.Live, s.Index)
	}
	want := time.Unix(fixtureBegins, 120_000_000)
	if d := s.Begins.Sub(want); d < -time.Millisecond || d > time.Millisecond {
		t.Errorf("Begins %v, want %v", s.Begins, want)
	}
	if got := s.Begins.Format("2006-01-02 15:04:05"); got != "2026-09-14 21:05:00" {
		t.Errorf("Begins in Melbourne %s", got)
	}
	if s.Ends.IsZero() || s.Ends.Sub(s.Begins) < 41*time.Second {
		t.Errorf("Ends %v", s.Ends)
	}
	if len(s.Samples) != 15 {
		t.Fatalf("samples %d, want 15", len(s.Samples))
	}
	if s.PixelScale != 1.43 || s.ExposureMS != 2000 || s.DecDeg != -0.3197 || s.Profile != "AT12IN" || s.Mount != "On Camera" {
		t.Errorf("header: scale %v exp %d dec %v profile %q mount %q", s.PixelScale, s.ExposureMS, s.DecDeg, s.Profile, s.Mount)
	}
	if s.Header["Target"] != "Sadalmelik" || s.Header["Position source"] != "nina" {
		t.Errorf("header map %v", s.Header)
	}
	first := s.Samples[0]
	if first.Frame != 1 || first.Offset != 2.5 || first.RADuration != 120 || first.RADirection != "West" {
		t.Errorf("first sample %+v", first)
	}
	if s.Samples[3].RADuration != 0 {
		t.Errorf("sample 4 should carry no pulse: %+v", s.Samples[3])
	}
	if at := s.Samples[0].At.Sub(s.Begins).Seconds(); at != 2.5 {
		t.Errorf("At offset %v", at)
	}
	g := s.Guiding()
	if !g.Disabled || g.DisabledAfter != 2 || g.ReenabledAfter != 14 || !g.IsGA || g.Corrections != 3 {
		t.Errorf("guiding %+v", g)
	}
	if s.Dithers() != 1 || s.Drops() != 1 {
		t.Errorf("dithers %d drops %d", s.Dithers(), s.Drops())
	}
	if b := s.Breaks(); len(b) != 1 || b[0] != 30 {
		t.Errorf("breaks %v", b)
	}
	rows, warnings := s.MeasurementSamples()
	if len(rows) != 12 {
		t.Errorf("measurement rows %d, want 12", len(rows))
	}
	for _, w := range warnings {
		if strings.Contains(w, "carry RA corrections") {
			t.Errorf("unexpected warning %q", w)
		}
	}
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], "3 samples before") {
		t.Errorf("warnings %v", warnings)
	}
}

func TestParseEventsOutputDisabledAtStart(t *testing.T) {
	stream := `{"Event":"Version","Timestamp":100,"PHDVersion":"2.6.14","PHDSubver":""}
{"Event":"StartGuiding","Timestamp":100.5}
{"Event":"pec.SessionInfo","Timestamp":101,"PixelScale":1.43,"ExposureMS":2000,"DecDeg":-5,"PositionSource":"target","GuideOutputEnabled":false}
{"Event":"GuideStep","Timestamp":103,"Frame":1,"Time":2.5,"dx":0.1,"dy":0,"RADistanceRaw":0.1,"DECDistanceRaw":0,"RADistanceGuide":0,"DECDistanceGuide":0,"StarMass":1,"SNR":30,"HFD":2,"AvgDist":0.1}
{"Event":"GuideStep","Timestamp":105.5,"Frame":2,"Time":5,"dx":0.2,"dy":0,"RADistanceRaw":0.2,"DECDistanceRaw":0,"RADistanceGuide":0,"DECDistanceGuide":0,"StarMass":1,"SNR":30,"HFD":2,"AvgDist":0.1}
`
	l, err := ParseEvents(strings.NewReader(stream), "test", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	s := l.Sessions[0]
	g := s.Guiding()
	if !g.Disabled || g.DisabledAfter != -1 || !g.IsGA {
		t.Errorf("guiding %+v", g)
	}
	if rows, _ := s.MeasurementSamples(); len(rows) != 2 {
		t.Errorf("rows %d", len(rows))
	}
	if !s.Ends.IsZero() {
		t.Errorf("Ends should be zero without GuidingStopped: %v", s.Ends)
	}
	if got := s.Begins.Unix(); got != 100 { // 103 - 2.5, whole seconds
		t.Errorf("Begins %d", got)
	}
}

func TestParseEventsMidRun(t *testing.T) {
	stream := `{"Event":"Version","Timestamp":1000,"PHDVersion":"2.6.14","PHDSubver":""}
{"Event":"StartGuiding","Timestamp":1000}
{"Event":"AppState","Timestamp":1000,"State":"Guiding"}
{"Event":"GuideStep","Timestamp":1001,"Frame":100,"Time":250,"RADistanceRaw":0.1}
{"Event":"GuideStep","Timestamp":1003.5,"Frame":101,"Time":252.5,"RADistanceRaw":0.2}
{"Event":"GuidingStopped","Timestamp":1010}
`
	l, err := ParseEvents(strings.NewReader(stream), "test", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Sessions) != 1 {
		t.Fatalf("sessions %d", len(l.Sessions))
	}
	s := l.Sessions[0]
	if got := s.Begins.Unix(); got != 751 {
		t.Errorf("Begins %d, want 751 (1001 - 250)", got)
	}
	if len(s.Samples) != 2 || s.Ends.Unix() != 1010 {
		t.Errorf("samples %d ends %v", len(s.Samples), s.Ends)
	}
}

func TestParseEventsTruncated(t *testing.T) {
	data, err := os.ReadFile("../../testdata/phd2_events_excerpt.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	cut := strings.TrimRight(string(data), "\r\n")
	cut = cut[:len(cut)-9] // inside the GuidingStopped line
	l, err := ParseEvents(strings.NewReader(cut), "cut", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	s := l.Sessions[0]
	if len(s.Samples) != 15 || !s.Ends.IsZero() {
		t.Errorf("samples %d ends %v", len(s.Samples), s.Ends)
	}
	if _, err := ParseEvents(strings.NewReader("not json at all\n"), "x", time.UTC); err == nil {
		t.Error("garbage should fail")
	}
}

func TestLoadFileSniff(t *testing.T) {
	loc := melbourne(t)
	if l, err := LoadFile("../../testdata/guidelog_excerpt.txt", loc); err != nil || len(l.Sessions) != 4 {
		t.Errorf("guide log via LoadFile: %v", err)
	}
	if l, err := LoadFile("../../testdata/phd2_events_excerpt.jsonl", loc); err != nil || len(l.Sessions) != 1 || !l.Sessions[0].Live {
		t.Errorf("recording via LoadFile: %v", err)
	}
	if !IsEventsStream([]byte("  \n{\"Event\":\"Version\"}")) || IsEventsStream([]byte("PHD2 version")) {
		t.Error("IsEventsStream")
	}
}
