package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// Proxmox's create_rule puts every new rule at the top of the list
// (internal/proxmox/client_firewall.go), so the list a template leaves is its
// creates replayed that way. Sent first row first, the rules landed reversed,
// with a closing broad DROP above the template's own ACCEPTs.
func TestApplyTemplateKeepsTheTemplatesOrder(t *testing.T) {
	t.Parallel()

	templateID := uuid.New()
	template := db.FirewallTemplate{
		ID:   templateID,
		Name: "web",
		Rules: json.RawMessage(`[
			{"type":"in","action":"ACCEPT","dport":"22","enable":1,"comment":"first"},
			{"type":"in","action":"ACCEPT","dport":"443","enable":1,"comment":"second"},
			{"type":"in","action":"DROP","enable":1,"comment":"third"}]`),
	}
	h := newPathParamHarness(t, func(app *fiber.App, _ *db.Queries) {
		queries := db.New(&auditRowsDBTX{rowFor: map[string]any{"GetFirewallTemplate": template}})
		app.Post("/api/v1/clusters/:cluster_id/firewall-templates/:id/apply",
			withRequestParams(t, apischema.Properties{
				"cluster_id": apischema.StdOption("cluster-id"),
				"id":         {Type: apischema.String, Format: "uuid"},
			}, []string{"cluster_id", "id"}, NewNetworkHandler(queries, pathParamEncKey, nil).ApplyTemplate))
	})

	resp, err := h.app.Test(httptest.NewRequest(http.MethodPost,
		"/api/v1/clusters/"+h.clusterID.String()+"/firewall-templates/"+templateID.String()+"/apply", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	want := []string{"first", "second", "third"}
	for _, target := range h.capture.seen() {
		if target != "/api2/json/cluster/firewall/rules" {
			t.Fatalf("Proxmox was called at %q; want only rule creates: %q", target, h.capture.seen())
		}
	}
	var list []string
	for _, form := range h.capture.sentForms() {
		list = append([]string{form.Get("comment")}, list...)
	}
	if !slices.Equal(list, want) {
		t.Fatalf("the cluster's rules read %q from the top, want the template's order %q", list, want)
	}
}
