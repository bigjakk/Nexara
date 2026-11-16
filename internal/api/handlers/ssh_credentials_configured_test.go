package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// sshConfiguredDBTX answers HasClusterSSHCredentials with has, or fails it.
type sshConfiguredDBTX struct {
	rollingSSHDBTX
	has bool
	err error
}

func (d *sshConfiguredDBTX) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	if d.err != nil {
		return failRow{d.err}
	}
	return replayRow{struct{ B bool }{d.has}}
}

// The answer is the boolean alone, and a read that failed is a 500 rather
// than a "no", which the SPA would take as "disable Maintenance".
func TestSSHCredentialsConfigured(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name       string
		dbtx       *sshConfiguredDBTX
		wantStatus int
		wantBody   string
	}{
		{"configured", &sshConfiguredDBTX{has: true}, http.StatusOK, `{"configured":true}`},
		{"none", &sshConfiguredDBTX{has: false}, http.StatusOK, `{"configured":false}`},
		{"could not look", &sshConfiguredDBTX{err: errors.New("db down")}, http.StatusInternalServerError, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := NewRollingUpdateHandler(db.New(tt.dbtx), "", nil, nil)
			mirror := compiledMirror(t, apischema.Properties{
				"cluster_id": {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
			})
			app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
			app.Get("/clusters/:cluster_id/ssh-credentials/configured",
				withRequestParams(t, mirror, []string{"cluster_id"}, h.SSHCredentialsConfigured))
			resp, err := app.Test(httptest.NewRequest(http.MethodGet,
				"/clusters/"+uuid.New().String()+"/ssh-credentials/configured", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tt.wantStatus || (tt.wantBody != "" && string(body) != tt.wantBody) {
				t.Fatalf("got %d %s, want %d %s", resp.StatusCode, body, tt.wantStatus, tt.wantBody)
			}
		})
	}
}

// GET .../ssh-credentials answers null for none, and 503 when it could not look,
// rather than a null that reads as "none configured".
func TestGetSSHCredentials_CouldNotLookIsNotNone(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"none", pgx.ErrNoRows, http.StatusOK},
		{"could not look", errors.New("db down"), http.StatusServiceUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := NewRollingUpdateHandler(db.New(&sshConfiguredDBTX{err: tt.err}), "", nil, nil)
			mirror := compiledMirror(t, apischema.Properties{
				"cluster_id": {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
			})
			app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
			app.Get("/clusters/:cluster_id/ssh-credentials",
				withRequestParams(t, mirror, []string{"cluster_id"}, h.GetSSHCredentials))
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/clusters/"+uuid.New().String()+"/ssh-credentials", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tt.wantStatus || (tt.wantStatus == http.StatusOK && string(body) != "null") {
				t.Fatalf("got %d %s, want %d", resp.StatusCode, body, tt.wantStatus)
			}
		})
	}
}
