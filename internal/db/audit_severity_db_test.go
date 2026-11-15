package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

var severityTestUserID = uuid.MustParse("5e7e0000-0000-4000-8000-000000000001")

// TestAuditSeverityFilter pins ListAuditLogAdvanced's severity filter to the
// SPA's deriveSeverity (components/layout/activity-columns.ts), which marks the
// rows: the export and the list must hold the rows the page calls by that name.
// A reserved setting's details count only when the caller may read them.
// Not parallel: it migrates the shared test database, as its neighbours do.
func TestAuditSeverityFilter(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE user_id = $1`, severityTestUserID)
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, severityTestUserID)
	}
	purge()
	defer purge()

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, email, password_hash, display_name)
		 VALUES ($1, 'audit-severity-test@example.invalid', 'x', 'Audit Severity Test')`,
		severityTestUserID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	rows := []struct {
		resourceType, resourceID, action, details string
		want, wantHidden                          string
	}{
		{"vm", "1", "vm_created", `{"error":"boom"}`, "error", "error"},
		{"vm", "2", "vm_created", `{"error":""}`, "info", "info"},
		{"vm", "3", "vm_created", `{"status":"failed"}`, "error", "error"},
		{"vm", "4", "vm_created", `{"status":["failed"]}`, "info", "info"},
		{"vm", "5", "Vm_Start_Failed", `{}`, "error", "error"},
		{"vm", "6", "vm_shutdown", `{}`, "warning", "warning"},
		{"vm", "7", "vm_created", `[]`, "info", "info"},
		{"setting", "syslog_forwarding", "syslog_tested", `{"error":"refused"}`, "error", "info"},
	}
	ids := make(map[uuid.UUID]int, len(rows))
	for i, r := range rows {
		id := uuid.New()
		ids[id] = i
		if _, err := pool.Exec(ctx,
			`INSERT INTO audit_log (id, user_id, resource_type, resource_id, action, details)
			 VALUES ($1, $2, $3, $4, $5, $6::jsonb)`,
			id, severityTestUserID, r.resourceType, r.resourceID, r.action, r.details); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}

	queries := gen.New(pool)
	for _, hidden := range [][]string{nil, {"syslog_forwarding"}} {
		for _, severity := range []string{"info", "warning", "error"} {
			got, err := queries.ListAuditLogAdvanced(ctx, gen.ListAuditLogAdvancedParams{
				Limit:             100,
				UserID:            pgtype.UUID{Bytes: severityTestUserID, Valid: true},
				Severity:          pgtype.Text{String: severity, Valid: true},
				HiddenSettingKeys: hidden,
			})
			if err != nil {
				t.Fatalf("ListAuditLogAdvanced: %v", err)
			}
			count, err := queries.CountAuditLogAdvanced(ctx, gen.CountAuditLogAdvancedParams{
				UserID:            pgtype.UUID{Bytes: severityTestUserID, Valid: true},
				Severity:          pgtype.Text{String: severity, Valid: true},
				HiddenSettingKeys: hidden,
			})
			if err != nil {
				t.Fatalf("CountAuditLogAdvanced: %v", err)
			}
			want := 0
			for _, r := range rows {
				w := r.want
				if hidden != nil {
					w = r.wantHidden
				}
				if w == severity {
					want++
				}
			}
			for _, g := range got {
				r := rows[ids[g.ID]]
				w := r.want
				if hidden != nil {
					w = r.wantHidden
				}
				if w != severity {
					t.Errorf("hidden=%v severity=%s returned %s %s, which the SPA marks %s",
						hidden, severity, r.action, r.details, w)
				}
			}
			if len(got) != want || count != int64(want) {
				t.Errorf("hidden=%v severity=%s: %d rows, count %d, want %d", hidden, severity, len(got), count, want)
			}
		}
	}
}
