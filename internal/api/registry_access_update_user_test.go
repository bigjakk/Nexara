package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
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
)

// The unit tests of accessUserUpdateDetails (internal/api/handlers/access_test.go)
// show what the builder makes of a request. They pass with the builder deleted
// from the handler, and they cannot see where `force` comes from. This file
// keeps the REAL PUT .../access/users/:userid declaration, its permission gate
// and the real AccessHandler.UpdateUser, with stand-ins only at the two edges
// the handler reaches out to, Proxmox and the database, so a value can be
// followed from the request to the form Proxmox receives and the audit row
// every Viewer can read.

// accessUpdateEncKey is the AES-256 key the stand-in cluster row's token secret
// is encrypted with. Fixed, and a test fixture only.
const accessUpdateEncKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// accessUpdateSelf is the account Nexara authenticates as in these tests: the
// user half of the stand-in cluster row's token id.
const accessUpdateSelf = "nexara@pve"

var errAccessUpdateUnexpected = errors.New("unexpected statement")

// accessUpdatePVERequest is one request the stand-in Proxmox received.
type accessUpdatePVERequest struct {
	method string
	path   string
	form   url.Values
}

// accessUpdatePVE stands in for the cluster's Proxmox API: it records every
// request and answers each with an empty success envelope, unless reply gave
// the path a body or refuse a status too.
type accessUpdatePVE struct {
	mu       sync.Mutex
	seen     []accessUpdatePVERequest
	replies  map[string]string
	statuses map[string]int
}

// reply makes the stand-in answer requests for path, as the server sees it
// decoded (/api2/json/access/users), with body in place of the empty envelope.
func (p *accessUpdatePVE) reply(path, body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.replies == nil {
		p.replies = map[string]string{}
	}
	p.replies[path] = body
}

// refuse is reply with an HTTP status: a Proxmox that will not do what it was
// asked, so that a test can follow what the handler makes of the refusal.
func (p *accessUpdatePVE) refuse(path string, status int, body string) {
	p.reply(path, body)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.statuses == nil {
		p.statuses = map[string]int{}
	}
	p.statuses[path] = status
}

func (p *accessUpdatePVE) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("stand-in Proxmox: ParseForm: %v", err)
		}
		p.mu.Lock()
		p.seen = append(p.seen, accessUpdatePVERequest{method: r.Method, path: r.URL.Path, form: r.PostForm})
		body, ok := p.replies[r.URL.Path]
		status := p.statuses[r.URL.Path]
		p.mu.Unlock()
		if !ok {
			body = `{"data":null}`
		}
		w.Header().Set("Content-Type", "application/json")
		if status != 0 {
			w.WriteHeader(status)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (p *accessUpdatePVE) requests() []accessUpdatePVERequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]accessUpdatePVERequest(nil), p.seen...)
}

// accessUpdateDB stands in for the database behind the handler. UpdateUser reads
// the cluster row for the self-credential guard on an unforced edit the guard
// covers, and again to build its Proxmox client, and it writes one audit row
// after Proxmox has answered. Any other statement fails, so a handler that starts
// reading something else fails here instead of being handed a row that happens to
// satisfy it.
//
// The two cluster reads are told apart by their order, which is what
// failClusterRead and clusterReadCount take: the guard's is the first when it
// runs at all, and a forced request has the client's alone.
type accessUpdateDB struct {
	mu      sync.Mutex
	cluster db.Cluster
	audits  [][]any
	// clusterReads counts the GetCluster reads made, and clusterFailures maps the
	// number of a read, the first being 1, to the error it fails with.
	clusterReads    int
	clusterFailures map[int]error
}

func (d *accessUpdateDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "-- name: GetCluster :one") && len(args) == 1 && args[0] == d.cluster.ID {
		d.mu.Lock()
		d.clusterReads++
		err := d.clusterFailures[d.clusterReads]
		d.mu.Unlock()
		return accessUpdateRow{cluster: d.cluster, err: err}
	}
	return accessUpdateRow{err: fmt.Errorf("%w: %.60s", errAccessUpdateUnexpected, sql)}
}

// failClusterRead makes the nth GetCluster read, the first being 1, fail with
// err, as the database does for a cluster that is not there (pgx.ErrNoRows) or
// for a connection that has gone.
func (d *accessUpdateDB) failClusterRead(n int, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.clusterFailures == nil {
		d.clusterFailures = map[int]error{}
	}
	d.clusterFailures[n] = err
}

// clusterReadCount is how many GetCluster reads the handler has made.
func (d *accessUpdateDB) clusterReadCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.clusterReads
}

func (*accessUpdateDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("%w: %.60s", errAccessUpdateUnexpected, sql)
}

func (d *accessUpdateDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if !strings.Contains(sql, "INSERT INTO audit_log") {
		return pgconn.CommandTag{}, fmt.Errorf("%w: %.60s", errAccessUpdateUnexpected, sql)
	}
	d.mu.Lock()
	d.audits = append(d.audits, args)
	d.mu.Unlock()
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (d *accessUpdateDB) auditRows() [][]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([][]any(nil), d.audits...)
}

// accessAuditRow is an audit row the stand-in database recorded, its arguments
// named.
type accessAuditRow struct {
	resourceType, resourceID, action string
	details                          json.RawMessage
}

// oneAccessAuditRow returns the audit row a request wrote, and fails unless it
// wrote exactly one.
//
// The argument positions are InsertAuditLog's (internal/db/generated): cluster,
// user, resource type, resource id, action, details. A regenerated query that
// moved them fails here on a type or a value rather than reading the wrong one.
func oneAccessAuditRow(t *testing.T, rows [][]any) accessAuditRow {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("the request wrote %d audit rows, want exactly 1", len(rows))
	}
	args := rows[0]
	if len(args) != 6 {
		t.Fatalf("the audit insert took %d arguments, want InsertAuditLog's 6", len(args))
	}
	details, ok := args[5].(json.RawMessage)
	if !ok {
		t.Fatalf("the audit details argument is %T, want json.RawMessage", args[5])
	}
	resourceType, _ := args[2].(string)
	resourceID, _ := args[3].(string)
	action, _ := args[4].(string)
	return accessAuditRow{resourceType: resourceType, resourceID: resourceID, action: action, details: details}
}

// accessUpdateRow replays the cluster row through pgx.Row positionally, in the
// struct's field order, which is the order GetCluster scans in. A destination
// whose type differs from the field's fails the scan, which catches most
// reorderings of the query but not a swap of two fields of the same type.
type accessUpdateRow struct {
	cluster db.Cluster
	err     error
}

func (r accessUpdateRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	row := reflect.ValueOf(r.cluster)
	if len(dest) != row.NumField() {
		return fmt.Errorf("scan got %d destinations, want %d", len(dest), row.NumField())
	}
	for i, d := range dest {
		ptr := reflect.ValueOf(d)
		if ptr.Kind() != reflect.Pointer || ptr.Elem().Type() != row.Field(i).Type() {
			return fmt.Errorf("scan destination %d is %T, want *%s", i, d, row.Field(i).Type())
		}
		ptr.Elem().Set(row.Field(i))
	}
	return nil
}

// newAccessStandIns builds what a route test here mounts the real AccessHandler
// on: the stand-in Proxmox, and the stand-in database holding the cluster row
// that authenticates as accessUpdateSelf and points at it.
func newAccessStandIns(t *testing.T) (*accessUpdatePVE, *accessUpdateDB, *handlers.AccessHandler) {
	t.Helper()
	pve := &accessUpdatePVE{}
	secret, err := crypto.Encrypt("token-secret-value", accessUpdateEncKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	store := &accessUpdateDB{cluster: db.Cluster{
		ID:                   uuid.MustParse(testClusterID),
		Name:                 "cluster01",
		ApiUrl:               pve.serve(t),
		TokenID:              accessUpdateSelf + "!api",
		TokenSecretEncrypted: secret,
		IsActive:             true,
	}}
	return pve, store, handlers.NewAccessHandler(db.New(store), accessUpdateEncKey, nil)
}

// newAccessUpdateApp mounts the real PUT .../access/users/:userid declaration,
// with its real permission, carrying the real AccessHandler.UpdateUser wired to
// the two stand-ins in place of the one newRouteStubServer bound to an empty
// AccessHandler. The cluster it finds authenticates as accessUpdateSelf.
func newAccessUpdateApp(t *testing.T) (*fiber.App, *accessUpdatePVE, *accessUpdateDB) {
	t.Helper()
	pve, store, h := newAccessStandIns(t)

	e := declaredEndpoint(t, fiber.MethodPut, accessScope+"/users/:userid")
	e.Handler = h.UpdateUser

	// stubAuth sets the user AuditLog needs — without one it writes nothing —
	// and grants the declared manage:access.
	return newRegistryApp(t, stubAuth(map[string]bool{"manage:" + handlers.AccessResource: true}), e), pve, store
}

// accessUpdateRequest is the PUT a client sends to edit user, given as the path
// segment a client sends: percent-encoded.
func accessUpdateRequest(user, query, body string) *http.Request {
	target := strings.Replace(accessRoute(accessScope+"/users/:userid"), testAccessUserID, user, 1) + query
	req := jsonRequest(http.MethodPut, target, body)
	req.Header.Set("X-Test-User", "yes")
	return req
}

// The values of the fields an audit row must never carry, each recognisable in
// the row whatever key it might be recorded under. The group list is here too:
// the row says a membership was set, not what it became.
const accessUpdateEverything = `{
	"enable": false, "expire": 1767225600, "groups": "sentinel-group-list", "append": true,
	"comment": "sentinel-comment", "email": "sentinel-email@example.com",
	"firstname": "sentinel-first", "lastname": "sentinel-last", "keys": "sentinel-keys"
}`

var accessUpdateEverythingForm = url.Values{
	"enable":    {"0"},
	"expire":    {"1767225600"},
	"groups":    {"sentinel-group-list"},
	"append":    {"1"},
	"comment":   {"sentinel-comment"},
	"email":     {"sentinel-email@example.com"},
	"firstname": {"sentinel-first"},
	"lastname":  {"sentinel-last"},
	"keys":      {"sentinel-keys"},
}

// TestAccessUpdateUserAuditRow follows a user edit from the request, through the
// declaration and the handler, to the form Proxmox receives and the audit row.
//
// The row is what every Viewer reads (view:audit), so this is where the promise
// that it carries the enable, expire and groups an edit set, and whether the
// edit was sent with force, and no e-mail, comment, name or two-factor keys, is
// kept against the code that writes it. The first case sends every field, and
// asserts they reached Proxmox, so that their absence from the row is an absence
// and not an edit that never carried them.
//
// The guard is live in this harness: the second case is the same edit without
// force, refused by it. Without that case a stand-in whose cluster row did not
// name the account would let every edit through, and the forced flag in the first
// case would prove nothing.
//
// The last case has Proxmox refuse the edit: the caller gets an error and
// nothing is audited, because nothing changed. The other cases' stand-in always
// succeeds, so without it a row written ahead of the Proxmox call would pass.
func TestAccessUpdateUserAuditRow(t *testing.T) {
	tests := []struct {
		name  string
		user  string // as the client sends it in the path
		query string
		body  string

		wantStatus int
		// wantForm is the form the one Proxmox request carried. Nil marks a
		// refused edit, which must reach Proxmox not at all and write no audit
		// row; wantAudit and wantID are then unused.
		wantForm url.Values
		// wantAudit is the exact details of the one audit row, as stored.
		wantAudit string
		// wantID is the account the edit names: the row's resource id and the
		// last segment of the Proxmox path.
		wantID string
		// pveStatus, when set, is the HTTP status the stand-in Proxmox refuses the
		// edit with, and pveBody what it says. The edit still reaches Proxmox, and
		// wantForm is what it carried. The caller must get an error, but which one
		// is the house mappers' to decide (a 404 would be as right as today's 502
		// for a missing user), so wantStatus is unused; and no audit row may be
		// written, so wantAudit is too.
		pveStatus int
		pveBody   string
	}{
		{
			name:       "a forced edit of the account Nexara uses, with every field",
			user:       testAccessUserID,
			query:      "?force=true",
			body:       accessUpdateEverything,
			wantStatus: fiber.StatusOK,
			wantForm:   accessUpdateEverythingForm,
			wantAudit:  `{"enable":false,"expire":1767225600,"forced":true,"groups_changed":true,"userid":"nexara@pve"}`,
			wantID:     accessUpdateSelf,
		},
		{
			name:       "the same edit without force is refused and leaves no trace",
			user:       testAccessUserID,
			body:       accessUpdateEverything,
			wantStatus: fiber.StatusConflict,
		},
		{
			name:       "force on an edit the guard does not cover overrides nothing",
			user:       testAccessUserID,
			query:      "?force=true",
			body:       `{"comment":"sentinel-comment"}`,
			wantStatus: fiber.StatusOK,
			wantForm:   url.Values{"comment": {"sentinel-comment"}},
			wantAudit:  `{"forced":false,"userid":"nexara@pve"}`,
			wantID:     accessUpdateSelf,
		},
		{
			name:       "clearing the expiry needs no force and is recorded as 0",
			user:       testAccessUserID,
			body:       `{"expire":0}`,
			wantStatus: fiber.StatusOK,
			wantForm:   url.Values{"expire": {"0"}},
			wantAudit:  `{"expire":0,"forced":false,"userid":"nexara@pve"}`,
			wantID:     accessUpdateSelf,
		},
		{
			name:       "another account is edited without force and the row names it",
			user:       "alice%40pve",
			body:       `{"enable":false}`,
			wantStatus: fiber.StatusOK,
			wantForm:   url.Values{"enable": {"0"}},
			wantAudit:  `{"enable":false,"forced":false,"userid":"alice@pve"}`,
			wantID:     "alice@pve",
		},
		{
			// force is recorded as sent, not as a verdict: this is not the
			// account Nexara uses, the guard would never have refused the edit,
			// and the row still says forced. It is also the case where force is
			// true and append is not, so a handler that hands the builder the
			// wrong flag cannot pass by both happening to be true.
			name:       "force on a guarded edit of another account is recorded whether or not the guard would have refused it",
			user:       "alice%40pve",
			query:      "?force=true",
			body:       `{"enable":false}`,
			wantStatus: fiber.StatusOK,
			wantForm:   url.Values{"enable": {"0"}},
			wantAudit:  `{"enable":false,"forced":true,"userid":"alice@pve"}`,
			wantID:     "alice@pve",
		},
		{
			// Proxmox says no, as it does for an account that does not exist, so
			// nothing was edited and nothing may be recorded as if it had been.
			name:      "an edit Proxmox refuses answers with an error and writes no audit row",
			user:      "alice%40pve",
			body:      `{"comment":"sentinel-comment"}`,
			pveStatus: http.StatusInternalServerError,
			pveBody:   `{"data":null,"message":"no such user ('alice@pve')\n"}`,
			wantForm:  url.Values{"comment": {"sentinel-comment"}},
			wantID:    "alice@pve",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, pve, store := newAccessUpdateApp(t)
			if tt.pveStatus != 0 {
				pve.refuse("/api2/json/access/users/"+tt.wantID, tt.pveStatus, tt.pveBody)
			}
			status, env := send(t, app, accessUpdateRequest(tt.user, tt.query, tt.body))
			if tt.pveStatus != 0 {
				if status < fiber.StatusBadRequest {
					t.Fatalf("Proxmox refused the edit and the caller got %d (%q), want an error", status, env.Message)
				}
			} else if status != tt.wantStatus {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, tt.wantStatus)
			}

			sent, rows := pve.requests(), store.auditRows()
			if tt.wantStatus == fiber.StatusConflict {
				// The guard's own refusal, not some other 409: it names the way
				// past it.
				if !strings.Contains(env.Message, "force=true") {
					t.Errorf("the refusal does not name force=true: %q", env.Message)
				}
			}
			if tt.wantForm == nil {
				if len(sent) != 0 {
					t.Errorf("the refused edit reached Proxmox: %+v", sent)
				}
				if len(rows) != 0 {
					t.Errorf("the refused edit wrote %d audit row(s), want none", len(rows))
				}
				return
			}

			// What Proxmox received is established first: it is what makes the
			// row's silence about a field mean something.
			if len(sent) != 1 {
				t.Fatalf("the handler made %d Proxmox calls, want exactly 1: %+v", len(sent), sent)
			}
			wantPath := "/api2/json/access/users/" + tt.wantID
			if sent[0].method != http.MethodPut || sent[0].path != wantPath {
				t.Fatalf("the handler sent %s %s, want PUT %s", sent[0].method, sent[0].path, wantPath)
			}
			if !reflect.DeepEqual(sent[0].form, tt.wantForm) {
				t.Errorf("Proxmox received the form %v, want %v", sent[0].form, tt.wantForm)
			}

			if tt.pveStatus != 0 {
				if len(rows) != 0 {
					t.Errorf("Proxmox refused the edit and the handler wrote %d audit row(s), want none", len(rows))
				}
				return
			}
			row := oneAccessAuditRow(t, rows)
			if row.resourceType != "pve_user" || row.resourceID != tt.wantID || row.action != "updated" {
				t.Errorf("the audit row is (%q, %q, %q), want (pve_user, %q, updated)",
					row.resourceType, row.resourceID, row.action, tt.wantID)
			}
			if string(row.details) != tt.wantAudit {
				t.Errorf("the audit details are %s, want %s", row.details, tt.wantAudit)
			}
		})
	}
}
