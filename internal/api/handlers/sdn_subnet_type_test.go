package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// TestSDNSubnetCreateSendsSubnetForAnEmptyType drives the real CreateSDNSubnet
// against a stand-in Proxmox that records the form it was sent.
//
// Before the registry, the handler substituted "subnet" for an EMPTY type as
// well as an absent one. The declaration's Default now covers the absent
// case, but a Default never applies to a key the caller sent, so an explicit
// "" has to be substituted by the handler — or it reaches the form builder,
// which drops an empty value, and Proxmox refuses the create for a missing
// type it has no default for.
func TestSDNSubnetCreateSendsSubnetForAnEmptyType(t *testing.T) {
	var mu sync.Mutex
	var sent []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		mu.Lock()
		sent = append(sent, form)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	t.Cleanup(srv.Close)

	clusterID := uuid.New()
	encrypted, err := crypto.Encrypt("token-secret-value", pathParamEncKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	cache := proxmox.NewClientCache(pathParamCacheQueries{cluster: db.Cluster{
		ID:                   clusterID,
		Name:                 "cluster01",
		ApiUrl:               srv.URL,
		TokenID:              "user@pam!test",
		TokenSecretEncrypted: encrypted,
		IsActive:             true,
	}}, pathParamEncKey, nil, nil)

	// A mirror of the POST .../sdn/vnets/:vnet/subnets declaration's parts
	// this case sends; the type property is the declaration's own shape —
	// optional, Default "subnet", no MinLength.
	mirror := compiledMirror(t, apischema.Properties{
		"cluster_id": {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
		"vnet":       {Type: apischema.String, Source: apischema.SourcePath},
		"subnet":     {Type: apischema.String, Format: "cidr"},
		"type":       {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(32), Default: "subnet"},
		// Read by sdnSubnetSettingsFromParams; declared so the handler can
		// read them, and never sent.
		"gateway":         {Type: apischema.String, Optional: true},
		"snat":            {Type: apischema.Integer, Optional: true},
		"dhcp-range":      {Type: apischema.String, Optional: true},
		"dhcp-dns-server": {Type: apischema.String, Optional: true},
	})
	handler := NewNetworkHandler(db.New(pathParamDBTX{}), pathParamEncKey, nil)
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Use(func(c fiber.Ctx) error {
		SetProxmoxCacheLocal(c, cache)
		return c.Next()
	})
	app.Post("/clusters/:cluster_id/sdn/vnets/:vnet/subnets",
		withRequestParams(t, mirror, []string{"cluster_id", "vnet"}, handler.CreateSDNSubnet))

	for _, tt := range []struct {
		name string
		body string
	}{
		{"omitted", `{"subnet":"192.0.2.0/24"}`},
		{"empty", `{"subnet":"192.0.2.0/24","type":""}`},
		{"explicit", `{"subnet":"192.0.2.0/24","type":"subnet"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mu.Lock()
			sent = nil
			mu.Unlock()

			req := httptest.NewRequest(http.MethodPost,
				"/clusters/"+clusterID.String()+"/sdn/vnets/vnet01/subnets", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusCreated {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d (body: %s), want 201", resp.StatusCode, body)
			}

			mu.Lock()
			defer mu.Unlock()
			if len(sent) != 1 {
				t.Fatalf("Proxmox was sent %d requests, want 1", len(sent))
			}
			if got := sent[0].Get("type"); got != "subnet" {
				t.Errorf("Proxmox was sent type=%q, want \"subnet\"", got)
			}
		})
	}
}
