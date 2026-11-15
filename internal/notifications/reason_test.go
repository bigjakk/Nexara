package notifications

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"net/url"
	"strings"
	"syscall"
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
		{"smtp: auth: 535 bad credentials for alerts/pw-PROBE-1", "smtp: auth: 535 bad credentials for (redacted)/(redacted)"},
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

// TestFailureClass: a caller and a dead letter get the kind of failure and
// nothing the target said — no banner, no certificate names, no greeting.
func TestFailureClass(t *testing.T) {
	t.Parallel()
	banner := &url.Error{Op: "Post", URL: "http://192.0.2.1:22/", Err: errors.New(`malformed HTTP response "SSH-2.0-OpenSSH_9.6"`)}
	cert := &x509.Certificate{DNSNames: []string{"db.example.com"}}
	for _, tt := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("send request: %w", banner), "the request failed"},
		{&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, "could not connect"},
		{fmt.Errorf("send request: %w", context.DeadlineExceeded), "timed out"},
		{fmt.Errorf("tls: %w", x509.HostnameError{Certificate: cert, Host: "192.0.2.1"}), "TLS failed: certificate not valid for this host"},
		{fmt.Errorf("smtp: mail from: %w", &textproto.Error{Code: 550, Msg: "mx01.example.com says no"}), "SMTP code 550"},
		{textproto.ProtocolError("short response: SSH-2.0-OpenSSH"), "the target did not answer as an SMTP server"},
		{&HTTPStatusError{Code: 401}, "HTTP status 401"},
		{errors.New("webhook config: url required"), "webhook config: url required"},
	} {
		if got := FailureClass(tt.err, nil); got != tt.want {
			t.Errorf("FailureClass(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}
