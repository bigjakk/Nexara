package notifications

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"text/template"
	"unicode/utf8"
)

const maxTemplateOutput = 64 * 1024 // 64KB max rendered output

// renderTemplate renders a Go text/template with the given payload.
// Output is limited to 64KB to prevent memory exhaustion.
func renderTemplate(tmpl string, payload AlertPayload) (string, error) {
	if tmpl == "" {
		return "", nil
	}

	t, err := template.New("alert").
		Option("missingkey=error").
		Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}

	var buf bytes.Buffer
	lw := &limitWriter{w: &buf, remaining: maxTemplateOutput}
	if err := t.Execute(lw, payload); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	return buf.String(), nil
}

type limitWriter struct {
	w         io.Writer
	remaining int
}

func (lw *limitWriter) Write(p []byte) (int, error) {
	if lw.remaining <= 0 {
		return 0, fmt.Errorf("template output exceeds size limit")
	}
	if len(p) > lw.remaining {
		p = p[:lw.remaining]
	}
	n, err := lw.w.Write(p)
	lw.remaining -= n
	return n, err
}

// defaultMessage builds a plain-text notification message from a payload.
func defaultMessage(payload AlertPayload) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[%s] %s\n", strings.ToUpper(payload.Severity), payload.RuleName)
	fmt.Fprintf(&sb, "Resource: %s\n", payload.ResourceName)
	fmt.Fprintf(&sb, "Metric: %s %s %.2f (current: %.2f)\n",
		payload.Metric, payload.Operator, payload.Threshold, payload.CurrentValue)
	fmt.Fprintf(&sb, "State: %s\n", payload.State)
	if payload.FiredAt != "" {
		fmt.Fprintf(&sb, "Fired at: %s\n", payload.FiredAt)
	}
	return sb.String()
}

// maxChatMessage bounds the message a chat channel's text carries.
const maxChatMessage = 3000

// withMessage is a chat channel's built-in text with the notification's message
// under it: a rule's rendered template, a CVE notice's list. Every chat
// channel sends it, so the message reaches each channel type, not only email
// and webhook; a channel's own template replaces the text and can place the
// message itself with {{.Message}}. escape is applied to the message alone.
func withMessage(text string, p AlertPayload, escape func(string) string) string {
	if p.Message == "" {
		return text
	}
	msg := p.Message
	if len(msg) > maxChatMessage {
		end := maxChatMessage
		for end > 0 && !utf8.RuneStart(msg[end]) {
			end--
		}
		msg = msg[:end] + "…"
	}
	if escape != nil {
		msg = escape(msg)
	}
	return strings.TrimRight(text, "\n") + "\n\n" + msg
}

// severityColor returns a hex color for the severity level.
func severityColor(severity string) int {
	switch severity {
	case "critical":
		return 0xDC2626 // red
	case "warning":
		return 0xF59E0B // amber
	default:
		return 0x3B82F6 // blue
	}
}

// severityEmoji returns an emoji for the severity level.
func severityEmoji(severity string) string {
	switch severity {
	case "critical":
		return "🔴"
	case "warning":
		return "🟡"
	default:
		return "🔵"
	}
}
