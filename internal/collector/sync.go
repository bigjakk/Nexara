package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// staleVMGrace is how long a guest must go unseen on an otherwise-healthy node
// before the collector prunes its vms row. It absorbs momentary non-observation
// (most importantly the cutover instant of a live migration, when Proxmox lists
// the guest on neither source nor destination) so a single missed sync doesn't
// delete and re-insert the row — that churn mints a fresh vms.id, which folder
// membership and other natural-key state are deliberately decoupled from, but
// avoiding the churn altogether also keeps metrics/inventory continuous.
const staleVMGrace = 5 * time.Minute

// staleInventoryGrace is the equivalent unseen-grace for the non-guest
// inventory pruned each sync — storage pools, node hardware (disks, NICs,
// PCI devices), and PBS snapshots/sync/verify jobs. Same rationale as
// staleVMGrace: prune on the DB clock only after the grace window, so a
// momentary non-observation (or app/DB clock skew) doesn't churn rows — and,
// for storage pools, doesn't emit spurious inventory-change events.
const staleInventoryGrace = 5 * time.Minute

// SyncQueries defines the database operations needed by the Syncer.
// This interface enables testing with mock implementations.
type SyncQueries interface {
	ListActiveClusters(ctx context.Context) ([]db.Cluster, error)
	UpsertNode(ctx context.Context, arg db.UpsertNodeParams) (db.Node, error)
	UpdateClusterPVEVersion(ctx context.Context, arg db.UpdateClusterPVEVersionParams) error
	UpsertVM(ctx context.Context, arg db.UpsertVMParams) (db.Vm, error)
	SetVMOSType(ctx context.Context, arg db.SetVMOSTypeParams) error
	SetVMConfigOSType(ctx context.Context, arg db.SetVMConfigOSTypeParams) error
	UpsertStoragePool(ctx context.Context, arg db.UpsertStoragePoolParams) (db.UpsertStoragePoolRow, error)
	DeleteStaleStoragePools(ctx context.Context, arg db.DeleteStaleStoragePoolsParams) (int64, error)
	GetNodeByClusterAndName(ctx context.Context, arg db.GetNodeByClusterAndNameParams) (db.Node, error)
	ListVMStatusesByCluster(ctx context.Context, clusterID uuid.UUID) ([]db.ListVMStatusesByClusterRow, error)
	DeleteStaleVMsForNodes(ctx context.Context, arg db.DeleteStaleVMsForNodesParams) (int64, error)
	UpdateNodeAddress(ctx context.Context, arg db.UpdateNodeAddressParams) error
	// Audit
	InsertAuditLog(ctx context.Context, arg db.InsertAuditLogParams) error
	InsertAuditLogWithSource(ctx context.Context, arg db.InsertAuditLogWithSourceParams) error
	// Task sync
	GetTaskSyncState(ctx context.Context, clusterID uuid.UUID) (int64, error)
	UpsertTaskSyncState(ctx context.Context, arg db.UpsertTaskSyncStateParams) error
	ListExistingTaskHistoryUPIDs(ctx context.Context, upids []string) ([]string, error)
	ListExistingAuditLogUPIDs(ctx context.Context, upids []string) ([]string, error)
	InsertExternalTaskHistory(ctx context.Context, arg db.InsertExternalTaskHistoryParams) error
	// Task reconcile
	ListRunningTaskHistoryByCluster(ctx context.Context, clusterID uuid.UUID) ([]db.TaskHistory, error)
	ReconcileTaskHistory(ctx context.Context, arg db.ReconcileTaskHistoryParams) (int64, error)
	GetVMByClusterAndVmid(ctx context.Context, arg db.GetVMByClusterAndVmidParams) (db.Vm, error)
	// Node queries
	ListNodesByCluster(ctx context.Context, clusterID uuid.UUID) ([]db.Node, error)
	// Storage queries (inventory diff)
	ListStoragePoolsByCluster(ctx context.Context, clusterID uuid.UUID) ([]db.StoragePool, error)
	// PBS queries
	ListActivePBSServers(ctx context.Context) ([]db.PbsServer, error)
	UpsertPBSSnapshot(ctx context.Context, arg db.UpsertPBSSnapshotParams) (db.PbsSnapshot, error)
	UpsertPBSSyncJob(ctx context.Context, arg db.UpsertPBSSyncJobParams) (db.PbsSyncJob, error)
	UpsertPBSVerifyJob(ctx context.Context, arg db.UpsertPBSVerifyJobParams) (db.PbsVerifyJob, error)
	DeleteStalePBSSnapshots(ctx context.Context, arg db.DeleteStalePBSSnapshotsParams) error
	DeleteStalePBSSyncJobs(ctx context.Context, arg db.DeleteStalePBSSyncJobsParams) error
	DeleteStalePBSVerifyJobs(ctx context.Context, arg db.DeleteStalePBSVerifyJobsParams) error
	// Node hardware detail queries
	UpsertNodeDisk(ctx context.Context, arg db.UpsertNodeDiskParams) (db.NodeDisk, error)
	DeleteStaleNodeDisks(ctx context.Context, arg db.DeleteStaleNodeDisksParams) error
	UpsertNodeNetworkInterface(ctx context.Context, arg db.UpsertNodeNetworkInterfaceParams) (db.NodeNetworkInterface, error)
	DeleteStaleNodeNetworkInterfaces(ctx context.Context, arg db.DeleteStaleNodeNetworkInterfacesParams) error
	UpsertNodePCIDevice(ctx context.Context, arg db.UpsertNodePCIDeviceParams) (db.NodePciDevice, error)
	DeleteStaleNodePCIDevices(ctx context.Context, arg db.DeleteStaleNodePCIDevicesParams) error
}

// ProxmoxClient defines the Proxmox API methods needed by the Syncer.
type ProxmoxClient interface {
	GetNodes(ctx context.Context) ([]proxmox.NodeListEntry, error)
	GetNodeStatus(ctx context.Context, node string) (*proxmox.NodeStatus, error)
	GetNodeDNS(ctx context.Context, node string) (*proxmox.NodeDNS, error)
	GetNodeTime(ctx context.Context, node string) (*proxmox.NodeTime, error)
	GetNodeSubscription(ctx context.Context, node string) (*proxmox.NodeSubscription, error)
	GetNodeDisks(ctx context.Context, node string) ([]proxmox.NodeDisk, error)
	GetNodePCIDevices(ctx context.Context, node string) ([]proxmox.NodePCIDevice, error)
	GetNetworkInterfaces(ctx context.Context, node string) ([]proxmox.NetworkInterface, error)
	GetVMs(ctx context.Context, node string) ([]proxmox.VirtualMachine, error)
	GetContainers(ctx context.Context, node string) ([]proxmox.Container, error)
	GetVMConfig(ctx context.Context, node string, vmid int) (proxmox.VMConfig, error)
	GetCTConfig(ctx context.Context, node string, vmid int) (proxmox.VMConfig, error)
	GetGuestAgentOSInfo(ctx context.Context, node string, vmid int) (*proxmox.GuestOSInfo, error)
	GetStoragePools(ctx context.Context, node string) ([]proxmox.StoragePool, error)
	GetCephStatus(ctx context.Context, node string) (*proxmox.CephStatus, error)
	GetCephOSDs(ctx context.Context, node string) (*proxmox.CephOSDResponse, error)
	GetCephPools(ctx context.Context, node string) ([]proxmox.CephPool, error)
	GetClusterStatus(ctx context.Context) ([]proxmox.ClusterStatusEntry, error)
	GetClusterResources(ctx context.Context, resourceType string) ([]proxmox.ClusterResource, error)
	GetNodeTasks(ctx context.Context, node string, since int64, limit int) ([]proxmox.NodeTask, error)
	GetTaskStatus(ctx context.Context, node string, upid string) (*proxmox.TaskStatus, error)
	GetVersion(ctx context.Context) (*proxmox.Version, error)
	GetHAManagerStatus(ctx context.Context) (map[string]json.RawMessage, error)
}

// ClientFactory creates a ProxmoxClient from cluster credentials.
// Extracted for testability.
type ClientFactory func(apiURL, tokenID, tokenSecret, tlsFingerprint string) (ProxmoxClient, error)

// vmExtraFields holds per-VM data only available from the cluster resources endpoint.
type vmExtraFields struct {
	HAState string
	Pool    string
}

// PBSProxmoxClient defines the PBS API methods needed by the Syncer.
type PBSProxmoxClient interface {
	GetDatastores(ctx context.Context) ([]proxmox.PBSDatastore, error)
	GetDatastoreStatus(ctx context.Context) ([]proxmox.PBSDatastoreStatus, error)
	GetSnapshots(ctx context.Context, store string) ([]proxmox.PBSSnapshot, error)
	GetSyncJobs(ctx context.Context) ([]proxmox.PBSSyncJob, error)
	GetVerifyJobs(ctx context.Context) ([]proxmox.PBSVerifyJob, error)
}

// PBSClientFactory creates a PBSProxmoxClient from server credentials.
type PBSClientFactory func(apiURL, tokenID, tokenSecret, tlsFingerprint string) (PBSProxmoxClient, error)

// DefaultClientFactory creates a real proxmox.Client.
func DefaultClientFactory(apiURL, tokenID, tokenSecret, tlsFingerprint string) (ProxmoxClient, error) {
	return proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        apiURL,
		TokenID:        tokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: tlsFingerprint,
		Timeout:        30 * time.Second,
	})
}

// DefaultPBSClientFactory creates a real proxmox.PBSClient.
func DefaultPBSClientFactory(apiURL, tokenID, tokenSecret, tlsFingerprint string) (PBSProxmoxClient, error) {
	return proxmox.NewPBSClient(proxmox.ClientConfig{
		BaseURL:        apiURL,
		TokenID:        tokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: tlsFingerprint,
		Timeout:        30 * time.Second,
	})
}

// Syncer discovers and persists Proxmox inventory data.
type Syncer struct {
	queries          SyncQueries
	encryptionKey    string
	clientFactory    ClientFactory
	pbsClientFactory PBSClientFactory
	cache            *proxmox.ClientCache // nil-safe; tests may leave unset
	healthMonitor    *HealthMonitor
	eventPub         eventPublisher
	logger           *slog.Logger
	lastSyncError    map[uuid.UUID]time.Time // rate-limit sync error reporting per cluster
}

// NewSyncer creates a Syncer with the default Proxmox client factory.
func NewSyncer(queries SyncQueries, encryptionKey string, logger *slog.Logger) *Syncer {
	return &Syncer{
		queries:          queries,
		encryptionKey:    encryptionKey,
		clientFactory:    DefaultClientFactory,
		pbsClientFactory: DefaultPBSClientFactory,
		logger:           logger,
	}
}

// eventPublisher is the slice of events.Publisher the Syncer needs. An
// interface so tests can record published events; *events.Publisher
// satisfies it (and its methods are nil-receiver-safe).
type eventPublisher interface {
	ClusterEvent(ctx context.Context, clusterID, kind, resourceType, resourceID, action string)
	SystemEvent(ctx context.Context, kind, action string)
}

// SetEventPublisher attaches an event publisher for status change notifications.
func (s *Syncer) SetEventPublisher(pub eventPublisher) {
	s.eventPub = pub
}

// SetProxmoxCache attaches the per-server cache so SyncCluster can reuse
// cached *Client instances across ticks. Nil-safe: when unset, the
// Syncer falls back to clientFactory + per-call construction.
func (s *Syncer) SetProxmoxCache(cache *proxmox.ClientCache) {
	s.cache = cache
}

// proxmoxClient returns a ProxmoxClient for the given cluster, preferring
// the shared cache when available so collector ticks reuse the same
// *http.Transport idle-conn pool. Falls through to the legacy
// clientFactory path when the cache is nil (test scaffolding).
func (s *Syncer) proxmoxClient(ctx context.Context, cluster db.Cluster) (ProxmoxClient, error) {
	if s.cache != nil {
		client, err := s.cache.Get(ctx, cluster.ID)
		if err == nil {
			return client, nil
		}
		s.logger.Warn("collector: proxmox cache get failed, building per-call",
			"cluster_id", cluster.ID, "error", err)
	}
	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, s.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt token: %w", err)
	}
	client, err := s.clientFactory(cluster.ApiUrl, cluster.TokenID, tokenSecret, cluster.TlsFingerprint)
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}
	return client, nil
}

// failoverCluster attempts to reach the cluster through an alternate member
// after the primary api_url endpoint fails. It is a last resort, invoked only
// when the initial GetNodes call returns a connectivity error: the configured
// endpoint pins to one node, so when that node is down (commonly because it is
// being rebooted during a rolling upgrade) the collector would otherwise lose
// the entire cluster and mark every node offline.
//
// It walks the nodes recorded by the last successful sync, builds a throwaway
// client for each alternate endpoint — pinned to that node's own TLS
// fingerprint, since each cluster member presents a distinct certificate — and
// returns the first that responds together with its node list. Members without
// a recorded fingerprint are skipped rather than connecting unpinned (which
// would silently downgrade to system-CA verification). ok is false when the
// failure is not a connectivity error or no alternate endpoint answers, in
// which case the caller surfaces the original error.
func (s *Syncer) failoverCluster(ctx context.Context, cluster db.Cluster, primaryErr error) (ProxmoxClient, []proxmox.NodeListEntry, bool) {
	if s.clientFactory == nil || !errors.Is(primaryErr, proxmox.ErrConnectionFailed) {
		return nil, nil, false
	}

	nodes, err := s.queries.ListNodesByCluster(ctx, cluster.ID)
	if err != nil || len(nodes) == 0 {
		return nil, nil, false
	}

	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, s.encryptionKey)
	if err != nil {
		return nil, nil, false
	}

	primaryHost := proxmox.APIURLHost(cluster.ApiUrl)
	for _, n := range nodes {
		// Skip nodes without a known address, the primary endpoint itself
		// (already tried — it is the source of primaryErr), and any node whose
		// certificate fingerprint we haven't recorded: connecting unpinned
		// would downgrade TLS to system-CA verification, so fail closed.
		if n.Address == "" || n.Address == primaryHost || n.SslFingerprint == "" {
			continue
		}
		failoverURL := proxmox.FailoverBaseURL(cluster.ApiUrl, n.Address)
		failClient, ferr := s.clientFactory(failoverURL, cluster.TokenID, tokenSecret, n.SslFingerprint)
		if ferr != nil {
			continue
		}
		entries, ferr := failClient.GetNodes(ctx)
		if ferr != nil {
			continue
		}
		s.logger.Warn("collector: primary endpoint unreachable, synced via failover member",
			"cluster_id", cluster.ID,
			"primary_url", cluster.ApiUrl,
			"failover_node", n.Name,
			"failover_url", failoverURL,
		)
		return failClient, entries, true
	}

	return nil, nil, false
}

// pbsProxmoxClient returns a PBSProxmoxClient for the given PBS server,
// preferring the cache. Mirrors proxmoxClient's logic.
func (s *Syncer) pbsProxmoxClient(ctx context.Context, server db.PbsServer) (PBSProxmoxClient, error) {
	if s.cache != nil {
		client, err := s.cache.GetPBS(ctx, server.ID)
		if err == nil {
			return client, nil
		}
		s.logger.Warn("collector: pbs cache get failed, building per-call",
			"pbs_id", server.ID, "error", err)
	}
	tokenSecret, err := crypto.Decrypt(server.TokenSecretEncrypted, s.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt token: %w", err)
	}
	client, err := s.pbsClientFactory(server.ApiUrl, server.TokenID, tokenSecret, server.TlsFingerprint)
	if err != nil {
		return nil, fmt.Errorf("create pbs client: %w", err)
	}
	return client, nil
}

// SetHealthMonitor attaches a health monitor to the syncer.
func (s *Syncer) SetHealthMonitor(h *HealthMonitor) {
	s.healthMonitor = h
}

// SyncCluster discovers nodes, VMs, containers, and storage from a Proxmox cluster,
// upserts them into the database, and returns collected metric snapshots.
func (s *Syncer) SyncCluster(ctx context.Context, cluster db.Cluster) (*ClusterMetricResult, error) {
	client, err := s.proxmoxClient(ctx, cluster)
	if err != nil {
		return nil, fmt.Errorf("sync cluster %s: %w", cluster.ID, err)
	}

	nodes, err := client.GetNodes(ctx)
	if err != nil {
		// The configured api_url targets a single cluster member. If that node
		// is unreachable — e.g. it is mid-reboot during a rolling upgrade — fail
		// over to another member so we keep visibility into the whole cluster
		// instead of marking every node offline. Only connectivity failures are
		// retried; auth/permission errors surface unchanged.
		failClient, failNodes, ok := s.failoverCluster(ctx, cluster, err)
		if !ok {
			return nil, fmt.Errorf("sync cluster %s: get nodes: %w", cluster.ID, err)
		}
		client, nodes = failClient, failNodes
	}

	// Capture the PVE version once per sync cycle. Used by frontend feature gates
	// (e.g. OCI image pull requires PVE 9.1+). Failure is non-fatal.
	if ver, verErr := client.GetVersion(ctx); verErr == nil && ver != nil && ver.Release != "" {
		if ver.Release != cluster.PveVersion {
			if updErr := s.queries.UpdateClusterPVEVersion(ctx, db.UpdateClusterPVEVersionParams{
				ID:         cluster.ID,
				PveVersion: ver.Release,
			}); updErr != nil {
				s.logger.Warn("failed to update cluster PVE version",
					"cluster_id", cluster.ID, "error", updErr)
			}
		}
	} else if verErr != nil {
		s.logger.Debug("failed to fetch PVE version",
			"cluster_id", cluster.ID, "error", verErr)
	}

	// Fetch cluster-level resource data for HA state and pool membership.
	// These fields are only available from /cluster/resources, not per-node endpoints.
	vmExtra := make(map[int32]vmExtraFields)
	if resources, resErr := client.GetClusterResources(ctx, ""); resErr == nil {
		for _, r := range resources {
			if (r.Type == "qemu" || r.Type == "lxc") && r.VMID > 0 {
				vmExtra[safeconv.Int32(r.VMID)] = vmExtraFields{HAState: r.HAState, Pool: r.Pool}
			}
		}
	} else {
		s.logger.Warn("failed to get cluster resources for HA/pool data",
			"cluster_id", cluster.ID,
			"error", resErr,
		)
	}

	// Node-level HA state (online/maintenance/fence/...) read live from the
	// Proxmox HA manager each sync — never assumed from Nexara-initiated actions.
	// Only present when HA is configured; best-effort.
	nodeHAState := map[string]string{}
	if mgr, haErr := client.GetHAManagerStatus(ctx); haErr == nil {
		nodeHAState = parseNodeHAState(mgr)
	} else {
		s.logger.Debug("failed to get HA manager status for node maintenance state",
			"cluster_id", cluster.ID, "error", haErr)
	}

	// Snapshot current VM, node, and storage state so we can detect changes
	// after sync (status transitions, migrations, renames, node state flips,
	// storage (de)activation) and tell the frontend. A nil map means that
	// snapshot query failed — treated as "changed" after the sync, because a
	// spurious refetch is cheap while a swallowed transition leaves the UI
	// stale until the next unrelated event.
	type vmSnapshot struct {
		Status   string
		NodeID   uuid.UUID
		Name     string
		Template bool
		Pool     string
		HAState  string
		Tags     string
	}
	type nodeSnapshot struct {
		Status  string
		HAState string
	}
	var oldVMs map[int32]vmSnapshot
	var oldNodes map[uuid.UUID]nodeSnapshot
	var oldStorage map[uuid.UUID]bool
	if s.eventPub != nil {
		if rows, err := s.queries.ListVMStatusesByCluster(ctx, cluster.ID); err == nil {
			oldVMs = make(map[int32]vmSnapshot, len(rows))
			for _, r := range rows {
				oldVMs[r.Vmid] = vmSnapshot{
					Status:   r.Status,
					NodeID:   r.NodeID,
					Name:     r.Name,
					Template: r.Template,
					Pool:     r.Pool,
					HAState:  r.HaState,
					Tags:     r.Tags,
				}
			}
		}
		if rows, err := s.queries.ListNodesByCluster(ctx, cluster.ID); err == nil {
			oldNodes = make(map[uuid.UUID]nodeSnapshot, len(rows))
			for _, r := range rows {
				oldNodes[r.ID] = nodeSnapshot{Status: r.Status, HAState: r.HaState}
			}
		}
		if rows, err := s.queries.ListStoragePoolsByCluster(ctx, cluster.ID); err == nil {
			oldStorage = make(map[uuid.UUID]bool, len(rows))
			for _, r := range rows {
				oldStorage[r.ID] = r.Active
			}
		}
	}

	now := time.Now()
	result := &ClusterMetricResult{
		ClusterID:   cluster.ID,
		CollectedAt: now,
	}

	storageAdded := false
	// Node IDs whose VM+container sync completed without error this cycle.
	// Only these nodes are eligible for stale-VM pruning — see the
	// DeleteStaleVMsForNodes call below.
	syncedNodeIDs := make([]uuid.UUID, 0, len(nodes))
	for _, node := range nodes {
		nr, err := s.syncNode(ctx, client, cluster.ID, node, vmExtra, nodeHAState)
		if err != nil {
			s.logger.Warn("failed to sync node",
				"cluster_id", cluster.ID,
				"node", node.Node,
				"error", err,
			)
			if s.healthMonitor != nil {
				dbNode, lookupErr := s.queries.GetNodeByClusterAndName(ctx, db.GetNodeByClusterAndNameParams{
					ClusterID: cluster.ID,
					Name:      node.Node,
				})
				if lookupErr == nil {
					s.healthMonitor.RecordFailure(ctx, cluster.ID, dbNode.ID, node.Node)
				}
			}
			continue
		}

		if s.healthMonitor != nil {
			s.healthMonitor.RecordSuccess(ctx, nr.Node)
		}

		result.NodeMetrics = append(result.NodeMetrics, nr.NodeMetric)
		result.VMMetrics = append(result.VMMetrics, nr.VMMetrics...)
		if nr.StorageAdded {
			storageAdded = true
		}
		if nr.SyncOK {
			syncedNodeIDs = append(syncedNodeIDs, nr.Node)
		}
	}

	// Update node addresses from corosync cluster status.
	s.syncNodeAddresses(ctx, client, cluster.ID)

	// Sync Ceph data once per cluster using the first online node.
	if len(nodes) > 0 {
		cephResult := s.syncCeph(ctx, client, cluster.ID, nodes[0].Node)
		if cephResult != nil {
			result.CephCluster = cephResult.CephCluster
			result.CephOSDs = cephResult.CephOSDs
			result.CephPools = cephResult.CephPools
		}
	}

	// Check for inventory changes: VM status/placement/identity, node state,
	// and storage activation. Anything detected ORs into inventoryChanged,
	// which publishes a single inventory_change at the end of the pass.
	inventoryChanged := storageAdded
	if s.eventPub != nil {
		if oldVMs == nil {
			inventoryChanged = true
		} else if newRows, err := s.queries.ListVMStatusesByCluster(ctx, cluster.ID); err == nil {
			for _, r := range newRows {
				old, existed := oldVMs[r.Vmid]
				if !existed {
					// New VM appeared.
					inventoryChanged = true
					continue
				}
				if old.Status != r.Status {
					s.logger.Info("VM status changed during sync",
						"vmid", r.Vmid,
						"old_status", old.Status,
						"new_status", r.Status,
						"cluster_id", cluster.ID,
					)
					s.eventPub.ClusterEvent(ctx, cluster.ID.String(),
						events.KindVMStateChange, "vm", r.ID.String(), "status_sync")
					inventoryChanged = true
				}
				if old.NodeID != r.NodeID {
					s.logger.Info("VM moved to different node during sync",
						"vmid", r.Vmid,
						"cluster_id", cluster.ID,
					)
					inventoryChanged = true
				}
				if old.Name != r.Name || old.Template != r.Template ||
					old.Pool != r.Pool || old.HAState != r.HaState || old.Tags != r.Tags {
					inventoryChanged = true
				}
			}
		} else {
			inventoryChanged = true
		}

		// Node status / HA-state transitions (offline, maintenance, …) and
		// node additions/removals — previously invisible to the frontend.
		if oldNodes == nil {
			inventoryChanged = true
		} else if newNodes, err := s.queries.ListNodesByCluster(ctx, cluster.ID); err == nil {
			if len(newNodes) != len(oldNodes) {
				inventoryChanged = true
			}
			for _, n := range newNodes {
				old, existed := oldNodes[n.ID]
				if !existed {
					inventoryChanged = true
					continue
				}
				if old.Status != n.Status || old.HAState != n.HaState {
					s.logger.Info("node state changed during sync",
						"node", n.Name,
						"old_status", old.Status,
						"new_status", n.Status,
						"old_ha_state", old.HAState,
						"new_ha_state", n.HaState,
						"cluster_id", cluster.ID,
					)
					inventoryChanged = true
				}
			}
		} else {
			inventoryChanged = true
		}

		// Storage pools flipping active/inactive (additions are covered by
		// storageAdded, removals by the prune row count below).
		if oldStorage == nil {
			inventoryChanged = true
		} else if newPools, err := s.queries.ListStoragePoolsByCluster(ctx, cluster.ID); err == nil {
			for _, p := range newPools {
				if active, existed := oldStorage[p.ID]; existed && active != p.Active {
					inventoryChanged = true
				}
			}
		} else {
			inventoryChanged = true
		}
	}

	// Prune VMs/CTs that no longer exist on Proxmox. Two guards keep this from
	// churning rows for guests that still exist: we restrict the delete to nodes
	// whose VM+container fetch succeeded this cycle (so a transient per-node API
	// failure won't wipe that node's inventory), and the query only deletes
	// guests unseen for longer than staleVMGrace (so a momentary non-observation
	// — e.g. a live migration's cutover instant — won't delete+reinsert the row
	// and mint a fresh vms.id). VMs on nodes that failed to sync are left intact
	// and reconciled on the next clean cycle.
	if len(syncedNodeIDs) > 0 {
		if removed, err := s.queries.DeleteStaleVMsForNodes(ctx, db.DeleteStaleVMsForNodesParams{
			ClusterID:    cluster.ID,
			GraceSeconds: int32(staleVMGrace.Seconds()),
			NodeIds:      syncedNodeIDs,
		}); err != nil {
			s.logger.Warn("failed to prune stale VMs",
				"cluster_id", cluster.ID,
				"error", err,
			)
		} else if removed > 0 {
			inventoryChanged = true
		}
	}

	// Prune storage pools that no longer exist on Proxmox (e.g. an NFS
	// share removed at the datacenter level). Flag inventory change when
	// rows are actually deleted so the frontend refreshes the UI.
	if removed, err := s.queries.DeleteStaleStoragePools(ctx, db.DeleteStaleStoragePoolsParams{
		ClusterID:    cluster.ID,
		GraceSeconds: int32(staleInventoryGrace.Seconds()),
	}); err != nil {
		s.logger.Warn("failed to prune stale storage pools",
			"cluster_id", cluster.ID,
			"error", err,
		)
	} else if removed > 0 {
		inventoryChanged = true
	}

	// Ingest completed Proxmox tasks into the audit log (deduplicating
	// against tasks that Nexara itself initiated).
	s.syncTasks(ctx, client, cluster)

	// Reconcile Nexara-dispatched tasks still marked "running": poll each UPID
	// and flip to completed/failed. Kept separate from syncTasks because
	// GetNodeTasks(since=…) filters by start-time and would never re-list a long
	// task after it finishes (watermark blind spot).
	s.reconcileRunningTasks(ctx, client, cluster)

	// Only notify the frontend when inventory data actually changed
	// (VM status transitions, VMs added/removed). This avoids triggering
	// constant refetches on every 10-second sync cycle.
	if s.eventPub != nil && inventoryChanged {
		s.eventPub.ClusterEvent(ctx, cluster.ID.String(),
			events.KindInventoryChange, "cluster", cluster.ID.String(), "sync_complete")
	}

	return result, nil
}

// syncNode syncs a single node and all its resources (VMs, containers, storage)
// and returns collected metric snapshots.
// parseNodeHAState extracts per-node HA state (e.g. "online", "maintenance",
// "fence") from the HA manager-status response. Returns an empty map when HA
// isn't configured or the response shape is unexpected.
func parseNodeHAState(raw map[string]json.RawMessage) map[string]string {
	out := make(map[string]string)
	ms, ok := raw["manager_status"]
	if !ok {
		return out
	}
	var parsed struct {
		NodeStatus map[string]string `json:"node_status"`
	}
	if err := json.Unmarshal(ms, &parsed); err != nil {
		return out
	}
	for n, st := range parsed.NodeStatus {
		out[n] = st
	}
	return out
}

func (s *Syncer) syncNode(ctx context.Context, client ProxmoxClient, clusterID uuid.UUID, node proxmox.NodeListEntry, vmExtra map[int32]vmExtraFields, nodeHAState map[string]string) (*nodeCollectionResult, error) {
	// Fetch detailed node status for PVE version, CPU info, and metrics.
	var pveVersion string
	var cpuCount int32
	var memTotal int64
	var diskTotal int64
	var cpuUsage float64
	var memUsed int64
	var cpuModel string
	var cpuCores int32
	var cpuSockets int32
	var cpuThreads int32
	var cpuMhz string
	var kernelVersion string
	var swapTotal, swapUsed, swapFree int64
	var loadAvg string
	var ioWait float64

	status, err := client.GetNodeStatus(ctx, node.Node)
	if err != nil {
		s.logger.Warn("failed to get node status, using list data",
			"node", node.Node,
			"error", err,
		)
		cpuCount = safeconv.Int32(node.MaxCPU)
		memTotal = node.MaxMem
		diskTotal = node.MaxDisk
		cpuUsage = node.CPU
		memUsed = node.Mem
	} else {
		pveVersion = status.PVEVersion
		cpuCount = safeconv.Int32(status.CPUInfo.CPUs)
		memTotal = status.Memory.Total
		diskTotal = status.RootFS.Total
		cpuUsage = status.CPU
		memUsed = status.Memory.Used
		cpuModel = status.CPUInfo.Model
		cpuCores = safeconv.Int32(status.CPUInfo.Cores)
		cpuSockets = safeconv.Int32(status.CPUInfo.Sockets)
		cpuThreads = safeconv.Int32(status.CPUInfo.Threads)
		cpuMhz = status.CPUInfo.MHz
		kernelVersion = status.Kversion
		swapTotal = status.Swap.Total
		swapUsed = status.Swap.Used
		swapFree = status.Swap.Free
		ioWait = status.Wait
		if len(status.LoadAvg) > 0 {
			loadAvg = strings.Join(status.LoadAvg, ", ")
		}
	}

	// Fetch DNS configuration.
	var dnsServers, dnsSearch string
	if dns, err := client.GetNodeDNS(ctx, node.Node); err != nil {
		s.logger.Warn("failed to get node DNS", "node", node.Node, "error", err)
	} else {
		var servers []string
		if dns.DNS1 != "" {
			servers = append(servers, dns.DNS1)
		}
		if dns.DNS2 != "" {
			servers = append(servers, dns.DNS2)
		}
		if dns.DNS3 != "" {
			servers = append(servers, dns.DNS3)
		}
		dnsServers = strings.Join(servers, ", ")
		dnsSearch = dns.Search
	}

	// Fetch timezone.
	var timezone string
	if t, err := client.GetNodeTime(ctx, node.Node); err != nil {
		s.logger.Warn("failed to get node time", "node", node.Node, "error", err)
	} else {
		timezone = t.Timezone
	}

	// Fetch subscription status.
	var subStatus, subLevel string
	if sub, err := client.GetNodeSubscription(ctx, node.Node); err != nil {
		s.logger.Warn("failed to get node subscription", "node", node.Node, "error", err)
	} else {
		subStatus = sub.Status
		subLevel = sub.Level
	}

	dbNode, err := s.queries.UpsertNode(ctx, db.UpsertNodeParams{
		ClusterID:          clusterID,
		Name:               node.Node,
		Status:             node.Status,
		CpuCount:           cpuCount,
		MemTotal:           memTotal,
		DiskTotal:          diskTotal,
		PveVersion:         pveVersion,
		SslFingerprint:     node.SSLFingerprint,
		Uptime:             node.Uptime,
		CpuModel:           cpuModel,
		CpuCores:           cpuCores,
		CpuSockets:         cpuSockets,
		CpuThreads:         cpuThreads,
		CpuMhz:             cpuMhz,
		KernelVersion:      kernelVersion,
		SwapTotal:          swapTotal,
		SwapUsed:           swapUsed,
		SwapFree:           swapFree,
		DnsServers:         dnsServers,
		DnsSearch:          dnsSearch,
		Timezone:           timezone,
		SubscriptionStatus: subStatus,
		SubscriptionLevel:  subLevel,
		LoadAvg:            loadAvg,
		IoWait:             ioWait,
		HaState:            nodeHAState[node.Node],
	})
	if err != nil {
		return nil, fmt.Errorf("upsert node %s: %w", node.Node, err)
	}

	// Sync VMs and collect metric snapshots.
	vmSnapshots, vmErr := s.syncVMs(ctx, client, clusterID, dbNode.ID, node.Node, vmExtra)
	if vmErr != nil {
		s.logger.Warn("failed to sync VMs",
			"node", node.Node,
			"error", vmErr,
		)
	}

	// Sync containers and collect metric snapshots.
	ctSnapshots, ctErr := s.syncContainers(ctx, client, clusterID, dbNode.ID, node.Node, vmExtra)
	if ctErr != nil {
		s.logger.Warn("failed to sync containers",
			"node", node.Node,
			"error", ctErr,
		)
	}

	// Sync storage pools (no metrics collected).
	storageAdded, err := s.syncStorage(ctx, client, clusterID, dbNode.ID, node.Node)
	if err != nil {
		s.logger.Warn("failed to sync storage",
			"node", node.Node,
			"error", err,
		)
	}

	// Sync physical disks.
	if disks, err := client.GetNodeDisks(ctx, node.Node); err != nil {
		s.logger.Warn("failed to sync node disks", "node", node.Node, "error", err)
	} else {
		for _, d := range disks {
			if _, err := s.queries.UpsertNodeDisk(ctx, db.UpsertNodeDiskParams{
				NodeID:    dbNode.ID,
				ClusterID: clusterID,
				DevPath:   d.DevPath,
				Model:     d.Model,
				Serial:    d.Serial,
				Size:      d.Size,
				DiskType:  d.Type,
				Health:    d.Health,
				Wearout:   d.Wearout.String(),
				Rpm:       safeconv.Int32(d.RPM),
				Vendor:    d.Vendor,
				Wwn:       d.WWN,
			}); err != nil {
				s.logger.Warn("failed to upsert node disk", "node", node.Node, "dev", d.DevPath, "error", err)
			}
		}
		_ = s.queries.DeleteStaleNodeDisks(ctx, db.DeleteStaleNodeDisksParams{NodeID: dbNode.ID, GraceSeconds: int32(staleInventoryGrace.Seconds())})
	}

	// Sync network interfaces.
	if ifaces, err := client.GetNetworkInterfaces(ctx, node.Node); err != nil {
		s.logger.Warn("failed to sync network interfaces", "node", node.Node, "error", err)
	} else {
		for _, iface := range ifaces {
			if _, err := s.queries.UpsertNodeNetworkInterface(ctx, db.UpsertNodeNetworkInterfaceParams{
				NodeID:      dbNode.ID,
				ClusterID:   clusterID,
				Iface:       iface.Iface,
				IfaceType:   iface.Type,
				Active:      iface.Active == 1,
				Autostart:   iface.Autostart == 1,
				Method:      iface.Method,
				Method6:     iface.Method6,
				Address:     iface.Address,
				Netmask:     iface.Netmask,
				Gateway:     iface.Gateway,
				Cidr:        iface.CIDR,
				BridgePorts: iface.BridgePorts,
				Comments:    iface.Comments,
			}); err != nil {
				s.logger.Warn("failed to upsert network interface", "node", node.Node, "iface", iface.Iface, "error", err)
			}
		}
		_ = s.queries.DeleteStaleNodeNetworkInterfaces(ctx, db.DeleteStaleNodeNetworkInterfacesParams{NodeID: dbNode.ID, GraceSeconds: int32(staleInventoryGrace.Seconds())})
	}

	// Sync PCI devices.
	if devs, err := client.GetNodePCIDevices(ctx, node.Node); err != nil {
		s.logger.Warn("failed to sync PCI devices", "node", node.Node, "error", err)
	} else {
		for _, d := range devs {
			if _, err := s.queries.UpsertNodePCIDevice(ctx, db.UpsertNodePCIDeviceParams{
				NodeID:          dbNode.ID,
				ClusterID:       clusterID,
				PciID:           d.ID,
				Class:           d.Class,
				DeviceName:      d.DeviceName,
				VendorName:      d.VendorName,
				Device:          d.Device,
				Vendor:          d.Vendor,
				IommuGroup:      safeconv.Int32(d.IOMMUGroup),
				SubsystemDevice: d.SubsystemDevice,
				SubsystemVendor: d.SubsystemVendor,
			}); err != nil {
				s.logger.Warn("failed to upsert PCI device", "node", node.Node, "pci", d.ID, "error", err)
			}
		}
		_ = s.queries.DeleteStaleNodePCIDevices(ctx, db.DeleteStaleNodePCIDevicesParams{NodeID: dbNode.ID, GraceSeconds: int32(staleInventoryGrace.Seconds())})
	}

	// Sum VM/CT disk and network I/O into the node metric snapshot.
	var nodeDiskRead, nodeDiskWrite, nodeNetIn, nodeNetOut int64
	allVMSnapshots := make([]vmMetricSnapshot, 0, len(vmSnapshots)+len(ctSnapshots))
	allVMSnapshots = append(allVMSnapshots, vmSnapshots...)
	allVMSnapshots = append(allVMSnapshots, ctSnapshots...)
	for _, vm := range allVMSnapshots {
		nodeDiskRead += vm.DiskRead
		nodeDiskWrite += vm.DiskWrite
		nodeNetIn += vm.NetIn
		nodeNetOut += vm.NetOut
	}

	return &nodeCollectionResult{
		Node: dbNode.ID,
		NodeMetric: nodeMetricSnapshot{
			NodeID:    dbNode.ID,
			CPUUsage:  cpuUsage,
			MemUsed:   memUsed,
			MemTotal:  memTotal,
			DiskRead:  nodeDiskRead,
			DiskWrite: nodeDiskWrite,
			NetIn:     nodeNetIn,
			NetOut:    nodeNetOut,
		},
		VMMetrics:    allVMSnapshots,
		StorageAdded: storageAdded,
		SyncOK:       vmErr == nil && ctErr == nil,
	}, nil
}

func (s *Syncer) syncVMs(ctx context.Context, client ProxmoxClient, clusterID, nodeID uuid.UUID, nodeName string, vmExtra map[int32]vmExtraFields) ([]vmMetricSnapshot, error) {
	vms, err := client.GetVMs(ctx, nodeName)
	if err != nil {
		return nil, fmt.Errorf("get VMs on %s: %w", nodeName, err)
	}

	snapshots := make([]vmMetricSnapshot, 0, len(vms))
	for _, vm := range vms {
		extra := vmExtra[safeconv.Int32(vm.VMID)]
		dbVM, err := s.queries.UpsertVM(ctx, db.UpsertVMParams{
			ClusterID: clusterID,
			NodeID:    nodeID,
			Vmid:      safeconv.Int32(vm.VMID),
			Name:      vm.Name,
			Type:      "qemu",
			Status:    vm.EffectiveStatus(),
			CpuCount:  safeconv.Int32(vm.CPUs),
			MemTotal:  vm.MaxMem,
			DiskTotal: vm.MaxDisk,
			Uptime:    vm.Uptime,
			Template:  vm.Template == 1,
			Tags:      vm.Tags,
			HaState:   extra.HAState,
			Pool:      extra.Pool,
		})
		if err != nil {
			return nil, fmt.Errorf("upsert VM %d on %s: %w", vm.VMID, nodeName, err)
		}

		s.refreshOSType(ctx, client, dbVM, nodeName, vm.VMID, "qemu", vm.EffectiveStatus())

		snapshots = append(snapshots, vmMetricSnapshot{
			VMID:      dbVM.ID,
			CPUUsage:  vm.CPU,
			MemUsed:   vm.Mem,
			MemTotal:  vm.MaxMem,
			DiskRead:  vm.DiskRead,
			DiskWrite: vm.DiskWrite,
			NetIn:     vm.NetIn,
			NetOut:    vm.NetOut,
		})
	}

	return snapshots, nil
}

// refreshOSType detects the current OS for a guest and persists it. Tracks
// two fields:
//
//   - ostype: the "best detected" value — guest-agent ID when available
//     (e.g. "ubuntu", "home-assistant"), else the Proxmox config setting.
//   - config_ostype: the Proxmox config setting (e.g. "l26", "win11") used
//     as a UI fallback when the guest-agent value isn't a known distro and
//     we still want to render a family icon.
//
// Best-effort — a failure is logged but never propagated, since this is a
// cosmetic OS detection path.
func (s *Syncer) refreshOSType(ctx context.Context, client ProxmoxClient, dbVM db.Vm, nodeName string, vmid int, kind, status string) {
	// Always fetch the Proxmox config first — its ostype value is the
	// fallback we display when the guest-agent reports something the UI
	// doesn't recognize.
	var (
		config proxmox.VMConfig
		err    error
	)
	switch kind {
	case "qemu":
		config, err = client.GetVMConfig(ctx, nodeName, vmid)
	case "lxc":
		config, err = client.GetCTConfig(ctx, nodeName, vmid)
	default:
		return
	}
	if err != nil {
		s.logger.Debug("ostype fetch failed",
			"vmid", vmid, "node", nodeName, "kind", kind, "error", err)
		return
	}
	rawConfigOS, _ := config["ostype"].(string)
	configOSType := strings.ToLower(rawConfigOS)

	if configOSType != "" && configOSType != dbVM.ConfigOstype {
		if err := s.queries.SetVMConfigOSType(ctx, db.SetVMConfigOSTypeParams{ID: dbVM.ID, ConfigOstype: configOSType}); err != nil {
			s.logger.Debug("config_ostype persist failed",
				"vmid", vmid, "kind", kind, "error", err)
		}
	}

	// Prefer the guest-agent reported OS when QEMU and running — it's more
	// specific (distro name vs. generic "l26") and handles the case where
	// the Proxmox config says "other" but the guest is really Linux.
	detected := ""
	if kind == "qemu" && status == "running" {
		if info, err := client.GetGuestAgentOSInfo(ctx, nodeName, vmid); err == nil && info != nil {
			if info.ID != "" {
				detected = strings.ToLower(info.ID)
			} else if info.Name != "" {
				detected = strings.ToLower(info.Name)
			}
		}
	}
	if detected == "" {
		detected = configOSType
	}

	if detected == "" || detected == dbVM.Ostype {
		return
	}
	if err := s.queries.SetVMOSType(ctx, db.SetVMOSTypeParams{ID: dbVM.ID, Ostype: detected}); err != nil {
		s.logger.Debug("ostype persist failed",
			"vmid", vmid, "kind", kind, "error", err)
	}
}

func (s *Syncer) syncContainers(ctx context.Context, client ProxmoxClient, clusterID, nodeID uuid.UUID, nodeName string, vmExtra map[int32]vmExtraFields) ([]vmMetricSnapshot, error) {
	cts, err := client.GetContainers(ctx, nodeName)
	if err != nil {
		return nil, fmt.Errorf("get containers on %s: %w", nodeName, err)
	}

	snapshots := make([]vmMetricSnapshot, 0, len(cts))
	for _, ct := range cts {
		extra := vmExtra[safeconv.Int32(ct.VMID)]
		dbVM, err := s.queries.UpsertVM(ctx, db.UpsertVMParams{
			ClusterID: clusterID,
			NodeID:    nodeID,
			Vmid:      safeconv.Int32(ct.VMID),
			Name:      ct.Name,
			Type:      "lxc",
			Status:    ct.Status,
			CpuCount:  safeconv.Int32(ct.CPUs),
			MemTotal:  ct.MaxMem,
			DiskTotal: ct.MaxDisk,
			Uptime:    ct.Uptime,
			Template:  ct.Template == 1,
			Tags:      ct.Tags,
			HaState:   extra.HAState,
			Pool:      extra.Pool,
		})
		if err != nil {
			return nil, fmt.Errorf("upsert container %d on %s: %w", ct.VMID, nodeName, err)
		}

		s.refreshOSType(ctx, client, dbVM, nodeName, ct.VMID, "lxc", ct.Status)

		snapshots = append(snapshots, vmMetricSnapshot{
			VMID:      dbVM.ID,
			CPUUsage:  ct.CPU,
			MemUsed:   ct.Mem,
			MemTotal:  ct.MaxMem,
			DiskRead:  ct.DiskRead,
			DiskWrite: ct.DiskWrite,
			NetIn:     ct.NetIn,
			NetOut:    ct.NetOut,
		})
	}

	return snapshots, nil
}

// syncStorage upserts the node's storage pools. Returns true if any pool
// was newly inserted (i.e. created on Proxmox since the last sync).
func (s *Syncer) syncStorage(ctx context.Context, client ProxmoxClient, clusterID, nodeID uuid.UUID, nodeName string) (bool, error) {
	pools, err := client.GetStoragePools(ctx, nodeName)
	if err != nil {
		return false, fmt.Errorf("get storage pools on %s: %w", nodeName, err)
	}

	added := false
	for _, pool := range pools {
		row, err := s.queries.UpsertStoragePool(ctx, db.UpsertStoragePoolParams{
			ClusterID: clusterID,
			NodeID:    nodeID,
			Storage:   pool.Storage,
			Type:      pool.Type,
			Content:   pool.Content,
			Active:    pool.Active == 1,
			Enabled:   pool.Enabled == 1,
			Shared:    pool.Shared == 1,
			Total:     pool.Total,
			Used:      pool.Used,
			Avail:     pool.Avail,
		})
		if err != nil {
			return added, fmt.Errorf("upsert storage pool %s on %s: %w", pool.Storage, nodeName, err)
		}
		if row.Inserted {
			added = true
		}
	}

	return added, nil
}

// cephCollectionResult holds Ceph metric data from a sync cycle.
type cephCollectionResult struct {
	CephCluster *cephClusterMetricSnapshot
	CephOSDs    []cephOSDMetricSnapshot
	CephPools   []cephPoolMetricSnapshot
}

// syncCeph syncs Ceph data once per cluster. Returns nil if Ceph is not available.
func (s *Syncer) syncCeph(ctx context.Context, client ProxmoxClient, clusterID uuid.UUID, nodeName string) *cephCollectionResult {
	status, err := client.GetCephStatus(ctx, nodeName)
	if err != nil {
		// Ceph not installed or not configured — skip silently.
		// Proxmox returns 404 when ceph is not configured, and 500
		// with "binary not installed" when ceph-mon is not present.
		errMsg := err.Error()
		if errors.Is(err, proxmox.ErrNotFound) ||
			strings.Contains(errMsg, "binary not installed") ||
			strings.Contains(errMsg, "ceph-mon") {
			return nil
		}
		s.logger.Warn("failed to get ceph status",
			"cluster_id", clusterID,
			"node", nodeName,
			"error", err,
		)
		return nil
	}

	result := &cephCollectionResult{
		CephCluster: &cephClusterMetricSnapshot{
			ClusterID:    clusterID,
			HealthStatus: status.Health.Status,
			OSDsTotal:    status.OSDMap.NumOSDs,
			OSDsUp:       status.OSDMap.NumUpOSDs,
			OSDsIn:       status.OSDMap.NumInOSDs,
			PGsTotal:     status.PGMap.NumPGs,
			BytesUsed:    status.PGMap.BytesUsed,
			BytesAvail:   status.PGMap.BytesAvail,
			BytesTotal:   status.PGMap.BytesTotal,
			ReadOpsSec:   status.PGMap.ReadOpPerSec,
			WriteOpsSec:  status.PGMap.WritOpPerSec,
			ReadBytesSec: status.PGMap.ReadBytesSec,
			WritBytesSec: status.PGMap.WritBytesSec,
		},
	}

	// Collect OSD metrics.
	osdResp, err := client.GetCephOSDs(ctx, nodeName)
	if err != nil {
		s.logger.Warn("failed to get ceph osds", "cluster_id", clusterID, "error", err)
	} else {
		result.CephOSDs = flattenOSDs(clusterID, &osdResp.Root)
	}

	// Collect pool metrics.
	pools, err := client.GetCephPools(ctx, nodeName)
	if err != nil {
		s.logger.Warn("failed to get ceph pools", "cluster_id", clusterID, "error", err)
	} else {
		for _, p := range pools {
			result.CephPools = append(result.CephPools, cephPoolMetricSnapshot{
				ClusterID:    clusterID,
				PoolID:       int(p.Pool),
				PoolName:     p.PoolName,
				Size:         int(p.Size),
				MinSize:      int(p.MinSize),
				PGNum:        int(p.PGNum),
				BytesUsed:    p.BytesUsed,
				PercentUsed:  p.PercentUsed,
				ReadOpsSec:   p.ReadOpPerSec,
				WriteOpsSec:  p.WritOpPerSec,
				ReadBytesSec: p.ReadBytesSec,
				WritBytesSec: p.WritBytesSec,
			})
		}
	}

	return result
}

// flattenOSDs walks the OSD tree and extracts OSD nodes.
func flattenOSDs(clusterID uuid.UUID, node *proxmox.CephOSDTreeNode) []cephOSDMetricSnapshot {
	var result []cephOSDMetricSnapshot
	if node.Type == "osd" {
		result = append(result, cephOSDMetricSnapshot{
			ClusterID:   clusterID,
			OSDID:       int(node.ID),
			OSDName:     node.Name,
			Host:        node.Host,
			StatusUp:    node.Status == "up",
			StatusIn:    true, // present in tree means "in"
			CrushWeight: node.CrushWeight,
		})
	}
	for i := range node.Children {
		// Propagate host name to child OSDs
		if node.Type == "host" {
			for j := range node.Children {
				if node.Children[j].Host == "" {
					node.Children[j].Host = node.Name
				}
			}
		}
		result = append(result, flattenOSDs(clusterID, &node.Children[i])...)
	}
	return result
}

// SyncAllPBS syncs all active PBS servers and returns collected metric results.
func (s *Syncer) SyncAllPBS(ctx context.Context) []*PBSMetricResult {
	servers, err := s.queries.ListActivePBSServers(ctx)
	if err != nil {
		s.logger.Error("failed to list active PBS servers", "error", err)
		return nil
	}

	var results []*PBSMetricResult
	for _, server := range servers {
		s.logger.Info("syncing PBS server", "pbs_id", server.ID, "name", server.Name)
		result, err := s.syncPBSServer(ctx, server)
		if err != nil {
			s.logger.Error("failed to sync PBS server",
				"pbs_id", server.ID,
				"name", server.Name,
				"error", err,
			)
			continue
		}
		if result != nil {
			results = append(results, result)
		}
	}

	return results
}

// syncPBSServer syncs a single PBS server: datastores, snapshots, sync jobs, verify jobs.
func (s *Syncer) syncPBSServer(ctx context.Context, server db.PbsServer) (*PBSMetricResult, error) {
	client, err := s.pbsProxmoxClient(ctx, server)
	if err != nil {
		return nil, fmt.Errorf("sync PBS %s: %w", server.ID, err)
	}

	now := time.Now()
	result := &PBSMetricResult{
		PBSServerID: server.ID,
		CollectedAt: now,
	}

	// Sync datastore status (metrics).
	dsStatus, err := client.GetDatastoreStatus(ctx)
	if err != nil {
		s.logger.Warn("failed to get PBS datastore status", "pbs_id", server.ID, "error", err)
	} else {
		s.logger.Debug("PBS datastore status", "pbs_id", server.ID, "count", len(dsStatus))
		for _, ds := range dsStatus {
			result.DatastoreMetrics = append(result.DatastoreMetrics, pbsDatastoreMetricSnapshot{
				PBSServerID: server.ID,
				Datastore:   ds.Store,
				Total:       ds.Total,
				Used:        ds.Used,
				Avail:       ds.Avail,
			})
		}
	}

	// Sync snapshots per datastore.
	datastores, err := client.GetDatastores(ctx)
	if err != nil {
		s.logger.Warn("failed to get PBS datastores", "pbs_id", server.ID, "error", err)
	} else {
		s.logger.Debug("PBS datastores", "pbs_id", server.ID, "count", len(datastores))
		for _, ds := range datastores {
			snaps, err := client.GetSnapshots(ctx, ds.Name)
			if err != nil {
				s.logger.Warn("failed to get PBS snapshots",
					"pbs_id", server.ID,
					"datastore", ds.Name,
					"error", err,
				)
				continue
			}
			s.logger.Debug("PBS snapshots", "pbs_id", server.ID, "datastore", ds.Name, "count", len(snaps))
			for _, snap := range snaps {
				verified := false
				if snap.Verification != nil && snap.Verification.State == "ok" {
					verified = true
				}
				_, uErr := s.queries.UpsertPBSSnapshot(ctx, db.UpsertPBSSnapshotParams{
					PbsServerID: server.ID,
					Datastore:   ds.Name,
					BackupType:  snap.BackupType,
					BackupID:    snap.BackupID,
					BackupTime:  snap.BackupTime,
					Size:        snap.Size,
					Verified:    verified,
					Protected:   snap.Protected,
					Comment:     snap.Comment,
					Owner:       snap.Owner,
				})
				if uErr != nil {
					s.logger.Warn("failed to upsert PBS snapshot",
						"pbs_id", server.ID,
						"error", uErr,
					)
				}
			}
		}
	}

	// Sync sync jobs.
	syncJobs, err := client.GetSyncJobs(ctx)
	if err != nil {
		s.logger.Warn("failed to get PBS sync jobs", "pbs_id", server.ID, "error", err)
	} else {
		s.logger.Debug("PBS sync jobs", "pbs_id", server.ID, "count", len(syncJobs))
		for _, job := range syncJobs {
			_, uErr := s.queries.UpsertPBSSyncJob(ctx, db.UpsertPBSSyncJobParams{
				PbsServerID:  server.ID,
				JobID:        job.ID,
				Store:        job.Store,
				Remote:       job.Remote,
				RemoteStore:  job.RemoteStore,
				Schedule:     job.Schedule,
				LastRunState: job.LastRunState,
				NextRun:      job.NextRun,
				Comment:      job.Comment,
			})
			if uErr != nil {
				s.logger.Warn("failed to upsert PBS sync job", "pbs_id", server.ID, "error", uErr)
			}
		}
	}

	// Sync verify jobs.
	verifyJobs, err := client.GetVerifyJobs(ctx)
	if err != nil {
		s.logger.Warn("failed to get PBS verify jobs", "pbs_id", server.ID, "error", err)
	} else {
		s.logger.Debug("PBS verify jobs", "pbs_id", server.ID, "count", len(verifyJobs))
		for _, job := range verifyJobs {
			_, uErr := s.queries.UpsertPBSVerifyJob(ctx, db.UpsertPBSVerifyJobParams{
				PbsServerID:  server.ID,
				JobID:        job.ID,
				Store:        job.Store,
				Schedule:     job.Schedule,
				LastRunState: job.LastRunState,
				Comment:      job.Comment,
			})
			if uErr != nil {
				s.logger.Warn("failed to upsert PBS verify job", "pbs_id", server.ID, "error", uErr)
			}
		}
	}

	// Warn if PBS returned no data at all — likely a token permissions issue.
	if len(dsStatus) == 0 && len(datastores) == 0 && len(syncJobs) == 0 && len(verifyJobs) == 0 {
		s.logger.Warn("PBS server returned no data — check API token permissions (ACLs)",
			"pbs_id", server.ID,
			"name", server.Name,
		)
	}

	// Prune stale inventory.
	if err := s.queries.DeleteStalePBSSnapshots(ctx, db.DeleteStalePBSSnapshotsParams{
		PbsServerID:  server.ID,
		GraceSeconds: int32(staleInventoryGrace.Seconds()),
	}); err != nil {
		s.logger.Warn("failed to prune stale PBS snapshots", "pbs_id", server.ID, "error", err)
	}
	if err := s.queries.DeleteStalePBSSyncJobs(ctx, db.DeleteStalePBSSyncJobsParams{
		PbsServerID:  server.ID,
		GraceSeconds: int32(staleInventoryGrace.Seconds()),
	}); err != nil {
		s.logger.Warn("failed to prune stale PBS sync jobs", "pbs_id", server.ID, "error", err)
	}
	if err := s.queries.DeleteStalePBSVerifyJobs(ctx, db.DeleteStalePBSVerifyJobsParams{
		PbsServerID:  server.ID,
		GraceSeconds: int32(staleInventoryGrace.Seconds()),
	}); err != nil {
		s.logger.Warn("failed to prune stale PBS verify jobs", "pbs_id", server.ID, "error", err)
	}

	// Emit pbs_change event so frontend caches auto-refresh.
	if s.eventPub != nil {
		s.eventPub.SystemEvent(ctx, events.KindPBSChange, "pbs_sync_complete")
	}

	return result, nil
}

// syncNodeAddresses fetches corosync cluster status and updates node IP addresses.
func (s *Syncer) syncNodeAddresses(ctx context.Context, client ProxmoxClient, clusterID uuid.UUID) {
	entries, err := client.GetClusterStatus(ctx)
	if err != nil {
		s.logger.Debug("failed to get cluster status for node addresses", "cluster_id", clusterID, "error", err)
		return
	}

	for _, entry := range entries {
		if entry.Type != "node" || entry.IP == "" {
			continue
		}
		_ = s.queries.UpdateNodeAddress(ctx, db.UpdateNodeAddressParams{
			ClusterID: clusterID,
			Name:      entry.Name,
			Address:   entry.IP,
		})
	}
}

// SyncAll syncs all active clusters and returns collected metric results.
func (s *Syncer) SyncAll(ctx context.Context) []*ClusterMetricResult {
	clusters, err := s.queries.ListActiveClusters(ctx)
	if err != nil {
		s.logger.Error("failed to list active clusters", "error", err)
		return nil
	}

	var results []*ClusterMetricResult
	for _, cluster := range clusters {
		s.logger.Info("syncing cluster", "cluster_id", cluster.ID, "name", cluster.Name)
		result, err := s.SyncCluster(ctx, cluster)
		if err != nil {
			s.logger.Error("failed to sync cluster",
				"cluster_id", cluster.ID,
				"name", cluster.Name,
				"error", err,
			)
			s.reportSyncError(ctx, cluster, err)
			// Record failure for every known node so the health monitor
			// can mark them offline after the threshold is reached.
			if s.healthMonitor != nil {
				if nodes, listErr := s.queries.ListNodesByCluster(ctx, cluster.ID); listErr == nil {
					for _, n := range nodes {
						s.healthMonitor.RecordFailure(ctx, cluster.ID, n.ID, n.Name)
					}
				}
			}
			continue
		}
		if result != nil {
			results = append(results, result)
		}
	}

	return results
}

// reportSyncError writes an audit log entry and publishes an event when a cluster sync fails.
// Rate-limited to one report per cluster per 5 minutes to avoid flooding the audit log.
func (s *Syncer) reportSyncError(ctx context.Context, cluster db.Cluster, syncErr error) {
	if s.lastSyncError == nil {
		s.lastSyncError = make(map[uuid.UUID]time.Time)
	}
	if last, ok := s.lastSyncError[cluster.ID]; ok && time.Since(last) < 5*time.Minute {
		return
	}
	s.lastSyncError[cluster.ID] = time.Now()

	errMsg := syncErr.Error()

	action := "sync_failed"
	if strings.Contains(errMsg, "fingerprint mismatch") {
		action = "tls_fingerprint_mismatch"
	}

	details, _ := json.Marshal(map[string]string{
		"cluster_name": cluster.Name,
		"error":        errMsg,
	})

	_ = s.queries.InsertAuditLog(ctx, db.InsertAuditLogParams{
		ClusterID:    pgtype.UUID{Bytes: cluster.ID, Valid: true},
		UserID:       pgtype.UUID{Bytes: auth.SystemUserID, Valid: true},
		ResourceType: "cluster",
		ResourceID:   cluster.ID.String(),
		Action:       action,
		Details:      details,
	})

	if s.eventPub != nil {
		s.eventPub.ClusterEvent(ctx, cluster.ID.String(), events.KindAuditEntry, "cluster", cluster.ID.String(), action)
	}
}
