package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/api/handlers"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/virtiowin"
)

// virtioWinNodeDB is a db.DBTX holding one virtio-win config whose node is
// 192.0.2.10 and one release. It answers the node-membership lookup with no
// row (or lookErr), records every statement by its sqlc name, and answers
// anything else with errStop.
type virtioWinNodeDB struct {
	t       *testing.T
	cluster uuid.UUID
	lookErr error
	names   []string
}

func (d *virtioWinNodeDB) note(sql string) string {
	name := sql
	if m := sqlcNameRe.FindStringSubmatch(sql); m != nil {
		name = m[1]
	}
	d.names = append(d.names, name)
	return name
}

func (d *virtioWinNodeDB) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	d.note(sql)
	return pgconn.CommandTag{}, errStop
}

func (d *virtioWinNodeDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	d.note(sql)
	return nil, errStop
}

func (d *virtioWinNodeDB) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	switch d.note(sql) {
	case "GetVirtioWinConfig":
		return fieldRow{t: d.t, v: db.VirtioWinConfig{ClusterID: d.cluster, Enabled: true, Storage: "local", Node: "192.0.2.10"}}
	case "GetVirtioWinRelease":
		return fieldRow{t: d.t, v: db.VirtioWinRelease{Version: "0.1.285-1", IsoVersion: "0.1.285", IsoFilename: "virtio-win-0.1.285.iso"}}
	case "GetNodeByClusterAndName":
		if d.lookErr != nil {
			return fieldRow{err: d.lookErr}
		}
		return fieldRow{err: pgx.ErrNoRows}
	}
	return fieldRow{err: errStop}
}

// TestVirtioWinDownload_RefusesAConfiguredNodeTheClusterDoesNotHold drives the
// real Download over a config whose stored node the cluster does not hold (one
// saved before the API checked it): it answers 409 with the reason, or 500
// when the lookup fails, and stops before anything reaches for a Proxmox
// client — the cluster row is never read.
func TestVirtioWinDownload_RefusesAConfiguredNodeTheClusterDoesNotHold(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, virtioWinClusterScope+"/download")
	cluster := uuid.MustParse(testClusterID)
	for _, tt := range []struct {
		name       string
		lookErr    error
		wantStatus int
		wantMsg    string
	}{
		{name: "a stranger", wantStatus: fiber.StatusConflict,
			wantMsg: `node "192.0.2.10" is not one of this cluster's nodes; it was not contacted`},
		{name: "a failed lookup", lookErr: errors.New("connection refused"),
			wantStatus: fiber.StatusInternalServerError, wantMsg: "Failed to look up the node"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := &virtioWinNodeDB{t: t, cluster: cluster, lookErr: tt.lookErr}
			q := db.New(fake)
			probe := e
			probe.Handler = handlers.NewVirtioWinHandler(q, nil,
				virtiowin.NewEngine(q, "", slog.New(slog.NewTextHandler(io.Discard, nil)))).Download
			reg := NewRegistry()
			reg.Register(probe)
			app := fiber.New(fiber.Config{ErrorHandler: errorHandler})
			mountRegistry(app, reg, stubAuth(map[string]bool{"manage:storage": true}), everyNodeIsAMember())

			target := strings.Replace(probe.Path, ":cluster_id", testClusterID, 1)
			req := httptest.NewRequest(fiber.MethodPost, target, bytes.NewReader([]byte(`{"version":"0.1.285-1"}`)))
			req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			req.Header.Set("X-Test-User", "yes")
			status, env := send(t, app, req)

			if status != tt.wantStatus || env.Message != tt.wantMsg {
				t.Errorf("got %d %q, want %d %q", status, env.Message, tt.wantStatus, tt.wantMsg)
			}
			if i := slices.Index(fake.names, "GetNodeByClusterAndName"); i < 0 || i != len(fake.names)-1 {
				t.Errorf("statements %v: want the membership lookup, and nothing after it", fake.names)
			}
		})
	}
}
