package notifications

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/template"
	"text/template/parse"
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
	if err := refuseRange(t); err != nil {
		return "", err
	}

	var buf bytes.Buffer
	lw := &limitWriter{w: &buf, remaining: maxTemplateOutput}
	if err := t.Execute(lw, payload); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	return buf.String(), nil
}

// errTemplateRange refuses a template with a range in it. A notification has
// nothing to range over, and Go ranges over an integer: {{range 99999999999}}
// runs for hours inside the alert engine's evaluation tick, writing nothing
// the output cap could stop.
var errTemplateRange = errors.New("a notification template cannot use range: a notification has nothing to range over")

// CheckTemplate refuses a template renderTemplate would refuse for its
// shape — a range — before it is saved. One that does not parse passes; a
// send then falls back to its built-in text.
func CheckTemplate(tmpl string) error {
	if tmpl == "" {
		return nil
	}
	t, err := template.New("alert").Parse(tmpl)
	if err != nil {
		return nil //nolint:nilerr // a parse failure is the send's to fall back from
	}
	return refuseRange(t)
}

// refuseRange walks every template t defines for a range node.
func refuseRange(t *template.Template) error {
	for _, tt := range t.Templates() {
		if tt.Tree != nil && hasRange(tt.Root) {
			return errTemplateRange
		}
	}
	return nil
}

func hasRange(n parse.Node) bool {
	switch n := n.(type) {
	case *parse.RangeNode:
		return true
	case *parse.ListNode:
		if n == nil {
			return false
		}
		for _, c := range n.Nodes {
			if hasRange(c) {
				return true
			}
		}
	case *parse.IfNode:
		return hasRange(n.List) || hasRange(n.ElseList)
	case *parse.WithNode:
		return hasRange(n.List) || hasRange(n.ElseList)
	}
	return false
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

// chatText is a Slack or Teams message's text: the channel's own template
// (tmpl), or the built-in text with the notification's message under it.
func chatText(tmpl string, payload AlertPayload) string {
	if tmpl != "" {
		if rendered, err := renderTemplate(tmpl, payload); err == nil {
			return rendered
		}
	}
	return withMessage(defaultMessage(payload), payload, nil)
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
