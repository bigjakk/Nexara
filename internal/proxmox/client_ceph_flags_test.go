package proxmox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestReadCephStatus: /cluster/ceph/status is answered by the endpoint node
// alone, so its "binary not installed" says nothing of the others. The cluster
// is absent only when every member says so, or one says the shared config is
// missing; a member that cannot be asked, or a bare 404, is unknown.
func TestReadCephStatus(t *testing.T) {
	t.Parallel()
	const (
		ok           = `{"data":{"pgmap":{"num_pgs":1},"osdmap":{"num_up_osds":3}}}`
		notInstalled = `{"data":null,"message":"binary not installed: /usr/bin/ceph-mon\n"}`
		notInited    = `{"data":null,"message":"pveceph configuration not initialized - missing '/etc/pve/ceph.conf'\n"}`
	)
	type reply struct {
		code int
		body string
	}
	for _, tt := range []struct {
		name    string
		cluster reply
		members map[string]reply // pve-01 and pve-02's node route
		want    CephPresence
	}{
		{"the endpoint runs Ceph", reply{200, ok}, nil, CephPresent},
		{"the configuration is missing", reply{500, notInited}, nil, CephAbsent},
		{"a 404 is not an answer", reply{404, ""}, nil, CephPresenceUnknown},
		{"a compute-only endpoint, Ceph on a member", reply{500, notInstalled},
			map[string]reply{"pve-01": {500, notInstalled}, "pve-02": {200, ok}}, CephPresent},
		{"no member runs Ceph", reply{500, notInstalled},
			map[string]reply{"pve-01": {500, notInstalled}, "pve-02": {500, notInstalled}}, CephAbsent},
		{"a member that cannot be asked", reply{500, notInstalled},
			map[string]reply{"pve-01": {500, notInstalled}, "pve-02": {595, `{"data":null}`}}, CephPresenceUnknown},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				rep := reply{404, ""}
				switch r.URL.Path {
				case "/api2/json/cluster/ceph/status":
					rep = tt.cluster
				case "/api2/json/nodes":
					rep = reply{200, `{"data":[{"node":"pve-01","status":"online"},{"node":"pve-02","status":"online"}]}`}
				case "/api2/json/nodes/pve-01/ceph/status":
					rep = tt.members["pve-01"]
				case "/api2/json/nodes/pve-02/ceph/status":
					rep = tt.members["pve-02"]
				}
				w.WriteHeader(rep.code)
				_, _ = w.Write([]byte(rep.body))
			}))
			t.Cleanup(srv.Close)
			c, err := NewClient(ClientConfig{BaseURL: srv.URL, TokenID: "nexara@pve!api", TokenSecret: "secret"})
			if err != nil {
				t.Fatal(err)
			}
			status, got, err := c.ReadCephStatus(context.Background())
			if got != tt.want || (status != nil) != (got == CephPresent) || (status == nil) != (err != nil) {
				t.Errorf("ReadCephStatus = %v, %v, %v; want presence %v", status, got, err, tt.want)
			}
		})
	}
}
