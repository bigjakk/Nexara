package api

import (
	"encoding/json"
	"errors"

	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"
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
// 405, the 431 and 501 its serverErrorHandler makes of a request head fasthttp
// cannot read, and the 408 it makes of one the read deadline cut short
// (buildFiberConfig's ReadTimeout).
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
	case 408:
		return "request_timeout"
	case 409:
		return "conflict"
	case 411:
		return "length_required"
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

// redactServerErrorBodies makes the answer to every request fasthttp cannot
// read carry nothing of that request: its body is always the error envelope
// with the status's slug and the status's standard reason phrase.
//
// fasthttp answers a request it cannot read — a head it cannot parse, one that
// does not arrive within ReadTimeout, a Host it cannot parse — through
// Server.ErrorHandler, whose one caller is Server.writeErrorResponse
// (writeFastError, the one other way it answers on its own, writes fixed
// text). Fiber installs App.serverErrorHandler there, which turns the error
// into a fiber.Error — classifying it by errors.As and errors.Is and by two
// strings in its text, "unsupported http request method" and "timeout" — runs
// the Use middleware, and has errorHandler render it, the error's text as the
// envelope's message. That text is built from the request. With
// SecureErrorLogMessage set, fasthttp leaves out the snippet of the buffered
// request it otherwise appends (headerErrorMsg), but not the bytes some errors
// quote themselves: RequestHeader.parseFirstLine quotes the whole buffered
// head — Cookie and Authorization included — for a request line with no HTTP
// version, and the request URI with its query for one it will not accept;
// parseHeaders quotes an invalid header value; the header scanner
// (headerScanner.next, readContinuedLineSlice) quotes the whole line for a
// header name with an invalid byte or a line with no colon; and parseHost,
// under URI.parse, quotes a Host it cannot parse.
//
// So this wraps Fiber's handler rather than replacing it or rewriting the
// error on the way in: Fiber's handler still classifies the error by its own
// text — rewritten first, a 501 or a 408 would become a 400 — still runs the
// middleware, and still sets the status and the headers. Then the body is
// replaced with {error: statusText(code), message: fasthttp.StatusMessage(code)},
// marked JSON — whatever the handler rendered, errorHandler's envelope or
// Fiber's own plain text — and stripped of any Content-Encoding the middleware
// left, which would describe a body no longer there. Nothing here logs the
// error. New installs it on the server fiber.New makes, right after
// closeConnectionsLeftMidBody.
func redactServerErrorBodies(app *fiber.App) {
	srv := app.Server()
	fiberErrorHandler := srv.ErrorHandler
	srv.ErrorHandler = func(ctx *fasthttp.RequestCtx, err error) {
		fiberErrorHandler(ctx, err)
		code := ctx.Response.StatusCode()
		// Marshal cannot fail on an envelope of two strings.
		body, _ := json.Marshal(ErrorResponse{Error: statusText(code), Message: fasthttp.StatusMessage(code)})
		ctx.Response.Header.Del(fiber.HeaderContentEncoding)
		ctx.Response.Header.SetContentType(fiber.MIMEApplicationJSONCharsetUTF8)
		ctx.Response.SetBody(body)
	}
}
