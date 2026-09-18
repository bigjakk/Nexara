package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"

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

// TestGuard_GuestSnapshotResyncReadsPermissionsBeforeTheGuest is what is left
// of the gates-before-lookup guard now that the COARSE gate has moved out of
// this package.
//
// It used to mount Resync with NIL queries and assert that a caller holding
// neither guest permission got a 403 before anything touched the database. That
// refusal is now the declared Alternatives middleware in
// internal/api/registry_guest_snapshots.go, and
// TestGuestSnapshotResyncGateIsEitherGuestPermission proves it end to end —
// including that the handler never runs, which is a stronger statement than
// "it returned 403".
//
// The ORDERING inside the handler still matters and cannot be seen from there:
// the two hasClusterPerm reads feed the PRECISE per-class refusal, which
// answers 404 rather than 403 so a caller holding only the other guest class
// cannot learn that a guest with this vmid exists. If the guest lookup moved
// ahead of them, the 404-vs-403 split would become a vmid-existence oracle
// again. This pins the order statically.
func TestGuard_GuestSnapshotResyncReadsPermissionsBeforeTheGuest(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "guest_snapshots.go", nil, 0)
	if err != nil {
		t.Fatalf("parse guest_snapshots.go: %v", err)
	}

	var lastPermRead, guestLookup token.Pos
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Name.Name != "Resync" {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				if fun.Name == "hasClusterPerm" && call.Pos() > lastPermRead {
					lastPermRead = call.Pos()
				}
			case *ast.SelectorExpr:
				if fun.Sel.Name == "GetVMByClusterAndVmid" && guestLookup == token.NoPos {
					guestLookup = call.Pos()
				}
			}
			return true
		})
	}

	if lastPermRead == token.NoPos {
		t.Fatal("Resync makes no hasClusterPerm call; the precise per-class refusal has nothing to decide on")
	}
	if guestLookup == token.NoPos {
		t.Fatal("Resync makes no GetVMByClusterAndVmid call; this guard would pass vacuously")
	}
	if lastPermRead > guestLookup {
		t.Errorf("Resync resolves the guest (offset %d) before reading its permissions (offset %d) — "+
			"that turns the 404-vs-403 split into a vmid-existence oracle", guestLookup, lastPermRead)
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
