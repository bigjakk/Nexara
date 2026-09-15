// Package backupcoverage decides, for every guest on a cluster, whether it is
// backed up, by which provider, and how recently.
//
// One computation, two consumers: the GET /api/v1/backup-coverage endpoint
// and the backup compliance report. Until they shared it, the report read
// pbs_snapshots on its own, keyed on VMID with no cluster or datastore scope,
// and called a Veeam-protected guest "missing" while the coverage page called
// the same guest, on the same day, "protected". A verdict that two screens can
// disagree about is not a verdict, so this package owns the only copy.
package backupcoverage

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// Eligibility values: why a guest is, or is not, a backup target at all.
// Templates never reach this computation — ListAllVMs excludes them.
const (
	Eligible          = "eligible"
	VeeamWorker       = "veeam_worker"
	VeeamBackupServer = "veeam_backup_server"
)

// Protection values: which providers actually protect a guest.
const (
	ProtectionBoth        = "both"
	ProtectionVeeam       = "veeam"
	ProtectionPBS         = "pbs"
	ProtectionNone        = "none"
	ProtectionNotEligible = "not_eligible"
)

// Status values: freshness across every provider the caller may see.
const (
	StatusRecent      = "recent"
	StatusStale       = "stale"
	StatusNone        = "none"
	StatusNotEligible = "not_eligible"
)

// DefaultStaleAfter is the freshness threshold every consumer uses unless a
// report parameter says otherwise. The coverage page has always said 24 h;
// the report used to say 48 h, which hid a guest that failed last night.
const DefaultStaleAfter = 24 * time.Hour

// Entry is one guest's backup coverage across every provider the caller may
// see. The JSON tags are the /backup-coverage response contract — the
// frontend switches on these strings, so the values are constants above.
type Entry struct {
	VMID        int32  `json:"vmid"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	ClusterID   string `json:"cluster_id"`
	ClusterName string `json:"cluster_name"`
	// LatestBackup and BackupCount are the PBS figures, unchanged, and remain
	// the fields the existing UI reads.
	LatestBackup *int64 `json:"latest_backup"`
	BackupCount  int    `json:"backup_count"`
	// CoverageStatus is freshness across EVERY provider — "recent", "stale",
	// "none", or "not_eligible". A guest protected only by Veeam used to
	// report "none" here, which was simply wrong; a deployment with no Veeam
	// server sees exactly what it saw before.
	CoverageStatus string `json:"coverage_status"`
	// Eligibility is why a guest is not a backup target at all: "eligible",
	// "veeam_worker", or "veeam_backup_server".
	Eligibility string `json:"eligibility"`
	// VeeamCapable is false for LXC containers, which Veeam cannot back up at
	// all. They are still backup targets — PBS handles them — so this is a
	// per-provider fact, not an eligibility one, and conflating the two would
	// hide a container that genuinely has no backups.
	VeeamCapable bool `json:"veeam_capable"`
	// Veeam is nil when no Veeam data applies: no server configured, the
	// cluster's platform is unmapped, or the caller holds no view:veeam.
	Veeam *Veeam `json:"veeam"`
	// Protection names the providers actually protecting this guest:
	// "both", "veeam", "pbs", "none", or "not_eligible".
	Protection string `json:"protection"`

	// NodeID is the guest's node, for consumers that group or label by node.
	// Not part of the API response.
	NodeID uuid.UUID `json:"-"`
	// Freshest is the newest restore point from any provider, as unix
	// seconds. It is the number CoverageStatus was judged on, exposed so a
	// report can bucket guests by age without re-deriving it, and it is 0
	// whenever there is nothing to judge: no provider holds a point, or the
	// guest is not a backup target at all (a backed-up VBR server still reads
	// 0 here, because its backups are not this report's concern). Not part of
	// the API response.
	Freshest int64 `json:"-"`
}

// Veeam is the Veeam side of one guest's protection.
type Veeam struct {
	Protected          bool       `json:"protected"`
	LatestRestorePoint *time.Time `json:"latest_restore_point"`
	RestorePointCount  int64      `json:"restore_point_count"`
	RestorePointBytes  int64      `json:"restore_point_bytes"`
	// MatchMethod is how this guest was tied to its Veeam backup: "smbios"
	// (deterministic), "manual" (an operator said so), or "name" — which every
	// consumer must flag, because a name match is a guess.
	MatchMethod string `json:"match_method"`
	// MalwareStatus is the verdict on the NEWEST restore point only. An old
	// "Suspicious" that a later clean backup superseded is history.
	MalwareStatus string `json:"malware_status"`
	LastRunFailed bool   `json:"last_run_failed"`
}

// Querier is the slice of the store the computation reads. *db.Queries
// satisfies it; tests supply a fake.
type Querier interface {
	ListAllVMs(ctx context.Context) ([]db.ListAllVMsRow, error)
	ListClusters(ctx context.Context) ([]db.Cluster, error)
	ListStoragePoolsByCluster(ctx context.Context, clusterID uuid.UUID) ([]db.StoragePool, error)
	ListPBSServers(ctx context.Context) ([]db.PbsServer, error)
	ListPBSSnapshotsByServer(ctx context.Context, pbsServerID uuid.UUID) ([]db.PbsSnapshot, error)
	ListVeeamGuestProtectionForCluster(ctx context.Context, clusterID uuid.UUID) ([]db.ListVeeamGuestProtectionForClusterRow, error)
	ListVeeamInfrastructureGuestsForCluster(ctx context.Context, clusterID uuid.UUID) ([]db.ListVeeamInfrastructureGuestsForClusterRow, error)
}

// Scope limits the computation to what the caller may see.
//
// The two predicates are separate because the grants are. A caller holding
// view:backup but not view:veeam gets exactly the answer they got before Veeam
// existed: no restore-point data, and no eligibility refinement either —
// telling them a guest is a Veeam worker would leak the mapping they have no
// grant over, and the honest alternative to a partial answer is the old one.
type Scope struct {
	// Cluster reports whether a cluster's guests are in scope. nil means
	// every cluster.
	Cluster func(uuid.UUID) bool
	// Veeam reports whether Veeam data may be consulted for a cluster. nil
	// means never.
	Veeam func(uuid.UUID) bool
}

// OneCluster is the predicate for a single cluster, for callers such as the
// report generator that compute coverage cluster by cluster.
func OneCluster(id uuid.UUID) func(uuid.UUID) bool {
	return func(c uuid.UUID) bool { return c == id }
}

// Options tunes the computation. Zero values take the documented defaults.
type Options struct {
	// StaleAfter is how old the newest restore point may be before a guest
	// is "stale" rather than "recent". Zero means DefaultStaleAfter.
	StaleAfter time.Duration
	// Now anchors every age. Zero means time.Now().
	Now time.Time
	// Logger receives the warnings for a Veeam read that failed. nil means
	// slog.Default().
	Logger *slog.Logger
	// VeeamStrict turns a failed Veeam read into an error instead of a
	// warning. The coverage page leaves it off: it re-fetches every few
	// seconds and a momentary Veeam outage should degrade to the PBS answer
	// rather than blank the page. A report turns it on: a compliance document
	// that quietly calls every Veeam-only guest "no backup" because a table
	// was unreadable for one second is the exact lie this package exists to
	// end, and a failed run in the history is the honest outcome.
	VeeamStrict bool
}

// Verdict decides one guest's protection and freshness.
//
// Split out from the loop so the decision has a test of its own: it is the
// sentence the whole feature exists to say, and every branch of it is a claim
// about whether someone's data is recoverable.
//
// Ineligibility wins over everything. A Veeam worker appliance with no backup
// is not an alarm, and rendering it as one is how a coverage view teaches
// operators to ignore it — on one real estate nearly half of the naive
// alarms were of that kind.
func Verdict(eligibility string, pbsProtected, veeamProtected bool, freshest, now, staleThreshold int64) (protection, status string) {
	if eligibility != Eligible {
		return ProtectionNotEligible, StatusNotEligible
	}

	switch {
	case pbsProtected && veeamProtected:
		protection = ProtectionBoth
	case veeamProtected:
		protection = ProtectionVeeam
	case pbsProtected:
		protection = ProtectionPBS
	default:
		// No provider protects it, so there is no freshness to report — and
		// "stale" would read as "there is an old backup", which there is not.
		return ProtectionNone, StatusNone
	}

	if now-freshest < staleThreshold {
		return protection, StatusRecent
	}
	return protection, StatusStale
}

// Compute cross-references three data sources to determine every in-scope
// guest's backup coverage:
//  1. PVE storage pools — which clusters have PBS-type storage and which
//     datastore name each maps to (PVE's PBS storage name = PBS datastore name)
//  2. PBS snapshots — keyed by (datastore, backup_id/VMID)
//  3. Veeam backup objects and restore points, correlated to guests by SMBIOS
//     UUID, plus Veeam's own appliances on the cluster
//
// VMs are matched only against datastores their cluster actually mounts. That
// is what makes multi-cluster installs with overlapping VMIDs come out right:
// cluster02's VMID 100 backing up to its own datastore says nothing about
// cluster01's VMID 100.
//
// Entries come back in ListAllVMs order (by name).
func Compute(ctx context.Context, q Querier, scope Scope, opts Options) ([]Entry, error) {
	staleAfter := opts.StaleAfter
	if staleAfter <= 0 {
		staleAfter = DefaultStaleAfter
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	inScope := scope.Cluster
	if inScope == nil {
		inScope = func(uuid.UUID) bool { return true }
	}
	veeamAllowed := scope.Veeam
	if veeamAllowed == nil {
		veeamAllowed = func(uuid.UUID) bool { return false }
	}

	vms, err := q.ListAllVMs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list VMs: %w", err)
	}
	clusters, err := q.ListClusters(ctx)
	if err != nil {
		return nil, fmt.Errorf("list clusters: %w", err)
	}

	// Step 1: cluster → set of PBS datastore names, from PVE storage config.
	clusterDatastores := make(map[uuid.UUID]map[string]bool)
	for _, cl := range clusters {
		if !inScope(cl.ID) {
			continue
		}
		pools, pErr := q.ListStoragePoolsByCluster(ctx, cl.ID)
		if pErr != nil {
			return nil, fmt.Errorf("list storage pools for cluster %s: %w", cl.ID, pErr)
		}
		for _, pool := range pools {
			if pool.Type != "pbs" {
				continue
			}
			if clusterDatastores[cl.ID] == nil {
				clusterDatastores[cl.ID] = make(map[string]bool)
			}
			clusterDatastores[cl.ID][pool.Storage] = true
		}
	}

	// Step 2: snapshot map keyed by "datastore:backup_id".
	//
	// The key carries no PBS server identity, because storage_pools records
	// only the PVE storage name, which for a PBS storage is the datastore
	// name on whichever server the storage points at. Two PBS servers that
	// both expose a datastore called "store01" are therefore merged, and a
	// PVE storage whose name differs from its datastore is not matched at
	// all. Resolving either needs the datastore and server recorded on the
	// storage pool by the collector; until then this is the same rule the
	// coverage page has always applied.
	type backupInfo struct {
		latestTime int64
		count      int
	}
	backupMap := make(map[string]*backupInfo)
	servers, err := q.ListPBSServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list PBS servers: %w", err)
	}
	for _, srv := range servers {
		snaps, sErr := q.ListPBSSnapshotsByServer(ctx, srv.ID)
		if sErr != nil {
			return nil, fmt.Errorf("list PBS snapshots for server %s: %w", srv.ID, sErr)
		}
		for _, snap := range snaps {
			key := snap.Datastore + ":" + snap.BackupID
			info, ok := backupMap[key]
			if !ok {
				info = &backupInfo{}
				backupMap[key] = info
			}
			info.count++
			if snap.BackupTime > info.latestTime {
				info.latestTime = snap.BackupTime
			}
		}
	}

	// Step 3: Veeam protection and Veeam-owned guests, per cluster the caller
	// may see Veeam data for. One pair of queries per cluster rather than one
	// per guest — the per-guest aggregation happens in SQL, because a guest
	// appears once per backup it belongs to and a naive join would report it
	// several times with a fraction of its restore points each.
	veeamByCluster := make(map[uuid.UUID]map[int32]db.ListVeeamGuestProtectionForClusterRow)
	veeamOwned := make(map[uuid.UUID]map[int32]string)
	for _, cl := range clusters {
		if !inScope(cl.ID) || !veeamAllowed(cl.ID) {
			continue
		}
		if rows, vErr := q.ListVeeamGuestProtectionForCluster(ctx, cl.ID); vErr == nil {
			byVmid := make(map[int32]db.ListVeeamGuestProtectionForClusterRow, len(rows))
			for _, r := range rows {
				byVmid[r.Vmid] = r
			}
			veeamByCluster[cl.ID] = byVmid
		} else if opts.VeeamStrict {
			return nil, fmt.Errorf("list Veeam protection for cluster %s: %w", cl.ID, vErr)
		} else {
			logger.Warn("backup coverage: Veeam protection unreadable",
				"cluster_id", cl.ID, "error", vErr)
		}
		if rows, iErr := q.ListVeeamInfrastructureGuestsForCluster(ctx, cl.ID); iErr == nil {
			owned := make(map[int32]string, len(rows))
			for _, r := range rows {
				// 'backup_server' wins over 'worker' whatever order the rows
				// arrive in. One guest can legitimately hold both — the VBR
				// server fills a proxy role itself — and the more specific
				// fact is the one worth showing; depending on the query's
				// ORDER BY for that would let a re-ordered query flip the
				// reason shown to an operator between requests.
				if _, seen := owned[r.Vmid]; !seen || r.Role == "backup_server" {
					owned[r.Vmid] = r.Role
				}
			}
			veeamOwned[cl.ID] = owned
		} else if opts.VeeamStrict {
			return nil, fmt.Errorf("list Veeam infrastructure for cluster %s: %w", cl.ID, iErr)
		} else {
			logger.Warn("backup coverage: Veeam infrastructure unreadable",
				"cluster_id", cl.ID, "error", iErr)
		}
	}

	// Step 4: match each VM against only the datastores its cluster uses.
	nowUnix := now.Unix()
	staleThreshold := int64(staleAfter / time.Second)

	entries := make([]Entry, 0, len(vms))
	for _, vm := range vms {
		if !inScope(vm.ClusterID) {
			continue
		}
		vmidStr := strconv.Itoa(int(vm.Vmid))
		entry := Entry{
			VMID:        vm.Vmid,
			Name:        vm.Name,
			Type:        vm.Type,
			Status:      vm.Status,
			ClusterID:   vm.ClusterID.String(),
			ClusterName: vm.ClusterName,
			Eligibility: Eligible,
			// Veeam cannot back up an LXC container at all. That is a fact
			// about the provider, not about the guest — PBS backs containers
			// up perfectly well, so it must not make one "not eligible" and
			// hide that it has no backups.
			VeeamCapable: vm.Type == "qemu",
			NodeID:       vm.NodeID,
		}

		switch veeamOwned[vm.ClusterID][vm.Vmid] {
		case "worker":
			entry.Eligibility = VeeamWorker
		case "backup_server":
			entry.Eligibility = VeeamBackupServer
		}

		// Aggregate across datastores: a VM could be backed up to several.
		var totalCount int
		var latestTime int64
		for ds := range clusterDatastores[vm.ClusterID] {
			if info, ok := backupMap[ds+":"+vmidStr]; ok && info.count > 0 {
				totalCount += info.count
				if info.latestTime > latestTime {
					latestTime = info.latestTime
				}
			}
		}
		if totalCount > 0 {
			entry.LatestBackup = &latestTime
			entry.BackupCount = totalCount
		}

		// The freshest point from EITHER provider drives the status. Reading
		// only PBS here reported every Veeam-protected guest as unprotected,
		// which is the wrong answer in the one direction that matters.
		freshest := latestTime
		if row, ok := veeamByCluster[vm.ClusterID][vm.Vmid]; ok {
			v := &Veeam{
				RestorePointCount: row.RestorePointCount,
				RestorePointBytes: row.RestorePointBytes,
				MatchMethod:       row.MatchMethod,
				MalwareStatus:     row.LatestMalwareStatus,
				LastRunFailed:     row.LastRunFailed,
			}
			// A backup object with every restore point pruned is NOT
			// protected — Veeam knows the guest and can restore nothing —
			// so protection keys on the points, never on the object.
			v.Protected = row.RestorePointCount > 0
			if row.LatestRestorePoint.Valid {
				t := row.LatestRestorePoint.Time
				v.LatestRestorePoint = &t
				if unix := t.Unix(); unix > freshest {
					freshest = unix
				}
			}
			entry.Veeam = v
		}

		entry.Protection, entry.CoverageStatus = Verdict(
			entry.Eligibility,
			totalCount > 0,
			entry.Veeam != nil && entry.Veeam.Protected,
			freshest, nowUnix, staleThreshold,
		)
		if entry.Protection != ProtectionNone && entry.Protection != ProtectionNotEligible {
			entry.Freshest = freshest
		}

		entries = append(entries, entry)
	}

	return entries, nil
}
