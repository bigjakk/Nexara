package handlers

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/changelog"
)

// ChangelogHandler serves release notes parsed from GitHub Releases.
type ChangelogHandler struct {
	svc *changelog.Service
}

// NewChangelogHandler creates a new changelog handler. svc may be nil — the
// handler will then return an empty list rather than failing.
func NewChangelogHandler(svc *changelog.Service) *ChangelogHandler {
	return &ChangelogHandler{svc: svc}
}

// Get returns the cached changelog entries.
//
// Always returns 200 with a (possibly empty) entries array — the popup is
// expected to gracefully no-op on empty data, so callers don't need to
// distinguish "no releases yet" from "GitHub is unreachable".
func (h *ChangelogHandler) Get(c fiber.Ctx) error {
	if h.svc == nil {
		return c.JSON(fiber.Map{"entries": []changelog.Entry{}})
	}
	entries, err := h.svc.Get(c.Context())
	if err != nil {
		return c.JSON(fiber.Map{"entries": []changelog.Entry{}})
	}
	if entries == nil {
		entries = []changelog.Entry{}
	}
	return c.JSON(fiber.Map{"entries": entries})
}
