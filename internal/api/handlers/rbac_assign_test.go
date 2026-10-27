package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// assignRoleDB answers the three statements AssignUserRole makes: the cluster
// lookup, the insert and the audit row.
type assignRoleDB struct {
	lookupErr error // the cluster lookup's answer: nil finds it
	insertErr error
	lookups   int
	inserts   int
}

func (*assignRoleDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (*assignRoleDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errCaptured
}

func (d *assignRoleDB) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "-- name: GetCluster :one"):
		d.lookups++
		if d.lookupErr != nil {
			return failRow{err: d.lookupErr}
		}
		return replayRow{row: db.Cluster{}}
	case strings.Contains(sql, "-- name: AssignUserRole :one"):
		d.inserts++
		if d.insertErr != nil {
			return failRow{err: d.insertErr}
		}
		return replayRow{row: db.UserRole{}}
	}
	return failRow{err: errCaptured}
}

// A cluster-scoped assignment has to name a cluster that exists: it is deleted with
// that cluster, and one on a cluster that is not there could never be. A cluster
// removed between the lookup and the insert is refused by the key, and answered the
// same; a violation of any other key is not the cluster's.
func TestAssignUserRole_ClusterScopeNamesAnExistingCluster(t *testing.T) {
	t.Parallel()
	fkViolation := func(constraint string) error {
		return &pgconn.PgError{Code: "23503", ConstraintName: constraint}
	}
	tests := []struct {
		name        string
		scopeType   string
		db          assignRoleDB
		wantStatus  int
		wantMessage string
		wantLookups int
		wantInserts int
	}{
		{"a cluster that exists", "cluster", assignRoleDB{}, http.StatusCreated, "", 1, 1},
		{"a cluster that does not exist", "cluster", assignRoleDB{lookupErr: pgx.ErrNoRows}, http.StatusBadRequest, errScopeClusterMissing, 1, 0},
		{"a cluster removed before the insert", "cluster", assignRoleDB{insertErr: fkViolation(scopeClusterKey)}, http.StatusBadRequest, errScopeClusterMissing, 1, 1},
		{"a role that does not exist is not the cluster's mistake", "cluster", assignRoleDB{insertErr: fkViolation("user_roles_role_id_fkey")}, http.StatusInternalServerError, "", 1, 1},
		{"a lookup that fails", "cluster", assignRoleDB{lookupErr: errCaptured}, http.StatusInternalServerError, "", 1, 0},
		{"an instance-wide assignment asks about no cluster", "global", assignRoleDB{lookupErr: pgx.ErrNoRows}, http.StatusCreated, "", 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fake := tt.db
			props := apischema.Properties{
				"user_id":    {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
				"role_id":    {Type: apischema.String, Format: "uuid"},
				"scope_type": {Type: apischema.String, Optional: true, Default: "global", Enum: []string{"global", "cluster"}},
				"scope_id":   {Type: apischema.String, Optional: true, Format: "uuid"},
			}
			h := NewRBACHandler(db.New(&fake), auth.NewRBACEngine(nil, nil), nil)
			app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
			app.Post("/users/:user_id/roles", withRequestParams(t, props, []string{"user_id"}, h.AssignUserRole))

			body := `{"role_id":"` + uuid.NewString() + `","scope_type":"` + tt.scopeType + `","scope_id":"` + uuid.NewString() + `"}`
			if tt.scopeType == "global" {
				body = `{"role_id":"` + uuid.NewString() + `","scope_type":"global"}`
			}
			req := httptest.NewRequest(http.MethodPost, "/users/"+uuid.NewString()+"/roles", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			raw, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != tt.wantStatus || !strings.Contains(string(raw), tt.wantMessage) {
				t.Errorf("answered %d %s, want %d containing %q", resp.StatusCode, raw, tt.wantStatus, tt.wantMessage)
			}
			if fake.lookups != tt.wantLookups || fake.inserts != tt.wantInserts {
				t.Errorf("%d cluster lookups and %d inserts, want %d and %d", fake.lookups, fake.inserts, tt.wantLookups, tt.wantInserts)
			}
		})
	}
}
