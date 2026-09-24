package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/config"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The cluster delete requires confirm=<the cluster's current name>. The tests
// here drive the REAL declaration and the REAL ClusterHandler.Delete behind the
// production middleware stack, with stand-ins only at the two edges the
// handler reaches out to: the database, holding one cluster, and the cluster's
// Proxmox API, which the credential revocation calls.
//
// The request that matters most is the one a proxy makes. Traefik resolves dot
// segments by default, so a script's DELETE /api/v1/clusters/<id>/pools/..
// reaches Nexara as DELETE /api/v1/clusters/<id>, with no trailing slash for
// refuseTrailingSlashWrites to refuse (README.md, Reverse Proxy). It carries no
// confirm, and neither does any other request written for some other route.

// clusterDeleteEncKey encrypts the stand-in cluster's token secret. A fixture.
const clusterDeleteEncKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// clusterDeleteMissing is the schema's answer to a request with no confirm at
// all: the registry rejects it before the handler runs.
const clusterDeleteMissing = "confirm: missing required parameter"

// clusterDeleteWrong is the handler's answer to a confirm that is not the
// cluster's name. It is pinned here, as a client reads it, rather than shared
// with the handler: changing it is changing the API.
const clusterDeleteWrong = "confirm: must be the cluster's current name; " +
	"send confirm=<the cluster's name> to delete a cluster"

var errClusterDeleteUnexpected = errors.New("unexpected query")

// clusterDeleteDB stands in for the database: one cluster row, which
// DeleteCluster removes. The handler's other reads are answered as an idle
// cluster would answer them.
type clusterDeleteDB struct {
	mu      sync.Mutex
	cluster db.Cluster
	busy    bool // an active rolling update holds the cluster
	deleted bool
	audits  int
}

func (d *clusterDeleteDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case strings.Contains(sql, "-- name: GetCluster :one"):
		if d.deleted || len(args) != 1 || args[0] != d.cluster.ID {
			return clusterDeleteRow{err: pgx.ErrNoRows}
		}
		return clusterDeleteRow{values: structFields(d.cluster)}
	case strings.Contains(sql, "-- name: HasRunningJobForCluster :one"):
		return clusterDeleteRow{values: []any{d.busy}}
	case strings.Contains(sql, "-- name: CountClustersSharingBootstrapUser :one"):
		return clusterDeleteRow{values: []any{int64(0)}}
	}
	return clusterDeleteRow{err: errClusterDeleteUnexpected}
}

// Query serves ListCleanupPendingJobsForCluster, the handler's one multi-row
// read. An error reads there as "no pending cleanup", which is what an idle
// cluster has.
func (d *clusterDeleteDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	if !strings.Contains(sql, "-- name: ListCleanupPendingJobsForCluster :many") {
		return nil, fmt.Errorf("%w: %.60s", errClusterDeleteUnexpected, sql)
	}
	return nil, errClusterDeleteUnexpected
}

func (d *clusterDeleteDB) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case strings.Contains(sql, "-- name: DeleteCluster :exec"):
		d.deleted = true
		return pgconn.NewCommandTag("DELETE 1"), nil
	case strings.Contains(sql, "INSERT INTO audit_log"):
		d.audits++
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	return pgconn.CommandTag{}, errClusterDeleteUnexpected
}

func (d *clusterDeleteDB) state() (deleted bool, audits int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.deleted, d.audits
}

// structFields lists a struct's field values in declaration order — the order
// sqlc's generated Scan calls take their destinations in.
func structFields(v any) []any {
	rv := reflect.ValueOf(v)
	out := make([]any, rv.NumField())
	for i := range out {
		out[i] = rv.Field(i).Interface()
	}
	return out
}

// clusterDeleteRow replays values through pgx.Row positionally, refusing a
// destination of the wrong type rather than shifting a value into it.
type clusterDeleteRow struct {
	values []any
	err    error
}

func (r clusterDeleteRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan got %d destinations, want %d", len(dest), len(r.values))
	}
	for i, d := range dest {
		ptr := reflect.ValueOf(d)
		val := reflect.ValueOf(r.values[i])
		if ptr.Kind() != reflect.Pointer || ptr.Elem().Type() != val.Type() {
			return fmt.Errorf("scan destination %d is %T, want *%s", i, d, val.Type())
		}
		ptr.Elem().Set(val)
	}
	return nil
}

// clusterDeletePVE stands in for the cluster's Proxmox API. It lists the one
// token Nexara minted, so the revocation finds nothing else depending on the
// user and deletes it, and records every request.
type clusterDeletePVE struct {
	mu   sync.Mutex
	seen []string
}

func (p *clusterDeletePVE) serve(t *testing.T, userID, tokenName string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.seen = append(p.seen, r.Method+" "+r.URL.Path)
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api2/json/access/users/"+userID+"/token" {
			_, _ = fmt.Fprintf(w, `{"data":[{"tokenid":%q}]}`, tokenName)
			return
		}
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (p *clusterDeletePVE) requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

// clusterDeleteFixture is what newClusterDeleteApp builds.
type clusterDeleteFixture struct {
	name string // the stored cluster's name
	busy bool   // an active rolling update holds the cluster
	// grants is what the caller holds. Nil grants delete:cluster, the global
	// manage:cluster the revocation asks for, and the manage:pool the pool
	// routes ask for, so that no permission is what refuses anything.
	grants map[string]bool
	extra  []Endpoint // mounted after the cluster delete
}

// newClusterDeleteApp is the production middleware stack with the real
// cluster DELETE declaration mounted on it, its handler the real
// ClusterHandler.Delete over a database holding one cluster, whose credential
// Nexara minted, as the fixture describes it.
func newClusterDeleteApp(t *testing.T, f clusterDeleteFixture) (*fiber.App, *clusterDeleteDB, *clusterDeletePVE) {
	t.Helper()
	const userID, tokenName = "nexara@pve", "nexara-cluster01"
	pve := &clusterDeletePVE{}
	secret, err := crypto.Encrypt("token-secret-value", clusterDeleteEncKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	store := &clusterDeleteDB{cluster: db.Cluster{
		ID:                   uuid.MustParse(testClusterID),
		Name:                 f.name,
		ApiUrl:               pve.serve(t, userID, tokenName),
		TokenID:              userID + "!" + tokenName,
		TokenSecretEncrypted: secret,
		SyncIntervalSeconds:  30,
		IsActive:             true,
		CredentialSource:     "bootstrap",
		BootstrapUserID:      userID,
		BootstrapTokenName:   tokenName,
		BootstrapCreatedUser: true,
	}, busy: f.busy}

	e := declaredEndpoint(t, fiber.MethodDelete, clusterByID)
	e.Handler = handlers.NewClusterHandler(db.New(store), clusterDeleteEncKey, nil).Delete

	cfg := &config.Config{RateLimitMax: 1_000_000, RateLimitExpiration: time.Minute}
	s := &Server{config: cfg, app: fiber.New(buildFiberConfig(cfg))}
	s.setupMiddleware()
	reg := NewRegistry()
	reg.Register(e)
	for _, x := range f.extra {
		reg.Register(x)
	}
	grants := f.grants
	if grants == nil {
		grants = map[string]bool{"delete:cluster": true, "manage:cluster": true, "manage:pool": true}
	}
	mountRegistry(s.app, reg, stubAuth(grants))
	return s.app, store, pve
}

// TestClusterDeleteRequiresTheClusterName drives every refusal with its
// precondition twin. The twins are the requests that DO delete — with the
// revocation that also deletes the Proxmox user among them — so the refusals
// are shown to stop a request that would otherwise have done the damage.
func TestClusterDeleteRequiresTheClusterName(t *testing.T) {
	const deleteUser = "DELETE /api2/json/access/users/nexara@pve"
	base := pathPrefix + "clusters/" + testClusterID

	for _, tt := range []struct {
		name  string
		query string
		// wantMessage is the refusal; "" means the delete goes ahead.
		wantMessage string
		// wantPVE is what the stand-in Proxmox receives when it does.
		wantPVE []string
	}{
		{
			name:        "the request Traefik makes of DELETE …/pools/..: no confirm",
			wantMessage: clusterDeleteMissing,
		},
		{name: "no confirm, revoking", query: "?revoke_pve_credentials=1", wantMessage: clusterDeleteMissing},
		{name: "another cluster's name", query: "?confirm=cluster02", wantMessage: clusterDeleteWrong},
		{
			name:        "another cluster's name, revoking",
			query:       "?confirm=cluster02&revoke_pve_credentials=1",
			wantMessage: clusterDeleteWrong,
		},
		{name: "the name in another case", query: "?confirm=CLUSTER01", wantMessage: clusterDeleteWrong},
		{name: "the name with a trailing space", query: "?confirm=cluster01%20", wantMessage: clusterDeleteWrong},
		{name: "an empty confirm", query: "?confirm=", wantMessage: clusterDeleteWrong},
		{name: "the name", query: "?confirm=cluster01"},
		{
			name:    "the name, revoking",
			query:   "?confirm=cluster01&revoke_pve_credentials=1",
			wantPVE: []string{"GET /api2/json/access/users/nexara@pve/token", deleteUser},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app, store, pve := newClusterDeleteApp(t, clusterDeleteFixture{name: "cluster01"})
			status, env := send(t, app, authedRequest(http.MethodDelete, base+tt.query))
			deleted, audits := store.state()

			if tt.wantMessage == "" {
				if status != fiber.StatusNoContent || !deleted || audits != 1 {
					t.Fatalf("DELETE %s answered %d %+v (deleted: %v, audit rows: %d), want 204, the cluster "+
						"deleted and the delete audited", tt.query, status, env, deleted, audits)
				}
				if got := pve.requests(); !reflect.DeepEqual(got, tt.wantPVE) {
					t.Errorf("Proxmox received %q, want %q", got, tt.wantPVE)
				}
				return
			}
			if status != fiber.StatusBadRequest || env.Error != "bad_request" || env.Message != tt.wantMessage {
				t.Errorf("DELETE %s answered %d %+v, want 400 %q", tt.query, status, env, tt.wantMessage)
			}
			if deleted || audits != 0 {
				t.Errorf("DELETE %s was refused, but the cluster was deleted (%v) or audited (%d rows)",
					tt.query, deleted, audits)
			}
			if got := pve.requests(); len(got) != 0 {
				t.Errorf("DELETE %s was refused, but Proxmox received %q — the revocation ran", tt.query, got)
			}
		})
	}
}

// TestClusterDeleteDoesNotTellAGuessRightFromWrong pins where the name check
// sits among the refusals. Two refusals only read — an active rolling update
// (409), and revocation asked for without the global manage:cluster it needs
// (403) — and the name check must come after both: otherwise a caller who can
// reach the route but has only guessed the name learns whether the guess was
// right from a 400 against a 403 or 409, with nothing deleted either way.
// Each case therefore sends a wrong guess and the right name, and requires
// the two answers to be identical and nothing to change.
func TestClusterDeleteDoesNotTellAGuessRightFromWrong(t *testing.T) {
	base := pathPrefix + "clusters/" + testClusterID
	for _, tt := range []struct {
		name    string
		fixture clusterDeleteFixture
		revoke  bool
		want    int
	}{
		{
			name:    "revoking without the global manage:cluster",
			fixture: clusterDeleteFixture{name: "cluster01", grants: map[string]bool{"delete:cluster": true}},
			revoke:  true,
			want:    fiber.StatusForbidden,
		},
		{
			name:    "during an active rolling update",
			fixture: clusterDeleteFixture{name: "cluster01", busy: true},
			want:    fiber.StatusConflict,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// The error code and message, which is the whole envelope these
			// refusals send; Details is compared as JSON would show it.
			var answers [2]string
			for i, guess := range []string{"cluster02", "cluster01"} {
				app, store, pve := newClusterDeleteApp(t, tt.fixture)
				query := "?confirm=" + guess
				if tt.revoke {
					query += "&revoke_pve_credentials=1"
				}
				status, env := send(t, app, authedRequest(http.MethodDelete, base+query))
				deleted, audits := store.state()
				if status != tt.want || deleted || audits != 0 || len(pve.requests()) != 0 {
					t.Errorf("confirm=%s answered %d %+v (deleted: %v, audit rows: %d, Proxmox: %q), want %d "+
						"and nothing changed", guess, status, env, deleted, audits, pve.requests(), tt.want)
				}
				answers[i] = fmt.Sprintf("%d %s %s %v", status, env.Error, env.Message, env.Details)
			}
			if answers[0] != answers[1] {
				t.Errorf("a wrong guess answered %q and the right name %q; they must not differ", answers[0], answers[1])
			}
		})
	}
}

// TestAnUnresolvedDotSegmentReachesThePoolRoute is what lets the README say
// Nexara does not rely on Traefik's sanitizePath. With it off, Traefik
// forwards a dot segment unresolved (it still decodes %2E, so the segment
// arrives as the dots themselves), and nothing here resolves it either:
// Fiber routes the path as it arrives, so the request reaches the POOL
// route, whose pool_id rule refuses the name — never the cluster delete.
// The ordinary pool id is the precondition that the pool route is mounted
// and reachable, so the refusals come from its rule and not from a 404.
func TestAnUnresolvedDotSegmentReachesThePoolRoute(t *testing.T) {
	for _, tt := range []struct {
		id      string
		reaches bool
	}{
		{"pool01", true},
		{"..", false},
		{".", false},
	} {
		t.Run(tt.id, func(t *testing.T) {
			pool := &capture{}
			pe := declaredEndpoint(t, fiber.MethodDelete, clusterScope+"/pools/:pool_id")
			pe.Handler = pool.handler()
			app, store, pve := newClusterDeleteApp(t, clusterDeleteFixture{name: "cluster01", extra: []Endpoint{pe}})

			target := pathPrefix + "clusters/" + testClusterID + "/pools/" + tt.id
			status, env := send(t, app, authedRequest(http.MethodDelete, target))
			deleted, _ := store.state()
			if deleted || len(pve.requests()) != 0 {
				t.Fatalf("DELETE %s deleted the cluster (%v) or reached Proxmox (%q)", target, deleted, pve.requests())
			}
			if tt.reaches {
				if status != fiber.StatusNoContent || !pool.called || pool.params.String("pool_id") != tt.id {
					t.Fatalf("precondition: DELETE %s answered %d %+v (pool handler ran: %v), want the pool route "+
						"to run with pool_id %q", target, status, env, pool.called, tt.id)
				}
				return
			}
			if status != fiber.StatusBadRequest || !strings.HasPrefix(env.Message, "pool_id: ") || pool.called {
				t.Errorf("DELETE %s answered %d %+v (pool handler ran: %v), want the pool_id rule's 400",
					target, status, env, pool.called)
			}
		})
	}
}

// TestARewrittenPoolEditCannotEditTheCluster is the PUT twin of the cluster
// delete's confirm. Behind a proxy that resolves dot segments, a script's
// PUT /api/v1/clusters/<id>/pools/.. arrives as PUT /api/v1/clusters/<id> —
// the cluster edit — carrying a pool edit's body. The cluster edit refuses
// it only because the two share no parameter, and this test is what keeps
// that true: every body parameter the pool edit declares, read from its
// declaration, is sent on its own and must be refused as one the cluster edit
// does not declare. A cluster field added under one of those names (a
// cluster comment, say) fails here, rather than quietly letting a rewritten
// pool edit through as a cluster edit.
func TestARewrittenPoolEditCannotEditTheCluster(t *testing.T) {
	pool := declaredEndpoint(t, fiber.MethodPut, clusterScope+"/pools/:pool_id")
	var keys []string
	for name, prop := range pool.Parameters {
		if apischema.ResolveSource(name, prop, pool.Method, pool.pathParams) == apischema.SourceBody {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	if want := []string{"comment", "delete", "storage", "vms"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("precondition: the pool edit's body parameters are %q, want %q — update this test with them", keys, want)
	}

	cap := &capture{}
	e := declaredEndpoint(t, fiber.MethodPut, clusterByID)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	app := newRegistryApp(t, noAuth(), e)
	target := pathPrefix + "clusters/" + testClusterID

	// Precondition: the cluster edit takes a body that changes nothing, so a
	// refusal below is the key's and not the request's.
	if status, env := send(t, app, jsonRequest(http.MethodPut, target, `{}`)); status != fiber.StatusNoContent || !cap.called {
		t.Fatalf("precondition: PUT %s {} answered %d %+v (handler ran: %v), want the handler to run", target, status, env, cap.called)
	}

	const unknown = ": unknown parameter (not declared by this endpoint)"
	type probe struct{ body, want string }
	probes := make([]probe, 0, len(keys)+1)
	for _, key := range keys {
		probes = append(probes, probe{fmt.Sprintf(`{%q:"100"}`, key), key + unknown})
	}
	// The whole of a pool edit, as the SPA sends one; the first unknown key
	// in name order is the one the refusal names.
	probes = append(probes, probe{
		`{"comment":"web tier","vms":"100,101","storage":"store01","delete":"102"}`, "comment" + unknown,
	})
	for _, pr := range probes {
		cap.called = false
		status, env := send(t, app, jsonRequest(http.MethodPut, target, pr.body))
		if status != fiber.StatusBadRequest || env.Message != pr.want || cap.called {
			t.Errorf("PUT %s %s answered %d %+v (handler ran: %v), want 400 %q",
				target, pr.body, status, env, cap.called, pr.want)
		}
	}
}

// TestClusterDeleteConfirmIsReadAsTheSPASendsIt pins the other half of the
// contract: the SPA builds the query with URLSearchParams (useDeleteCluster,
// via queryParams), which writes a space as "+" and percent-encodes "+", "&",
// "=" and anything outside ASCII — the first query below is its measured
// output for this name. The server must read that back as the name, or a
// cluster with any of those characters in its name could never be deleted
// from the UI.
func TestClusterDeleteConfirmIsReadAsTheSPASendsIt(t *testing.T) {
	const name = "cluster 01+&=ü"
	for _, tt := range []struct {
		query   string
		deletes bool
	}{
		{"?confirm=cluster+01%2B%26%3D%C3%BC", true},   // URLSearchParams, as the SPA sends it
		{"?confirm=cluster%2001%2B%26%3D%C3%BC", true}, // the space percent-encoded instead
		{"?confirm=cluster%2001%2b%26%3d%c3%bc", true}, // lower-case escapes
		// The "+" of the name sent raw reads as a space, so this is
		// "cluster 01 &=ü" — refused, which shows the value is decoded
		// and then compared exactly rather than matched loosely.
		{"?confirm=cluster+01+%26%3D%C3%BC", false},
		{"?confirm=cluster01", false},
	} {
		t.Run(tt.query, func(t *testing.T) {
			app, store, _ := newClusterDeleteApp(t, clusterDeleteFixture{name: name})
			status, env := send(t, app, authedRequest(http.MethodDelete, pathPrefix+"clusters/"+testClusterID+tt.query))
			deleted, _ := store.state()
			switch {
			case tt.deletes && (status != fiber.StatusNoContent || !deleted):
				t.Errorf("answered %d %+v (deleted: %v), want 204 and the cluster deleted", status, env, deleted)
			case !tt.deletes && (status != fiber.StatusBadRequest || env.Message != clusterDeleteWrong || deleted):
				t.Errorf("answered %d %+v (deleted: %v), want the handler's 400 and nothing deleted", status, env, deleted)
			}
		})
	}
}

// TestClusterDeleteConfirmIsDeclared pins the declaration the SPA and every
// API client rely on: confirm is required, read from the query of a DELETE,
// and bounded like the name it must equal.
func TestClusterDeleteConfirmIsDeclared(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodDelete, clusterByID)
	prop, ok := e.Parameters["confirm"]
	if !ok {
		t.Fatal("DELETE /api/v1/clusters/:id declares no confirm parameter")
	}
	if prop.Optional || prop.Default != nil {
		t.Error("confirm must be required with no default: a request that does not carry it is the one to refuse")
	}
	if prop.Type != "string" || prop.MaxLength == nil || *prop.MaxLength != 255 || prop.MinLength != nil {
		t.Errorf("confirm is %+v, want a string capped at 255 characters with no minimum, like the name it must equal", prop)
	}
	if got := apischema.ResolveSource("confirm", prop, e.Method, e.pathParams); got != apischema.SourceQuery {
		t.Errorf("confirm is read from the %s, want the query", got)
	}
	if !strings.Contains(e.Description, "confirm") {
		t.Error("the endpoint's description, which /api/v1/api-docs renders, does not mention confirm")
	}
}
