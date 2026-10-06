package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The REAL PUT .../access/users/:userid/tokens/:tokenid declaration, permission gate
// and AccessHandler.UpdateToken over the stand-ins of registry_access_kit_test.go.
// The handler's unit tests pass with the builder deleted from it and with an
// UpdateToken that never consults the guard's decision.

func newAccessTokenApp(t *testing.T) (*fiber.App, *accessUpdatePVE, *accessUpdateDB) {
	t.Helper()
	return newAccessApp(t, manageAccess, func(h *handlers.AccessHandler) []realRoute {
		return []realRoute{{fiber.MethodPut, accessScope + "/users/:userid/tokens/:tokenid", h.UpdateToken}}
	})
}

// accessTokenRequest is the request a client sends about a token, given the two
// path segments as a client sends them (the user id percent-encoded). A request
// with no body is sent with none, as a DELETE is.
func accessTokenRequest(method, user, token, query, body string) *http.Request {
	target := strings.NewReplacer(":cluster_id", testClusterID, ":userid", user, ":tokenid", token).
		Replace(accessScope+"/users/:userid/tokens/:tokenid") + query
	if body == "" {
		return authedRequest(method, target)
	}
	req := jsonRequest(method, target, body)
	req.Header.Set("X-Test-User", "yes")
	return req
}

// accessTokenSecret is the secret the stand-in Proxmox hands back for a
// regeneration: findable in a row whatever key it is under, and not a real one.
const accessTokenSecret = "PROBE-TOKEN-SECRET-NOT-A-REAL-VALUE"

// accessTokenRegenerated is what Proxmox answers the regeneration of token fullID with.
func accessTokenRegenerated(fullID string) string {
	return `{"data":{"full-tokenid":"` + fullID + `","value":"` + accessTokenSecret + `"}}`
}

func pveTokenPath(fullID string) string {
	user, token, _ := strings.Cut(fullID, "!")
	return pveUserPath(user) + "/token/" + token
}

// TestAccessUpdateTokenAuditRow follows a token update to the form Proxmox receives and the
// audit row, which carries the account, the token, whether the secret was rotated, whether the
// request was sent with force on an update the guard covers, and the expire and privsep it
// set; never the comment or the secret. The stand-in cluster's token is the account Nexara
// authenticates as, so the guarded updates without force (regenerate, a non-zero expire,
// privsep=true) are refused 409 and reach Proxmox not at all, and their forced twins go
// through, recorded as forced. The unguarded ones (expire 0, privsep=false, a comment) record
// forced=false even when sent with it, which a flag merely copied from the request would get
// wrong. Another account's token, and another token of the same account, are not the credential.
func TestAccessUpdateTokenAuditRow(t *testing.T) {
	const (
		own      = "nexara@pve!api"
		conflict = "token nexara@pve!api"
	)
	never := []string{"sentinel-comment", accessTokenSecret}
	refused := auditWant{status: fiber.StatusConflict, message: []string{conflict, "force=true"}}
	// sent is a request that goes through: what Proxmox must receive, and the row.
	sent := func(fullID string, form url.Values, rowAction, details string, body ...string) auditWant {
		user, token, _ := strings.Cut(fullID, "!")
		return auditWant{
			status: fiber.StatusOK, method: http.MethodPut, path: pveTokenPath(fullID), form: form, body: body, never: never,
			audit: auditRow("pve_token", fullID, rowAction, `{`+details+`,"tokenid":"`+token+`","userid":"`+user+`"}`),
		}
	}

	tests := []struct {
		name        string
		user, token string // as the client sends them in the path
		query, body string
		pveReply    string // what the stand-in Proxmox answers the update with
		pveStatus   int    // or the status it refuses with, and pveBody what it says
		pveBody     string
		want        auditWant
	}{
		// The guarded updates of Nexara's own token, without force.
		{name: "regenerating the token Nexara uses is refused and leaves no trace",
			user: testAccessUserID, token: testAccessTokenID, body: `{"regenerate":true}`, want: refused},
		{name: "giving it an expiry is refused and leaves no trace",
			user: testAccessUserID, token: testAccessTokenID, body: `{"expire":1767225600}`, want: refused},
		{name: "turning its privilege separation on is refused and leaves no trace",
			user: testAccessUserID, token: testAccessTokenID, body: `{"privsep":true}`, want: refused},
		{name: "a guarded field beside unguarded ones is still refused",
			user: testAccessUserID, token: testAccessTokenID, body: `{"comment":"sentinel-comment","expire":0,"privsep":true}`, want: refused},

		// The same updates, forced.
		{name: "a forced regeneration is recorded as forced and as a regeneration, without the secret",
			user: testAccessUserID, token: testAccessTokenID, query: "?force=true", body: `{"regenerate":true}`,
			pveReply: accessTokenRegenerated(own),
			want:     sent(own, url.Values{"regenerate": {"1"}}, "regenerated", `"forced":true,"regenerate":true`, accessTokenSecret)},
		{name: "a forced expiry is recorded as sent and as forced",
			user: testAccessUserID, token: testAccessTokenID, query: "?force=true", body: `{"expire":1767225600}`,
			want: sent(own, url.Values{"expire": {"1767225600"}}, "updated", `"expire":1767225600,"forced":true,"regenerate":false`)},
		{name: "forcing privilege separation on is recorded as sent and as forced",
			user: testAccessUserID, token: testAccessTokenID, query: "?force=true", body: `{"privsep":true}`,
			want: sent(own, url.Values{"privsep": {"1"}}, "updated", `"forced":true,"privsep":true,"regenerate":false`)},
		{name: "every field, forced, records six keys and not the comment or the secret",
			user: testAccessUserID, token: testAccessTokenID, query: "?force=true",
			body:     `{"comment":"sentinel-comment","expire":1767225600,"privsep":true,"regenerate":true}`,
			pveReply: accessTokenRegenerated(own),
			want: sent(own, url.Values{"comment": {"sentinel-comment"}, "expire": {"1767225600"}, "privsep": {"1"}, "regenerate": {"1"}},
				"regenerated", `"expire":1767225600,"forced":true,"privsep":true,"regenerate":true`, accessTokenSecret)},

		// The updates the guard does not cover, on the same token: no force needed.
		{name: "an expiry of 0 needs no force and is recorded as 0",
			user: testAccessUserID, token: testAccessTokenID, body: `{"expire":0}`,
			want: sent(own, url.Values{"expire": {"0"}}, "updated", `"expire":0,"forced":false,"regenerate":false`)},
		{name: "turning privilege separation off needs no force and is recorded as false",
			user: testAccessUserID, token: testAccessTokenID, body: `{"privsep":false}`,
			want: sent(own, url.Values{"privsep": {"0"}}, "updated", `"forced":false,"privsep":false,"regenerate":false`)},
		{name: "a comment needs no force and is not recorded",
			user: testAccessUserID, token: testAccessTokenID, body: `{"comment":"sentinel-comment"}`,
			want: sent(own, url.Values{"comment": {"sentinel-comment"}}, "updated", `"forced":false,"regenerate":false`)},
		{name: "force on the updates the guard does not cover overrides nothing",
			user: testAccessUserID, token: testAccessTokenID, query: "?force=true",
			body: `{"comment":"sentinel-comment","expire":0,"privsep":false}`,
			want: sent(own, url.Values{"comment": {"sentinel-comment"}, "expire": {"0"}, "privsep": {"0"}},
				"updated", `"expire":0,"forced":false,"privsep":false,"regenerate":false`)},

		// Not the credential Nexara uses.
		{name: "another account's token is updated without force and the row names it",
			user: "alice%40pve", token: "ci", body: `{"expire":1767225600,"privsep":true}`,
			want: sent("alice@pve!ci", url.Values{"expire": {"1767225600"}, "privsep": {"1"}},
				"updated", `"expire":1767225600,"forced":false,"privsep":true,"regenerate":false`)},
		{name: "another token of the account Nexara uses is regenerated without force",
			user: testAccessUserID, token: "other", body: `{"regenerate":true}`,
			pveReply: accessTokenRegenerated("nexara@pve!other"),
			want:     sent("nexara@pve!other", url.Values{"regenerate": {"1"}}, "regenerated", `"forced":false,"regenerate":true`, accessTokenSecret)},
		{
			// force is recorded as sent, not as a verdict.
			name: "force on a guarded update of another account's token is recorded whether or not the guard would have refused it",
			user: "alice%40pve", token: "ci", query: "?force=true", body: `{"privsep":true}`,
			want: sent("alice@pve!ci", url.Values{"privsep": {"1"}}, "updated", `"forced":true,"privsep":true,"regenerate":false`),
		},
		{name: "an update Proxmox refuses answers with an error and writes no audit row",
			user: "alice%40pve", token: "ci", body: `{"comment":"sentinel-comment"}`,
			pveStatus: http.StatusInternalServerError, pveBody: `{"data":null,"message":"no such token 'ci' for user 'alice@pve'\n"}`,
			want: auditWant{method: http.MethodPut, path: pveTokenPath("alice@pve!ci"), form: url.Values{"comment": {"sentinel-comment"}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, pve, store := newAccessTokenApp(t)
			switch {
			case tt.pveStatus != 0:
				pve.refuse(tt.want.path, tt.pveStatus, tt.pveBody)
			case tt.pveReply != "":
				pve.reply(tt.want.path, tt.pveReply)
			}
			requireAudited(t, app, pve, store, accessTokenRequest(http.MethodPut, tt.user, tt.token, tt.query, tt.body), tt.want)
		})
	}
}
