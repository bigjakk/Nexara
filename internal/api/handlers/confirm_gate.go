package handlers

import (
	"errors"

	"github.com/gofiber/fiber/v3"
)

// A confirm gate refuses an action the caller can still choose to take, by
// re-submitting with an acknowledgement flag. The project prefers this to a
// hard block wherever the risky option has a legitimate self-hosted use: a lab
// directory with no CA, a homelab on a private address, a cluster genuinely
// moving to new machines.
//
// THE SHAPE MATTERS. A gate must RETURN this error and let its caller render
// it. Writing the response inside the gate and returning nil does not refuse
// anything — fiber's c.Status(...).JSON(...) returns nil on success, so the
// caller's `if err != nil` stays false and the handler runs on and performs the
// very action the gate just "refused", with the 422 status making the response
// look like a refusal while carrying the result of the thing it did. That bug
// shipped in the first draft of the LDAP transport gate and was reproduced in
// the SSH one within the hour, which is why it has a type and a guard.
//
// The convention that prevents it: a gate takes a context.Context, never a
// fiber.Ctx, so it has nothing to write a response WITH.
//
// That is a convention, not a compiler-enforced invariant, and nothing checks
// it directly — requirePerm, requireClusterPerm and their siblings legitimately
// take a fiber.Ctx, so "no fiber.Ctx in a require*" is not a rule a guard can
// state. TestGuard_RequireGatesDoNotWriteResponses is a backstop rather than
// the whole defence: it catches a gate that calls a response method by name,
// which is how this bug was actually written both times, but a gate that
// delegated the write elsewhere would still slip past it.

// confirmRequiredError is a refusal the caller can override by acknowledging.
type confirmRequiredError struct {
	// Code is the stable identifier the frontend keys on to swap its error
	// banner for a confirm prompt.
	Code    string
	Message string
	// AcknowledgeField names the request field that overrides this refusal. A
	// UI already knows which flag to resend, but a direct API caller has only
	// the response to go on — and Message deliberately does not spell it out,
	// because every consumer appends its own call to action and two of them
	// read as a stutter ("Confirm to proceed. … confirm to continue").
	AcknowledgeField string

	// Fields carries whatever the prompt needs to word itself, and is rendered
	// under the "details" JSON key. Never put a secret here: it goes into a
	// response body, and gates like these are reached by callers who have not
	// proven possession of anything.
	//
	// Named Fields, not Details, so it does not collide with an audit row's
	// Details — TestGuard_AuditReadPathsRedact keys on that name to catch a
	// read path that forgets to redact, and a second meaning for it would cost
	// that guard its precision.
	Fields map[string]any
}

func (e *confirmRequiredError) Error() string { return e.Message }

// renderConfirmRequired turns the refusal into the structured 422 the frontend
// recognizes, in the same shape as renderAddressPolicyError. Any other error is
// passed through untouched, so a call site can render unconditionally.
func renderConfirmRequired(c fiber.Ctx, err error) error {
	var confirmErr *confirmRequiredError
	if !errors.As(err, &confirmErr) {
		return err
	}

	details := fiber.Map{}
	for k, v := range confirmErr.Fields {
		details[k] = v
	}

	if confirmErr.AcknowledgeField != "" {
		details["acknowledge_field"] = confirmErr.AcknowledgeField
	}

	return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
		"error":   confirmErr.Code,
		"message": confirmErr.Message,
		"details": details,
	})
}
