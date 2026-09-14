// Package nina talks to the N.I.N.A. Advanced API plugin over HTTP. pec
// reads equipment state through it and sends exactly two commands: a slew
// of the mount to the star the Point page chose, and a filter change. It
// never parks, homes, syncs or sets tracking.
package nina

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/exploded/pec/internal/sky"
)

// DefaultBase is where the plugin listens by default.
const DefaultBase = "http://127.0.0.1:1888"

// Client is one NINA instance.
type Client struct {
	Base string // without /v2/api
	HTTP *http.Client
}

// New makes a client. base may carry a trailing slash or /v2/api.
func New(base string, timeout time.Duration) *Client {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	base = strings.TrimSuffix(base, "/v2/api")
	base = strings.TrimRight(base, "/")
	return &Client{Base: base, HTTP: &http.Client{Timeout: timeout}}
}

// APIError is a reply the plugin marked as failed.
type APIError struct {
	Status int
	Msg    string
}

func (e *APIError) Error() string { return "NINA: " + e.Msg }

type envelope struct {
	Response   json.RawMessage `json:"Response"`
	Error      string          `json:"Error"`
	StatusCode int             `json:"StatusCode"`
	Success    bool            `json:"Success"`
}

// get performs one GET and decodes the envelope, whatever the HTTP status.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	u := c.Base + "/v2/api" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("NINA at %s is not answering: %s", c.Base, dialText(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("NINA at %s did not answer like the Advanced API (%s)", c.Base, resp.Status)
	}
	if !env.Success {
		msg := env.Error
		if msg == "" {
			msg = strings.Trim(string(env.Response), `"`)
		}
		if msg == "" {
			msg = "request failed"
		}
		status := env.StatusCode
		if status == 0 {
			status = resp.StatusCode
		}
		return &APIError{Status: status, Msg: msg}
	}
	if out != nil && len(env.Response) > 0 && string(env.Response) != "null" {
		if err := json.Unmarshal(env.Response, out); err != nil {
			return fmt.Errorf("NINA %s: %w", path, err)
		}
	}
	return nil
}

// dialText strips the request URL and dial prefix from a transport error.
func dialText(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Err != nil {
		return op.Err.Error()
	}
	return err.Error()
}

// Version returns the plugin's version.
func (c *Client) Version(ctx context.Context) (string, error) {
	var v string
	err := c.get(ctx, "/version", nil, &v)
	return v, err
}

// Coordinates is NINA's coordinate object: RA in hours, Dec in degrees, in
// the epoch it names.
type Coordinates struct {
	RA        float64
	RADegrees float64
	Dec       float64
	Epoch     string // JNOW | J2000 | ...
}

// MountInfo is the mount as NINA sees it.
type MountInfo struct {
	Connected        bool
	Name             string
	AtPark           bool
	AtHome           bool
	TrackingEnabled  bool
	TrackingMode     string
	Slewing          bool
	SideOfPier       string
	RightAscension   float64 // hours, native epoch
	Declination      float64 // degrees, native epoch
	Coordinates      Coordinates
	EquatorialSystem string
	SiteLatitude     float64
	SiteLongitude    float64
	Altitude         nanFloat // the plugin writes "NaN" as a string when nothing is connected
	Azimuth          nanFloat
	SiderealTime     nanFloat
}

// nanFloat decodes a JSON number, or the string "NaN" the plugin emits for
// values it does not have.
type nanFloat float64

func (f *nanFloat) UnmarshalJSON(b []byte) error {
	var v float64
	if json.Unmarshal(b, &v) == nil {
		*f = nanFloat(v)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if v, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
		*f = nanFloat(v)
	} else {
		*f = nanFloat(math.NaN())
	}
	return nil
}

// MountInfo reads the mount.
func (c *Client) MountInfo(ctx context.Context) (MountInfo, error) {
	var m MountInfo
	err := c.get(ctx, "/equipment/mount/info", nil, &m)
	return m, err
}

// Native returns the pointing in the mount's own epoch.
func (m MountInfo) Native() (raHours, decDeg float64, epoch string) {
	if m.Coordinates.Epoch != "" && (m.Coordinates.RA != 0 || m.Coordinates.Dec != 0) {
		return m.Coordinates.RA, m.Coordinates.Dec, m.Coordinates.Epoch
	}
	return m.RightAscension, m.Declination, m.EquatorialSystem
}

// J2000 returns the pointing in J2000, precessing from the epoch of date
// when that is what the mount reports (the usual case for ASCOM drivers).
func (m MountInfo) J2000(at time.Time) (raHours, decDeg float64) {
	ra, dec, epoch := m.Native()
	if strings.Contains(strings.ToUpper(epoch), "2000") {
		return ra, dec
	}
	return sky.DateToJ2000(ra, dec, at)
}

// Slew moves the mount to J2000 coordinates given in degrees and waits for
// it to finish. The plugin takes J2000 whatever the mount's native epoch.
func (c *Client) Slew(ctx context.Context, raDeg, decDeg float64) error {
	q := url.Values{
		"ra": {strconv.FormatFloat(raDeg, 'f', 6, 64)}, "dec": {strconv.FormatFloat(decDeg, 'f', 6, 64)},
		"waitForResult": {"true"},
	}
	var resp json.RawMessage
	if err := c.get(ctx, "/equipment/mount/slew", q, &resp); err != nil {
		return err
	}
	if s := strings.ToLower(strings.Trim(string(resp), `"`)); strings.Contains(s, "fail") {
		return &APIError{Msg: strings.Trim(string(resp), `"`)}
	}
	return nil
}

// Filter is one filter-wheel position.
type Filter struct {
	Name string
	ID   int `json:"Id"`
}

// FilterWheel is the filter wheel as NINA sees it.
type FilterWheel struct {
	Connected        bool
	IsMoving         bool
	SelectedFilter   Filter
	AvailableFilters []Filter
}

// FilterWheel reads the filter wheel.
func (c *Client) FilterWheel(ctx context.Context) (FilterWheel, error) {
	var f FilterWheel
	err := c.get(ctx, "/equipment/filterwheel/info", nil, &f)
	return f, err
}

// ChangeFilter selects a filter by id.
func (c *Client) ChangeFilter(ctx context.Context, id int) error {
	return c.get(ctx, "/equipment/filterwheel/change-filter", url.Values{"filterId": {strconv.Itoa(id)}}, nil)
}

// FindFilter picks the first filter whose name starts with prefix, ignoring
// case, so "L" matches "L", "Lum" and "Luminance".
func (f FilterWheel) FindFilter(prefix string) (Filter, bool) {
	for _, x := range f.AvailableFilters {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(x.Name)), strings.ToLower(prefix)) {
			return x, true
		}
	}
	return Filter{}, false
}

// Dome is the dome or roof as NINA sees it.
type Dome struct {
	Connected     bool
	ShutterStatus shutter
}

// Open reports whether the shutter reads open.
func (d Dome) Open() bool { return strings.EqualFold(string(d.ShutterStatus), "ShutterOpen") }

// shutter accepts NINA's enum as a string or, from an older build, a number
// in ASCOM order (0 open, 1 closed, 2 opening, 3 closing, 4 error).
type shutter string

func (s *shutter) UnmarshalJSON(b []byte) error {
	var str string
	if json.Unmarshal(b, &str) == nil {
		*s = shutter(str)
		return nil
	}
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	names := []string{"ShutterOpen", "ShutterClosed", "ShutterOpening", "ShutterClosing", "ShutterError"}
	if n >= 0 && n < len(names) {
		*s = shutter(names[n])
	} else {
		*s = shutter("ShutterNone")
	}
	return nil
}

// Dome reads the dome.
func (c *Client) Dome(ctx context.Context) (Dome, error) {
	var d Dome
	err := c.get(ctx, "/equipment/dome/info", nil, &d)
	return d, err
}

// Safety is the safety monitor as NINA sees it.
type Safety struct {
	Connected bool
	IsSafe    bool
}

// SafetyMonitor reads the safety monitor.
func (c *Client) SafetyMonitor(ctx context.Context) (Safety, error) {
	var s Safety
	err := c.get(ctx, "/equipment/safetymonitor/info", nil, &s)
	return s, err
}

// Camera is the camera as NINA sees it.
type Camera struct {
	Connected           bool
	CoolerOn            bool
	Temperature         float64
	TemperatureSetPoint float64
}

// Camera reads the camera.
func (c *Client) Camera(ctx context.Context) (Camera, error) {
	var cam Camera
	err := c.get(ctx, "/equipment/camera/info", nil, &cam)
	return cam, err
}
