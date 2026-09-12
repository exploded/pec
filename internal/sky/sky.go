// Package sky is just enough spherical astronomy to say where to point:
// sidereal time, hour angle and altitude for a site, a low-precision Moon,
// and a short list of bright named stars near the celestial equator. It
// exists so a PEC measurement can be aimed at a sensible field (Dec near 0,
// an hour or two east of the meridian) by typing a star name into
// TheSkyX's Find box. Accuracy is a fraction of a degree, which is plenty.
package sky

import (
	"math"
	"sort"
	"time"
)

// Site is an observing location. Longitude is east-positive, in degrees.
type Site struct {
	LatDeg float64
	LonDeg float64
}

const (
	deg = math.Pi / 180
	// SiderealRate is sidereal hours per solar hour.
	SiderealRate = 1.00273790935
)

// JD is the Julian date of t.
func JD(t time.Time) float64 {
	return 2440587.5 + float64(t.UnixNano())/8.64e13
}

// GMST is Greenwich mean sidereal time in hours (0-24).
func GMST(t time.Time) float64 {
	d := JD(t) - 2451545.0
	// IAU 1982 expression, truncated: good to well under a second.
	h := 18.697374558 + 24.06570982441908*d
	return wrap24(h)
}

// LST is local sidereal time in hours (0-24) at the site.
func LST(t time.Time, s Site) float64 {
	return wrap24(GMST(t) + s.LonDeg/15)
}

// HourAngle returns the hour angle of an RA (hours) in hours, wrapped to
// (-12, 12]: negative means east of the meridian, still rising.
func HourAngle(lst, raHours float64) float64 {
	return wrap12(lst - raHours)
}

// Altitude of a point at hour angle ha (hours) and declination dec
// (degrees) seen from latitude lat (degrees), in degrees.
func Altitude(ha, dec, lat float64) float64 {
	h := ha * 15 * deg
	sinAlt := math.Sin(lat*deg)*math.Sin(dec*deg) + math.Cos(lat*deg)*math.Cos(dec*deg)*math.Cos(h)
	return math.Asin(clamp(sinAlt, -1, 1)) / deg
}

// Separation is the angle between two RA/Dec positions (hours, degrees),
// in degrees.
func Separation(ra1, dec1, ra2, dec2 float64) float64 {
	a1, d1 := ra1*15*deg, dec1*deg
	a2, d2 := ra2*15*deg, dec2*deg
	c := math.Sin(d1)*math.Sin(d2) + math.Cos(d1)*math.Cos(d2)*math.Cos(a1-a2)
	return math.Acos(clamp(c, -1, 1)) / deg
}

// Moon is the Moon's position and phase at an instant.
type Moon struct {
	RAHours    float64
	DecDeg     float64
	AltDeg     float64
	Illum      float64 // fraction of the disc lit, 0-1
	HAHours    float64
	Elongation float64 // degrees from the Sun
}

// MoonAt returns the Moon for a site and time. Positions come from the
// truncated series in the Astronomical Almanac (about 0.3 degrees), which
// is enough to keep a bright Moon out of a guide field.
func MoonAt(t time.Time, s Site) Moon {
	T := (JD(t) - 2451545.0) / 36525
	sd := func(a, b float64) float64 { return math.Sin((a + b*T) * deg) }
	lon := 218.32 + 481267.881*T +
		6.29*sd(135.0, 477198.87) - 1.27*sd(259.3, -413335.36) + 0.66*sd(235.7, 890534.22) +
		0.21*sd(269.9, 954397.74) - 0.19*sd(357.5, 35999.05) - 0.11*sd(186.5, 966404.03)
	lat := 5.13*sd(93.3, 483202.02) + 0.28*sd(228.2, 960400.89) - 0.28*sd(318.3, 6003.15) - 0.17*sd(217.6, -407332.21)
	ra, dec := eclToEq(lon, lat, T)

	// The Sun, for the phase.
	m := (357.529 + 35999.05*T) * deg
	sunLon := 280.459 + 36000.771*T + 1.915*math.Sin(m) + 0.020*math.Sin(2*m)
	sunRA, sunDec := eclToEq(sunLon, 0, T)
	elong := Separation(ra, dec, sunRA, sunDec)

	lst := LST(t, s)
	ha := HourAngle(lst, ra)
	return Moon{
		RAHours: ra, DecDeg: dec, AltDeg: Altitude(ha, dec, s.LatDeg), HAHours: ha,
		Illum: (1 - math.Cos(elong*deg)) / 2, Elongation: elong,
	}
}

// eclToEq converts ecliptic longitude and latitude (degrees) to RA (hours)
// and Dec (degrees) using the mean obliquity of date.
func eclToEq(lon, lat, T float64) (raHours, decDeg float64) {
	eps := (23.439291 - 0.0130042*T) * deg
	l, b := lon*deg, lat*deg
	x := math.Cos(b) * math.Cos(l)
	y := math.Cos(eps)*math.Cos(b)*math.Sin(l) - math.Sin(eps)*math.Sin(b)
	z := math.Sin(eps)*math.Cos(b)*math.Sin(l) + math.Cos(eps)*math.Sin(b)
	ra := math.Atan2(y, x) / deg / 15
	return wrap24(ra), math.Asin(clamp(z, -1, 1)) / deg
}

// Status classifies a candidate for the PEC field.
type Status string

const (
	StatusGood  Status = "good" // in the window now
	StatusSoon  Status = "soon" // will enter the window
	StatusLow   Status = "low"  // in the window but under the altitude floor
	StatusMoon  Status = "moon" // in the window but close to a bright Moon
	StatusPast  Status = "past" // too near or west of the meridian
	StatusUnder Status = "under"
)

// Window is the aiming rule: hour angle between MinHA and MaxHA (hours,
// negative = east), altitude at least MinAlt, and MoonSep degrees from a
// Moon that is up and more than MoonIllum lit.
type Window struct {
	MinHA, MaxHA float64
	MinAlt       float64
	MoonSep      float64
	MoonIllum    float64
}

// DefaultWindow is 1 to 2 hours east of the meridian, above 30 degrees,
// 30 degrees from a Moon that is more than a quarter lit. That keeps the
// mount on one pier side for an hour of runs without a flip.
func DefaultWindow() Window {
	return Window{MinHA: -2, MaxHA: -0.75, MinAlt: 30, MoonSep: 30, MoonIllum: 0.25}
}

// MinGoodFor is how long a star should stay in the window to be the pick:
// two Guiding Assistant runs and a little slack.
const MinGoodFor = 20 * time.Minute

// Candidate is a star with its geometry for the site and instant.
type Candidate struct {
	Star
	HAHours   float64
	AltDeg    float64
	MoonSep   float64
	Status    Status
	GoodIn    time.Duration // for StatusSoon: time until it enters the window
	GoodFor   time.Duration // for StatusGood: time left in the window
	Recommend bool          // the single pick
}

// Plan is the aiming advice for a site and instant.
type Plan struct {
	At         time.Time
	LST        float64 // hours
	Moon       Moon
	MoonBright bool // up and lit enough to matter
	// IdealRA is the RA on the equator that sits in the middle of the
	// window now, for the chart method.
	IdealRA    float64
	Candidates []Candidate
}

// PlanAt evaluates the catalogue for a site and instant.
func PlanAt(t time.Time, s Site, w Window) Plan {
	lst := LST(t, s)
	moon := MoonAt(t, s)
	p := Plan{At: t, LST: lst, Moon: moon, IdealRA: wrap24(lst - (w.MinHA+w.MaxHA)/2)}
	p.MoonBright = moon.AltDeg > 0 && moon.Illum >= w.MoonIllum
	for _, st := range Stars {
		c := Candidate{Star: st}
		c.HAHours = HourAngle(lst, st.RAHours)
		c.AltDeg = Altitude(c.HAHours, st.DecDeg, s.LatDeg)
		c.MoonSep = Separation(st.RAHours, st.DecDeg, moon.RAHours, moon.DecDeg)
		switch {
		case c.HAHours > w.MaxHA:
			c.Status = StatusPast
		case c.HAHours < w.MinHA:
			c.Status = StatusSoon
			c.GoodIn = solarDuration(w.MinHA - c.HAHours)
			// Not worth listing if it will still be below the floor when it
			// enters the window.
			if Altitude(w.MinHA, st.DecDeg, s.LatDeg) < w.MinAlt {
				c.Status = StatusUnder
			}
		case c.AltDeg < w.MinAlt:
			c.Status = StatusLow
		case p.MoonBright && c.MoonSep < w.MoonSep:
			c.Status = StatusMoon
		default:
			c.Status = StatusGood
			c.GoodFor = solarDuration(w.MaxHA - c.HAHours)
		}
		p.Candidates = append(p.Candidates, c)
	}
	sort.SliceStable(p.Candidates, func(i, j int) bool {
		a, b := p.Candidates[i], p.Candidates[j]
		if rank(a.Status) != rank(b.Status) {
			return rank(a.Status) < rank(b.Status)
		}
		switch a.Status {
		case StatusGood:
			// Anything with less than MinGoodFor left is a poor pick; among
			// the rest prefer the equator (smallest cos(Dec) correction).
			ua, ub := a.GoodFor >= MinGoodFor, b.GoodFor >= MinGoodFor
			if ua != ub {
				return ua
			}
			if !ua {
				return a.GoodFor > b.GoodFor
			}
			return math.Abs(a.DecDeg) < math.Abs(b.DecDeg)
		case StatusSoon:
			return a.GoodIn < b.GoodIn
		}
		return a.HAHours < b.HAHours
	})
	for i := range p.Candidates {
		if p.Candidates[i].Status == StatusGood {
			p.Candidates[i].Recommend = true
			break
		}
	}
	return p
}

func rank(s Status) int {
	switch s {
	case StatusGood:
		return 0
	case StatusSoon:
		return 1
	case StatusMoon:
		return 2
	case StatusLow:
		return 3
	case StatusPast:
		return 4
	}
	return 5
}

// solarDuration converts a change in hour angle (sidereal hours) to clock time.
func solarDuration(siderealHours float64) time.Duration {
	return time.Duration(siderealHours / SiderealRate * float64(time.Hour))
}

// FormatHA renders an hour angle as "-1h 23m" (east negative).
func FormatHA(h float64) string {
	sign := "+"
	if h < 0 {
		sign = "-"
		h = -h
	}
	hh := int(h)
	mm := int(math.Round((h - float64(hh)) * 60))
	if mm == 60 {
		hh, mm = hh+1, 0
	}
	return sign + itoa(hh) + "h " + pad2(mm) + "m"
}

// FormatRA renders hours as "21h 32m".
func FormatRA(h float64) string {
	h = wrap24(h)
	hh := int(h)
	mm := int(math.Round((h - float64(hh)) * 60))
	if mm == 60 {
		hh, mm = (hh+1)%24, 0
	}
	return pad2(hh) + "h " + pad2(mm) + "m"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func pad2(n int) string {
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}

func wrap24(h float64) float64 {
	h = math.Mod(h, 24)
	if h < 0 {
		h += 24
	}
	return h
}

func wrap12(h float64) float64 {
	h = math.Mod(h+12, 24)
	if h < 0 {
		h += 24
	}
	return h - 12
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
