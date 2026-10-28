package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// A cached permission set that cannot be cleared stays until its entry expires, so a
// failed DEL is logged with the user it was for; one that succeeds says nothing about
// the user. Sequential: captureLockedLog swaps the process-wide logger.
func TestInvalidateUser_LogsAFailedDelete(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	engine := NewRBACEngine(nil, rdb)
	user := uuid.New()
	key := rbacCachePrefix + user.String()
	logs := captureLockedLog(t)

	mr.Set(key, `{"permissions":[]}`)
	engine.InvalidateUser(context.Background(), user)
	if mr.Exists(key) || strings.Contains(logs.String(), user.String()) {
		t.Errorf("a delete that works left the key (%v) or logged the user: %s", mr.Exists(key), logs.String())
	}

	mr.Set(key, `{"permissions":[]}`)
	mr.SetError("ERR cache unavailable")
	engine.InvalidateUser(context.Background(), user)
	mr.SetError("")
	if got := logs.String(); !strings.Contains(got, "level=WARN") || !strings.Contains(got, user.String()) {
		t.Errorf("a failed delete logged %q, want a warning that names the user %s", got, user)
	}
	if !mr.Exists(key) {
		t.Error("the failed delete somehow removed the key: the fixture is not exercising a failure")
	}
}
