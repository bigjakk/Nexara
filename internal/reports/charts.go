package reports

import (
	"fmt"
	"html"
	"math"
	"strings"
)

// Chart rendering: inline SVG, no script, drawn to one scale per chart.
//
// The rules the drawings follow come from the design pass that produced the
// report layout: bars no thicker than 24 units with a rounded data-end and a
// square baseline, a 2-unit surface gap between touching fills, hairline solid
// gridlines, values labelled selectively (a cap label on a histogram whose
// axis is otherwise unlabelled, never a number on every stacked segment), and
// every mark carrying a <title> so a hovering reader gets the exact value
// without any JavaScript — the preview iframe forbids scripts and email
// clients run none.
//
// Every chart is 640 units wide and scales to its container through the
// viewBox; text scales with it, which at the widths a report renders at
// stays between 11 and 13 px.

const chartWidth = 640

// tone colours, shared with the stylesheet in renderer.go.
var toneFill = map[string]string{
	ToneGood:    "#059669",
	ToneWarn:    "#D97706",
	ToneCrit:    "#DC2626",
	ToneNeutral: "#94A3B8",
	ToneAccent:  "#087D5A",
}

const (
	inkColor  = "#12201A"
	muteColor = "#66786F"
	lineColor = "#DCE5E1"
	axisColor = "#C3CFC9"
)

func fillFor(tone string) string {
	if c, ok := toneFill[tone]; ok {
		return c
	}
	return toneFill[ToneAccent]
}

// chartSVG draws a chart. n distinguishes clip-path ids when several charts
// share a page.
func chartSVG(c *Chart, n int) string {
	if c == nil {
		return ""
	}
	switch c.Kind {
	case ChartStackedBar:
		return stackedBarSVG(c, n)
	case ChartColumns:
		return columnsSVG(c)
	case ChartHist:
		return histSVG(c)
	case ChartBars:
		return barsSVG(c)
	case ChartLines:
		return linesSVG(c)
	}
	return ""
}

// formatValue writes a chart value the way its format asks.
func formatValue(v float64, format string) string {
	switch format {
	case FormatPercent:
		return fmt.Sprintf("%.1f%%", v)
	case FormatBytes:
		return formatBytes(v)
	case FormatRate:
		return formatBytesRate(v)
	}
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

// labelFits estimates whether a label fits inside a mark of width w. There
// are no font metrics server-side, so this is a conservative per-character
// estimate for the 12-unit semibold the charts use, plus padding either side.
// A label that would not fit is left to the legend, tooltip or table rather
// than clipped.
func labelFits(label string, w, fontSize float64) bool {
	return w >= float64(len(label))*fontSize*0.57+16
}

func esc(s string) string { return html.EscapeString(s) }

func f1(v float64) string { return fmt.Sprintf("%.1f", v) }

// niceMax rounds a data peak up to a tidy axis maximum (1, 2, 5 × 10^k, with
// 2.5 and 4 admitted for small counts so a peak of 4 gets an axis of 4, not 5).
func niceMax(peak float64) float64 {
	if peak <= 0 {
		return 1
	}
	exp := math.Floor(math.Log10(peak))
	base := math.Pow(10, exp)
	frac := peak / base
	var nice float64
	switch {
	case frac <= 1:
		nice = 1
	case frac <= 2:
		nice = 2
	case frac <= 2.5:
		nice = 2.5
	case frac <= 4:
		nice = 4
	case frac <= 5:
		nice = 5
	default:
		nice = 10
	}
	return nice * base
}

// stackedBarSVG draws one horizontal 100 % bar. Segments keep square joins
// (a clip path rounds only the outer ends) and a 2-unit gap.
func stackedBarSVG(c *Chart, n int) string {
	const h, y, gap = 22.0, 2.0, 2.0
	total := c.Total
	if total <= 0 {
		for _, s := range c.Series {
			if len(s.Values) > 0 {
				total += s.Values[0]
			}
		}
	}
	if total <= 0 {
		return ""
	}
	var rects, labels strings.Builder
	x := 0.0
	segs := 0
	for _, s := range c.Series {
		if len(s.Values) > 0 && s.Values[0] > 0 {
			segs++
		}
	}
	usable := chartWidth - gap*float64(max(segs-1, 0))
	for _, s := range c.Series {
		if len(s.Values) == 0 || s.Values[0] <= 0 {
			continue
		}
		w := s.Values[0] / total * usable
		label := fmt.Sprintf("%s %s", s.Name, formatValue(s.Values[0], c.Format))
		fmt.Fprintf(&rects, "<rect x='%s' y='%s' width='%s' height='%s' fill='%s'><title>%s of %s</title></rect>",
			f1(x), f1(y), f1(w), f1(h), fillFor(s.Tone), esc(label), esc(formatValue(total, c.Format)))
		if labelFits(label, w, 12) {
			fmt.Fprintf(&labels, "<text x='%s' y='%s' font-size='12' font-weight='600' fill='#fff'>%s</text>",
				f1(x+10), f1(y+h/2+4), esc(label))
		}
		x += w + gap
	}
	clip := fmt.Sprintf("rpt-clip-%d", n)
	return fmt.Sprintf("<svg viewBox='0 0 %d %d' role='img' aria-label='%s'><defs><clipPath id='%s'><rect x='0' y='%s' width='%d' height='%s' rx='4'/></clipPath></defs><g clip-path='url(#%s)'>%s</g>%s</svg>",
		chartWidth, 28, esc(c.AriaLabel), clip, f1(y), chartWidth, f1(h), clip, rects.String(), labels.String())
}

// columnsSVG draws stacked columns per category with hairline gridlines and a
// labelled value axis.
func columnsSVG(c *Chart) string {
	const left, right, top, base, height, bw, gap = 34.0, 10.0, 16.0, 160.0, 200.0, 24.0, 2.0
	if len(c.Categories) == 0 {
		return ""
	}
	peak := c.Max
	if peak <= 0 {
		for i := range c.Categories {
			sum := 0.0
			for _, s := range c.Series {
				if i < len(s.Values) {
					sum += s.Values[i]
				}
			}
			peak = math.Max(peak, sum)
		}
		peak = niceMax(peak)
	}
	unit := (base - top) / peak
	slot := (chartWidth - left - right) / float64(len(c.Categories))
	var out strings.Builder
	for _, v := range []float64{0, peak / 2, peak} {
		yy := base - v*unit
		col := lineColor
		if v == 0 {
			col = axisColor
		}
		fmt.Fprintf(&out, "<line x1='%s' y1='%s' x2='%s' y2='%s' stroke='%s' stroke-width='1'/>", f1(left), f1(yy), f1(chartWidth-right), f1(yy), col)
		fmt.Fprintf(&out, "<text x='%s' y='%s' text-anchor='end' font-size='11' fill='%s'>%s</text>", f1(left-8), f1(yy+4), muteColor, esc(formatValue(v, c.Format)))
	}
	for i, cat := range c.Categories {
		cx := left + slot*float64(i) + slot/2
		y := base
		for _, s := range c.Series {
			if i >= len(s.Values) || s.Values[i] <= 0 {
				continue
			}
			hh := s.Values[i]*unit - gap
			if hh < 1 {
				hh = 1
			}
			fmt.Fprintf(&out, "<rect x='%s' y='%s' width='%s' height='%s' fill='%s'><title>%s: %s %s</title></rect>",
				f1(cx-bw/2), f1(y-hh), f1(bw), f1(hh), fillFor(s.Tone), esc(cat), esc(formatValue(s.Values[i], c.Format)), esc(s.Name))
			y -= s.Values[i] * unit
		}
		fmt.Fprintf(&out, "<text x='%s' y='%s' text-anchor='middle' font-size='11.5' fill='%s'>%s</text>", f1(cx), f1(base+18), muteColor, esc(cat))
	}
	if c.Footnote != "" {
		fmt.Fprintf(&out, "<text x='%s' y='%s' font-size='11' fill='%s'>%s</text>", f1(left), f1(height-4), muteColor, esc(c.Footnote))
	}
	return fmt.Sprintf("<svg viewBox='0 0 %d %d' role='img' aria-label='%s'>%s</svg>", chartWidth, int(height), esc(c.AriaLabel), out.String())
}

// histSVG draws single-series columns with a value on every cap (the axis is
// otherwise unlabelled) and an optional threshold line after a category.
func histSVG(c *Chart) string {
	const left, right, top, base, height, bw = 10.0, 10.0, 30.0, 150.0, 196.0, 24.0
	if len(c.Categories) == 0 || len(c.Series) == 0 {
		return ""
	}
	s := c.Series[0]
	peak := c.Max
	if peak <= 0 {
		for _, v := range s.Values {
			peak = math.Max(peak, v)
		}
	}
	if peak <= 0 {
		peak = 1
	}
	slot := (chartWidth - left - right) / float64(len(c.Categories))
	var out strings.Builder
	fmt.Fprintf(&out, "<line x1='%s' y1='%s' x2='%s' y2='%s' stroke='%s' stroke-width='1'/>", f1(left), f1(base), f1(chartWidth-right), f1(base), axisColor)
	if c.Threshold != nil {
		tx := left + slot*float64(c.Threshold.AfterIndex+1)
		fmt.Fprintf(&out, "<line x1='%s' y1='%s' x2='%s' y2='%s' stroke='#9A5B00' stroke-width='1'/>", f1(tx), f1(top-14), f1(tx), f1(base))
		fmt.Fprintf(&out, "<text x='%s' y='%s' font-size='11' fill='#9A5B00' font-weight='600'>%s</text>", f1(tx+6), f1(top-4), esc(c.Threshold.Label))
	}
	for i, cat := range c.Categories {
		v := 0.0
		if i < len(s.Values) {
			v = s.Values[i]
		}
		cx := left + slot*float64(i) + slot/2
		hh := v / peak * (base - top - 6)
		y := base - hh
		if v > 0 {
			// Rounded cap, square base: the rounded rect plus a square patch
			// over its bottom corners.
			fmt.Fprintf(&out, "<rect x='%s' y='%s' width='%s' height='%s' rx='4' fill='%s'><title>%s: %s</title></rect>",
				f1(cx-bw/2), f1(y), f1(bw), f1(hh), fillFor(s.Tone), esc(cat), esc(formatValue(v, c.Format)))
			patch := math.Min(4, hh)
			fmt.Fprintf(&out, "<rect x='%s' y='%s' width='%s' height='%s' fill='%s'/>", f1(cx-bw/2), f1(base-patch), f1(bw), f1(patch), fillFor(s.Tone))
		}
		fmt.Fprintf(&out, "<text x='%s' y='%s' text-anchor='middle' font-size='12' font-weight='600' fill='%s'>%s</text>", f1(cx), f1(y-7), inkColor, esc(formatValue(v, c.Format)))
		fmt.Fprintf(&out, "<text x='%s' y='%s' text-anchor='middle' font-size='11.5' fill='%s'>%s</text>", f1(cx), f1(base+18), muteColor, esc(cat))
	}
	if c.Footnote != "" {
		fmt.Fprintf(&out, "<text x='%s' y='%s' font-size='11' fill='%s'>%s</text>", f1(left), f1(height-4), muteColor, esc(c.Footnote))
	}
	return fmt.Sprintf("<svg viewBox='0 0 %d %d' role='img' aria-label='%s'>%s</svg>", chartWidth, int(height), esc(c.AriaLabel), out.String())
}

// barsSVG draws horizontal bars, one per category, longest first as given,
// with the category at the left and the value at the bar end.
func barsSVG(c *Chart) string {
	const labelW, right, rowH, bh, top = 170.0, 70.0, 26.0, 16.0, 6.0
	if len(c.Categories) == 0 || len(c.Series) == 0 {
		return ""
	}
	s := c.Series[0]
	peak := c.Max
	if peak <= 0 {
		for _, v := range s.Values {
			peak = math.Max(peak, v)
		}
	}
	if peak <= 0 {
		peak = 1
	}
	height := top*2 + rowH*float64(len(c.Categories))
	usable := chartWidth - labelW - right
	var out strings.Builder
	fmt.Fprintf(&out, "<line x1='%s' y1='%s' x2='%s' y2='%s' stroke='%s' stroke-width='1'/>", f1(labelW), f1(top), f1(labelW), f1(height-top), axisColor)
	for i, cat := range c.Categories {
		v := 0.0
		if i < len(s.Values) {
			v = s.Values[i]
		}
		y := top + rowH*float64(i) + (rowH-bh)/2
		w := v / peak * usable
		name := cat
		if len(name) > 26 {
			name = name[:25] + "…"
		}
		fmt.Fprintf(&out, "<text x='%s' y='%s' text-anchor='end' font-size='12' fill='%s'>%s</text>", f1(labelW-8), f1(y+bh-4), inkColor, esc(name))
		if w > 0 {
			fmt.Fprintf(&out, "<rect x='%s' y='%s' width='%s' height='%s' rx='4' fill='%s'><title>%s: %s</title></rect>",
				f1(labelW), f1(y), f1(w), f1(bh), fillFor(s.Tone), esc(cat), esc(formatValue(v, c.Format)))
			fmt.Fprintf(&out, "<rect x='%s' y='%s' width='%s' height='%s' fill='%s'/>", f1(labelW), f1(y), f1(math.Min(4, w)), f1(bh), fillFor(s.Tone))
		}
		fmt.Fprintf(&out, "<text x='%s' y='%s' font-size='11.5' font-weight='600' fill='%s'>%s</text>", f1(labelW+w+6), f1(y+bh-4), inkColor, esc(formatValue(v, c.Format)))
	}
	if c.Threshold != nil && c.Threshold.Value > 0 && c.Threshold.Value <= peak {
		tx := labelW + c.Threshold.Value/peak*usable
		fmt.Fprintf(&out, "<line x1='%s' y1='%s' x2='%s' y2='%s' stroke='#9A5B00' stroke-width='1'/>", f1(tx), f1(2), f1(tx), f1(height-top))
		fmt.Fprintf(&out, "<text x='%s' y='%s' font-size='10.5' fill='#9A5B00' font-weight='600' text-anchor='end'>%s</text>", f1(tx-4), f1(top+4), esc(c.Threshold.Label))
	}
	return fmt.Sprintf("<svg viewBox='0 0 %d %d' role='img' aria-label='%s'>%s</svg>", chartWidth, int(height), esc(c.AriaLabel), out.String())
}

// linesSVG draws one or more 2-unit lines over ordered categories, with an
// end marker on each series and an optional horizontal threshold.
func linesSVG(c *Chart) string {
	const left, right, top, base, height = 40.0, 14.0, 16.0, 160.0, 200.0
	if len(c.Categories) < 2 || len(c.Series) == 0 {
		return ""
	}
	peak := c.Max
	if peak <= 0 {
		for _, s := range c.Series {
			for _, v := range s.Values {
				peak = math.Max(peak, v)
			}
		}
		if c.Threshold != nil {
			peak = math.Max(peak, c.Threshold.Value)
		}
		peak = niceMax(peak)
	}
	unit := (base - top) / peak
	step := (chartWidth - left - right) / float64(len(c.Categories)-1)
	var out strings.Builder
	for _, v := range []float64{0, peak / 2, peak} {
		yy := base - v*unit
		col := lineColor
		if v == 0 {
			col = axisColor
		}
		fmt.Fprintf(&out, "<line x1='%s' y1='%s' x2='%s' y2='%s' stroke='%s' stroke-width='1'/>", f1(left), f1(yy), f1(chartWidth-right), f1(yy), col)
		fmt.Fprintf(&out, "<text x='%s' y='%s' text-anchor='end' font-size='11' fill='%s'>%s</text>", f1(left-8), f1(yy+4), muteColor, esc(formatValue(v, c.Format)))
	}
	if c.Threshold != nil && c.Threshold.Value > 0 {
		yy := base - c.Threshold.Value*unit
		fmt.Fprintf(&out, "<line x1='%s' y1='%s' x2='%s' y2='%s' stroke='#9A5B00' stroke-width='1'/>", f1(left), f1(yy), f1(chartWidth-right), f1(yy))
		fmt.Fprintf(&out, "<text x='%s' y='%s' font-size='10.5' fill='#9A5B00' font-weight='600' text-anchor='end'>%s</text>", f1(chartWidth-right), f1(yy-4), esc(c.Threshold.Label))
	}
	// Category labels: every one when they fit, else every k-th.
	every := 1
	if len(c.Categories) > 8 {
		every = (len(c.Categories) + 7) / 8
	}
	for i, cat := range c.Categories {
		if i%every != 0 && i != len(c.Categories)-1 {
			continue
		}
		x := left + step*float64(i)
		fmt.Fprintf(&out, "<text x='%s' y='%s' text-anchor='middle' font-size='11' fill='%s'>%s</text>", f1(x), f1(base+18), muteColor, esc(cat))
	}
	for _, s := range c.Series {
		var d strings.Builder
		lastX, lastY := 0.0, 0.0
		for i, v := range s.Values {
			if i >= len(c.Categories) {
				break
			}
			x := left + step*float64(i)
			y := base - math.Min(v, peak)*unit
			if i == 0 {
				fmt.Fprintf(&d, "M %s %s", f1(x), f1(y))
			} else {
				fmt.Fprintf(&d, " L %s %s", f1(x), f1(y))
			}
			lastX, lastY = x, y
		}
		fmt.Fprintf(&out, "<path d='%s' fill='none' stroke='%s' stroke-width='2' stroke-linejoin='round' stroke-linecap='round'><title>%s</title></path>", d.String(), fillFor(s.Tone), esc(s.Name))
		if len(s.Values) > 0 {
			last := s.Values[min(len(s.Values), len(c.Categories))-1]
			fmt.Fprintf(&out, "<circle cx='%s' cy='%s' r='4' fill='%s' stroke='#fff' stroke-width='2'><title>%s: %s</title></circle>", f1(lastX), f1(lastY), fillFor(s.Tone), esc(s.Name), esc(formatValue(last, c.Format)))
		}
	}
	if c.Footnote != "" {
		fmt.Fprintf(&out, "<text x='%s' y='%s' font-size='11' fill='%s'>%s</text>", f1(left), f1(height-4), muteColor, esc(c.Footnote))
	}
	return fmt.Sprintf("<svg viewBox='0 0 %d %d' role='img' aria-label='%s'>%s</svg>", chartWidth, int(height), esc(c.AriaLabel), out.String())
}

// meterSVG draws one capacity gauge: a tinted track and a fill in the tone.
func meterSVG(m Meter) string {
	pct := 0.0
	if m.Total > 0 {
		pct = math.Min(1, math.Max(0, m.Used/m.Total))
	}
	fill := fillFor(m.Tone)
	track := map[string]string{ToneCrit: "#FCE4E4", ToneWarn: "#FDF0DC"}[m.Tone]
	if track == "" {
		track = "#E1F1EA"
	}
	return fmt.Sprintf("<svg viewBox='0 0 200 8' preserveAspectRatio='none' role='img' aria-label='%s %.0f percent used'><rect x='0' y='0' width='200' height='8' rx='4' fill='%s'/><rect x='0' y='0' width='%s' height='8' rx='4' fill='%s'><title>%s</title></rect></svg>",
		esc(m.Name), pct*100, track, f1(pct*200), fill, esc(m.UsedText))
}
