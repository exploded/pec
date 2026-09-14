package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/exploded/pec/internal/nina"
	"github.com/exploded/pec/internal/phd2live"
	"github.com/exploded/pec/internal/sky"
)

// --- The mount, through NINA ------------------------------------------------
//
// The slew below is the only mount command pec sends. It goes through the
// NINA Advanced API, after the user confirms in a dialog, and only when
// NINA reports the mount connected, unparked, tracking and still, the roof
// open if NINA has a roof device, and safe if it has a safety monitor.

const (
	slewMinAlt       = 20.0 // degrees; below this the slew is refused
	ninaStatusBudget = 1500 * time.Millisecond
	slewBudget       = 4 * time.Minute
)

// nina returns a client for the configured NINA, or nil when none is set.
func (s *Server) nina(ctx context.Context, timeout time.Duration) *nina.Client {
	base := s.ninaAPI(ctx)
	if base == "" {
		return nil
	}
	return nina.New(base, timeout)
}

type mountStatus struct {
	Configured bool // a NINA address is set
	Base       string
	Reachable  bool
	Err        string

	Connected, Tracking, AtPark, Slewing bool
	Name, TrackingMode, Epoch            string
	RAText, DecText                      string // native epoch
	SepDeg                               float64
	SepText                              string // from the pick

	RoofKnown, RoofOpen bool // a dome device is connected in NINA
	SafetyKnown, Safe   bool // a safety monitor is connected in NINA

	LastSlew string // "Sadalmelik at 21:05"
	Verdict  string // why the mount is not ready, or "ready"
	Ready    bool
}

// mountStatus asks NINA about the mount, roof and safety monitor, within
// one short budget so an absent NINA never stalls a page.
func (s *Server) mountStatus(ctx context.Context, budget time.Duration, pick *targetRow) *mountStatus {
	m := &mountStatus{Base: s.ninaAPI(ctx)}
	if name := s.st.Setting(ctx, settingTargetName); name != "" {
		if at, err := time.Parse(time.RFC3339, s.st.Setting(ctx, settingTargetAt)); err == nil {
			m.LastSlew = name + " at " + at.In(s.loc(ctx)).Format("15:04")
		}
	}
	c := s.nina(ctx, budget)
	if c == nil {
		return m
	}
	m.Configured = true
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var (
		wg    sync.WaitGroup
		mi    nina.MountInfo
		miErr error
		dome  nina.Dome
		dErr  error
		safe  nina.Safety
		sErr  error
	)
	wg.Add(3)
	go func() { defer wg.Done(); mi, miErr = c.MountInfo(ctx) }()
	go func() { defer wg.Done(); dome, dErr = c.Dome(ctx) }()
	go func() { defer wg.Done(); safe, sErr = c.SafetyMonitor(ctx) }()
	wg.Wait()
	if miErr != nil {
		m.Err = miErr.Error()
		return m
	}
	m.Reachable = true
	m.Connected, m.AtPark, m.Tracking, m.Slewing = mi.Connected, mi.AtPark, mi.TrackingEnabled, mi.Slewing
	m.Name, m.TrackingMode = mi.Name, mi.TrackingMode
	if m.Name == "" {
		m.Name = "mount"
	}
	if m.Connected {
		ra, dec, epoch := mi.Native()
		m.Epoch = strings.ToUpper(epoch)
		if m.Epoch == "" {
			m.Epoch = "native"
		}
		m.RAText, m.DecText = sky.FormatRA(ra), fmt.Sprintf("%+.2f°", dec)
		if pick != nil {
			ra2000, dec2000 := mi.J2000(time.Now())
			m.SepDeg = sky.Separation(ra2000, dec2000, pick.RAHours, pick.DecDeg)
			m.SepText = fmt.Sprintf("%.1f° from %s", m.SepDeg, pick.Name)
		}
	}
	if dErr == nil && dome.Connected {
		m.RoofKnown, m.RoofOpen = true, dome.Open()
	}
	if sErr == nil && safe.Connected {
		m.SafetyKnown, m.Safe = true, safe.IsSafe
	}
	switch {
	case !m.Connected:
		m.Verdict = "NINA has no mount connected"
	case m.AtPark:
		m.Verdict = "the mount is parked"
	case !m.Tracking:
		m.Verdict = "the mount is not tracking (home it and start tracking in TheSkyX first)"
	case m.Slewing:
		m.Verdict = "the mount is slewing"
	case m.RoofKnown && !m.RoofOpen:
		m.Verdict = "the roof reads closed"
	case m.SafetyKnown && !m.Safe:
		m.Verdict = "the safety monitor says unsafe"
	default:
		m.Verdict, m.Ready = "ready", true
	}
	return m
}

type slewResult struct {
	Star   string
	SepDeg float64
	OK     bool
	Text   string
}

// findStar looks a star up by name, Bayer designation or "HIP n".
func findStar(name string) (sky.Star, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, st := range sky.Stars {
		if name == strings.ToLower(st.Name) || name == strings.ToLower(st.Bayer) || name == "hip "+strconv.Itoa(st.HIP) {
			return st, true
		}
	}
	return sky.Star{}, false
}

// targetSlew sends the one mount command pec has: slew to the chosen star.
func (s *Server) targetSlew(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	ctx := r.Context()
	base := pageData{Title: "Where to point", Nav: "target"}
	refuse := func(msg string) { s.problem(w, r, "target", base, msg) }

	if r.FormValue("confirm") != "on" {
		refuse("tick the confirmation first")
		return
	}
	star, ok := findStar(r.FormValue("star"))
	if !ok {
		refuse("pec does not know a star called " + strings.TrimSpace(r.FormValue("star")))
		return
	}
	site, haveSite, err := s.siteSetting(ctx)
	if err != nil || !haveSite {
		refuse("set the site first")
		return
	}
	now := time.Now()
	alt := sky.Altitude(sky.HourAngle(sky.LST(now, site), star.RAHours), star.DecDeg, site.LatDeg)
	if alt < slewMinAlt {
		refuse(fmt.Sprintf("%s is only %.0f° up right now; pec will not slew below %.0f°", star.Name, alt, slewMinAlt))
		return
	}
	c := s.nina(ctx, slewBudget)
	if c == nil {
		refuse("NINA is not set up: enter the Advanced API address under Settings")
		return
	}
	m := s.mountStatus(ctx, 5*time.Second, nil)
	if !m.Reachable {
		refuse(m.Err)
		return
	}
	if !m.Ready {
		refuse("not slewing: " + m.Verdict)
		return
	}
	sctx, cancel := context.WithTimeout(ctx, slewBudget)
	defer cancel()
	s.log.Info("slew", "star", star.Name, "ra", star.RAHours, "dec", star.DecDeg)
	if err := c.Slew(sctx, star.RAHours*15, star.DecDeg); err != nil {
		refuse("the slew did not complete: " + err.Error())
		return
	}
	for k, v := range map[string]string{
		settingTargetName: star.Name,
		settingTargetRA:   strconv.FormatFloat(star.RAHours, 'f', -1, 64),
		settingTargetDec:  strconv.FormatFloat(star.DecDeg, 'f', -1, 64),
		settingTargetAt:   now.Format(time.RFC3339),
	} {
		if err := s.st.SetSetting(ctx, k, v); err != nil {
			s.fail(w, err)
			return
		}
	}
	v, err := s.targetData(r)
	if err != nil {
		refuse(err.Error())
		return
	}
	res := &slewResult{Star: star.Name, OK: true, Text: "Slewed to " + star.Name}
	if v.Mount != nil && v.Mount.Reachable && v.Mount.Connected {
		// Compare with the star just slewed to, whatever the pick is now.
		ra, dec := s.mountJ2000(ctx)
		if ra != 0 || dec != 0 {
			res.SepDeg = sky.Separation(ra, dec, star.RAHours, star.DecDeg)
			res.OK = res.SepDeg < 0.5
			res.Text = fmt.Sprintf("Mount reports %.1f arcmin from %s", res.SepDeg*60, star.Name)
			if !res.OK {
				res.Text = fmt.Sprintf("Mount reports %.1f° from %s: check TheSkyX before guiding", res.SepDeg, star.Name)
			}
		}
	}
	v.Slew = res
	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast":{"msg":"Slewed to %s"},"slewDone":{}}`, asciiOnly(star.Name)))
	s.page(w, r, "target", "target/_result", pageData{Title: "Where to point", Nav: "target", Data: v})
}

// mountJ2000 reads the mount's pointing in J2000; zeros when it cannot.
func (s *Server) mountJ2000(ctx context.Context) (float64, float64) {
	c := s.nina(ctx, 5*time.Second)
	if c == nil {
		return 0, 0
	}
	mi, err := c.MountInfo(ctx)
	if err != nil || !mi.Connected {
		return 0, 0
	}
	return mi.J2000(time.Now())
}

// ninaPosition tells the PHD2 recorder where the mount points, from NINA.
func (s *Server) ninaPosition(ctx context.Context) (phd2live.Position, bool) {
	c := s.nina(ctx, ninaStatusBudget)
	if c == nil {
		return phd2live.Position{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, ninaStatusBudget)
	defer cancel()
	mi, err := c.MountInfo(ctx)
	if err != nil || !mi.Connected {
		return phd2live.Position{}, false
	}
	ra, dec := mi.J2000(time.Now())
	p := phd2live.Position{RAHours: ra, DecDeg: dec, Source: "nina"}
	if at, err := time.Parse(time.RFC3339, s.st.Setting(ctx, settingTargetAt)); err == nil && time.Since(at) < 6*time.Hour {
		p.Target = s.st.Setting(ctx, settingTargetName)
	}
	return p, true
}
