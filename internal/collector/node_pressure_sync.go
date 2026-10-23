package collector

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// nodePressureTimeout bounds one cluster's pressure pass: one RRD read per
// online node, in turn, plus the inserts.
const nodePressureTimeout = 2 * time.Minute

// nodePressureBackfillAge is how old a node's newest stored step may be before
// a pass reads the RRD's whole day rather than its last hour. Under an hour, so
// an hour read always reaches back past the newest stored step.
const nodePressureBackfillAge = 55 * time.Minute

// SyncAllNodePressure copies each online node's pressure stall steps from its
// RRD into node_pressure. Proxmox VE 9 keeps them in the node RRD at one-minute
// steps for a day and serves them only through /nodes/{node}/rrddata — the node
// status the metrics tick reads does not carry them — so a pass reads the last
// hour, or the whole day when the node has nothing recent stored, and inserts
// the steps newer than the newest it holds. A Proxmox VE 8 node's RRD has no
// pressure series, so it yields nothing.
//
// Re-entrancy-guarded like SyncAllGuestSnapshots.
func (s *Syncer) SyncAllNodePressure(ctx context.Context) {
	if !s.nodePressureInFlight.CompareAndSwap(false, true) {
		return
	}
	defer s.nodePressureInFlight.Store(false)

	clusters, err := s.queries.ListActiveClusters(ctx)
	if err != nil {
		s.logger.Warn("node pressure sync: failed to list active clusters", "error", err)
		return
	}

	var wg sync.WaitGroup
	for _, cluster := range clusters {
		wg.Add(1)
		go func(cluster db.Cluster) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					s.logger.Error("node pressure sync panicked",
						"cluster_id", cluster.ID, "panic", r)
				}
			}()
			s.syncClusterNodePressure(ctx, cluster)
		}(cluster)
	}
	wg.Wait()
}

// syncClusterNodePressure runs one cluster's pass. Failures are per node and
// logged at Debug: the metrics tick already reports an unreachable cluster or
// node loudly, and a node that fails this pass is read again on the next.
func (s *Syncer) syncClusterNodePressure(ctx context.Context, cluster db.Cluster) {
	syncCtx, cancel := context.WithTimeout(ctx, nodePressureTimeout)
	defer cancel()

	client, err := s.proxmoxClient(syncCtx, cluster)
	if err != nil {
		s.logger.Debug("node pressure sync: build client", "cluster_id", cluster.ID, "error", err)
		return
	}
	nodes, err := s.queries.ListNodesByCluster(syncCtx, cluster.ID)
	if err != nil {
		s.logger.Debug("node pressure sync: list nodes", "cluster_id", cluster.ID, "error", err)
		return
	}
	for _, node := range nodes {
		if node.Status != "online" {
			continue
		}
		if err := s.syncNodePressure(syncCtx, client, node); err != nil {
			s.logger.Debug("node pressure sync failed",
				"cluster_id", cluster.ID, "node", node.Name, "error", err)
		}
	}
}

func (s *Syncer) syncNodePressure(ctx context.Context, client ProxmoxClient, node db.Node) error {
	var newest time.Time
	switch t, err := s.queries.GetNewestNodePressureTime(ctx, node.ID); {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return fmt.Errorf("read newest stored step: %w", err)
	default:
		newest = t
	}

	timeframe := "hour"
	if newest.IsZero() || time.Since(newest) > nodePressureBackfillAge {
		timeframe = "day"
	}
	steps, err := client.GetNodeRRDData(ctx, node.Name, timeframe, "AVERAGE")
	if err != nil {
		return fmt.Errorf("read RRD: %w", err)
	}
	for _, step := range steps {
		at := time.Unix(int64(step.Time), 0)
		if !at.After(newest) || !step.HasPressure() {
			continue
		}
		if err := s.queries.InsertNodePressure(ctx, db.InsertNodePressureParams{
			Time:    at,
			NodeID:  node.ID,
			CpuSome: float4(step.PressureCPUSome),
			IoSome:  float4(step.PressureIOSome),
			IoFull:  float4(step.PressureIOFull),
			MemSome: float4(step.PressureMemorySome),
			MemFull: float4(step.PressureMemoryFull),
		}); err != nil {
			return fmt.Errorf("store step %s: %w", at.Format(time.RFC3339), err)
		}
	}
	return nil
}

func float4(f proxmox.OptFloat) pgtype.Float4 {
	return pgtype.Float4{Float32: float32(f.Value), Valid: f.Set}
}
