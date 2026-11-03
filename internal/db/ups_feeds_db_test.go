package db

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// Fixed ids so a run that aborts before its purge leaves rows this run's
// up-front purge can find.
var (
	upsFeedUser    = uuid.MustParse("a7000000-0000-4000-8000-000000000001")
	upsFeedCluster = uuid.MustParse("a7000000-0000-4000-8000-000000000002")
	upsFeedServer  = uuid.MustParse("a7000000-0000-4000-8000-000000000003")
	upsFeedDevice  = uuid.MustParse("a7000000-0000-4000-8000-000000000004")
)

// What a UPS powers (000132) is held by the schema: one feed per node and per
// label, only a node can be found, and a removed node stays removed until it
// is added back by hand. Logins count their connections and the listings in a
// row they missed. A deleted server takes its devices' feeds and logins along;
// a deleted cluster, its nodes' feeds and a server's host. Skipped unless
// NEXARA_TEST_DB_URL names a throwaway database.
func TestUpsFeedsAndClients(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)

	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM nut_servers WHERE id = $1`, upsFeedServer)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, upsFeedCluster)
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, upsFeedUser)
	}
	purge()
	defer purge()
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	exec("user", `INSERT INTO users (id, email, password_hash) VALUES ($1, 'ups-feeds@example.invalid', '')`, upsFeedUser)
	exec("cluster", `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
		VALUES ($1, 'ups-feeds-fixture', 'https://pve.invalid:8006', 'root@pam!t', 'enc')`, upsFeedCluster)
	exec("server", `INSERT INTO nut_servers (id, name, host, host_cluster_id, host_node_name)
		VALUES ($1, 'ups-feeds-fixture', '192.0.2.40', $2, 'pve-01')`, upsFeedServer, upsFeedCluster)
	exec("device", `INSERT INTO ups_devices (id, server_id, ups_name) VALUES ($1, $2, 'ups01')`, upsFeedDevice, upsFeedServer)
	exec("nodes", `INSERT INTO nodes (cluster_id, name, status, last_seen_at) VALUES
		($1, 'pve-01', 'online', now()), ($1, 'pve-02', 'online', now() - interval '10 minutes')`, upsFeedCluster)
	q := gen.New(pool)

	found := gen.InsertFoundUpsFeedParams{UpsID: upsFeedDevice, ClusterID: upsFeedCluster, NodeName: "pve-01"}
	for i, want := range []int64{1, 0} { // found again: left as it is
		if n, err := q.InsertFoundUpsFeed(ctx, found); err != nil || n != want {
			t.Fatalf("found feed, attempt %d = %d, %v; want %d", i+1, n, err, want)
		}
	}
	manual := func(node, label string) (gen.UpsFeed, error) {
		p := gen.InsertManualUpsFeedParams{UpsID: upsFeedDevice, Label: label, CreatedBy: upsFeedUser}
		if node != "" {
			p.ClusterID = pgtype.UUID{Bytes: upsFeedCluster, Valid: true}
			p.NodeName = pgtype.Text{String: node, Valid: true}
		}
		return q.InsertManualUpsFeed(ctx, p)
	}
	if _, err := manual("pve-02", ""); err != nil {
		t.Fatalf("manual node feed: %v", err)
	}
	if _, err := manual("", "Core switch"); err != nil {
		t.Fatalf("manual label feed: %v", err)
	}
	for _, tc := range []struct {
		name, node, label, code string // code "" means no row comes back
	}{
		{"a node already fed", "pve-01", "", ""},
		{"a label differing only in case", "", "core SWITCH", "23505"},
		{"a node and a label at once", "pve-03", "Core switch", "23514"},
		{"neither", "", "", "23514"},
	} {
		_, err := manual(tc.node, tc.label)
		var pgErr *pgconn.PgError
		if tc.code == "" && !errors.Is(err, pgx.ErrNoRows) || tc.code != "" && (!errors.As(err, &pgErr) || pgErr.Code != tc.code) {
			t.Errorf("%s: err = %v, want %q (empty: no row)", tc.name, err, tc.code)
		}
	}
	var pgErr *pgconn.PgError
	if _, err := pool.Exec(ctx, `INSERT INTO ups_feeds (ups_id, label, source) VALUES ($1, 'PDU', 'found')`, upsFeedDevice); !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Errorf("a found label: err = %v, want SQLSTATE 23514", err)
	}

	// The operator removes the found node: its login no longer finds it, and
	// adding it by hand brings it back.
	listed := func() (nodes []string) {
		t.Helper()
		rows, err := q.ListUpsFeeds(ctx, time.Now().Add(-2*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.UpsID == upsFeedDevice && r.NodeName.Valid {
				nodes = append(nodes, r.NodeName.String+":"+r.Source)
			}
		}
		return nodes
	}
	all, err := q.ListUpsFeeds(ctx, time.Now().Add(-2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	pve01 := all[slices.IndexFunc(all, func(f gen.ListUpsFeedsRow) bool { return f.NodeName.String == "pve-01" })]
	if _, err := q.SetUpsFeedProtectedElsewhere(ctx, gen.SetUpsFeedProtectedElsewhereParams{ID: pve01.ID, UpsID: upsFeedDevice, ProtectedElsewhere: true}); err != nil {
		t.Fatalf("protected elsewhere: %v", err)
	}
	if _, err := q.DismissUpsNodeFeed(ctx, gen.DismissUpsNodeFeedParams{ID: pve01.ID, UpsID: upsFeedDevice}); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if n, err := q.InsertFoundUpsFeed(ctx, found); err != nil || n != 0 || !slices.Equal(listed(), []string{"pve-02:manual"}) {
		t.Errorf("found after removal = %d, %v, listed %v; want it to stay removed", n, err, listed())
	}
	// Re-added, it is a fresh claim: "protected another way" does not carry over.
	if f, err := manual("pve-01", ""); err != nil || f.Dismissed || f.ProtectedElsewhere || f.Source != "manual" || !slices.Equal(listed(), []string{"pve-01:manual", "pve-02:manual"}) {
		t.Errorf("added back by hand = %+v, %v, listed %v", f, err, listed())
	}
	// A cluster Nexara cannot reach keeps its nodes' last status: one no full
	// sync has reached since the cutoff is not current, whatever it says.
	feeds, err := q.ListUpsFeeds(ctx, time.Now().Add(-2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	current := map[string]bool{}
	for _, f := range feeds {
		if f.UpsID == upsFeedDevice && f.NodeName.Valid {
			current[f.NodeName.String] = f.NodeStatusCurrent
		}
	}
	if !current["pve-01"] || current["pve-02"] {
		t.Errorf("node status current = %v; want pve-01 only", current)
	}

	// Four listings: two connections from one address, one address gone,
	// nobody (a nil slice, which SQL sees as NULL), then the address back.
	type client struct {
		addr                string
		connections, missed int16
	}
	for _, step := range []struct {
		addrs []string
		want  []client
	}{
		{[]string{"192.0.2.11", "192.0.2.12", "192.0.2.11"}, []client{{"192.0.2.11", 2, 0}, {"192.0.2.12", 1, 0}}},
		{[]string{"192.0.2.11"}, []client{{"192.0.2.11", 1, 0}, {"192.0.2.12", 0, 1}}},
		{nil, []client{{"192.0.2.11", 0, 1}, {"192.0.2.12", 0, 2}}},
		{[]string{"192.0.2.12"}, []client{{"192.0.2.11", 0, 2}, {"192.0.2.12", 1, 0}}},
	} {
		if err := q.UpsertUpsClients(ctx, gen.UpsertUpsClientsParams{UpsID: upsFeedDevice, Addresses: step.addrs, SeenAt: time.Now()}); err != nil {
			t.Fatalf("UpsertUpsClients(%v): %v", step.addrs, err)
		}
		if err := q.MarkUpsClientsMissed(ctx, gen.MarkUpsClientsMissedParams{UpsID: upsFeedDevice, Addresses: step.addrs}); err != nil {
			t.Fatalf("MarkUpsClientsMissed(%v): %v", step.addrs, err)
		}
		rows, err := q.ListUpsClients(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var got []client
		for _, r := range rows {
			if r.UpsID == upsFeedDevice {
				got = append(got, client{r.Address, r.Connections, r.Missed})
			}
		}
		if !slices.Equal(got, step.want) {
			t.Errorf("after listing %v: clients = %v, want %v", step.addrs, got, step.want)
		}
	}

	count := func(sql string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, sql, upsFeedDevice).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// upsd output is untrusted: a listing of made-up addresses keeps only the
	// newest 256 logins per UPS.
	flood := make([]string, 300)
	for i := range flood {
		flood[i] = fmt.Sprintf("198.51.100.%d", i%250) + fmt.Sprintf("%c", 'a'+rune(i/250))
	}
	if err := q.UpsertUpsClients(ctx, gen.UpsertUpsClientsParams{UpsID: upsFeedDevice, Addresses: flood, SeenAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := q.PruneUpsClients(ctx, gen.PruneUpsClientsParams{UpsID: upsFeedDevice, Before: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM ups_clients WHERE ups_id = $1`); n != 256 {
		t.Errorf("after a flood: %d logins stored, want 256", n)
	}

	// A new address forgets the host node, so the poller finds it again; the
	// same address keeps it.
	hostSet := func() bool {
		t.Helper()
		var set bool
		if err := pool.QueryRow(ctx, `SELECT host_cluster_id IS NOT NULL FROM nut_servers WHERE id = $1`, upsFeedServer).Scan(&set); err != nil {
			t.Fatal(err)
		}
		return set
	}
	update := func(addr string) {
		t.Helper()
		if _, err := q.UpdateNutServer(ctx, gen.UpdateNutServerParams{ID: upsFeedServer, Name: "ups-feeds-fixture", Host: addr, Port: 3493, TlsMode: "off", VerifyTls: true, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	if update("192.0.2.40"); !hostSet() {
		t.Error("the same address forgot the host node")
	}
	if update("192.0.2.41"); hostSet() {
		t.Error("a new address kept the host node")
	}
	setHost := func(addr string) error {
		return q.SetNutServerHostNode(ctx, gen.SetNutServerHostNodeParams{
			ID: upsFeedServer, ClusterID: upsFeedCluster, NodeName: "pve-01", Host: addr, Port: 3493})
	}
	// A worker still on the replaced address must not store the old host.
	if err := setHost("192.0.2.40"); err != nil || hostSet() {
		t.Errorf("a worker on the old address stored a host: %v", err)
	}
	if err := setHost("192.0.2.41"); err != nil || !hostSet() {
		t.Fatalf("finding the host again: %v", err)
	}
	exec("delete cluster", `DELETE FROM clusters WHERE id = $1`, upsFeedCluster)
	if n := count(`SELECT count(*) FROM ups_feeds WHERE ups_id = $1`); n != 1 {
		t.Errorf("after the cluster went: %d feeds, want only the label", n)
	}
	var host pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT host_cluster_id FROM nut_servers WHERE id = $1`, upsFeedServer).Scan(&host); err != nil || host.Valid {
		t.Errorf("host cluster after the cluster went = %v, %v; want NULL", host, err)
	}
	exec("delete server", `DELETE FROM nut_servers WHERE id = $1`, upsFeedServer)
	if n := count(`SELECT (SELECT count(*) FROM ups_feeds WHERE ups_id = $1) + (SELECT count(*) FROM ups_clients WHERE ups_id = $1)`); n != 0 {
		t.Errorf("after the server went: %d feeds and logins left, want 0", n)
	}
}

// Node rows are never deleted, so the login matcher leaves out a node its
// cluster stopped listing; staleness is judged against the cluster's own
// latest sync, so a cluster Nexara cannot reach keeps its nodes. Without the
// filter, a removed node's address shadows the node that took it over.
func TestListNodeLoginAddresses_LeavesRemovedNodesOut(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)

	reachable := uuid.MustParse("a7000000-0000-4000-8000-000000000011")
	unreachable := uuid.MustParse("a7000000-0000-4000-8000-000000000012")
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id IN ($1, $2)`, reachable, unreachable)
	}
	purge()
	defer purge()
	for _, s := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
		  VALUES ($1, 'ups-addr-reachable', 'https://pve.invalid:8006', 'root@pam!t', 'enc'),
		         ($2, 'ups-addr-unreachable', 'https://pve2.invalid:8006', 'root@pam!t', 'enc')`, []any{reachable, unreachable}},
		{`INSERT INTO nodes (id, cluster_id, name, address, last_seen_at) VALUES
		  ('a7000000-0000-4000-8000-000000000021', $1, 'pve-01', '192.0.2.61', now()),
		  ('a7000000-0000-4000-8000-000000000022', $1, 'pve-02', '192.0.2.62', now() - interval '2 hours'),
		  ('a7000000-0000-4000-8000-000000000024', $1, 'pve-04', '192.0.2.64', now() - interval '30 minutes'),
		  ('a7000000-0000-4000-8000-000000000023', $2, 'pve-03', '192.0.2.63', now() - interval '1 day')`, []any{reachable, unreachable}},
		{`INSERT INTO node_network_interfaces (node_id, cluster_id, iface, address) VALUES
		  ('a7000000-0000-4000-8000-000000000021', $1, 'vmbr0', '198.51.100.61'),
		  ('a7000000-0000-4000-8000-000000000022', $1, 'vmbr0', '198.51.100.62')`, []any{reachable}},
	} {
		if _, err := pool.Exec(ctx, s.sql, s.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	rows, err := gen.New(pool).ListNodeLoginAddresses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		if r.ClusterID == reachable || r.ClusterID == unreachable {
			got = append(got, r.NodeName+" "+r.Address)
		}
	}
	want := []string{"pve-01 192.0.2.61", "pve-01 198.51.100.61", "pve-03 192.0.2.63", "pve-04 192.0.2.64"}
	if slices.Sort(got); !slices.Equal(got, want) {
		t.Errorf("login addresses = %q, want %q (pve-02 left out; pve-03 and pve-04, within the hour, kept)", got, want)
	}
}
