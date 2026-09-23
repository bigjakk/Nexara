package api

import (
	"errors"

	"github.com/gofiber/fiber/v3"
)

// ErrorResponse is the standard error envelope returned by all API endpoints.
type ErrorResponse struct {
	Error   string      `json:"error"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

// errorHandler is the custom Fiber error handler that returns JSON error envelopes.
func errorHandler(c fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	message := "Internal Server Error"

	var e *fiber.Error
	if errors.As(err, &e) {
		code = e.Code
		message = e.Message
	}

	return c.Status(code).JSON(ErrorResponse{
		Error:   statusText(code),
		Message: message,
	})
}

// statusText is the envelope's error slug for a status: its reason phrase in
// snake_case — RFC 9110's, or RFC 6585's for 429 and 431 — so a caller can
// switch on it instead of parsing the message.
//
// A status with no case here goes out as internal_server_error — as every 502
// did, an unreachable Proxmox's included, before 502 had one. So every status
// that can reach errorHandler needs a case: the ones Nexara's own code sends,
// which TestGuard_EveryErrorStatusTheAPISendsHasASlug finds in the source, and
// the ones Fiber sends through errorHandler on its own — its router's 404 and
// 405, and the 431 and 501 its serverErrorHandler makes of a request head
// fasthttp cannot read.
//
// Two slugs keep the phrase from before RFC 9110 renamed their status — 413
// is "Content Too Large" there and 422 "Unprocessable Content" — because
// clients already switch on these.
func statusText(code int) string {
	switch code {
	case 400:
		return "bad_request"
	case 401:
		return "unauthorized"
	case 403:
		return "forbidden"
	case 404:
		return "not_found"
	case 405:
		return "method_not_allowed"
	case 409:
		return "conflict"
	case 412:
		return "precondition_failed"
	case 413:
		return "request_entity_too_large"
	case 415:
		return "unsupported_media_type"
	case 422:
		return "unprocessable_entity"
	case 426:
		return "upgrade_required"
	case 429:
		return "too_many_requests"
	case 431:
		return "request_header_fields_too_large"
	case 501:
		return "not_implemented"
	case 502:
		return "bad_gateway"
	case 503:
		return "service_unavailable"
	default:
		return "internal_server_error"
	}
}
