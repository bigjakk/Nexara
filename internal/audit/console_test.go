package audit

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// oneCluster knows one cluster id.
type oneCluster uuid.UUID

func (c oneCluster) GetCluster(_ context.Context, id uuid.UUID) (db.Cluster, error) {
	if id != uuid.UUID(c) {
		return db.Cluster{}, pgx.ErrNoRows
	}
	return db.Cluster{ID: id}, nil
}

// Any signed-in user can ask for a refusal at will: one row a minute per user,
// cluster and reason, and an id that names no cluster files none (so a stream
// of made-up ids is one key, not one row each).
func TestConsoleOpenRefusalIsOncePerMinute(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	known, user := uuid.New(), uuid.New()
	for _, tt := range []struct {
		name, cluster, reason string
		write, filed          bool
	}{
		{"first refusal on a cluster", known.String(), ConsoleRefusedPermission, true, true},
		{"the same again", known.String(), ConsoleRefusedPermission, false, true},
		{"another reason", known.String(), ConsoleRefusedScope, true, true},
		{"an id that names no cluster", uuid.NewString(), ConsoleRefusedPermission, true, false},
		{"another such id", uuid.NewString(), ConsoleRefusedPermission, false, false},
	} {
		cluster, _, write := ConsoleOpenRefusal(context.Background(), oneCluster(known), rdb, user, tt.cluster, tt.reason, "vm_serial")
		if write != tt.write || cluster.Valid != tt.filed {
			t.Errorf("%s: write %v, cluster set %v; want %v, %v", tt.name, write, cluster.Valid, tt.write, tt.filed)
		}
	}
	mr.FastForward(consoleRefusalEvery)
	if _, _, write := ConsoleOpenRefusal(context.Background(), oneCluster(known), rdb, user, known.String(),
		ConsoleRefusedPermission, "vm_serial"); !write {
		t.Error("a minute later the refusal is not written")
	}
}
