package app

import (
	"context"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bigjakk/nexara/internal/config"
)

func testConfig() *config.Config {
	return &config.Config{
		APIPort:         8080,
		LogLevel:        "info",
		JWTSecret:       "test-secret-at-least-16-chars",
		EncryptionKey:   "0000000000000000000000000000000000000000000000000000000000000000",
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 7 * 24 * time.Hour,
	}
}

// TestNew_NilPoolLeavesDBServicesNil pins the degraded shape the API server is
// constructed with in tests and on a DB-less boot: everything database-backed
// stays nil rather than panicking, and the JWT service (config-only) is still
// built.
func TestNew_NilPoolLeavesDBServicesNil(t *testing.T) {
	a := New(context.Background(), testConfig(), nil, nil, slog.Default())

	if a.Queries != nil {
		t.Error("Queries should be nil without a pool")
	}
	if a.HasDB() {
		t.Error("HasDB should be false without a pool")
	}
	if a.JWT == nil {
		t.Error("JWT should be built from config alone")
	}

	// Every DB-gated engine must be nil, and every consumer nil-checks them.
	if a.NotifyRegistry != nil || a.AlertEngine != nil || a.CVEScanner != nil ||
		a.DRSEngine != nil || a.DRSExecutor != nil || a.RollingOrch != nil || a.ReportGen != nil {
		t.Error("domain engines should all be nil without a pool")
	}
	if a.RBAC != nil || a.SessionMgr != nil || a.ProxmoxCache != nil {
		t.Error("RBAC, SessionMgr and ProxmoxCache should be nil without a pool")
	}

	// Close must tolerate a partially-constructed App.
	a.Close()
}

// testPool returns a pool that is never dialed. pgxpool.New only parses the
// DSN and returns lazily, which is all app.New needs — it calls db.New(pool)
// and runs no queries — so the gating rules below execute for real without a
// database. The DSN points at a closed port so an accidental query fails
// loudly rather than reaching anything.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	pool, err := pgxpool.New(context.Background(),
		"postgres://nobody:nobody@127.0.0.1:1/nexara_unreachable_test?sslmode=disable")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestNew_NilRedisStillBuildsRBAC guards the trap called out in New: the RBAC
// engine is deliberately built without Redis, degrading to a Postgres lookup.
// Adding `&& rdb != nil` to that gate would 500 every authenticated request on
// a Redis-less install, and this test is what fails if someone does.
func TestNew_NilRedisStillBuildsRBAC(t *testing.T) {
	a := New(context.Background(), testConfig(), testPool(t), nil, slog.Default())
	defer a.Close()

	if a.RBAC == nil {
		t.Error("RBAC must be built without Redis — gating it on Redis fails every authenticated request")
	}
	if a.EventPub != nil {
		t.Error("EventPub should be nil without Redis")
	}
	if a.SessionMgr != nil {
		t.Error("SessionMgr requires Redis and should be nil")
	}
}

// TestNew_NoEncryptionKeyStillBuildsEngines is the panic guard. The scheduler
// dereferences AlertEngine with no nil check, so the engines must be gated on
// the database alone: a keyless install has to degrade per-operation (decrypt
// fails at use) rather than nil-panic the scheduler on its first tick.
func TestNew_NoEncryptionKeyStillBuildsEngines(t *testing.T) {
	cfg := testConfig()
	cfg.EncryptionKey = ""

	a := New(context.Background(), cfg, testPool(t), nil, slog.Default())
	defer a.Close()

	if a.AlertEngine == nil || a.DRSEngine == nil || a.DRSExecutor == nil ||
		a.CVEScanner == nil || a.RollingOrch == nil || a.ReportGen == nil || a.NotifyRegistry == nil {
		t.Error("domain engines must be gated on the database alone; " +
			"gating them on the encryption key nil-panics the scheduler on a keyless install")
	}
	// The client cache is the one service that genuinely needs the key — it
	// decrypts cluster credentials to build clients.
	if a.ProxmoxCache != nil {
		t.Error("ProxmoxCache requires an encryption key and should be nil")
	}
	if a.HasCrypto() {
		t.Error("HasCrypto should be false with an empty key")
	}
}

// TestNew_FullyWiredBuildsEverything exercises the production path — pool,
// encryption key, no Redis — and asserts every service a consumer takes from
// the App is present. Consumers dereference several of these without a nil
// check at tick time, so a gate that silently drops one here surfaces as a
// scheduler panic in production rather than a test failure.
func TestNew_FullyWiredBuildsEverything(t *testing.T) {
	a := New(context.Background(), testConfig(), testPool(t), nil, slog.Default())
	defer a.Close()

	services := map[string]any{
		"Queries":        a.Queries,
		"JWT":            a.JWT,
		"RBAC":           a.RBAC,
		"ProxmoxCache":   a.ProxmoxCache,
		"NotifyRegistry": a.NotifyRegistry,
		"AlertEngine":    a.AlertEngine,
		"CVEScanner":     a.CVEScanner,
		"DRSEngine":      a.DRSEngine,
		"DRSExecutor":    a.DRSExecutor,
		"RollingOrch":    a.RollingOrch,
		"ReportGen":      a.ReportGen,
	}
	for name, svc := range services {
		if svc == nil || reflect.ValueOf(svc).IsNil() {
			t.Errorf("%s is nil on a fully-wired App", name)
		}
	}
	if !a.HasDB() || !a.HasCrypto() {
		t.Error("HasDB and HasCrypto should both hold on a fully-wired App")
	}
}

// TestNew_NilLoggerAndContextDefaulted keeps the zero-argument path safe for
// callers that don't thread a logger or context.
func TestNew_NilLoggerAndContextDefaulted(t *testing.T) {
	//nolint:staticcheck // SA1012: passing a nil ctx is exactly what's under test
	a := New(nil, testConfig(), nil, nil, nil)
	if a.ShutdownCtx == nil {
		t.Error("ShutdownCtx should default to context.Background()")
	}
	if a.Logger == nil {
		t.Error("Logger should default to slog.Default()")
	}
}
