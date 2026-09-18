package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// registerChangelogEndpoints declares ChangelogHandler's single route.
//
// The Public reason is carried across VERBATIM from publicRoutes in
// rbac_route_guard_test.go — registryPublicRouteKeys folds this declaration
// into that reviewed list, and TestAuthExemptionReasonsMatchTheReviewedLists
// pins that the two say the same thing rather than drifting into two
// justifications for one exemption.
//
// Public rather than authenticated, and that is the route's whole shape: the
// changelog popup is fetched by the SPA shell, which renders before a session
// exists, and the payload is the project's own published release notes — the
// same bytes GitHub serves anonymously. Nothing about the install is in it.
func registerChangelogEndpoints(reg *Registry, h *handlers.ChangelogHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   pathPrefix + "changelog",
		Description: "Return the cached release notes parsed from this project's public GitHub releases " +
			"feed. Always answers 200 with a possibly-empty list: the popup no-ops on empty data, so a " +
			"caller never has to tell \"no releases yet\" from \"GitHub is unreachable\".",
		Group:       "Settings",
		Permissions: Permissions{Public: "release notes proxied from the public GitHub releases feed"},
		Parameters:  apischema.Properties{},
		Handler:     h.Get,
	})
}
