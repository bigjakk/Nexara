package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// upsCaller holds the global grants it names and one grant on one cluster
// only, the caller the UPS routes must tell from one who holds it everywhere.
type upsCaller struct {
	clusterGrantEngine
	global []string
}

func (u upsCaller) HasGlobalPermission(_ context.Context, _ uuid.UUID, action, resource string) (bool, error) {
	return slices.Contains(u.global, action+":"+resource), nil
}

// What each feed route does with what it is asked, past the node-membership
// check TestNodesCheckedByHandler_RefuseANodeTheClusterDoesNotHold drives: a
// node already powered is a 409, a feed in a cluster the caller cannot see is
// not found, a node is dismissed and a label deleted, and a label is no node.
func TestUpsFeedRoutes(t *testing.T) {
	t.Parallel()
	seen, hidden := uuid.New(), uuid.New()
	dev := db.GetUpsDeviceRow{ID: uuid.New(), UpsName: "ups01", ServerName: "nut01"}
	node := func(cluster uuid.UUID) db.UpsFeed {
		return db.UpsFeed{ID: uuid.New(), UpsID: dev.ID, Source: "found",
			ClusterID: pgtype.UUID{Bytes: cluster, Valid: true}, NodeName: pgtype.Text{String: "pve-01", Valid: true}}
	}
	label := db.UpsFeed{ID: uuid.New(), UpsID: dev.ID, Label: "Core switch", Source: "manual"}
	member := db.Node{ID: uuid.New(), ClusterID: seen, Name: "pve-01"}
	server := db.NutServer{ID: uuid.New(), Name: "nut01"}
	str := apischema.Property{Type: apischema.String, Optional: true}
	flag := apischema.Property{Type: apischema.Boolean, Optional: true}
	routes := map[string]struct {
		method, path string
		props        apischema.Properties
		serve        func(*NutHandler) func(fiber.Ctx, *apischema.Params) error
	}{
		"add":    {http.MethodPost, "/ups/:id/feeds", apischema.Properties{"id": str, "feed_cluster_id": str, "node_name": str, "label": str}, func(h *NutHandler) func(fiber.Ctx, *apischema.Params) error { return h.AddFeed }},
		"update": {http.MethodPut, "/ups/:id/feeds/:feed_id", apischema.Properties{"id": str, "feed_id": str, "protected_elsewhere": flag}, func(h *NutHandler) func(fiber.Ctx, *apischema.Params) error { return h.UpdateFeed }},
		"remove": {http.MethodDelete, "/ups/:id/feeds/:feed_id", apischema.Properties{"id": str, "feed_id": str}, func(h *NutHandler) func(fiber.Ctx, *apischema.Params) error { return h.RemoveFeed }},
		"host":   {http.MethodPut, "/nut/:id/host-node", apischema.Properties{"id": str, "host_cluster_id": str, "node_name": str, "detect": flag}, func(h *NutHandler) func(fiber.Ctx, *apischema.Params) error { return h.SetHostNode }},
	}
	nodeFeed, hiddenFeed := node(seen), node(hidden)
	for _, tc := range []struct {
		name, route, target string
		body                map[string]any
		rows                map[string]any
		want                int
		everywhere          bool // the caller may view nodes in every cluster
	}{
		{"add a label", "add", "/ups/" + dev.ID.String() + "/feeds", map[string]any{"label": "Core switch"},
			map[string]any{"GetUpsDevice": dev, "InsertManualUpsFeed": label}, fiber.StatusCreated, false},
		{"add a node and a label at once", "add", "/ups/" + dev.ID.String() + "/feeds",
			map[string]any{"label": "Core switch", "node_name": "pve-01", "feed_cluster_id": seen.String()},
			map[string]any{"GetUpsDevice": dev}, fiber.StatusBadRequest, false},
		{"add a node already powered", "add", "/ups/" + dev.ID.String() + "/feeds",
			map[string]any{"node_name": "pve-01", "feed_cluster_id": seen.String()},
			map[string]any{"GetUpsDevice": dev, "GetNodeByClusterAndName": member, "InsertManualUpsFeed": pgx.ErrNoRows}, fiber.StatusConflict, false},
		{"add a label already there", "add", "/ups/" + dev.ID.String() + "/feeds", map[string]any{"label": "Core switch"},
			map[string]any{"GetUpsDevice": dev, "InsertManualUpsFeed": &pgconn.PgError{Code: "23505"}}, fiber.StatusConflict, false},
		{"add a node of a hidden cluster", "add", "/ups/" + dev.ID.String() + "/feeds",
			map[string]any{"node_name": "pve-01", "feed_cluster_id": hidden.String()},
			map[string]any{"GetUpsDevice": dev}, fiber.StatusForbidden, false},
		{"remove a node", "remove", "/ups/" + dev.ID.String() + "/feeds/" + nodeFeed.ID.String(), nil,
			map[string]any{"GetUpsDevice": dev, "GetUpsFeed": nodeFeed, "DismissUpsNodeFeed": nodeFeed}, fiber.StatusNoContent, false},
		{"remove a label", "remove", "/ups/" + dev.ID.String() + "/feeds/" + label.ID.String(), nil,
			map[string]any{"GetUpsDevice": dev, "GetUpsFeed": label, "DeleteUpsLabelFeed": label}, fiber.StatusNoContent, false},
		{"remove a hidden cluster's node", "remove", "/ups/" + dev.ID.String() + "/feeds/" + hiddenFeed.ID.String(), nil,
			map[string]any{"GetUpsDevice": dev, "GetUpsFeed": hiddenFeed}, fiber.StatusNotFound, false},
		{"protect a node elsewhere", "update", "/ups/" + dev.ID.String() + "/feeds/" + nodeFeed.ID.String(), map[string]any{"protected_elsewhere": true},
			map[string]any{"GetUpsDevice": dev, "GetUpsFeed": nodeFeed, "SetUpsFeedProtectedElsewhere": nodeFeed}, fiber.StatusOK, false},
		{"protect a label elsewhere", "update", "/ups/" + dev.ID.String() + "/feeds/" + label.ID.String(), map[string]any{"protected_elsewhere": true},
			map[string]any{"GetUpsDevice": dev, "GetUpsFeed": label}, fiber.StatusBadRequest, false},
		{"set the host node", "host", "/nut/" + server.ID.String() + "/host-node", map[string]any{"node_name": "pve-01", "host_cluster_id": seen.String()},
			map[string]any{"GetNutServer": server, "GetNodeByClusterAndName": member, "SetNutServerHostNodeByHand": server}, fiber.StatusNoContent, true},
		// One host per server, possibly in a cluster the caller cannot see.
		{"set the host node, seeing one cluster", "host", "/nut/" + server.ID.String() + "/host-node", map[string]any{"node_name": "pve-01", "host_cluster_id": seen.String()},
			map[string]any{"GetNutServer": server, "GetNodeByClusterAndName": member, "SetNutServerHostNodeByHand": server}, fiber.StatusForbidden, false},
		{"hand the host node back to detection", "host", "/nut/" + server.ID.String() + "/host-node", map[string]any{"detect": true},
			map[string]any{"GetNutServer": server, "SetNutServerHostNodeByHand": server}, fiber.StatusNoContent, true},
		{"detect and name a node at once", "host", "/nut/" + server.ID.String() + "/host-node", map[string]any{"detect": true, "node_name": "pve-01"},
			map[string]any{"GetNutServer": server}, fiber.StatusBadRequest, true},
	} {
		r := routes[tc.route]
		h := NewNutHandler(db.New(&auditRowsDBTX{rowFor: tc.rows}), testEncryptionKey, nil)
		app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
		var engine permissionEngine = upsCaller{clusterGrantEngine{"view", "node", seen}, []string{"manage:ups"}}
		if tc.everywhere {
			engine = newGrantEngine("manage:ups", "view:node")
		}
		app.Use(func(c fiber.Ctx) error {
			c.Locals("user_id", uuid.New())
			c.Locals("rbac_engine", engine)
			return c.Next()
		})
		app.Add([]string{r.method}, r.path, withRequestParams(t, r.props, []string{"id", "feed_id"}, r.serve(h)))
		if tc.body == nil {
			tc.body = map[string]any{} // JSON null would leave the params helper a nil map
		}
		body, _ := json.Marshal(tc.body)
		req := httptest.NewRequest(r.method, tc.target, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, resp.StatusCode, tc.want)
		}
	}
}
