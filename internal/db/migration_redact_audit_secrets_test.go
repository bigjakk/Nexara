package db

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"
)

func redactAuditID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("12700000-0000-4000-8000-%012d", n))
}

// The scrub of 000127 over the shapes the old rows have: secrets of a guest
// config write's fields become "(set)" or "(cleared)" and a URL's credentials
// "(set)", in the row and the task's description, while a fields list of names, a
// top-level key, an e-mail address and what lies beside a secret stay as written.
// Not parallel: it moves the shared schema down and back up.
func TestMigrationRedactAuditSecrets(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool, m := env.Ctx, env.Pool, env.Migrate

	before, version, up := migrationByName(t, "redact_audit_secrets")
	const probe = "PROBE-SECRET-NOT-A-REAL-VALUE"
	cluster, user := redactAuditID(1), redactAuditID(2)
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE id::text LIKE '12700000-0000-4000-8000-%'`)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, cluster) // its tasks go with it
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, user)
	}
	purge()
	defer purge()
	defer func() {
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			t.Errorf("restore to head: %v", err)
		}
	}()
	if err := m.Migrate(before); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate to %d: %v", before, err)
	}
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	exec("user", `INSERT INTO users (id, email, password_hash, display_name)
		VALUES ($1, 'migration-redact-audit@example.invalid', 'x', 'Redact audit')`, user)
	exec("cluster", `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
		VALUES ($1, 'cluster01', 'https://cluster.invalid:8006', 'test@pam!t', 'x')`, cluster)

	source := "https://user01:" + probe + "@mirror.example.com/x.iso"
	rows := []struct{ written, want string }{
		{`{"fields": {"cipassword": "` + probe + `", "ciuser": "user01", "delete": "sshkeys"}}`,
			`{"fields": {"ciuser": "user01", "delete": "sshkeys", "cipassword": "(set)"}}`},
		{`{"fields": {"cipassword": ""}}`, `{"fields": {"cipassword": "(cleared)"}}`},
		{`{"fields": {"onboot": "1", "password": "` + probe + `"}}`, `{"fields": {"onboot": "1", "password": "(set)"}}`},
		{`{"url": "` + source + `", "node": "pve-01"}`, `{"url": "https://(set)@mirror.example.com/x.iso", "node": "pve-01"}`},
		{`{"fields": ["password", "content"], "storage": "store01"}`, ``},
		{`{"key": "syslog_forwarding", "scope": "global", "email": "user01@example.com"}`, ``},
	}
	for i, r := range rows {
		exec("audit row", `INSERT INTO audit_log (id, user_id, resource_type, resource_id, action, details)
			VALUES ($1, NULL, 'vm', '101', 'config_update', $2)`, redactAuditID(10+i), r.written)
	}
	exec("tasks", `INSERT INTO task_history (cluster_id, user_id, upid, description) VALUES
		($1, $2, 'UPID:pve-01:00001270:00000001:65000000:download:x.iso:user01@pve:', $3),
		($1, $2, 'UPID:pve-01:00001270:00000002:65000000:qmcreate:101:user01@pve:', 'create VM 101')`,
		cluster, user, "Download "+source)

	// check reads each row back as the database renders it, so a want is compared
	// byte for byte; an empty want is the row as written.
	check := func(when string) {
		t.Helper()
		for i, r := range rows {
			var got string
			if err := pool.QueryRow(ctx, `SELECT details::text FROM audit_log WHERE id = $1`, redactAuditID(10+i)).Scan(&got); err != nil {
				t.Fatalf("read row %d: %v", i, err)
			}
			var want string
			if err := pool.QueryRow(ctx, `SELECT $1::jsonb::text`, cmp.Or(r.want, r.written)).Scan(&want); err != nil {
				t.Fatalf("render want %d: %v", i, err)
			}
			if got != want {
				t.Errorf("%s: row %d is %s, want %s", when, i, got, want)
			}
		}
		var descriptions []string
		if err := pool.QueryRow(ctx, `SELECT array_agg(description ORDER BY upid) FROM task_history WHERE cluster_id = $1`, cluster).Scan(&descriptions); err != nil {
			t.Fatalf("read tasks: %v", err)
		}
		if want := []string{"Download https://(set)@mirror.example.com/x.iso", "create VM 101"}; fmt.Sprint(descriptions) != fmt.Sprint(want) {
			t.Errorf("%s: task descriptions %q, want %q", when, descriptions, want)
		}
	}

	if err := m.Migrate(version); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate to %d: %v", version, err)
	}
	check("after the migration")
	exec("a second run", up)
	check("after a second run")
}
