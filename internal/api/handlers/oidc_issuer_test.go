package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// oidcConfigMirror is a local copy of the OIDC write body's declaration
// (internal/api/registry_oidc.go), for the reason migrationListMirror gives.
func oidcConfigMirror() apischema.Properties {
	str := apischema.Property{Type: apischema.String, Optional: true}
	flag := apischema.Property{Type: apischema.Boolean, Optional: true, Default: false}
	list := apischema.Property{Type: apischema.Array, Optional: true, Items: &apischema.Property{Type: apischema.String}}
	return apischema.Properties{
		"id":   {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath, Optional: true},
		"name": str, "enabled": flag, "issuer_url": {Type: apischema.String, MinLength: apischema.Ptr(1)},
		"client_id": {Type: apischema.String, MinLength: apischema.Ptr(1)}, "client_secret": str,
		"redirect_uri": str, "scopes": list, "email_claim": str, "display_name_claim": str, "groups_claim": str,
		"group_role_mapping": {Type: apischema.Object, Optional: true}, "default_role_id": str,
		"auto_provision": flag, "allowed_domains": list, "acknowledge_insecure_redirect": flag,
		"allow_private_address": flag,
	}
}

// TestOIDCConfigWrites_AskToConfirmAPrivateIssuer: an issuer on a private address is
// refused with the notification channels' 422 and its address until confirmed; the
// confirmation is stored and audited, kept by an edit that leaves the issuer alone and
// asked again for a new one, and the test of a stored, unconfirmed one answers the same
// 422. Loopback and a zoned address are refused confirmed or not.
func TestOIDCConfigWrites_AskToConfirmAPrivateIssuer(t *testing.T) {
	t.Parallel()
	const redirect = `"redirect_uri":"https://nexara.example.com/api/v1/auth/oidc/callback"`
	body := func(issuer string, confirm bool) string {
		b := `{"issuer_url":"` + issuer + `","client_id":"nexara",` + redirect
		if confirm {
			b += `,"allow_private_address":true`
		}
		return b + "}"
	}
	confirmed := &db.OidcConfig{ID: uuid.New(), IssuerUrl: "https://10.0.0.5/realms/main", AllowPrivateAddress: true,
		RedirectUri: "https://nexara.example.com/api/v1/auth/oidc/callback"}
	for _, tc := range []struct {
		name, method, path, body string
		stored                   *db.OidcConfig
		status                   int
		allowStored              bool   // what the configuration holds after a write that answered 2xx
		ip                       string // details.ip of a 422
	}{
		{"create, not confirmed", http.MethodPost, "/oidc/configs", body("https://10.0.0.5/realms/main", false), nil, 422, false, "10.0.0.5"},
		{"create, confirmed", http.MethodPost, "/oidc/configs", body("https://10.0.0.5/realms/main", true), nil, 201, true, ""},
		{"create, loopback confirmed", http.MethodPost, "/oidc/configs", body("https://127.0.0.1/realms/main", true), nil, 400, false, ""},
		{"create, a zoned private address confirmed", http.MethodPost, "/oidc/configs", body("https://[fd12::1%25eth0]/realms/main", true), nil, 400, false, ""},
		{"update, issuer kept", http.MethodPut, "/oidc/configs/" + confirmed.ID.String(), body(confirmed.IssuerUrl, false), confirmed, 200, true, ""},
		{"update, a new private issuer", http.MethodPut, "/oidc/configs/" + confirmed.ID.String(), body("https://10.0.0.6/realms/main", false), confirmed, 422, false, "10.0.0.6"},
		{"test, not confirmed", http.MethodPost, "/oidc/configs/" + confirmed.ID.String() + "/test", "",
			&db.OidcConfig{ID: confirmed.ID, IssuerUrl: confirmed.IssuerUrl}, 422, false, "10.0.0.5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := newEpochStore()
			if tc.stored != nil {
				cfg := *tc.stored
				store.oidcConfig = &cfg
			}
			a := newEpochApp(t, store, epochOptions{})
			app := fiber.New(fiber.Config{ErrorHandler: authErrorHandler})
			app.Use(func(c fiber.Ctx) error { c.Locals("user_id", uuid.New()); return c.Next() })
			app.Post("/oidc/configs", withRequestParams(t, oidcConfigMirror(), nil, a.oidc.Create))
			app.Put("/oidc/configs/:id", withRequestParams(t, oidcConfigMirror(), []string{"id"}, a.oidc.Update))
			app.Post("/oidc/configs/:id/test", withRequestParams(t, apischema.Properties{
				"id": {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath}}, []string{"id"}, a.oidc.TestConnection))
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != tc.status {
				t.Fatalf("status %d (%s), want %d", resp.StatusCode, raw, tc.status)
			}
			if tc.ip != "" {
				var env struct {
					Error   string            `json:"error"`
					Details map[string]string `json:"details"`
				}
				_ = json.Unmarshal(raw, &env)
				if env.Error != "private_address_confirm_required" || env.Details["ip"] != tc.ip {
					t.Errorf("answer %s, want private_address_confirm_required with ip %s", raw, tc.ip)
				}
			}
			wrote := len(store.named("CreateOIDCConfig"))+len(store.named("UpdateOIDCConfig")) > 0
			if want := tc.status < 300; wrote != want {
				t.Errorf("the configuration was written: %v, answering %d", wrote, tc.status)
			}
			if wrote && store.oidcConfig.AllowPrivateAddress != tc.allowStored {
				t.Errorf("allow_private_address stored %v, want %v", store.oidcConfig.AllowPrivateAddress, tc.allowStored)
			}
			if wrote && tc.allowStored {
				if d := store.auditDetailsWritten(); len(d) != 1 || !strings.Contains(d[0], `"allow_private_address":true`) {
					t.Errorf("audit details %v, want the confirmation recorded", d)
				}
			}
		})
	}
}
