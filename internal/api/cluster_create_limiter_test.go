package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// TestClusterCreateLimiterCannotBeSpelledAround is a regression test for a
// bypass that a path-comparing limiter has by construction.
//
// Fiber routes on a lowercased, slash-trimmed path — CaseSensitive and
// StrictRouting are both left false in buildFiberConfig — so "POST
// /API/v1/clusters//" reaches the same handler as "/api/v1/clusters". An
// app-level limiter whose Next() compares c.Path() against "/api/v1/clusters"
// does NOT match those spellings and steps aside, while the request still runs
// the handler and, in bootstrap mode, still spends a real /access/ticket
// attempt against the operator's hypervisor. The cap that exists to stop Nexara
// being used as a password-spraying proxy was 60x looser than advertised for
// anyone who typed the path differently.
//
// Attaching the limiter to the route removes the class: matching is Fiber's job
// and happens before the middleware runs.
func TestClusterCreateLimiterCannotBeSpelledAround(t *testing.T) {
	const max = 10

	// Every spelling that reaches the same route must draw on the same bucket.
	for _, path := range []string{
		"/api/v1/clusters",
		"/API/v1/clusters",
		"/api/V1/Clusters",
		"/api/v1/clusters/",
		"/api/v1/clusters//",
	} {
		t.Run(path, func(t *testing.T) {
			s := &Server{}
			// Mirrors buildFiberConfig: neither flag is set in production.
			app := fiber.New()
			app.Post("/api/v1/clusters", s.clusterCreateLimiter(), func(c fiber.Ctx) error {
				return c.SendStatus(fiber.StatusOK)
			})

			var got []int
			for i := 0; i < max+2; i++ {
				resp, err := app.Test(httptest.NewRequest(http.MethodPost, path, nil))
				if err != nil {
					t.Fatalf("Test: %v", err)
				}
				got = append(got, resp.StatusCode)
				_ = resp.Body.Close()
			}

			// Exactly two outcomes are acceptable, and neither is a skip: either
			// the spelling does not reach the handler (nothing to bypass), or it
			// does and the cap applies to it. "Reaches the handler, uncapped" is
			// the bug.
			if got[0] == fiber.StatusNotFound {
				for i, code := range got {
					if code != fiber.StatusNotFound {
						t.Fatalf("%s returned 404 then %d on request %d — routing is inconsistent", path, code, i+1)
					}
				}
				return
			}
			if got[max] != fiber.StatusTooManyRequests || got[max+1] != fiber.StatusTooManyRequests {
				t.Errorf("statuses = %v: request %d onward should be 429 — this spelling reaches the handler and escapes the cap",
					got, max+1)
			}
		})
	}
}

// TestClusterCreateLimiterIsRouteScopedNotAppLevel is a static guard over the
// real wiring, because the behavioural version of this assertion is a tautology
// — building a route with auth ahead of the limiter and then observing that the
// limiter sits behind auth proves nothing about setupRoutes.
//
// Two properties matter and neither is visible from a hand-built app:
//
//   - Registered on the ROUTE. An app.Use limiter matching on c.Path() is
//     bypassable by path spelling (see the test above), because Fiber routes on
//     a lowercased, slash-trimmed path.
//   - Therefore it runs after the group's authRequired, so anonymous traffic
//     cannot drain the budget and lock legitimate onboarding out.
func TestClusterCreateLimiterIsRouteScopedNotAppLevel(t *testing.T) {
	fset := token.NewFileSet()

	routes, err := parser.ParseFile(fset, "router.go", nil, 0)
	if err != nil {
		t.Fatalf("parse router.go: %v", err)
	}
	if !mentionsClusterCreateLimiter(routes) {
		t.Error("router.go does not wire clusterCreateLimiter onto the POST /clusters route — " +
			"an app-level limiter is bypassable by path spelling")
	}

	mw, err := parser.ParseFile(fset, "middleware.go", nil, 0)
	if err != nil {
		t.Fatalf("parse middleware.go: %v", err)
	}
	ast.Inspect(mw, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "setupMiddleware" || fn.Body == nil {
			return true
		}
		if mentionsClusterCreateLimiterIn(fn.Body) {
			t.Error("setupMiddleware registers clusterCreateLimiter app-wide; it must be attached " +
				"to the route so path spelling cannot bypass it and so it runs behind authRequired")
		}
		return false
	})
}

func mentionsClusterCreateLimiter(f *ast.File) bool {
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "clusterCreateLimiter" {
			found = true
		}
		return true
	})
	return found
}

func mentionsClusterCreateLimiterIn(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "clusterCreateLimiter" {
			found = true
		}
		return true
	})
	return found
}
