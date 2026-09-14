package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

// fakeNINA stands in for the Advanced API plugin. Its state is a plain
// struct the test mutates between requests.
type fakeNINA struct {
	*httptest.Server
	mu sync.Mutex

	Connected, AtPark, Tracking, Slewing bool
	RAHours, DecDeg                      float64
	Epoch                                string
	HasDome, RoofOpen                    bool
	HasSafety, Safe                      bool
	SlewFails                            bool
	Filters                              []string
	Filter                               int

	Slews   []url.Values
	Changes []string
}

func newFakeNINA(t *testing.T) *fakeNINA {
	t.Helper()
	f := &fakeNINA{Connected: true, Tracking: true, Epoch: "JNOW", RAHours: 12, DecDeg: -20, Filters: []string{"Luminance", "R", "G", "B"}, Filter: 2}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeNINA) reply(w http.ResponseWriter, status int, resp any, errMsg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"Response": resp, "Error": errMsg, "StatusCode": status, "Success": errMsg == "", "Type": "API"})
}

func (f *fakeNINA) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/v2/api/version":
		f.reply(w, 200, "2.2.15.0", "")
	case "/v2/api/equipment/mount/info":
		var alt any = 45.0
		if !f.Connected {
			alt = "NaN" // what the real plugin writes with nothing connected
		}
		f.reply(w, 200, map[string]any{
			"Altitude": alt, "Azimuth": alt, "TrackingRate": map[string]any{},
			"Connected": f.Connected, "Name": "Telescope Simulator", "AtPark": f.AtPark, "TrackingEnabled": f.Tracking,
			"TrackingMode": "Siderial", "Slewing": f.Slewing, "RightAscension": f.RAHours, "Declination": f.DecDeg,
			"Coordinates":      map[string]any{"RA": f.RAHours, "RADegrees": f.RAHours * 15, "Dec": f.DecDeg, "Epoch": f.Epoch},
			"EquatorialSystem": f.Epoch, "SiteLatitude": -37.8, "SiteLongitude": 145.0,
		}, "")
	case "/v2/api/equipment/mount/slew":
		q := r.URL.Query()
		f.Slews = append(f.Slews, q)
		switch {
		case !f.Connected:
			f.reply(w, 409, "", "Mount not connected")
		case f.AtPark:
			f.reply(w, 409, "", "Mount parked")
		case f.SlewFails:
			f.reply(w, 200, "Slew failed", "")
		default:
			var ra, dec float64
			fmt.Sscan(q.Get("ra"), &ra)
			fmt.Sscan(q.Get("dec"), &dec)
			f.RAHours, f.DecDeg, f.Epoch = ra/15, dec, "J2000"
			f.reply(w, 200, "Slew finished", "")
		}
	case "/v2/api/equipment/dome/info":
		status := "ShutterClosed"
		if f.RoofOpen {
			status = "ShutterOpen"
		}
		f.reply(w, 200, map[string]any{"Connected": f.HasDome, "ShutterStatus": status}, "")
	case "/v2/api/equipment/safetymonitor/info":
		f.reply(w, 200, map[string]any{"Connected": f.HasSafety, "IsSafe": f.Safe}, "")
	case "/v2/api/equipment/filterwheel/info":
		var avail []map[string]any
		for i, n := range f.Filters {
			avail = append(avail, map[string]any{"Name": n, "Id": i})
		}
		sel := map[string]any{"Name": "", "Id": -1}
		if f.Filter >= 0 && f.Filter < len(f.Filters) {
			sel = map[string]any{"Name": f.Filters[f.Filter], "Id": f.Filter}
		}
		f.reply(w, 200, map[string]any{"Connected": len(f.Filters) > 0, "IsMoving": false, "SelectedFilter": sel, "AvailableFilters": avail}, "")
	case "/v2/api/equipment/filterwheel/change-filter":
		id := r.URL.Query().Get("filterId")
		f.Changes = append(f.Changes, id)
		fmt.Sscan(id, &f.Filter)
		f.reply(w, 200, "Filter changed", "")
	case "/v2/api/equipment/camera/info":
		f.reply(w, 200, map[string]any{"Connected": true, "CoolerOn": true, "Temperature": -9.5, "TemperatureSetPoint": -10}, "")
	default:
		f.reply(w, 404, "", "Unknown path "+r.URL.Path)
	}
}
