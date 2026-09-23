package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// The tests in registry_ha_test.go swap the handler for a capture, which shows
// what the schema hands over but not what the handler does with it. This file
// keeps the REAL handler behind the REAL declaration and its permission gate,
// with stubAuth in place of the session check as elsewhere in this package, and
// puts stand-ins only at the two edges the handler reaches out to — Proxmox and
// the database — so a value can be followed from the request body to the form
// Proxmox receives and the audit row that records it.

// haCreateEncKey is the AES-256 key the stand-in cluster row's token secret is
// encrypted with. Fixed, and a test fixture only.
const haCreateEncKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// errHACreateUnexpectedQuery is what the stand-in database's Exec and Query
// answer for a statement the create handler is not expected to run.
var errHACreateUnexpectedQuery = errors.New("unexpected query")

// haCreatePVERequest is one request the stand-in Proxmox received.
type haCreatePVERequest struct {
	method string
	path   string
	form   url.Values
}

// haCreatePVE stands in for the cluster's Proxmox API: it records every
// request and answers each with an empty success envelope.
type haCreatePVE struct {
	mu   sync.Mutex
	seen []haCreatePVERequest
}

func (p *haCreatePVE) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("stand-in Proxmox: ParseForm: %v", err)
		}
		p.mu.Lock()
		p.seen = append(p.seen, haCreatePVERequest{method: r.Method, path: r.URL.Path, form: r.PostForm})
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// only returns the form of the one create the handler sent, failing unless
// exactly one POST to the resources collection arrived.
func (p *haCreatePVE) only(t *testing.T) url.Values {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.seen) != 1 {
		t.Fatalf("the handler made %d Proxmox calls, want exactly 1: %+v", len(p.seen), p.seen)
	}
	got := p.seen[0]
	if got.method != http.MethodPost || got.path != "/api2/json/cluster/ha/resources" {
		t.Fatalf("the handler sent %s %s, want POST /api2/json/cluster/ha/resources", got.method, got.path)
	}
	return got.form
}

// haCreateCacheQueries is the proxmox.CacheQueries a ClientCache needs: one
// cluster, pointed at the stand-in Proxmox.
type haCreateCacheQueries struct{ cluster db.Cluster }

func (q haCreateCacheQueries) GetCluster(_ context.Context, id uuid.UUID) (db.Cluster, error) {
	if id != q.cluster.ID {
		return db.Cluster{}, pgx.ErrNoRows
	}
	return q.cluster, nil
}

func (haCreateCacheQueries) GetPBSServer(context.Context, uuid.UUID) (db.PbsServer, error) {
	return db.PbsServer{}, pgx.ErrNoRows
}

// No node endpoints, so the client keeps the configured api_url — the stand-in
// — rather than failing over to a member address.
func (haCreateCacheQueries) ListNodeEndpoints(context.Context, uuid.UUID) ([]db.ListNodeEndpointsRow, error) {
	return nil, nil
}

// haCreateDBTX stands in for the database behind the handler. CreateResource
// touches it twice, both after Proxmox has answered: resolveSIDName's guest
// lookup, answered with no rows so the audit row carries no guest name, and
// AuditLog's insert, whose arguments are recorded. Any other Exec or Query
// fails.
type haCreateDBTX struct {
	mu     sync.Mutex
	audits [][]any
}

func (d *haCreateDBTX) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if !strings.Contains(sql, "INSERT INTO audit_log") {
		return pgconn.CommandTag{}, errHACreateUnexpectedQuery
	}
	d.mu.Lock()
	d.audits = append(d.audits, args)
	d.mu.Unlock()
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (*haCreateDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errHACreateUnexpectedQuery
}

func (*haCreateDBTX) QueryRow(context.Context, string, ...any) pgx.Row {
	return haCreateNoRow{}
}

// haCreateNoRow is a QueryRow result with nothing in it.
type haCreateNoRow struct{}

func (haCreateNoRow) Scan(...any) error { return pgx.ErrNoRows }

// onlyAudit returns the details of the one audit row the request wrote,
// decoded with UseNumber so a count reads back as the digits that were stored.
// It fails unless exactly one row was written and it is the create's.
//
// The argument positions are InsertAuditLog's (internal/db/generated): cluster,
// user, resource type, resource id, action, details. A regenerated query that
// moved them fails here, on a type or a value, rather than reading the wrong one.
func (d *haCreateDBTX) onlyAudit(t *testing.T) map[string]any {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.audits) != 1 {
		t.Fatalf("the request wrote %d audit rows, want exactly 1", len(d.audits))
	}
	args := d.audits[0]
	if len(args) != 6 {
		t.Fatalf("the audit insert took %d arguments, want InsertAuditLog's 6", len(args))
	}
	resourceType, _ := args[2].(string)
	resourceID, _ := args[3].(string)
	action, _ := args[4].(string)
	if resourceType != "ha_resource" || resourceID != "vm:109" || action != "created" {
		t.Fatalf("the audit row is (%q, %q, %q), want (ha_resource, vm:109, created)", resourceType, resourceID, action)
	}
	raw, ok := args[5].(json.RawMessage)
	if !ok {
		t.Fatalf("the audit details argument is %T, want json.RawMessage", args[5])
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var details map[string]any
	if err := dec.Decode(&details); err != nil {
		t.Fatalf("the audit details are not a JSON object (%s): %v", raw, err)
	}
	return details
}

// newHACreateApp mounts the real POST .../ha/resources declaration, with its
// real permission, on a fresh app — carrying the real HAHandler.CreateResource,
// wired to the two stand-ins, in place of the one newRouteStubServer bound to an
// empty HAHandler.
func newHACreateApp(t *testing.T) (*fiber.App, *haCreatePVE, *haCreateDBTX) {
	t.Helper()
	pve := &haCreatePVE{}
	baseURL := pve.serve(t)

	encrypted, err := crypto.Encrypt("token-secret-value", haCreateEncKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	cache := proxmox.NewClientCache(haCreateCacheQueries{cluster: db.Cluster{
		ID:                   uuid.MustParse(testClusterID),
		Name:                 "cluster01",
		ApiUrl:               baseURL,
		TokenID:              "user@pam!test",
		TokenSecretEncrypted: encrypted,
		IsActive:             true,
	}}, haCreateEncKey, nil, nil)

	dbtx := &haCreateDBTX{}
	e := declaredEndpoint(t, fiber.MethodPost, haScope+"/resources")
	e.Handler = handlers.NewHAHandler(db.New(dbtx), haCreateEncKey, nil).CreateResource

	// stubAuth sets the user AuditLog needs — without one it writes nothing —
	// and grants the declared manage:ha. The cache is what CreateProxmoxClient
	// reads the cluster through, so no database is needed for the client.
	authed := stubAuth(map[string]bool{"manage:ha": true})
	auth := func(c fiber.Ctx) error {
		handlers.SetProxmoxCacheLocal(c, cache)
		return authed(c)
	}
	return newRegistryApp(t, auth, e), pve, dbtx
}

// TestHACreateResourceKeepsAnExplicitZeroRetryCount follows max_restart and
// max_relocate from the request body, through the declaration and the handler,
// to the form Proxmox receives and the audit row.
//
// Proxmox defaults both to 1 but applies that only to a key that is absent (see
// CreateHAResource in internal/proxmox/client_ha.go), so "omitted" and "0" are
// different requests and have to stay different at every hop. The handler used
// to read both with p.Int, which returns 0 for either, and the client sent a
// count only when it was > 0 — so an explicit 0 reached Proxmox as no key at
// all and became 1, while the audit row recorded neither.
func TestHACreateResourceKeepsAnExplicitZeroRetryCount(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		// want is sent in the form AND recorded in the audit row, as these
		// digits; absent is in neither.
		want   map[string]string
		absent []string
	}{
		{
			name: "an explicit 0 for both",
			body: `{"sid":"vm:109","max_restart":0,"max_relocate":0}`,
			want: map[string]string{"max_restart": "0", "max_relocate": "0"},
		},
		{
			name:   "both omitted",
			body:   `{"sid":"vm:109"}`,
			absent: []string{"max_restart", "max_relocate"},
		},
		{
			name: "positive counts",
			body: `{"sid":"vm:109","max_restart":3,"max_relocate":2}`,
			want: map[string]string{"max_restart": "3", "max_relocate": "2"},
		},
		{
			// Each count follows its own key: a 0 on one must neither bring
			// the other along nor land under the other's name.
			name:   "a 0 for max_restart alone",
			body:   `{"sid":"vm:109","max_restart":0}`,
			want:   map[string]string{"max_restart": "0"},
			absent: []string{"max_relocate"},
		},
		{
			name:   "a 0 for max_relocate alone",
			body:   `{"sid":"vm:109","max_relocate":0}`,
			want:   map[string]string{"max_relocate": "0"},
			absent: []string{"max_restart"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app, pve, dbtx := newHACreateApp(t)
			req := jsonRequest(http.MethodPost, haRoute(haScope+"/resources"), tt.body)
			req.Header.Set("X-Test-User", "yes")
			if status, env := send(t, app, req); status != fiber.StatusCreated {
				t.Fatalf("status = %d (%q), want 201", status, env.Message)
			}

			// Both readers first establish that what they are reading is the
			// create's own form and row, or an absence below would pass for
			// want of anything to look at.
			form := pve.only(t)
			if got := form.Get("sid"); got != "vm:109" {
				t.Fatalf("sid reached Proxmox as %q, want vm:109", got)
			}
			details := dbtx.onlyAudit(t)
			if details["sid"] != "vm:109" {
				t.Fatalf("the audit row's sid is %v, want vm:109", details["sid"])
			}

			for key, want := range tt.want {
				if got, ok := form[key]; !ok || len(got) != 1 || got[0] != want {
					t.Errorf("%s reached Proxmox as %q (present=%v), want [%q]", key, got, ok, want)
				}
				if got, ok := details[key].(json.Number); !ok || got.String() != want {
					t.Errorf("the audit row records %s as %v, want %s", key, details[key], want)
				}
			}
			for _, key := range tt.absent {
				if _, ok := form[key]; ok {
					t.Errorf("%s reached Proxmox as %q for a body that omitted it; Proxmox's default "+
						"applies only to a key it is not sent", key, form.Get(key))
				}
				if v, ok := details[key]; ok {
					t.Errorf("the audit row records %s as %v for a body that omitted it", key, v)
				}
			}
		})
	}
}
