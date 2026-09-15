package collector

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestInventoryHeartbeatWithinGrace pins the relationship between the two
// windows the gated inventory upserts depend on.
//
// What actually keeps a live row from being pruned is the ORDERING, not the
// margin: the upsert and the prune are adjacent statements in the same block,
// and the gate's own condition means that the instant the upsert returns, every
// row in the payload has age(last_seen_at) < inventoryHeartbeat. The prune only
// deletes past staleInventoryGrace. So as long as the heartbeat is the smaller
// of the two, no row the node is still reporting can be pruned — whatever the
// sweep interval, however late a tick runs, however long a sweep takes.
//
// The margin's job is narrower but still real: it keeps the two thresholds from
// meeting at their boundary. With heartbeat == grace, a row sitting a hair past
// the heartbeat may miss the upsert's now() and then be caught by the prune's
// now() microseconds later, deleting and re-inserting a row that never changed —
// which is worse than the churn the gate removes, since an INSERT is never HOT
// and rewrites every index entry.
//
// Both values are compile-time constants, so this is a drift guard: it fails if
// someone redefines inventoryHeartbeat as an absolute literal, or retunes the
// grace window without it. It asserts nothing about SQL behaviour — that is
// TestNodeInventoryUpdatedAt_MovesOnlyOnContentChange in internal/db.
func TestInventoryHeartbeatWithinGrace(t *testing.T) {
	if inventoryHeartbeat >= staleInventoryGrace {
		t.Fatalf("inventoryHeartbeat (%s) >= staleInventoryGrace (%s): the stale prune "+
			"would reach rows the heartbeat has not refreshed, churning inventory through "+
			"delete/re-insert", inventoryHeartbeat, staleInventoryGrace)
	}
	if inventoryHeartbeat > staleInventoryGrace/2 {
		t.Errorf("inventoryHeartbeat (%s) leaves too little margin under staleInventoryGrace (%s); "+
			"keep it at or below half the grace window so the two thresholds cannot meet at the boundary",
			inventoryHeartbeat, staleInventoryGrace)
	}
}

// TestInventoryPayloadTagsMatchQueryColumns is the contract between the Go row
// structs and the jsonb_to_recordset column lists they are expanded into.
//
// Nothing else checks it. The collector mocks accept any payload bytes, and the
// DB-backed test in internal/db declares its own local struct, so a single typo
// in a `json:"..."` tag compiles, lints, and passes the whole suite — then, in
// production, the key no longer matches its column, the column comes back NULL,
// and every one of them is NOT NULL. The statement aborts on every sweep. The
// node's inventory is never refreshed again, and the write is silent: a Warn
// line is the only trace.
//
// Adding `omitempty` to any of these tags is the same bug with a narrower
// trigger — the key vanishes only for zero values, so it survives testing with
// populated fixtures and fails on the first empty string from Proxmox.
func TestInventoryPayloadTagsMatchQueryColumns(t *testing.T) {
	cases := []struct {
		query string
		param string
		row   any
	}{
		{"node_pci_devices", "devices", nodePCIDeviceRow{}},
		{"node_network_interfaces", "interfaces", nodeNetworkInterfaceRow{}},
		{"node_disks", "disks", nodeDiskRow{}},
	}

	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			path := filepath.Join("..", "..", "queries", tc.query+".sql")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}

			re := regexp.MustCompile(`jsonb_to_recordset\(@` + tc.param + `::jsonb\) AS \w+\(([^)]*)\)`)
			m := re.FindStringSubmatch(string(raw))
			if m == nil {
				t.Fatalf("no jsonb_to_recordset(@%s::jsonb) column list in %s; the query's shape "+
					"changed and this contract is no longer being checked", tc.param, path)
			}
			var want []string
			for _, col := range strings.Split(m[1], ",") {
				if f := strings.Fields(col); len(f) > 0 {
					want = append(want, f[0])
				}
			}

			encoded, err := json.Marshal(tc.row)
			if err != nil {
				t.Fatalf("marshal %T: %v", tc.row, err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("unmarshal %T: %v", tc.row, err)
			}
			got := make([]string, 0, len(decoded))
			for k := range decoded {
				got = append(got, k)
			}

			sort.Strings(got)
			sorted := append([]string(nil), want...)
			sort.Strings(sorted)

			if len(want) == 0 {
				t.Fatalf("parsed no columns from %s; this test would pass vacuously", path)
			}
			if strings.Join(got, ",") != strings.Join(sorted, ",") {
				t.Errorf("%T marshals keys %v but %s expands columns %v; a key that does not match "+
					"its column arrives as NULL and every column is NOT NULL, so the statement aborts "+
					"on every sweep", tc.row, got, tc.query, sorted)
			}
		})
	}
}

// TestUpsertInventoryBatchReportsFailure pins the return contract the stale
// prunes read. Each caller runs its DeleteStaleNode* only when this returns
// true, because the batch is all-or-nothing: if the statement aborted, NO row
// for that node was refreshed, they all age past the grace window together, and
// an unconditional prune would wipe the node's entire inventory and keep wiping
// it every sweep while the node is perfectly healthy.
//
// The empty case must report success — nothing failed, and rows the node has
// stopped reporting still need to age out, which is what the old per-row loop
// did by iterating zero times.
func TestUpsertInventoryBatchReportsFailure(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tests := []struct {
		name   string
		rows   []nodeDiskRow
		exec   func(json.RawMessage) error
		want   bool
		called bool
	}{
		{
			name: "successful batch prunes",
			rows: []nodeDiskRow{{DevPath: "/dev/sda"}},
			exec: func(json.RawMessage) error { return nil },
			want: true, called: true,
		},
		{
			name: "failed batch must not prune",
			rows: []nodeDiskRow{{DevPath: "/dev/sda"}},
			exec: func(json.RawMessage) error { return errors.New("null value violates not-null constraint") },
			want: false, called: true,
		},
		{
			name: "empty batch prunes without executing",
			rows: nil,
			exec: func(json.RawMessage) error { return errors.New("must not be called") },
			want: true, called: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			executed := false
			got := upsertInventoryBatch(logger, "pve-01", "node disks", tc.rows,
				func(p json.RawMessage) error {
					executed = true
					return tc.exec(p)
				})
			if got != tc.want {
				t.Errorf("upsertInventoryBatch = %v, want %v", got, tc.want)
			}
			if executed != tc.called {
				t.Errorf("exec called = %v, want %v", executed, tc.called)
			}
		})
	}
}
