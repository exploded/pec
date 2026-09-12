package sky

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// NINAProfilesDir is where NINA keeps its profiles on Windows.
func NINAProfilesDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		if home, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(home, "AppData", "Local")
		}
	}
	return filepath.Join(base, "NINA", "Profiles")
}

// NINASite reads the site from the most recently modified NINA profile in
// dir. It returns the site and the profile's name. Profiles are JSON in
// current NINA versions and XML in old ones; both carry the values under
// AstrometrySettings as Latitude and Longitude (east-positive degrees).
func NINASite(dir string) (Site, string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Site{}, "", fmt.Errorf("no NINA profiles at %s", dir)
	}
	var newest string
	var newestMod int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".profile") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if m := info.ModTime().UnixNano(); newest == "" || m > newestMod {
			newest, newestMod = filepath.Join(dir, e.Name()), m
		}
	}
	if newest == "" {
		return Site{}, "", fmt.Errorf("no .profile files in %s", dir)
	}
	data, err := os.ReadFile(newest)
	if err != nil {
		return Site{}, "", err
	}
	s, name, err := parseNINAProfile(data)
	if err != nil {
		return Site{}, "", fmt.Errorf("%s: %w", filepath.Base(newest), err)
	}
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(newest), filepath.Ext(newest))
	}
	return s, name, nil
}

var (
	xmlLat  = regexp.MustCompile(`<Latitude>\s*([-+0-9.eE]+)\s*</Latitude>`)
	xmlLon  = regexp.MustCompile(`<Longitude>\s*([-+0-9.eE]+)\s*</Longitude>`)
	xmlName = regexp.MustCompile(`<Name>([^<]*)</Name>`)
)

func parseNINAProfile(data []byte) (Site, string, error) {
	var doc struct {
		Name               string `json:"Name"`
		AstrometrySettings struct {
			Latitude  *float64 `json:"Latitude"`
			Longitude *float64 `json:"Longitude"`
		} `json:"AstrometrySettings"`
	}
	if err := json.Unmarshal(data, &doc); err == nil {
		a := doc.AstrometrySettings
		if a.Latitude == nil || a.Longitude == nil {
			return Site{}, "", errors.New("profile has no AstrometrySettings latitude/longitude")
		}
		return checkSite(Site{LatDeg: *a.Latitude, LonDeg: *a.Longitude}, doc.Name)
	}
	// Old XML profile.
	lat, lon := xmlLat.FindSubmatch(data), xmlLon.FindSubmatch(data)
	if lat == nil || lon == nil {
		return Site{}, "", errors.New("profile is neither JSON nor XML with Latitude/Longitude")
	}
	la, err1 := strconv.ParseFloat(string(lat[1]), 64)
	lo, err2 := strconv.ParseFloat(string(lon[1]), 64)
	if err1 != nil || err2 != nil {
		return Site{}, "", errors.New("profile latitude/longitude are not numbers")
	}
	name := ""
	if m := xmlName.FindSubmatch(data); m != nil {
		name = string(m[1])
	}
	return checkSite(Site{LatDeg: la, LonDeg: lo}, name)
}

func checkSite(s Site, name string) (Site, string, error) {
	if s.LatDeg == 0 && s.LonDeg == 0 {
		return Site{}, "", errors.New("profile site is 0, 0 (not set in NINA)")
	}
	if s.LatDeg < -90 || s.LatDeg > 90 || s.LonDeg < -180 || s.LonDeg > 180 {
		return Site{}, "", fmt.Errorf("profile site %.4f, %.4f is out of range", s.LatDeg, s.LonDeg)
	}
	return s, name, nil
}
