package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// registerVersionEndpoint declares the build-version probe.
//
// It is the only registry endpoint whose Handler is a *Server* method rather
// than a handlers-package one, because what it reports are package api's own
// ldflags variables (Version, Commit, BuildTime). That costs it the call-graph
// half of the RBAC guards — routeHandlerKey can only resolve a name under
// /handlers. — which is harmless here and only here: Public installs no
// middleware by design and is checked instead by
// TestGuard_PublicRoutesAreExpected, through registryPublicRouteKeys. Any other
// shape on a Server-method handler WOULD be a hole, and
// registryEnforcementGaps' first branch reports exactly that.
//
// The Public reason is carried across VERBATIM from publicRoutes in
// rbac_route_guard_test.go; TestVersionAndChangelogReasonsMatchTheReviewedList
// pins that the two stay one sentence.
//
// /healthz is the Server's other legacy route and CANNOT be declared: Register
// refuses any path outside /api/v1/ (pathPrefix), and the container health
// check has to answer on a path that is not part of the API surface. It stays
// in router.go, and TestHealthzIsStillLegacy records that as a decision.
func registerVersionEndpoint(reg *Registry, s *Server) {
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   pathPrefix + "version",
		Description: "Report the running build: the version string git describe produced at build time, " +
			"the commit, the build timestamp and the Go toolchain. Nothing about the install or its " +
			"clusters is in the response.",
		Group:       "Settings",
		Permissions: Permissions{Public: "build version; the SPA reads it before login to render the shell"},
		Parameters:  apischema.Properties{},
		Handler:     s.handleVersion,
	})
}
