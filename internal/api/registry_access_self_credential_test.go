package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The unit tests of selfCredentialRefusal (internal/api/handlers/access_test.go) decide
// the guard's three outcomes with no database and no Proxmox. They pass with a handler
// that never reads the cluster row, or reads it and drops the error, or calls the guard
// only for some of the requests it covers. This file follows the guard through the
// routes that call it, on the real declarations, permission gate and handlers and the
// stand-ins of registry_access_kit_test.go. Every case names Nexara's own account or
// token: the stand-in cluster's credential is accessUpdateSelf!api.

// accessGuardedRoute is a request the self-credential guard covers, aimed at Nexara's
// own account or token, with what the same request does when it is forced.
type accessGuardedRoute struct {
	name string
	// method and path are the declaration's, and body the request's.
	method, path, body string
	handler            func(*handlers.AccessHandler) Handler
	// wantSubject is what the guard's conflict names.
	wantSubject string
	// What the request does when it is sent with force=true: the answer, the one Proxmox
	// request it makes ("METHOD /path", with its form) and the audit row.
	wantStatus int
	wantPVE    string
	wantForm   url.Values
	wantRow    auditRowWant
}

const (
	selfUserPVE  = "DELETE /api2/json/access/users/nexara@pve"
	selfTokenPVE = "/api2/json/access/users/nexara@pve/token/api"
)

// accessGuardedRoutes are the four routes that call guardSelfCredential (the user and
// the token, each deleted or updated), with the token update once for each kind of
// change that reaches the guard.
var accessGuardedRoutes = []accessGuardedRoute{
	{
		name: "delete the user", method: http.MethodDelete, path: accessScope + "/users/:userid",
		handler:     func(h *handlers.AccessHandler) Handler { return h.DeleteUser },
		wantSubject: "user nexara@pve", wantStatus: fiber.StatusNoContent, wantPVE: selfUserPVE,
		wantRow: auditRow("pve_user", "nexara@pve", "deleted", `{"forced":true,"userid":"nexara@pve"}`),
	},
	{
		name: "disable the user", method: http.MethodPut, path: accessScope + "/users/:userid", body: `{"enable":false}`,
		handler:     func(h *handlers.AccessHandler) Handler { return h.UpdateUser },
		wantSubject: "user nexara@pve", wantStatus: fiber.StatusOK, wantPVE: "PUT /api2/json/access/users/nexara@pve",
		wantForm: url.Values{"enable": {"0"}},
		wantRow:  auditRow("pve_user", "nexara@pve", "updated", `{"enable":false,"forced":true,"userid":"nexara@pve"}`),
	},
	{
		name: "regenerate the token", method: http.MethodPut, path: accessScope + "/users/:userid/tokens/:tokenid", body: `{"regenerate":true}`,
		handler:     func(h *handlers.AccessHandler) Handler { return h.UpdateToken },
		wantSubject: "token nexara@pve!api", wantStatus: fiber.StatusOK, wantPVE: "PUT " + selfTokenPVE,
		wantForm: url.Values{"regenerate": {"1"}},
		wantRow:  auditRow("pve_token", "nexara@pve!api", "regenerated", `{"forced":true,"regenerate":true,"tokenid":"api","userid":"nexara@pve"}`),
	},
	{
		name: "give the token an expiry", method: http.MethodPut, path: accessScope + "/users/:userid/tokens/:tokenid", body: `{"expire":1767225600}`,
		handler:     func(h *handlers.AccessHandler) Handler { return h.UpdateToken },
		wantSubject: "token nexara@pve!api", wantStatus: fiber.StatusOK, wantPVE: "PUT " + selfTokenPVE,
		wantForm: url.Values{"expire": {"1767225600"}},
		wantRow:  auditRow("pve_token", "nexara@pve!api", "updated", `{"expire":1767225600,"forced":true,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`),
	},
	{
		name: "turn the token's privilege separation on", method: http.MethodPut, path: accessScope + "/users/:userid/tokens/:tokenid", body: `{"privsep":true}`,
		handler:     func(h *handlers.AccessHandler) Handler { return h.UpdateToken },
		wantSubject: "token nexara@pve!api", wantStatus: fiber.StatusOK, wantPVE: "PUT " + selfTokenPVE,
		wantForm: url.Values{"privsep": {"1"}},
		wantRow:  auditRow("pve_token", "nexara@pve!api", "updated", `{"forced":true,"privsep":true,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`),
	},
	{
		name: "delete the token", method: http.MethodDelete, path: accessScope + "/users/:userid/tokens/:tokenid",
		handler:     func(h *handlers.AccessHandler) Handler { return h.DeleteToken },
		wantSubject: "token nexara@pve!api", wantStatus: fiber.StatusNoContent, wantPVE: "DELETE " + selfTokenPVE,
		wantRow: auditRow("pve_token", "nexara@pve!api", "deleted", `{"forced":true,"tokenid":"api","userid":"nexara@pve"}`),
	},
}

// request is the request a client sends for the route, with query appended.
func (r accessGuardedRoute) request(query string) *http.Request {
	target := accessRoute(r.path) + query
	if r.body == "" {
		return authedRequest(r.method, target)
	}
	req := jsonRequest(r.method, target, r.body)
	req.Header.Set("X-Test-User", "yes")
	return req
}

// newAccessGuardedApp mounts the real declaration of the route, with its real
// permission, carrying the real handler wired to the two stand-ins.
func newAccessGuardedApp(t *testing.T, route accessGuardedRoute) (*fiber.App, *accessUpdatePVE, *accessUpdateDB) {
	t.Helper()
	return newAccessApp(t, manageAccess, func(h *handlers.AccessHandler) []realRoute {
		return []realRoute{{route.method, route.path, route.handler(h)}}
	})
}

// accessGuardLeak is an address the database error below names, so that a test can see
// that the answer to the caller does not carry the error's own text.
const accessGuardLeak = "192.0.2.10:5432"

// accessGuardLookupFailures are the two ways the guard's read of the cluster row can
// fail, and what the guard answers each with.
var accessGuardLookupFailures = []struct {
	name       string
	err        error
	wantStatus int
	wantIn     string
}{
	{"the database fails", errors.New("dial tcp " + accessGuardLeak + ": connection refused"),
		fiber.StatusInternalServerError, "could not check"},
	{"the cluster does not exist", pgx.ErrNoRows, fiber.StatusNotFound, "Cluster not found"},
}

// TestAccessGuardRefuses covers the guard's two refusals on each route that calls it. When it
// cannot check (the stand-in database fails the guard's own read of the cluster row and only
// that one) a guard that let the request through would reach Proxmox and act on the account
// Nexara uses, so it must refuse before anything is sent, and not with the conflict, whose
// override the SPA words as cutting Nexara off: a 500 saying the check could not be made, or a
// 404 for a cluster that is not there. When it can read the credential and it is Nexara's own,
// the answer is the conflict, naming the credential and the way past it. Either way nothing
// reaches Proxmox, nothing is audited, and the row was read once, by the guard.
func TestAccessGuardRefuses(t *testing.T) {
	for _, route := range accessGuardedRoutes {
		for _, failure := range accessGuardLookupFailures {
			t.Run(route.name+", "+failure.name, func(t *testing.T) {
				app, pve, store := newAccessGuardedApp(t, route)
				store.failClusterRead(1, failure.err)
				requireGuardRefusal(t, app, pve, store, route, failure.wantStatus, failure.wantIn)
			})
		}
		t.Run(route.name+", the credential it can read", func(t *testing.T) {
			app, pve, store := newAccessGuardedApp(t, route)
			requireGuardRefusal(t, app, pve, store, route, fiber.StatusConflict, route.wantSubject, "force=true")
		})
	}
}

func requireGuardRefusal(t *testing.T, app *fiber.App, pve *accessUpdatePVE, store *accessUpdateDB, route accessGuardedRoute, status int, fragments ...string) {
	t.Helper()
	got, env := send(t, app, route.request(""))
	if got != status {
		t.Fatalf("status = %d (%q), want %d", got, env.Message, status)
	}
	for _, f := range fragments {
		if !strings.Contains(env.Message, f) {
			t.Errorf("the refusal %q does not carry %q", env.Message, f)
		}
	}
	if strings.Contains(env.Message, accessGuardLeak) {
		t.Errorf("the refusal %q carries the lookup's own text", env.Message)
	}
	if sent := pve.requests(); len(sent) != 0 {
		t.Errorf("the refused request reached Proxmox: %+v", sent)
	}
	if rows := store.auditRows(); len(rows) != 0 {
		t.Errorf("the refused request wrote %d audit row(s), want none", len(rows))
	}
	if reads := store.clusterReadCount(); reads != 1 {
		t.Errorf("the cluster row was read %d times, want once, by the guard", reads)
	}
}

// TestAccessForcedRequestGoesThroughWithoutTheGuardReadingTheRow is what force is for,
// on each route: the same request the guard refuses, sent with force=true, reaches
// Proxmox once and is recorded as forced. The cluster row is read once, to build the
// Proxmox client, and not a second time for a guard the operator has already answered.
func TestAccessForcedRequestGoesThroughWithoutTheGuardReadingTheRow(t *testing.T) {
	for _, route := range accessGuardedRoutes {
		t.Run(route.name, func(t *testing.T) {
			app, pve, store := newAccessGuardedApp(t, route)
			method, path, _ := strings.Cut(route.wantPVE, " ")
			requireAudited(t, app, pve, store, route.request("?force=true"), auditWant{
				status: route.wantStatus, method: method, path: path, form: route.wantForm, audit: route.wantRow,
			})
			if reads := store.clusterReadCount(); reads != 1 {
				t.Errorf("the cluster row was read %d times, want once, to build the client", reads)
			}
		})
	}
}

// TestAccessGuardRecognisesATokenWhoseUserNameHasABang follows an own token such as
// svc!x@pve!api through the route. PVE's user names may contain "!", and the guard once
// cut the cluster's token id at the first one, so it never matched such an account and
// let the delete through without force. The request names the account as a client sends
// it, percent-encoded.
func TestAccessGuardRecognisesATokenWhoseUserNameHasABang(t *testing.T) {
	route := accessGuardedRoutes[len(accessGuardedRoutes)-1] // delete the token
	if route.name != "delete the token" {
		t.Fatalf("the last guarded route is %q, want the token delete this test names", route.name)
	}
	const user, encoded = "svc!x@pve", "svc%21x%40pve"

	app, pve, store := newAccessGuardedApp(t, route)
	store.cluster.TokenID = user + "!api"

	status, env := send(t, app, accessTokenRequest(http.MethodDelete, encoded, testAccessTokenID, "", ""))
	if status != fiber.StatusConflict {
		t.Fatalf("status = %d (%q), want %d", status, env.Message, fiber.StatusConflict)
	}
	for _, fragment := range []string{"token " + user + "!api", "force=true"} {
		if !strings.Contains(env.Message, fragment) {
			t.Errorf("the refusal %q does not carry %q", env.Message, fragment)
		}
	}
	if sent := pve.requests(); len(sent) != 0 {
		t.Errorf("the refused request reached Proxmox: %+v", sent)
	}

	// With force the same request goes through, and names the account decoded.
	status, env = send(t, app, accessTokenRequest(http.MethodDelete, encoded, testAccessTokenID, "?force=true", ""))
	if status != fiber.StatusNoContent {
		t.Fatalf("forced: status = %d (%q), want %d", status, env.Message, fiber.StatusNoContent)
	}
	sent := pve.requests()
	if len(sent) != 1 || sent[0].method+" "+sent[0].path != "DELETE /api2/json/access/users/"+user+"/token/api" {
		t.Errorf("the forced request sent %+v, want one DELETE of %s!api", sent, user)
	}
}

// TestAccessGuardLogsWhyItCouldNotCheck: the refusal says only that the check could not
// be made, so its cause goes to the server log, which is where an operator finds it. A
// database that failed is an Error, a timeout or a cancellation among them: a request's
// own context never ends here (Fiber's c.Context() is Background unless SetContext is
// called), so a context error in the failure's chain is the database driver's. The Warn
// for a context that has ended is pinned in TestLogGuardLookupFailure. A cluster that is
// not there is the caller's mistake and is not logged at all.
func TestAccessGuardLogsWhyItCouldNotCheck(t *testing.T) {
	route := accessGuardedRoutes[0]

	for _, tt := range []struct {
		name string
		err  error
		// wantLevel is the level of the one record the guard writes; "" is none.
		wantLevel string
	}{
		{"the database fails", errors.New("dial tcp " + accessGuardLeak + ": connection refused"), "ERROR"},
		{"the database times out", context.DeadlineExceeded, "ERROR"},
		{"the database cancels the query", context.Canceled, "ERROR"},
		{"the cluster does not exist", pgx.ErrNoRows, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			logged := captureSlog(t)
			app, _, store := newAccessGuardedApp(t, route)
			store.failClusterRead(1, tt.err)

			if status, env := send(t, app, route.request("")); status < fiber.StatusBadRequest {
				t.Fatalf("status = %d (%q), want a refusal", status, env.Message)
			}

			var record string
			for _, line := range strings.Split(logged(), "\n") {
				if strings.Contains(line, "self-credential guard") {
					record = line
				}
			}
			if tt.wantLevel == "" {
				if record != "" {
					t.Errorf("the guard logged %q, want nothing", record)
				}
				return
			}
			if record == "" {
				t.Fatal("the guard logged nothing")
			}
			for _, want := range []string{"level=" + tt.wantLevel, "cluster_id=" + testClusterID, tt.err.Error()} {
				if !strings.Contains(record, want) {
					t.Errorf("the record %q does not carry %q", record, want)
				}
			}
		})
	}
}

// TestAccessGuardRefusalMirrorsBuildingTheClient pins the statuses of the refusals
// above to the ones a request gets when it is the client build that meets the same
// failure. An edit the guard does not cover, a comment change, never reaches the guard,
// so its build is the first to read the row, and what it answers is
// CreateProxmoxClient's. selfCredentialRefusal answers with those statuses on purpose,
// so that one failure gets one answer whichever of the two reads meets it.
func TestAccessGuardRefusalMirrorsBuildingTheClient(t *testing.T) {
	for _, failure := range accessGuardLookupFailures {
		t.Run(failure.name, func(t *testing.T) {
			app, pve, store := newAccessUpdateApp(t)
			store.failClusterRead(1, failure.err)

			status, env := send(t, app, accessUpdateRequest(testAccessUserID, "", `{"comment":"sentinel-comment"}`))
			if status != failure.wantStatus {
				t.Fatalf("an unguarded edit whose client build met the failure answered %d (%q), but the guard answers %d for it",
					status, env.Message, failure.wantStatus)
			}
			if failure.wantStatus == fiber.StatusNotFound && !strings.Contains(env.Message, failure.wantIn) {
				t.Errorf("the client build answered %q for a missing cluster, but the guard says %q", env.Message, failure.wantIn)
			}
			if sent := pve.requests(); len(sent) != 0 {
				t.Errorf("the request reached Proxmox: %+v", sent)
			}
		})
	}
}

// TestAccessGuardRefusesWithoutADatabase is the third way the guard cannot look: an
// AccessHandler built with no database at all. No production wiring builds one, so this
// pins a decision rather than a reachable state. The guard once let the request
// through; it refuses, without dereferencing the missing database.
func TestAccessGuardRefusesWithoutADatabase(t *testing.T) {
	e := sharedEndpoint(t, fiber.MethodDelete, accessScope+"/users/:userid/tokens/:tokenid")
	e.Handler = handlers.NewAccessHandler(nil, accessUpdateEncKey, nil).DeleteToken
	// A guard that let this request through would dereference the missing database;
	// recover makes that a failed assertion below, where the registry's own re-panic
	// would crash the test binary and hide every test that runs after this one.
	app := mountWith(stubAuth(manageAccess), nil, e, recover.New())

	status, env := send(t, app, accessTokenRequest(http.MethodDelete, testAccessUserID, testAccessTokenID, "", ""))
	if status != fiber.StatusInternalServerError || !strings.Contains(env.Message, "could not check") {
		t.Errorf("status = %d (%q), want a 500 saying the check could not be made", status, env.Message)
	}
}
