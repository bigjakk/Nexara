package handlers

import (
	"context"
	"encoding/json"
	"errors"
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

// rollingSSHDBTX answers CreateJob up to its SSH check as the real tables
// would: no job is running, the cluster has credentials, only pve-02 has an
// address, and no host key is pinned. Anything else, an insert above all,
// fails the test.
type rollingSSHDBTX struct {
	t       *testing.T
	cluster uuid.UUID
}

func (d *rollingSSHDBTX) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	var name string
	if m := sqlcQueryName.FindStringSubmatch(sql); m != nil {
		name = m[1]
	}
	switch name {
	case "HasRunningJobForCluster":
		return replayRow{struct{ B bool }{false}}
	case "HasClusterSSHCredentials":
		return replayRow{struct{ B bool }{true}}
	case "GetClusterSSHCredentials":
		return replayRow{db.ClusterSshCredential{ClusterID: d.cluster, Username: "root", Port: 22}}
	case "GetNodeAddressByName":
		if node, _ := args[1].(string); node == "pve-02" {
			return replayRow{struct{ A string }{"192.0.2.12"}}
		}
		return failRow{pgx.ErrNoRows}
	case "GetSSHKnownHost":
		return failRow{pgx.ErrNoRows}
	}
	d.t.Errorf("unexpected query row %s", name)
	return failRow{errors.New("unexpected")}
}

func (d *rollingSSHDBTX) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	d.t.Errorf("unexpected write: %.80s", sql)
	return pgconn.CommandTag{}, errors.New("unexpected")
}

func (d *rollingSSHDBTX) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	d.t.Errorf("unexpected query: %.80s", sql)
	return nil, errors.New("unexpected")
}

// An automated job naming a node its upgrade could not reach over SSH is
// refused before anything touches the cluster, naming each such node and why,
// without the node address rolling.NodeSSHConfig's error carries.
func TestCreateJob_RefusesAnAutomatedJobItsUpgradeCouldNotReach(t *testing.T) {
	t.Parallel()
	cluster := uuid.New()
	h := NewRollingUpdateHandler(db.New(&rollingSSHDBTX{t: t, cluster: cluster}), "", nil, nil)
	mirror := compiledMirror(t, apischema.Properties{
		"cluster_id":   {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
		"nodes":        {Type: apischema.Array, Items: &apischema.Property{Type: apischema.String}},
		"parallelism":  {Type: apischema.Integer, Optional: true, Default: 1},
		"auto_upgrade": {Type: apischema.Boolean, Optional: true, Default: false},
	})
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Post("/clusters/:cluster_id/rolling-updates", withRequestParams(t, mirror, []string{"cluster_id"}, h.CreateJob))

	req := httptest.NewRequest(http.MethodPost, "/clusters/"+cluster.String()+"/rolling-updates",
		strings.NewReader(`{"nodes":["pve-01","pve-02"],"auto_upgrade":true}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct{ Message string }
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	want := "Automated upgrade cannot reach pve-01 (no IP address is known for it yet), " +
		"pve-02 (its SSH host key is not pinned) over SSH."
	if resp.StatusCode != http.StatusBadRequest || !strings.HasPrefix(body.Message, want) {
		t.Fatalf("got %d %q, want 400 starting %q", resp.StatusCode, body.Message, want)
	}
	if strings.Contains(body.Message, "192.0.2.") {
		t.Errorf("the refusal names a node address: %q", body.Message)
	}
}
