package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/veeam"
)

// newVeeamControlTestApp mounts the job-control routes with a nil queries
// handle.
//
// Safe for the same reason newVeeamTestApp is: every path exercised here
// returns before the first database call. The permission refusal in
// veeamScopeForAction runs BEFORE ListVeeamPlatformsByServer, which is exactly
// the ordering these tests exist to pin — a nil-pointer panic would be the
// failure signal if that ordering ever inverted.
func newVeeamControlTestApp(t *testing.T) *fiber.App {
	t.Helper()

	handler := NewVeeamHandler(nil, testEncryptionKey, nil)

	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Use(func(c fiber.Ctx) error {
		if role := c.Get("X-Test-Role"); role != "" {
			c.Locals("role", role)
			c.Locals("user_id", uuid.New())
		}
		return c.Next()
	})
	installStubEngineMiddleware(app)

	app.Post("/veeam-servers/:id/jobs/:job_id/start", handler.StartJob)
	app.Post("/veeam-servers/:id/jobs/:job_id/stop", handler.StopJob)
	app.Post("/veeam-servers/:id/jobs/:job_id/enable", handler.EnableJob)
	app.Post("/veeam-servers/:id/jobs/:job_id/disable", handler.DisableJob)
	app.Post("/veeam-servers/:id/sessions/:session_id/stop", handler.StopSession)
	app.Get("/veeam-servers/:id/sessions/:session_id/logs", handler.GetSessionLogs)

	return app
}

// A caller with no execute:veeam grant anywhere is refused BEFORE any row is
// loaded, so 404-vs-403 cannot be used to probe which job or session ids
// exist. The nil queries handle is the enforcement: reordering the check after
// the lookup panics instead of passing.
func TestVeeamControl_RefusesBeforeAnyLookup(t *testing.T) {
	app := newVeeamControlTestApp(t)
	serverID := uuid.New().String()
	jobID := uuid.New().String()
	sessionID := uuid.New().String()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"start", http.MethodPost, "/veeam-servers/" + serverID + "/jobs/" + jobID + "/start"},
		{"stop", http.MethodPost, "/veeam-servers/" + serverID + "/jobs/" + jobID + "/stop"},
		{"enable", http.MethodPost, "/veeam-servers/" + serverID + "/jobs/" + jobID + "/enable"},
		{"disable", http.MethodPost, "/veeam-servers/" + serverID + "/jobs/" + jobID + "/disable"},
		{"session stop", http.MethodPost, "/veeam-servers/" + serverID + "/sessions/" + sessionID + "/stop"},
		{"session logs", http.MethodGet, "/veeam-servers/" + serverID + "/sessions/" + sessionID + "/logs"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, body := doVeeamRequest(t, app, tc.method, tc.path, "viewer", "")
			if status != http.StatusForbidden {
				t.Errorf("status = %d, want 403 — body: %s", status, body)
			}
		})
	}
}

// A malformed id is caller-supplied syntax, not server state, so rejecting it
// first reveals nothing. It must be a 400 rather than reaching the client and
// being concatenated into a request path.
func TestVeeamControl_RejectsMalformedIDs(t *testing.T) {
	app := newVeeamControlTestApp(t)
	serverID := uuid.New().String()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"bad server id", http.MethodPost, "/veeam-servers/not-a-uuid/jobs/" + uuid.New().String() + "/start"},
		{"bad job id", http.MethodPost, "/veeam-servers/" + serverID + "/jobs/not-a-uuid/start"},
		{"bad session id", http.MethodPost, "/veeam-servers/" + serverID + "/sessions/not-a-uuid/stop"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, body := doVeeamRequest(t, app, tc.method, tc.path, "admin", "")
			if status != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 — body: %s", status, body)
			}
		})
	}
}

// The status map the SPA acts on.
//
// The 422-not-401 rule is the load-bearing one: the SPA reads a 401 as ITS OWN
// session expiring, refreshes and REPLAYS the request. That spends a second
// failed logon against the domain account per click — halving the operator's
// lockout budget — and logs them out of Nexara if the refresh fails, because
// their VEEAM password was wrong.
func TestRenderVeeamControlError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{
			name: "auth failure is 422, never 401",
			err:  fmt.Errorf("veeam: bad credentials: %w", veeam.ErrAuthFailed),
			want: http.StatusUnprocessableEntity,
		},
		{
			// "…job is not currently running." The request was fine when the
			// operator clicked and is no longer true, which is what 409 says
			// and what tells the UI to refetch rather than blame the input.
			name: "a 400 from Veeam becomes 409",
			err:  &veeam.APIError{StatusCode: http.StatusBadRequest, Message: "The job is not currently running."},
			want: http.StatusConflict,
		},
		{
			name: "a 404 from Veeam stays 404",
			err:  &veeam.APIError{StatusCode: http.StatusNotFound, Message: "not found"},
			want: http.StatusNotFound,
		},
		{
			name: "unreachable is a gateway problem",
			err:  fmt.Errorf("%w: dial tcp: refused", veeam.ErrUnreachable),
			want: http.StatusBadGateway,
		},
		{
			name: "a bad stored config is the caller's 400",
			err:  fmt.Errorf("%w: BaseURL is not a valid URL", veeam.ErrInvalidInput),
			want: http.StatusBadRequest,
		},
		{
			name: "the Proxmox-platform 400 is unprocessable, not a conflict",
			err:  fmt.Errorf("veeam: unsupported: %w", veeam.ErrPlatformUnsupported),
			want: http.StatusUnprocessableEntity,
		},
		{
			// controlClient builds fiber errors of its own (a failed
			// decrypt is a 500). Flattening those to 502 would report a
			// Nexara-side fault as the Veeam server's.
			name: "a fiber error keeps its own status",
			err:  fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt stored password"),
			want: http.StatusInternalServerError,
		},
		{
			name: "anything else is a gateway problem",
			err:  errors.New("something unexpected"),
			want: http.StatusBadGateway,
		},
	}

	// Routed by index rather than by name: renderVeeamControlError writes to a
	// fiber.Ctx, so it needs a real request, and the names here contain
	// spaces.
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	for i, tc := range tests {
		app.Get("/err/"+strconv.Itoa(i), func(c fiber.Ctx) error {
			return renderVeeamControlError(c, tc.err)
		})
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, body := doVeeamRequest(t, app, http.MethodGet, "/err/"+strconv.Itoa(i), "admin", "")
			if status != tc.want {
				t.Errorf("status = %d, want %d — body: %s", status, tc.want, body)
			}
		})
	}
}

// The cluster an action is filed under. A cluster-scoped operator reads
// audit_log filtered by cluster, so an unattributable row must be NULL rather
// than a zero uuid — a zero uuid matches no cluster and would look like a
// deliberate attribution to one that does not exist.
func TestVeeamScope_ClusterFor(t *testing.T) {
	clusterA := uuid.New()
	platformA := uuid.New()
	platformUnmapped := uuid.New()

	scope := veeamScope{platformCluster: map[uuid.UUID]uuid.UUID{platformA: clusterA}}

	if got := scope.clusterFor(platformA, true); !got.Valid || uuid.UUID(got.Bytes) != clusterA {
		t.Errorf("clusterFor(mapped) = %+v, want %s", got, clusterA)
	}
	if got := scope.clusterFor(platformUnmapped, true); got.Valid {
		t.Errorf("clusterFor(unmapped platform) = %+v, want NULL", got)
	}
	// A job that has never run has no platform at all.
	if got := scope.clusterFor(uuid.Nil, false); got.Valid {
		t.Errorf("clusterFor(no platform) = %+v, want NULL", got)
	}
}

// A percentage from an upstream server reaches an INTEGER column. Veeam
// reports 0-100; anything else is a server inventing values, and it must not
// become a negative or absurd stored figure.
func TestClampPercent(t *testing.T) {
	tests := []struct {
		in   int
		want int32
	}{
		{0, 0}, {50, 50}, {100, 100}, {-1, 0}, {101, 100}, {1 << 40, 100},
	}
	for _, tc := range tests {
		if got := clampPercent(tc.in); got != tc.want {
			t.Errorf("clampPercent(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// An unparseable id becomes SQL NULL, never the zero uuid — every row that
// failed to parse would otherwise collide on one key and appear to be the same
// job.
func TestOptionalUUIDFromString(t *testing.T) {
	id := uuid.New()
	if got := optionalUUIDFromString(id.String()); !got.Valid || uuid.UUID(got.Bytes) != id {
		t.Errorf("optionalUUIDFromString(valid) = %+v, want %s", got, id)
	}
	for _, bad := range []string{"", "not-a-uuid", "00000000"} {
		if got := optionalUUIDFromString(bad); got.Valid {
			t.Errorf("optionalUUIDFromString(%q) = %+v, want NULL", bad, got)
		}
	}
}
