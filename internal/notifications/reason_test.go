package notifications

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestFailureReason: a failed send's reason reaches the caller and the log, so
// the secrets a channel's settings hold are cut out of whatever a peer echoed.
func TestFailureReason(t *testing.T) {
	t.Parallel()
	config := json.RawMessage(`{"host":"smtp.example.com","username":"alerts","password":"pw-PROBE-1",
		"url":"https://hooks.example.com/T0/PROBE-2?sig=PROBE-3","headers":{"Authorization":"Bearer PROBE-4"},
		"chat_id":"PROBE-5"}`)
	for _, tt := range []struct{ err, want string }{
		{"smtp: auth: 535 bad credentials for alerts/pw-PROBE-1", "smtp: auth: 535 bad credentials for alerts/(redacted)"},
		{"send request: https://hooks.example.com/T0/PROBE-2?sig=PROBE-3", "send request: (redacted)"},
		{"bad path /T0/PROBE-2 or query sig=PROBE-3", "bad path (redacted) or query (redacted)"},
		{"upstream said Bearer PROBE-4", "upstream said (redacted)"},
		// Not a secret: a chat id, the host.
		{"chat PROBE-5 on smtp.example.com", "chat PROBE-5 on smtp.example.com"},
		{strings.Repeat("é", 400), strings.Repeat("é", 256) + "…"},
	} {
		if got := FailureReason(errors.New(tt.err), config); got != tt.want {
			t.Errorf("FailureReason(%q) = %q, want %q", tt.err, got, tt.want)
		}
	}
}
