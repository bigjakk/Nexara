package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestLogoutThroughTheRealAuthOptional puts the real authOptional in front of the
// real Logout, which the handler tests cannot: they stand in for the middleware
// with a header. What it adds is the agreement between the two halves of the
// ownership check.
//
// A valid token or API key is turned into user_id by authOptional, and Logout
// compares it with the session's owner. Any other token this server signed — expired,
// not yet valid — is ignored by authOptional, which names a caller only for one that
// validates, so Logout reads the Authorization header itself, and has to read it the
// way the middleware does: both go through auth.BearerToken, and the rows that differ
// only in how the header is spelled are the proof. "bearer" and "BEARER" are a bearer
// to both, so a valid token under either is named by the middleware and refused. A
// second space is read exactly by the shared parser, so authOptional sees a token no
// signature verifies and names no one — and the refusal, which can only refuse and
// reads past stray space, still turns the sign-out away. Another scheme is no token
// to either, and is held to the cookie.
//
// The session is the owner's, found by whatever cookie the request carries; the
// fake database answers the lookup with it and records every statement, so a row
// says whether the session was revoked by whether RevokeSession was sent.
func TestLogoutThroughTheRealAuthOptional(t *testing.T) {
	const secret = "logout-e2e-secret"
	owner, other := uuid.New(), uuid.New()
	svc := auth.NewJWTService(secret, 15*time.Minute, 7*24*time.Hour)
	lapsed := auth.NewJWTService(secret, -time.Hour, 7*24*time.Hour)

	accessToken := func(t *testing.T, s *auth.JWTService, user uuid.UUID) string {
		t.Helper()
		tok, _, err := s.GenerateAccessToken(user, "alice@example.com", "admin")
		if err != nil {
			t.Fatalf("GenerateAccessToken: %v", err)
		}
		return tok
	}
	consoleToken := func(t *testing.T, user uuid.UUID) string {
		t.Helper()
		scope := auth.ConsoleScope{ClusterID: uuid.NewString(), Node: "pve-01", Type: "node_shell"}
		tok, _, err := lapsed.GenerateConsoleToken(user, "alice@example.com", "admin", scope, -time.Hour)
		if err != nil {
			t.Fatalf("GenerateConsoleToken: %v", err)
		}
		return tok
	}

	// notYetValid is a token this server signed whose nbf is an hour away: the
	// middleware's parser refuses it, and its signature still says whom it is for.
	notYetValid := func(t *testing.T, user uuid.UUID) string {
		t.Helper()
		claims := auth.Claims{
			RegisteredClaims: jwt.RegisteredClaims{
				Subject:   user.String(),
				NotBefore: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(2 * time.Hour)),
			},
			UserID: user, Email: "alice@example.com", Role: "admin",
		}
		tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return tok
	}

	const (
		refused   = http.StatusForbidden
		signedOut = http.StatusOK
	)
	tests := []struct {
		name string
		// header is the Authorization header; "" sends none.
		header func(t *testing.T) string
		// key, when set, is an API key of that user the fake database knows.
		keyOf uuid.UUID

		want    int
		revoked bool
		// cleared is whether the answer deletes the refresh cookie.
		cleared bool
	}{
		{
			name:   "a valid token of the owner",
			header: func(t *testing.T) string { return "Bearer " + accessToken(t, svc, owner) },
			want:   signedOut, revoked: true, cleared: true,
		},
		{
			name:   "a valid token of another user",
			header: func(t *testing.T) string { return "Bearer " + accessToken(t, svc, other) },
			want:   refused,
		},
		{
			name:   "a valid token of another user, the scheme in lower case",
			header: func(t *testing.T) string { return "bearer " + accessToken(t, svc, other) },
			want:   refused,
		},
		{
			name:   "a valid token of another user, the scheme in upper case",
			header: func(t *testing.T) string { return "BEARER " + accessToken(t, svc, other) },
			want:   refused,
		},
		{
			name:   "a valid token of another user after two spaces: authOptional turns it away, the refusal reads past the space",
			header: func(t *testing.T) string { return "Bearer  " + accessToken(t, svc, other) },
			want:   refused,
		},
		{
			name:   "a valid token of the owner after two spaces",
			header: func(t *testing.T) string { return "Bearer  " + accessToken(t, svc, owner) },
			want:   signedOut, revoked: true, cleared: true,
		},
		{
			name:   "a valid token of another user under another scheme",
			header: func(t *testing.T) string { return "Basic " + accessToken(t, svc, other) },
			want:   signedOut, revoked: true, cleared: true,
		},
		{
			name:   "a token of another user that is not valid yet, which authOptional turns away",
			header: func(t *testing.T) string { return "Bearer " + notYetValid(t, other) },
			want:   refused,
		},
		{
			name:   "a token of the owner that is not valid yet",
			header: func(t *testing.T) string { return "Bearer " + notYetValid(t, owner) },
			want:   signedOut, revoked: true, cleared: true,
		},
		{
			name:   "an expired token of the owner",
			header: func(t *testing.T) string { return "Bearer " + accessToken(t, lapsed, owner) },
			want:   signedOut, revoked: true, cleared: true,
		},
		{
			name:   "an expired token of another user",
			header: func(t *testing.T) string { return "Bearer " + accessToken(t, lapsed, other) },
			want:   refused,
		},
		{
			name: "no Authorization header",
			want: signedOut, revoked: true, cleared: true,
		},
		{
			name:   "an expired token of another user, the scheme in lower case",
			header: func(t *testing.T) string { return "bearer " + accessToken(t, lapsed, other) },
			want:   refused,
		},
		{
			name:   "an expired token of another user, the scheme in upper case",
			header: func(t *testing.T) string { return "BEARER " + accessToken(t, lapsed, other) },
			want:   refused,
		},
		{
			name:   "an expired token of another user after two spaces",
			header: func(t *testing.T) string { return "Bearer  " + accessToken(t, lapsed, other) },
			want:   refused,
		},
		{
			name:   "an expired token of another user under another scheme",
			header: func(t *testing.T) string { return "Basic " + accessToken(t, lapsed, other) },
			want:   signedOut, revoked: true, cleared: true,
		},
		{
			name:   "an Authorization header that is only the scheme",
			header: func(*testing.T) string { return "Bearer" },
			want:   signedOut, revoked: true, cleared: true,
		},
		{
			name: "an expired token signed with another secret, naming another user",
			header: func(t *testing.T) string {
				tok, _, err := auth.NewJWTService("some-other-secret", -time.Hour, time.Hour).GenerateAccessToken(other, "alice@example.com", "admin")
				if err != nil {
					t.Fatalf("GenerateAccessToken: %v", err)
				}
				return "Bearer " + tok
			},
			want: signedOut, revoked: true, cleared: true,
		},
		{
			name:   "an expired console token of another user",
			header: func(t *testing.T) string { return "Bearer " + consoleToken(t, other) },
			want:   signedOut, revoked: true, cleared: true,
		},
		{
			name:   "an API key that does not authenticate",
			header: func(*testing.T) string { return "Bearer nxra_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" },
			want:   signedOut, revoked: true, cleared: true,
		},
		{
			name:   "an API key that does not authenticate, after two spaces",
			header: func(*testing.T) string { return "Bearer  nxra_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" },
			want:   signedOut, revoked: true, cleared: true,
		},
		{
			name:   "an API key of the owner",
			header: func(*testing.T) string { return "Bearer nxra_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB" },
			keyOf:  owner,
			want:   signedOut, revoked: true, cleared: true,
		},
		{
			name:   "an API key of another user",
			header: func(*testing.T) string { return "Bearer nxra_CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC" },
			keyOf:  other,
			want:   refused,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const cookie = "the-owners-refresh-token"
			rows := map[string]any{
				"GetSessionByTokenHash": db.Session{
					ID: uuid.New(), UserID: owner, TokenHash: auth.HashToken(cookie),
					ExpiresAt: time.Now().Add(24 * time.Hour), LastUsedAt: time.Now(),
					UserRole: "admin", RotatedAt: pgtype.Timestamptz{},
				},
			}
			if tt.keyOf != uuid.Nil {
				rows["GetAPIKeyByHash"] = db.GetAPIKeyByHashRow{
					ID: uuid.New(), UserID: tt.keyOf, UserEmail: "alice@example.com", UserRole: "admin", UserIsActive: true,
				}
			}
			fake := &handlerNodeDB{
				t:    t,
				rows: rows,
				tags: map[string]string{"RevokeSession": "UPDATE 1", "InsertAuditLog": "INSERT 0 1"},
			}
			queries := db.New(fake)

			mr := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })

			s := &Server{jwtService: svc, queries: queries}
			authHandler := handlers.NewAuthHandler(nil, queries, svc, auth.NewSessionManager(queries, rdb), nil, nil)
			app := fiber.New()
			app.Post("/api/v1/auth/logout", s.authOptional(), authHandler.Logout)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: handlers.RefreshCookieName, Value: cookie})
			if tt.header != nil {
				req.Header.Set("Authorization", tt.header(t))
			}
			resp, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second, FailOnTimeout: true})
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			t.Cleanup(func() { _ = resp.Body.Close() })

			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
			fake.mu.Lock()
			sent := slices.Contains(fake.log, "RevokeSession")
			fake.mu.Unlock()
			if sent != tt.revoked {
				t.Errorf("RevokeSession sent = %t, want %t", sent, tt.revoked)
			}
			var cleared bool
			for _, c := range resp.Cookies() {
				if c.Name == handlers.RefreshCookieName {
					cleared = c.Value == "" && (c.MaxAge < 0 || (!c.Expires.IsZero() && c.Expires.Before(time.Now())))
				}
			}
			if cleared != tt.cleared {
				t.Errorf("the refresh cookie is deleted = %t, want %t (Set-Cookie: %v)", cleared, tt.cleared, resp.Header.Values("Set-Cookie"))
			}
		})
	}
}

// TestExtractBearerTokenIsTheSharedParser holds the middleware's reading of the
// Authorization header to auth.BearerToken, the one Logout's ownership check shares:
// the two must see the same token, or one of them decides on a request the other
// never looked at. Each spelling is asked of both and compared, and each is also held
// to the value the shared reading has always given it, so that the two cannot change
// together without this test saying so — a second space is part of the token, which
// is how authentication has always read it.
func TestExtractBearerTokenIsTheSharedParser(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{header: "Bearer abc", want: "abc"},
		{header: "bearer abc", want: "abc"},
		{header: "BEARER abc", want: "abc"},
		{header: "Bearer  abc", want: " abc"},
		{header: "Bearer a b", want: "a b"},
		{header: "Bearer ", want: ""},
		{header: "Bearer", want: ""},
		{header: "Basic abc", want: ""},
		{header: "Bearerabc", want: ""},
		{header: "abc", want: ""},
		{header: "", want: ""},
	}
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error { return c.SendString(extractBearerToken(c)) })

	for _, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			resp, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second, FailOnTimeout: true})
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			t.Cleanup(func() { _ = resp.Body.Close() })
			raw, _ := io.ReadAll(resp.Body)
			got := string(raw)

			if shared := auth.BearerToken(tt.header); got != shared {
				t.Errorf("extractBearerToken(%q) = %q but auth.BearerToken says %q: there are two readings of one header", tt.header, got, shared)
			}
			if got != tt.want {
				t.Errorf("extractBearerToken(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}
