package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// The `delete` list reaches the handler as a list through the parameter schema
// (internal/api/registry_acme.go): TestNodeACMEDeleteListReachesTheHandlerAsAList in
// internal/api/registry_acme_test.go drives the REAL declaration end to end.

// TestMapNodeConfigError: mapNodeConfigError turns PVE's digest-mismatch die into a
// 409, and pveproxy's refusal of a body over its post limit into a 413. Everything else
// keeps the status mapProxmoxError gave it — the phrase list is the only thing
// separating "someone else edited this node" from every other way the write can fail,
// and answering 409 to an unrelated failure would tell the operator to reload when
// reloading will not help; and 501 is also how pveproxy says "no such uri", which is
// not a request that is too large.
func TestMapNodeConfigError(t *testing.T) {
	apiErr := func(status int, message string) error { return &proxmox.APIError{StatusCode: status, Message: message} }
	const stale = "detected modified configuration - file changed by other user? Try again."
	for _, tt := range []struct {
		name string
		err  error
		want int
	}{
		{"digest mismatch is a conflict", apiErr(500, stale), fiber.StatusConflict},
		{"a digest mismatch is still a conflict when the client wrapped it",
			fmt.Errorf("set node pve-01 options: %w", apiErr(500, `{"data":null,"message":"`+stale+`\n"}`)), fiber.StatusConflict},
		// The client refused it; it never reached the node.
		{"an undeletable key stays a 400", proxmox.ErrInvalidInput, fiber.StatusBadRequest},
		{"an unreachable node stays a 502", proxmox.ErrConnectionFailed, fiber.StatusBadGateway},
		{"a permission failure stays a 403", proxmox.ErrForbidden, fiber.StatusForbidden},
		{"an unrelated PVE die stays a 502", apiErr(500, "unable to parse acme domain config"), fiber.StatusBadGateway},
		// pve-http-server answers a body over $limit_max_post with a 501 "for data too
		// large" and writes the reason into the body; the client wraps it like any error.
		{"a body pveproxy refuses as too large is a 413",
			fmt.Errorf("set node pve-01 options: %w", apiErr(501, "for data too large")), fiber.StatusRequestEntityTooLarge},
		{"the phrase is matched whatever its case", apiErr(501, "For Data Too Large"), fiber.StatusRequestEntityTooLarge},
		{"any other 501 stays a 502", apiErr(501, "no such uri"), fiber.StatusBadGateway},
		{"a 501 for an unimplemented method stays a 502", apiErr(501, "method 'PATCH' not available"), fiber.StatusBadGateway},
		{"the phrase on another status stays a 502", apiErr(500, "for data too large"), fiber.StatusBadGateway},
	} {
		t.Run(tt.name, func(t *testing.T) {
			wantStatus(t, mapNodeConfigError(tt.err), tt.want)
		})
	}
	if err := mapNodeConfigError(nil); err != nil {
		t.Errorf("mapNodeConfigError(nil) = %v, want nil", err)
	}
}

// TestMapNodeConfigErrorBodyTooLargeSaysTheLimit pins what the caller is told. The
// declared bound on the notes is 65536 characters, which is not what decides the
// refusal, so the message has to say what does: the limit in bytes, that it moved at
// Proxmox VE 8.4, that it is counted after encoding, and what to do about it.
func TestMapNodeConfigErrorBodyTooLargeSaysTheLimit(t *testing.T) {
	err := mapNodeConfigError(&proxmox.APIError{StatusCode: 501, Message: "for data too large"})
	wantStatus(t, err, fiber.StatusRequestEntityTooLarge)
	var fe *fiber.Error
	errors.As(err, &fe)
	for _, want := range []string{"too large for Proxmox", "64 KiB", "512 KiB", "Proxmox VE 8.4", "after encoding", "shorten the notes"} {
		if !strings.Contains(fe.Message, want) {
			t.Errorf("the message %q does not say %q", fe.Message, want)
		}
	}
	if strings.Contains(fe.Message, "for data too large") {
		t.Errorf("the message %q repeats pveproxy's own words, which say nothing to the caller", fe.Message)
	}
}

// --- POST /clusters/:cluster_id/acme/accounts: what the audit trail names ---

// acmeAccountCreateMirror mirrors createACMEAccountParams from
// internal/api/registry_acme.go: cluster_id from the shared standard option and the
// name's pattern from the shared catalogue, rather than either being retyped, so "" stays
// acceptable for `name` (pve-object-id-or-empty) — the whole reason an empty name can
// reach the handler at all.
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
		// Declared because the handler READS them: p.String panics on an undeclared key.
		// Their pattern is emptyOrACMEURL, a constant in package api, which imports this
		// package, so it is retyped here; registry_acme_test.go holds the declaration to it.
		"directory": {Type: apischema.String, Optional: true, Pattern: `^$|^https?://`, MaxLength: apischema.Ptr(2048)},
		"tos_url":   {Type: apischema.String, Optional: true, Pattern: `^$|^https?://`, MaxLength: apischema.Ptr(2048)},
	})
}

// acmeCreateHarness is one wired-up POST .../acme/accounts pipeline: a real Fiber app
// carrying the real ACMEHandler, a real proxmox.Client pointed at a capture server that
// answers with a UPID, and a DBTX that records every statement the handler sends.
type acmeCreateHarness struct {
	app       *fiber.App
	clusterID uuid.UUID
	dbtx      *captureDBTX
	// pubsub carries everything the request publishes. A real Publisher over miniredis,
	// not the nil one the sibling harnesses pass: ClusterEvent returns on a nil receiver,
	// so with nil the fourth sink is a no-op and reverting it to req.Name would leave this
	// file green.
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

	// PSubscribe rather than a computed channel name: events.publishChannel splits audit
	// entries onto their own room, and re-deriving that split here would be a second copy
	// of the routing rule.
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
		// Load-bearing, and what newPathParamHarness deliberately does NOT do: AuditLog
		// returns at its first line without a user_id local, and TrackTask returns before
		// InsertTaskHistory for the same reason, so every assertion below would run
		// against an empty slice and pass no matter what the handler recorded.
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

// taskDescription reads the description off the task_history insert. auditInserts
// cannot see this row (it filters on the statement text), but captureDBTX.record
// carries every call, so the insert is in calls; description is arg 3 of (cluster_id,
// user_id, upid, description, status, node, task_type).
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
// published — the fourth sink, outside the TrackTask struct literal, so the one most
// easily edited on its own. The create publishes THREE events and the Kind tells them
// apart: AuditLog publishes its audit_entry with the same "acme_account"/"created" pair,
// and it goes out FIRST, so filtering on resource_type and action alone returned the sink
// that was not under test and reverting the real one left this file green.
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
			if ev.Kind == events.KindACMEChange && ev.ResourceType == "acme_account" && ev.Action == "created" {
				return ev.ResourceID
			}
		case <-deadline:
			t.Fatal("no acme_change/acme_account/created event was published — the WS sink assertion is vacuous")
		}
	}
}

// TestACMEAccountCreateRecordsTheNameTheAccountGetsNotTheOneSent is the proof that the
// audit trail names an account that exists. An omitted or empty name is valid input
// (`name` is optional under pve-object-id-or-empty, and proxmox.CreateACMEAccount sets
// the form key only `if params.Name != ""`), so an empty name genuinely reaches Proxmox
// as no name at all and the account is registered under "default"
// (`extract_param($param, 'name') // 'default'` in PVE's register_account). Every sink
// here used to record the caller's "", naming nothing, in a row view:audit shows every
// Viewer. ALL FOUR sinks are asserted per case — the audit row's resource_id, its
// details.name, the task_history description and the WS event's resource id — because
// they are separately mutable lines fed from one value.
func TestACMEAccountCreateRecordsTheNameTheAccountGetsNotTheOneSent(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantName      string
		wantDefaulted bool
	}{
		{"name omitted", `{"contact":"admin@example.com"}`, "default", true},
		// Distinct from the case above at the wire: "" is a value apischema does not fall
		// back to a default for, so it reaches the handler as a supplied empty string.
		{"name explicitly empty", `{"name":"","contact":"admin@example.com"}`, "default", true},
		// The only input that separates `req.Name == ""` from `name == default`: the caller
		// ASKED for the name Proxmox would have chosen anyway.
		{"name explicitly default", `{"name":"default","contact":"admin@example.com"}`, "default", false},
		// The control that keeps the fix from becoming "always say default".
		{"name populated", `{"name":"letsencrypt-prod","contact":"admin@example.com"}`, "letsencrypt-prod", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newACMECreateHarness(t)
			h.send(t, tt.body)

			row, details := h.auditRow(t)
			if row.ResourceID != tt.wantName {
				t.Errorf("audit resource_id = %q, want %q — the row names an account that does not exist", row.ResourceID, tt.wantName)
			}
			if got, _ := details["name"].(string); got != tt.wantName {
				t.Errorf("audit details.name = %v, want %q", details["name"], tt.wantName)
			}
			if got, ok := details["name_defaulted"].(bool); !ok || got != tt.wantDefaulted {
				t.Errorf("audit details.name_defaulted = %v, want %t — without it the row cannot say whether Proxmox chose the name or the caller did",
					details["name_defaulted"], tt.wantDefaulted)
			}
			if got, want := h.taskDescription(t), "Create ACME account "+tt.wantName; got != want {
				t.Errorf("task_history description = %q, want %q", got, want)
			}
			if got := h.eventResourceID(t); got != tt.wantName {
				t.Errorf("WS event resource id = %q, want %q — the live feed names an account that does not exist", got, tt.wantName)
			}
		})
	}
}
