package db

import (
	"errors"
	"testing"

	"github.com/golang-migrate/migrate/v4"
)

// TestNewMigrator_OpensWithPoolKeysInTheURL: the admin guide tells operators to put
// pool_max_conns in DATABASE_URL, which pgxpool reads; the migrator gets the same
// URL and must still open, or the container never starts.
func TestNewMigrator_OpensWithPoolKeysInTheURL(t *testing.T) {
	m, cleanup, err := NewMigrator(testDBURL(t) + "&pool_max_conns=8")
	if err != nil {
		t.Fatalf("NewMigrator with pool_max_conns in the URL: %v", err)
	}
	defer cleanup()
	if _, _, err := m.Version(); err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		t.Fatalf("migrator version: %v", err)
	}
}
