package notifications

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestSendErrorsKeepNoChannelSecret holds a failed send's error, which a
// dead-letter row every Viewer can read keeps, to its channel's scheme and host:
// a chat webhook's path and token query are its secret. Each sender's own
// request path, and the URL check both share, fail here without a network.
func TestSendErrorsKeepNoChannelSecret(t *testing.T) {
	t.Parallel()
	const probe = "PROBE-WEBHOOK-SECRET-NOT-A-REAL-VALUE"
	hook := "https://192.0.2.1/services/T000/B000/" + probe + "?token=" + probe
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tt := range []struct {
		name       string
		dispatcher Dispatcher
		ctx        context.Context
		config     map[string]string
	}{
		{"a chat webhook's send", &SlackDispatcher{}, canceled, map[string]string{"webhook_url": hook}},
		{"a webhook's send", &WebhookDispatcher{}, canceled, map[string]string{"url": hook}},
		{"a URL that does not parse", &DiscordDispatcher{}, context.Background(), map[string]string{"webhook_url": hook + "\x7f"}},
	} {
		config, _ := json.Marshal(tt.config)
		err := tt.dispatcher.Send(tt.ctx, config, AlertPayload{RuleName: "rule01", Severity: "warning"})
		if err == nil || strings.Contains(err.Error(), probe) || !strings.Contains(err.Error(), "https://192.0.2.1/(set)") {
			t.Errorf("%s: error %v, want one naming the channel as https://192.0.2.1/(set)", tt.name, err)
		}
	}
}
