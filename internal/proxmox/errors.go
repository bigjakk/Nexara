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
