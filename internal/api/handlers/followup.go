package handlers

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v3"
)

// followUpBound is d, or authFollowUpTimeout when d is zero: the deadline of one
// follow-up for a handler that keeps its bound in a field so that a test can
// shorten it (UserHandler and TOTPHandler; AuthHandler has its own).
func followUpBound(d time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return authFollowUpTimeout
}

// followUpContext returns the context for one follow-up of a decision that has
// already been made — the audit row, the RBAC cache, a Redis cleanup: a deadline of
// its own, d, from a fresh start, on a context detached from the deciding work's,
// which may have spent every second of its budget and must not take the record or
// the cleanup down with it. The returned cancel must be called.
func followUpContext(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), d)
}

// runFollowUp runs fn — a call that reads c.Context() itself, such as AuditLogAs
// with the event it publishes — under a follow-up deadline of d, and puts the
// request's own context back afterwards. A follow-up that waited for ever would hold
// the response of a decision that is already made.
//
// The user and TOTP handlers use it; AuthHandler has its own withFollowUp, with the
// same body, whose duration a test can shorten through the handler's field.
func runFollowUp(c fiber.Ctx, d time.Duration, fn func()) {
	prev := c.Context()
	ctx, cancel := followUpContext(prev, d)
	c.SetContext(ctx)
	defer func() {
		cancel()
		c.SetContext(prev)
	}()
	fn()
}
