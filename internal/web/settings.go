package web

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/exploded/pec/internal/sky"
)

// Settings the user can change live in the settings table and are read per
// request, so a change takes effect without a restart. Options hold the
// defaults (tests set them; the exe never does).
const (
	settingLat     = "site.lat"
	settingLon     = "site.lon"
	settingPHD2Dir = "phd2.dir"
	settingNINADir = "nina.dir"
	settingTZ      = "tz"
)

// phd2Dir is the folder the Runs page lists guide logs from.
func (s *Server) phd2Dir(ctx context.Context) string {
	if v := strings.TrimSpace(s.st.Setting(ctx, settingPHD2Dir)); v != "" {
		return v
	}
	return s.opt.PHD2Dir
}

// ninaDir is the folder "Read from NINA" looks in.
func (s *Server) ninaDir(ctx context.Context) string {
	if v := strings.TrimSpace(s.st.Setting(ctx, settingNINADir)); v != "" {
		return v
	}
	return s.opt.NINADir
}

// loc is the zone PHD2 guide logs were written in. An empty or "Local"
// setting means the machine's zone (Options.Loc); a bad name falls back to
// it too, so a hand-edited database cannot break the pages.
func (s *Server) loc(ctx context.Context) *time.Location {
	name := strings.TrimSpace(s.st.Setting(ctx, settingTZ))
	if name == "" || name == "Local" {
		return s.opt.Loc
	}
	s.tzMu.Lock()
	defer s.tzMu.Unlock()
	if s.tzName == name && s.tzLoc != nil {
		return s.tzLoc
	}
	l, err := time.LoadLocation(name)
	if err != nil {
		return s.opt.Loc
	}
	s.tzName, s.tzLoc = name, l
	return l
}

// parseSite validates typed latitude and longitude (decimal degrees, south
// and west negative).
func parseSite(lat, lon string) (sky.Site, error) {
	la, err1 := strconv.ParseFloat(strings.TrimSpace(lat), 64)
	lo, err2 := strconv.ParseFloat(strings.TrimSpace(lon), 64)
	switch {
	case err1 != nil || la < -90 || la > 90:
		return sky.Site{}, fmt.Errorf("latitude must be a number from -90 to 90 (south negative)")
	case err2 != nil || lo < -180 || lo > 180:
		return sky.Site{}, fmt.Errorf("longitude must be a number from -180 to 180 (west negative)")
	}
	return sky.Site{LatDeg: la, LonDeg: lo}, nil
}

// saveSite remembers the site for the Target and Start pages.
func (s *Server) saveSite(ctx context.Context, site sky.Site) error {
	if err := s.st.SetSetting(ctx, settingLat, strconv.FormatFloat(site.LatDeg, 'f', -1, 64)); err != nil {
		return err
	}
	return s.st.SetSetting(ctx, settingLon, strconv.FormatFloat(site.LonDeg, 'f', -1, 64))
}

// --- Settings page ---------------------------------------------------------

type settingsView struct {
	PHD2Dir, NINADir, TZ, Lat, Lon string // stored values ("" = default)
	DefaultPHD2Dir, DefaultNINADir string
	PHD2Found, NINAFound           bool   // the effective folder exists
	Zone                           string // the effective zone name
	Saved                          bool
}

func (s *Server) settingsData(ctx context.Context) settingsView {
	v := settingsView{
		PHD2Dir: s.st.Setting(ctx, settingPHD2Dir), NINADir: s.st.Setting(ctx, settingNINADir),
		TZ: s.st.Setting(ctx, settingTZ), Lat: s.st.Setting(ctx, settingLat), Lon: s.st.Setting(ctx, settingLon),
		DefaultPHD2Dir: s.opt.PHD2Dir, DefaultNINADir: s.opt.NINADir,
		Zone: s.loc(ctx).String(),
	}
	v.PHD2Found = dirExists(s.phd2Dir(ctx))
	v.NINAFound = dirExists(s.ninaDir(ctx))
	return v
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	s.page(w, r, "settings", "", pageData{Title: "Settings", Nav: "settings", Data: s.settingsData(r.Context())})
}

// settingsSave validates and stores every field, then re-renders the form.
func (s *Server) settingsSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	base := pageData{Title: "Settings", Nav: "settings", Data: s.settingsData(ctx)}
	tz := strings.TrimSpace(r.FormValue("tz"))
	if tz != "" && tz != "Local" {
		if _, err := time.LoadLocation(tz); err != nil {
			s.problem(w, r, "settings", base, fmt.Sprintf("time zone %q is not an IANA name such as Australia/Melbourne; leave it empty for this PC's zone", tz))
			return
		}
	}
	lat, lon := strings.TrimSpace(r.FormValue("lat")), strings.TrimSpace(r.FormValue("lon"))
	var site *sky.Site
	if lat != "" || lon != "" {
		st, err := parseSite(lat, lon)
		if err != nil {
			s.problem(w, r, "settings", base, err.Error())
			return
		}
		site = &st
	}
	for k, v := range map[string]string{
		settingPHD2Dir: strings.TrimSpace(r.FormValue("phd2_dir")),
		settingNINADir: strings.TrimSpace(r.FormValue("nina_dir")),
		settingTZ:      tz,
	} {
		if err := s.st.SetSetting(ctx, k, v); err != nil {
			s.fail(w, err)
			return
		}
	}
	if site != nil {
		if err := s.saveSite(ctx, *site); err != nil {
			s.fail(w, err)
			return
		}
	}
	v := s.settingsData(ctx)
	v.Saved = true
	w.Header().Set("HX-Trigger", `{"showToast": {"msg": "Settings saved"}}`)
	s.page(w, r, "settings", "settings/_form", pageData{Title: "Settings", Nav: "settings", Data: v})
}
