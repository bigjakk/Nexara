package collector

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// A realm sync (pve-access-control API2/Domains.pm sync, worker
// auth-realm-sync), once it has read the directory and locked user.cfg, logs
// "got data from server, updating <what>", then a section for the users and
// one for the groups it syncs, each opened by "syncing users" or "syncing
// groups" (with the remove-vanished options it was given), and, once it has
// written user.cfg, "successfully updated <what> configuration". In its
// sections it logs each user and group it removes, each whose ACL entries it
// purges, and each user it replaces whole (remove-vanished properties), as
// update_users and update_groups write them:
//
//	remove user '<userid>'
//	purge users '<userid>' ACL entries
//	overwriting user '<userid>'
//	remove group '<groupid>'
//	purge groups '<groupid>' ACL entries
//
// What a sync did is what its log names, so a user deleted in the GUI while
// it ran is not taken for one of its own. Before its sections it echoes the
// directory's values in the warnings of what it skips (Auth/LDAP.pm get_users
// and get_groups, through Auth/Plugin.pm verify_username, AccessControl.pm
// verify_groupname and JSONSchema validate), and a value may hold a newline,
// which ends its warning's line early and starts one of the directory's
// choosing. So a line counts only in its own section, of a sync that finished
// and wrote what it logged; a log with any of these lines out of place, or a
// warning whose quoted value does not close on its line, names nothing, as
// does one that could not be read. ClassifyObservedChanges takes the logs as
// task_logs, and what no log names stays shown.
var (
	realmSyncData    = regexp.MustCompile(`^got data from server, updating (users|groups|users and groups)$`)
	realmSyncSection = regexp.MustCompile(`^syncing (users|groups)(?: \(remove-vanished opts: .*\))?$`)
	realmSyncDone    = regexp.MustCompile(`^successfully updated (users|groups|users and groups) configuration$`)
	realmSyncLogLine = regexp.MustCompile(`^(?:(remove) (user|group) '(.+)'|(purge) (user|group)s '(.+)' ACL entries|(overwriting) (user) '(.+)')$`)
	// realmSyncWarnings are the warnings that quote a directory's value, by
	// how each begins and the whole of it.
	realmSyncWarnings = []struct {
		start string
		whole *regexp.Regexp
	}{
		{"user name '", regexp.MustCompile(`^user name '.*' is too (?:short|long \(\d+ > 64\))$`)},
		{"value '", regexp.MustCompile(`^value '.*' does not look like a valid user name$`)},
		{"group name '", regexp.MustCompile(`^group name '.*' contains invalid characters$`)},
		{"skipping attribute mapping '", regexp.MustCompile(`^skipping attribute mapping '.*'->'.*' for user '.*' - ` +
			`(?:value '.*' does not look like a valid user name|400 invalid value '.*'|` +
			`internal error: no schema for attribute '.*' with value '.*' available!)$`)},
		{"skipping bad 'sync_attributes' entry – '", regexp.MustCompile(`^skipping bad 'sync_attributes' entry – '.*' is not a valid target attribute$`)},
	}
)

// realmSyncLog is what one sync's log names, by list (users, groups): task_logs'
// entry for its UPID.
type realmSyncLog struct {
	Removed     map[string][]string `json:"removed"`
	Purged      map[string][]string `json:"purged"`
	Overwritten map[string][]string `json:"overwritten"`
}

func newRealmSyncLog() realmSyncLog {
	return realmSyncLog{Removed: map[string][]string{}, Purged: map[string][]string{}, Overwritten: map[string][]string{}}
}

const (
	// realmSyncLogsPerPass bounds how many logs one pass reads for a cluster:
	// a finished sync's is read once, so only a backlog waits.
	realmSyncLogsPerPass = 4
	// realmSyncLogMaxPages bounds one log's read: 50,000 lines, a line for
	// each user or group a sync touches. A longer one is read as far as that,
	// and, without its end, names nothing.
	realmSyncLogMaxPages = 10
	realmSyncLogBudget   = 15 * time.Second
)

// realmSyncFields are the user fields a realm sync writes on a user it has only
// where the realm's sync_attributes maps them (realmSyncRules).
var realmSyncFields = []string{"enable", "expire", "keys"}

// readRealmSyncs reads, for the cluster's realm syncs a change waiting to be
// classified may lie in the window of — the give-up's day and the look-back
// before it — what ClassifyObservedChanges takes of them: taskLogs, each
// finished sync's log by UPID, newest first, read once (s.realmSyncLogs), and
// realmFields, each synced realm's mapped realmSyncFields, read each pass (one
// that cannot be read maps none). A sync still running names nothing yet: its
// log ends only once it wrote user.cfg. Nothing read is "{}".
func (s *Syncer) readRealmSyncs(ctx context.Context, client ProxmoxClient, cluster db.Cluster) (taskLogs, realmFields json.RawMessage) {
	none := json.RawMessage(`{}`)
	since := time.Now().Add(-statusChangeGiveUp - observedLookback(s.objectObserveInterval) - time.Hour)
	tasks, err := s.queries.ListRealmSyncTasks(ctx, db.ListRealmSyncTasksParams{ClusterID: cluster.ID, Since: since})
	if err != nil {
		s.logger.Debug("list realm syncs", "cluster_id", cluster.ID, "error", err)
		return none, none
	}
	ctx, cancel := context.WithTimeout(ctx, realmSyncLogBudget)
	defer cancel()
	prefix := cluster.ID.String() + "/"
	listed := map[string]bool{}
	fields := map[string][]string{}
	for _, t := range tasks {
		_, realm, ok := upidTypeAndID(t.Upid)
		if _, read := fields[realm]; read || !ok || realm == "" {
			continue
		}
		fields[realm] = []string{}
		if attrs, err := client.GetRealmSyncAttributes(ctx, realm); err != nil {
			s.logger.Debug("read a synced realm's mapping", "cluster_id", cluster.ID, "realm", realm, "error", err)
		} else {
			fields[realm] = realmMappedFields(attrs)
		}
	}
	logs, reads := map[string]realmSyncLog{}, 0
	for _, t := range tasks {
		if t.Status != "completed" {
			continue // running, or it failed and wrote nothing
		}
		listed[prefix+t.Upid] = true
		if v, ok := s.realmSyncLogs.Load(prefix + t.Upid); ok {
			logs[t.Upid] = v.(realmSyncLog)
			continue
		}
		if reads == realmSyncLogsPerPass || ctx.Err() != nil {
			continue
		}
		reads++
		if lines, ok := readTaskLogLines(ctx, client, t.Node, t.Upid, realmSyncLogMaxPages); ok {
			logs[t.Upid] = parseRealmSyncLog(lines)
			s.realmSyncLogs.Store(prefix+t.Upid, logs[t.Upid])
		}
	}
	s.realmSyncLogs.Range(func(key, _ any) bool {
		if k := key.(string); strings.HasPrefix(k, prefix) && !listed[k] {
			s.realmSyncLogs.Delete(k)
		}
		return true
	})
	taskLogs, err = json.Marshal(logs)
	if err != nil {
		taskLogs = none
	}
	realmFields, err = json.Marshal(fields)
	if err != nil {
		realmFields = none
	}
	return taskLogs, realmFields
}

// realmMappedFields is which of realmSyncFields a realm's sync_attributes maps,
// read as LDAP.pm get_users reads it: split_list, each "<ours>=<ldap>".
func realmMappedFields(syncAttributes string) []string {
	mapped := []string{}
	for _, pair := range strings.FieldsFunc(syncAttributes, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) }) {
		if ours, _, ok := strings.Cut(pair, "="); ok && slices.Contains(realmSyncFields, ours) && !slices.Contains(mapped, ours) {
			mapped = append(mapped, ours)
		}
	}
	return mapped
}

// readTaskLogLines reads up to pages of a task's log, its lines in order.
func readTaskLogLines(ctx context.Context, client ProxmoxClient, node, upid string, pages int) ([]string, bool) {
	var lines []string
	for page, start := 0, 0; page < pages; page++ {
		entries, err := client.GetTaskLog(ctx, node, upid, start)
		if err != nil {
			return nil, false
		}
		for _, e := range entries {
			lines = append(lines, e.T)
		}
		if len(entries) < proxmox.TaskLogPageLines {
			break
		}
		start += len(entries)
	}
	return lines, true
}

// parseRealmSyncLog is what a finished sync's log names, or nothing — an empty
// log — when it is not one a sync wrote whole as realmSyncLogLine's comment
// says: each line it pins by in its own section after the data line, once, and
// its end.
func parseRealmSyncLog(lines []string) realmSyncLog {
	log := newRealmSyncLog()
	var what, section string
	sections, done := map[string]bool{}, false
	for _, line := range lines {
		for _, w := range realmSyncWarnings {
			if strings.HasPrefix(line, w.start) && !w.whole.MatchString(line) {
				return newRealmSyncLog()
			}
		}
		if m := realmSyncData.FindStringSubmatch(line); m != nil {
			if what != "" {
				return newRealmSyncLog()
			}
			what = m[1]
			continue
		}
		if m := realmSyncSection.FindStringSubmatch(line); m != nil {
			if what == "" || done || sections[m[1]] {
				return newRealmSyncLog()
			}
			section, sections[m[1]] = m[1], true
			continue
		}
		if m := realmSyncDone.FindStringSubmatch(line); m != nil {
			if done || m[1] != what {
				return newRealmSyncLog()
			}
			done = true
			continue
		}
		m := realmSyncLogLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		into, list, id := log.Removed, m[2]+"s", m[3]
		switch {
		case m[4] != "":
			into, list, id = log.Purged, m[5]+"s", m[6]
		case m[7] != "":
			into, list, id = log.Overwritten, m[8]+"s", m[9]
		}
		if done || list != section {
			return newRealmSyncLog()
		}
		into[list] = append(into[list], id)
	}
	if !done {
		return newRealmSyncLog()
	}
	return log
}
