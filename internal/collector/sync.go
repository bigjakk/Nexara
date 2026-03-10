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

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// SyncQueries defines the database operations needed by the Syncer.
// This interface enables testing with mock implementations.
type SyncQueries interface {
	ListActiveClusters(ctx context.Context) ([]db.Cluster, error)
	UpsertNode(ctx context.Context, arg db.UpsertNodeParams) (db.Node, error)
	UpsertVM(ctx context.Context, arg db.UpsertVMParams) (db.Vm, error)
	UpsertStoragePool(ctx context.Context, arg db.UpsertStoragePoolParams) (db.StoragePool, error)
	GetNodeByClusterAndName(ctx context.Context, arg db.GetNodeByClusterAndNameParams) (db.Node, error)
	ListVMStatusesByCluster(ctx context.Context, clusterID uuid.UUID) ([]db.ListVMStatusesByClusterRow, error)
	DeleteStaleVMs(ctx context.Context, arg db.DeleteStaleVMsParams) error
	UpdateNodeAddress(ctx context.Context, arg db.UpdateNodeAddressParams) error
	// Audit
	InsertAuditLog(ctx context.Context, arg db.InsertAuditLogParams) error
	InsertAuditLogWithSource(ctx context.Context, arg db.InsertAuditLogWithSourceParams) error
	// Task sync
	GetTaskSyncState(ctx context.Context, clusterID uuid.UUID) (int64, error)
	UpsertTaskSyncState(ctx context.Context, arg db.UpsertTaskSyncStateParams) error
	ExistsTaskHistoryByUPID(ctx context.Context, upid string) (bool, error)
	ExistsAuditLogByUPID(ctx context.Context, upid string) (bool, error)
	GetVMByClusterAndVmid(ctx context.Context, arg db.GetVMByClusterAndVmidParams) (db.Vm, error)
	// PBS queries
	ListActivePBSServers(ctx context.Context) ([]db.PbsServer, error)
	UpsertPBSSnapshot(ctx context.Context, arg db.UpsertPBSSnapshotParams) (db.PbsSnapshot, error)
	UpsertPBSSyncJob(ctx context.Context, arg db.UpsertPBSSyncJobParams) (db.PbsSyncJob, error)
	UpsertPBSVerifyJob(ctx context.Context, arg db.UpsertPBSVerifyJobParams) (db.PbsVerifyJob, error)
	DeleteStalePBSSnapshots(ctx context.Context, arg db.DeleteStalePBSSnapshotsParams) error
	DeleteStalePBSSyncJobs(ctx context.Context, arg db.DeleteStalePBSSyncJobsParams) error
	DeleteStalePBSVerifyJobs(ctx context.Context, arg db.DeleteStalePBSVerifyJobsParams) error
}

// ProxmoxClient defines the Proxmox API methods needed by the Syncer.
type ProxmoxClient interface {
	GetNodes(ctx context.Context) ([]proxmox.NodeListEntry, error)
	GetNodeStatus(ctx context.Context, node string) (*proxmox.NodeStatus, error)
	GetVMs(ctx context.Context, node string) ([]proxmox.VirtualMachine, error)
	GetContainers(ctx context.Context, node string) ([]proxmox.Container, error)
	GetStoragePools(ctx context.Context, node string) ([]proxmox.StoragePool, error)
	GetCephStatus(ctx context.Context, node string) (*proxmox.CephStatus, error)
	GetCephOSDs(ctx context.Context, node string) (*proxmox.CephOSDResponse, error)
	GetCephPools(ctx context.Context, node string) ([]proxmox.CephPool, error)
	GetClusterStatus(ctx context.Context) ([]proxmox.ClusterStatusEntry, error)
	GetClusterResources(ctx context.Context, resourceType string) ([]proxmox.ClusterResource, error)
	GetNodeTasks(ctx context.Context, node string, since int64, limit int) ([]proxmox.NodeTask, error)
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

// systemUserID is the well-known UUID for system-initiated actions (migration 000013).
var systemUserID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

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
	healthMonitor    *HealthMonitor
	eventPub         *events.Publisher
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

// SetEventPublisher attaches an event publisher for status change notifications.
func (s *Syncer) SetEventPublisher(pub *events.Publisher) {
	s.eventPub = pub
}

// SetHealthMonitor attaches a health monitor to the syncer.
func (s *Syncer) SetHealthMonitor(h *HealthMonitor) {
	s.healthMonitor = h
}

// SyncCluster discovers nodes, VMs, containers, and storage from a Proxmox cluster,
// upserts them into the database, and returns collected metric snapshots.
func (s *Syncer) SyncCluster(ctx context.Context, cluster db.Cluster) (*clusterMetricResult, error) {
	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, s.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("sync cluster %s: decrypt token: %w", cluster.ID, err)
	}

	client, err := s.clientFactory(cluster.ApiUrl, cluster.TokenID, tokenSecret, cluster.TlsFingerprint)
	if err != nil {
		return nil, fmt.Errorf("sync cluster %s: create client: %w", cluster.ID, err)
	}

	nodes, err := client.GetNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("sync cluster %s: get nodes: %w", cluster.ID, err)
	}

	// Fetch cluster-level resource data for HA state and pool membership.
	// These fields are only available from /cluster/resources, not per-node endpoints.
	vmExtra := make(map[int32]vmExtraFields)
	if resources, resErr := client.GetClusterResources(ctx, ""); resErr == nil {
		for _, r := range resources {
			if (r.Type == "qemu" || r.Type == "lxc") && r.VMID > 0 {
				vmExtra[int32(r.VMID)] = vmExtraFields{HAState: r.HAState, Pool: r.Pool}
			}
		}
	} else {
		s.logger.Warn("failed to get cluster resources for HA/pool data",
			"cluster_id", cluster.ID,
			"error", resErr,
		)
	}

	// Snapshot current VM statuses so we can detect changes after sync.
	var oldStatuses map[int32]string
	if s.eventPub != nil {
		if rows, err := s.queries.ListVMStatusesByCluster(ctx, cluster.ID); err == nil {
			oldStatuses = make(map[int32]string, len(rows))
			for _, r := range rows {
				oldStatuses[r.Vmid] = r.Status
			}
		}
	}

	now := time.Now()
	result := &clusterMetricResult{
		ClusterID:   cluster.ID,
		CollectedAt: now,
	}

	for _, node := range nodes {
		nr, err := s.syncNode(ctx, client, cluster.ID, node, vmExtra)
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

	// Check for VM status changes and notify the frontend.
	if s.eventPub != nil && oldStatuses != nil {
		if newRows, err := s.queries.ListVMStatusesByCluster(ctx, cluster.ID); err == nil {
			for _, r := range newRows {
				if old, ok := oldStatuses[r.Vmid]; ok && old != r.Status {
					s.logger.Info("VM status changed during sync",
						"vmid", r.Vmid,
						"old_status", old,
						"new_status", r.Status,
						"cluster_id", cluster.ID,
					)
					s.eventPub.ClusterEvent(ctx, cluster.ID.String(),
						events.KindVMStateChange, "vm", r.ID.String(), "status_sync")
				}
			}
		}
	}

	// Prune VMs/CTs that no longer exist on Proxmox. Any VM that wasn't
	// upserted during this sync cycle will have a last_seen_at older than
	// the timestamp captured at the start of the cycle.
	if err := s.queries.DeleteStaleVMs(ctx, db.DeleteStaleVMsParams{
		ClusterID:  cluster.ID,
		LastSeenAt: now,
	}); err != nil {
		s.logger.Warn("failed to prune stale VMs",
			"cluster_id", cluster.ID,
			"error", err,
		)
	}

	// Ingest completed Proxmox tasks into the audit log (deduplicating
	// against tasks that Nexara itself initiated).
	s.syncTasks(ctx, client, cluster)

	return result, nil
}

// syncNode syncs a single node and all its resources (VMs, containers, storage)
// and returns collected metric snapshots.
func (s *Syncer) syncNode(ctx context.Context, client ProxmoxClient, clusterID uuid.UUID, node proxmox.NodeListEntry, vmExtra map[int32]vmExtraFields) (*nodeCollectionResult, error) {
	// Fetch detailed node status for PVE version, CPU info, and metrics.
	var pveVersion string
	var cpuCount int32
	var memTotal int64
	var diskTotal int64
	var cpuUsage float64
	var memUsed int64

	status, err := client.GetNodeStatus(ctx, node.Node)
	if err != nil {
		s.logger.Warn("failed to get node status, using list data",
			"node", node.Node,
			"error", err,
		)
		cpuCount = int32(node.MaxCPU)
		memTotal = node.MaxMem
		diskTotal = node.MaxDisk
		cpuUsage = node.CPU
		memUsed = node.Mem
	} else {
		pveVersion = status.PVEVersion
		cpuCount = int32(status.CPUInfo.CPUs)
		memTotal = status.Memory.Total
		diskTotal = status.RootFS.Total
		cpuUsage = status.CPU
		memUsed = status.Memory.Used
	}

	dbNode, err := s.queries.UpsertNode(ctx, db.UpsertNodeParams{
		ClusterID:      clusterID,
		Name:           node.Node,
		Status:         node.Status,
		CpuCount:       cpuCount,
		MemTotal:       memTotal,
		DiskTotal:      diskTotal,
		PveVersion:     pveVersion,
		SslFingerprint: node.SSLFingerprint,
		Uptime:         node.Uptime,
	})
	if err != nil {
		return nil, fmt.Errorf("upsert node %s: %w", node.Node, err)
	}

	// Sync VMs and collect metric snapshots.
	vmSnapshots, err := s.syncVMs(ctx, client, clusterID, dbNode.ID, node.Node, vmExtra)
	if err != nil {
		s.logger.Warn("failed to sync VMs",
			"node", node.Node,
			"error", err,
		)
	}

	// Sync containers and collect metric snapshots.
	ctSnapshots, err := s.syncContainers(ctx, client, clusterID, dbNode.ID, node.Node, vmExtra)
	if err != nil {
		s.logger.Warn("failed to sync containers",
			"node", node.Node,
			"error", err,
		)
	}

	// Sync storage pools (no metrics collected).
	if err := s.syncStorage(ctx, client, clusterID, dbNode.ID, node.Node); err != nil {
		s.logger.Warn("failed to sync storage",
			"node", node.Node,
			"error", err,
		)
	}

	// Sum VM/CT disk and network I/O into the node metric snapshot.
	var nodeDiskRead, nodeDiskWrite, nodeNetIn, nodeNetOut int64
	allVMSnapshots := append(vmSnapshots, ctSnapshots...)
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
		VMMetrics: allVMSnapshots,
	}, nil
}

func (s *Syncer) syncVMs(ctx context.Context, client ProxmoxClient, clusterID, nodeID uuid.UUID, nodeName string, vmExtra map[int32]vmExtraFields) ([]vmMetricSnapshot, error) {
	vms, err := client.GetVMs(ctx, nodeName)
	if err != nil {
		return nil, fmt.Errorf("get VMs on %s: %w", nodeName, err)
	}

	var snapshots []vmMetricSnapshot
	for _, vm := range vms {
		extra := vmExtra[int32(vm.VMID)]
		dbVM, err := s.queries.UpsertVM(ctx, db.UpsertVMParams{
			ClusterID: clusterID,
			NodeID:    nodeID,
			Vmid:      int32(vm.VMID),
			Name:      vm.Name,
			Type:      "qemu",
			Status:    vm.EffectiveStatus(),
			CpuCount:  int32(vm.CPUs),
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

func (s *Syncer) syncContainers(ctx context.Context, client ProxmoxClient, clusterID, nodeID uuid.UUID, nodeName string, vmExtra map[int32]vmExtraFields) ([]vmMetricSnapshot, error) {
	cts, err := client.GetContainers(ctx, nodeName)
	if err != nil {
		return nil, fmt.Errorf("get containers on %s: %w", nodeName, err)
	}

	var snapshots []vmMetricSnapshot
	for _, ct := range cts {
		extra := vmExtra[int32(ct.VMID)]
		dbVM, err := s.queries.UpsertVM(ctx, db.UpsertVMParams{
			ClusterID: clusterID,
			NodeID:    nodeID,
			Vmid:      int32(ct.VMID),
			Name:      ct.Name,
			Type:      "lxc",
			Status:    ct.Status,
			CpuCount:  int32(ct.CPUs),
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

func (s *Syncer) syncStorage(ctx context.Context, client ProxmoxClient, clusterID, nodeID uuid.UUID, nodeName string) error {
	pools, err := client.GetStoragePools(ctx, nodeName)
	if err != nil {
		return fmt.Errorf("get storage pools on %s: %w", nodeName, err)
	}

	for _, pool := range pools {
		_, err := s.queries.UpsertStoragePool(ctx, db.UpsertStoragePoolParams{
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
			return fmt.Errorf("upsert storage pool %s on %s: %w", pool.Storage, nodeName, err)
		}
	}

	return nil
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
		// Ceph not installed or not configured — skip silently for 404.
		if errors.Is(err, proxmox.ErrNotFound) {
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
func (s *Syncer) SyncAllPBS(ctx context.Context) []*pbsMetricResult {
	servers, err := s.queries.ListActivePBSServers(ctx)
	if err != nil {
		s.logger.Error("failed to list active PBS servers", "error", err)
		return nil
	}

	var results []*pbsMetricResult
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
func (s *Syncer) syncPBSServer(ctx context.Context, server db.PbsServer) (*pbsMetricResult, error) {
	tokenSecret, err := crypto.Decrypt(server.TokenSecretEncrypted, s.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("sync PBS %s: decrypt token: %w", server.ID, err)
	}

	client, err := s.pbsClientFactory(server.ApiUrl, server.TokenID, tokenSecret, server.TlsFingerprint)
	if err != nil {
		return nil, fmt.Errorf("sync PBS %s: create client: %w", server.ID, err)
	}

	now := time.Now()
	result := &pbsMetricResult{
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

	// Prune stale inventory.
	if err := s.queries.DeleteStalePBSSnapshots(ctx, db.DeleteStalePBSSnapshotsParams{
		PbsServerID: server.ID,
		LastSeenAt:  now,
	}); err != nil {
		s.logger.Warn("failed to prune stale PBS snapshots", "pbs_id", server.ID, "error", err)
	}
	if err := s.queries.DeleteStalePBSSyncJobs(ctx, db.DeleteStalePBSSyncJobsParams{
		PbsServerID: server.ID,
		LastSeenAt:  now,
	}); err != nil {
		s.logger.Warn("failed to prune stale PBS sync jobs", "pbs_id", server.ID, "error", err)
	}
	if err := s.queries.DeleteStalePBSVerifyJobs(ctx, db.DeleteStalePBSVerifyJobsParams{
		PbsServerID: server.ID,
		LastSeenAt:  now,
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
func (s *Syncer) SyncAll(ctx context.Context) []*clusterMetricResult {
	clusters, err := s.queries.ListActiveClusters(ctx)
	if err != nil {
		s.logger.Error("failed to list active clusters", "error", err)
		return nil
	}

	var results []*clusterMetricResult
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
		UserID:       systemUserID,
		ResourceType: "cluster",
		ResourceID:   cluster.ID.String(),
		Action:       action,
		Details:      details,
	})

	if s.eventPub != nil {
		s.eventPub.ClusterEvent(ctx, cluster.ID.String(), events.KindAuditEntry, "cluster", cluster.ID.String(), action)
	}
}
