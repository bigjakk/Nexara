package db

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedTables lists tables to export, in dependency order (parents before children).
// These are configuration/setup tables — not metrics, history, or transient data.
var seedTables = []string{
	"roles",
	"permissions",
	"role_permissions",
	"users",
	"user_roles",
	"totp_recovery_codes",
	"clusters",
	"pbs_servers",
	"cluster_ssh_credentials",
	"ldap_configs",
	"oidc_configs",
	"settings",
	"drs_configs",
	"drs_rules",
	"scheduled_tasks",
	"alert_rules",
	"notification_channels",
	"maintenance_windows",
	"report_schedules",
	"firewall_templates",
}

// SeedData holds the full seed export.
type SeedData struct {
	ExportedAt string                       `json:"exported_at"`
	Version    string                       `json:"version"`
	Tables     map[string][]json.RawMessage `json:"tables"`
}

// ExportSeed dumps all seed tables to a JSON file.
func ExportSeed(ctx context.Context, pool *pgxpool.Pool, path string) error {
	seed := SeedData{
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Version:    "1",
		Tables:     make(map[string][]json.RawMessage),
	}

	for _, table := range seedTables {
		rows, err := exportTable(ctx, pool, table)
		if err != nil {
			// Table may not exist yet — skip silently.
			log.Printf("  skip %s: %v", table, err)
			continue
		}
		if len(rows) > 0 {
			seed.Tables[table] = rows
			log.Printf("  exported %s: %d rows", table, len(rows))
		}
	}

	data, err := json.MarshalIndent(seed, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal seed data: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write seed file: %w", err)
	}

	log.Printf("seed exported to %s (%d tables)", path, len(seed.Tables))
	return nil
}

// ImportSeed loads seed data from a JSON file into the database.
// Uses upsert (ON CONFLICT DO NOTHING) so it's safe on non-empty databases.
func ImportSeed(ctx context.Context, pool *pgxpool.Pool, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read seed file: %w", err)
	}

	var seed SeedData
	if err := json.Unmarshal(data, &seed); err != nil {
		return fmt.Errorf("parse seed file: %w", err)
	}

	log.Printf("importing seed from %s (exported %s)", path, seed.ExportedAt)

	// Check if DB already has data — if users exist, skip import unless empty.
	var userCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&userCount); err == nil && userCount > 0 {
		log.Printf("database already has %d users — skipping seed import (not a fresh install)", userCount)
		return nil
	}

	// Import in dependency order.
	for _, table := range seedTables {
		rows, ok := seed.Tables[table]
		if !ok || len(rows) == 0 {
			continue
		}
		imported, err := importTable(ctx, pool, table, rows)
		if err != nil {
			log.Printf("  warning: %s import error: %v", table, err)
			continue
		}
		log.Printf("  imported %s: %d rows", table, imported)
	}

	log.Printf("seed import complete")
	return nil
}

// exportTable returns all rows from a table as JSON objects.
func exportTable(ctx context.Context, pool *pgxpool.Pool, table string) ([]json.RawMessage, error) {
	query := fmt.Sprintf(
		`SELECT row_to_json(t) FROM (SELECT * FROM %s) t`, //nolint:gosec // table name from hardcoded list
		table,
	)
	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []json.RawMessage
	for rows.Next() {
		var raw json.RawMessage
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan row from %s: %w", table, err)
		}
		result = append(result, raw)
	}
	return result, rows.Err()
}

// importTable inserts rows into a table using a temp-table + merge approach.
// Each row is a JSON object matching the table's columns.
func importTable(ctx context.Context, pool *pgxpool.Pool, table string, rows []json.RawMessage) (int, error) {
	imported := 0
	for _, raw := range rows {
		// Build column names and values from the JSON object.
		var obj map[string]interface{}
		if err := json.Unmarshal(raw, &obj); err != nil {
			return imported, fmt.Errorf("unmarshal row: %w", err)
		}

		cols := make([]string, 0, len(obj))
		vals := make([]interface{}, 0, len(obj))
		placeholders := make([]string, 0, len(obj))
		i := 1
		for k, v := range obj {
			cols = append(cols, fmt.Sprintf("%q", k))
			vals = append(vals, v)
			placeholders = append(placeholders, fmt.Sprintf("$%d", i))
			i++
		}

		query := fmt.Sprintf(
			`INSERT INTO %s (%s) VALUES (%s) ON CONFLICT DO NOTHING`, //nolint:gosec // table name from hardcoded list
			table,
			joinStrings(cols, ", "),
			joinStrings(placeholders, ", "),
		)

		tag, err := pool.Exec(ctx, query, vals...)
		if err != nil {
			return imported, fmt.Errorf("insert into %s: %w", table, err)
		}
		if tag.RowsAffected() > 0 {
			imported++
		}
	}
	return imported, nil
}

func joinStrings(s []string, sep string) string {
	result := ""
	for i, v := range s {
		if i > 0 {
			result += sep
		}
		result += v
	}
	return result
}
