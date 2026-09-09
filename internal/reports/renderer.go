package reports

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"html/template"
)

// RenderHTML renders the report data as a self-contained HTML page.
func RenderHTML(data *ReportData) (string, error) {
	// We build HTML manually for reliable table row rendering since
	// nested range + index on maps is tricky in Go templates.
	var buf bytes.Buffer
	tmpl, err := template.New("report").Parse(htmlShell)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}

	type templateData struct {
		Title        string
		ClusterName  string
		GeneratedAt  string
		TimeRange    TimeRange
		SectionsHTML template.HTML
	}

	// Build sections HTML manually.
	var sectBuf bytes.Buffer
	for _, s := range data.Sections {
		sectBuf.WriteString(`<div class="section"><h2>`)
		sectBuf.WriteString(template.HTMLEscapeString(s.Title))
		sectBuf.WriteString(`</h2>`)

		if len(s.Rows) == 0 {
			sectBuf.WriteString(`<p>No data available.</p></div>`)
			continue
		}

		sectBuf.WriteString(`<table><thead><tr>`)
		for _, h := range s.Headers {
			sectBuf.WriteString(`<th>`)
			sectBuf.WriteString(template.HTMLEscapeString(h))
			sectBuf.WriteString(`</th>`)
		}
		sectBuf.WriteString(`</tr></thead><tbody>`)

		for _, row := range s.Rows {
			sectBuf.WriteString(`<tr>`)
			for _, h := range s.Headers {
				sectBuf.WriteString(`<td>`)
				sectBuf.WriteString(template.HTMLEscapeString(row[h]))
				sectBuf.WriteString(`</td>`)
			}
			sectBuf.WriteString(`</tr>`)
		}
		sectBuf.WriteString(`</tbody></table></div>`)
	}

	td := templateData{
		Title:        data.Title,
		ClusterName:  data.ClusterName,
		GeneratedAt:  data.GeneratedAt,
		TimeRange:    data.TimeRange,
		SectionsHTML: template.HTML(sectBuf.String()), //nolint:gosec // G203: all user data is escaped via template.HTMLEscapeString above
	}

	if err := tmpl.Execute(&buf, td); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	return buf.String(), nil
}

const htmlShell = `<!DOCTYPE html>
<html>
<head>
<meta charset="UTF-8">
<title>{{.Title}}</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; margin: 0; padding: 20px; background: #f8f9fa; color: #1a1a2e; }
  .report { max-width: 1100px; margin: 0 auto; background: white; border-radius: 8px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); overflow: hidden; }
  .header { background: #1e293b; color: white; padding: 24px 32px; }
  .header h1 { margin: 0 0 8px 0; font-size: 24px; }
  .header .meta { font-size: 13px; opacity: 0.8; }
  .content { padding: 24px 32px; }
  .section { margin-bottom: 32px; }
  .section h2 { font-size: 18px; margin: 0 0 12px 0; padding-bottom: 8px; border-bottom: 2px solid #e2e8f0; }
  table { width: 100%; border-collapse: collapse; font-size: 14px; }
  th { background: #f1f5f9; text-align: left; padding: 10px 12px; font-weight: 600; border-bottom: 2px solid #e2e8f0; }
  td { padding: 8px 12px; border-bottom: 1px solid #f1f5f9; }
  tr:hover td { background: #f8fafc; }
  .footer { padding: 16px 32px; border-top: 1px solid #e2e8f0; color: #94a3b8; font-size: 12px; }

  /* Print / Save-as-PDF. Browsers drop background colours from printed output
     unless the user ticks "Background graphics", so anything that relied on a
     dark fill has to restate its colours here — the header was white text on a
     #1e293b bar, i.e. invisible on paper. Rather than force the fill on with
     print-color-adjust and burn a page of ink, print inverts the header to dark
     text on white and keeps the rule beneath it. */
  @page { margin: 14mm; }
  @media print {
    body { background: white; padding: 0; }
    /* overflow is reset with the rest: a non-visible overflow on the element
       wrapping the whole report is the classic cause of printed output being
       clipped to the first page. */
    .report { box-shadow: none; border-radius: 0; max-width: none; overflow: visible; }
    .header { background: none; color: #1a1a2e; padding: 0 0 12px 0; border-bottom: 3px solid #1e293b; }
    .header .meta { opacity: 1; color: #475569; }
    .content { padding: 16px 0 0 0; }
    .footer { padding: 12px 0 0 0; }
    /* Repeat column headers on every page a table spills onto, and keep a row
       from being sliced through the middle. */
    thead { display: table-header-group; }
    tr, td, th { break-inside: avoid; }
    th { background: none; border-bottom: 2px solid #94a3b8; }
    td { border-bottom: 1px solid #cbd5e1; }
    tr:hover td { background: none; }
    /* A section heading stranded alone at the foot of a page reads as an empty
       section; keep it with the content it introduces. The section itself is
       free to span pages — some are longer than one. */
    .section h2 { break-after: avoid; }
    .section { margin-bottom: 20px; }
  }
</style>
</head>
<body>
<div class="report">
  <div class="header">
    <h1>{{.Title}}</h1>
    <div class="meta">
      Cluster: {{.ClusterName}} | Period: {{.TimeRange.StartTime}} to {{.TimeRange.EndTime}} | Generated: {{.GeneratedAt}}
    </div>
  </div>
  <div class="content">
    {{.SectionsHTML}}
  </div>
  <div class="footer">Generated by Nexara Reporting System</div>
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

// RenderCSV renders the report data as CSV text.
// Each section becomes a block separated by an empty line.
func RenderCSV(data *ReportData) (string, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	// Header row with report metadata
	_ = WriteSafeCSVRow(w, []string{"Report", data.Title})
	_ = WriteSafeCSVRow(w, []string{"Cluster", data.ClusterName})
	_ = WriteSafeCSVRow(w, []string{"Generated", data.GeneratedAt})
	_ = WriteSafeCSVRow(w, []string{"Period", data.TimeRange.StartTime + " to " + data.TimeRange.EndTime})
	_ = w.Write([]string{}) // blank line

	for i, s := range data.Sections {
		if i > 0 {
			_ = w.Write([]string{}) // section separator
		}
		_ = WriteSafeCSVRow(w, []string{"# " + s.Title})
		if len(s.Headers) > 0 {
			_ = WriteSafeCSVRow(w, s.Headers)
		}
		for _, row := range s.Rows {
			vals := make([]string, len(s.Headers))
			for j, h := range s.Headers {
				vals[j] = row[h]
			}
			_ = WriteSafeCSVRow(w, vals)
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return "", fmt.Errorf("csv write: %w", err)
	}

	return buf.String(), nil
}
