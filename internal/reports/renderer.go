package reports

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"html"
	"html/template"
	"strings"
)

// RenderHTML renders the report as a self-contained HTML document: no
// scripts, no external resources, charts as inline SVG. The same document is
// the in-app preview, the print/PDF source and the email attachment, so there
// is exactly one rendering to get right.
func RenderHTML(data *ReportData) (string, error) {
	charts := 0
	funcs := template.FuncMap{
		"cell": func(c Cell) template.HTML { return template.HTML(cellHTML(c)) }, //nolint:gosec // G203: cellHTML escapes every string it embeds
		"chart": func(c *Chart) template.HTML {
			charts++
			return template.HTML(chartSVG(c, charts)) //nolint:gosec // G203: chartSVG escapes every string it embeds
		},
		"meter":  func(m Meter) template.HTML { return template.HTML(meterSVG(m)) },    //nolint:gosec // G203: meterSVG escapes every string it embeds
		"legend": func(c *Chart) template.HTML { return template.HTML(legendHTML(c)) }, //nolint:gosec // G203: legendHTML escapes every string it embeds
		"toneClass": func(t string) string {
			switch t {
			case ToneGood, ToneWarn, ToneCrit, ToneNeutral, ToneAccent:
				return t
			}
			return ""
		},
		"sevClass": func(s string) string {
			switch s {
			case SevCritical, SevSerious, SevWarning, SevInfo:
				return s
			}
			return SevInfo
		},
		"pct": func(m Meter) string {
			if m.Total <= 0 {
				return "—"
			}
			return fmt.Sprintf("%.0f %%", m.Used/m.Total*100)
		},
		"showLegend":   func(c *Chart) bool { return c != nil && c.Legend && len(c.Series) > 1 },
		"chartHasData": chartHasData,
		"tableEmpty":   func(b Block) bool { return b.Kind == BlockTable && (b.Table == nil || len(b.Table.Rows) == 0) },
	}
	tmpl, err := template.New("report").Funcs(funcs).Parse(htmlShell)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}

// chartHasData reports whether a chart would draw anything, so a block with
// no categories shows its empty message rather than a titled blank.
func chartHasData(c *Chart) bool {
	if c == nil || len(c.Series) == 0 {
		return false
	}
	switch c.Kind {
	case ChartStackedBar:
		if c.Total > 0 {
			return true
		}
		for _, s := range c.Series {
			if len(s.Values) > 0 && s.Values[0] > 0 {
				return true
			}
		}
		return false
	case ChartLines:
		return len(c.Categories) >= 2
	}
	return len(c.Categories) > 0
}

// cellHTML writes one table cell's inner markup with every string escaped.
func cellHTML(c Cell) string {
	var b strings.Builder
	switch c.Kind {
	case CellPill:
		glyph := map[string]string{ToneGood: "●", ToneWarn: "▲", ToneCrit: "✕", ToneNeutral: "○"}[c.Tone]
		if glyph == "" {
			glyph = "●"
		}
		fmt.Fprintf(&b, `<span class="pill %s">%s %s</span>`, pillClass(c.Tone), glyph, html.EscapeString(c.Text))
	case CellChips:
		for _, ch := range c.Chips {
			switch ch.Kind {
			case ChipPBS, ChipVeeam:
				fmt.Fprintf(&b, `<span class="prov %s"><i></i>%s</span>`, ch.Kind, html.EscapeString(ch.Text))
			case ChipFlag:
				fmt.Fprintf(&b, `<span class="flag">%s</span>`, html.EscapeString(ch.Text))
			default:
				fmt.Fprintf(&b, `<span class="type">%s</span>`, html.EscapeString(ch.Text))
			}
		}
		if c.Text != "" {
			if len(c.Chips) > 0 {
				b.WriteString(" ")
			}
			fmt.Fprintf(&b, `<span class="ts">%s</span>`, html.EscapeString(c.Text))
		}
	case CellID:
		fmt.Fprintf(&b, `<span class="id">%s</span>`, html.EscapeString(c.Text))
	case CellStrong:
		fmt.Fprintf(&b, `<span class="name">%s</span>`, html.EscapeString(c.Text))
	case CellMute:
		fmt.Fprintf(&b, `<span class="mute">%s</span>`, html.EscapeString(c.Text))
	default:
		b.WriteString(html.EscapeString(c.Text))
	}
	if c.Sub != "" {
		fmt.Fprintf(&b, `<br><span class="ts">%s</span>`, html.EscapeString(c.Sub))
	}
	return b.String()
}

func pillClass(tone string) string {
	switch tone {
	case ToneGood:
		return "ok"
	case ToneWarn:
		return "stale"
	case ToneCrit:
		return "none"
	}
	return "na"
}

// legendHTML lists a chart's series with their swatches.
func legendHTML(c *Chart) string {
	if c == nil || len(c.Series) < 2 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="legend">`)
	for _, s := range c.Series {
		label := s.Name
		if c.Kind == ChartStackedBar && len(s.Values) > 0 {
			label = fmt.Sprintf("%s %s", s.Name, formatValue(s.Values[0], c.Format))
		}
		fmt.Fprintf(&b, `<span><i style="background:%s"></i>%s</span>`, fillFor(s.Tone), html.EscapeString(label))
	}
	b.WriteString(`</div>`)
	return b.String()
}

const htmlShell = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
  :root {
    --ink: #12201A; --ink2: #4B5F58; --mute: #66786F; --line: #DCE5E1; --ground: #F3F7F5; --accent: #087D5A;
    --good: #059669; --good-ink: #0B6B45; --good-soft: #E3F5EC;
    --warn: #D97706; --warn-ink: #9A5B00; --warn-soft: #FDF0DC;
    --crit: #DC2626; --crit-ink: #B42318; --crit-soft: #FCE4E4;
    --neutral: #94A3B8; --neutral-ink: #4B5563; --neutral-soft: #EEF1F4;
    --pbs: #A36814; --veeam: #54821C;
  }
  * { box-sizing: border-box; }
  body { margin: 0; padding: 24px 16px; background: var(--ground); color: var(--ink); font-family: system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif; font-size: 13.5px; line-height: 1.45; }
  .paper { background: #fff; max-width: 1080px; margin: 0 auto; padding: 36px 44px 32px; box-shadow: 0 1px 2px rgba(0,0,0,.06), 0 12px 40px rgba(10,30,22,.10); border-top: 4px solid var(--accent); }
  h1, h2, h3, h4 { margin: 0; }
  .mast { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: flex-end; gap: 12px 24px; padding-bottom: 16px; border-bottom: 1px solid var(--line); }
  .mast .kicker { font-size: 11px; letter-spacing: .08em; text-transform: uppercase; color: var(--mute); font-weight: 600; margin-bottom: 4px; }
  .mast h1 { font-size: 26px; font-weight: 600; letter-spacing: -0.01em; }
  .mast .sub { color: var(--ink2); margin-top: 2px; }
  .mast .who { text-align: right; font-size: 12px; color: var(--ink2); line-height: 1.5; }
  .mast .who b { color: var(--ink); font-weight: 600; }
  .meta { display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: 8px 20px; padding: 12px 0; border-bottom: 1px solid var(--line); font-size: 12.5px; }
  .meta .k { color: var(--mute); font-size: 11px; letter-spacing: .06em; text-transform: uppercase; font-weight: 600; }
  .kpis { display: flex; margin: 20px 0 8px; border-bottom: 1px solid var(--line); }
  .kpi { flex: 1 1 0; min-width: 0; padding: 12px 12px 14px 0; margin-right: 12px; border-right: 1px solid var(--line); overflow-wrap: anywhere; }
  .kpi:last-child { border-right: 0; margin-right: 0; }
  .kpi.hero { flex: 1.7 1 0; }
  .kpi .l { font-size: 11.5px; color: var(--mute); font-weight: 500; }
  .kpi .v { font-size: 26px; font-weight: 600; letter-spacing: -0.01em; line-height: 1.15; margin-top: 2px; }
  .kpi .v small { font-size: 13px; font-weight: 500; color: var(--ink2); margin-left: 3px; }
  .kpi .d { font-size: 11.5px; color: var(--ink2); margin-top: 3px; }
  .kpi.hero .v { font-size: 46px; letter-spacing: -0.02em; line-height: 1; margin-top: 6px; }
  .kpi.hero .v small { font-size: 15px; }
  .kpi .v.good { color: var(--good-ink); } .kpi .v.warn { color: var(--warn-ink); } .kpi .v.crit { color: var(--crit-ink); } .kpi .v.neutral { color: var(--neutral-ink); }
  section { margin-top: 30px; }
  h2 { font-size: 17px; font-weight: 600; margin: 0 0 4px; }
  .why { color: var(--mute); font-size: 12.5px; margin: 0 0 12px; }
  .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 28px; align-items: start; }
  .block.wide { grid-column: 1 / -1; }
  .block h3 { font-size: 13px; font-weight: 600; margin: 0 0 2px; }
  .block .bsub { font-size: 12px; color: var(--mute); margin: 0 0 8px; }
  .block p.text { margin: 0; color: var(--ink2); }
  .block .empty { color: var(--mute); font-size: 12.5px; padding: 10px 0; border-top: 1px solid var(--line); }
  svg { display: block; width: 100%; height: auto; }
  svg text { font-family: inherit; }
  .legend { display: flex; flex-wrap: wrap; gap: 6px 16px; font-size: 12px; color: var(--ink2); margin-top: 8px; }
  .legend i { display: inline-block; width: 10px; height: 10px; border-radius: 2px; vertical-align: -1px; margin-right: 6px; }
  .findings { list-style: none; margin: 0; padding: 0; }
  .findings li { display: grid; grid-template-columns: 84px 1fr; gap: 14px; padding: 9px 0; border-top: 1px solid var(--line); align-items: start; }
  .findings li:first-child { border-top: 0; }
  .findings .sev { font-size: 11px; font-weight: 700; letter-spacing: .06em; text-transform: uppercase; padding-top: 3px; }
  .findings .sev.critical { color: var(--crit-ink); } .findings .sev.serious, .findings .sev.warning { color: var(--warn-ink); } .findings .sev.info { color: var(--neutral-ink); }
  .findings b { font-weight: 600; }
  .findings .where { color: var(--mute); font-size: 12px; margin-top: 1px; }
  .scroll { overflow-x: auto; }
  table { width: 100%; border-collapse: collapse; font-size: 12.5px; }
  th { text-align: left; font-weight: 600; color: var(--ink2); font-size: 11px; letter-spacing: .05em; text-transform: uppercase; padding: 8px 10px 8px 0; border-bottom: 1.5px solid var(--ink); white-space: nowrap; }
  td { padding: 7px 10px 7px 0; border-bottom: 1px solid var(--line); vertical-align: top; }
  td.num, th.num { text-align: right; font-variant-numeric: tabular-nums; padding-right: 0; padding-left: 10px; }
  td.num + td, th.num + th { padding-left: 12px; }
  .nw { white-space: nowrap; }
  .id { font-variant-numeric: tabular-nums; color: var(--mute); }
  .name { font-weight: 600; }
  .mute { color: var(--mute); }
  .ts { color: var(--mute); font-size: 11.5px; white-space: nowrap; }
  .pill { display: inline-flex; align-items: center; gap: 5px; padding: 1px 8px 1px 6px; border-radius: 999px; font-size: 11.5px; font-weight: 600; white-space: nowrap; }
  .pill.ok { background: var(--good-soft); color: var(--good-ink); } .pill.stale { background: var(--warn-soft); color: var(--warn-ink); } .pill.none { background: var(--crit-soft); color: var(--crit-ink); } .pill.na { background: var(--neutral-soft); color: var(--neutral-ink); }
  .prov { display: inline-flex; align-items: center; gap: 5px; border: 1px solid var(--line); border-radius: 4px; padding: 0 6px; font-size: 11px; font-weight: 600; color: var(--ink); white-space: nowrap; margin-right: 4px; }
  .prov i { width: 7px; height: 7px; border-radius: 50%; display: inline-block; }
  .prov.pbs i { background: var(--pbs); } .prov.veeam i { background: var(--veeam); }
  .flag { display: inline-block; border: 1px dashed var(--warn-ink); color: var(--warn-ink); border-radius: 4px; padding: 0 5px; font-size: 10.5px; font-weight: 600; white-space: nowrap; }
  .type { font-size: 10.5px; font-weight: 600; color: var(--ink2); border: 1px solid var(--line); border-radius: 3px; padding: 0 4px; margin-right: 4px; }
  .meters .row { display: grid; grid-template-columns: 1.3fr 90px 2fr 1.2fr; gap: 14px; align-items: center; padding: 9px 0; border-bottom: 1px solid var(--line); font-size: 12.5px; }
  .meters .row.head { border-top: 1.5px solid var(--ink); font-size: 11px; letter-spacing: .05em; text-transform: uppercase; color: var(--ink2); font-weight: 600; }
  .meters .n b { font-weight: 600; display: block; } .meters .n span { color: var(--mute); font-size: 11.5px; }
  .meters .pct { font-variant-numeric: tabular-nums; font-weight: 600; text-align: right; }
  .meters .bar svg { height: 8px; }
  .meters .proj { color: var(--ink2); font-size: 12px; }
  .meters .proj.warn { color: var(--warn-ink); font-weight: 600; }
  .foot { margin-top: 34px; padding-top: 14px; border-top: 1px solid var(--line); display: grid; grid-template-columns: 1fr 1fr; gap: 24px; font-size: 11.5px; color: var(--ink2); }
  .foot h4 { margin: 0 0 4px; font-size: 11px; letter-spacing: .06em; text-transform: uppercase; color: var(--mute); }
  .foot ul { margin: 0; padding-left: 16px; }
  .foot li { margin: 2px 0; }
  .credit { margin-top: 16px; font-size: 11px; color: var(--mute); }
  @media (max-width: 760px) {
    .paper { padding: 22px 16px; }
    .kpis { display: grid; grid-template-columns: repeat(2, 1fr); }
    .kpi { border-right: 0; margin-right: 0; }
    .kpi.hero { grid-column: 1 / -1; }
    .grid, .foot { grid-template-columns: 1fr; }
    .mast .who { text-align: left; }
    .meters .row { grid-template-columns: 1.2fr 70px 1.6fr; }
    .meters .row .proj { grid-column: 1 / -1; }
    .findings li { grid-template-columns: 1fr; gap: 2px; }
  }
  /* Print / Save-as-PDF. Browsers drop background colours from printed output
     unless the user ticks "Background graphics", so nothing here relies on a
     fill: pills keep their coloured text and glyph, provider chips are
     outlined, and the charts are SVG, which prints. */
  @page { margin: 14mm; }
  @media print {
    body { background: #fff; padding: 0; }
    .paper { box-shadow: none; max-width: none; padding: 0; border-top-width: 3px; }
    thead { display: table-header-group; }
    tr, td, th, .kpi, .findings li, .meters .row { break-inside: avoid; }
    h2, h3 { break-after: avoid; }
    section { margin-top: 20px; }
    .scroll { overflow: visible; }
  }
</style>
</head>
<body>
<div class="paper">
  <div class="mast">
    <div>
      <div class="kicker">Nexara · {{.Kicker}}</div>
      <h1>{{.Heading}}</h1>
      {{if .Subtitle}}<div class="sub">{{.Subtitle}}</div>{{end}}
    </div>
    <div class="who">Generated <b>{{.GeneratedAt}}</b></div>
  </div>
  {{if .Meta}}<div class="meta">{{range .Meta}}<div><div class="k">{{.Label}}</div><div class="v">{{.Value}}</div></div>{{end}}</div>{{end}}
  {{if .KPIs}}<div class="kpis">{{range .KPIs}}<div class="kpi{{if .Hero}} hero{{end}}"><div class="l">{{.Label}}</div><div class="v {{toneClass .Tone}}">{{.Value}}{{if .Unit}}<small>{{.Unit}}</small>{{end}}</div>{{if .Detail}}<div class="d">{{.Detail}}</div>{{end}}</div>{{end}}</div>{{end}}
  {{if .Findings}}<section>
    <h2>Findings</h2>
    <p class="why">Ranked by severity. Each one names the thing to act on.</p>
    <ol class="findings">{{range .Findings}}<li><span class="sev {{sevClass .Severity}}">{{.Severity}}</span><div><b>{{.Lead}}</b>{{if .Text}} {{.Text}}{{end}}{{if .Where}}<div class="where">{{.Where}}</div>{{end}}</div></li>{{end}}</ol>
  </section>{{end}}
  {{range .Sections}}<section>
    <h2>{{.Title}}</h2>
    {{if .Why}}<p class="why">{{.Why}}</p>{{end}}
    <div class="grid">{{range .Blocks}}<div class="block{{if .Wide}} wide{{end}}">
      {{if .Title}}<h3>{{.Title}}</h3>{{end}}
      {{if .Subtitle}}<p class="bsub">{{.Subtitle}}</p>{{end}}
      {{if eq .Kind "table"}}{{if tableEmpty .}}<div class="empty">{{if .Empty}}{{.Empty}}{{else}}Nothing to report.{{end}}</div>{{else}}<div class="scroll"><table><thead><tr>{{range .Table.Columns}}<th{{if .Numeric}} class="num"{{end}}>{{.Name}}</th>{{end}}</tr></thead><tbody>{{$cols := .Table.Columns}}{{range .Table.Rows}}<tr>{{range $i, $c := .}}<td class="{{if (index $cols $i).Numeric}}num{{end}}{{if (index $cols $i).NoWrap}} nw{{end}}">{{cell $c}}</td>{{end}}</tr>{{end}}</tbody></table></div>{{end}}
      {{else if eq .Kind "chart"}}{{if chartHasData .Chart}}{{chart .Chart}}{{if showLegend .Chart}}{{legend .Chart}}{{end}}{{else}}<div class="empty">{{if .Empty}}{{.Empty}}{{else}}Nothing to chart.{{end}}</div>{{end}}
      {{else if eq .Kind "meters"}}{{if .Meters}}<div class="meters"><div class="row head"><div>Repository</div><div class="pct">Used</div><div>Fill</div><div>Projection</div></div>{{range .Meters}}<div class="row"><div class="n"><b>{{.Name}}</b>{{if .Detail}}<span>{{.Detail}}</span>{{end}}</div><div class="pct">{{pct .}}</div><div class="bar">{{meter .}}<div class="ts" style="margin-top:3px">{{.UsedText}}</div></div><div class="proj{{if .ProjectionWarn}} warn{{end}}">{{.Projection}}</div></div>{{end}}</div>{{else}}<div class="empty">{{if .Empty}}{{.Empty}}{{else}}Nothing to report.{{end}}</div>{{end}}
      {{else if eq .Kind "text"}}<p class="text">{{.Text}}</p>{{end}}
    </div>{{end}}</div>
  </section>{{end}}
  {{if .Notes}}<div class="foot">{{range .Notes}}<div><h4>{{.Title}}</h4><ul>{{range .Items}}<li>{{.}}</li>{{end}}</ul></div>{{end}}</div>{{end}}
  <div class="credit">Generated by Nexara reporting · {{.Title}} · all times UTC</div>
</div>
</body>
</html>`

// EscapeCSVCell prefixes cells whose first byte would cause a spreadsheet
// app to evaluate them as a formula with a single quote, neutralising the
// CSV formula-injection class (CWE-1236).
//
// Triggers: '=', '+', '-', '@', '\t', '\r'. The check is on the first byte
// because every trigger is ASCII (UTF-8 continuation bytes are >=0x80).
// See https://owasp.org/www-community/attacks/CSV_Injection.
func EscapeCSVCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// WriteSafeCSVRow writes a row through EscapeCSVCell on every cell.
func WriteSafeCSVRow(w *csv.Writer, row []string) error {
	safe := make([]string, len(row))
	for i, c := range row {
		safe[i] = EscapeCSVCell(c)
	}
	return w.Write(safe)
}

// cellCSV flattens a cell to one CSV value: the text, with the sub-line in
// parentheses so a timestamp under an age is not lost.
func cellCSV(c Cell) string {
	if c.Kind == CellChips {
		parts := make([]string, 0, len(c.Chips)+1)
		for _, ch := range c.Chips {
			parts = append(parts, ch.Text)
		}
		if c.Text != "" {
			parts = append(parts, c.Text)
		}
		return strings.Join(parts, " ")
	}
	if c.Sub != "" {
		return c.Text + " (" + c.Sub + ")"
	}
	return c.Text
}

// RenderCSV renders every part of the report as CSV: the headline figures,
// the findings, and each block as its own header-prefixed table. Charts and
// meters become the tables they were drawn from, so nothing the HTML shows
// is missing from the export.
func RenderCSV(data *ReportData) (string, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	_ = WriteSafeCSVRow(w, []string{"Report", data.Title})
	_ = WriteSafeCSVRow(w, []string{"Cluster", data.ClusterName})
	_ = WriteSafeCSVRow(w, []string{"Generated", data.GeneratedAt})
	_ = WriteSafeCSVRow(w, []string{"Period", data.TimeRange.StartTime + " to " + data.TimeRange.EndTime})
	for _, m := range data.Meta {
		_ = WriteSafeCSVRow(w, []string{m.Label, m.Value})
	}

	block := func(title string, header []string, rows [][]string) {
		_ = w.Write([]string{})
		_ = WriteSafeCSVRow(w, []string{"# " + title})
		if len(header) > 0 {
			_ = WriteSafeCSVRow(w, header)
		}
		for _, r := range rows {
			_ = WriteSafeCSVRow(w, r)
		}
	}

	if len(data.KPIs) > 0 {
		rows := make([][]string, 0, len(data.KPIs))
		for _, k := range data.KPIs {
			rows = append(rows, []string{k.Label, k.Value + k.Unit, k.Detail})
		}
		block("Key figures", []string{"Figure", "Value", "Detail"}, rows)
	}
	if len(data.Findings) > 0 {
		rows := make([][]string, 0, len(data.Findings))
		for _, f := range data.Findings {
			rows = append(rows, []string{f.Severity, strings.TrimSpace(f.Lead + " " + f.Text), f.Where})
		}
		block("Findings", []string{"Severity", "Finding", "Where"}, rows)
	}
	for _, s := range data.Sections {
		for _, b := range s.Blocks {
			title := s.Title
			if b.Title != "" {
				title += " — " + b.Title
			}
			switch b.Kind {
			case BlockTable:
				if b.Table == nil {
					continue
				}
				header := make([]string, 0, len(b.Table.Columns))
				for _, c := range b.Table.Columns {
					header = append(header, c.Name)
				}
				rows := make([][]string, 0, len(b.Table.Rows))
				for _, r := range b.Table.Rows {
					vals := make([]string, 0, len(r))
					for _, c := range r {
						vals = append(vals, cellCSV(c))
					}
					rows = append(rows, vals)
				}
				block(title, header, rows)
			case BlockChart:
				if b.Chart == nil {
					continue
				}
				c := b.Chart
				if c.Kind == ChartStackedBar {
					rows := make([][]string, 0, len(c.Series))
					for _, se := range c.Series {
						v := 0.0
						if len(se.Values) > 0 {
							v = se.Values[0]
						}
						rows = append(rows, []string{se.Name, formatValue(v, c.Format)})
					}
					block(title, []string{"Segment", "Value"}, rows)
					continue
				}
				header := []string{"Category"}
				for _, se := range c.Series {
					header = append(header, se.Name)
				}
				rows := make([][]string, 0, len(c.Categories))
				for i, cat := range c.Categories {
					row := []string{cat}
					for _, se := range c.Series {
						if i < len(se.Values) {
							row = append(row, formatValue(se.Values[i], c.Format))
						} else {
							row = append(row, "")
						}
					}
					rows = append(rows, row)
				}
				block(title, header, rows)
			case BlockMeters:
				rows := make([][]string, 0, len(b.Meters))
				for _, m := range b.Meters {
					pct := ""
					if m.Total > 0 {
						pct = fmt.Sprintf("%.1f", m.Used/m.Total*100)
					}
					rows = append(rows, []string{m.Name, m.Detail, m.UsedText, pct, m.Projection})
				}
				block(title, []string{"Name", "Detail", "Used", "Used %", "Projection"}, rows)
			case BlockText:
				block(title, nil, [][]string{{b.Text}})
			}
		}
	}
	for _, n := range data.Notes {
		rows := make([][]string, 0, len(n.Items))
		for _, it := range n.Items {
			rows = append(rows, []string{it})
		}
		block(n.Title, nil, rows)
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return "", fmt.Errorf("csv write: %w", err)
	}
	return buf.String(), nil
}
