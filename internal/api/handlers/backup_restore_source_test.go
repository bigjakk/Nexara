package handlers

import (
	"errors"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

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
	)
	server := db.PbsServer{Name: "pbs01", ApiUrl: "https://" + host + ":8007", TlsFingerprint: fp}
	pbs := func(name, srv, datastore string, edit ...func(*proxmox.StorageConfig)) proxmox.StorageConfig {
		cfg := proxmox.StorageConfig{Storage: name, Type: "pbs", Server: srv, Datastore: datastore}
		for _, e := range edit {
			e(&cfg)
		}
		return cfg
	}
	with := func(f func(*proxmox.StorageConfig)) func(*proxmox.StorageConfig) { return f }

	tests := []struct {
		name      string
		server    db.PbsServer // zero: the server above
		cfgs      []proxmox.StorageConfig
		datastore string
		want      string
		status    int
		msg       []string
	}{
		{name: "the storage on the server's host, not the first pbs storage",
			cfgs:      []proxmox.StorageConfig{pbs("store01", "pbs02.example.com", "datastore01"), pbs("store02", host, "datastore01")},
			datastore: "datastore01", want: "store02"},
		{name: "host compared without case",
			cfgs: []proxmox.StorageConfig{pbs("store02", "PBS01.Example.COM", "datastore01")}, datastore: "datastore01", want: "store02"},
		{name: "fingerprint finds a server the storage reaches by another address, however it is written",
			cfgs: []proxmox.StorageConfig{pbs("store02", "192.0.2.10", "datastore01", with(func(c *proxmox.StorageConfig) {
				c.FingerprintPBS = strings.ToLower(strings.ReplaceAll(fp, ":", ""))
			}))}, datastore: "datastore01", want: "store02"},
		{name: "an IPv6 server, bracketed in the URL and bare in the storage as Proxmox keeps it",
			server: db.PbsServer{Name: "pbs01", ApiUrl: "https://[2001:db8::10]:8007"},
			cfgs:   []proxmox.StorageConfig{pbs("store02", "2001:db8::10", "datastore01")}, datastore: "datastore01", want: "store02"},
		{name: "no fingerprint on either side is not a match between two different servers",
			server: db.PbsServer{Name: "pbs01", ApiUrl: "https://" + host + ":8007"},
			cfgs:   []proxmox.StorageConfig{pbs("store01", "pbs02.example.com", "datastore01")}, datastore: "datastore01",
			status: fiber.StatusBadRequest, msg: []string{`"datastore01" of PBS server "pbs01"`, "store01"}},
		{name: "the datastore picks between the storages of one server",
			cfgs: []proxmox.StorageConfig{pbs("store02", host, "datastore01"), pbs("store03", host, "datastore02")}, datastore: "datastore02", want: "store03"},
		{name: "no datastore named and two on the server is ambiguous",
			cfgs:   []proxmox.StorageConfig{pbs("store02", host, "datastore01"), pbs("store03", host, "datastore02")},
			status: fiber.StatusConflict, msg: []string{"store02, store03", "name the datastore"}},
		{name: "two storages for the same datastore is ambiguous",
			cfgs:      []proxmox.StorageConfig{pbs("store05", host, "datastore01"), pbs("store02", host, "datastore01")},
			datastore: "datastore01", status: fiber.StatusConflict, msg: []string{"store02, store05", "remove the duplicate"}},
		{name: "a namespaced storage cannot hold a root-namespace snapshot",
			cfgs:      []proxmox.StorageConfig{pbs("store02", host, "datastore01", with(func(c *proxmox.StorageConfig) { c.Namespace = "tenant" }))},
			datastore: "datastore01", status: fiber.StatusBadRequest, msg: []string{"root namespace"}},
		{name: "a disabled storage is skipped",
			cfgs:      []proxmox.StorageConfig{pbs("store02", host, "datastore01", with(func(c *proxmox.StorageConfig) { c.Disable = 1 }))},
			datastore: "datastore01", status: fiber.StatusBadRequest},
		{name: "a storage on other nodes is skipped, one listing the node is used",
			cfgs: []proxmox.StorageConfig{
				pbs("store02", host, "datastore01", with(func(c *proxmox.StorageConfig) { c.Nodes = "pve-02" })),
				pbs("store03", host, "datastore01", with(func(c *proxmox.StorageConfig) { c.Nodes = "pve-03, pve-01" })),
			}, datastore: "datastore01", want: "store03"},
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
			got, err := restoreStorage(tt.cfgs, srv, tt.datastore, "pve-01")
			if tt.status == 0 {
				if err != nil || got != tt.want {
					t.Fatalf("restoreStorage = %q, %v; want %q", got, err, tt.want)
				}
				return
			}
			var fe *fiber.Error
			if !errors.As(err, &fe) || fe.Code != tt.status {
				t.Fatalf("restoreStorage = %q, %v; want a %d refusal", got, err, tt.status)
			}
			for _, m := range tt.msg {
				if !strings.Contains(fe.Message, m) {
					t.Errorf("refusal %q lacks %q", fe.Message, m)
				}
			}
		})
	}
}
