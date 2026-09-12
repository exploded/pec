package report

// Inline SVG charts generated in Go. Geometry, draw order and class names
// follow skyq's design/CHARTS.md: one y-axis per chart, fixed colour slots,
// gridlines behind the data, markers only at low density, direct labels at
// the last point, no JavaScript needed to read the chart.

import (
	"fmt"
	"math"
	"strings"
)

// XY is one chart point.
type XY struct {
	X, Y float64
}

// Series is one line or scatter on a chart. Color is a CSS custom property
// reference such as "var(--series-1)", assigned by what the series is, never
// by its position.
type Series struct {
	Label  string
	Color  string
	Pts    []XY
	ErrY   []float64 // optional +/- error bar per point
	NoLine bool      // markers only
	Dashed bool
	Thin   bool
	Always bool // draw markers regardless of density
}

// VLine is a dashed vertical reference rule.
type VLine struct {
	X     float64
	Label string
}

// Axes describes a chart's frame.
type Axes struct {
	H            int // viewBox height
	XMin, XMax   float64
	YMin, YMax   float64 // 0,0 = auto from data, symmetric about zero
	XTicks       int
	YTicks       int
	XLabel       string
	YLabel       string
	XFmt, YFmt   func(float64) string
	ZeroLine     bool
	VLines       []VLine
	Gap          float64 // start a new subpath when consecutive x differ by more
	NoDirectLabs bool
}

const (
	chartW = 940
	padL   = 58
	padR   = 70
	padT   = 30
	padB   = 34
)

// RenderXY returns the complete <svg> element for the series.
func RenderXY(series []Series, a Axes) string {
	if a.H == 0 {
		a.H = 300
	}
	if a.XTicks == 0 {
		a.XTicks = 5
	}
	if a.YTicks == 0 {
		a.YTicks = 4
	}
	if a.XFmt == nil {
		a.XFmt = func(v float64) string { return fmt.Sprintf("%g", v) }
	}
	if a.YFmt == nil {
		a.YFmt = func(v float64) string { return fmt.Sprintf("%g", v) }
	}
	if a.YMin == 0 && a.YMax == 0 {
		lim := 0.0
		for _, s := range series {
			for i, p := range s.Pts {
				e := 0.0
				if i < len(s.ErrY) {
					e = s.ErrY[i]
				}
				lim = math.Max(lim, math.Abs(p.Y)+e)
			}
		}
		if lim == 0 {
			lim = 1
		}
		lim = niceCeil(lim)
		a.YMin, a.YMax = -lim, lim
	}
	if a.XMax == a.XMin {
		a.XMax = a.XMin + 1
	}
	pw := float64(chartW - padL - padR)
	ph := float64(a.H - padT - padB)
	X := func(x float64) float64 { return padL + (x-a.XMin)/(a.XMax-a.XMin)*pw }
	Y := func(y float64) float64 {
		y = math.Max(a.YMin, math.Min(a.YMax, y))
		return padT + ph - (y-a.YMin)/(a.YMax-a.YMin)*ph
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" role="img" aria-label="%s">`, chartW, a.H, esc(a.YLabel))

	// gridlines and y ticks
	for i := 0; i <= a.YTicks; i++ {
		v := a.YMin + (a.YMax-a.YMin)*float64(i)/float64(a.YTicks)
		fmt.Fprintf(&b, `<line x1="%d" x2="%d" y1="%.1f" y2="%.1f" class="gl"/>`, padL, chartW-padR, Y(v), Y(v))
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" class="tick" text-anchor="end">%s</text>`, padL-9, Y(v)+4, esc(a.YFmt(v)))
	}
	// x ticks
	for i := 0; i <= a.XTicks; i++ {
		v := a.XMin + (a.XMax-a.XMin)*float64(i)/float64(a.XTicks)
		fmt.Fprintf(&b, `<line x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f" class="ax"/>`, X(v), X(v), padT+ph, padT+ph+4)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" class="tick" text-anchor="middle">%s</text>`, X(v), padT+ph+16, esc(a.XFmt(v)))
	}
	// baseline and zero line
	fmt.Fprintf(&b, `<line x1="%d" x2="%d" y1="%.1f" y2="%.1f" class="ax"/>`, padL, chartW-padR, padT+ph, padT+ph)
	if a.ZeroLine && a.YMin < 0 && a.YMax > 0 {
		fmt.Fprintf(&b, `<line x1="%d" x2="%d" y1="%.1f" y2="%.1f" class="ax"/>`, padL, chartW-padR, Y(0), Y(0))
	}
	// axis labels
	fmt.Fprintf(&b, `<text x="%d" y="%d" class="alab" text-anchor="start">%s</text>`, padL-9, padT-11, esc(a.YLabel))
	if a.XLabel != "" {
		fmt.Fprintf(&b, `<text x="%d" y="%d" class="alab" text-anchor="end">%s</text>`, chartW-padR, padT-11, esc(a.XLabel))
	}
	// reference rules
	for _, v := range a.VLines {
		fmt.Fprintf(&b, `<line x1="%.1f" x2="%.1f" y1="%d" y2="%.1f" class="refline"/>`, X(v.X), X(v.X), padT, padT+ph)
		if v.Label != "" {
			fmt.Fprintf(&b, `<text x="%.1f" y="%d" class="reftxt">%s</text>`, X(v.X)+5, padT+11, esc(v.Label))
		}
	}
	// series
	for _, s := range series {
		if len(s.Pts) == 0 {
			continue
		}
		if !s.NoLine {
			var d strings.Builder
			prev := math.Inf(-1)
			for _, p := range s.Pts {
				cmd := " L"
				if math.IsInf(prev, -1) || (a.Gap > 0 && p.X-prev > a.Gap) {
					cmd = " M"
				}
				fmt.Fprintf(&d, "%s%.1f %.1f", cmd, X(p.X), Y(p.Y))
				prev = p.X
			}
			w := "2"
			if s.Thin {
				w = "1.2"
			}
			dash := ""
			if s.Dashed {
				dash = ` stroke-dasharray="5 4"`
			}
			fmt.Fprintf(&b, `<path d="%s" fill="none" stroke="%s" stroke-width="%s" stroke-linejoin="round" stroke-linecap="round"%s/>`,
				strings.TrimSpace(d.String()), s.Color, w, dash)
		}
		if s.NoLine || s.Always || len(s.Pts) <= 150 {
			for i, p := range s.Pts {
				if i < len(s.ErrY) && s.ErrY[i] > 0 {
					fmt.Fprintf(&b, `<line x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f" stroke="%s" stroke-width="1" opacity=".6"/>`,
						X(p.X), X(p.X), Y(p.Y-s.ErrY[i]), Y(p.Y+s.ErrY[i]), s.Color)
				}
				r := 2.8
				if s.NoLine && len(s.Pts) > 150 {
					r = 1.8
				}
				fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.1f" fill="%s" stroke="var(--surface-1)" stroke-width="1.2"/>`,
					X(p.X), Y(p.Y), r, s.Color)
			}
		}
		if !a.NoDirectLabs && s.Label != "" {
			last := s.Pts[len(s.Pts)-1]
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" class="dl" fill="%s">%s</text>`,
				math.Min(chartW-3, X(last.X)+9), Y(last.Y)+4, s.Color, esc(s.Label))
		}
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// BarGroup is one category on a bar chart with one value per series.
type BarGroup struct {
	Label  string
	Values []float64
	Sigma  []float64 // optional whisker per value
}

// RenderBars draws grouped vertical bars. colors is one CSS colour per
// series (fixed slots). Bars are anchored at zero.
func RenderBars(groups []BarGroup, colors []string, a Axes) string {
	if a.H == 0 {
		a.H = 260
	}
	if a.YTicks == 0 {
		a.YTicks = 4
	}
	if a.YFmt == nil {
		a.YFmt = func(v float64) string { return fmt.Sprintf("%g", v) }
	}
	nser := len(colors)
	if a.YMax == 0 {
		lim := 0.0
		for _, g := range groups {
			for i, v := range g.Values {
				e := 0.0
				if i < len(g.Sigma) {
					e = g.Sigma[i]
				}
				lim = math.Max(lim, v+e)
			}
		}
		if lim == 0 {
			lim = 1
		}
		a.YMax = niceCeil(lim)
	}
	pw := float64(chartW - padL - padR)
	ph := float64(a.H - padT - padB)
	Y := func(y float64) float64 { return padT + ph - math.Min(y, a.YMax)/a.YMax*ph }
	gw := pw / float64(max(len(groups), 1))
	bw := math.Min(38, gw*0.7/float64(max(nser, 1)))

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" role="img" aria-label="%s">`, chartW, a.H, esc(a.YLabel))
	for i := 0; i <= a.YTicks; i++ {
		v := a.YMax * float64(i) / float64(a.YTicks)
		fmt.Fprintf(&b, `<line x1="%d" x2="%d" y1="%.1f" y2="%.1f" class="gl"/>`, padL, chartW-padR, Y(v), Y(v))
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" class="tick" text-anchor="end">%s</text>`, padL-9, Y(v)+4, esc(a.YFmt(v)))
	}
	fmt.Fprintf(&b, `<line x1="%d" x2="%d" y1="%.1f" y2="%.1f" class="ax"/>`, padL, chartW-padR, padT+ph, padT+ph)
	fmt.Fprintf(&b, `<text x="%d" y="%d" class="alab" text-anchor="start">%s</text>`, padL-9, padT-11, esc(a.YLabel))
	for gi, g := range groups {
		cx := padL + gw*(float64(gi)+0.5)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" class="tick" text-anchor="middle">%s</text>`, cx, padT+ph+16, esc(g.Label))
		total := bw*float64(nser) + 2*float64(nser-1)
		x0 := cx - total/2
		for si := 0; si < nser && si < len(g.Values); si++ {
			v := g.Values[si]
			x := x0 + float64(si)*(bw+2)
			top := Y(v)
			h := math.Max(0, padT+ph-top)
			fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="3" fill="%s"><title>%s: %s</title></rect>`,
				x, top, bw, h, colors[si], esc(g.Label), esc(a.YFmt(v)))
			if si < len(g.Sigma) && g.Sigma[si] > 0 {
				mx := x + bw/2
				fmt.Fprintf(&b, `<line x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f" stroke="var(--text-secondary)" stroke-width="1.2"/>`,
					mx, mx, Y(v-g.Sigma[si]), Y(v+g.Sigma[si]))
			}
		}
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// niceCeil rounds up to 1, 2, 2.5, 5 x 10^n.
func niceCeil(v float64) float64 {
	if v <= 0 {
		return 1
	}
	e := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10} {
		if m*e >= v {
			return m * e
		}
	}
	return 10 * e
}

func esc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
