package api

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/handlers"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/virtiowin"
)

// TestVirtioWinDownload_RefusesAConfiguredNodeTheClusterDoesNotHold drives the real
// Download over a config whose stored node the cluster does not hold (one saved before
// the API checked it): it answers 409 with the reason, or 500 when the lookup fails,
// and stops before anything reaches for a Proxmox client — the cluster row is never read.
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
			// The cluster holds no node, and the stored config names 192.0.2.10.
			fake := &handlerNodeDB{t: t, lookErr: tt.lookErr, rows: map[string]any{
				"GetVirtioWinConfig":  db.VirtioWinConfig{ClusterID: cluster, Enabled: true, Storage: "local", Node: "192.0.2.10"},
				"GetVirtioWinRelease": db.VirtioWinRelease{Version: "0.1.285-1", IsoVersion: "0.1.285", IsoFilename: "virtio-win-0.1.285.iso"},
			}}
			q := db.New(fake)
			probe := e
			probe.Handler = handlers.NewVirtioWinHandler(q, nil,
				virtiowin.NewEngine(q, "", slog.New(slog.NewTextHandler(io.Discard, nil)))).Download

			req := jsonRequest(fiber.MethodPost, strings.Replace(probe.Path, ":cluster_id", testClusterID, 1), `{"version":"0.1.285-1"}`)
			req.Header.Set("X-Test-User", "yes")
			status, env := send(t, mountWith(stubAuth(grantsOf("manage:storage")), everyNodeIsAMember(), probe), req)

			if status != tt.wantStatus || env.Message != tt.wantMsg {
				t.Errorf("got %d %q, want %d %q", status, env.Message, tt.wantStatus, tt.wantMsg)
			}
			if len(fake.asked()) == 0 || len(fake.afterLastQuestion()) != 0 {
				t.Errorf("statements %v: want the membership lookup, and nothing after it", fake.log)
			}
		})
	}
}
