package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// fakeNodeLookup answers the node lookup with err, recording what it was
// asked.
type fakeNodeLookup struct {
	err   error
	asked db.GetNodeByClusterAndNameParams
}

func (f *fakeNodeLookup) GetNodeByClusterAndName(_ context.Context, arg db.GetNodeByClusterAndNameParams) (db.Node, error) {
	f.asked = arg
	return db.Node{}, f.err
}

// Only "no such row" means the node is not the cluster's; any other failure
// of the lookup must not let a Proxmox call through as if it were.
func TestNodeMembership(t *testing.T) {
	cluster := uuid.New()

	found := &fakeNodeLookup{}
	member, err := nodeMembership(found, cluster)(context.Background(), "pve-01")
	if err != nil || !member {
		t.Errorf("found: member = %v, err = %v; want true, nil", member, err)
	}
	if found.asked.ClusterID != cluster || found.asked.Name != "pve-01" {
		t.Errorf("asked %+v, want cluster %s and node pve-01", found.asked, cluster)
	}

	member, err = nodeMembership(&fakeNodeLookup{err: pgx.ErrNoRows}, cluster)(context.Background(), "pve-09")
	if err != nil || member {
		t.Errorf("no row: member = %v, err = %v; want false, nil", member, err)
	}

	member, err = nodeMembership(&fakeNodeLookup{err: errors.New("connection reset")}, cluster)(context.Background(), "pve-01")
	var fe *fiber.Error
	if member || !errors.As(err, &fe) || fe.Code != fiber.StatusInternalServerError {
		t.Errorf("failed lookup: member = %v, err = %v; want false and a 500", member, err)
	}
}

// clusterNodes is a NodeLookup holding a fixed set of nodes per cluster,
// recording every question in order. err, when set, answers every question.
type clusterNodes struct {
	members map[uuid.UUID][]string
	err     error
	asked   []db.GetNodeByClusterAndNameParams
}

func (f *clusterNodes) GetNodeByClusterAndName(_ context.Context, arg db.GetNodeByClusterAndNameParams) (db.Node, error) {
	f.asked = append(f.asked, arg)
	if f.err != nil {
		return db.Node{}, f.err
	}
	if slices.Contains(f.members[arg.ClusterID], arg.Name) {
		return db.Node{ClusterID: arg.ClusterID, Name: arg.Name}, nil
	}
	return db.Node{}, pgx.ErrNoRows
}

// requireNodesResult is what one request through RequireNodesInCluster came
// back with.
type requireNodesResult struct {
	status  int
	message string
	reached bool
}

// runRequireNodes calls RequireNodesInCluster from a route shaped like the
// ones the registry runs it on — the cluster in :cluster_id — and reports
// whether the handler behind it went on.
func runRequireNodes(t *testing.T, q NodeLookup, clusterID string, nodes ...string) requireNodesResult {
	t.Helper()
	var got requireNodesResult
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Get("/api/v1/clusters/:cluster_id/probe", func(c fiber.Ctx) error {
		if err := RequireNodesInCluster(c, q, nodes); err != nil {
			return err
		}
		got.reached = true
		return c.SendStatus(http.StatusOK)
	})
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID+"/probe", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var env struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &env)
	got.status, got.message = resp.StatusCode, env.Message
	return got
}

// RequireNodesInCluster is the check the registry runs for every node a URL
// names and the task routes run for the node in a UPID. Each outcome has to
// stop the request except "every node is the cluster's", and the refusals
// have to say which of the three things happened.
func TestRequireNodesInCluster(t *testing.T) {
	cluster := uuid.New()
	other := uuid.New()
	members := map[uuid.UUID][]string{cluster: {"pve-01", "pve-02"}, other: {"pve-03"}}

	t.Run("a member is let through, asked of the path's cluster", func(t *testing.T) {
		q := &clusterNodes{members: members}
		got := runRequireNodes(t, q, cluster.String(), "pve-01")
		if got.status != http.StatusOK || !got.reached {
			t.Fatalf("got %+v, want 200 and the handler reached", got)
		}
		want := []db.GetNodeByClusterAndNameParams{{ClusterID: cluster, Name: "pve-01"}}
		if !slices.Equal(q.asked, want) {
			t.Errorf("asked %+v, want %+v", q.asked, want)
		}
	})

	t.Run("every node of several is asked about, in order", func(t *testing.T) {
		q := &clusterNodes{members: members}
		if got := runRequireNodes(t, q, cluster.String(), "pve-01", "pve-02"); got.status != http.StatusOK || !got.reached {
			t.Fatalf("got %+v, want 200", got)
		}
		want := []db.GetNodeByClusterAndNameParams{{ClusterID: cluster, Name: "pve-01"}, {ClusterID: cluster, Name: "pve-02"}}
		if !slices.Equal(q.asked, want) {
			t.Errorf("asked %+v, want %+v", q.asked, want)
		}
	})

	for _, tt := range []struct {
		name  string
		nodes []string
	}{
		// The shape the check exists for: a name that resolves somewhere.
		{"an address", []string{"192.0.2.10"}},
		{"a name that is another cluster's node", []string{"pve-03"}},
		{"a member in another case", []string{"PVE-01"}},
		{"a member followed by a stranger", []string{"pve-01", "pve-09"}},
	} {
		t.Run("refused: "+tt.name, func(t *testing.T) {
			got := runRequireNodes(t, &clusterNodes{members: members}, cluster.String(), tt.nodes...)
			if got.status != http.StatusNotFound || got.message != "Node not found in this cluster" || got.reached {
				t.Errorf("got %+v, want 404 \"Node not found in this cluster\" with the handler not reached", got)
			}
		})
	}

	t.Run("a lookup that fails is a 500, never a member", func(t *testing.T) {
		got := runRequireNodes(t, &clusterNodes{err: errors.New("connection reset")}, cluster.String(), "pve-01")
		if got.status != http.StatusInternalServerError || got.message != "Failed to look up the node" || got.reached {
			t.Errorf("got %+v, want 500 \"Failed to look up the node\" with the handler not reached", got)
		}
	})

	t.Run("an unreadable cluster id is refused before any lookup", func(t *testing.T) {
		q := &clusterNodes{members: members}
		got := runRequireNodes(t, q, "not-a-cluster", "pve-01")
		if got.status != http.StatusBadRequest || got.reached {
			t.Errorf("got %+v, want 400 with the handler not reached", got)
		}
		if len(q.asked) != 0 {
			t.Errorf("asked %+v about a cluster that could not be read", q.asked)
		}
	})

	t.Run("no lookup is a 500, not a pass", func(t *testing.T) {
		got := runRequireNodes(t, nil, cluster.String(), "pve-01")
		if got.status != http.StatusInternalServerError || got.reached {
			t.Errorf("got %+v, want 500 with the handler not reached", got)
		}
	})

	t.Run("no node named asks nothing", func(t *testing.T) {
		q := &clusterNodes{members: members}
		if got := runRequireNodes(t, q, cluster.String()); got.status != http.StatusOK || !got.reached {
			t.Errorf("got %+v, want 200", got)
		}
		if len(q.asked) != 0 {
			t.Errorf("asked %+v with no node to check", q.asked)
		}
	})
}
