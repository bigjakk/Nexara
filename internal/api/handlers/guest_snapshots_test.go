package handlers

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

func snapRow(clusterID uuid.UUID, vmid int32, name, guestType string) db.ListAllGuestSnapshotsRow {
	return db.ListAllGuestSnapshotsRow{
		ClusterID:   clusterID,
		ClusterName: "c-" + clusterID.String()[:8],
		Vmid:        vmid,
		Name:        name,
		GuestType:   guestType,
		Node:        "pve1",
		SnapTime:    1000,
		LastSeenAt:  time.Unix(2000, 0),
	}
}

func TestFilterGuestSnapshotRows(t *testing.T) {
	clusterA := uuid.New()
	clusterB := uuid.New()
	global := clusterAccess{HasGlobal: true}
	none := clusterAccess{Allowed: map[uuid.UUID]bool{}}
	onlyA := clusterAccess{Allowed: map[uuid.UUID]bool{clusterA: true}}

	rows := []db.ListAllGuestSnapshotsRow{
		snapRow(clusterA, 100, "vm-snap-a", "qemu"),
		snapRow(clusterA, 101, "ct-snap-a", "lxc"),
		snapRow(clusterB, 200, "vm-snap-b", "qemu"),
		snapRow(clusterB, 201, "ct-snap-b", "lxc"),
	}

	tests := []struct {
		name          string
		vmAccess      clusterAccess
		ctAccess      clusterAccess
		clusterFilter uuid.UUID
		wantNames     []string
	}{
		{
			name:     "global on both sees everything",
			vmAccess: global, ctAccess: global,
			wantNames: []string{"vm-snap-a", "ct-snap-a", "vm-snap-b", "ct-snap-b"},
		},
		{
			name:     "vm-only user sees qemu rows only",
			vmAccess: global, ctAccess: none,
			wantNames: []string{"vm-snap-a", "vm-snap-b"},
		},
		{
			name:     "cluster-scoped container grant sees that cluster's lxc rows only",
			vmAccess: none, ctAccess: onlyA,
			wantNames: []string{"ct-snap-a"},
		},
		{
			name:     "split grants combine per row",
			vmAccess: onlyA, ctAccess: clusterAccess{Allowed: map[uuid.UUID]bool{clusterB: true}},
			wantNames: []string{"vm-snap-a", "ct-snap-b"},
		},
		{
			name:     "cluster filter intersects with access",
			vmAccess: global, ctAccess: global,
			clusterFilter: clusterA,
			wantNames:     []string{"vm-snap-a", "ct-snap-a"},
		},
		{
			name:     "no grants sees nothing",
			vmAccess: none, ctAccess: none,
			wantNames: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterGuestSnapshotRows(rows, tt.vmAccess, tt.ctAccess, tt.clusterFilter)
			if len(got) != len(tt.wantNames) {
				t.Fatalf("got %d rows, want %d: %+v", len(got), len(tt.wantNames), got)
			}
			for i, want := range tt.wantNames {
				if got[i].Name != want {
					t.Fatalf("row %d = %q, want %q", i, got[i].Name, want)
				}
			}
		})
	}
}

func TestFilterGuestSnapshotRows_UnknownGuestTypeDropped(t *testing.T) {
	clusterA := uuid.New()
	row := snapRow(clusterA, 100, "weird", "openvz")
	got := filterGuestSnapshotRows([]db.ListAllGuestSnapshotsRow{row},
		clusterAccess{HasGlobal: true}, clusterAccess{HasGlobal: true}, uuid.Nil)
	if len(got) != 0 {
		t.Fatalf("unknown guest_type must be dropped, got %+v", got)
	}
}

// newGuestSnapshotTestApp wires the Resync route with NIL queries: every case
// below must resolve before any DB access, so a regression that moves the
// guest lookup ahead of the permission gate panics on the nil Queries and
// fails loudly instead of silently reintroducing the vmid-existence oracle.
func newGuestSnapshotTestApp(t *testing.T) *fiber.App {
	t.Helper()
	handler := NewGuestSnapshotHandler(nil, testEncryptionKey, nil)
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Use(func(c fiber.Ctx) error {
		role := c.Get("X-Test-Role")
		if role != "" {
			c.Locals("role", role)
			c.Locals("user_id", uuid.New())
		}
		return c.Next()
	})
	installStubEngineMiddleware(app)
	app.Post("/clusters/:cluster_id/guest-snapshots/resync", handler.Resync)
	return app
}

func TestGuestSnapshotResync_GatesBeforeLookup(t *testing.T) {
	app := newGuestSnapshotTestApp(t)
	clusterID := uuid.New().String()

	tests := []struct {
		name   string
		role   string
		path   string
		body   string
		status int
	}{
		{
			name:   "no guest permission is rejected before any lookup",
			role:   "viewer",
			path:   "/clusters/" + clusterID + "/guest-snapshots/resync",
			body:   `{"vmid":100}`,
			status: http.StatusForbidden,
		},
		{
			name:   "invalid cluster id",
			role:   "admin",
			path:   "/clusters/not-a-uuid/guest-snapshots/resync",
			body:   `{"vmid":100}`,
			status: http.StatusBadRequest,
		},
		{
			name:   "malformed body",
			role:   "admin",
			path:   "/clusters/" + clusterID + "/guest-snapshots/resync",
			body:   `{`,
			status: http.StatusBadRequest,
		},
		{
			name:   "missing vmid",
			role:   "admin",
			path:   "/clusters/" + clusterID + "/guest-snapshots/resync",
			body:   `{"vmid":0}`,
			status: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewReader([]byte(tt.body)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Test-Role", tt.role)
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tt.status {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d, want %d (body: %s)", resp.StatusCode, tt.status, body)
			}
		})
	}
}

func TestMapGuestSnapshotRow_OrphanHasNullGuestFields(t *testing.T) {
	clusterA := uuid.New()
	orphan := snapRow(clusterA, 100, "left-behind", "qemu")
	// LEFT JOIN misses: vms row gone, all three nullable columns invalid.
	item := mapGuestSnapshotRow(orphan)
	if item.VMID != nil || item.VMName != nil || item.VMStatus != nil {
		t.Fatalf("orphan row must map guest fields to null, got %+v", item)
	}

	vmRowID := uuid.New()
	linked := orphan
	linked.VmID = pgtype.UUID{Bytes: vmRowID, Valid: true}
	linked.VmName = pgtype.Text{String: "web01", Valid: true}
	linked.VmStatus = pgtype.Text{String: "running", Valid: true}
	item = mapGuestSnapshotRow(linked)
	if item.VMID == nil || *item.VMID != vmRowID {
		t.Fatalf("linked row must expose the vms UUID, got %+v", item.VMID)
	}
	if item.VMName == nil || *item.VMName != "web01" || item.VMStatus == nil || *item.VMStatus != "running" {
		t.Fatalf("linked row must expose name/status, got %+v", item)
	}
}
