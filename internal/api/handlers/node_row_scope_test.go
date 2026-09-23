package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The three node-row listings — disks, network interfaces and PCI devices —
// key their queries on the node's row id alone, and the route authorizes the
// cluster in the PATH. nodeInCluster is what ties the two together; before it,
// a caller holding view:node on one cluster read another cluster's hardware
// inventory by putting their own cluster in the path and the other node's id
// after it. These cases drive the real handlers, so deleting the comparison —
// or the call to it — fails here rather than nowhere.

// nodeRowDBTX serves GetNode from one node row and records which listing
// query ran, answering it with no rows.
//
// It answers only when asked about THAT node. A fake that ignored its
// arguments would hand back the node for any id and record a listing keyed
// on anything, so a handler that looked up the cluster id as the node, or
// listed by the cluster, would pass the "is listed" case while every real
// request came back 404 or empty.
type nodeRowDBTX struct {
	node      db.Node
	found     bool
	lookupErr error // when set, GetNode fails with it: neither found nor absent
	listed    []string
}

func (*nodeRowDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errCaptured
}

func (d *nodeRowDBTX) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	if len(args) != 1 || args[0] != d.node.ID {
		return nil, errCaptured
	}
	for _, table := range []string{"node_disks", "node_network_interfaces", "node_pci_devices"} {
		if strings.Contains(sql, "FROM "+table) {
			d.listed = append(d.listed, table)
			return &structRows{}, nil
		}
	}
	return nil, errCaptured
}

func (d *nodeRowDBTX) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if !strings.Contains(sql, "FROM nodes WHERE id") {
		return failRow{err: errCaptured}
	}
	if d.lookupErr != nil {
		return failRow{err: d.lookupErr}
	}
	if d.found && len(args) == 1 && args[0] == d.node.ID {
		return nodeLookupRow{node: d.node}
	}
	return failRow{err: pgx.ErrNoRows}
}

// nodeLookupRow replays one db.Node through pgx.Row, in the generated
// struct's field order, which is GetNode's scan order.
type nodeLookupRow struct{ node db.Node }

func (r nodeLookupRow) Scan(dest ...any) error {
	rows := &structRows{rows: []any{r.node}}
	rows.Next()
	return rows.Scan(dest...)
}

func TestNodeRowListingsRefuseAnotherClustersNode(t *testing.T) {
	pathCluster := uuid.New()
	otherCluster := uuid.New()
	nodeID := uuid.New()

	mirror := compiledMirror(t, apischema.Properties{
		"cluster_id": {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
		"node_id":    {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
	})

	routes := []struct {
		suffix string
		table  string
		serve  func(h *NodeHandler) func(fiber.Ctx, *apischema.Params) error
	}{
		{"disks", "node_disks", func(h *NodeHandler) func(fiber.Ctx, *apischema.Params) error { return h.ListNodeDisks }},
		{"network-interfaces", "node_network_interfaces", func(h *NodeHandler) func(fiber.Ctx, *apischema.Params) error {
			return h.ListNodeNetworkInterfaces
		}},
		{"pci-devices", "node_pci_devices", func(h *NodeHandler) func(fiber.Ctx, *apischema.Params) error { return h.ListNodePCIDevices }},
	}
	cases := []struct {
		name        string
		nodeCluster uuid.UUID
		found       bool
		lookupErr   error
		wantStatus  int
	}{
		{"a node in the path's cluster is listed", pathCluster, true, nil, http.StatusOK},
		{"a node in ANOTHER cluster is refused", otherCluster, true, nil, http.StatusNotFound},
		{"a node that does not exist answers the same 404", pathCluster, false, nil, http.StatusNotFound},
		// The third outcome of the lookup: it neither found the node nor
		// said it is absent. Treating that as "not refused" would list the
		// hardware of a node nobody checked.
		{"a failed lookup refuses rather than listing", pathCluster, true, errCaptured, http.StatusInternalServerError},
	}

	for _, route := range routes {
		for _, tc := range cases {
			t.Run(route.suffix+"/"+tc.name, func(t *testing.T) {
				dbtx := &nodeRowDBTX{
					node:      db.Node{ID: nodeID, ClusterID: tc.nodeCluster, Name: "pve-01", Status: "online"},
					found:     tc.found,
					lookupErr: tc.lookupErr,
				}
				handler := NewNodeHandler(db.New(dbtx), "", nil)
				app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
				app.Get("/clusters/:cluster_id/nodes/:node_id/"+route.suffix,
					withRequestParams(t, mirror, []string{"cluster_id", "node_id"}, route.serve(handler)))

				resp, err := app.Test(httptest.NewRequest(http.MethodGet,
					"/clusters/"+pathCluster.String()+"/nodes/"+nodeID.String()+"/"+route.suffix, nil))
				if err != nil {
					t.Fatalf("request: %v", err)
				}
				defer func() { _ = resp.Body.Close() }()
				body, _ := io.ReadAll(resp.Body)
				if resp.StatusCode != tc.wantStatus {
					t.Fatalf("status = %d, want %d (body: %s)", resp.StatusCode, tc.wantStatus, body)
				}
				listed := len(dbtx.listed) == 1 && dbtx.listed[0] == route.table
				if wantListed := tc.wantStatus == http.StatusOK; listed != wantListed {
					t.Errorf("the %s query ran = %v (queries: %v), want %v — a refused request must not reach it",
						route.table, listed, dbtx.listed, wantListed)
				}
				if tc.wantStatus == http.StatusNotFound && !strings.Contains(string(body), "Node not found in this cluster") {
					t.Errorf("body = %s, want the same \"Node not found in this cluster\" on both branches — a "+
						"distinct answer for \"exists but not yours\" is an existence oracle", body)
				}
			})
		}
	}
}
