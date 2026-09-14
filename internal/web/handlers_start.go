package web

import (
	"context"
	"fmt"
	"math"
	"net/http"

	"github.com/exploded/pec/internal/sky"
	"github.com/exploded/pec/internal/store"
)

// --- Start: the sequence with live status ------------------------------------

type startStep struct {
	N      int
	Name   string
	Href   string
	Status string
	Done   bool
	Next   bool
	Check  bool // needed when only checking the current PEC
}

type startView struct {
	Steps    []startStep
	NextText string
	NextHref string
	Empty    bool // nothing measured yet
}

// startData works out where the user is in the sequence from what the
// store holds. No new queries: the list queries the step pages use.
func (s *Server) startData(ctx context.Context) (startView, error) {
	v := startView{}
	steps := []startStep{
		{N: 1, Name: "Point", Href: "/target", Check: true},
		{N: 2, Name: "Record", Href: "/tonight", Check: true},
		{N: 3, Name: "Capture", Href: "/capture"},
		{N: 4, Name: "Runs", Href: "/analyse", Check: true},
		{N: 5, Name: "Verify", Href: "/verify", Check: true},
		{N: 6, Name: "Fit", Href: "/fit"},
	}

	// 1 Point
	site, haveSite, _ := s.siteSetting(ctx)
	if haveSite {
		steps[0].Done = true
		steps[0].Status = fmt.Sprintf("site %.4f %s, %.4f %s; the page names a star to slew to", math.Abs(site.LatDeg), hemi(site.LatDeg, "N", "S"), math.Abs(site.LonDeg), hemi(site.LonDeg, "E", "W"))
	} else {
		steps[0].Status = "not yet: set the site once, then the page names a star to slew to"
	}

	// 2 Record
	steps[1].Status = "the night checklist: mount, PHD2 Guiding Assistant runs, and the USB capture if you are fitting a new table"

	// 3 Capture
	var haveAnchor bool
	if caps, err := s.st.Q.ListCaptures(ctx, 1); err == nil && len(caps) > 0 {
		c := caps[0]
		haveAnchor = c.AnchorID.Valid
		steps[2].Done = true
		steps[2].Status = fmt.Sprintf("capture %d: period %.3f s", c.ID, c.PeriodS)
		if c.AnchorID.Valid {
			steps[2].Status += fmt.Sprintf(", anchor #%d", c.AnchorID.Int64)
		} else {
			steps[2].Status += ", no index readings (no anchor)"
		}
	} else if anchors, err := s.st.Q.ListAnchors(ctx, 1); err == nil && len(anchors) > 0 {
		haveAnchor = true
		steps[2].Done = true
		steps[2].Status = fmt.Sprintf("anchor #%d typed by hand, index %d", anchors[0].ID, anchors[0].PecIndex)
	} else {
		steps[2].Status = "not yet: needed only to fit a new table"
	}

	// 4 Runs
	var off, on, unknown int
	runs, err := s.st.Q.ListAnalyseRuns(ctx, 200)
	if err != nil {
		return v, err
	}
	for _, r := range runs {
		switch p := store.PecOn(r.PecOn); {
		case p == nil:
			unknown++
		case *p:
			on++
		default:
			off++
		}
	}
	tables, _ := s.st.Q.ListTableRuns(ctx, 1)
	switch {
	case len(runs) == 0 && len(tables) == 0:
		steps[3].Status = "not yet: analyse the Guiding Assistant sessions from the guide log"
	default:
		steps[3].Status = fmt.Sprintf("%d guide-log run%s: %d PEC off, %d PEC on, %d unmarked", len(runs), plural(len(runs)), off, on, unknown)
		if len(tables) > 0 {
			steps[3].Status += "; the TCS table is on file"
		}
		steps[3].Done = off > 0 || on > 0
	}

	// 5 Verify
	if off > 0 && on > 0 {
		steps[4].Status = "ready: compare a PEC-off run with a PEC-on run"
	} else {
		steps[4].Status = "needs a PEC-off run and a PEC-on run"
	}

	// 6 Fit
	var fitID int64
	if fits, err := s.st.Q.ListFits(ctx, 1); err == nil && len(fits) > 0 {
		f := fits[0]
		fitID = f.ID
		steps[5].Done = true
		steps[5].Status = fmt.Sprintf("table #%d: %d ticks peak-to-peak", f.ID, f.P2pTicks)
		if f.PhaseErrDeg > 0 {
			steps[5].Status += fmt.Sprintf(", phase ± %.1f°", f.PhaseErrDeg)
		}
	} else {
		steps[5].Status = "not yet: writes the paste-ready table from a PEC-off run and the anchor"
	}

	// What to do next.
	next := 0
	switch {
	case !haveSite:
		next, v.NextText, v.NextHref = 1, "Set the site on the Point page", "/target"
	case len(runs) == 0 && !haveAnchor:
		next, v.NextText, v.NextHref = 2, "Record a night: Guiding Assistant runs with PEC off and on, with a USB capture if you want a new table", "/tonight"
	case len(runs) == 0:
		next, v.NextText, v.NextHref = 4, "Analyse the guide-log sessions on the Runs page", "/analyse"
	case fitID > 0:
		v.NextText, v.NextHref = fmt.Sprintf("Review table #%d, then paste it into the TCS by hand and verify it with a new PEC-on run", fitID), fmt.Sprintf("/fits/%d", fitID)
	case off > 0 && on > 0 && !haveAnchor:
		next, v.NextText, v.NextHref = 5, "Verify the two runs; to fit a new table, upload a USB capture first", "/verify"
	case off > 0 && on > 0:
		next, v.NextText, v.NextHref = 5, "Verify the two runs, then Fit a table from the PEC-off run", "/verify"
	case off > 0 && haveAnchor:
		next, v.NextText, v.NextHref = 6, "Fit a table from the PEC-off run with the anchor", "/fit"
	case off > 0:
		next, v.NextText, v.NextHref = 3, "Upload the USB capture for the anchor, or record a PEC-on run to Verify", "/capture"
	default:
		next, v.NextText, v.NextHref = 4, "Analyse a PEC-off Guiding Assistant run", "/analyse"
	}
	for i := range steps {
		steps[i].Next = steps[i].N == next
	}
	v.Steps = steps
	v.Empty = !haveSite && len(runs) == 0 && !haveAnchor && fitID == 0
	return v, nil
}

// siteSetting returns the remembered site, if any.
func (s *Server) siteSetting(ctx context.Context) (sky.Site, bool, error) {
	lat, lon := s.st.Setting(ctx, settingLat), s.st.Setting(ctx, settingLon)
	if lat == "" && lon == "" {
		return sky.Site{}, false, nil
	}
	site, err := parseSite(lat, lon)
	if err != nil {
		return sky.Site{}, false, err
	}
	return site, true, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (s *Server) startPage(w http.ResponseWriter, r *http.Request) {
	v, err := s.startData(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	s.page(w, r, "start", "", pageData{Title: "Start", Nav: "home", Data: v})
}
