package nina

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fake answers routes from a map; the last query of each route is kept.
type fake struct {
	routes  map[string]string
	queries map[string]url.Values
	paths   []string
}

func newFake(t *testing.T) (*fake, *Client) {
	t.Helper()
	f := &fake{routes: map[string]string{}, queries: map[string]url.Values{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.paths = append(f.paths, r.URL.Path)
		f.queries[r.URL.Path] = r.URL.Query()
		body, ok := f.routes[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"Response":"","Error":"Unknown path","StatusCode":404,"Success":false,"Type":"API"}`)
			return
		}
		if strings.Contains(body, `"StatusCode":409`) {
			w.WriteHeader(409)
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return f, New(srv.URL+"/v2/api/", time.Second)
}

func TestMountInfoAndEpoch(t *testing.T) {
	f, c := newFake(t)
	f.routes["/v2/api/equipment/mount/info"] = `{"Response":{"Connected":true,"Name":"Telescope Simulator","AtPark":false,"TrackingEnabled":true,"TrackingMode":"Siderial","Slewing":false,"RightAscension":22.1234,"Declination":-0.5,"Coordinates":{"RA":22.1234,"RAString":"22:07:24","RADegrees":331.851,"Dec":-0.5,"DecString":"-00:30:00","Epoch":"JNOW"},"EquatorialSystem":"JNOW","SiteLatitude":-37.8,"SiteLongitude":145.0},"Error":"","StatusCode":200,"Success":true,"Type":"API"}`
	m, err := c.MountInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !m.Connected || !m.TrackingEnabled || m.AtPark || m.SiteLatitude != -37.8 || m.Coordinates.Epoch != "JNOW" {
		t.Errorf("mount %+v", m)
	}
	at := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
	ra, dec := m.J2000(at)
	// 26 years of precession near the equator: RA back by about 0.022 h.
	if d := 22.1234 - ra; d < 0.020 || d > 0.025 {
		t.Errorf("J2000 RA %.4f from JNOW 22.1234 (delta %.4f)", ra, d)
	}
	if math.Abs(dec-(-0.5)) > 0.2 {
		t.Errorf("J2000 Dec %.4f", dec)
	}
	m.Coordinates.Epoch = "J2000"
	if ra, _ := m.J2000(at); ra != 22.1234 {
		t.Errorf("J2000 epoch should pass through: %v", ra)
	}
	if !strings.HasSuffix(c.Base, ":"+strings.TrimPrefix(c.Base[strings.LastIndex(c.Base, ":")+1:], "")) || strings.Contains(c.Base, "/v2") {
		t.Errorf("base not trimmed: %q", c.Base)
	}
}

// With nothing connected the plugin writes "NaN" strings for angles.
func TestMountInfoDisconnected(t *testing.T) {
	f, c := newFake(t)
	f.routes["/v2/api/equipment/mount/info"] = `{"Response":{"SiderealTime":0,"RightAscension":0,"Declination":0,"Altitude":"NaN","AltitudeString":"","Azimuth":"NaN","AtPark":false,"TrackingRate":{},"TrackingEnabled":false,"EquatorialSystem":"JNOW","Slewing":false,"Connected":false},"Error":"","StatusCode":200,"Success":true,"Type":"API"}`
	m, err := c.MountInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if m.Connected || !math.IsNaN(float64(m.Altitude)) {
		t.Errorf("mount %+v", m)
	}
}

func TestSlew(t *testing.T) {
	f, c := newFake(t)
	f.routes["/v2/api/equipment/mount/slew"] = `{"Response":"Slew finished","Error":"","StatusCode":200,"Success":true,"Type":"API"}`
	if err := c.Slew(context.Background(), 331.4, -0.32); err != nil {
		t.Fatal(err)
	}
	q := f.queries["/v2/api/equipment/mount/slew"]
	if q.Get("ra") != "331.400000" || q.Get("dec") != "-0.320000" || q.Get("waitForResult") != "true" {
		t.Errorf("query %v", q)
	}
	f.routes["/v2/api/equipment/mount/slew"] = `{"Response":"","Error":"Mount parked","StatusCode":409,"Success":false,"Type":"API"}`
	err := c.Slew(context.Background(), 1, 2)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 409 || ae.Msg != "Mount parked" {
		t.Errorf("parked: %v", err)
	}
	f.routes["/v2/api/equipment/mount/slew"] = `{"Response":"Slew failed","Error":"","StatusCode":200,"Success":true,"Type":"API"}`
	if err := c.Slew(context.Background(), 1, 2); err == nil || !strings.Contains(err.Error(), "Slew failed") {
		t.Errorf("failed slew: %v", err)
	}
}

func TestDevices(t *testing.T) {
	f, c := newFake(t)
	f.routes["/v2/api/version"] = `{"Response":"2.2.15.0","Error":"","StatusCode":200,"Success":true,"Type":"API"}`
	f.routes["/v2/api/equipment/filterwheel/info"] = `{"Response":{"Connected":true,"IsMoving":false,"SelectedFilter":{"Name":"Ha","Id":3},"AvailableFilters":[{"Name":"Luminance","Id":0},{"Name":"R","Id":1},{"Name":"Ha","Id":3}]},"Error":"","StatusCode":200,"Success":true,"Type":"API"}`
	f.routes["/v2/api/equipment/filterwheel/change-filter"] = `{"Response":"Filter changed","Error":"","StatusCode":200,"Success":true,"Type":"API"}`
	f.routes["/v2/api/equipment/dome/info"] = `{"Response":{"Connected":true,"ShutterStatus":"ShutterOpen"},"Error":"","StatusCode":200,"Success":true,"Type":"API"}`
	f.routes["/v2/api/equipment/safetymonitor/info"] = `{"Response":{"Connected":true,"IsSafe":false},"Error":"","StatusCode":200,"Success":true,"Type":"API"}`
	f.routes["/v2/api/equipment/camera/info"] = `{"Response":{"Connected":true,"CoolerOn":true,"Temperature":-9.8,"TemperatureSetPoint":-10},"Error":"","StatusCode":200,"Success":true,"Type":"API"}`
	ctx := context.Background()
	if v, err := c.Version(ctx); err != nil || v != "2.2.15.0" {
		t.Errorf("version %q %v", v, err)
	}
	fw, err := c.FilterWheel(ctx)
	if err != nil || fw.SelectedFilter.Name != "Ha" || len(fw.AvailableFilters) != 3 {
		t.Errorf("filter wheel %+v %v", fw, err)
	}
	l, ok := fw.FindFilter("L")
	if !ok || l.ID != 0 {
		t.Errorf("FindFilter L: %+v %v", l, ok)
	}
	if _, ok := fw.FindFilter("G"); ok {
		t.Error("no G filter")
	}
	if err := c.ChangeFilter(ctx, 0); err != nil || f.queries["/v2/api/equipment/filterwheel/change-filter"].Get("filterId") != "0" {
		t.Errorf("change filter %v", err)
	}
	d, err := c.Dome(ctx)
	if err != nil || !d.Connected || !d.Open() {
		t.Errorf("dome %+v %v", d, err)
	}
	f.routes["/v2/api/equipment/dome/info"] = `{"Response":{"Connected":true,"ShutterStatus":1},"Error":"","StatusCode":200,"Success":true,"Type":"API"}`
	if d, _ := c.Dome(ctx); d.Open() {
		t.Error("numeric shutter 1 is closed")
	}
	s, err := c.SafetyMonitor(ctx)
	if err != nil || !s.Connected || s.IsSafe {
		t.Errorf("safety %+v %v", s, err)
	}
	cam, err := c.Camera(ctx)
	if err != nil || !cam.CoolerOn || cam.Temperature != -9.8 {
		t.Errorf("camera %+v %v", cam, err)
	}
}

func TestNotAnswering(t *testing.T) {
	c := New("http://127.0.0.1:1", 300*time.Millisecond)
	if _, err := c.Version(context.Background()); err == nil || !strings.Contains(err.Error(), "not answering") {
		t.Errorf("down: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html>not nina</html>") }))
	defer srv.Close()
	if _, err := New(srv.URL, time.Second).Version(context.Background()); err == nil || !strings.Contains(err.Error(), "Advanced API") {
		t.Errorf("wrong server: %v", err)
	}
}
