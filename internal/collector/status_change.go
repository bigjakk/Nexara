package collector

import (
	"context"
	"time"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// statusChangeSettle is how long a guest's status change waits before it is
// classified (ClassifyGuestStatusChanges, queries/guest_history.sql). A stop
// made in the Proxmox GUI reaches the inventory within seconds, but its task
// reaches task_history only on a slow pass's ingest — every 30s by default,
// later when a node's task list fails to load and the pass holds its
// watermark back. Classified sooner, the change would be filed as one no task
// explains, for good. Until then it is not shown at all.
const statusChangeSettle = 5 * time.Minute

// statusChangeGiveUp is how long a change waits for its node's task list
// before it is decided without it (see ClassifyGuestStatusChanges): a node
// the cluster calls online whose list never loads would otherwise hide its
// guests' changes for good.
const statusChangeGiveUp = 24 * time.Hour

// classifyStatusChanges decides, for the cluster's settled status changes on
// the nodes this pass listed the tasks of — those to and from "unknown" on its
// offline members, and any change that has waited statusChangeGiveUp —
// whether a task explains each (see syncTasks). The changes themselves are
// recorded by the trg_vms_status_change trigger (migrations/000115), whatever
// wrote the row.
//
// A failure is only logged: the changes stay unclassified, and hidden, until a
// later pass gets through.
func (s *Syncer) classifyStatusChanges(ctx context.Context, cluster db.Cluster, listedNodes, offlineNodes []string) {
	if _, err := s.queries.ClassifyGuestStatusChanges(ctx, db.ClassifyGuestStatusChangesParams{
		ClusterID:     cluster.ID,
		SettledBefore: time.Now().Add(-statusChangeSettle),
		ListedNodes:   listedNodes,
		OfflineNodes:  offlineNodes,
		GiveUpBefore:  time.Now().Add(-statusChangeGiveUp),
	}); err != nil {
		s.logger.Warn("failed to classify guest status changes",
			"cluster_id", cluster.ID,
			"error", err,
		)
	}
}
