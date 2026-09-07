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
