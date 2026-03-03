package proxmox

import (
	"errors"
	"fmt"
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
