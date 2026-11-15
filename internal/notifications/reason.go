package notifications

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"
)

// maxReasonLen bounds a failure reason handed back to a caller.
const maxReasonLen = 512

// secretConfigKeys are the channel settings whose values are secrets: a
// password, a token, a URL whose path or query is the credential. Every header
// value is one too (a webhook's Authorization).
var secretConfigKeys = map[string]bool{
	"password": true, "url": true, "webhook_url": true, "bot_token": true, "routing_key": true,
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
