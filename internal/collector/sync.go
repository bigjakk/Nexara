package collector

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/proxdash/proxdash/internal/crypto"
	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/proxmox"
)

// SyncQueries defines the database operations needed by the Syncer.
// This interface enables testing with mock implementations.
type SyncQueries interface {
	ListActiveClusters(ctx context.Context) ([]db.Cluster, error)
	UpsertNode(ctx context.Context, arg db.UpsertNodeParams) (db.Node, error)
	UpsertVM(ctx context.Context, arg db.UpsertVMParams) (db.Vm, error)
	UpsertStoragePool(ctx context.Context, arg db.UpsertStoragePoolParams) (db.StoragePool, error)
	GetNodeByClusterAndName(ctx context.Context, arg db.GetNodeByClusterAndNameParams) (db.Node, error)
	DeleteStaleVMs(ctx context.Context, arg db.DeleteStaleVMsParams) error
}

// ProxmoxClient defines the Proxmox API methods needed by the Syncer.
type ProxmoxClient interface {
	GetNodes(ctx context.Context) ([]proxmox.NodeListEntry, error)
	GetNodeStatus(ctx context.Context, node string) (*proxmox.NodeStatus, error)
	GetVMs(ctx context.Context, node string) ([]proxmox.VirtualMachine, error)
	GetContainers(ctx context.Context, node string) ([]proxmox.Container, error)
	GetStoragePools(ctx context.Context, node string) ([]proxmox.StoragePool, error)
}

// ClientFactory creates a ProxmoxClient from cluster credentials.
// Extracted for testability.
type ClientFactory func(apiURL, tokenID, tokenSecret, tlsFingerprint string) (ProxmoxClient, error)

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

// Syncer discovers and persists Proxmox inventory data.
type Syncer struct {
	queries       SyncQueries
	encryptionKey string
	clientFactory ClientFactory
	healthMonitor *HealthMonitor
	logger        *slog.Logger
}

// NewSyncer creates a Syncer with the default Proxmox client factory.
func NewSyncer(queries SyncQueries, encryptionKey string, logger *slog.Logger) *Syncer {
	return &Syncer{
		queries:       queries,
		encryptionKey: encryptionKey,
		clientFactory: DefaultClientFactory,
		logger:        logger,
	}
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

	now := time.Now()
	result := &clusterMetricResult{
		ClusterID:   cluster.ID,
		CollectedAt: now,
	}

	for _, node := range nodes {
		nr, err := s.syncNode(ctx, client, cluster.ID, node)
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

	return result, nil
}

// syncNode syncs a single node and all its resources (VMs, containers, storage)
// and returns collected metric snapshots.
func (s *Syncer) syncNode(ctx context.Context, client ProxmoxClient, clusterID uuid.UUID, node proxmox.NodeListEntry) (*nodeCollectionResult, error) {
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
	vmSnapshots, err := s.syncVMs(ctx, client, clusterID, dbNode.ID, node.Node)
	if err != nil {
		s.logger.Warn("failed to sync VMs",
			"node", node.Node,
			"error", err,
		)
	}

	// Sync containers and collect metric snapshots.
	ctSnapshots, err := s.syncContainers(ctx, client, clusterID, dbNode.ID, node.Node)
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

func (s *Syncer) syncVMs(ctx context.Context, client ProxmoxClient, clusterID, nodeID uuid.UUID, nodeName string) ([]vmMetricSnapshot, error) {
	vms, err := client.GetVMs(ctx, nodeName)
	if err != nil {
		return nil, fmt.Errorf("get VMs on %s: %w", nodeName, err)
	}

	var snapshots []vmMetricSnapshot
	for _, vm := range vms {
		dbVM, err := s.queries.UpsertVM(ctx, db.UpsertVMParams{
			ClusterID: clusterID,
			NodeID:    nodeID,
			Vmid:      int32(vm.VMID),
			Name:      vm.Name,
			Type:      "qemu",
			Status:    vm.Status,
			CpuCount:  int32(vm.CPUs),
			MemTotal:  vm.MaxMem,
			DiskTotal: vm.MaxDisk,
			Uptime:    vm.Uptime,
			Template:  vm.Template == 1,
			Tags:      vm.Tags,
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

func (s *Syncer) syncContainers(ctx context.Context, client ProxmoxClient, clusterID, nodeID uuid.UUID, nodeName string) ([]vmMetricSnapshot, error) {
	cts, err := client.GetContainers(ctx, nodeName)
	if err != nil {
		return nil, fmt.Errorf("get containers on %s: %w", nodeName, err)
	}

	var snapshots []vmMetricSnapshot
	for _, ct := range cts {
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
			continue
		}
		if result != nil {
			results = append(results, result)
		}
	}

	return results
}
