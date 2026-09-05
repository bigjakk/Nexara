package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// Fixed ids so a run that aborts before its purge leaves rows the next run's
// up-front purge can find.
var (
	m089ClusterA = uuid.MustParse("89000000-0000-4000-8000-000000000001")
	m089ClusterB = uuid.MustParse("89000000-0000-4000-8000-000000000002")
	m089Node     = uuid.MustParse("89000000-0000-4000-8000-000000000003")
	m089Server   = uuid.MustParse("89000000-0000-4000-8000-000000000004")
	m089Platform = uuid.MustParse("89000000-0000-4000-8000-000000000005")
)

// TestMigration089_CorrelatesBackupObjectsToGuests is the behaviour lock for
// migration 000089 and the CorrelateVeeamBackupObjects statement it exists to
// support.
//
// Correlation is the whole point of the Veeam integration: Veeam's console can
// say "job X failed", and only Nexara can say "these guests on THIS cluster
// have no restore point". Every property below is one that, if it broke, would
// make that sentence a lie in a way nothing else would catch — most of them
// silently, and in the direction of reporting a guest protected when it is not.
//
// Skipped unless NEXARA_TEST_DB_URL is set (a throwaway database — never the
// live nexara DB).
func TestMigration089_CorrelatesBackupObjectsToGuests(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	ctx, pool := env.Ctx, env.Pool

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		// veeam_backup_objects, veeam_platforms, nodes and vms all cascade
		// from their parents, so dropping the server and the clusters is
		// enough.
		_, _ = pool.Exec(pctx, `DELETE FROM veeam_servers WHERE id = $1`, m089Server)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`,
			[]uuid.UUID{m089ClusterA, m089ClusterB})
	}
	purge()
	defer purge()

	migrateUp(t, env.Migrate)

	seedCluster := func(id uuid.UUID, name string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			 VALUES ($1, $2, 'https://cluster.invalid:8006', 'test@pam!t', 'x')`, id, name); err != nil {
			t.Fatalf("seed cluster %s: %v", name, err)
		}
	}
	seedCluster(m089ClusterA, "migration-089-a")
	seedCluster(m089ClusterB, "migration-089-b")

	if _, err := pool.Exec(ctx,
		`INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $2, 'pve-01')`,
		m089Node, m089ClusterA); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO veeam_servers (id, name, base_url, username, password_encrypted)
		 VALUES ($1, 'migration-089-vbr', 'https://vbr.invalid:9419', 'ad\jdoe', 'x')`,
		m089Server); err != nil {
		t.Fatalf("seed veeam server: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO veeam_platforms (veeam_server_id, platform_id, display_name, cluster_id)
		 VALUES ($1, $2, 'cluster01', $3)`,
		m089Server, m089Platform, m089ClusterA); err != nil {
		t.Fatalf("seed veeam platform: %v", err)
	}

	// --- guests -------------------------------------------------------------
	seedGuest := func(vmid int32, name, guestType string, cluster uuid.UUID) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO vms (id, cluster_id, node_id, vmid, name, type)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			uuid.New(), cluster, m089Node, vmid, name, guestType); err != nil {
			t.Fatalf("seed guest %d: %v", vmid, err)
		}
	}
	seedSmbios := func(vmid int32, smbios string, cluster uuid.UUID) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO guest_smbios (cluster_id, vmid, smbios_uuid) VALUES ($1, $2, lower($3))`,
			cluster, vmid, smbios); err != nil {
			t.Fatalf("seed smbios %d: %v", vmid, err)
		}
	}

	const (
		uuidWeb01   = "413babd1-94db-4f46-83ed-7c1ba5ab644e"
		uuidDB01    = "5fab8faa-c4cb-4605-ad44-feda61f0e2bf"
		uuidGone    = "316e531d-55c0-4fef-adc2-f1bb9c4e1873"
		uuidRebuilt = "422f6174-ff3a-f9d2-9411-1e7e9690fadf"
		uuidTwin    = "422f73f4-a21c-8cf0-d04e-643a665d7af6"
	)

	seedGuest(100, "web01", "qemu", m089ClusterA) // smbios tier
	seedSmbios(100, uuidWeb01, m089ClusterA)
	// Name tier: SCANNED, and affirmatively found to carry no smbios1 uuid.
	// The empty string is that record — it is what separates "has no uuid"
	// from "not looked at yet".
	seedGuest(101, "db01", "qemu", m089ClusterA)
	seedSmbios(101, "", m089ClusterA)
	seedGuest(102, "app", "qemu", m089ClusterA) // ambiguous name…
	seedSmbios(102, "", m089ClusterA)
	seedGuest(103, "app", "qemu", m089ClusterA) // …with its twin
	seedSmbios(103, "", m089ClusterA)
	seedGuest(104, "ct01", "lxc", m089ClusterA) // a container sharing a name
	seedGuest(105, "linux03", "qemu", m089ClusterA)
	seedSmbios(105, uuidRebuilt, m089ClusterA) // rebuilt: NEW uuid, same name
	// Never scanned — no guest_smbios row at all. This is every guest on a
	// cluster whose platform was mapped moments ago, before the SMBIOS pass
	// has run.
	seedGuest(106, "unscanned", "qemu", m089ClusterA)
	// Two guests holding the SAME smbios uuid. Nothing in Proxmox enforces
	// uniqueness — smbios1 is operator-settable, and a guest built by copying
	// another's config inherits it.
	seedGuest(107, "twin-a", "qemu", m089ClusterA)
	seedSmbios(107, uuidTwin, m089ClusterA)
	seedGuest(108, "twin-b", "qemu", m089ClusterA)
	seedSmbios(108, uuidTwin, m089ClusterA)
	// A guest on the OTHER cluster whose uuid is the one the object carries.
	// Nothing may match across the platform mapping.
	seedGuest(200, "leak", "qemu", m089ClusterB)
	seedSmbios(200, uuidDB01, m089ClusterB)

	// --- backup objects -----------------------------------------------------
	type objSpec struct {
		key         string
		smbios      string
		name        string
		platform    *uuid.UUID
		matchMethod string
		vmid        *int32
		manualKey   string
	}
	i32 := func(n int32) *int32 { return &n }

	objects := []objSpec{
		{key: "smbios-hit", smbios: uuidWeb01, name: "web01", platform: &m089Platform},
		// Stored uppercase. Proxmox and Veeam both emit lowercase today, but a
		// case difference must not silently demote a deterministic match to
		// the name tier.
		{key: "smbios-case", smbios: "413BABD1-94DB-4F46-83ED-7C1BA5AB644E", name: "web01", platform: &m089Platform},
		{key: "name-hit", smbios: "", name: "db01", platform: &m089Platform},
		{key: "name-ambiguous", smbios: "", name: "app", platform: &m089Platform},
		// The container's name matches a guest, but Veeam cannot back up LXC
		// at all — matching it would invent a protected container.
		{key: "name-lxc", smbios: "", name: "ct01", platform: &m089Platform},
		// The headline orphan: a rebuilt host. Veeam still holds the OLD
		// machine's backup under the old uuid; the live guest carries a new
		// one. A name match would report the replacement as protected by a
		// backup of the machine it replaced.
		{key: "orphan-rebuilt", smbios: uuidGone, name: "linux03", platform: &m089Platform},
		// Right uuid, but its platform belongs to a cluster nobody mapped.
		{key: "unmapped-platform", smbios: uuidDB01, name: "db01", platform: ptrUUID(uuid.New())},
		// A job that has never run has no platform at all.
		{key: "no-platform", smbios: uuidWeb01, name: "web01", platform: nil},
		// No objectId at all, sharing a name with a guest whose uuid IS
		// known. The object's identity is unknown, the guest's is known and
		// unmatched — matching them would be a guess dressed as a fact, so
		// this stays unresolved. (Every Proxmox backup object on 13.1 carries
		// an objectId, so this is a lock on the rule rather than a live case.)
		{key: "no-smbios-known-guest", smbios: "", name: "web01", platform: &m089Platform},
		// The name matches a guest the collector has never scanned. Nothing is
		// known about that guest's identity, so the honest answer is "not
		// resolved" — NOT a name match. This is the lock on the window right
		// after an operator maps a platform: if unscanned guests were treated
		// as uuid-less, every object on the cluster would name-match at once,
		// orphans included, and the coverage view would announce protection
		// that does not exist.
		{key: "name-unscanned-guest", smbios: "", name: "unscanned", platform: &m089Platform},
		// The uuid identifies two guests, so it identifies neither. Picking
		// one would be a coin flip presented as the deterministic tier — the
		// one tier the UI tells operators to trust without qualification.
		{key: "smbios-duplicate", smbios: uuidTwin, name: "twin-a", platform: &m089Platform},
		// An operator's own mapping. Deliberately WRONG on every automatic
		// tier, so a pass that overwrote it would be unmistakable.
		{key: "manual", smbios: uuidWeb01, name: "web01", platform: &m089Platform,
			matchMethod: "manual", vmid: i32(999)},
		// A pin onto guest 100, recorded when that guest's SMBIOS uuid was
		// something else — i.e. Proxmox destroyed the pinned guest and handed
		// VMID 100 to a new one. The pin must fall back to automatic
		// resolution, which here finds the real owner of uuidWeb01.
		{key: "manual-stale-guest", smbios: uuidWeb01, name: "web01", platform: &m089Platform,
			matchMethod: "manual", vmid: i32(100), manualKey: uuidGone},
	}

	ids := make(map[string]uuid.UUID, len(objects))
	for _, o := range objects {
		id := uuid.New()
		ids[o.key] = id
		method := o.matchMethod
		if method == "" {
			method = "none"
		}
		var cluster *uuid.UUID
		if method == "manual" {
			cluster = &m089ClusterA
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO veeam_backup_objects
			   (id, veeam_server_id, veeam_object_id, smbios_uuid, platform_id, name,
			    object_type, cluster_id, vmid, match_method, manual_guest_key)
			 VALUES ($1, $2, $3, $4, $5, $6, 'VM', $7, $8, $9, $10)`,
			id, m089Server, uuid.New(), o.smbios, o.platform, o.name, cluster, o.vmid, method, o.manualKey); err != nil {
			t.Fatalf("seed backup object %s: %v", o.key, err)
		}
	}

	q := gen.New(pool)
	if _, err := q.CorrelateVeeamBackupObjects(ctx, m089Server); err != nil {
		t.Fatalf("CorrelateVeeamBackupObjects: %v", err)
	}

	type result struct {
		cluster *uuid.UUID
		vmid    *int32
		method  string
	}
	read := func(key string) result {
		t.Helper()
		var r result
		if err := pool.QueryRow(ctx,
			`SELECT cluster_id, vmid, match_method FROM veeam_backup_objects WHERE id = $1`,
			ids[key]).Scan(&r.cluster, &r.vmid, &r.method); err != nil {
			t.Fatalf("read %s: %v", key, err)
		}
		return r
	}
	assert := func(key, wantMethod string, wantCluster *uuid.UUID, wantVmid *int32) {
		t.Helper()
		got := read(key)
		if got.method != wantMethod {
			t.Errorf("%s: match_method = %q, want %q", key, got.method, wantMethod)
		}
		switch {
		case wantCluster == nil && got.cluster != nil:
			t.Errorf("%s: cluster_id = %v, want NULL", key, *got.cluster)
		case wantCluster != nil && got.cluster == nil:
			t.Errorf("%s: cluster_id = NULL, want %v", key, *wantCluster)
		case wantCluster != nil && *got.cluster != *wantCluster:
			t.Errorf("%s: cluster_id = %v, want %v", key, *got.cluster, *wantCluster)
		}
		switch {
		case wantVmid == nil && got.vmid != nil:
			t.Errorf("%s: vmid = %d, want NULL", key, *got.vmid)
		case wantVmid != nil && got.vmid == nil:
			t.Errorf("%s: vmid = NULL, want %d", key, *wantVmid)
		case wantVmid != nil && *got.vmid != *wantVmid:
			t.Errorf("%s: vmid = %d, want %d", key, *got.vmid, *wantVmid)
		}
	}

	assert("smbios-hit", "smbios", &m089ClusterA, i32(100))
	assert("smbios-case", "smbios", &m089ClusterA, i32(100))
	assert("name-hit", "name", &m089ClusterA, i32(101))
	// Two guests share the name, so the fallback cannot tell them apart.
	// Resolving to whichever row sorted first would be a coin flip presented
	// as a fact.
	assert("name-ambiguous", "none", nil, nil)
	assert("name-lxc", "none", nil, nil)
	assert("orphan-rebuilt", "none", nil, nil)
	// Its platform is mapped to no cluster, so the uuid on the OTHER cluster
	// must not be reachable. This is the authorization boundary, not a
	// nicety: matching here would attribute cluster B's backup to a caller
	// scoped to cluster A.
	assert("unmapped-platform", "none", nil, nil)
	assert("no-platform", "none", nil, nil)
	assert("no-smbios-known-guest", "none", nil, nil)
	assert("name-unscanned-guest", "none", nil, nil)
	assert("smbios-duplicate", "none", nil, nil)
	// The operator's mapping survives untouched, wrong or not.
	assert("manual", "manual", &m089ClusterA, i32(999))
	// …but only while it is still TRUE. A pin whose guest has been replaced
	// is not an operator decision to respect, it is a claim that the new
	// occupant of that VMID owns the old machine's restore points — the exact
	// "protected by a backup of the machine it replaced" failure the SMBIOS
	// tier exists to prevent, made permanent by the manual exemption.
	assert("manual-stale-guest", "smbios", &m089ClusterA, i32(100))

	// --- unmapping must un-attribute ---------------------------------------
	//
	// An admin who detaches a platform is revoking an attribution. Leaving the
	// old cluster_id on the rows would keep a cluster-scoped viewer's coverage
	// numbers built on data they no longer have any grant over.
	if _, err := pool.Exec(ctx,
		`UPDATE veeam_platforms SET cluster_id = NULL WHERE veeam_server_id = $1 AND platform_id = $2`,
		m089Server, m089Platform); err != nil {
		t.Fatalf("unmap platform: %v", err)
	}
	if _, err := q.CorrelateVeeamBackupObjects(ctx, m089Server); err != nil {
		t.Fatalf("CorrelateVeeamBackupObjects after unmap: %v", err)
	}
	assert("smbios-hit", "none", nil, nil)
	assert("name-hit", "none", nil, nil)
	// The manual pin goes too. Unmapping IS the documented way to revoke
	// cluster-scoped visibility of a server's data, and a pin that kept its
	// cluster_id would go on feeding the coverage view and the VM detail card
	// of a viewer whose grant was just withdrawn — the coverage read keys on
	// veeam_backup_objects.cluster_id directly, bypassing the platform scope
	// that protects every other listing.
	assert("manual", "none", nil, nil)

	// --- idempotence --------------------------------------------------------
	//
	// A converged pass must report no changed rows. The statement runs on
	// every sync tick and fires an updated_at trigger for every row it
	// touches, so a statement that "changed" everything each pass would churn
	// the table forever and make updated_at meaningless.
	if _, err := pool.Exec(ctx,
		`UPDATE veeam_platforms SET cluster_id = $3 WHERE veeam_server_id = $1 AND platform_id = $2`,
		m089Server, m089Platform, m089ClusterA); err != nil {
		t.Fatalf("remap platform: %v", err)
	}
	if _, err := q.CorrelateVeeamBackupObjects(ctx, m089Server); err != nil {
		t.Fatalf("CorrelateVeeamBackupObjects remap: %v", err)
	}
	changed, err := q.CorrelateVeeamBackupObjects(ctx, m089Server)
	if err != nil {
		t.Fatalf("CorrelateVeeamBackupObjects settled: %v", err)
	}
	if changed != 0 {
		t.Errorf("a settled correlation changed %d rows, want 0", changed)
	}
}

func ptrUUID(id uuid.UUID) *uuid.UUID { return &id }

var (
	m089SoloCluster   = uuid.MustParse("89000000-0000-4000-8000-000000000011")
	m089SoloServer    = uuid.MustParse("89000000-0000-4000-8000-000000000012")
	m089SoloPlat      = uuid.MustParse("89000000-0000-4000-8000-000000000013")
	m089SecondCluster = uuid.MustParse("89000000-0000-4000-8000-000000000014")
)

// TestVeeamPlatform_AutoMapsOnDiscoveryOnly pins the two halves of the
// single-cluster convenience: a platform maps itself the first time it is
// seen, and a sync NEVER re-maps one an operator has deliberately unmapped.
//
// The second half is the load-bearing one. The mapping is what every
// cluster-scoped Veeam permission resolves through, so unmapping is an
// operator revoking access — and an earlier draft implemented the convenience
// as a recurring "fill anything still NULL" sweep, which put the mapping back
// on the next sync tick, minutes later, with nothing to show it had happened.
//
// Skipped unless NEXARA_TEST_DB_URL is set (a throwaway database).
func TestVeeamPlatform_AutoMapsOnDiscoveryOnly(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	ctx, pool := env.Ctx, env.Pool

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		_, _ = pool.Exec(pctx, `DELETE FROM veeam_servers WHERE id = $1`, m089SoloServer)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`,
			[]uuid.UUID{m089SoloCluster, m089SecondCluster})
	}
	purge()
	defer purge()

	migrateUp(t, env.Migrate)

	// Any cluster left active by another test would make this install
	// multi-cluster and the convenience would correctly decline to fire, so
	// the fixture owns the whole active set for the duration.
	var parked []uuid.UUID
	if err := pool.QueryRow(ctx,
		`WITH off AS (UPDATE clusters SET is_active = false WHERE is_active RETURNING id)
		 SELECT coalesce(array_agg(id), '{}') FROM off`).Scan(&parked); err != nil {
		t.Fatalf("park existing clusters: %v", err)
	}
	defer func() {
		rctx, rcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer rcancel()
		_, _ = pool.Exec(rctx, `UPDATE clusters SET is_active = true WHERE id = ANY($1)`, parked)
	}()

	if _, err := pool.Exec(ctx,
		`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
		 VALUES ($1, 'migration-089-solo', 'https://solo.invalid:8006', 'test@pam!t', 'x')`,
		m089SoloCluster); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO veeam_servers (id, name, base_url, username, password_encrypted)
		 VALUES ($1, 'migration-089-solo-vbr', 'https://vbr.invalid:9419', 'u', 'x')`,
		m089SoloServer); err != nil {
		t.Fatalf("seed veeam server: %v", err)
	}

	q := gen.New(pool)
	upsert := func() {
		t.Helper()
		if err := q.UpsertVeeamPlatform(ctx, gen.UpsertVeeamPlatformParams{
			VeeamServerID: m089SoloServer,
			PlatformID:    m089SoloPlat,
			DisplayName:   "cluster01",
		}); err != nil {
			t.Fatalf("UpsertVeeamPlatform: %v", err)
		}
	}
	mapping := func() *uuid.UUID {
		t.Helper()
		var got *uuid.UUID
		if err := pool.QueryRow(ctx,
			`SELECT cluster_id FROM veeam_platforms WHERE veeam_server_id = $1 AND platform_id = $2`,
			m089SoloServer, m089SoloPlat).Scan(&got); err != nil {
			t.Fatalf("read mapping: %v", err)
		}
		return got
	}

	// Discovery maps it.
	upsert()
	if got := mapping(); got == nil || *got != m089SoloCluster {
		t.Fatalf("after discovery, cluster_id = %v, want %v", got, m089SoloCluster)
	}

	// An operator revokes it, and sync ticks keep arriving.
	if _, err := pool.Exec(ctx,
		`UPDATE veeam_platforms SET cluster_id = NULL WHERE veeam_server_id = $1 AND platform_id = $2`,
		m089SoloServer, m089SoloPlat); err != nil {
		t.Fatalf("unmap: %v", err)
	}
	upsert()
	upsert()
	if got := mapping(); got != nil {
		t.Errorf("a sync re-mapped a platform the operator unmapped: cluster_id = %v", *got)
	}

	// A second active cluster makes the choice ambiguous. The subquery must
	// then yield NULL rather than erroring with "more than one row returned
	// by a subquery" — which would fail every platform upsert, and so every
	// inventory pass, on the multi-cluster installs this product is for.
	if _, err := pool.Exec(ctx,
		`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
		 VALUES ($1, 'migration-089-second', 'https://second.invalid:8006', 'test@pam!t', 'x')`,
		m089SecondCluster); err != nil {
		t.Fatalf("seed second cluster: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM veeam_platforms WHERE veeam_server_id = $1`, m089SoloServer); err != nil {
		t.Fatalf("clear platform: %v", err)
	}
	upsert()
	if got := mapping(); got != nil {
		t.Errorf("a multi-cluster install auto-mapped to %v; the choice is the operator's", *got)
	}
}
