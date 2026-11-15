package api

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestAnyAuthenticatedRoute_RefusesASessionThatHasEnded: an access token outlives its
// session, and on a route that is NOT InteractiveOnly — a plain read — it is refused at
// its next call once the session has ended (401, which the SPA's one refresh turns
// into a sign-out), and a lookup that cannot answer is a 503, never a yes. An API key
// is not looked up a second time; one whose owner is inactive is refused by
// authenticateAPIKey's own read. Through the real authRequired and mountRegistry.
func TestAnyAuthenticatedRoute_RefusesASessionThatHasEnded(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		edit     func(f *chainDB, sid uuid.UUID)
		byKey    bool
		status   int
		message  string
		liveRead bool // IsConsoleCredentialLive was asked
	}{
		{"a live session", nil, false, http.StatusNoContent, "", true},
		{"a session that has ended", func(f *chainDB, sid uuid.UUID) { f.ended = map[uuid.UUID]bool{sid: true} },
			false, http.StatusUnauthorized, "Session has ended", true},
		{"a session that cannot be checked", func(f *chainDB, _ uuid.UUID) { f.liveErr = context.DeadlineExceeded },
			false, http.StatusServiceUnavailable, "Service temporarily unavailable", true},
		{"an API key", nil, true, http.StatusNoContent, "", false},
		{"an API key whose owner is inactive", func(f *chainDB, _ uuid.UUID) { f.key.UserIsActive = false },
			true, http.StatusUnauthorized, "User account is inactive", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake, uid, apiKey := newChainDB(t)
			jwtSvc := auth.NewJWTService("live-session-test-secret", 15*time.Minute, 7*24*time.Hour)
			srv := &Server{queries: db.New(fake), jwtService: jwtSvc}
			sid := uuid.New()
			token, _, err := jwtSvc.GenerateAccessToken(uid, chainEmail, "admin", sid)
			if err != nil {
				t.Fatalf("access token: %v", err)
			}
			if tc.edit != nil {
				tc.edit(fake, sid)
			}
			reached := false
			reg := NewRegistry()
			reg.Register(Endpoint{
				Method: fiber.MethodGet, Path: "/api/v1/probe/read",
				Description: "A plain read.", Group: "Test", Parameters: apischema.Properties{},
				Permissions: Permissions{SelfService: "test: authorization is not what is under test"},
				Handler: func(c fiber.Ctx, _ *apischema.Params) error {
					reached = true
					return c.SendStatus(fiber.StatusNoContent)
				},
			})
			app := fiber.New(fiber.Config{ErrorHandler: errorHandler})
			mountRegistry(app, reg, srv.authRequired(), everyNodeIsAMember(), srv.queries)

			bearer := token
			if tc.byKey {
				bearer = apiKey
			}
			req := authedRequest(fiber.MethodGet, "/api/v1/probe/read")
			req.Header.Set(fiber.HeaderAuthorization, "Bearer "+bearer)
			status, env := send(t, app, req)

			if status != tc.status || env.Message != tc.message {
				t.Errorf("status = %d, message = %q; want %d %q", status, env.Message, tc.status, tc.message)
			}
			if reached != (tc.status == http.StatusNoContent) {
				t.Errorf("handler reached = %t with status %d", reached, status)
			}
			if asked := slices.Contains(fake.statements(), "IsConsoleCredentialLive"); asked != tc.liveRead {
				t.Errorf("IsConsoleCredentialLive asked = %t, want %t (statements %v)", asked, tc.liveRead, fake.statements())
			}
		})
	}
}

// TestGuard_EveryAuthenticatedRouteChecksItsSessionIsLive walks every mounted route,
// declared and legacy (a legacy group's middleware is a route of its own): wherever the real
// authRequired runs, the link right after it asks whether the session is live —
// liveCredentialGate, or the InteractiveOnly gate, which asks it itself (and alone may
// skip it, on the owner's remedies). authOptional is followed by liveIfAuthenticated,
// but on sign-out, which a session that has ended must still be able to do.
func TestGuard_EveryAuthenticatedRouteChecksItsSessionIsLive(t *testing.T) {
	t.Parallel()
	s := sharedRouteStub(t)
	var gated, legacy int
	for _, r := range s.app.GetRoutes(false) {
		key := r.Method + " " + normalizeRoutePath(r.Path)
		for i, h := range r.Handlers {
			name := handlerName(h)
			var next string
			if i+1 < len(r.Handlers) {
				next = handlerName(r.Handlers[i+1])
			}
			switch {
			case strings.Contains(name, "internal/api.(*Server).authRequired"):
				if !strings.Contains(next, "internal/api.liveCredentialGate") && !strings.Contains(next, "internal/api.interactiveOnlyGate") {
					t.Errorf("%s: authRequired is followed by %q, not by a check that the session is live", key, next)
				}
				gated++
				if !slices.ContainsFunc(r.Handlers, func(h fiber.Handler) bool {
					return strings.Contains(handlerName(h), "internal/api.Endpoint.serve")
				}) {
					legacy++ // mounted in router.go, not by mountRegistry
				}
			case strings.Contains(name, "internal/api.(*Server).authOptional") && key != "POST /api/v1/auth/logout":
				if !strings.Contains(next, "internal/api.liveIfAuthenticated") {
					t.Errorf("%s: authOptional is followed by %q, not by liveIfAuthenticated", key, next)
				}
			}
		}
	}
	if gated < 100 || legacy == 0 {
		t.Fatalf("saw authRequired on %d routes (%d legacy groups): this guard is not reading the route table", gated, legacy)
	}
}

// TestLiveIfAuthenticated: POST /auth/register runs behind authOptional, so a request
// that names no caller is the anonymous first-user sign-up and goes through, while an
// admin's access token whose session has ended must not create accounts.
func TestLiveIfAuthenticated(t *testing.T) {
	t.Parallel()
	ended := uuid.New()
	live := endedSessions{ended: ended}
	for _, tc := range []struct {
		name    string
		session uuid.UUID // uuid.Nil: nobody authenticated
		want    int
	}{
		{"anonymous", uuid.Nil, http.StatusNoContent},
		{"a live session", uuid.New(), http.StatusNoContent},
		{"a session that has ended", ended, http.StatusUnauthorized},
	} {
		app := fiber.New(fiber.Config{ErrorHandler: errorHandler})
		app.Post("/register", func(c fiber.Ctx) error {
			if tc.session != uuid.Nil {
				c.Locals("user_id", uuid.MustParse(testUserID))
				c.Locals(handlers.LocalsAuthMethod, handlers.AuthMethodSession)
				c.Locals(handlers.LocalsSessionID, tc.session)
			}
			return c.Next()
		}, liveIfAuthenticated(live), func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })
		if status, env := send(t, app, authedRequest(fiber.MethodPost, "/register")); status != tc.want {
			t.Errorf("%s: status = %d (%q), want %d", tc.name, status, env.Message, tc.want)
		}
	}
}

// endedSessions answers that one session has ended and every other is live.
type endedSessions struct{ ended uuid.UUID }

func (e endedSessions) IsConsoleCredentialLive(_ context.Context, arg db.IsConsoleCredentialLiveParams) (bool, error) {
	return arg.SessionID.Valid && uuid.UUID(arg.SessionID.Bytes) != e.ended, nil
}

// TestAPIKeyLookupIsBounded: anyone can send a bearer that starts nxra_, and the
// /auth/ paths skip the general limiter, so a key lookup that never answers must end
// at its deadline with the usual 401, not hold the request (and a pool connection).
func TestAPIKeyLookupIsBounded(t *testing.T) {
	t.Parallel()
	fake, _, apiKey := newChainDB(t)
	fake.keyStalls = true
	srv := &Server{queries: db.New(fake), jwtService: auth.NewJWTService("key-bound-test-secret", 15*time.Minute, time.Hour),
		apiKeyLookupTimeout: 50 * time.Millisecond}
	app := fiber.New(fiber.Config{ErrorHandler: errorHandler})
	app.Get("/api/v1/auth/me", srv.authRequired(), func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })
	req := authedRequest(fiber.MethodGet, "/api/v1/auth/me")
	req.Header.Set(fiber.HeaderAuthorization, "Bearer "+apiKey)
	status, _, body := sendFull(t, app, req)
	if status != http.StatusUnauthorized || !strings.Contains(string(body), "Invalid or expired API key") {
		t.Errorf("status = %d (%s), want 401 Invalid or expired API key", status, body)
	}
}
