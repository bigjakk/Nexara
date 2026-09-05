package veeam

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors the API layer branches on to turn a client failure into an
// actionable message. Everything else surfaces as *APIError.
var (
	// ErrAuthFailed indicates VBR rejected the username/password pair.
	// Veeam answers a mangled DOMAIN\user with a 401 that is byte-identical
	// to the one a wrong password produces, so authFailure attaches a hint
	// when the username looks domain-qualified.
	ErrAuthFailed = errors.New("veeam: authentication failed")

	// ErrVersionUnsupported indicates the server is older than MinBuildVersion.
	// Always wrapped by *VersionError, which carries the version we saw.
	ErrVersionUnsupported = errors.New("veeam: unsupported Veeam version")

	// ErrUnreachable indicates the transport never got an HTTP response —
	// DNS, dial, TLS handshake, fingerprint mismatch or timeout.
	ErrUnreachable = errors.New("veeam: server unreachable")

	// ErrPlatformUnsupported is the 400 VBR returns from endpoints that
	// refuse Proxmox objects, e.g. GET /jobs/{id} answering "Specify job of
	// supported platform type." Phase 1 does not call one, but the mapping
	// lives here so later phases cannot mistake it for a generic 400.
	ErrPlatformUnsupported = errors.New("veeam: endpoint does not support this platform type")

	// ErrRevisionUnknown indicates no supported x-api-version could be
	// negotiated — neither /swagger/index.js nor the descending probe found
	// a revision this client understands.
	ErrRevisionUnknown = errors.New("veeam: no supported API revision")

	// ErrInvalidInput indicates the caller supplied an argument the client
	// refused to send. It never reaches the network.
	ErrInvalidInput = errors.New("veeam: invalid input")

	// ErrSessionNotFound indicates GET /sessions/{id} ITSELF answered 404 —
	// the run is gone from VBR.
	//
	// A sentinel rather than a status code the caller reads off an *APIError,
	// because a 404 can reach the caller from somewhere else entirely: a
	// failed token grant surfaces the token endpoint's own *APIError verbatim
	// (see requestToken), so a restarting VBR answering 404 on /oauth2/token
	// is byte-identical to this at the *APIError level. The collector DELETES
	// the stored run on this sentinel, so the distinction is the difference
	// between converging one row and destroying a server's in-flight history.
	ErrSessionNotFound = errors.New("veeam: session not found")
)

// APIError carries Veeam's structured error body. VBR shapes these
// consistently across every endpoint — errorCode / message / title / status —
// which is what makes matching on ErrorCode safe rather than string-sniffing
// the message.
type APIError struct {
	StatusCode int
	ErrorCode  string
	Message    string
}

func (e *APIError) Error() string {
	if e.ErrorCode != "" {
		return fmt.Sprintf("veeam API error %d (%s): %s", e.StatusCode, e.ErrorCode, e.Message)
	}
	return fmt.Sprintf("veeam API error %d: %s", e.StatusCode, e.Message)
}

// VersionError reports a server below the supported minimum.
//
// 13.1 is the floor because 13.0.x reports Proxmox jobs with type "Unknown"
// and no lastRun, which makes every downstream feature — job state, RPO,
// coverage — silently wrong rather than absent. Refusing is the honest answer.
type VersionError struct {
	Got     string
	Minimum string
}

func (e *VersionError) Error() string {
	got := e.Got
	if got == "" {
		got = "unknown"
	}
	return fmt.Sprintf("veeam: server version %s is not supported (minimum %s)", got, e.Minimum)
}

func (e *VersionError) Unwrap() error { return ErrVersionUnsupported }

// authFailure builds the 401 error, attaching the domain-qualifier hint when
// the username contains a backslash.
//
// The spike lost half an hour to this: a shell ate the backslash in
// `ad\jdoe`, and VBR answered the resulting `adjdoe` with exactly the same
// AccessDenied body a wrong password produces. Veeam gives us nothing to
// distinguish the two, so the client says so rather than letting the operator
// re-type a password that was never wrong.
func authFailure(username string, apiErr *APIError) error {
	msg := "veeam: authentication failed"
	if apiErr != nil && apiErr.Message != "" {
		msg = "veeam: " + apiErr.Message
	}
	if strings.Contains(username, `\`) {
		msg += ` (the username is domain-qualified — confirm it reached the server as DOMAIN\user with the backslash intact)`
	}
	return fmt.Errorf("%s: %w", msg, ErrAuthFailed)
}

// IsPlatformUnsupported reports whether err is VBR's "Specify job of supported
// platform type." rejection. Matched on the message because VBR returns it as
// a bare 400 with errorCode "UnknownError", so the status and code carry no
// signal.
func IsPlatformUnsupported(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return strings.Contains(strings.ToLower(apiErr.Message), "supported platform type")
}
