package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The unit tests of selfCredentialRefusal (internal/api/handlers/access_test.go)
// decide the guard's three outcomes with no database and no Proxmox. They pass
// with a handler that never reads the cluster row, or reads it and drops the
// error, or calls the guard only for some of the requests it covers. This file
// follows the guard through the routes that call it, on the real declarations,
// permission gate and handlers, and the stand-in Proxmox and database of
// registry_access_update_user_test.go.
//
// Every case names Nexara's own account or token: the stand-in cluster's
// credential is accessUpdateSelf!api.

// accessGuardedRoute is a request the self-credential guard covers, aimed at
// Nexara's own account or token, with what the same request does when it is
// forced.
type accessGuardedRoute struct {
	name string
	// method and path are the declaration's, and body the request's.
	method, path, body string
	handler            func(*handlers.AccessHandler) Handler
	// wantSubject is what the guard's conflict names.
	wantSubject string

	// What the request does when it is sent with force=true.
	wantStatus int
	// wantPVE is the one Proxmox request it makes, "METHOD /path".
	wantPVE string
	// wantForm is the form that request carries; nil is not compared.
	wantForm                                url.Values
	wantType, wantID, wantAction, wantAudit string
}

// accessGuardedRoutes are the four routes that call guardSelfCredential — the
// user and the token, each deleted or updated — with the update of the token
// once for each kind of change that reaches the guard.
var accessGuardedRoutes = []accessGuardedRoute{
	{
		name:   "delete the user",
		method: http.MethodDelete, path: accessScope + "/users/:userid",
		handler:     func(h *handlers.AccessHandler) Handler { return h.DeleteUser },
		wantSubject: "user nexara@pve",
		wantStatus:  fiber.StatusNoContent,
		wantPVE:     "DELETE /api2/json/access/users/nexara@pve",
		wantType:    "pve_user", wantID: "nexara@pve", wantAction: "deleted",
		wantAudit: `{"forced":true,"userid":"nexara@pve"}`,
	},
	{
		name:   "disable the user",
		method: http.MethodPut, path: accessScope + "/users/:userid",
		body:        `{"enable":false}`,
		handler:     func(h *handlers.AccessHandler) Handler { return h.UpdateUser },
		wantSubject: "user nexara@pve",
		wantStatus:  fiber.StatusOK,
		wantPVE:     "PUT /api2/json/access/users/nexara@pve",
		wantForm:    url.Values{"enable": {"0"}},
		wantType:    "pve_user", wantID: "nexara@pve", wantAction: "updated",
		wantAudit: `{"enable":false,"forced":true,"userid":"nexara@pve"}`,
	},
	{
		name:   "regenerate the token",
		method: http.MethodPut, path: accessScope + "/users/:userid/tokens/:tokenid",
		body:        `{"regenerate":true}`,
		handler:     func(h *handlers.AccessHandler) Handler { return h.UpdateToken },
		wantSubject: "token nexara@pve!api",
		wantStatus:  fiber.StatusOK,
		wantPVE:     "PUT /api2/json/access/users/nexara@pve/token/api",
		wantForm:    url.Values{"regenerate": {"1"}},
		wantType:    "pve_token", wantID: "nexara@pve!api", wantAction: "regenerated",
		wantAudit: `{"forced":true,"regenerate":true,"tokenid":"api","userid":"nexara@pve"}`,
	},
	{
		name:   "give the token an expiry",
		method: http.MethodPut, path: accessScope + "/users/:userid/tokens/:tokenid",
		body:        `{"expire":1767225600}`,
		handler:     func(h *handlers.AccessHandler) Handler { return h.UpdateToken },
		wantSubject: "token nexara@pve!api",
		wantStatus:  fiber.StatusOK,
		wantPVE:     "PUT /api2/json/access/users/nexara@pve/token/api",
		wantForm:    url.Values{"expire": {"1767225600"}},
		wantType:    "pve_token", wantID: "nexara@pve!api", wantAction: "updated",
		wantAudit: `{"expire":1767225600,"forced":true,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`,
	},
	{
		name:   "turn the token's privilege separation on",
		method: http.MethodPut, path: accessScope + "/users/:userid/tokens/:tokenid",
		body:        `{"privsep":true}`,
		handler:     func(h *handlers.AccessHandler) Handler { return h.UpdateToken },
		wantSubject: "token nexara@pve!api",
		wantStatus:  fiber.StatusOK,
		wantPVE:     "PUT /api2/json/access/users/nexara@pve/token/api",
		wantForm:    url.Values{"privsep": {"1"}},
		wantType:    "pve_token", wantID: "nexara@pve!api", wantAction: "updated",
		wantAudit: `{"forced":true,"privsep":true,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`,
	},
	{
		name:   "delete the token",
		method: http.MethodDelete, path: accessScope + "/users/:userid/tokens/:tokenid",
		handler:     func(h *handlers.AccessHandler) Handler { return h.DeleteToken },
		wantSubject: "token nexara@pve!api",
		wantStatus:  fiber.StatusNoContent,
		wantPVE:     "DELETE /api2/json/access/users/nexara@pve/token/api",
		wantType:    "pve_token", wantID: "nexara@pve!api", wantAction: "deleted",
		wantAudit: `{"forced":true,"tokenid":"api","userid":"nexara@pve"}`,
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
	pve, store, h := newAccessStandIns(t)

	e := declaredEndpoint(t, route.method, route.path)
	e.Handler = route.handler(h)

	return newRegistryApp(t, stubAuth(map[string]bool{"manage:" + handlers.AccessResource: true}), e), pve, store
}

// accessGuardLeak is an address the database error below names, so that a test
// can see that the answer to the caller does not carry the error's own text.
const accessGuardLeak = "192.0.2.10:5432"

// accessGuardLookupFailures are the two ways the guard's read of the cluster row
// can fail, and what the guard answers each with.
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

// TestAccessGuardRefusesWhenItCannotCheck is the fail-open the guard used to
// have, closed on each route that calls it. The stand-in database fails the
// guard's own read of the cluster row and only that one: the read that builds the
// Proxmox client, which comes next, would succeed. So a guard that let the
// request through would reach Proxmox and do what it was sent to do, on the
// account Nexara uses, and a guard that refuses must do so before anything is
// sent.
//
// The refusal is not the conflict. The SPA answers a 409 with an override whose
// title says the action will cut Nexara off, and here nothing says so: the row
// that would tell could not be read. It is a 500 whose text says the check could
// not be made and nothing changed, or a 404 for a cluster that is not there.
//
// Nothing reaches Proxmox, nothing is audited, and the row was read once, by the
// guard: the request stopped before the client was built.
func TestAccessGuardRefusesWhenItCannotCheck(t *testing.T) {
	for _, route := range accessGuardedRoutes {
		for _, failure := range accessGuardLookupFailures {
			t.Run(route.name+", "+failure.name, func(t *testing.T) {
				app, pve, store := newAccessGuardedApp(t, route)
				store.failClusterRead(1, failure.err)

				status, env := send(t, app, route.request(""))
				if status != failure.wantStatus {
					t.Fatalf("status = %d (%q), want %d", status, env.Message, failure.wantStatus)
				}
				if !strings.Contains(env.Message, failure.wantIn) {
					t.Errorf("the refusal %q does not carry %q", env.Message, failure.wantIn)
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
			})
		}
	}
}

// TestAccessGuardRefusesTheCredentialItCanRead is the guard's other refusal, on
// the same routes: the row is read, the credential is Nexara's own, and the
// answer is the conflict, which names the credential and the way past it. It is
// the counterpart of the test above, so that the two refusals stay different.
func TestAccessGuardRefusesTheCredentialItCanRead(t *testing.T) {
	for _, route := range accessGuardedRoutes {
		t.Run(route.name, func(t *testing.T) {
			app, pve, store := newAccessGuardedApp(t, route)

			status, env := send(t, app, route.request(""))
			if status != fiber.StatusConflict {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, fiber.StatusConflict)
			}
			for _, fragment := range []string{route.wantSubject, "force=true"} {
				if !strings.Contains(env.Message, fragment) {
					t.Errorf("the refusal %q does not carry %q", env.Message, fragment)
				}
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
		})
	}
}

// TestAccessForcedRequestGoesThroughWithoutTheGuardReadingTheRow is what force
// is for, on each route: the same request the tests above refuse, sent with
// force=true, reaches Proxmox once and is recorded as forced. The cluster row is
// read once, to build the Proxmox client, and not a second time for a guard that
// the operator has already answered.
func TestAccessForcedRequestGoesThroughWithoutTheGuardReadingTheRow(t *testing.T) {
	for _, route := range accessGuardedRoutes {
		t.Run(route.name, func(t *testing.T) {
			app, pve, store := newAccessGuardedApp(t, route)

			status, env := send(t, app, route.request("?force=true"))
			if status != route.wantStatus {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, route.wantStatus)
			}

			sent := pve.requests()
			if len(sent) != 1 {
				t.Fatalf("the handler made %d Proxmox calls, want exactly 1: %+v", len(sent), sent)
			}
			if got := sent[0].method + " " + sent[0].path; got != route.wantPVE {
				t.Fatalf("the handler sent %s, want %s", got, route.wantPVE)
			}
			if route.wantForm != nil && !reflect.DeepEqual(sent[0].form, route.wantForm) {
				t.Errorf("Proxmox received the form %v, want %v", sent[0].form, route.wantForm)
			}

			row := oneAccessAuditRow(t, store.auditRows())
			if row.resourceType != route.wantType || row.resourceID != route.wantID || row.action != route.wantAction {
				t.Errorf("the audit row is (%q, %q, %q), want (%s, %s, %s)",
					row.resourceType, row.resourceID, row.action, route.wantType, route.wantID, route.wantAction)
			}
			if string(row.details) != route.wantAudit {
				t.Errorf("the audit details are %s, want %s", row.details, route.wantAudit)
			}

			if reads := store.clusterReadCount(); reads != 1 {
				t.Errorf("the cluster row was read %d times, want once, to build the client", reads)
			}
		})
	}
}

// TestAccessGuardRecognisesATokenWhoseUserNameHasABang follows an own token such
// as svc!x@pve!api through the route. PVE's user names may contain "!", and the
// guard once cut the cluster's token id at the first one, so it never matched such
// an account and let the delete through without force. The request names the
// account as a client sends it, percent-encoded.
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

// TestAccessGuardLogsWhyItCouldNotCheck: the refusal says only that the check
// could not be made, so its cause goes to the server log, which is where an
// operator finds it. A database that failed is an Error, a timeout or a
// cancellation among them: a request's own context never ends here (Fiber's
// c.Context() is Background unless SetContext is called), so a context error in
// the failure's chain is the database driver's. The Warn for a context that has
// ended is pinned where one can be made to end, in TestLogGuardLookupFailure. A
// cluster that is not there is the caller's mistake and is not logged at all.
func TestAccessGuardLogsWhyItCouldNotCheck(t *testing.T) {
	route := accessGuardedRoutes[0]

	tests := []struct {
		name string
		err  error
		// wantLevel is the level of the one record the guard writes; "" is none.
		wantLevel string
	}{
		{"the database fails", errors.New("dial tcp " + accessGuardLeak + ": connection refused"), "ERROR"},
		{"the database times out", context.DeadlineExceeded, "ERROR"},
		{"the database cancels the query", context.Canceled, "ERROR"},
		{"the cluster does not exist", pgx.ErrNoRows, ""},
	}
	for _, tt := range tests {
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

// TestAccessGuardRefusalMirrorsBuildingTheClient pins the statuses of the
// refusals above to the ones a request gets when it is the client build that
// meets the same failure. An edit the guard does not cover, a comment change,
// never reaches the guard, so its build is the first to read the row, and what it
// answers is CreateProxmoxClient's.
//
// selfCredentialRefusal answers with those statuses on purpose, so that one
// failure gets one answer whichever of the two reads meets it. A change to either
// side that left the other behind fails here.
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

// TestAccessGuardRefusesWithoutADatabase is the third way the guard cannot look:
// an AccessHandler built with no database at all. No production wiring builds one
// — server.go builds the handler only where queries exist — so this pins a
// decision rather than a reachable state. The guard once let the request through;
// it refuses, and it does so without dereferencing the missing database.
func TestAccessGuardRefusesWithoutADatabase(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodDelete, accessScope+"/users/:userid/tokens/:tokenid")
	e.Handler = handlers.NewAccessHandler(nil, accessUpdateEncKey, nil).DeleteToken

	// A guard that let this request through would go on to dereference the missing
	// database. The recover middleware makes that a failed assertion below, where
	// the registry's own re-panic would crash the test binary and hide every test
	// that runs after this one. It is a 500 too, so the message assertion is the
	// one that bites.
	reg := NewRegistry()
	reg.Register(e)
	app := fiber.New(fiber.Config{ErrorHandler: errorHandler})
	app.Use(recover.New())
	mountRegistry(app, reg, stubAuth(map[string]bool{"manage:" + handlers.AccessResource: true}), nil)

	status, env := send(t, app, accessTokenRequest(http.MethodDelete, testAccessUserID, testAccessTokenID, "", ""))
	if status != fiber.StatusInternalServerError {
		t.Fatalf("status = %d (%q), want %d", status, env.Message, fiber.StatusInternalServerError)
	}
	if !strings.Contains(env.Message, "could not check") {
		t.Errorf("the refusal %q does not say the check could not be made", env.Message)
	}
}
