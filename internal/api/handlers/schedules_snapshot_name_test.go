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

// TestScheduleCreateRefusesASnapshotNameProxmoxWouldReject is the test that
// turns the live bug into a 400.
//
// Every payload below was storable before this change. The row was created,
// the SPA showed the schedule as armed, and then every fire failed against PVE
// with a raw sentence in last_error — a recurring snapshot the operator
// believed was protecting a guest, silently never running.
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
			name: "a VM snapshot named current",
			body: `{"resource_type":"vm","resource_id":"101","node":"pve-01","action":"snapshot",` +
				`"schedule":"0 3 * * *","params":{"snap_name":"current"}}`,
			wantStatus: http.StatusBadRequest, wantPhrase: "reserved",
		},
		{
			name: "a VM snapshot named Pending — PVE folds the case on this one",
			body: `{"resource_type":"vm","resource_id":"101","node":"pve-01","action":"snapshot",` +
				`"schedule":"0 3 * * *","params":{"snap_name":"Pending"}}`,
			wantStatus: http.StatusBadRequest, wantPhrase: "reserved",
		},
		{
			name: "a container snapshot named vzdump",
			body: `{"resource_type":"ct","resource_id":"201","node":"pve-01","action":"snapshot",` +
				`"schedule":"0 3 * * *","params":{"snap_name":"vzdump"}}`,
			wantStatus: http.StatusBadRequest, wantPhrase: "reserved",
		},
		{
			name: "a name one over Proxmox's cap",
			body: `{"resource_type":"vm","resource_id":"101","node":"pve-01","action":"snapshot",` +
				`"schedule":"0 3 * * *","params":{"snap_name":"` + strings.Repeat("a", SnapshotMaxNameLen+1) + `"}}`,
			wantStatus: http.StatusBadRequest, wantPhrase: "snap_name",
		},
		{
			name: "a name with a space",
			body: `{"resource_type":"vm","resource_id":"101","node":"pve-01","action":"snapshot",` +
				`"schedule":"0 3 * * *","params":{"snap_name":"my snap.1"}}`,
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
			name: "an ordinary name is stored",
			body: `{"resource_type":"vm","resource_id":"101","node":"pve-01","action":"snapshot",` +
				`"schedule":"0 3 * * *","params":{"snap_name":"nightly"}}`,
			wantStatus: http.StatusInternalServerError, wantInsert: true,
		},
		{
			name: "the OTHER kind's reserved name is legal here",
			body: `{"resource_type":"ct","resource_id":"201","node":"pve-01","action":"snapshot",` +
				`"schedule":"0 3 * * *","params":{"snap_name":"pending"}}`,
			wantStatus: http.StatusInternalServerError, wantInsert: true,
		},
		{
			name: "no snap_name at all leaves the scheduler to mint one",
			body: `{"resource_type":"vm","resource_id":"101","node":"pve-01","action":"snapshot",` +
				`"schedule":"0 3 * * *","params":{}}`,
			wantStatus: http.StatusInternalServerError, wantInsert: true,
		},
		{
			name: "a reboot task carries no snapshot name to check",
			body: `{"resource_type":"vm","resource_id":"101","node":"pve-01","action":"reboot",` +
				`"schedule":"0 3 * * *","params":{"snap_name":"current"}}`,
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
// resource is fixed at creation — so the guest kind has to come off the stored
// row. That makes this the case a create-only check would miss entirely:
// create a schedule with a good name, then edit it to "current".
func TestScheduleUpdateRefusesASnapshotNameProxmoxWouldReject(t *testing.T) {
	clusterID := uuid.New()
	taskID := uuid.New()

	tests := []struct {
		name         string
		action       string
		resourceType string
		body         string
		wantStatus   int
		wantWrite    bool
	}{
		{
			name:   "a stored VM snapshot task edited to a reserved name",
			action: "snapshot", resourceType: "vm",
			body:       `{"schedule":"0 3 * * *","params":{"snap_name":"current"},"enabled":true}`,
			wantStatus: http.StatusBadRequest, wantWrite: false,
		},
		{
			name:   "a stored CT snapshot task edited to its own reserved name",
			action: "snapshot", resourceType: "ct",
			body:       `{"schedule":"0 3 * * *","params":{"snap_name":"vzdump"},"enabled":true}`,
			wantStatus: http.StatusBadRequest, wantWrite: false,
		},
		{
			name:   "the kind comes off the ROW, so a CT task keeps accepting pending",
			action: "snapshot", resourceType: "ct",
			body:       `{"schedule":"0 3 * * *","params":{"snap_name":"pending"},"enabled":true}`,
			wantStatus: http.StatusOK, wantWrite: true,
		},
		{
			name:   "a stored reboot task has no snapshot name to check",
			action: "reboot", resourceType: "vm",
			body:       `{"schedule":"0 3 * * *","params":{"snap_name":"current"},"enabled":true}`,
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
