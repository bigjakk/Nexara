package collector

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

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

// A sync's log names what it removed and purged, each in its own
// section of a sync that wrote it; a log with a line out of place — a
// directory's value echoed in a warning may start one — or a warning whose
// quoted value runs past its line, names nothing, as does one that never got
// to write.
func TestParseRealmSyncLog(t *testing.T) {
	t.Parallel()
	users := []string{"remove user 'user01@ldap01'", "purge users 'o'brien@ldap01' ACL entries",
		"overwriting user 'user02@ldap01'", "adding user 'user03@ldap01'"}
	groups := []string{"remove group 'staff-ldap01'", "purge groups 'ops-ldap01' ACL entries"}
	whole := realmSyncLog{Removed: map[string][]string{"users": {"user01@ldap01"}, "groups": {"staff-ldap01"}},
		Purged: map[string][]string{"users": {"o'brien@ldap01"}, "groups": {"ops-ldap01"}}}
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
	} {
		if got := parseRealmSyncLog(tc.lines); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// A finished sync's log is read once; one still running, or one that failed,
// names nothing. A realm maps enable, expire and keys only as its
// sync_attributes say, and one that cannot be read maps none.
func TestRealmSyncLogs(t *testing.T) {
	t.Parallel()
	const (
		done    = "UPID:pve-01:00000001:00000001:00000001:auth-realm-sync:ldap01:root@pam:"
		running = "UPID:pve-01:00000002:00000001:00000002:auth-realm-sync:ldap01:root@pam:"
		failed  = "UPID:pve-01:00000003:00000001:00000003:auth-realm-sync:ldap01:root@pam:"
		other   = "UPID:pve-01:00000004:00000001:00000004:auth-realm-sync:ldap02:root@pam:"
	)
	mq := &mockQueries{realmSyncTasks: []db.ListRealmSyncTasksRow{
		{Upid: done, Node: "pve-01", Status: "completed"}, {Upid: running, Node: "pve-01", Status: "running"},
		{Upid: failed, Node: "pve-01", Status: "failed"}, {Upid: other, Node: "pve-01", Status: "completed"},
	}}
	synced := realmSyncLines([]string{"remove user 'user01@ldap01'"}, nil)
	client := &realmSyncFakeClient{backupLogFakeClient: &backupLogFakeClient{logs: map[string][]string{
		done: synced, running: synced, failed: synced, other: {},
	}}, syncAttributes: map[string]string{"ldap01": "email=mail,enable=accountActive; keys=sshPublicKey firstname=givenName"}}
	s := &Syncer{queries: mq, logger: testLogger()}
	cluster := db.Cluster{ID: uuid.New()}
	want := map[string]realmSyncLog{done: parseRealmSyncLog(synced), other: newRealmSyncLog()}
	for pass := range 2 {
		logs, fields := s.readRealmSyncs(context.Background(), client, cluster)
		var got map[string]realmSyncLog
		if err := json.Unmarshal(logs, &got); err != nil || !reflect.DeepEqual(got, want) || len(got[done].Removed["users"]) != 1 {
			t.Fatalf("pass %d: %+v (%v), want %+v", pass, got, err, want)
		}
		if string(fields) != `{"ldap01":["enable","keys"],"ldap02":[]}` || client.logReads != 2 {
			t.Fatalf("pass %d: realms map %s, %d log reads, want ldap01's enable and keys, none of the unread ldap02, "+
				"and each finished log read once", pass, fields, client.logReads)
		}
		// The finished sync's log is kept.
		client.logs[done] = nil
	}
}
