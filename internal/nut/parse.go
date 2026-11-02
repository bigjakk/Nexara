package nut

import (
	"errors"
	"strings"
)

// ProtocolError is an ERR reply from upsd. Code is upsd's token, such as
// DATA-STALE. Error() names only the code: the rest of the line is the
// server's own text, and these errors reach API callers.
type ProtocolError struct {
	Code  string
	Extra string
}

func (e *ProtocolError) Error() string {
	return "nut: upsd replied ERR " + e.Code
}

// parseErr returns the ProtocolError for an "ERR <CODE> …" line, or nil.
func parseErr(line string) *ProtocolError {
	if line != "ERR" && !strings.HasPrefix(line, "ERR ") {
		return nil
	}
	toks, err := splitLine(line)
	if err != nil || len(toks) < 2 {
		return &ProtocolError{Code: "MALFORMED"}
	}
	return &ProtocolError{Code: clip(toks[1], 64), Extra: clip(strings.Join(toks[2:], " "), 128)}
}

func errCode(err error) string {
	var pe *ProtocolError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

// IsStale reports whether upsd knows the UPS but has no fresh data for it:
// the driver has stopped updating, or is not running at all.
func IsStale(err error) bool {
	code := errCode(err)
	return code == "DATA-STALE" || code == "DRIVER-NOT-CONNECTED"
}

// IsAuth reports whether upsd refused the host or the credentials.
func IsAuth(err error) bool {
	switch errCode(err) {
	case "ACCESS-DENIED", "USERNAME-REQUIRED", "PASSWORD-REQUIRED", "INVALID-USERNAME", "INVALID-PASSWORD":
		return true
	}
	return false
}

// IsUnknownUPS reports whether upsd has no UPS by that name.
func IsUnknownUPS(err error) bool {
	return errCode(err) == "UNKNOWN-UPS"
}

// splitLine splits one reply into its words. A double-quoted word may hold
// spaces and the escapes \" and \\, which is how upsd sends every value.
func splitLine(line string) ([]string, error) {
	var (
		toks    []string
		cur     strings.Builder
		inWord  bool
		quoted  bool
		escaped bool
	)
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case escaped:
			cur.WriteByte(ch)
			escaped = false
		case quoted && ch == '\\':
			escaped = true
		case ch == '"':
			quoted = !quoted
			inWord = true
		case !quoted && (ch == ' ' || ch == '\t'):
			if inWord {
				toks = append(toks, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteByte(ch)
			inWord = true
		}
	}
	if quoted || escaped {
		return nil, errors.New("nut: unterminated quote in reply")
	}
	if inWord {
		toks = append(toks, cur.String())
	}
	return toks, nil
}

// quoteArg quotes a command argument when upsd's tokenizer would otherwise
// split it or read part of it as syntax. upsd's parser (parseconf.c) takes an
// unescaped # as the start of a comment, and as a parse error inside quotes,
// so it is escaped along with \ and ", the set upsclient's pconf_encode uses.
func quoteArg(a string) string {
	if a != "" && !strings.ContainsAny(a, " \t\"\\#") {
		return a
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `#`, `\#`)
	return `"` + r.Replace(a) + `"`
}
