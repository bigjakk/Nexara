package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

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

// Each chat channel's request carries the notification's message, escaped in
// Telegram's HTML mode.
func TestChatBodiesCarryTheMessage(t *testing.T) {
	t.Parallel()
	p := AlertPayload{RuleName: "rule01", Severity: "warning", Message: "disk <full> on linux01"}
	for _, tt := range []struct {
		name string
		body map[string]interface{}
		want string
	}{
		{"slack", slackBody(slackConfig{}, p), "disk <full> on linux01"},
		{"teams", teamsBody(teamsConfig{}, p), "disk <full> on linux01"},
		{"discord", discordBody(discordConfig{}, p), "disk <full> on linux01"},
		{"telegram", telegramBody(telegramConfig{ParseMode: "HTML"}, p), "disk &lt;full&gt; on linux01"},
	} {
		raw, err := json.Marshal(tt.body)
		if err != nil {
			t.Fatal(err)
		}
		var decoded any
		_ = json.Unmarshal(raw, &decoded)
		if !strings.Contains(fmt.Sprint(decoded), tt.want) {
			t.Errorf("%s body %s does not carry %q", tt.name, raw, tt.want)
		}
	}
}

// A rule's rendered template is the message the engine hands each channel.
func TestDispatchCarriesTheRuleMessage(t *testing.T) {
	t.Parallel()
	sent := make(chan AlertPayload, 1)
	e, _, ch := newDispatchEngine(t, recordingDispatcher{sent: sent})
	rule := db.AlertRule{ID: uuid.New(), Metric: "cpu_usage", Operator: ">", Threshold: 90, MessageTemplate: "{{.Message}}!"}
	e.dispatchToChannel(context.Background(), rule, uuid.New(), ch.ID, 95, "pve-01", "firing", 0)
	select {
	case got := <-sent:
		if want := "cpu_usage > 90 on pve-01 (current: 95.00)!"; got.Message != want {
			t.Errorf("message %q, want %q", got.Message, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("nothing was sent")
	}
}
