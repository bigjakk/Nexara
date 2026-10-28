package collector

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// A realm sync (pve-access-control API2/Domains.pm sync, worker
// auth-realm-sync) logs each user and group it removes, and each whose ACL
// entries it purges, before it writes user.cfg. The lines, transcribed from
// its update_users and update_groups:
//
//	remove user '<userid>'
//	purge users '<userid>' ACL entries
//	remove group '<groupid>'
//	purge groups '<groupid>' ACL entries
//
// What a sync removed is what its log names, so a user deleted in the GUI
// while it ran is not taken for one of its own, and a purge with
// remove-vanished acl but not entry — the grantee kept, its entries gone — is
// the sync's exactly. ClassifyObservedChanges takes the logs as task_logs; a
// log that could not be read names nothing, and the removals stay shown.
var realmSyncLogLine = regexp.MustCompile(`^(?:remove (user|group) '(.+)'|purge (user|group)s '(.+)' ACL entries)$`)

// realmSyncLog is what one sync's log names, by list (users, groups): task_logs'
// entry for its UPID.
type realmSyncLog struct {
	Removed map[string][]string `json:"removed"`
	Purged  map[string][]string `json:"purged"`
}

const (
	// realmSyncLogsPerPass bounds how many logs one pass reads for a cluster:
	// a finished sync's is read once, so only a backlog waits.
	realmSyncLogsPerPass = 4
	// realmSyncLogMaxPages bounds one log's read: 50,000 lines, a line for
	// each user or group a sync touches. A longer one is read as far as that.
	realmSyncLogMaxPages = 10
	realmSyncLogBudget   = 15 * time.Second
)

// readRealmSyncLogs is the logs of the cluster's realm syncs a change waiting to
// be classified may lie in the window of — the give-up's day and the look-back
// before it — as task_logs, by UPID. A finished sync's log is read once
// (s.realmSyncLogs); a running one's each pass, as it logs a removal before it
// writes it. Nothing read is "{}".
func (s *Syncer) readRealmSyncLogs(ctx context.Context, client ProxmoxClient, cluster db.Cluster) json.RawMessage {
	since := time.Now().Add(-statusChangeGiveUp - observedLookback(s.objectObserveInterval) - time.Hour)
	tasks, err := s.queries.ListRealmSyncTasks(ctx, db.ListRealmSyncTasksParams{ClusterID: cluster.ID, Since: since})
	if err != nil {
		s.logger.Debug("list realm syncs", "cluster_id", cluster.ID, "error", err)
		return json.RawMessage(`{}`)
	}
	ctx, cancel := context.WithTimeout(ctx, realmSyncLogBudget)
	defer cancel()
	prefix := cluster.ID.String() + "/"
	logs, listed, reads := map[string]realmSyncLog{}, map[string]bool{}, 0
	for _, t := range tasks {
		listed[prefix+t.Upid] = true
		if v, ok := s.realmSyncLogs.Load(prefix + t.Upid); ok {
			logs[t.Upid] = v.(realmSyncLog)
			continue
		}
		if reads == realmSyncLogsPerPass || ctx.Err() != nil {
			continue
		}
		reads++
		if log, ok := readRealmSyncLog(ctx, client, t.Node, t.Upid); ok {
			logs[t.Upid] = log
			if t.Status != "running" {
				s.realmSyncLogs.Store(prefix+t.Upid, log)
			}
		}
	}
	s.realmSyncLogs.Range(func(key, _ any) bool {
		if k := key.(string); strings.HasPrefix(k, prefix) && !listed[k] {
			s.realmSyncLogs.Delete(k)
		}
		return true
	})
	b, err := json.Marshal(logs)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// readRealmSyncLog reads one sync's log for what it removed and purged.
func readRealmSyncLog(ctx context.Context, client ProxmoxClient, node, upid string) (realmSyncLog, bool) {
	log := realmSyncLog{Removed: map[string][]string{}, Purged: map[string][]string{}}
	for page, start := 0, 0; page < realmSyncLogMaxPages; page++ {
		entries, err := client.GetTaskLog(ctx, node, upid, start)
		if err != nil {
			return realmSyncLog{}, false
		}
		for _, e := range entries {
			switch m := realmSyncLogLine.FindStringSubmatch(e.T); {
			case m == nil:
			case m[1] != "":
				log.Removed[m[1]+"s"] = append(log.Removed[m[1]+"s"], m[2])
			default:
				log.Purged[m[3]+"s"] = append(log.Purged[m[3]+"s"], m[4])
			}
		}
		if len(entries) < proxmox.TaskLogPageLines {
			break
		}
		start += len(entries)
	}
	return log, true
}
