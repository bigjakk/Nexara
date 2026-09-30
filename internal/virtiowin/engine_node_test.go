package virtiowin

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/nodemember"
)

// nodeLookupDB is a db.DBTX that answers only GetNodeByClusterAndName: a row
// for member in cluster, no row for anything else, or err for everything —
// and records FinishVirtioWinDownload, the one write a refused download row
// makes. Any other statement fails the test, so pickNode cannot reach for
// another query (the storage-based fallback) when a node is configured, and
// nothing can reach for a Proxmox client (the cluster row read).
type nodeLookupDB struct {
	t        *testing.T
	cluster  uuid.UUID
	member   string
	err      error
	asked    []string
	finishes [][]any // the arguments of each FinishVirtioWinDownload
}

func (d *nodeLookupDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "-- name: FinishVirtioWinDownload ") {
		d.finishes = append(d.finishes, args)
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	d.t.Errorf("unexpected statement: %s", sql)
	return pgconn.CommandTag{}, errors.New("unexpected")
}

func (d *nodeLookupDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	d.t.Errorf("unexpected query: %s", sql)
	return nil, errors.New("unexpected")
}

func (d *nodeLookupDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if !strings.Contains(sql, "-- name: GetNodeByClusterAndName ") {
		d.t.Errorf("unexpected query row: %s", sql)
		return lookupRow{errors.New("unexpected")}
	}
	cluster, _ := args[0].(uuid.UUID)
	name, _ := args[1].(string)
	d.asked = append(d.asked, name)
	switch {
	case d.err != nil:
		return lookupRow{d.err}
	case cluster == d.cluster && name == d.member:
		return lookupRow{}
	}
	return lookupRow{pgx.ErrNoRows}
}

type lookupRow struct{ err error }

func (r lookupRow) Scan(...any) error { return r.err }

// TestPickNode_RefusesAConfiguredNodeTheClusterDoesNotHold: the configured
// node goes to Proxmox as /nodes/{node}/…, which pveproxy resolves and dials
// whatever it names, so pickNode — the one place both download paths and the
// scheduled check get their node from — hands back only a member, and says
// why otherwise, in words fit for last_error.
func TestPickNode_RefusesAConfiguredNodeTheClusterDoesNotHold(t *testing.T) {
	cluster := uuid.New()
	for _, tt := range []struct {
		name    string
		node    string
		err     error
		want    string
		wantErr func(error) bool
	}{
		{name: "a member", node: "pve-01", want: "pve-01"},
		{
			name:    "an address the cluster does not hold",
			node:    "192.0.2.10",
			wantErr: func(err error) bool { return errors.As(err, new(*nodemember.NotMemberError)) },
		},
		{
			name: "the member, when the lookup fails",
			node: "pve-01",
			err:  errors.New("connection refused"),
			wantErr: func(err error) bool {
				return errors.As(err, new(*nodemember.LookupError)) && !strings.Contains(err.Error(), "connection refused")
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := &nodeLookupDB{t: t, cluster: cluster, member: "pve-01", err: tt.err}
			e := &Engine{queries: db.New(fake), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

			got, err := e.pickNode(context.Background(), db.VirtioWinConfig{ClusterID: cluster, Node: tt.node, Storage: "local"})

			if len(fake.asked) != 1 || fake.asked[0] != tt.node {
				t.Errorf("asked about %v, want exactly %q", fake.asked, tt.node)
			}
			if tt.wantErr == nil {
				if err != nil || got != tt.want {
					t.Errorf("pickNode = %q, %v; want %q", got, err, tt.want)
				}
				return
			}
			if got != "" || err == nil || !tt.wantErr(err) {
				t.Errorf("pickNode = %q, %v; want no node and the refusal", got, err)
			}
		})
	}
}

// TestReconcileOne_NeverPollsANodeTheClusterDoesNotHold: a download row's node
// is where it was dispatched, and the reconcile polls it — and prunes on it —
// by name. A row naming a node the cluster does not hold (one written before
// pickNode checked) is failed with the reason, and no Proxmox client is built;
// a lookup that fails leaves the row for the next pass.
func TestReconcileOne_NeverPollsANodeTheClusterDoesNotHold(t *testing.T) {
	cluster := uuid.New()
	for _, tt := range []struct {
		name       string
		err        error
		wantFinish bool
	}{
		{name: "an address the cluster does not hold", wantFinish: true},
		{name: "a failed lookup", err: errors.New("connection refused")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := &nodeLookupDB{t: t, cluster: cluster, member: "pve-01", err: tt.err}
			e := &Engine{queries: db.New(fake), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			row := db.VirtioWinDownload{ID: uuid.New(), ClusterID: cluster, Node: "192.0.2.10",
				Upid: "UPID:192.0.2.10:0000A1B2:0001C3D4:66F4E3C0:download:virtio-win.iso:root@pam:"}

			e.reconcileOne(context.Background(), row)

			if len(fake.asked) != 1 || fake.asked[0] != row.Node {
				t.Errorf("asked about %v, want exactly %q", fake.asked, row.Node)
			}
			if !tt.wantFinish {
				if len(fake.finishes) != 0 {
					t.Errorf("finished the row %v on a failed lookup", fake.finishes)
				}
				return
			}
			want := `node "192.0.2.10" is not one of this cluster's nodes; it was not contacted`
			if len(fake.finishes) != 1 || fake.finishes[0][1] != "failed" || fake.finishes[0][2] != want {
				t.Errorf("finished with %v, want one (failed, %q)", fake.finishes, want)
			}
		})
	}
}
