package analysis

import (
	"strings"
	"testing"
	"time"

	"github.com/exploded/pec/internal/phd2"
)

// A live Guiding Assistant run has no "GA Result" line; guide output being
// off must be enough to count it as unguided.
func TestLiveSessionIsGA(t *testing.T) {
	l, err := phd2.LoadFile("../../testdata/phd2_events_excerpt.jsonl", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	sm := Summaries(l)
	if len(sm) != 1 || sm[0].Note != "Guiding Assistant run" || !sm[0].Guiding.IsGA || sm[0].UsableRows != 12 {
		t.Errorf("summary %+v", sm[0])
	}
	s := l.Sessions[0]
	_, warnings := s.MeasurementSamples()
	for _, w := range warnings {
		if strings.Contains(w, "corrections") {
			t.Errorf("live GA run warned %q", w)
		}
	}
	// The same session parsed from a guide log would need the GA Result line.
	s.Live = false
	if s.Guiding().IsGA {
		t.Error("without Live and without a GA Result the run is not a GA run")
	}
	if Summaries(l)[0].Note != "guiding off after frame 3" {
		t.Errorf("note %q", Summaries(l)[0].Note)
	}
}
