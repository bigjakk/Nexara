package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The unit tests of accessTokenUpdateDetails and accessTokenUpdateAffectsAccess
// (internal/api/handlers/access_test.go) show what the builder and the decision
// make of a request. They pass with the builder deleted from the handler, and
// with an UpdateToken that never consults the decision. This file keeps the REAL
// PUT .../access/users/:userid/tokens/:tokenid declaration, its permission gate
// and the real AccessHandler.UpdateToken, on the stand-in Proxmox and database
// that registry_access_update_user_test.go builds, so a value can be followed from
// the request to the form Proxmox receives and the audit row every Viewer can
// read.

// newAccessTokenApp mounts the real PUT .../access/users/:userid/tokens/:tokenid
// declaration, with its real permission, carrying the real
// AccessHandler.UpdateToken wired to the two stand-ins in place of the one
// newRouteStubServer bound to an empty AccessHandler. The cluster it finds
// authenticates as accessUpdateSelf!api.
func newAccessTokenApp(t *testing.T) (*fiber.App, *accessUpdatePVE, *accessUpdateDB) {
	t.Helper()
	pve, store, h := newAccessStandIns(t)

	e := declaredEndpoint(t, fiber.MethodPut, accessScope+"/users/:userid/tokens/:tokenid")
	e.Handler = h.UpdateToken

	// stubAuth sets the user AuditLog needs — without one it writes nothing —
	// and grants the declared manage:access.
	return newRegistryApp(t, stubAuth(map[string]bool{"manage:" + handlers.AccessResource: true}), e), pve, store
}

// accessTokenRequest is the request a client sends about a token, given the two
// path segments as a client sends them: the user id percent-encoded. A request
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
// regeneration: findable in a row whatever key it is under, and obviously not a
// real one.
const accessTokenSecret = "PROBE-TOKEN-SECRET-NOT-A-REAL-VALUE"

// accessTokenRegenerated is what Proxmox answers the regeneration of the token
// fullID with: the new secret, once.
func accessTokenRegenerated(fullID string) string {
	return `{"data":{"full-tokenid":"` + fullID + `","value":"` + accessTokenSecret + `"}}`
}

// accessSend is send, returning the response body as well, for a test that reads
// more of it than the error envelope.
func accessSend(t *testing.T, app *fiber.App, req *http.Request) (int, ErrorResponse, string) {
	t.Helper()
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var env ErrorResponse
	_ = json.Unmarshal(raw, &env)
	return resp.StatusCode, env, string(raw)
}

// TestAccessUpdateTokenAuditRow follows a token update from the request, through
// the declaration and the handler, to the form Proxmox receives and the audit row.
//
// The row is what every Viewer reads (view:audit), so this is where the promise
// that it carries the account, the token, whether the secret was rotated, whether
// the request was sent with force on an update the guard covers, and the expire
// and privsep it set, and no comment and no secret, is kept against the code that
// writes it. Each case that reaches Proxmox asserts that what it sent got there,
// so that a field's absence from the row is an absence and not a request that
// never carried it.
//
// The guard is live in this harness, and the stand-in cluster's token is the
// account Nexara authenticates as, which is why most cases name it. The refused
// cases are the guarded updates without force: a regeneration, a non-zero expire
// and privsep=true, each answering 409 with the credential named, and reaching
// Proxmox not at all. Their forced twins go through, and are recorded as forced.
// The unguarded updates on the same token — an expire of 0, privsep=false, a
// comment — need no force and record forced=false even when they are sent with it,
// which is what a forced flag that is merely copied from the request would get
// wrong. A token of another account, and another token of the same account, are
// not the credential, so the same updates go through unforced.
//
// The cases that rotate the secret and reach Proxmox have it answer with one, and
// assert that it reaches the caller: the row's silence about it is then an
// absence, not a reply the handler never received.
//
// The last case has Proxmox refuse the update: the caller gets an error and
// nothing is audited, because nothing changed. The other cases' stand-in always
// succeeds, so without it a row written ahead of the Proxmox call would pass.
func TestAccessUpdateTokenAuditRow(t *testing.T) {
	const (
		own      = "nexara@pve!api"
		conflict = "token nexara@pve!api"
	)

	tests := []struct {
		name        string
		user, token string // as the client sends them in the path
		query       string
		body        string

		wantStatus int
		// wantIn are the fragments a refusal's message must carry.
		wantIn []string
		// wantBodyIn are the fragments the response of a successful update must
		// carry: the secret a regeneration returns to the operator.
		wantBodyIn []string
		// wantForm is the form the one Proxmox request carried. Nil marks a refused
		// update, which must reach Proxmox not at all and write no audit row;
		// wantAudit, wantID and wantAction are then unused.
		wantForm url.Values
		// wantAudit is the exact details of the one audit row, as stored.
		wantAudit string
		// wantID is the token the update names: the row's resource id, and the two
		// last segments of the Proxmox path.
		wantID string
		// wantAction is the row's action: "regenerated" for a rotation, else
		// "updated".
		wantAction string
		// pveReply, when set, is what the stand-in Proxmox answers the update with.
		pveReply string
		// pveStatus, when set, is the HTTP status the stand-in Proxmox refuses the
		// update with, and pveBody what it says. The update still reaches Proxmox,
		// and wantForm is what it carried. The caller must get an error, but which
		// one is the house mappers' to decide, so wantStatus is unused; and no audit
		// row may be written, so wantAudit is too.
		pveStatus int
		pveBody   string
	}{
		// The guarded updates of Nexara's own token, without force.
		{
			name: "regenerating the token Nexara uses is refused and leaves no trace",
			user: testAccessUserID, token: testAccessTokenID,
			body:       `{"regenerate":true}`,
			wantStatus: fiber.StatusConflict,
			wantIn:     []string{conflict, "force=true"},
		},
		{
			name: "giving it an expiry is refused and leaves no trace",
			user: testAccessUserID, token: testAccessTokenID,
			body:       `{"expire":1767225600}`,
			wantStatus: fiber.StatusConflict,
			wantIn:     []string{conflict, "force=true"},
		},
		{
			name: "turning its privilege separation on is refused and leaves no trace",
			user: testAccessUserID, token: testAccessTokenID,
			body:       `{"privsep":true}`,
			wantStatus: fiber.StatusConflict,
			wantIn:     []string{conflict, "force=true"},
		},
		{
			// One guarded field is enough, and the unguarded ones beside it do not
			// dilute it.
			name: "a guarded field beside unguarded ones is still refused",
			user: testAccessUserID, token: testAccessTokenID,
			body:       `{"comment":"sentinel-comment","expire":0,"privsep":true}`,
			wantStatus: fiber.StatusConflict,
			wantIn:     []string{conflict, "force=true"},
		},

		// The same updates, forced.
		{
			name: "a forced regeneration is recorded as forced and as a regeneration, without the secret",
			user: testAccessUserID, token: testAccessTokenID,
			query:      "?force=true",
			body:       `{"regenerate":true}`,
			wantStatus: fiber.StatusOK,
			wantForm:   url.Values{"regenerate": {"1"}},
			wantAudit:  `{"forced":true,"regenerate":true,"tokenid":"api","userid":"nexara@pve"}`,
			wantID:     own, wantAction: "regenerated",
			pveReply: accessTokenRegenerated(own), wantBodyIn: []string{accessTokenSecret},
		},
		{
			name: "a forced expiry is recorded as sent and as forced",
			user: testAccessUserID, token: testAccessTokenID,
			query:      "?force=true",
			body:       `{"expire":1767225600}`,
			wantStatus: fiber.StatusOK,
			wantForm:   url.Values{"expire": {"1767225600"}},
			wantAudit:  `{"expire":1767225600,"forced":true,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`,
			wantID:     own, wantAction: "updated",
		},
		{
			name: "forcing privilege separation on is recorded as sent and as forced",
			user: testAccessUserID, token: testAccessTokenID,
			query:      "?force=true",
			body:       `{"privsep":true}`,
			wantStatus: fiber.StatusOK,
			wantForm:   url.Values{"privsep": {"1"}},
			wantAudit:  `{"forced":true,"privsep":true,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`,
			wantID:     own, wantAction: "updated",
		},
		{
			name: "every field, forced, records six keys and not the comment or the secret",
			user: testAccessUserID, token: testAccessTokenID,
			query:      "?force=true",
			body:       `{"comment":"sentinel-comment","expire":1767225600,"privsep":true,"regenerate":true}`,
			wantStatus: fiber.StatusOK,
			wantForm: url.Values{
				"comment": {"sentinel-comment"}, "expire": {"1767225600"}, "privsep": {"1"}, "regenerate": {"1"},
			},
			wantAudit: `{"expire":1767225600,"forced":true,"privsep":true,"regenerate":true,"tokenid":"api","userid":"nexara@pve"}`,
			wantID:    own, wantAction: "regenerated",
			pveReply: accessTokenRegenerated(own), wantBodyIn: []string{accessTokenSecret},
		},

		// The updates the guard does not cover, on the same token: no force needed.
		{
			name: "an expiry of 0 needs no force and is recorded as 0",
			user: testAccessUserID, token: testAccessTokenID,
			body:       `{"expire":0}`,
			wantStatus: fiber.StatusOK,
			wantForm:   url.Values{"expire": {"0"}},
			wantAudit:  `{"expire":0,"forced":false,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`,
			wantID:     own, wantAction: "updated",
		},
		{
			name: "turning privilege separation off needs no force and is recorded as false",
			user: testAccessUserID, token: testAccessTokenID,
			body:       `{"privsep":false}`,
			wantStatus: fiber.StatusOK,
			wantForm:   url.Values{"privsep": {"0"}},
			wantAudit:  `{"forced":false,"privsep":false,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`,
			wantID:     own, wantAction: "updated",
		},
		{
			name: "a comment needs no force and is not recorded",
			user: testAccessUserID, token: testAccessTokenID,
			body:       `{"comment":"sentinel-comment"}`,
			wantStatus: fiber.StatusOK,
			wantForm:   url.Values{"comment": {"sentinel-comment"}},
			wantAudit:  `{"forced":false,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`,
			wantID:     own, wantAction: "updated",
		},
		{
			name: "force on the updates the guard does not cover overrides nothing",
			user: testAccessUserID, token: testAccessTokenID,
			query:      "?force=true",
			body:       `{"comment":"sentinel-comment","expire":0,"privsep":false}`,
			wantStatus: fiber.StatusOK,
			wantForm: url.Values{
				"comment": {"sentinel-comment"}, "expire": {"0"}, "privsep": {"0"},
			},
			wantAudit: `{"expire":0,"forced":false,"privsep":false,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`,
			wantID:    own, wantAction: "updated",
		},

		// Not the credential Nexara uses.
		{
			name: "another account's token is updated without force and the row names it",
			user: "alice%40pve", token: "ci",
			body:       `{"expire":1767225600,"privsep":true}`,
			wantStatus: fiber.StatusOK,
			wantForm:   url.Values{"expire": {"1767225600"}, "privsep": {"1"}},
			wantAudit:  `{"expire":1767225600,"forced":false,"privsep":true,"regenerate":false,"tokenid":"ci","userid":"alice@pve"}`,
			wantID:     "alice@pve!ci", wantAction: "updated",
		},
		{
			name: "another token of the account Nexara uses is regenerated without force",
			user: testAccessUserID, token: "other",
			body:       `{"regenerate":true}`,
			wantStatus: fiber.StatusOK,
			wantForm:   url.Values{"regenerate": {"1"}},
			wantAudit:  `{"forced":false,"regenerate":true,"tokenid":"other","userid":"nexara@pve"}`,
			wantID:     "nexara@pve!other", wantAction: "regenerated",
			pveReply: accessTokenRegenerated("nexara@pve!other"), wantBodyIn: []string{accessTokenSecret},
		},
		{
			// force is recorded as sent, not as a verdict: this is not the token
			// Nexara uses, the guard would never have refused the update, and the
			// row still says forced.
			name: "force on a guarded update of another account's token is recorded whether or not the guard would have refused it",
			user: "alice%40pve", token: "ci",
			query:      "?force=true",
			body:       `{"privsep":true}`,
			wantStatus: fiber.StatusOK,
			wantForm:   url.Values{"privsep": {"1"}},
			wantAudit:  `{"forced":true,"privsep":true,"regenerate":false,"tokenid":"ci","userid":"alice@pve"}`,
			wantID:     "alice@pve!ci", wantAction: "updated",
		},
		{
			// Proxmox says no, as it does for a token that does not exist, so
			// nothing was updated and nothing may be recorded as if it had been.
			name: "an update Proxmox refuses answers with an error and writes no audit row",
			user: "alice%40pve", token: "ci",
			body:      `{"comment":"sentinel-comment"}`,
			pveStatus: http.StatusInternalServerError,
			pveBody:   `{"data":null,"message":"no such token 'ci' for user 'alice@pve'\n"}`,
			wantForm:  url.Values{"comment": {"sentinel-comment"}},
			wantID:    "alice@pve!ci",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, pve, store := newAccessTokenApp(t)
			wantUser, wantToken, _ := strings.Cut(tt.wantID, "!")
			pvePath := "/api2/json/access/users/" + wantUser + "/token/" + wantToken
			switch {
			case tt.pveStatus != 0:
				pve.refuse(pvePath, tt.pveStatus, tt.pveBody)
			case tt.pveReply != "":
				pve.reply(pvePath, tt.pveReply)
			}

			status, env, body := accessSend(t, app, accessTokenRequest(http.MethodPut, tt.user, tt.token, tt.query, tt.body))
			if tt.pveStatus != 0 {
				if status < fiber.StatusBadRequest {
					t.Fatalf("Proxmox refused the update and the caller got %d (%q), want an error", status, env.Message)
				}
			} else if status != tt.wantStatus {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, tt.wantStatus)
			}

			sent, rows := pve.requests(), store.auditRows()
			for _, fragment := range tt.wantIn {
				if !strings.Contains(env.Message, fragment) {
					t.Errorf("the refusal %q does not carry %q", env.Message, fragment)
				}
			}
			// The secret reached the caller, so that its absence from the row is an
			// absence and not a reply the handler never received.
			for _, fragment := range tt.wantBodyIn {
				if !strings.Contains(body, fragment) {
					t.Errorf("the response %s does not carry %q", body, fragment)
				}
			}
			if tt.wantForm == nil {
				if len(sent) != 0 {
					t.Errorf("the refused update reached Proxmox: %+v", sent)
				}
				if len(rows) != 0 {
					t.Errorf("the refused update wrote %d audit row(s), want none", len(rows))
				}
				return
			}

			// What Proxmox received is established first: it is what makes the
			// row's silence about a field mean something.
			if len(sent) != 1 {
				t.Fatalf("the handler made %d Proxmox calls, want exactly 1: %+v", len(sent), sent)
			}
			if sent[0].method != http.MethodPut || sent[0].path != pvePath {
				t.Fatalf("the handler sent %s %s, want PUT %s", sent[0].method, sent[0].path, pvePath)
			}
			if !reflect.DeepEqual(sent[0].form, tt.wantForm) {
				t.Errorf("Proxmox received the form %v, want %v", sent[0].form, tt.wantForm)
			}

			if tt.pveStatus != 0 {
				if len(rows) != 0 {
					t.Errorf("Proxmox refused the update and the handler wrote %d audit row(s), want none", len(rows))
				}
				return
			}
			row := oneAccessAuditRow(t, rows)
			if row.resourceType != "pve_token" || row.resourceID != tt.wantID || row.action != tt.wantAction {
				t.Errorf("the audit row is (%q, %q, %q), want (pve_token, %q, %s)",
					row.resourceType, row.resourceID, row.action, tt.wantID, tt.wantAction)
			}
			if string(row.details) != tt.wantAudit {
				t.Errorf("the audit details are %s, want %s", row.details, tt.wantAudit)
			}
			// The exact comparison above already excludes these; naming them says
			// why, and keeps the promise from resting on a wantAudit that was
			// written wrong.
			for _, never := range []string{"sentinel-comment", accessTokenSecret} {
				if strings.Contains(string(row.details), never) {
					t.Errorf("the audit details %s carry %q — audit rows are readable by every Viewer", row.details, never)
				}
			}
		})
	}
}
