package proxmox

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
)

// Ceph pool create and delete spent their whole lives posting to /ceph/pools —
// plural. PVE mounts the pool endpoints as a subclass, PVE::API2::Ceph
// registering PVE::API2::Ceph::Pool at path 'pool', and registers no plural
// alias; its dispatcher answers an unregistered path with 501 "Method not
// implemented". So neither operation has ever reached Ceph.
//
// The evidence was already in this file's source: GetCephPools reads the
// singular path and only falls back to plural. The read side had been
// corrected, the write side never was, and nothing failed because nothing
// asserted where these requests went.
//
// This pins the path and verb of every Ceph call, mutating and reading alike.
// The reads are here as much as the writes: they are what fixes the subclass
// names in place (cfg, osd, mds, mgr, mon, fs, pool — all singular), so the
// next author reaching for a plural has something to fail against.
//
// GetCephCrushRules is the one legitimate plural, and is not an exception to
// that: `rules` is a method registered directly on PVE::API2::Ceph rather than
// a subclass mount, so the singular/plural rule above simply does not apply to
// it. Do not "correct" it.
func TestCephEndpointRequestPaths(t *testing.T) {
	const node = "pve-01"
	base := "/api2/json/nodes/" + node + "/ceph/"

	tests := []struct {
		name       string
		wantMethod string
		wantPath   string
		// reply overrides the default response body for the reads that decode
		// into an object rather than a list. The path is what is under test;
		// the body only has to be shaped well enough to decode.
		reply any
		call  func(*Client) error
	}{
		// --- Writes ---
		{"CreateCephPool", http.MethodPost, base + "pool", nil, func(c *Client) error {
			_, err := c.CreateCephPool(context.Background(), node, CephPoolCreateParams{Name: "testpool", Size: 3, PGNum: 128})
			return err
		}},
		{"DeleteCephPool", http.MethodDelete, base + "pool/testpool", nil, func(c *Client) error {
			_, err := c.DeleteCephPool(context.Background(), node, "testpool")
			return err
		}},
		{"SetCephOSDIn", http.MethodPost, base + "osd/3/in", nil, func(c *Client) error {
			return c.SetCephOSDIn(context.Background(), node, 3)
		}},
		{"SetCephOSDOut", http.MethodPost, base + "osd/3/out", nil, func(c *Client) error {
			return c.SetCephOSDOut(context.Background(), node, 3)
		}},
		{"CephServiceAction/start", http.MethodPost, base + "start", nil, func(c *Client) error {
			_, err := c.CephServiceAction(context.Background(), node, "osd.3", "start")
			return err
		}},
		{"CephServiceAction/stop", http.MethodPost, base + "stop", nil, func(c *Client) error {
			_, err := c.CephServiceAction(context.Background(), node, "osd.3", "stop")
			return err
		}},
		{"CephServiceAction/restart", http.MethodPost, base + "restart", nil, func(c *Client) error {
			_, err := c.CephServiceAction(context.Background(), node, "osd.3", "restart")
			return err
		}},

		// --- Reads ---
		{"GetCephStatus", http.MethodGet, base + "status", map[string]any{}, func(c *Client) error {
			_, err := c.GetCephStatus(context.Background(), node)
			return err
		}},
		{"GetCephOSDs", http.MethodGet, base + "osd", map[string]any{}, func(c *Client) error {
			_, err := c.GetCephOSDs(context.Background(), node)
			return err
		}},
		{"GetCephMonitors", http.MethodGet, base + "mon", nil, func(c *Client) error {
			_, err := c.GetCephMonitors(context.Background(), node)
			return err
		}},
		{"GetCephFS", http.MethodGet, base + "fs", nil, func(c *Client) error {
			_, err := c.GetCephFS(context.Background(), node)
			return err
		}},
		{"GetCephCrushRules", http.MethodGet, base + "rules", nil, func(c *Client) error {
			_, err := c.GetCephCrushRules(context.Background(), node)
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotMethod, gotPath string
			srv := newTestServer(t, map[string]http.HandlerFunc{
				"/": func(w http.ResponseWriter, r *http.Request) {
					gotMethod, gotPath = r.Method, r.URL.Path
					switch {
					case tt.reply != nil:
						jsonResponse(w, tt.reply)
					case r.Method == http.MethodGet:
						// The default read shape: an empty list.
						jsonResponse(w, []map[string]any{})
					default:
						jsonResponse(w, "UPID:"+node+":00001234:00000000:68000000:cephcreatepool:testpool:root@pam:")
					}
				},
			})
			defer srv.Close()

			if err := tt.call(newTestClient(t, srv.URL)); err != nil {
				t.Fatalf("call: %v", err)
			}
			if gotMethod != tt.wantMethod {
				t.Errorf("method = %s, want %s", gotMethod, tt.wantMethod)
			}
			if gotPath != tt.wantPath {
				t.Errorf("path = %s, want %s", gotPath, tt.wantPath)
			}
		})
	}
}

// GetCephPools is the corroborating case, and needs its own test because it
// deliberately issues a second request: it asks for the singular path and only
// falls back to plural if that fails. Only the FIRST path is pinned — the
// fallback is tolerated on the read side, where an extra failed GET costs
// nothing, but it must never become the path tried first.
func TestGetCephPoolsAsksForTheSingularPathFirst(t *testing.T) {
	const node = "pve-01"
	var paths []string

	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/": func(w http.ResponseWriter, r *http.Request) {
			paths = append(paths, r.URL.Path)
			jsonResponse(w, []map[string]any{})
		},
	})
	defer srv.Close()

	if _, err := newTestClient(t, srv.URL).GetCephPools(context.Background(), node); err != nil {
		t.Fatalf("GetCephPools: %v", err)
	}
	want := "/api2/json/nodes/" + node + "/ceph/pool"
	if len(paths) == 0 || paths[0] != want {
		t.Errorf("first path = %v, want %s", paths, want)
	}
	if len(paths) != 1 {
		t.Errorf("requested %v; the singular read succeeded, so nothing should have fallen back", paths)
	}
}

// The path being right is only half of a create: PVE's createpool schema names
// the CRUSH rule parameter `crush_rule`, and register_method schemas reject
// properties they do not define. This was sent as `crush_rule_name` — the field
// name from the pool *listing* response — so even once the request reached the
// endpoint it would have been rejected on validation.
func TestCreateCephPoolSendsThePVEParameterNames(t *testing.T) {
	const node = "pve-01"
	var form url.Values

	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/": func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			form = r.PostForm
			jsonResponse(w, "UPID:"+node+":00001234:00000000:68000000:cephcreatepool:testpool:root@pam:")
		},
	})
	defer srv.Close()

	_, err := newTestClient(t, srv.URL).CreateCephPool(context.Background(), node, CephPoolCreateParams{
		Name:        "testpool",
		Size:        3,
		MinSize:     2,
		PGNum:       128,
		Application: "rbd",
		CrushRule:   "replicated_rule",
		PGAutoScale: "on",
	})
	if err != nil {
		t.Fatalf("CreateCephPool: %v", err)
	}

	want := map[string]string{
		"name":              "testpool",
		"size":              "3",
		"min_size":          "2",
		"pg_num":            "128",
		"application":       "rbd",
		"crush_rule":        "replicated_rule",
		"pg_autoscale_mode": "on",
	}
	for k, v := range want {
		if got := form.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if _, ok := form["crush_rule_name"]; ok {
		t.Error("sent crush_rule_name; PVE's createpool schema defines crush_rule and rejects undeclared properties")
	}
}

// Both pool operations run in a PVE fork_worker (cephcreatepool,
// cephdestroypool), so the UPID is the only handle on whether the pool actually
// appeared or went away. Both methods used to discard it and return error
// alone, which is why their handlers could not TrackTask and reported success
// the moment the worker was queued.
func TestCephPoolOperationsReturnTheUPID(t *testing.T) {
	const node = "pve-01"
	const upid = "UPID:" + node + ":00001234:00000000:68000000:cephcreatepool:testpool:root@pam:"

	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/": func(w http.ResponseWriter, _ *http.Request) { jsonResponse(w, upid) },
	})
	defer srv.Close()
	client := newTestClient(t, srv.URL)

	got, err := client.CreateCephPool(context.Background(), node, CephPoolCreateParams{Name: "testpool", Size: 3, PGNum: 128})
	if err != nil {
		t.Fatalf("CreateCephPool: %v", err)
	}
	if got != upid {
		t.Errorf("CreateCephPool upid = %q, want %q", got, upid)
	}

	got, err = client.DeleteCephPool(context.Background(), node, "testpool")
	if err != nil {
		t.Fatalf("DeleteCephPool: %v", err)
	}
	if got != upid {
		t.Errorf("DeleteCephPool upid = %q, want %q", got, upid)
	}
}

// DeleteCephPool puts the pool name in the path, and url.PathEscape does not
// make that safe on its own: it escapes "/" and leaves ".." untouched, so the
// segment stays a working traversal. ".." pops the pool collection too, landing
// DELETE on /nodes/{node}/ceph; "." stops a level short, on /ceph/pool.
//
// Neither PVE::API2::Ceph nor PVE::API2::Ceph::Pool registers a DELETE at that
// level, so both 501 rather than doing harm — but the same was true of the
// three /disks names guarded alongside this one, and "whatever it lands on
// happens not to take this verb" is a fact about PVE's routing table, not about
// this code. CreateCephPool needs no equivalent: its name travels in the form
// body.
func TestDeleteCephPoolRejectsPathTraversal(t *testing.T) {
	const node = "pve-01"

	for _, name := range []string{"", "..", ".", "../..", "pool/../other"} {
		label := name
		if label == "" {
			label = "empty"
		}
		t.Run(label, func(t *testing.T) {
			var reached bool
			srv := newTestServer(t, map[string]http.HandlerFunc{
				"/": func(w http.ResponseWriter, _ *http.Request) {
					reached = true
					jsonResponse(w, "UPID:"+node+":00001234:00000000:68000000:cephdestroypool:p:root@pam:")
				},
			})
			defer srv.Close()

			_, err := newTestClient(t, srv.URL).DeleteCephPool(context.Background(), node, name)
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("err = %v, want ErrInvalidInput", err)
			}
			if reached {
				t.Error("the request was sent; a rejected segment must never reach the node")
			}
		})
	}
}
