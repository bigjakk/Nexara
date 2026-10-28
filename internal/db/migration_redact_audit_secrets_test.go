package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/redact"
)

func redactAuditID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("12700000-0000-4000-8000-%012d", n))
}

// redactedJSON is s, a JSON document, with redact.URLs applied to every string
// value in it as the Go side does to the rows it writes from this release.
func redactedJSON(t *testing.T, s string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decode %s: %v", s, err)
	}
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			return redact.URLs(x)
		case map[string]any:
			for k, e := range x {
				x[k] = walk(e)
			}
		case []any:
			for i, e := range x {
				x[i] = walk(e)
			}
		}
		return v
	}
	out, _ := json.Marshal(walk(v))
	return string(out)
}

// The scrub of 000127 over the shapes the old rows have, each expected value
// computed by the Go side's own functions: a secret parameter in a guest config
// write's fields becomes "(set)" or "(cleared)"; every string of an audit row,
// a task description, an alert, a dead-letter payload and a report run keeps no
// URL userinfo, query or fragment, and a dead-letter error no channel path; a
// fields list of names, a key, an e-mail address and what lies beside a secret
// stay as written. Not parallel: it moves the shared schema down and back up.
func TestMigrationRedactAuditSecrets(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool, m := env.Ctx, env.Pool, env.Migrate

	before, version, up := migrationByName(t, "redact_audit_secrets")
	const probe = "PROBE-SECRET-NOT-A-REAL-VALUE"
	cluster, user, rule := redactAuditID(1), redactAuditID(2), redactAuditID(3)
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE id::text LIKE '12700000-0000-4000-8000-%'`)
		_, _ = pool.Exec(pctx, `DELETE FROM notification_dlq WHERE id::text LIKE '12700000-0000-4000-8000-%'`)
		_, _ = pool.Exec(pctx, `DELETE FROM alert_rules WHERE id = $1`, rule) // its alerts go with it
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, cluster) // its tasks and reports go with it
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
	exec("rule", `INSERT INTO alert_rules (id, name, metric, operator, threshold, created_by, cluster_id, scope_type)
		VALUES ($1, 'rule01', 'pve_task_failed', '>', 0, $2, $3, 'cluster')`, rule, user, cluster)

	source := "https://user01:" + probe + "@mirror.example.com/x.iso?X-Amz-Signature=" + probe
	failed := "cluster01 — 1 Proxmox task failed or was cancelled in the last 24h: Download " + source
	hook := "https://hooks.example.com/services/T000/B000/" + probe
	type text struct{ table, column, id, written, want string }
	// Audit rows: what each was written as and, where the scrub changes it, what it becomes.
	rows := []struct{ written, want string }{
		{`{"fields": {"cipassword": "` + probe + `", "ciuser": "user01", "delete": "sshkeys"}}`,
			`{"fields": {"ciuser": "user01", "delete": "sshkeys", "cipassword": "(set)"}}`},
		{`{"fields": {"cipassword": ""}}`, `{"fields": {"cipassword": "(cleared)"}}`},
		{`{"fields": {"onboot": "1", "password": "` + probe + `"}}`, `{"fields": {"onboot": "1", "password": "(set)"}}`},
		{`{"fields": {"env": "A=` + probe + `", "client-key": "` + probe + `", "tfa": "type=yubico,key=` + probe + `", "totp": "otpauth://totp/x?secret=` + probe + `"}}`,
			`{"fields": {"env": "(set)", "tfa": "(set)", "totp": "(set)", "client-key": "(set)"}}`},
		{`{"url": "` + source + `", "node": "pve-01"}`, ``},
		{`{"url": "https://bucket.example.com/x.iso?X-Amz-Signature=` + probe + `"}`, ``},
		{`{"items": [{"note": "see https://wiki.example.com/p?a=` + probe + `\nnext \"line\""}, "https://h.example.com/p#key=` + probe + `"], "fields": {"description": "x https://h.example.com/?t=` + probe + ` y"}}`, ``},
		{`{"fields": ["password", "content"], "storage": "store01"}`, `-`},
		{`{"key": "syslog_forwarding", "scope": "global", "email": "user01@example.com"}`, `-`},
	}
	for i, r := range rows {
		exec("audit row", `INSERT INTO audit_log (id, user_id, resource_type, resource_id, action, details)
			VALUES ($1, NULL, 'vm', '101', 'config_update', $2)`, redactAuditID(10+i), r.written)
	}
	exec("tasks", `INSERT INTO task_history (cluster_id, user_id, upid, description) VALUES
		($1, $2, 'UPID:pve-01:00001270:00000001:65000000:download:x.iso:user01@pve:', $3),
		($1, $2, 'UPID:pve-01:00001270:00000002:65000000:qmcreate:101:user01@pve:', 'create VM 101')`,
		cluster, user, "Download "+source)
	exec("alert", `INSERT INTO alert_history (id, rule_id, state, severity, cluster_id, resource_name, metric, current_value, threshold, message)
		VALUES ($1, $2, 'firing', 'warning', $3, $4, 'pve_task_failed', 1, 0, $5)`,
		redactAuditID(30), rule, cluster, failed, "pve_task_failed > 0 on "+failed+" (current: 1.00)")
	payload, _ := json.Marshal(map[string]any{"rule_name": "rule01", "resource_name": failed, "message": "on " + failed})
	exec("dead letter", `INSERT INTO notification_dlq (id, channel_type, payload, last_error) VALUES ($1, 'slack', $2, $3)`,
		redactAuditID(31), payload, `send request: Post "`+hook+`?token=`+probe+`": context canceled`)
	exec("report", `INSERT INTO report_runs (id, report_type, cluster_id, created_by, status, report_data, report_html, report_csv)
		VALUES ($1, 'digest', $2, $3, 'completed', $4, $5, $6)`, redactAuditID(32), cluster, user,
		`{"findings": [{"detail": "Download `+source+`."}]}`, `<td><strong>Download `+source+`</strong></td>`, `"Download `+source+`",failed`)
	texts := []text{
		{"task_history", "description", "upid", "UPID:pve-01:00001270:00000001:65000000:download:x.iso:user01@pve:", redact.URLs("Download " + source)},
		{"task_history", "description", "upid", "UPID:pve-01:00001270:00000002:65000000:qmcreate:101:user01@pve:", "create VM 101"},
		{"alert_history", "resource_name", "id", redactAuditID(30).String(), redact.URLs(failed)},
		{"alert_history", "message", "id", redactAuditID(30).String(), redact.URLs("pve_task_failed > 0 on " + failed + " (current: 1.00)")},
		{"notification_dlq", "payload::text", "id", redactAuditID(31).String(), redactedJSON(t, string(payload))},
		{"notification_dlq", "last_error", "id", redactAuditID(31).String(),
			fmt.Sprintf("send request: Post %q: context canceled", redact.TargetURL(hook+"?token="+probe))},
		{"report_runs", "report_data::text", "id", redactAuditID(32).String(), redactedJSON(t, `{"findings": [{"detail": "Download `+source+`."}]}`)},
		{"report_runs", "report_html", "id", redactAuditID(32).String(), redact.URLs(`<td><strong>Download ` + source + `</strong></td>`)},
		{"report_runs", "report_csv", "id", redactAuditID(32).String(), redact.URLs(`"Download ` + source + `",failed`)},
	}

	// check reads each value back as the database renders it, so a want is
	// compared byte for byte: an audit row's want "" is the Go walk of what was
	// written, "-" the row as written.
	check := func(when string) {
		t.Helper()
		for i, r := range rows {
			var got, want string
			if err := pool.QueryRow(ctx, `SELECT details::text FROM audit_log WHERE id = $1`, redactAuditID(10+i)).Scan(&got); err != nil {
				t.Fatalf("read row %d: %v", i, err)
			}
			w := r.want
			switch w {
			case "":
				w = redactedJSON(t, r.written)
			case "-":
				w = r.written
			}
			if err := pool.QueryRow(ctx, `SELECT $1::jsonb::text`, w).Scan(&want); err != nil {
				t.Fatalf("render want %d: %v", i, err)
			}
			if got != want || strings.Contains(got, probe) {
				t.Errorf("%s: row %d is %s, want %s", when, i, got, want)
			}
		}
		for _, x := range texts {
			var got, want string
			if err := pool.QueryRow(ctx, `SELECT `+x.column+` FROM `+x.table+` WHERE `+x.id+`::text = $1`, x.written).Scan(&got); err != nil {
				t.Fatalf("read %s.%s: %v", x.table, x.column, err)
			}
			want = x.want
			if strings.HasSuffix(x.column, "::text") {
				if err := pool.QueryRow(ctx, `SELECT $1::jsonb::text`, want).Scan(&want); err != nil {
					t.Fatalf("render want: %v", err)
				}
			}
			if got != want || strings.Contains(got, probe) {
				t.Errorf("%s: %s.%s is %q, want %q", when, x.table, x.column, got, want)
			}
		}
	}

	if err := m.Migrate(version); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate to %d: %v", version, err)
	}
	check("after the migration")
	exec("a second run", up)
	check("after a second run")
}
