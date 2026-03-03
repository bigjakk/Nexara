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

// SyncCluster discovers nodes, VMs, containers, and storage from a Proxmox cluster
// and upserts them into the database.
func (s *Syncer) SyncCluster(ctx context.Context, cluster db.Cluster) error {
	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, s.encryptionKey)
	if err != nil {
		return fmt.Errorf("sync cluster %s: decrypt token: %w", cluster.ID, err)
	}

	client, err := s.clientFactory(cluster.ApiUrl, cluster.TokenID, tokenSecret, cluster.TlsFingerprint)
	if err != nil {
		return fmt.Errorf("sync cluster %s: create client: %w", cluster.ID, err)
	}

	nodes, err := client.GetNodes(ctx)
	if err != nil {
		return fmt.Errorf("sync cluster %s: get nodes: %w", cluster.ID, err)
	}

	for _, node := range nodes {
		if err := s.syncNode(ctx, client, cluster.ID, node); err != nil {
			s.logger.Warn("failed to sync node",
				"cluster_id", cluster.ID,
				"node", node.Node,
				"error", err,
			)
			// Continue syncing other nodes on partial failure.
		}
	}

	return nil
}

// syncNode syncs a single node and all its resources (VMs, containers, storage).
func (s *Syncer) syncNode(ctx context.Context, client ProxmoxClient, clusterID uuid.UUID, node proxmox.NodeListEntry) error {
	// Fetch detailed node status for PVE version and CPU info.
	var pveVersion string
	var cpuCount int32
	var memTotal int64
	var diskTotal int64

	status, err := client.GetNodeStatus(ctx, node.Node)
	if err != nil {
		s.logger.Warn("failed to get node status, using list data",
			"node", node.Node,
			"error", err,
		)
		cpuCount = int32(node.MaxCPU)
		memTotal = node.MaxMem
		diskTotal = node.MaxDisk
	} else {
		pveVersion = status.PVEVersion
		cpuCount = int32(status.CPUInfo.CPUs)
		memTotal = status.Memory.Total
		diskTotal = status.RootFS.Total
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
		return fmt.Errorf("upsert node %s: %w", node.Node, err)
	}

	// Sync VMs.
	if err := s.syncVMs(ctx, client, clusterID, dbNode.ID, node.Node); err != nil {
		s.logger.Warn("failed to sync VMs",
			"node", node.Node,
			"error", err,
		)
	}

	// Sync containers.
	if err := s.syncContainers(ctx, client, clusterID, dbNode.ID, node.Node); err != nil {
		s.logger.Warn("failed to sync containers",
			"node", node.Node,
			"error", err,
		)
	}

	// Sync storage pools.
	if err := s.syncStorage(ctx, client, clusterID, dbNode.ID, node.Node); err != nil {
		s.logger.Warn("failed to sync storage",
			"node", node.Node,
			"error", err,
		)
	}

	return nil
}

func (s *Syncer) syncVMs(ctx context.Context, client ProxmoxClient, clusterID, nodeID uuid.UUID, nodeName string) error {
	vms, err := client.GetVMs(ctx, nodeName)
	if err != nil {
		return fmt.Errorf("get VMs on %s: %w", nodeName, err)
	}

	for _, vm := range vms {
		_, err := s.queries.UpsertVM(ctx, db.UpsertVMParams{
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
			return fmt.Errorf("upsert VM %d on %s: %w", vm.VMID, nodeName, err)
		}
	}

	return nil
}

func (s *Syncer) syncContainers(ctx context.Context, client ProxmoxClient, clusterID, nodeID uuid.UUID, nodeName string) error {
	cts, err := client.GetContainers(ctx, nodeName)
	if err != nil {
		return fmt.Errorf("get containers on %s: %w", nodeName, err)
	}

	for _, ct := range cts {
		_, err := s.queries.UpsertVM(ctx, db.UpsertVMParams{
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
			return fmt.Errorf("upsert container %d on %s: %w", ct.VMID, nodeName, err)
		}
	}

	return nil
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

// SyncAll syncs all active clusters.
func (s *Syncer) SyncAll(ctx context.Context) error {
	clusters, err := s.queries.ListActiveClusters(ctx)
	if err != nil {
		return fmt.Errorf("list active clusters: %w", err)
	}

	for _, cluster := range clusters {
		s.logger.Info("syncing cluster", "cluster_id", cluster.ID, "name", cluster.Name)
		if err := s.SyncCluster(ctx, cluster); err != nil {
			s.logger.Error("failed to sync cluster",
				"cluster_id", cluster.ID,
				"name", cluster.Name,
				"error", err,
			)
		}
	}

	return nil
}
