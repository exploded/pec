package web

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/exploded/pec/internal/sky"
)

// --- Target: where to point for a PEC run ----------------------------------

type targetRow struct {
	sky.Candidate
	RAText, DecText, HAText, AltText, MagText string
	StatusText                                string
	Class                                     string // good | soon | dim
}

type targetView struct {
	Lat, Lon  string // form values
	At        string // datetime-local value when planning ahead; "" = now
	HasSite   bool
	SiteText  string // "37.81 S, 144.96 E"
	Plan      *sky.Plan
	Window    sky.Window
	AtText    string
	LSTText   string
	IdealText string
	MoonText  string
	MoonWarn  bool
	Pick      *targetRow
	Next      *targetRow // first "soon" when nothing is good
	Rows      []targetRow
	Later     []targetRow // past or never usable, collapsed
	Live      bool        // refresh every minute
	NINADir   string
}

// targetSite reads the site from the form or query, falling back to the
// remembered setting. ok is false when no site is known yet.
func (s *Server) targetSite(r *http.Request) (v targetView, site sky.Site, ok bool, err error) {
	ctx := r.Context()
	v.Lat = strings.TrimSpace(r.FormValue("lat"))
	v.Lon = strings.TrimSpace(r.FormValue("lon"))
	v.At = strings.TrimSpace(r.FormValue("at"))
	v.NINADir = s.ninaDir(ctx)
	if v.Lat == "" && v.Lon == "" {
		v.Lat, v.Lon = s.st.Setting(ctx, settingLat), s.st.Setting(ctx, settingLon)
	}
	if v.Lat == "" && v.Lon == "" {
		return v, site, false, nil
	}
	site, err = parseSite(v.Lat, v.Lon)
	if err != nil {
		return v, site, false, err
	}
	return v, site, true, nil
}

func (s *Server) targetData(r *http.Request) (targetView, error) {
	v, site, ok, err := s.targetSite(r)
	if err != nil || !ok {
		return v, err
	}
	v.HasSite = true
	v.SiteText = fmt.Sprintf("%.4f %s, %.4f %s", math.Abs(site.LatDeg), hemi(site.LatDeg, "N", "S"), math.Abs(site.LonDeg), hemi(site.LonDeg, "E", "W"))
	at := time.Now()
	v.Live = true
	if v.At != "" {
		t, err := parseLocal(v.At, s.loc(r.Context()))
		if err != nil {
			return v, fmt.Errorf("the time must look like 2026-09-12T21:00 (local time)")
		}
		at, v.Live = t, false
	}
	w := sky.DefaultWindow()
	p := sky.PlanAt(at, site, w)
	v.Plan, v.Window = &p, w
	v.AtText = at.In(s.loc(r.Context())).Format("Mon 2 Jan 15:04:05 MST")
	v.LSTText = sky.FormatRA(p.LST)
	v.IdealText = fmt.Sprintf("RA %s, Dec 0", sky.FormatRA(p.IdealRA))
	if p.Moon.AltDeg > 0 {
		v.MoonText = fmt.Sprintf("up, altitude %.0f, %.0f %% lit", p.Moon.AltDeg, p.Moon.Illum*100)
		v.MoonWarn = p.MoonBright
	} else {
		v.MoonText = fmt.Sprintf("below the horizon (%.0f %% lit)", p.Moon.Illum*100)
	}
	for _, c := range p.Candidates {
		row := targetRow{
			Candidate: c,
			RAText:    sky.FormatRA(c.RAHours), DecText: fmt.Sprintf("%+.1f", c.DecDeg),
			HAText: sky.FormatHA(c.HAHours), AltText: fmt.Sprintf("%.0f", c.AltDeg), MagText: fmt.Sprintf("%.1f", c.Mag),
		}
		switch c.Status {
		case sky.StatusGood:
			row.StatusText, row.Class = fmt.Sprintf("in the window for another %d min", int(c.GoodFor.Minutes())), "good"
		case sky.StatusSoon:
			row.StatusText, row.Class = fmt.Sprintf("enters the window in %d min", int(c.GoodIn.Minutes())), "soon"
		case sky.StatusLow:
			row.StatusText, row.Class = "in the window but too low", "dim"
		case sky.StatusMoon:
			row.StatusText, row.Class = fmt.Sprintf("in the window but only %.0f from the Moon", c.MoonSep), "dim"
		case sky.StatusPast:
			row.StatusText, row.Class = "too close to or past the meridian", "dim"
		default:
			row.StatusText, row.Class = "never high enough from this site", "dim"
		}
		if c.Recommend {
			r := row
			v.Pick = &r
		}
		if c.Status == sky.StatusPast || c.Status == sky.StatusUnder {
			v.Later = append(v.Later, row)
		} else {
			v.Rows = append(v.Rows, row)
			if v.Next == nil && c.Status == sky.StatusSoon {
				r := row
				v.Next = &r
			}
		}
	}
	return v, nil
}

func hemi(v float64, pos, neg string) string {
	if v < 0 {
		return neg
	}
	return pos
}

func (s *Server) targetPage(w http.ResponseWriter, r *http.Request) {
	v, err := s.targetData(r)
	d := pageData{Title: "Where to point", Nav: "target", Data: v}
	if err != nil {
		s.problem(w, r, "target", d, err.Error())
		return
	}
	s.page(w, r, "target", "target/_result", d)
}

// targetSave remembers the site and answers with the plan.
func (s *Server) targetSave(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	v, site, ok, err := s.targetSite(r)
	d := pageData{Nav: "target", Data: v}
	switch {
	case err != nil:
		s.problem(w, r, "target", d, err.Error())
		return
	case !ok:
		s.problem(w, r, "target", d, "enter the site latitude and longitude, or read them from NINA")
		return
	}
	if err := s.saveSite(r.Context(), site); err != nil {
		s.fail(w, err)
		return
	}
	s.targetPage(w, r)
}

// targetNINA reads the site from the newest NINA profile, remembers it and
// reloads the page so the form shows the values.
func (s *Server) targetNINA(w http.ResponseWriter, r *http.Request) {
	site, name, err := sky.NINASite(s.ninaDir(r.Context()))
	if err != nil {
		s.problem(w, r, "target", pageData{Nav: "target"}, "could not read the site from NINA: "+err.Error())
		return
	}
	if err := s.saveSite(r.Context(), site); err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast":{"msg":"Site read from NINA profile %q"}}`, asciiOnly(name)))
	redirect(w, r, "/target")
}

// asciiOnly strips anything a header cannot carry.
func asciiOnly(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c >= 32 && c < 127 && c != '"' && c != '\\' {
			b.WriteRune(c)
		}
	}
	return b.String()
}
