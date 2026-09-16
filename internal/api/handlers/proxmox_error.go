package handlers

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// mapProxmoxError converts a Proxmox client error to an appropriate Fiber error.
func mapProxmoxError(err error) error {
	// The client refused to send this — it never reached Proxmox, so reporting
	// it as a Proxmox failure (500) would misdirect the operator. The message is
	// safe to surface: it describes their own input.
	if errors.Is(err, proxmox.ErrInvalidInput) {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if errors.Is(err, proxmox.ErrNotFound) {
		return fiber.NewError(fiber.StatusNotFound, "Resource not found on Proxmox")
	}
	if errors.Is(err, proxmox.ErrForbidden) {
		if unwrapped := err.Error(); unwrapped != "" && unwrapped != "forbidden" {
			return fiber.NewError(fiber.StatusForbidden, "Proxmox API: "+unwrapped)
		}
		return fiber.NewError(fiber.StatusForbidden, "Proxmox API permission denied")
	}
	if errors.Is(err, proxmox.ErrConnectionFailed) {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to connect to Proxmox")
	}
	var apiErr *proxmox.APIError
	if errors.As(err, &apiErr) {
		// Proxmox rejected the operator's own parameters, so this is a client
		// error, not a gateway one. checkStatus keeps the rejection map on the
		// error and has already flattened it into Message in a stable order.
		if apiErr.IsParameterRejection() {
			return fiber.NewError(fiber.StatusBadRequest, apiErr.Message)
		}
		// Everything else arrives as Proxmox's JSON envelope with the human
		// sentence in `message` — "binary not installed: /usr/bin/ceph-mon\n"
		// and the like. It is surfaced to the operator verbatim (see
		// describeError in ClusterCephTab.tsx), so hand over the sentence
		// rather than the envelope around it.
		var pxResp struct {
			Message string `json:"message"`
		}
		if jsonErr := json.Unmarshal([]byte(apiErr.Message), &pxResp); jsonErr == nil {
			if msg := strings.TrimSpace(pxResp.Message); msg != "" {
				return fiber.NewError(fiber.StatusBadGateway, msg)
			}
		}
		// Not JSON, or JSON carrying nothing to say: the raw text is still the
		// most informative thing available.
		return fiber.NewError(fiber.StatusBadGateway, apiErr.Message)
	}
	return fiber.NewError(fiber.StatusInternalServerError, "Proxmox operation failed: "+err.Error())
}

// mapNamedOpError keeps the operation in the message.
//
// mapProxmoxError has two kinds of branch. ErrForbidden, ErrInvalidInput and
// the unknown-error tail build their message from err.Error(), so they already
// carry the client's wrap and with it the operation. The rest — not-found,
// connection-failed, and any APIError — answer with either a static sentence or
// Proxmox's own words, neither of which says which call was being made.
//
// Only the second kind gets a prefix. Deciding that from the sentinel rather
// than by looking for the operation in the text matters: the handler's wording
// and the client's fmt.Errorf wording are written in different files and do not
// have to agree ("shut down node" vs "shutdown node %s"), so a text match
// silently stutters wherever they drift apart.
//
// It earns its keep on the calls with no other feedback. These handlers pass no
// onError, so the global MutationCache handler toasts the message verbatim, and
// on a node shutdown or a network apply the connection dropping is exactly when
// "which operation was that?" matters most — the request may well have been
// delivered and the work started before the link went.
func mapNamedOpError(op string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *proxmox.APIError
	contextless := errors.Is(err, proxmox.ErrNotFound) ||
		errors.Is(err, proxmox.ErrConnectionFailed) ||
		errors.As(err, &apiErr)
	if !contextless {
		return mapProxmoxError(err)
	}
	mapped := mapProxmoxError(err)
	var fe *fiber.Error
	if !errors.As(mapped, &fe) {
		return mapped
	}
	return fiber.NewError(fe.Code, op+": "+fe.Message)
}

// mapDuplicateNameError answers a duplicate object with 409 rather than the
// gateway failure mapProxmoxError would otherwise report. PVE reports it as a plain 500 die() string on some
// endpoints and a 400 param rejection on others; IsAlreadyExistsError matches
// the text either way, and 409 is the more precise answer to both. Without it
// the second operator to pick a name is told the cluster is unreachable.
//
// Note it matches "already exists" only: PVE's HA rules say "already defined"
// instead, so wiring this to CreateHARule would be silently dead code.
func mapDuplicateNameError(conflict string, err error) error {
	if err == nil {
		return nil
	}
	if proxmox.IsAlreadyExistsError(err) {
		return fiber.NewError(fiber.StatusConflict, conflict)
	}
	return mapProxmoxError(err)
}

// mapMissingObjectError answers PVE's "the object you named is not there" die
// with 404 rather than the gateway failure mapProxmoxError would otherwise
// report. Same reasoning as mapDuplicateNameError, one status along: PVE
// signals these with a plain 500 and a die() string carrying no rejection map,
// so the shared mapping can only call them a gateway failure. They are not
// one. Two operators with the same list open produce them routinely, and the
// answer is "your view is stale", which is what 404 says.
//
// The phrases are supplied by the caller rather than kept in one shared list
// here, because how loose a phrase may safely be depends entirely on which
// endpoint's errors reach it. MetricServer.pm's update sub dies both
// "no such server '$id'" (the object is gone) and "no such option '$k'" (the
// operator sent a bad delete=), so a predicate of "no such " would answer 404
// to a parameter mistake. Keeping the evidence next to each call site also
// keeps it re-checkable against PVE source, which is the only thing that makes
// these matches trustworthy — see the "already defined" note on
// mapDuplicateNameError for what a phrase that has quietly stopped matching
// costs.
//
// See mapProxmoxDieError for the phrase contract this shares.
func mapMissingObjectError(missing string, phrases []string, err error) error {
	return mapProxmoxDieError(fiber.StatusNotFound, missing, phrases, err)
}

// mapProxmoxDieError scans Proxmox's own die() sentence for a caller-supplied
// phrase and answers the status that phrase justifies. It is the shared body of
// mapMissingObjectError (404, "the object is not there") and of the 409 mappers
// like mapNodeConfigError in acme.go ("the config moved under you"); the
// precedence reasoning below is subtle enough to be worth having once rather
// than once per status.
//
// Phrases must be lowercase, and are matched against Proxmox's own message —
// the JSON envelope included, since that is where the die() sentence sits. The
// contract lives here, on the function that enforces it, because a caller can
// reach this directly without going through the 404 wrapper.
func mapProxmoxDieError(status int, message string, phrases []string, err error) error {
	if err == nil {
		return nil
	}
	// Only Proxmox's own words are worth scanning, and only when nothing better
	// has already been established. Deciding that from the mapped result rather
	// than re-testing the sentinels keeps a single copy of mapProxmoxError's
	// precedence, the way mapNamedOpError does: 502 is reached by exactly two
	// branches, so ruling out the connection failure leaves the APIError one —
	// the plain 500 die() these phrases describe.
	//
	// It matters because these phrases are looser than mapFirewallRuleError's
	// was. ErrForbidden's message is the raw 403 body, and PVE has 403s that
	// read "pool 'x' does not exist" (pve-access-control RPCEnvironment.pm);
	// ErrInvalidInput never reached Proxmox at all; a 400's message is the
	// flattened field map. Scanning first would let a phrase overwrite any of
	// those with a 404, turning a permission problem into "the list may be out
	// of date" — and the field map would be dropped with the 400.
	//
	// What is left is any APIError checkStatus did not tag as a parameter
	// rejection. That is wider than the plain 500 die() these phrases were read
	// off — a 409 or a 503 lands here too — but every one of them is Proxmox
	// answering in its own words, which is the only text worth scanning.
	mapped := mapProxmoxError(err)
	var fe *fiber.Error
	var apiErr *proxmox.APIError
	if !errors.As(mapped, &fe) || fe.Code != fiber.StatusBadGateway ||
		errors.Is(err, proxmox.ErrConnectionFailed) || !errors.As(err, &apiErr) {
		return mapped
	}
	// Proxmox's own words only. err.Error() would also carry the client's wrap,
	// which interpolates the caller's id — "get metric server %s" in
	// client_admin.go, "update ha rule %s" in client_ha.go — and that id comes
	// off the request path. PVE types both as pve-configid so an id spelling out
	// a phrase comes back as a rejection the gate above has already returned on,
	// but there is no reason to let operator-supplied text into the match.
	msg := strings.ToLower(apiErr.Message)
	for _, phrase := range phrases {
		if strings.Contains(msg, phrase) {
			return fiber.NewError(status, message)
		}
	}
	return mapped
}
