// Package redact holds what Nexara keeps in place of a secret in a text it
// records or passes on, and how it finds the parts of a URL that can carry one.
package redact

import (
	"regexp"
	"strings"
)

// What a record keeps in place of a secret: Set for a value, Cleared for one
// sent empty, which clears it.
const (
	Set     = "(set)"
	Cleared = "(cleared)"
)

// The parts of a URL in a text that can carry a secret, each from the scheme's
// "://". Its userinfo runs to the last "@" before a "/", "?" or "#" ends the
// authority; white space does not end it, so a password holding a space goes
// whole, and prose with a "://" and a later "@" may lose the words between. Its
// query runs from the "?" after the path, and its fragment from the "#", to the
// ASCII white space (tab, line feed, form feed, carriage return, space), quote,
// angle bracket or backtick that ends the URL: a pre-signed URL's signature
// (X-Amz-Signature, X-Goog-Signature), a SAS token's sig, a private_token; an
// access_token or a share link's key. A "?" or "#" with nothing after it is
// left. migrations/000127_redact_audit_secrets applies the same three patterns,
// in the same order, to the rows written before them.
var (
	userinfoRe = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/?#]*@`)
	queryRe    = regexp.MustCompile("([A-Za-z][A-Za-z0-9+.-]*://[^\\t\\n\\f\\r ?#\"'<>`]*)\\?[^\\t\\n\\f\\r #\"'<>`]+")
	fragmentRe = regexp.MustCompile("([A-Za-z][A-Za-z0-9+.-]*://[^\\t\\n\\f\\r #\"'<>`]*)#[^\\t\\n\\f\\r \"'<>`]+")
)

// URLs is s with the userinfo, the query and the fragment of every URL in it
// each kept as Set: "https://(set)@host/path?(set)#(set)". The scheme, host and
// path stay, so a record still says where the URL went.
func URLs(s string) string {
	if !strings.Contains(s, "://") {
		return s
	}
	s = userinfoRe.ReplaceAllString(s, "${1}"+Set+"@")
	s = queryRe.ReplaceAllString(s, "${1}?"+Set)
	return fragmentRe.ReplaceAllString(s, "${1}#"+Set)
}

// TargetURL is a URL whose path is a secret too — a chat webhook's (Slack,
// Discord, Teams), a bot's token (Telegram) — kept as its scheme and host alone:
// its userinfo becomes "(set)@" and whatever follows the host "/(set)". A text
// with no "://" in it cannot be told apart from its secret and is kept as Set.
func TargetURL(s string) string {
	scheme, rest, ok := strings.Cut(s, "://")
	if !ok {
		return Set
	}
	authority, tail := rest, ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		authority, tail = rest[:i], rest[i:]
	}
	if i := strings.LastIndex(authority, "@"); i >= 0 {
		authority = Set + "@" + authority[i+1:]
	}
	if tail != "" && tail != "/" {
		tail = "/" + Set
	}
	return scheme + "://" + authority + tail
}
