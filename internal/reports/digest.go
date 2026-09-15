package reports

import (
	"fmt"
	"html"
	"strings"
)

// DigestOptions names the things the digest says about where it came from.
type DigestOptions struct {
	// ScheduleName is shown when the run came from a schedule.
	ScheduleName string
	// RunID lets the reader find the run under Reports → History.
	RunID string
	// AttachmentNames are listed so the reader knows what else arrived.
	AttachmentNames []string
}

// RenderEmailDigest renders the email body: a 600 px, table-built digest of
// the report that every mail client can draw.
//
// Deliberately not the report itself. Gmail strips inline SVG and most of a
// <style> block, and Outlook renders with Word's engine, so the charts would
// arrive as blank space. The digest carries the headline figures, a coverage
// bar drawn with coloured table cells, and the findings; the full report
// travels alongside as an attachment. Because the digest is built from the
// same ReportData as the attachment, the two cannot disagree.
func RenderEmailDigest(data *ReportData, opts DigestOptions) string {
	e := html.EscapeString
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width"><title>`)
	b.WriteString(e(data.Title))
	b.WriteString(`</title></head><body style="margin:0;padding:16px 8px;background:#E9ECEF;font-family:Arial,Helvetica,sans-serif;font-size:14px;color:#12201A;">`)
	b.WriteString(`<table role="presentation" width="600" cellpadding="0" cellspacing="0" style="width:600px;max-width:100%;margin:0 auto;background:#ffffff;border-collapse:collapse;">`)
	b.WriteString(`<tr><td style="border-top:4px solid #087D5A;font-size:0;line-height:0;">&nbsp;</td></tr>`)
	b.WriteString(`<tr><td style="padding:16px 24px 8px 24px;">`)
	fmt.Fprintf(&b, `<div style="font-size:11px;letter-spacing:1px;text-transform:uppercase;color:#66786F;font-weight:bold;">Nexara &middot; %s</div>`, e(data.Kicker))
	fmt.Fprintf(&b, `<div style="font-size:20px;font-weight:bold;margin-top:4px;">%s</div>`, e(data.Heading))
	fmt.Fprintf(&b, `<div style="font-size:12px;color:#66786F;margin-top:2px;">Generated %s`, e(data.GeneratedAt))
	if opts.ScheduleName != "" {
		fmt.Fprintf(&b, ` &middot; schedule &ldquo;%s&rdquo;`, e(opts.ScheduleName))
	}
	b.WriteString(`</div></td></tr>`)

	// Headline figures: the hero first, then up to three more.
	if len(data.KPIs) > 0 {
		kpis := make([]KPI, 0, 4)
		for _, k := range data.KPIs {
			if k.Hero {
				kpis = append(kpis, k)
				break
			}
		}
		for _, k := range data.KPIs {
			if len(kpis) == 4 {
				break
			}
			if !k.Hero {
				kpis = append(kpis, k)
			}
		}
		b.WriteString(`<tr><td style="padding:0 24px 12px 24px;"><table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border-collapse:collapse;border-top:1px solid #DCE5E1;border-bottom:1px solid #DCE5E1;"><tr>`)
		for i, k := range kpis {
			border := "border-left:1px solid #DCE5E1;"
			if i == 0 {
				border = ""
			}
			color := map[string]string{ToneGood: "#0B6B45", ToneWarn: "#9A5B00", ToneCrit: "#B42318"}[k.Tone]
			if color == "" {
				color = "#12201A"
			}
			fmt.Fprintf(&b, `<td valign="top" style="padding:12px 14px;%s"><div style="font-size:12px;color:#66786F;">%s</div><div style="font-size:26px;font-weight:bold;line-height:1.1;color:%s;">%s%s</div>`,
				border, e(k.Label), color, e(k.Value), e(k.Unit))
			if k.Detail != "" {
				fmt.Fprintf(&b, `<div style="font-size:12px;color:#66786F;">%s</div>`, e(k.Detail))
			}
			b.WriteString(`</td>`)
		}
		b.WriteString(`</tr></table>`)
		// The first stacked bar becomes a row of coloured cells: no image, no
		// SVG, so it survives every client.
		if c := firstChart(data, ChartStackedBar); c != nil {
			total := c.Total
			if total <= 0 {
				for _, s := range c.Series {
					if len(s.Values) > 0 {
						total += s.Values[0]
					}
				}
			}
			if total > 0 {
				b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border-collapse:collapse;margin-top:12px;"><tr>`)
				legend := make([]string, 0, len(c.Series))
				for _, s := range c.Series {
					if len(s.Values) == 0 || s.Values[0] <= 0 {
						continue
					}
					pct := s.Values[0] / total * 100
					fmt.Fprintf(&b, `<td style="width:%.1f%%;height:10px;background:%s;font-size:0;line-height:0;">&nbsp;</td>`, pct, fillFor(s.Tone))
					legend = append(legend, fmt.Sprintf(`<span style="color:%s;">&#9632;</span> %s %s`, fillFor(s.Tone), e(s.Name), e(formatValue(s.Values[0], c.Format))))
				}
				b.WriteString(`</tr></table>`)
				fmt.Fprintf(&b, `<div style="font-size:12px;color:#66786F;margin-top:6px;">%s</div>`, strings.Join(legend, " &nbsp; "))
			}
		}
		b.WriteString(`</td></tr>`)
	}

	// Findings: the first four, and an honest count of the rest.
	if len(data.Findings) > 0 {
		b.WriteString(`<tr><td style="padding:0 24px 8px 24px;"><div style="font-weight:bold;margin-bottom:6px;">Findings</div><table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border-collapse:collapse;font-size:13px;">`)
		shown := data.Findings
		if len(shown) > 4 {
			shown = shown[:4]
		}
		for _, f := range shown {
			color := "#66786F"
			switch f.Severity {
			case SevCritical:
				color = "#B42318"
			case SevSerious, SevWarning:
				color = "#9A5B00"
			}
			fmt.Fprintf(&b, `<tr><td valign="top" style="padding:6px 10px 6px 0;border-top:1px solid #DCE5E1;width:78px;color:%s;font-weight:bold;font-size:11px;text-transform:uppercase;">%s</td><td style="padding:6px 0;border-top:1px solid #DCE5E1;"><b>%s</b> %s</td></tr>`,
				color, e(f.Severity), e(f.Lead), e(f.Text))
		}
		if rest := len(data.Findings) - len(shown); rest > 0 {
			fmt.Fprintf(&b, `<tr><td style="padding:6px 10px 6px 0;border-top:1px solid #DCE5E1;border-bottom:1px solid #DCE5E1;color:#66786F;font-weight:bold;font-size:11px;">+ %d MORE</td><td style="padding:6px 0;border-top:1px solid #DCE5E1;border-bottom:1px solid #DCE5E1;color:#66786F;">%s in the attached report.</td></tr>`, rest, e(countBySeverity(data.Findings[len(shown):])))
		} else {
			b.WriteString(`<tr><td colspan="2" style="border-top:1px solid #DCE5E1;font-size:0;line-height:0;">&nbsp;</td></tr>`)
		}
		b.WriteString(`</table></td></tr>`)
	} else {
		b.WriteString(`<tr><td style="padding:0 24px 8px 24px;color:#0B6B45;font-weight:bold;">Nothing needs your attention in this period.</td></tr>`)
	}

	b.WriteString(`<tr><td style="padding:12px 24px 18px 24px;font-size:12px;color:#66786F;">`)
	if len(opts.AttachmentNames) > 0 {
		fmt.Fprintf(&b, `Attached: %s.<br>`, e(strings.Join(opts.AttachmentNames, ", ")))
	}
	b.WriteString(`The full report, with every table and chart, is under <b>Reports &rarr; History</b> in Nexara`)
	if opts.RunID != "" {
		fmt.Fprintf(&b, ` (run %s)`, e(opts.RunID))
	}
	b.WriteString(`.</td></tr>`)
	b.WriteString(`</table></body></html>`)
	return b.String()
}

func firstChart(data *ReportData, kind string) *Chart {
	for _, s := range data.Sections {
		for _, bl := range s.Blocks {
			if bl.Kind == BlockChart && bl.Chart != nil && bl.Chart.Kind == kind {
				return bl.Chart
			}
		}
	}
	return nil
}

func countBySeverity(fs []Finding) string {
	counts := map[string]int{}
	for _, f := range fs {
		counts[f.Severity]++
	}
	parts := []string{}
	for _, sev := range []string{SevCritical, SevSerious, SevWarning, SevInfo} {
		if n := counts[sev]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, sev))
		}
	}
	return strings.Join(parts, ", ")
}
