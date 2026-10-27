package handlers

import (
	"errors"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// restoreStorage is the one place a restore decides which Proxmox storage reads
// the snapshot; the route test in internal/api proves the handler uses it.
func TestRestoreStorage(t *testing.T) {
	t.Parallel()

	const (
		host = "pbs01.example.com"
		fp   = "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"
		fp2  = "99:88:77:66:55:44:33:22:11:00:FF:EE:DD:CC:BB:AA:99:88:77:66:55:44:33:22:11:00:FF:EE:DD:CC:BB:AA"
	)
	server := db.PbsServer{ID: uuid.New(), Name: "pbs01", ApiUrl: "https://" + host + ":8007", TlsFingerprint: fp}
	// A second Nexara PBS server, which a storage can belong to instead.
	pbs02 := db.PbsServer{ID: uuid.New(), Name: "pbs02", ApiUrl: "https://pbs02.example.com:8007"}
	pbs := func(name, srv, datastore string, edit ...func(*proxmox.StorageConfig)) proxmox.StorageConfig {
		cfg := proxmox.StorageConfig{Storage: name, Type: "pbs", Server: srv, Datastore: datastore}
		for _, e := range edit {
			e(&cfg)
		}
		return cfg
	}
	with := func(f func(*proxmox.StorageConfig)) func(*proxmox.StorageConfig) { return f }
	other := func(addr string) db.PbsServer {
		return db.PbsServer{ID: uuid.New(), Name: "pbs03", ApiUrl: "https://" + addr + ":8007"}
	}

	tests := []struct {
		name   string
		server db.PbsServer // zero: the server above
		others []db.PbsServer
		cfgs   []proxmox.StorageConfig
		want   string // the storage chosen, with how it was tied
		by     string
		status int // the refusal, when there is one
		msg    []string
	}{
		{name: "the storage on the server's host, not the first pbs storage",
			cfgs: []proxmox.StorageConfig{pbs("store01", "pbs02.example.com", "datastore01"), pbs("store02", host, "datastore01")},
			want: "store02", by: "address"},
		{name: "host compared without case",
			cfgs: []proxmox.StorageConfig{pbs("store02", "PBS01.Example.COM", "datastore01")}, want: "store02", by: "address"},
		{name: "a port written out on both sides, not the default",
			server: db.PbsServer{Name: "pbs01", ApiUrl: "https://" + host + ":8443"},
			cfgs:   []proxmox.StorageConfig{pbs("store02", host, "datastore01", with(func(c *proxmox.StorageConfig) { c.Port = 8443 }))}, want: "store02", by: "address"},
		{name: "a URL with no port is the default port, like a storage with none",
			server: db.PbsServer{Name: "pbs01", ApiUrl: "https://" + host},
			cfgs:   []proxmox.StorageConfig{pbs("store02", host, "datastore01")}, want: "store02", by: "address"},
		{name: "fingerprint finds a server the storage reaches by another address, however it is written",
			cfgs: []proxmox.StorageConfig{pbs("store02", "192.0.2.10", "datastore01", with(func(c *proxmox.StorageConfig) {
				c.FingerprintPBS = strings.ToLower(strings.ReplaceAll(fp, ":", ""))
			}))}, want: "store02", by: "fingerprint"},
		{name: "an IPv6 server, bracketed in the URL and bare in the storage as Proxmox keeps it",
			server: db.PbsServer{Name: "pbs01", ApiUrl: "https://[2001:db8::10]:8007"},
			cfgs:   []proxmox.StorageConfig{pbs("store02", "2001:db8::10", "datastore01")}, want: "store02", by: "address"},
		{name: "different fingerprints exclude a storage whose host matches",
			cfgs:   []proxmox.StorageConfig{pbs("store02", host, "datastore01", with(func(c *proxmox.StorageConfig) { c.FingerprintPBS = fp2 }))},
			status: fiber.StatusBadRequest, msg: []string{`datastore "datastore01" of PBS server "pbs01"`, "store02"}},
		{name: "the same host on another port is another server",
			server: db.PbsServer{Name: "pbs01", ApiUrl: "https://" + host + ":8007"},
			cfgs:   []proxmox.StorageConfig{pbs("store02", host, "datastore01", with(func(c *proxmox.StorageConfig) { c.Port = 8008 }))},
			status: fiber.StatusBadRequest},
		{name: "an address in Nexara and a name in Proxmox, with no fingerprint to say: the only candidate is used",
			server: db.PbsServer{Name: "pbs01", ApiUrl: "https://192.0.2.10:8007"},
			cfgs:   []proxmox.StorageConfig{pbs("store02", host, "datastore01")}, want: "store02", by: "only_candidate"},
		{name: "the same, but another Nexara PBS server is tied to that storage",
			server: db.PbsServer{ID: uuid.New(), Name: "pbs01", ApiUrl: "https://192.0.2.10:8007"}, others: []db.PbsServer{pbs02},
			cfgs:   []proxmox.StorageConfig{pbs("store02", "pbs02.example.com", "datastore01")},
			status: fiber.StatusBadRequest},
		{name: "the same, but the claim is the other server's fingerprint",
			server: db.PbsServer{ID: uuid.New(), Name: "pbs01", ApiUrl: "https://192.0.2.10:8007"},
			others: []db.PbsServer{{ID: uuid.New(), Name: "pbs02", ApiUrl: "https://192.0.2.20:8007", TlsFingerprint: fp2}},
			cfgs: []proxmox.StorageConfig{pbs("store02", host, "datastore01", with(func(c *proxmox.StorageConfig) {
				c.FingerprintPBS = fp2
			}))},
			status: fiber.StatusBadRequest},
		{name: "two candidates nothing ties to the server are not a choice",
			server: db.PbsServer{Name: "pbs01", ApiUrl: "https://192.0.2.10:8007"},
			cfgs:   []proxmox.StorageConfig{pbs("store01", "pbs02.example.com", "datastore01"), pbs("store02", host, "datastore01")},
			status: fiber.StatusBadRequest},
		{name: "a contradicted candidate does not count against the only one left",
			server: db.PbsServer{Name: "pbs01", ApiUrl: "https://192.0.2.10:8007"},
			cfgs: []proxmox.StorageConfig{
				pbs("store01", "192.0.2.10", "datastore01", with(func(c *proxmox.StorageConfig) { c.Port = 8008 })),
				pbs("store02", host, "datastore01")},
			want: "store02", by: "only_candidate"},
		{name: "the datastore picks between the storages of one server",
			cfgs: []proxmox.StorageConfig{pbs("store02", host, "datastore02"), pbs("store03", host, "datastore01")}, want: "store03", by: "address"},
		{name: "two storages for the same datastore is ambiguous",
			cfgs:   []proxmox.StorageConfig{pbs("store05", host, "datastore01"), pbs("store02", host, "datastore01")},
			status: fiber.StatusConflict, msg: []string{"store02, store05", "remove the duplicate"}},
		{name: "a namespaced storage cannot hold a root-namespace snapshot",
			cfgs:   []proxmox.StorageConfig{pbs("store02", host, "datastore01", with(func(c *proxmox.StorageConfig) { c.Namespace = "tenant" }))},
			status: fiber.StatusBadRequest, msg: []string{"root namespace"}},
		{name: "a disabled storage is skipped",
			cfgs:   []proxmox.StorageConfig{pbs("store02", host, "datastore01", with(func(c *proxmox.StorageConfig) { c.Disable = 1 }))},
			status: fiber.StatusBadRequest},
		{name: "a storage on other nodes is skipped, one listing the node is used",
			cfgs: []proxmox.StorageConfig{
				pbs("store02", host, "datastore01", with(func(c *proxmox.StorageConfig) { c.Nodes = "pve-02" })),
				pbs("store03", host, "datastore01", with(func(c *proxmox.StorageConfig) { c.Nodes = "pve-03, pve-01" })),
			}, want: "store03", by: "address"},
		{name: "no pbs storage at all, though another type sits on the host",
			cfgs:   []proxmox.StorageConfig{{Storage: "store06", Type: "nfs", Server: host}},
			status: fiber.StatusBadRequest, msg: []string{"No PBS storage configured"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := server
			if tt.server.Name != "" {
				srv = tt.server
			}
			if len(tt.others) == 0 {
				tt.others = []db.PbsServer{other("192.0.2.99")}
			}
			got, by, err := restoreStorage(tt.cfgs, srv, tt.others, "datastore01", "pve-01")
			if tt.status == 0 {
				if err != nil || got != tt.want || by != tt.by {
					t.Fatalf("restoreStorage = %q by %q, %v; want %q by %q", got, by, err, tt.want, tt.by)
				}
				return
			}
			var fe *fiber.Error
			if !errors.As(err, &fe) || fe.Code != tt.status {
				t.Fatalf("restoreStorage = %q by %q, %v; want a %d refusal", got, by, err, tt.status)
			}
			for _, m := range tt.msg {
				if !strings.Contains(fe.Message, m) {
					t.Errorf("refusal %q lacks %q", fe.Message, m)
				}
			}
		})
	}
}
