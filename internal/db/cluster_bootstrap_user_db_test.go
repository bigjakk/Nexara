package db

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// CountClustersSharingBootstrapUser decides whether deleting a cluster may revoke its
// Proxmox account, so it must find a sibling whose user name holds a "!": Proxmox splits a
// token id at its LAST "!" (realm and token names cannot contain one), and a split at the
// first undercounts and deletes an account another cluster still uses.
func TestCountClustersSharingBootstrapUser_SplitsAtTheLastBang(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)

	id := func(n int) uuid.UUID { return uuid.MustParse(fmt.Sprintf("12900000-0000-4000-8000-%012d", n)) }
	rows := []struct {
		id      uuid.UUID
		tokenID string
	}{
		{id(1), "ops!lab@pve!nexara"},  // the cluster being deleted
		{id(2), "ops!lab@pve!other"},   // a sibling on the same account
		{id(3), "ops@pve!nexara"},      // a different account
		{id(4), "alice@pve!nexara-ci"}, // unrelated
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.id
	}
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, ids)
	}
	purge()
	defer purge()
	for i, r := range rows {
		if _, err := pool.Exec(ctx, `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, $2, 'https://cluster.invalid:8006', $3, 'x')`, r.id, fmt.Sprintf("bang-cluster%02d", i+1), r.tokenID); err != nil {
			t.Fatalf("insert %s: %v", r.tokenID, err)
		}
	}

	for _, tc := range []struct {
		user string
		want int64
	}{
		{"ops!lab@pve", 1}, // row 2
		{"ops@pve", 1},     // row 3
	} {
		n, err := gen.New(pool).CountClustersSharingBootstrapUser(ctx, gen.CountClustersSharingBootstrapUserParams{
			ExcludeClusterID: id(1), BootstrapUserID: tc.user,
		})
		if err != nil {
			t.Fatalf("count %s: %v", tc.user, err)
		}
		if n != tc.want {
			t.Errorf("clusters sharing %s = %d, want %d", tc.user, n, tc.want)
		}
	}
}
