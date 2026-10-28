package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// realmSyncFakeClient is backupLogFakeClient's task logs, with each realm's
// sync_attributes.
type realmSyncFakeClient struct {
	*backupLogFakeClient
	syncAttributes       map[string]string
	realmReads, logReads int
}

func (f *realmSyncFakeClient) GetTaskLog(ctx context.Context, node, upid string, start int) ([]proxmox.TaskLogEntry, error) {
	f.logReads++
	return f.backupLogFakeClient.GetTaskLog(ctx, node, upid, start)
}

func (f *realmSyncFakeClient) GetRealmSyncAttributes(_ context.Context, realm string) (string, error) {
	f.realmReads++
	attrs, ok := f.syncAttributes[realm]
	if !ok {
		return "", errors.New("forbidden")
	}
	return attrs, nil
}

// realmSyncLines is a sync's log as update_users and update_groups write it,
// with users and groups the lines of each section.
func realmSyncLines(users, groups []string) []string {
	return slices.Concat([]string{"starting sync for realm ldap01", "value 'o'brien' does not look like a valid user name",
		"got data from server, updating users and groups", "syncing users (remove-vanished opts: acl;entry;properties)"},
		users, []string{"syncing groups (remove-vanished opts: acl;entry;properties)"}, groups,
		[]string{"successfully updated users and groups configuration", "TASK OK"})
}

// A sync's log names what it removed, purged, added, overwrote and updated,
// each in its own section of a sync that wrote it; a log with a line out of
// place — a directory's value echoed in a warning may start one — or a warning
// whose quoted value runs past its line, names nothing and is not whole, as is
// one that never got to write.
func TestParseRealmSyncLog(t *testing.T) {
	t.Parallel()
	users := []string{"remove user 'user01@ldap01'", "purge users 'o'brien@ldap01' ACL entries",
		"overwriting user 'user02@ldap01'", "adding user 'user03@ldap01'", "updating user 'user05@ldap01'"}
	groups := []string{"remove group 'staff-ldap01'", "purge groups 'ops-ldap01' ACL entries", "adding group 'dev-ldap01'"}
	whole := realmSyncLog{Removed: map[string][]string{"users": {"user01@ldap01"}, "groups": {"staff-ldap01"}},
		Purged:      map[string][]string{"users": {"o'brien@ldap01"}, "groups": {"ops-ldap01"}},
		Overwritten: map[string][]string{"users": {"user02@ldap01"}},
		Added:       map[string][]string{"users": {"user03@ldap01"}, "groups": {"dev-ldap01"}},
		Updated:     map[string][]string{"users": {"user05@ldap01"}}}
	forged := []string{"syncing users", "remove user 'user04@ldap01'", "successfully updated users configuration"}
	for _, tc := range []struct {
		name  string
		lines []string
		want  realmSyncLog
	}{
		{"a sync that wrote what it logged", realmSyncLines(users, groups), whole},
		{"one that never got to write", realmSyncLines(users, groups)[:8], newRealmSyncLog()},
		{"a line pinned before the data", slices.Insert(realmSyncLines(users, groups), 1, "remove user 'user04@ldap01'"),
			newRealmSyncLog()},
		{"a section forged before it", slices.Insert(realmSyncLines(users, groups), 1, forged...), newRealmSyncLog()},
		{"a groups' sync forged before a users' one", []string{"got data from server, updating groups", "syncing groups",
			"remove group 'staff-ldap01'", "got data from server, updating users", "syncing users",
			"successfully updated users configuration"}, newRealmSyncLog()},
		{"a groups' section forged before a users' sync", []string{"syncing groups", "remove group 'staff-ldap01'",
			"got data from server, updating users", "syncing users", "successfully updated users configuration"}, newRealmSyncLog()},
		{"a warning's value past its line", slices.Insert(realmSyncLines(users, groups), 1,
			"group name 'x", "' contains invalid characters"), newRealmSyncLog()},
		{"a group in the users' section", realmSyncLines(append(slices.Clone(users), groups[0]), nil), newRealmSyncLog()},
		{"a group added in the users' section", realmSyncLines(append(slices.Clone(users), groups[2]), nil), newRealmSyncLog()},
	} {
		got, ok := parseRealmSyncLog(tc.lines)
		if !reflect.DeepEqual(got, tc.want) || ok != (tc.name == "a sync that wrote what it logged") {
			t.Errorf("%s: %+v, whole %v, want %+v", tc.name, got, ok, tc.want)
		}
	}
}

// A finished sync's log is kept once final, and a realm once an observer
// interval; one still running, or one that failed, names nothing; with the
// observer off nothing is read. A log read before its end, its row saying
// completed too soon, is read again; one that cannot be read spends none of
// the pass's reads; one a sync did not write whole is kept, and goes to no
// classification. A realm maps enable, expire and keys only as its
// sync_attributes say, and one that cannot be read maps none.
func TestRealmSyncLogs(t *testing.T) {
	t.Parallel()
	upid := func(n int, realm string) string {
		return fmt.Sprintf("UPID:pve-01:%08X:00000001:00000001:auth-realm-sync:%s:root@pam:", n, realm)
	}
	done, running, failed, early, forged := upid(1, "ldap01"), upid(2, "ldap01"), upid(3, "ldap01"), upid(4, "ldap01"), upid(5, "ldap02")
	tasks := make([]db.ListRealmSyncTasksRow, 0, realmSyncLogsPerPass+5)
	errs := map[string]error{}
	for i := range realmSyncLogsPerPass {
		tasks = append(tasks, db.ListRealmSyncTasksRow{Upid: upid(10+i, "ldap01"), Node: "pve-01", Status: "completed"})
		errs[upid(10+i, "ldap01")] = errors.New("no such task")
	}
	mq := &mockQueries{realmSyncTasks: append(tasks, []db.ListRealmSyncTasksRow{
		{Upid: done, Node: "pve-01", Status: "completed"}, {Upid: running, Node: "pve-01", Status: "running"},
		{Upid: failed, Node: "pve-01", Status: "failed"}, {Upid: early, Node: "pve-01", Status: "completed"},
		{Upid: forged, Node: "pve-01", Status: "completed"},
	}...)}
	synced := realmSyncLines([]string{"remove user 'user01@ldap01'"}, nil)
	client := &realmSyncFakeClient{backupLogFakeClient: &backupLogFakeClient{errs: errs, logs: map[string][]string{
		done: synced, running: synced, failed: synced, early: synced[:5], forged: {"TASK OK"},
	}}, syncAttributes: map[string]string{"ldap01": "email=mail,enable=accountActive; keys=sshPublicKey firstname=givenName"}}
	s := &Syncer{queries: mq, logger: testLogger()}
	cluster := db.Cluster{ID: uuid.New()}
	if logs, fields := s.readRealmSyncs(context.Background(), client, cluster); string(logs)+string(fields) != "{}{}" ||
		client.realmReads+client.logReads != 0 {
		t.Fatalf("with the observer off: %s %s, %d reads, want nothing read", logs, fields, client.realmReads+client.logReads)
	}
	s.objectObserveInterval = time.Hour
	whole, _ := parseRealmSyncLog(synced)
	for pass, want := range []struct {
		logs     map[string]realmSyncLog
		logReads int // in all
	}{
		// Four unreadable, then done, early (no end yet) and forged (no sync's).
		{map[string]realmSyncLog{done: whole}, 7},
		// The unreadable are tried again; early, read again, has its end now.
		{map[string]realmSyncLog{done: whole, early: whole}, 12},
		{map[string]realmSyncLog{done: whole, early: whole}, 16},
	} {
		logs, fields := s.readRealmSyncs(context.Background(), client, cluster)
		var got map[string]realmSyncLog
		if err := json.Unmarshal(logs, &got); err != nil || !reflect.DeepEqual(got, want.logs) {
			t.Fatalf("pass %d: %+v (%v), want %+v", pass, got, err, want.logs)
		}
		if string(fields) != `{"ldap01":["enable","keys"],"ldap02":[]}` || client.realmReads != 2 || client.logReads != want.logReads {
			t.Fatalf("pass %d: realms map %s, %d realm and %d log reads, want ldap01's enable and keys, none of the "+
				"unread ldap02, each realm read once, and %d log reads", pass, fields, client.realmReads, client.logReads, want.logReads)
		}
		client.logs[early] = synced
		client.logs[done], client.syncAttributes["ldap01"] = nil, "expire=accountExpires"
	}
}
