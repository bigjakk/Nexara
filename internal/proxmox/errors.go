package proxmox

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrNotFound indicates the requested resource does not exist (HTTP 404).
	ErrNotFound = errors.New("resource not found")

	// ErrForbidden indicates insufficient permissions (HTTP 401/403).
	ErrForbidden = errors.New("forbidden")

	// ErrConnectionFailed indicates the Proxmox host is unreachable.
	ErrConnectionFailed = errors.New("connection failed")

	// ErrInvalidResponse indicates the API returned an unparseable response.
	ErrInvalidResponse = errors.New("invalid response")

	// ErrInvalidInput indicates the caller supplied an argument the client
	// refused to send — a malformed identifier that would corrupt the request
	// path, say. It never reaches the network, so handlers should surface it as
	// a client error rather than as a Proxmox failure.
	ErrInvalidInput = errors.New("invalid input")
)

// APIError represents a non-sentinel HTTP error from the Proxmox API.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("proxmox API error %d: %s", e.StatusCode, e.Message)
}

// IsGroupsMigratedError reports whether err is the Proxmox VE 9.x response
// indicating HA groups have been migrated to (and superseded by) HA rules — at
// which point the /cluster/ha/groups write endpoints are soft-disabled and
// return an error containing "migrated to rules".
func IsGroupsMigratedError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "migrated to rules")
}

// IsGuestNotRunningError reports whether err is the Proxmox response for a
// console-proxy call (vncproxy/termproxy) against a guest that is not running,
// e.g. "VM 105 not running" or "CT 105 not running".
func IsGuestNotRunningError(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && strings.Contains(apiErr.Message, "not running")
}

// IsLockedError reports whether err is a Proxmox "VM/CT is locked (…)"
// response — returned when an operation (e.g. migrate) is attempted on a guest
// that is already locked by another in-flight task such as an HA-managed
// migration that is still settling. These are transient: the lock clears once
// the holding task finishes, so the operation is safe to retry after waiting.
func IsLockedError(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && strings.Contains(apiErr.Message, "is locked")
}

// ErrBootstrapAuthFailed indicates PVE rejected the username/password pair
// supplied during cluster onboarding.
//
// Distinct from ErrForbidden, which a caller reads as "the stored API token
// lacks a privilege". Here there is no stored token yet and the operator typed
// the credential moments ago, so the actionable message is a different one.
var ErrBootstrapAuthFailed = errors.New("bootstrap authentication failed")

// TFARequiredError reports that the account used for onboarding has two-factor
// authentication enabled and PVE answered with a challenge instead of a ticket.
//
// Recoverable: the operator retries with a one-time code, which rides the same
// /access/ticket request as the password.
type TFARequiredError struct {
	Username string
}

func (e *TFARequiredError) Error() string {
	return fmt.Sprintf("proxmox: %s requires a two-factor code", e.Username)
}

// TokenExistsError reports that the API token onboarding wanted to mint is
// already present on the cluster.
//
// It is a typed error rather than a retry-with-another-name because
// auto-suffixing is precisely the wrong response: each attempt would leave
// behind another live privsep=0 Administrator credential whose secret nobody
// holds. The operator picks a different name, or removes the old token.
type TokenExistsError struct {
	UserID    string
	TokenName string
}

func (e *TokenExistsError) Error() string {
	return fmt.Sprintf("proxmox: API token %s!%s already exists", e.UserID, e.TokenName)
}

// IsAlreadyExistsError reports whether err is Proxmox's "this object is
// already there" response.
//
// Matched on the message because PVE signals it with a plain 500 and a die()
// string rather than a distinguishing status code — "user 'x@pve' already
// exists", "token with same name already exists". The match is deliberately
// loose (substring, case-insensitive) to cover both the singular and plural
// phrasings PVE uses across endpoints and versions.
func IsAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "already exist")
}
