package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// These tests drive the real Create and Update handlers against the same
// stand-in DBTX schedules_scope_test.go uses. No database is needed, because
// the assertion is that the INSERT/UPDATE is never reached.
//
// The hazard they cover: scheduled_tasks.params is a jsonb blob the endpoint
// declaration carries through without describing, filled from a bare free-text
// field in the SPA. A snap_name stored there reached CreateVMSnapshot with no
// cap, no shape check and no reserved-name check. The client refuses it now —
// that is the fix, and internal/proxmox is where it is tested — but a task
// refused at fire time is refused on EVERY fire, silently, in a last_error
// column nobody is watching. These hold the other half: the operator is told
// while the form is still open.

// scheduleCreateMirror restates the POST schema registry_schedules.go
// declares, for the reason withRequestParams' own doc comment gives: package
// api imports this package, not the other way round.
func scheduleCreateMirror(t *testing.T) apischema.Properties {
	t.Helper()
	return compiledMirror(t, apischema.Properties{
		"cluster_id":    {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
		"resource_type": {Type: apischema.String, MinLength: apischema.Ptr(1), MaxLength: apischema.Ptr(32)},
		"resource_id":   {Type: apischema.String, MinLength: apischema.Ptr(1), MaxLength: apischema.Ptr(128)},
		"node":          {Type: apischema.String, MinLength: apischema.Ptr(1), MaxLength: apischema.Ptr(256)},
		"action":        {Type: apischema.String, Enum: []string{"snapshot", "reboot"}},
		"schedule":      {Type: apischema.String, MinLength: apischema.Ptr(1), MaxLength: apischema.Ptr(256)},
		"params":        {Type: apischema.Object, Optional: true},
		"enabled":       {Type: apischema.Boolean, Optional: true, Default: false},
	})
}

func newScheduleCreateApp(t *testing.T, dbtx db.DBTX) *fiber.App {
	t.Helper()
	handler := NewScheduleHandler(db.New(dbtx), nil)

	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Post("/clusters/:cluster_id/schedules",
		withRequestParams(t, scheduleCreateMirror(t), []string{"cluster_id"}, handler.Create))
	return app
}

// snapshotScheduleBody is a create body for a snapshot task carrying snapName.
func snapshotScheduleBody(resourceType, snapName string) string {
	return `{"resource_type":"` + resourceType + `","resource_id":"101","node":"pve-01","action":"snapshot",` +
		`"schedule":"0 3 * * *","params":{"snap_name":"` + snapName + `"}}`
}

// TestScheduleCreateRefusesASnapshotNameProxmoxWouldReject is the test that
// turns the live bug into a 400.
//
// A refused payload here was once storable. The row was created, the SPA
// showed the schedule as armed, and then every fire failed against PVE with a
// raw sentence in last_error — a recurring snapshot the operator believed was
// protecting a guest, silently never running.
//
// snap_name is a PREFIX now: each run adds -YYYYMMDD-HHMMSS, because a name
// sent verbatim on every run failed on every run after the first (a guest
// holds each snapshot name once). So the budget is 24 characters, not 40, and
// what Proxmox reserves is judged on the whole name a run sends — which is why
// the reserved words sit in the accepting half.
func TestScheduleCreateRefusesASnapshotNameProxmoxWouldReject(t *testing.T) {
	clusterID := uuid.New()

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantInsert bool
		wantPhrase string
	}{
		{
			name:       "a prefix one over the 24-character budget",
			body:       snapshotScheduleBody("vm", strings.Repeat("a", 25)),
			wantStatus: http.StatusBadRequest, wantPhrase: "at most 24 characters",
		},
		{
			// Accepted while the name was sent verbatim; with a date added
			// it composes a 56-character name no run could send.
			name:       "the old whole-name maximum of 40",
			body:       snapshotScheduleBody("ct", strings.Repeat("a", SnapshotMaxNameLen)),
			wantStatus: http.StatusBadRequest, wantPhrase: "prefix",
		},
		{
			name:       "a name with a space",
			body:       snapshotScheduleBody("vm", "my snap.1"),
			wantStatus: http.StatusBadRequest, wantPhrase: "snap_name",
		},
		{
			name:       "a name starting with a digit",
			body:       snapshotScheduleBody("ct", "1nightly"),
			wantStatus: http.StatusBadRequest, wantPhrase: "snap_name",
		},
		{
			name: "snap_name of the wrong JSON type",
			body: `{"resource_type":"vm","resource_id":"101","node":"pve-01","action":"snapshot",` +
				`"schedule":"0 3 * * *","params":{"snap_name":42}}`,
			wantStatus: http.StatusBadRequest, wantPhrase: "snap_name",
		},

		// The accepting half. These must reach the INSERT — which this fake
		// answers with an error, so a 500 here means "got as far as the
		// database", not "succeeded". Without them the test above would be
		// satisfied by a handler that refuses every snapshot schedule.
		{
			name:       "an ordinary prefix is stored",
			body:       snapshotScheduleBody("vm", "nightly"),
			wantStatus: http.StatusInternalServerError, wantInsert: true,
		},
		{
			name:       "exactly the 24-character budget",
			body:       snapshotScheduleBody("vm", strings.Repeat("a", 24)),
			wantStatus: http.StatusInternalServerError, wantInsert: true,
		},
		{
			name:       "a one-letter prefix, since Proxmox's two-character minimum is on the whole name",
			body:       snapshotScheduleBody("ct", "a"),
			wantStatus: http.StatusInternalServerError, wantInsert: true,
		},
		{
			name:       "current on a VM — Proxmox reserves the whole name, and a run's name carries a date",
			body:       snapshotScheduleBody("vm", "current"),
			wantStatus: http.StatusInternalServerError, wantInsert: true,
		},
		{
			name:       "Pending on a VM — the case PVE folds, still only as a whole name",
			body:       snapshotScheduleBody("vm", "Pending"),
			wantStatus: http.StatusInternalServerError, wantInsert: true,
		},
		{
			name:       "vzdump on a container — reserved only as a whole name",
			body:       snapshotScheduleBody("ct", "vzdump"),
			wantStatus: http.StatusInternalServerError, wantInsert: true,
		},
		{
			name: "no snap_name at all leaves the scheduler to use auto",
			body: `{"resource_type":"vm","resource_id":"101","node":"pve-01","action":"snapshot",` +
				`"schedule":"0 3 * * *","params":{}}`,
			wantStatus: http.StatusInternalServerError, wantInsert: true,
		},
		{
			// A value every snapshot case above refuses, so this row fails
			// the moment the action stops gating the check.
			name: "a reboot task carries no snapshot name to check",
			body: `{"resource_type":"vm","resource_id":"101","node":"pve-01","action":"reboot",` +
				`"schedule":"0 3 * * *","params":{"snap_name":"my snap.1"}}`,
			wantStatus: http.StatusInternalServerError, wantInsert: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbtx := &scheduleDBTX{}
			app := newScheduleCreateApp(t, dbtx)

			req := httptest.NewRequest(http.MethodPost,
				"/clusters/"+clusterID.String()+"/schedules", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", resp.StatusCode, tt.wantStatus, body)
			}
			if got := dbtx.ran("INSERT INTO scheduled_tasks"); got != tt.wantInsert {
				t.Errorf("the INSERT ran = %v, want %v — a name Proxmox refuses must not become a row",
					got, tt.wantInsert)
			}
			if tt.wantPhrase != "" && !strings.Contains(string(body), tt.wantPhrase) {
				t.Errorf("body = %s, want it to contain %q so the operator can fix it", body, tt.wantPhrase)
			}
		})
	}
}

// TestScheduleUpdateRefusesASnapshotNameProxmoxWouldReject covers the other
// write.
//
// The PUT body carries neither the action nor the resource type — a task's
// resource is fixed at creation — so both have to come off the stored row.
// That makes this the case a create-only check would miss entirely: create a
// schedule with a good prefix, then edit it to one no run could send.
func TestScheduleUpdateRefusesASnapshotNameProxmoxWouldReject(t *testing.T) {
	clusterID := uuid.New()
	taskID := uuid.New()
	overBudget := strings.Repeat("a", 25)

	tests := []struct {
		name         string
		action       string
		resourceType string
		body         string
		wantStatus   int
		wantWrite    bool
	}{
		{
			name:   "a stored VM snapshot task edited to a prefix over the budget",
			action: "snapshot", resourceType: "vm",
			body:       `{"schedule":"0 3 * * *","params":{"snap_name":"` + overBudget + `"},"enabled":true}`,
			wantStatus: http.StatusBadRequest, wantWrite: false,
		},
		{
			name:   "a stored CT snapshot task edited to a name with a space",
			action: "snapshot", resourceType: "ct",
			body:       `{"schedule":"0 3 * * *","params":{"snap_name":"my snap"},"enabled":true}`,
			wantStatus: http.StatusBadRequest, wantWrite: false,
		},
		{
			name:   "a stored snapshot task edited to a reserved word, which is a legal prefix",
			action: "snapshot", resourceType: "vm",
			body:       `{"schedule":"0 3 * * *","params":{"snap_name":"current"},"enabled":true}`,
			wantStatus: http.StatusOK, wantWrite: true,
		},
		{
			// The same over-budget value the first case refuses: the action
			// comes off the ROW, and a reboot has no snapshot name to check.
			name:   "a stored reboot task has no snapshot name to check",
			action: "reboot", resourceType: "vm",
			body:       `{"schedule":"0 3 * * *","params":{"snap_name":"` + overBudget + `"},"enabled":true}`,
			wantStatus: http.StatusOK, wantWrite: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbtx := &scheduleDBTX{
				task: db.ScheduledTask{
					ID: taskID, ClusterID: clusterID,
					ResourceType: tt.resourceType, ResourceID: "101", Node: "pve-01",
					Action: tt.action, Schedule: "0 3 * * *",
					Params: json.RawMessage(`{}`),
				},
				found:        true,
				rowsAffected: 1,
			}
			app := newScheduleScopeApp(t, dbtx)

			req := httptest.NewRequest(http.MethodPut,
				"/clusters/"+clusterID.String()+"/schedules/"+taskID.String(),
				strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", resp.StatusCode, tt.wantStatus, body)
			}
			if got := dbtx.ran("UPDATE scheduled_tasks"); got != tt.wantWrite {
				t.Errorf("the UPDATE ran = %v, want %v", got, tt.wantWrite)
			}
		})
	}
}
