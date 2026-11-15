package notifications

import (
	"log/slog"
	"strings"
	"testing"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestRuleMessage: a rule's template is the message every channel type
// carries, and {{.Message}} in it is the alert's own message, not "".
func TestRuleMessage(t *testing.T) {
	t.Parallel()
	e := &Engine{logger: slog.New(slog.DiscardHandler)}
	p := AlertPayload{ResourceName: "pve-01", Severity: "warning"}
	const builtIn = "cpu_usage > 90 on pve-01 (current: 95.00)"
	for _, tt := range []struct{ tmpl, want string }{
		{"", ""},
		{"{{.Severity}}: {{.Message}}", "warning: " + builtIn},
		{"{{.NoSuchField}}", builtIn},
	} {
		if got := e.ruleMessage(db.AlertRule{MessageTemplate: tt.tmpl}, p, builtIn); got != tt.want {
			t.Errorf("template %q = %q, want %q", tt.tmpl, got, tt.want)
		}
	}
}

// TestWithMessage: the chat channels' text carries the message, bounded and,
// for Telegram's HTML mode, escaped.
func TestWithMessage(t *testing.T) {
	t.Parallel()
	esc := strings.NewReplacer("<", "&lt;").Replace
	for _, tt := range []struct {
		msg  string
		esc  func(string) string
		want string
	}{
		{"", nil, "base\n"},
		{"a <b>", esc, "base\n\na &lt;b>"},
		{strings.Repeat("x", maxChatMessage+5), nil, "base\n\n" + strings.Repeat("x", maxChatMessage) + "…"},
	} {
		if got := withMessage("base\n", AlertPayload{Message: tt.msg}, tt.esc); got != tt.want {
			t.Errorf("withMessage(%.20q) = %.40q, want %.40q", tt.msg, got, tt.want)
		}
	}
}
