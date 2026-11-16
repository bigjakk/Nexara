package handlers

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The search finds a UPS by its name, description, model or NUT server, for a
// holder of global view:ups alone, as every UPS route is gated.
func TestGlobalSearchFindsUps(t *testing.T) {
	t.Parallel()
	rack := db.ListUpsDevicesRow{ID: uuid.New(), UpsName: "ups01", Description: "Rack UPS", ServerName: "nut01"}
	spare := db.ListUpsDevicesRow{ID: uuid.New(), UpsName: "ups02", ServerName: "nut02"}
	for _, tc := range []struct {
		name, q string
		engine  permissionEngine
		want    []string
	}{
		{"by its name", "ups0", newGrantEngine("view:ups"), []string{"ups01", "ups02"}},
		{"by its NUT server", "nut02", newGrantEngine("view:ups"), []string{"ups02"}},
		{"by its description", "rack", newGrantEngine("view:ups"), []string{"ups01"}},
		{"without view:ups", "ups0", newGrantEngine("view:cluster"), nil},
	} {
		dbtx := &auditRowsDBTX{rowsFor: map[string][]any{"ListUpsDevices": {rack, spare}}}
		h := NewSearchHandler(db.New(dbtx), testEncryptionKey, nil)
		app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
		app.Use(func(c fiber.Ctx) error {
			c.Locals("user_id", uuid.New())
			c.Locals("rbac_engine", tc.engine)
			return c.Next()
		})
		app.Get("/search", withRequestParams(t, apischema.Properties{"q": {Type: apischema.String}}, nil, h.GlobalSearch))
		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/search?q="+tc.q, nil))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		var page struct {
			Items []searchResult `json:"items"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatalf("%s: %v (%s)", tc.name, err, raw)
		}
		got := make([]string, 0, len(page.Items))
		for _, r := range page.Items {
			if r.Type == "ups" {
				got = append(got, r.Name)
			}
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: UPS results %v, want %v (%s)", tc.name, got, tc.want, raw)
		}
	}
}

// A guest result carries its halted QEMU state, which the SPA's dot shows in
// place of Running.
func TestGlobalSearchCarriesRunState(t *testing.T) {
	t.Parallel()
	vm := db.ListAllVMsRow{ID: uuid.New(), ClusterID: uuid.New(), Vmid: 101, Name: "linux01", Type: "qemu", Status: "running", RunState: "io-error"}
	dbtx := &auditRowsDBTX{rowsFor: map[string][]any{"ListAllVMs": {vm}}}
	h := NewSearchHandler(db.New(dbtx), testEncryptionKey, nil)
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", uuid.New())
		c.Locals("rbac_engine", newGrantEngine("view:cluster"))
		return c.Next()
	})
	app.Get("/search", withRequestParams(t, apischema.Properties{"q": {Type: apischema.String}}, nil, h.GlobalSearch))
	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/search?q=linux", nil))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var page struct {
		Items []searchResult `json:"items"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("%v (%s)", err, raw)
	}
	if len(page.Items) != 1 || page.Items[0].RunState != "io-error" {
		t.Errorf("results %s, want linux01 with run_state io-error", raw)
	}
}
