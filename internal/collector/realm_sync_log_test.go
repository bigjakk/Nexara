package collector

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// realmSyncFakeClient is backupLogFakeClient's task logs, with each realm's
// sync_attributes.
type realmSyncFakeClient struct {
	*backupLogFakeClient
	syncAttributes map[string]string
}

func (f *realmSyncFakeClient) GetRealmSyncAttributes(_ context.Context, realm string) (string, error) {
	attrs, ok := f.syncAttributes[realm]
	if !ok {
		return "", errors.New("forbidden")
	}
	return attrs, nil
}

// A realm sync's log is read for what it removed and purged: a finished sync's
// once, a running one's each pass, and one that cannot be read names nothing.
// Its realm maps enable, expire and keys only as its sync_attributes say, and
// a realm that cannot be read maps none.
func TestRealmSyncLogs(t *testing.T) {
	t.Parallel()
	const (
		done    = "UPID:pve-01:00000001:00000001:00000001:auth-realm-sync:ldap01:root@pam:"
		running = "UPID:pve-01:00000002:00000001:00000002:auth-realm-sync:ldap01:root@pam:"
		refused = "UPID:pve-01:00000003:00000001:00000003:auth-realm-sync:ldap01:root@pam:"
		other   = "UPID:pve-01:00000004:00000001:00000004:auth-realm-sync:ldap02:root@pam:"
	)
	mq := &mockQueries{realmSyncTasks: []db.ListRealmSyncTasksRow{
		{Upid: done, Node: "pve-01", Status: "completed"}, {Upid: running, Node: "pve-01", Status: "running"},
		{Upid: refused, Node: "pve-01", Status: "completed"}, {Upid: other, Node: "pve-01", Status: "completed"},
	}}
	client := &realmSyncFakeClient{backupLogFakeClient: &backupLogFakeClient{logs: map[string][]string{
		done: {"starting sync for realm ldap01", "remove user 'user01@ldap01'", "purge users 'user01@ldap01' ACL entries",
			"purge users 'o'brien@ldap01' ACL entries", "remove group 'staff-ldap01'", "updating user 'user02@ldap01'"},
		running: {"purge groups 'ops-ldap01' ACL entries"},
		other:   {},
	}, errs: map[string]error{refused: errors.New("refused")}},
		syncAttributes: map[string]string{"ldap01": "email=mail,enable=accountActive; keys=sshPublicKey firstname=givenName"}}
	s := &Syncer{queries: mq, logger: testLogger()}
	cluster := db.Cluster{ID: uuid.New()}
	want := map[string]realmSyncLog{
		done: {Removed: map[string][]string{"users": {"user01@ldap01"}, "groups": {"staff-ldap01"}},
			Purged: map[string][]string{"users": {"user01@ldap01", "o'brien@ldap01"}}},
		running: {Removed: map[string][]string{}, Purged: map[string][]string{"groups": {"ops-ldap01"}}},
		other:   {Removed: map[string][]string{}, Purged: map[string][]string{}},
	}
	for pass := range 2 {
		logs, fields := s.readRealmSyncs(context.Background(), client, cluster)
		var got map[string]realmSyncLog
		if err := json.Unmarshal(logs, &got); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("pass %d: %+v (%v), want %+v", pass, got, err, want)
		}
		if string(fields) != `{"ldap01":["enable","keys"],"ldap02":[]}` {
			t.Fatalf("pass %d: realms map %s, want ldap01's enable and keys, and none of the unread ldap02", pass, fields)
		}
		// The finished sync's log is kept; the running one's is read again.
		client.logs[done], client.logs[running] = nil, []string{"remove group 'ops-ldap01'"}
		want[running] = realmSyncLog{Removed: map[string][]string{"groups": {"ops-ldap01"}}, Purged: map[string][]string{}}
	}
}
