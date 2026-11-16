package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/pkg/redisutil"
)

// ConsoleOpenRefused is the action of the one row a refused console open
// writes, whether the mint refused the permission (internal/api/handlers,
// ConsoleToken) or the WebSocket gate refused a token for another console
// (internal/ws). Its resource is the user ("auth", the user id), as the mint's.
const ConsoleOpenRefused = "console_open_refused"

// Why a console open was refused.
const (
	ConsoleRefusedPermission = "permission" // no console:<resource> on the cluster
	ConsoleRefusedScope      = "scope"      // the token was minted for another console
)

// consoleRefusalEvery is how often one user's refusals for one cluster and
// reason write a row: any signed-in user can ask for a refusal at will.
const consoleRefusalEvery = time.Minute

// consoleRefusalWait bounds the cluster lookup and the Redis claim.
const consoleRefusalWait = time.Second

// ClusterGetter is the lookup ConsoleOpenRefusal names the row's cluster with.
type ClusterGetter interface {
	GetCluster(ctx context.Context, id uuid.UUID) (db.Cluster, error)
}

// ConsoleOpenRefusal is the row a refused console open writes, and whether to
// write it: the first refusal of a user, cluster and reason in
// consoleRefusalEvery (claimed in Redis, as the silent mint's dedupe is; without
// Redis, or when it does not answer, every one). The cluster is set only when
// one by that id is found, so no row is filed under an id that names nothing.
// The details are the reason and the console type, a value checked against its
// vocabulary before it gets here; nothing a caller typed goes in (no node name,
// no query value): view:audit is held by every built-in Viewer.
func ConsoleOpenRefusal(ctx context.Context, clusters ClusterGetter, rdb *redis.Client, userID uuid.UUID,
	clusterID, reason, consoleType string) (db.InsertAuditLogParams, bool) {
	ctx, cancel := context.WithTimeout(ctx, consoleRefusalWait)
	defer cancel()
	var cluster pgtype.UUID
	scope := "none"
	if id, err := uuid.Parse(clusterID); err == nil {
		if _, err := clusters.GetCluster(ctx, id); err == nil {
			cluster, scope = pgtype.UUID{Bytes: id, Valid: true}, id.String()
		}
	}
	details, _ := json.Marshal(map[string]string{"reason": reason, "type": consoleType})
	row := db.InsertAuditLogParams{
		ClusterID:    cluster,
		UserID:       pgtype.UUID{Bytes: userID, Valid: userID != uuid.Nil},
		ResourceType: "auth",
		ResourceID:   userID.String(),
		Action:       ConsoleOpenRefused,
		Details:      details,
	}
	if rdb == nil {
		return row, true
	}
	key := "console:refused-audit:" + userID.String() + ":" + scope + ":" + reason
	first, err := redisutil.BoundedByContext(ctx, func(ctx context.Context) (bool, error) {
		return rdb.SetNX(ctx, key, "1", consoleRefusalEvery).Result()
	})
	return row, err != nil || first
}
