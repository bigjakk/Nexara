package db

import (
	"errors"
	"regexp"
	"slices"
	"testing"

	"github.com/golang-migrate/migrate/v4"

	"github.com/bigjakk/nexara/internal/reports"
)

// The report_schedules CHECK must list exactly the report types the generator
// implements: a type missing from it makes saving its schedule a constraint
// violation, which 000081 had to repair for vm_resource_usage. Each migration
// that adds a type re-creates the CHECK, and this pins the newest one.
// Not parallel: it migrates the shared test database.
func TestReportScheduleTypeCheckMatchesTheCatalogue(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	if err := env.Migrate.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up: %v", err)
	}

	var def string
	if err := env.Pool.QueryRow(env.Ctx,
		`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'report_schedules_report_type_check'`,
	).Scan(&def); err != nil {
		t.Fatalf("read the CHECK: %v", err)
	}
	matches := regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(def, -1)
	allowed := make([]string, 0, len(matches))
	for _, m := range matches {
		allowed = append(allowed, m[1])
	}
	implemented := make([]string, 0, len(reports.AllTypes))
	for _, rt := range reports.AllTypes {
		implemented = append(implemented, string(rt))
	}
	slices.Sort(allowed)
	slices.Sort(implemented)
	if !slices.Equal(allowed, implemented) {
		t.Errorf("the CHECK allows %v; the generator implements %v", allowed, implemented)
	}
}
