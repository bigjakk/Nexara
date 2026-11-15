package notifications

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"
)

// maxReasonLen bounds a failure reason handed back to a caller.
const maxReasonLen = 512

// secretConfigKeys are the channel settings whose values are secrets: a
// password, a token, a URL whose path or query is the credential, an SMTP
// username (for SES, the access key id). Every header value is one too (a
// webhook's Authorization).
var secretConfigKeys = map[string]bool{
	"password": true, "username": true, "url": true, "webhook_url": true, "bot_token": true, "routing_key": true,
}

// FailureReason is a failed send's error as a caller and the log may see it:
// any secret the channel's settings hold is cut out, and it is bounded.
//
// The senders already keep their errors free of the channel's URL path and
// query (targetErr); this is the second line, for what a peer echoes back — an
// SMTP server's reply, a resolver's message — which no sender controls.
func FailureReason(err error, config json.RawMessage) string {
	if err == nil {
		return ""
	}
	reason := err.Error()
	for _, secret := range configSecrets(config) {
		reason = strings.ReplaceAll(reason, secret, "(redacted)")
	}
	if len(reason) > maxReasonLen {
		end := maxReasonLen
		for end > 0 && !utf8.RuneStart(reason[end]) {
			end--
		}
		reason = reason[:end] + "…"
	}
	return reason
}

// configSecrets lists the secret strings in a channel's settings, longest
// first so a URL is cut whole before its path is.
func configSecrets(config json.RawMessage) []string {
	var cfg map[string]json.RawMessage
	if json.Unmarshal(config, &cfg) != nil {
		return nil
	}
	var out []string
	add := func(s string) {
		// A value this short is no secret worth the damage cutting it out of
		// every word it appears in would do.
		if len(s) >= 4 {
			out = append(out, s)
		}
	}
	for key, raw := range cfg {
		if key == "headers" {
			var headers map[string]string
			if json.Unmarshal(raw, &headers) == nil {
				for _, v := range headers {
					add(v)
				}
			}
			continue
		}
		var s string
		if !secretConfigKeys[key] || json.Unmarshal(raw, &s) != nil {
			continue
		}
		add(s)
		if u, err := url.Parse(s); err == nil && u.Host != "" {
			if len(u.Path) > 1 {
				add(u.Path)
			}
			add(u.RawQuery)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return len(b) - len(a) })
	return out
}

// HTTPStatusError is a target's answer outside 2xx.
type HTTPStatusError struct{ Code int }

func (e *HTTPStatusError) Error() string { return fmt.Sprintf("unexpected status %d", e.Code) }

// FailureClass is a failed send's error as a caller, or a dead letter every
// Viewer reads, may see it: the kind of failure and nothing the target said.
// A reply that quoted the target would make a channel a probe of whatever it
// points at — an open port against a closed one, a service's banner, the names
// on its certificate, an SMTP greeting. An error Nexara raised itself before
// reaching the target (a missing setting, a refused address) is kept, with the
// channel's secrets cut out (FailureReason). The log keeps the full detail.
func FailureClass(err error, config json.RawMessage) string {
	if err == nil {
		return ""
	}
	var (
		target    *TargetAddressError
		status    *HTTPStatusError
		smtpCode  *textproto.Error
		protoErr  textproto.ProtocolError
		dnsErr    *net.DNSError
		netErr    net.Error
		unknownCA x509.UnknownAuthorityError
		hostErr   x509.HostnameError
		certErr   x509.CertificateInvalidError
		verifyErr *tls.CertificateVerificationError
		recordErr tls.RecordHeaderError
		alertErr  tls.AlertError
		opErr     *net.OpError
		urlErr    *url.Error
	)
	switch {
	case errors.As(err, &target):
		return "refused: the target resolves to " + target.Error()
	case errors.As(err, &status):
		return fmt.Sprintf("HTTP status %d", status.Code)
	case errors.As(err, &smtpCode):
		return fmt.Sprintf("SMTP code %d", smtpCode.Code)
	case errors.As(err, &protoErr):
		return "the target did not answer as an SMTP server"
	case errors.As(err, &dnsErr):
		return "could not resolve the host name"
	case errors.As(err, &unknownCA):
		return "TLS failed: certificate not trusted"
	case errors.As(err, &hostErr):
		return "TLS failed: certificate not valid for this host"
	case errors.As(err, &certErr):
		return "TLS failed: certificate not valid (expired, not yet valid or not for this use)"
	case errors.As(err, &verifyErr), errors.As(err, &recordErr), errors.As(err, &alertErr):
		return "TLS failed"
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return "timed out"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.As(err, &opErr):
		return "could not connect"
	case errors.As(err, &urlErr), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "the request failed"
	}
	return FailureReason(err, config)
}
