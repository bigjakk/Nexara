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

// MaxTemplateLen caps a notification template's length, at save and at
// render: with range and template refused, the template's own length is what
// bounds how much work rendering it can do.
const MaxTemplateLen = 8 * 1024

// renderTemplate renders a Go text/template with the given payload.
// Output is limited to 64KB to prevent memory exhaustion.
func renderTemplate(tmpl string, payload AlertPayload) (string, error) {
	if tmpl == "" {
		return "", nil
	}
	t, err := parseTemplate(tmpl)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	lw := &limitWriter{w: &buf, remaining: maxTemplateOutput}
	if err := t.Execute(lw, payload); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	return buf.String(), nil
}

// The limits a notification template is held to, each refused by name.
var (
	// errTemplateLength: longer than MaxTemplateLen.
	errTemplateLength = fmt.Errorf("a notification template may be at most %d bytes", MaxTemplateLen)
	// errTemplateRange: a notification has nothing to range over, and Go
	// ranges over an integer — {{range 99999999999}} runs for hours inside
	// the alert engine's evaluation tick, writing nothing the output cap
	// could stop.
	errTemplateRange = errors.New("a notification template cannot use range: a notification has nothing to range over")
	// errTemplateCall: {{template}} and {{block}} call other templates, and
	// chained calls run 2^n times writing nothing.
	errTemplateCall = errors.New("a notification template cannot use template or block")
	// errTemplateFuncInput: a print, printf, println, html, js or urlquery
	// handed more than it may build from — refused before it builds anything,
	// since each builds its whole result before the output cap sees a byte.
	errTemplateFuncInput = fmt.Errorf("a print, printf, println, html, js or urlquery in a notification template may be handed at most %d bytes", maxTemplateOutput)
	// errTemplateFuncOutput: one whose result would pass the output cap.
	errTemplateFuncOutput = fmt.Errorf("a print, printf, println, html, js or urlquery in a notification template may produce at most %d bytes", maxTemplateOutput)
)

// templateFuncs replace (Funcs takes precedence) every text/template builtin
// that can return a string longer than its input: print, println, printf,
// html, js and urlquery. The invariant they keep: each checks its input
// BEFORE it formats or escapes, and caps its result, so every string a
// template can hold is at most maxTemplateOutput and no call builds more than
// a small multiple of it (js, the widest escaper, at most 6x). The rest of the
// builtins return no string longer than one they were handed: len, index and
// slice return a length, a byte or a substring; and, or, not and the
// comparisons return one of their operands or a bool; call needs a func value,
// and AlertPayload has no func field and no methods.
var templateFuncs = template.FuncMap{
	"print":   bounded(fmt.Sprint),
	"println": bounded(fmt.Sprintln),
	"printf": func(format string, a ...any) (string, error) {
		if printfPadding(format) > maxTemplateOutput || printfSize(format, a) > maxTemplateOutput {
			return "", errTemplateFuncInput
		}
		return capped(fmt.Sprintf(format, a...))
	},
	// The escapers keep text/template's own work, and so its output.
	"html":     bounded(template.HTMLEscaper),
	"js":       bounded(template.JSEscaper),
	"urlquery": bounded(template.URLQueryEscaper),
}

// bounded is f refused for arguments over the cap before it runs, and for a
// result over it after.
func bounded(f func(...any) string) func(...any) (string, error) {
	return func(a ...any) (string, error) {
		if total, _ := argsSize(a); total > maxTemplateOutput {
			return "", errTemplateFuncInput
		}
		return capped(f(a...))
	}
}

// argsSize measures arguments before anything is built from them: a string
// by its length, any other value by its own fmt.Sprint's (a payload's values
// are small). It returns their total, stopping once it passes the cap, and
// the largest one.
func argsSize(a []any) (total, largest int) {
	for _, v := range a {
		n := 0
		if s, ok := v.(string); ok {
			n = len(s)
		} else {
			n = len(fmt.Sprint(v))
		}
		total += n
		largest = max(largest, n)
		if total > maxTemplateOutput {
			return total, largest
		}
	}
	return total, largest
}

// printfSize bounds what a printf builds from its arguments: every verb may
// print any argument (%[1]s repeats one), and every argument no verb takes
// is printed too (%!(EXTRA …)), so (verbs + arguments) × the largest.
func printfSize(format string, a []any) int {
	_, largest := argsSize(a)
	return (strings.Count(format, "%") + len(a)) * largest
}

func capped(s string) (string, error) {
	if len(s) > maxTemplateOutput {
		return "", errTemplateFuncOutput
	}
	return s, nil
}

// printfPadding is the sum of the widths and precisions a format asks for —
// the bytes its verbs pad to whatever their arguments — with a "*" width
// counted as over any cap.
func printfPadding(format string) int {
	total := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		for i++; i < len(format) && strings.IndexByte("+-# 0", format[i]) >= 0; i++ {
		}
		for i < len(format) && (format[i] == '.' || format[i] == '*' || format[i] == '[' ||
			format[i] == ']' || (format[i] >= '0' && format[i] <= '9')) {
			if format[i] == '*' {
				return maxTemplateOutput + 1
			}
			n := 0
			for ; i < len(format) && format[i] >= '0' && format[i] <= '9'; i++ {
				if n = n*10 + int(format[i]-'0'); n > maxTemplateOutput {
					return maxTemplateOutput + 1
				}
			}
			total += n
			if total > maxTemplateOutput {
				return total
			}
			if i < len(format) && (format[i] == '.' || format[i] == '[' || format[i] == ']') {
				i++
			}
		}
	}
	return total
}

// parseTemplate parses tmpl with templateFuncs, and refuses it for its length
// or for using range, template or block.
func parseTemplate(tmpl string) (*template.Template, error) {
	if len(tmpl) > MaxTemplateLen {
		return nil, errTemplateLength
	}
	t, err := template.New("alert").Funcs(templateFuncs).Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	for _, tt := range t.Templates() {
		if tt.Tree == nil {
			continue
		}
		if err := refusedNode(tt.Root); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// CheckTemplate refuses, before a template is saved, one renderTemplate
// would refuse for a limit: its length, a range, a template or block. One
// that does not parse passes; a send then falls back to its built-in text.
func CheckTemplate(tmpl string) error {
	if tmpl == "" {
		return nil
	}
	_, err := parseTemplate(tmpl)
	if errors.Is(err, errTemplateLength) || errors.Is(err, errTemplateRange) || errors.Is(err, errTemplateCall) {
		return err
	}
	return nil
}

// refusedNode walks a template's tree for a node a notification template may
// not use.
func refusedNode(n parse.Node) error {
	switch n := n.(type) {
	case *parse.RangeNode:
		return errTemplateRange
	case *parse.TemplateNode:
		return errTemplateCall
	case *parse.ListNode:
		if n == nil {
			return nil
		}
		for _, c := range n.Nodes {
			if err := refusedNode(c); err != nil {
				return err
			}
		}
	case *parse.IfNode:
		return firstErr(refusedNode(n.List), refusedNode(n.ElseList))
	case *parse.WithNode:
		return firstErr(refusedNode(n.List), refusedNode(n.ElseList))
	}
	return nil
}

func firstErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
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
