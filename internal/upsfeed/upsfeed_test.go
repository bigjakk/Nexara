package upsfeed

import (
	"net/netip"
	"testing"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

func TestNodes(t *testing.T) {
	t.Parallel()
	a, b := uuid.New(), uuid.New()
	nodes := NewNodes([]db.ListNodeLoginAddressesRow{
		{ClusterID: a, NodeName: "pve-01", Address: "192.0.2.11"},    // its cluster address
		{ClusterID: a, NodeName: "pve-01", Address: "198.51.100.11"}, // an interface
		{ClusterID: a, NodeName: "pve-02", Address: "2001:db8::12"},  // a cluster address over IPv6
		{ClusterID: a, NodeName: "pve-02", Address: "192.0.2.50"},    // shared with
		{ClusterID: b, NodeName: "pve-03", Address: "192.0.2.50"},    // a node of another cluster
		{ClusterID: b, NodeName: "pve-01", Address: "192.0.2.21"},    // the same name, another cluster
		{ClusterID: a, NodeName: "pve-04", Address: "pve-04.example.com"},
		{ClusterID: a, NodeName: "pve-04", Address: "127.0.1.1"}, // Debian's hostname address
	})
	pve01, pve02, otherPve01 := Node{a, "pve-01"}, Node{a, "pve-02"}, Node{b, "pve-01"}
	for _, tc := range []struct {
		login string
		host  *Node
		want  *Node
	}{
		{"192.0.2.11", nil, &pve01},
		{"198.51.100.11", nil, &pve01},
		{"::ffff:192.0.2.11", nil, &pve01},
		{"2001:db8::12", nil, &pve02},
		{"192.0.2.21", nil, &otherPve01},
		{"192.0.2.50", nil, nil}, // shared: names neither
		{"192.0.2.99", nil, nil},
		{"127.0.0.1", &pve01, &pve01}, // the NUT host's own upsmon
		{"::1", &pve01, &pve01},
		{"127.0.0.1", nil, nil}, // and no host known
	} {
		got, ok := nodes.Login(netip.MustParseAddr(tc.login), tc.host)
		if ok != (tc.want != nil) || ok && got != *tc.want {
			t.Errorf("Login(%s, host %v) = %v, %v; want %v", tc.login, tc.host, got, ok, tc.want)
		}
	}
	for _, tc := range []struct {
		remote netip.Addr
		want   *Node
	}{
		{netip.MustParseAddr("192.0.2.11"), &pve01},
		{netip.MustParseAddr("192.0.2.50"), nil},
		{netip.MustParseAddr("127.0.1.1"), nil}, // a loopback address names no node
		{netip.Addr{}, nil},
	} {
		got, ok := nodes.Host(tc.remote)
		if ok != (tc.want != nil) || ok && got != *tc.want {
			t.Errorf("Host(%v) = %v, %v; want %v", tc.remote, got, ok, tc.want)
		}
	}
}

func TestJudge(t *testing.T) {
	t.Parallel()
	c := uuid.New()
	pve01, pve02, pve03, pve05 := Node{c, "pve-01"}, Node{c, "pve-02"}, Node{c, "pve-03"}, Node{c, "pve-05"}
	nodes := NewNodes([]db.ListNodeLoginAddressesRow{
		{ClusterID: c, NodeName: "pve-01", Address: "192.0.2.11"},
		{ClusterID: c, NodeName: "pve-02", Address: "192.0.2.12"},
		{ClusterID: c, NodeName: "pve-03", Address: "192.0.2.50"}, // pve-03's only address
		{ClusterID: c, NodeName: "pve-04", Address: "192.0.2.50"}, //   is shared
	})
	login := func(addr string, missed int) Login { return Login{netip.MustParseAddr(addr), missed} }
	for _, tc := range []struct {
		name      string
		node      Node
		elsewhere bool
		status    string
		listed    bool
		logins    []Login
		host      *Node
		want      Verdict
		addr      string
	}{
		{"logged in", pve01, false, "online", true, []Login{login("192.0.2.11", 0)}, nil, Protected, "192.0.2.11"},
		{"one listing missed", pve01, false, "online", true, []Login{login("192.0.2.11", 1)}, nil, Protected, "192.0.2.11"},
		{"two in a row missed", pve01, false, "online", true, []Login{login("192.0.2.11", 2)}, nil, NotProtected, ""},
		{"someone else logged in", pve02, false, "online", true, []Login{login("192.0.2.11", 0)}, nil, NotProtected, ""},
		{"nobody logged in", pve01, false, "online", true, nil, nil, NotProtected, ""},
		{"the NUT host over loopback", pve01, false, "online", true, []Login{login("127.0.0.1", 0)}, &pve01, Protected, "127.0.0.1"},
		{"another node's loopback", pve02, false, "online", true, []Login{login("127.0.0.1", 0)}, &pve01, NotProtected, ""},
		{"a host Nexara has no address for", pve05, false, "online", true, []Login{login("::1", 0)}, &pve05, Protected, "::1"},
		{"protected another way", pve02, true, "offline", true, nil, nil, Elsewhere, ""},
		{"offline", pve02, false, "offline", true, nil, nil, Offline, ""},
		{"status unknown", pve02, false, "unknown", true, nil, nil, Unchecked, ""},
		{"no such node", pve02, false, "", true, nil, nil, Unchecked, ""},
		{"never listed", pve02, false, "online", false, nil, nil, Unchecked, ""},
		{"only a shared address", pve03, false, "online", true, []Login{login("192.0.2.50", 0)}, nil, Unchecked, ""},
	} {
		got, addr := Judge(tc.node, tc.elsewhere, tc.status, tc.listed, tc.logins, nodes, tc.host)
		wantAddr := netip.Addr{}
		if tc.addr != "" {
			wantAddr = netip.MustParseAddr(tc.addr)
		}
		if got != tc.want || addr != wantAddr {
			t.Errorf("%s: Judge = %s, %v; want %s, %v", tc.name, got, addr, tc.want, wantAddr)
		}
	}
}
