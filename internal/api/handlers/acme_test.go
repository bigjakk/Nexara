package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// The `delete` list used to be pinned here as a Fiber-binder test: nothing else
// proved that the list survived the bind, and a dropped one would have sent a
// perfectly valid request that cleared nothing.
//
// SetNodeACMEConfig no longer binds a struct — the route is declared
// (internal/api/registry_acme.go) and the list arrives through the parameter
// schema — so that link has moved with it.
// TestNodeACMEDeleteListReachesTheHandlerAsAList in
// internal/api/registry_acme_test.go drives the REAL declaration end to end and
// asserts the same thing about the same three fields, including that `digest`
// round-trips from the GET into the PUT.

// mapNodeConfigError turns PVE's digest-mismatch die into a 409. Everything
// else has to keep the status mapProxmoxError gave it — the phrase list is the
// only thing separating "someone else edited this node" from every other way
// the write can fail, and answering 409 to an unrelated failure would tell the
// operator to reload when reloading will not help.
func TestMapNodeConfigError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{
			name: "digest mismatch is a conflict",
			err: &proxmox.APIError{
				StatusCode: 500,
				Message:    "detected modified configuration - file changed by other user? Try again.",
			},
			want: fiber.StatusConflict,
		},
		{
			// The client refused it; it never reached the node.
			name: "an undeletable key stays a 400",
			err:  proxmox.ErrInvalidInput,
			want: fiber.StatusBadRequest,
		},
		{
			name: "an unreachable node stays a 502",
			err:  proxmox.ErrConnectionFailed,
			want: fiber.StatusBadGateway,
		},
		{
			name: "a permission failure stays a 403",
			err:  proxmox.ErrForbidden,
			want: fiber.StatusForbidden,
		},
		{
			name: "an unrelated PVE die stays a 502",
			err: &proxmox.APIError{
				StatusCode: 500,
				Message:    "unable to parse acme domain config",
			},
			want: fiber.StatusBadGateway,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fe *fiber.Error
			err := mapNodeConfigError(tt.err)
			if !errors.As(err, &fe) {
				t.Fatalf("mapNodeConfigError(%v) = %v, want a *fiber.Error", tt.err, err)
			}
			if fe.Code != tt.want {
				t.Errorf("status = %d (%q), want %d", fe.Code, fe.Message, tt.want)
			}
		})
	}

	if err := mapNodeConfigError(nil); err != nil {
		t.Errorf("mapNodeConfigError(nil) = %v, want nil", err)
	}
}

// --- POST /clusters/:cluster_id/acme/accounts: what the audit trail names ---

// acmeAccountCreateMirror mirrors createACMEAccountParams from
// internal/api/registry_acme.go: cluster_id from the shared standard option and
// the name's pattern from the shared catalogue, rather than either being
// retyped. The route's real vocabulary is then what these cases are validated
// against, and in particular "" stays acceptable for `name` because
// pve-object-id-or-empty is what the declaration reaches for — the whole reason
// an empty name can reach the handler at all.
func acmeAccountCreateMirror(t *testing.T) apischema.Properties {
	t.Helper()
	return compiledMirror(t, apischema.Properties{
		"cluster_id": apischema.StdOption("cluster-id"),
		"name": {
			Type:      apischema.String,
			Optional:  true,
			Pattern:   apischema.Rule("pve-object-id-or-empty"),
			MaxLength: apischema.Ptr(64),
		},
		"contact": {
			Type:      apischema.String,
			MinLength: apischema.Ptr(1),
			MaxLength: apischema.Ptr(1024),
		},
		// Declared because the handler READS them: p.String panics on an
		// undeclared key, so a mirror missing either one never reaches the
		// TrackTask call at all. Their pattern is emptyOrACMEURL, a constant
		// in package api rather than a catalogue rule, and package api imports
		// this package — so it is retyped here the way ipSetEntryMirror
		// retypes the cidr pattern. registry_acme_test.go is what holds the
		// declaration itself to that constant.
		"directory": {Type: apischema.String, Optional: true,
			Pattern: `^$|^https?://`, MaxLength: apischema.Ptr(2048)},
		"tos_url": {Type: apischema.String, Optional: true,
			Pattern: `^$|^https?://`, MaxLength: apischema.Ptr(2048)},
	})
}

// acmeCreateHarness is one wired-up POST .../acme/accounts pipeline: a real
// Fiber app carrying the real ACMEHandler, a real proxmox.Client pointed at a
// capture server that answers with a UPID, and a DBTX that records every
// statement the handler sends.
type acmeCreateHarness struct {
	app       *fiber.App
	clusterID uuid.UUID
	dbtx      *captureDBTX
	// pubsub carries everything the request publishes. A real Publisher over
	// miniredis, not the nil one the sibling harnesses pass: ClusterEvent
	// returns on a nil receiver (internal/events/publisher.go), so with nil
	// the fourth sink is a no-op and reverting it to req.Name would leave
	// this file green.
	pubsub *redis.PubSub
}

func newACMECreateHarness(t *testing.T) *acmeCreateHarness {
	t.Helper()

	baseURL, _ := newProxmoxCaptureServer(t)
	clusterID := uuid.New()

	encrypted, err := crypto.Encrypt("token-secret-value", pathParamEncKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	cache := proxmox.NewClientCache(pathParamCacheQueries{cluster: db.Cluster{
		ID:                   clusterID,
		Name:                 "cluster01",
		ApiUrl:               baseURL,
		TokenID:              "user@pam!test",
		TokenSecretEncrypted: encrypted,
		IsActive:             true,
	}}, pathParamEncKey, nil, nil)

	// PSubscribe rather than a computed channel name: events.publishChannel
	// splits audit entries onto their own room, and re-deriving that split
	// here would be a second copy of the routing rule to keep in step.
	rdb := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	pubsub := rdb.PSubscribe(context.Background(), "nexara:*")
	t.Cleanup(func() { _ = pubsub.Close() })
	// Wait for the subscription to be live, or the first events race it.
	if _, err := pubsub.Receive(context.Background()); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	eventPub := events.NewPublisher(rdb, slog.New(slog.NewTextHandler(io.Discard, nil)))

	dbtx := &captureDBTX{}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		SetProxmoxCacheLocal(c, cache)
		// Load-bearing, and the one thing newPathParamHarness deliberately
		// does NOT do. AuditLog returns at its first line without a user_id
		// local, and TrackTask returns before InsertTaskHistory for the same
		// reason, so every assertion below would run against an empty slice
		// and pass no matter what the handler recorded.
		c.Locals("user_id", uuid.New())
		return c.Next()
	})
	handler := NewACMEHandler(db.New(dbtx), pathParamEncKey, eventPub)
	app.Post("/api/v1/clusters/:cluster_id/acme/accounts",
		withRequestParams(t, acmeAccountCreateMirror(t), []string{"cluster_id"}, handler.CreateAccount))

	return &acmeCreateHarness{app: app, clusterID: clusterID, dbtx: dbtx, pubsub: pubsub}
}

// send drives one create and fails unless Proxmox was reached and answered.
func (h *acmeCreateHarness) send(t *testing.T, body string) {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/clusters/"+h.clusterID.String()+"/acme/accounts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusCreated {
		t.Fatalf("POST .../acme/accounts with %s: status = %d, want 201 — the request never "+
			"reached the recording path, so nothing below is being tested", body, resp.StatusCode)
	}
}

// auditRow returns the one audit row the create wrote, with its details decoded.
func (h *acmeCreateHarness) auditRow(t *testing.T) (db.InsertAuditLogParams, map[string]any) {
	t.Helper()

	rows := h.dbtx.auditInserts(t)
	if len(rows) != 1 {
		t.Fatalf("the handler wrote %d audit rows, want exactly 1 — no audit row written means "+
			"every assertion here is vacuous", len(rows))
	}
	var details map[string]any
	if err := json.Unmarshal(rows[0].Details, &details); err != nil {
		t.Fatalf("audit details are not a JSON object: %v (%s)", err, rows[0].Details)
	}
	return rows[0], details
}

// taskDescription reads the description off the task_history insert.
//
// auditInserts cannot see this row because it filters on the STATEMENT TEXT
// ("INSERT INTO audit_log"), not on which driver method carried it —
// captureDBTX.record is called from Exec, Query and QueryRow alike, so the
// task insert is in calls either way. What InsertTaskHistory being a :one
// does change is that it arrives via QueryRow, which hands back
// failRow{err: errCaptured} while captureDBTX.row is nil; the handler
// discards that with `_, _ =`, which is why the args are captured at all.
//
// Matched on the statement and read positionally: description is arg 3 of
// (cluster_id, user_id, upid, description, status, node, task_type).
func (h *acmeCreateHarness) taskDescription(t *testing.T) string {
	t.Helper()

	var found []capturedQuery
	for _, q := range h.dbtx.calls {
		if strings.Contains(q.sql, "INTO task_history") {
			found = append(found, q)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the handler wrote %d task_history rows, want exactly 1 — no task row written "+
			"means the description assertion is vacuous", len(found))
	}
	if len(found[0].args) != 7 {
		t.Fatalf("task_history insert got %d args, want 7: %#v", len(found[0].args), found[0].args)
	}
	return argAs[string](t, found[0].args, 3)
}

// eventResourceID returns the resource id of the acme_change event the create
// published — the fourth sink, and the one that sits outside the TrackTask
// struct literal, so the one most easily edited on its own.
//
// The create publishes THREE events and the Kind is what tells them apart.
// Filtering on resource_type and action alone is not enough and was wrong here
// until a mutation caught it: AuditLog publishes its audit_entry with the same
// "acme_account"/"created" pair, fed from TrackTaskParams.ResourceID, and it
// goes out FIRST — so the read returned the sink it was not testing and
// reverting the real one left this file green.
func (h *acmeCreateHarness) eventResourceID(t *testing.T) string {
	t.Helper()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg, ok := <-h.pubsub.Channel():
			if !ok {
				t.Fatal("the event channel closed before an acme_account event arrived")
			}
			var ev events.Event
			if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
				t.Fatalf("event payload is not an events.Event: %v (%s)", err, msg.Payload)
			}
			if ev.Kind == events.KindACMEChange && ev.ResourceType == "acme_account" &&
				ev.Action == "created" {
				return ev.ResourceID
			}
		case <-deadline:
			t.Fatal("no acme_change/acme_account/created event was published — the WS sink " +
				"assertion is vacuous")
		}
	}
}

// TestACMEAccountCreateRecordsTheNameTheAccountGetsNotTheOneSent is the proof
// that the audit trail names an account that exists.
//
// An omitted or empty name is valid input: registry_acme.go declares `name`
// optional under the pve-object-id-or-empty rule, and proxmox.CreateACMEAccount
// sets the form key only `if params.Name != ""`, so an empty name genuinely
// reaches Proxmox as no name at all and the account is registered under
// "default" (PVE/API2/ACMEAccount.pm, register_account:
// `extract_param($param, 'name') // 'default'`). Every sink here used to record
// the caller's "", naming nothing, in a row view:audit shows to every Viewer.
//
// ALL FOUR sinks are asserted per case on purpose: the audit row's resource_id,
// its details.name, the task_history description and the WS event's resource
// id. They are separately mutable lines fed from one value, and a test that
// checked a subset would let the rest regress silently — which is exactly what
// happened to the WS event while the harness passed a nil Publisher.
func TestACMEAccountCreateRecordsTheNameTheAccountGetsNotTheOneSent(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantName      string
		wantDesc      string
		wantDefaulted bool
	}{
		{
			name:          "name omitted",
			body:          `{"contact":"admin@example.com"}`,
			wantName:      "default",
			wantDesc:      "Create ACME account default",
			wantDefaulted: true,
		},
		{
			// Distinct from the case above at the wire: "" is a value
			// apischema does not fall back to a default for, so it reaches
			// the handler as a supplied empty string.
			name:          "name explicitly empty",
			body:          `{"name":"","contact":"admin@example.com"}`,
			wantName:      "default",
			wantDesc:      "Create ACME account default",
			wantDefaulted: true,
		},
		{
			// The case the name_defaulted flag exists for, and the only input
			// that separates `req.Name == ""` from `name == default`: the
			// caller ASKED for the name Proxmox would have chosen anyway.
			// registry_acme_test.go proves the real route accepts it.
			name:          "name explicitly default",
			body:          `{"name":"default","contact":"admin@example.com"}`,
			wantName:      "default",
			wantDesc:      "Create ACME account default",
			wantDefaulted: false,
		},
		{
			// The control that keeps the fix from becoming "always say
			// default": a chosen name must survive untouched.
			name:          "name populated",
			body:          `{"name":"letsencrypt-prod","contact":"admin@example.com"}`,
			wantName:      "letsencrypt-prod",
			wantDesc:      "Create ACME account letsencrypt-prod",
			wantDefaulted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newACMECreateHarness(t)
			h.send(t, tt.body)

			row, details := h.auditRow(t)
			if row.ResourceID != tt.wantName {
				t.Errorf("audit resource_id = %q, want %q — the row names an account that does "+
					"not exist", row.ResourceID, tt.wantName)
			}
			if got, _ := details["name"].(string); got != tt.wantName {
				t.Errorf("audit details.name = %v, want %q", details["name"], tt.wantName)
			}
			if got, ok := details["name_defaulted"].(bool); !ok || got != tt.wantDefaulted {
				t.Errorf("audit details.name_defaulted = %v, want %t — without it the row cannot "+
					"say whether Proxmox chose the name or the caller did",
					details["name_defaulted"], tt.wantDefaulted)
			}
			if got := h.taskDescription(t); got != tt.wantDesc {
				t.Errorf("task_history description = %q, want %q", got, tt.wantDesc)
			}
			if got := h.eventResourceID(t); got != tt.wantName {
				t.Errorf("WS event resource id = %q, want %q — the live feed names an account "+
					"that does not exist", got, tt.wantName)
			}
		})
	}
}
