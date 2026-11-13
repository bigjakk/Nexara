package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// A create naming a registered PBS server hands that server's token to
// Proxmox, so it may only send it to the server's own address, through the
// cluster it is attached to while that cluster keeps the URL the token was
// paired with, and only for a caller who may manage the server; one who may
// not view it is told it does not exist. The token reaches Proxmox at the URL
// the pairing checked, never through the client cache, whose failover could
// pick another member's address. The address and fingerprint mapping is
// TestPBSConnectionSettings's, the pairing TestPBSServerTokenPairing's.
func TestStorageCreateFromARegisteredPBSServer(t *testing.T) {
	t.Parallel()

	const secret = "CANARY-pbs-token-secret"
	encrypted, err := crypto.Encrypt(secret, backupRunEncKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	clusterSecret, err := crypto.Encrypt("token-secret-value", backupRunEncKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// attached is the server as a cluster's, its token paired with url.
	attached := func(clusterID, url string) db.PbsServer {
		return db.PbsServer{
			ID:                   uuid.MustParse(testPBSServerID),
			Name:                 "pbs01",
			ApiUrl:               "https://pbs01.example.com:8443",
			TokenID:              "backup@pbs!test",
			TokenSecretEncrypted: encrypted,
			ClusterID:            pgtype.UUID{Bytes: uuid.MustParse(clusterID), Valid: true},
			TlsFingerprint:       strings.Repeat("ab", 32),
			TokenClusterApiUrl:   url,
		}
	}
	// Each row's server, given the URL the cluster row has.
	here := func(url string) db.PbsServer { return attached(testClusterID, url) }
	elsewhere := func(url string) db.PbsServer { return attached("5c4b3a29-1817-4615-9413-000000000098", url) }
	// Its URL moved, as a made-up cluster secret lets anyone with manage:cluster move it.
	moved := func(string) db.PbsServer { return attached(testClusterID, "https://pve-02.example.com:8006") }

	body := func(storageType, params string) string {
		return `{"storage":"datastore01","type":"` + storageType + `","pbs_server_id":"` + testPBSServerID +
			`","params":{` + params + `}}`
	}
	ds := body("pbs", `"datastore":"datastore01"`)
	all := grantsOf("manage:storage", "view:pbs", "manage:pbs")

	tests := []struct {
		name     string
		grants   map[string]bool
		server   func(url string) db.PbsServer
		body     string
		status   int
		lookedUp bool // whether the server's row was read
	}{
		{"the server's address, port, pin and token, at the cluster row's URL", all, here, ds, 201, true},
		{"no manage:storage here: the server is not even looked up", grantsOf("view:pbs", "manage:pbs"), here, ds, 403, false},
		{"a server the caller may not view is a missing one", grantsOf("manage:storage"), here, ds, 404, true},
		{"a server the caller may view but not manage", grantsOf("manage:storage", "view:pbs"), here, ds, 403, true},
		{"a server attached to another cluster", all, elsewhere, ds, 400, true},
		{"this cluster's URL is not the one the token was paired with", all, moved, ds, 409, true},
		{"params that would send the token elsewhere", all, here,
			body("pbs", `"datastore":"datastore01","server":"elsewhere.example.com"`), 400, false},
		{"a storage that is not pbs", all, here, body("dir", `"path":"/mnt/store01"`), 400, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var sent, cached []url.Values
			record := func(into *[]url.Values, w http.ResponseWriter, r *http.Request) bool {
				if r.Method != http.MethodPost || r.URL.Path != "/api2/json/storage" {
					return false
				}
				raw, _ := io.ReadAll(r.Body)
				form, _ := url.ParseQuery(string(raw))
				mu.Lock()
				*into = append(*into, form)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"storage":"datastore01","type":"pbs"}}`))
				return true
			}
			// The cluster row's URL, the one place the token may go.
			rowPVE := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !record(&sent, w, r) {
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(rowPVE.Close)
			// The cache's client answers elsewhere, as a failover member would.
			pve := &backupRunPVE{handle: func(w http.ResponseWriter, r *http.Request) bool {
				return record(&cached, w, r)
			}}
			app, dbtx := newPVEAppAs(t, pve, tt.grants, fiber.MethodPost, storageScope,
				func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
					return handlers.NewStorageHandler(q, backupRunEncKey, nil).Create
				})
			dbtx.lookups = map[string]any{
				"GetPBSServer": tt.server(rowPVE.URL),
				"GetCluster": db.Cluster{ID: uuid.MustParse(testClusterID), Name: "cluster01", ApiUrl: rowPVE.URL,
					TokenID: "user@pam!test", TokenSecretEncrypted: clusterSecret},
			}

			req := httptest.NewRequest(http.MethodPost, storageRoute(storageScope), strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Test-User", "yes")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			answer, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()

			if resp.StatusCode != tt.status {
				t.Fatalf("status = %d (%s), want %d", resp.StatusCode, answer, tt.status)
			}
			if got := slices.Contains(dbtx.asked, "GetPBSServer"); got != tt.lookedUp {
				t.Errorf("the server's row was read: %v, want %v", got, tt.lookedUp)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(cached) != 0 {
				t.Errorf("the token went through the client cache's endpoint %d time(s)", len(cached))
			}
			audits := dbtx.decodedAudits(t)
			if tt.status != 201 {
				if len(sent) != 0 || len(audits) != 0 {
					t.Errorf("a refused create reached Proxmox %d time(s) and wrote %d audit row(s)", len(sent), len(audits))
				}
				return
			}

			if len(sent) != 1 {
				t.Fatalf("Proxmox received %d creates, want 1", len(sent))
			}
			want := url.Values{
				"storage": {"datastore01"}, "type": {"pbs"}, "datastore": {"datastore01"},
				"server": {"pbs01.example.com"}, "port": {"8443"},
				"username": {"backup@pbs!test"}, "password": {secret},
				"fingerprint": {strings.TrimSuffix(strings.Repeat("AB:", 32), ":")},
			}
			if got := sent[0]; got.Encode() != want.Encode() {
				t.Errorf("Proxmox received %v, want %v", got, want)
			}
			if len(audits) != 1 || audits[0].details["pbs_server_id"] != testPBSServerID {
				t.Fatalf("audit rows %+v, want one naming the server", audits)
			}
			if strings.Contains(audits[0].raw, secret) || strings.Contains(string(answer), secret) {
				t.Errorf("the token's secret reached the audit row or the answer: %s / %s", audits[0].raw, answer)
			}
		})
	}
}

// A PBS server's token is paired with its attached cluster's API URL when its
// secret is entered, kept by an edit that leaves the token, and broken by a
// move to another cluster without it; a standalone server pairs with nothing.
func TestPBSServerTokenPairing(t *testing.T) {
	t.Parallel()

	const clusterURL = "https://pve-01.example.com:8006"
	encrypted, err := crypto.Encrypt("CANARY-pbs-token-secret", backupRunEncKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	existing := db.PbsServer{
		ID:                   uuid.MustParse(testPBSServerID),
		Name:                 "pbs01",
		ApiUrl:               "https://192.0.2.10:8007",
		TokenID:              "backup@pbs!test",
		TokenSecretEncrypted: encrypted,
		ClusterID:            pgtype.UUID{Bytes: uuid.MustParse(testClusterID), Valid: true},
		TokenClusterApiUrl:   "https://pve-old.example.com:8006",
	}
	create := func(cluster string) string {
		return `{"name":"pbs01","api_url":"https://192.0.2.10:8007","token_id":"backup@pbs!test",` +
			`"token_secret":"s","allow_private_address":true` + cluster + `}`
	}
	tests := []struct {
		name, method, body string
		want               string // the pairing written
	}{
		{"registered on a cluster: its URL", fiber.MethodPost, create(`,"cluster_id":"` + testClusterID + `"`), clusterURL},
		{"registered standalone: nothing", fiber.MethodPost, create(""), ""},
		{"an edit that leaves the token keeps it", fiber.MethodPut, `{"name":"renamed"}`, existing.TokenClusterApiUrl},
		{"the token entered again: the cluster's URL now", fiber.MethodPut, `{"token_secret":"new"}`, clusterURL},
		{"moved without the token: broken", fiber.MethodPut,
			`{"cluster_id":"5c4b3a29-1817-4615-9413-000000000098"}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path, query, column := pbsScope, "CreatePBSServer", 6
			if tt.method == fiber.MethodPut {
				path, query, column = pbsScope+"/:id", "UpdatePBSServer", 7
			}
			app, dbtx := newPVEAppAs(t, &backupRunPVE{}, grantsOf("manage:pbs"), tt.method, path,
				func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
					h := handlers.NewPBSHandler(q, backupRunEncKey, nil)
					if tt.method == fiber.MethodPost {
						return h.Create
					}
					return h.Update
				})
			dbtx.lookups = map[string]any{
				"GetPBSServer":    existing,
				"GetCluster":      db.Cluster{ID: uuid.MustParse(testClusterID), ApiUrl: clusterURL},
				"CreatePBSServer": existing,
				"UpdatePBSServer": existing,
			}
			req := httptest.NewRequest(tt.method, pbsRoute(path), strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Test-User", "yes")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			answer, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode >= 300 {
				t.Fatalf("status = %d (%s)", resp.StatusCode, answer)
			}
			args := dbtx.askedWith[query]
			if len(args) <= column {
				t.Fatalf("%s ran with %d arguments", query, len(args))
			}
			if got := args[column]; got != tt.want {
				t.Errorf("token_cluster_api_url = %q, want %q", got, tt.want)
			}
		})
	}
}
