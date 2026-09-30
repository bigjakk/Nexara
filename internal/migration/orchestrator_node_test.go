package migration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// jobDB is a db.DBTX for the node-membership tests: it holds one migration
// job and the nodes of each cluster, records every statement by its sqlc name,
// and answers GetCluster — the read behind clientForCluster, which is the first
// step towards Proxmox — by failing the test.
type jobDB struct {
	t        *testing.T
	job      db.MigrationJob
	members  map[uuid.UUID][]string
	lookErr  error
	mu       sync.Mutex
	names    []string
	lookedAt []string // "cluster/node" for each membership question
	execs    map[string][]any
}

var sqlcName = regexp.MustCompile(`-- name: (\w+)`)

func (d *jobDB) record(sql string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	name := ""
	if m := sqlcName.FindStringSubmatch(sql); m != nil {
		name = m[1]
	}
	d.names = append(d.names, name)
	return name
}

func (d *jobDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	name := d.record(sql)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.execs == nil {
		d.execs = map[string][]any{}
	}
	d.execs[name] = args
	return pgconn.CommandTag{}, nil
}

func (d *jobDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	d.t.Errorf("unexpected query %s", d.record(sql))
	return nil, errors.New("unexpected")
}

func (d *jobDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	switch name := d.record(sql); name {
	case "GetMigrationJob":
		return structRow{t: d.t, v: d.job}
	case "GetVMByClusterAndVmid":
		return structRow{err: pgx.ErrNoRows}
	case "GetNodeByClusterAndName":
		cluster, _ := args[0].(uuid.UUID)
		node, _ := args[1].(string)
		d.mu.Lock()
		d.lookedAt = append(d.lookedAt, cluster.String()+"/"+node)
		d.mu.Unlock()
		switch {
		case d.lookErr != nil:
			return structRow{err: d.lookErr}
		case slices.Contains(d.members[cluster], node):
			return structRow{t: d.t, v: db.Node{ClusterID: cluster, Name: node}}
		}
		return structRow{err: pgx.ErrNoRows}
	default:
		d.t.Errorf("%s was asked: the job reached for a Proxmox client (or read something else) "+
			"with a node the cluster does not hold", name)
		return structRow{err: errors.New("unexpected")}
	}
}

// structRow scans v's fields into the destinations in order, which is how
// sqlc scans a SELECT * row; a count mismatch means the fake is out of step.
type structRow struct {
	t   *testing.T
	v   any
	err error
}

func (r structRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	v := reflect.ValueOf(r.v)
	if len(dest) != v.NumField() {
		r.t.Fatalf("scanned %d columns into a %T with %d fields", len(dest), r.v, v.NumField())
	}
	for i := range dest {
		reflect.ValueOf(dest[i]).Elem().Set(v.Field(i))
	}
	return nil
}

// TestJobNodes_ANodeTheClusterDoesNotHoldIsNeverSentToProxmox: a migration
// job's nodes are what the caller named at creation, and pre-flight and
// Execute both send them to Proxmox by name, which pveproxy resolves and dials
// whatever it names. A job naming a node its cluster does not hold — the
// TARGET cluster, for the target — fails before any Proxmox client is built,
// with the reason where the operator reads it: the pre-flight report, or the
// job's error_message.
func TestJobNodes_ANodeTheClusterDoesNotHoldIsNeverSentToProxmox(t *testing.T) {
	src, tgt := uuid.New(), uuid.New()
	members := map[uuid.UUID][]string{src: {"pve-01"}, tgt: {"pve-02"}}

	for _, tt := range []struct {
		name       string
		source     string
		target     string
		lookErr    error
		wantAsked  []string
		wantReason string
	}{
		{
			name: "a source the source cluster does not hold", source: "192.0.2.10", target: "pve-02",
			wantAsked:  []string{src.String() + "/192.0.2.10"},
			wantReason: `node "192.0.2.10" is not one of this cluster's nodes; it was not contacted`,
		},
		{
			// pve-01 is a member — of the SOURCE cluster. A target checked
			// against the wrong cluster would let it through.
			name: "a target only the source cluster holds", source: "pve-01", target: "pve-01",
			wantAsked:  []string{src.String() + "/pve-01", tgt.String() + "/pve-01"},
			wantReason: `node "pve-01" is not one of this cluster's nodes; it was not contacted`,
		},
		{
			name: "a lookup that fails", source: "pve-01", target: "pve-02", lookErr: errors.New("connection refused"),
			wantAsked:  []string{src.String() + "/pve-01"},
			wantReason: `could not confirm node "pve-01" is one of this cluster's nodes; it was not contacted`,
		},
	} {
		job := db.MigrationJob{
			ID:              uuid.New(),
			SourceClusterID: src,
			TargetClusterID: tgt,
			SourceNode:      tt.source,
			TargetNode:      tt.target,
			Vmid:            100,
			VmType:          VMTypeQEMU,
			MigrationType:   TypeCrossCluster,
			MigrationMode:   ModeLive,
		}
		newOrch := func(t *testing.T) (*Orchestrator, *jobDB) {
			d := &jobDB{t: t, job: job, members: members, lookErr: tt.lookErr}
			return NewOrchestrator(db.New(d), "", slog.New(slog.NewTextHandler(io.Discard, nil)), nil), d
		}

		t.Run(tt.name+": pre-flight", func(t *testing.T) {
			o, d := newOrch(t)
			report, err := o.RunPreFlight(context.Background(), job.ID)
			if err != nil {
				t.Fatalf("RunPreFlight = %v, want a failed report", err)
			}
			if report.Passed || len(report.Checks) != 1 || report.Checks[0].Severity != SeverityFail ||
				report.Checks[0].Message != tt.wantReason {
				t.Errorf("report = %+v, want one failed check saying %q", report, tt.wantReason)
			}
			if !slices.Equal(d.lookedAt, tt.wantAsked) {
				t.Errorf("asked %v, want %v", d.lookedAt, tt.wantAsked)
			}
			saved, ok := d.execs["UpdateMigrationJobChecks"]
			if !ok {
				t.Fatalf("the failed report was never saved; statements: %v", d.names)
			}
			var stored PreFlightReport
			if raw, _ := saved[1].([]byte); json.Unmarshal(raw, &stored) != nil || stored.Passed {
				t.Errorf("saved report %s, want the failed one", saved[1])
			}
			if status, _ := saved[2].(string); status != StatusFailed {
				t.Errorf("saved status %q, want %q", status, StatusFailed)
			}
		})

		t.Run(tt.name+": execute", func(t *testing.T) {
			o, d := newOrch(t)
			o.Execute(context.Background(), job.ID, uuid.New())
			if !slices.Equal(d.lookedAt, tt.wantAsked) {
				t.Errorf("asked %v, want %v", d.lookedAt, tt.wantAsked)
			}
			done, ok := d.execs["CompleteMigrationJob"]
			if !ok {
				t.Fatalf("the job was never failed; statements: %v", d.names)
			}
			var status, msg string
			for _, a := range done {
				if s, isString := a.(string); isString {
					switch {
					case s == StatusFailed:
						status = s
					case strings.Contains(s, "node"):
						msg = s
					}
				}
			}
			if status != StatusFailed || msg != tt.wantReason {
				t.Errorf("CompleteMigrationJob(%v), want status %q and error %q", done, StatusFailed, tt.wantReason)
			}
		})
	}

	t.Run("members pass", func(t *testing.T) {
		d := &jobDB{t: t, members: members}
		o := NewOrchestrator(db.New(d), "", slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
		if err := o.requireJobNodes(context.Background(), db.MigrationJob{
			SourceClusterID: src, TargetClusterID: tgt, SourceNode: "pve-01", TargetNode: "pve-02",
		}); err != nil {
			t.Errorf("requireJobNodes(members) = %v, want nil", err)
		}
		if want := []string{src.String() + "/pve-01", tgt.String() + "/pve-02"}; !slices.Equal(d.lookedAt, want) {
			t.Errorf("asked %v, want %v", d.lookedAt, want)
		}
	})
}
