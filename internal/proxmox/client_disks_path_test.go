package proxmox

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// WipeDisk spent its whole life PUTting to /disks/smart — the SMART reader's
// path, copied from the method twenty lines above it. PVE registers smart as
// GET only, so every wipe came back "not implemented" and the disk was never
// touched. Nothing caught it because nothing asserted where the request went:
// a copied URL still marshals, still authenticates, and still returns an error
// that looks like any other.
//
// So this pins the path and verb of every /disks call that mutates. There was
// no test file for client_nodes.go at all before it.
func TestDiskEndpointRequestPaths(t *testing.T) {
	const node = "pve-01"
	base := "/api2/json/nodes/" + node + "/disks/"

	tests := []struct {
		name       string
		wantMethod string
		wantPath   string
		call       func(*Client) (string, error)
	}{
		{"WipeDisk", http.MethodPut, base + "wipedisk", func(c *Client) (string, error) {
			return c.WipeDisk(context.Background(), node, "/dev/sdz")
		}},
		{"InitializeGPT", http.MethodPost, base + "initgpt", func(c *Client) (string, error) {
			return c.InitializeGPT(context.Background(), node, "/dev/sdz")
		}},
		{"CreateNodeZFSPool", http.MethodPost, base + "zfs", func(c *Client) (string, error) {
			return c.CreateNodeZFSPool(context.Background(), node, CreateZFSPoolParams{Name: "tank", RaidLevel: "single", Devices: "/dev/sdz"})
		}},
		{"DeleteNodeZFSPool", http.MethodDelete, base + "zfs/tank", func(c *Client) (string, error) {
			return c.DeleteNodeZFSPool(context.Background(), node, "tank", false, false)
		}},
		{"CreateNodeLVM", http.MethodPost, base + "lvm", func(c *Client) (string, error) {
			return c.CreateNodeLVM(context.Background(), node, CreateLVMParams{Name: "vg0", Device: "/dev/sdz"})
		}},
		{"DeleteNodeLVM", http.MethodDelete, base + "lvm/vg0", func(c *Client) (string, error) {
			return c.DeleteNodeLVM(context.Background(), node, "vg0", false, false)
		}},
		{"CreateNodeLVMThin", http.MethodPost, base + "lvmthin", func(c *Client) (string, error) {
			return c.CreateNodeLVMThin(context.Background(), node, CreateLVMThinParams{Name: "thin0", Device: "/dev/sdz"})
		}},
		{"DeleteNodeLVMThin", http.MethodDelete, base + "lvmthin/thin0", func(c *Client) (string, error) {
			return c.DeleteNodeLVMThin(context.Background(), node, "thin0", "vg0", false, false)
		}},
		{"CreateNodeDirectory", http.MethodPost, base + "directory", func(c *Client) (string, error) {
			return c.CreateNodeDirectory(context.Background(), node, CreateDirectoryParams{Name: "backups", Device: "/dev/sdz", Filesystem: "ext4"})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotMethod, gotPath string
			srv := newTestServer(t, map[string]http.HandlerFunc{
				"/": func(w http.ResponseWriter, r *http.Request) {
					gotMethod, gotPath = r.Method, r.URL.Path
					jsonResponse(w, "UPID:"+node+":00001234:00000000:68000000:wipedisk:sdz:root@pam:")
				},
			})
			defer srv.Close()

			if _, err := tt.call(newTestClient(t, srv.URL)); err != nil {
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

// WipeDisk sends the disk in the form body, so the path being right is only
// half of it — PVE names the parameter `disk` and rejects anything else.
func TestWipeDiskSendsTheDiskInTheBody(t *testing.T) {
	const node = "pve-01"
	var gotDisk string

	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/": func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			gotDisk = r.PostFormValue("disk")
			jsonResponse(w, "UPID:"+node+":00001234:00000000:68000000:wipedisk:sdz:root@pam:")
		},
	})
	defer srv.Close()

	if _, err := newTestClient(t, srv.URL).WipeDisk(context.Background(), node, "/dev/sdz"); err != nil {
		t.Fatalf("WipeDisk: %v", err)
	}
	if gotDisk != "/dev/sdz" {
		t.Errorf("disk = %q, want the disk it was asked to wipe", gotDisk)
	}
}

// The path WipeDisk was mistakenly using. It reads SMART data over GET with the
// disk as a query parameter, and is pinned here so the difference from the wipe
// endpoint stays on the record rather than in one line of git history.
func TestGetDiskSMARTUsesTheSmartPath(t *testing.T) {
	const node = "pve-01"
	var gotMethod, gotPath, gotQuery string

	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/": func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.Query().Get("disk")
			jsonResponse(w, map[string]any{"health": "PASSED"})
		},
	})
	defer srv.Close()

	if _, err := newTestClient(t, srv.URL).GetDiskSMART(context.Background(), node, "/dev/sdz"); err != nil {
		t.Fatalf("GetDiskSMART: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %s, want GET", gotMethod)
	}
	if want := "/api2/json/nodes/" + node + "/disks/smart"; gotPath != want {
		t.Errorf("path = %s, want %s", gotPath, want)
	}
	if gotQuery != "/dev/sdz" {
		t.Errorf("disk query = %q, want the requested disk", gotQuery)
	}
}

// The pool/VG/thin-pool name lands in the path as its own segment, and until
// now it reached url.PathEscape with nothing else in front of it. That is the
// case validatePathSegment's own doc comment calls out as insufficient: escaping
// a path segment escapes "/", and leaves ".." exactly as it found it. pveproxy
// would read the segment literally, as a name pve-storage-id refuses (see
// validatePathSegment), but behind a normalising proxy it is a traversal: ".."
// pops the collection along with the name, so DELETE
// /nodes/{node}/disks/zfs/.. arrives at /nodes/{node}/disks, and "." lands it
// on /nodes/{node}/disks/zfs.
//
// Nothing was exploitable even there — PVE registers no DELETE at either
// level, so both 501 — but "whatever it lands on happens not to take this verb"
// is a property of PVE's routing table, not of this code, and it
// is not the kind of thing that should be load-bearing. The guard is the fix;
// this pins it, including the part that matters most: the request is refused
// before it is sent, not after.
func TestDiskPoolNamesRejectPathTraversal(t *testing.T) {
	const node = "pve-01"

	tests := []struct {
		name string
		call func(*Client, string) (string, error)
	}{
		{"DeleteNodeZFSPool", func(c *Client, seg string) (string, error) {
			return c.DeleteNodeZFSPool(context.Background(), node, seg, false, false)
		}},
		{"DeleteNodeLVM", func(c *Client, seg string) (string, error) {
			return c.DeleteNodeLVM(context.Background(), node, seg, false, false)
		}},
		{"DeleteNodeLVMThin", func(c *Client, seg string) (string, error) {
			return c.DeleteNodeLVMThin(context.Background(), node, seg, "vg0", false, false)
		}},
	}

	for _, tt := range tests {
		for _, seg := range []string{"", "..", ".", "../..", "tank/../other"} {
			t.Run(tt.name+"/"+segLabel(seg), func(t *testing.T) {
				var reached bool
				srv := newTestServer(t, map[string]http.HandlerFunc{
					"/": func(w http.ResponseWriter, r *http.Request) {
						reached = true
						jsonResponse(w, "UPID:"+node+":00001234:00000000:68000000:zfsdestroy:tank:root@pam:")
					},
				})
				defer srv.Close()

				_, err := tt.call(newTestClient(t, srv.URL), seg)
				if !errors.Is(err, ErrInvalidInput) {
					t.Errorf("err = %v, want ErrInvalidInput", err)
				}
				if reached {
					t.Error("the request was sent; a rejected segment must never reach the node")
				}
			})
		}
	}
}

// segLabel names an empty segment so the subtest does not end in a bare slash.
func segLabel(seg string) string {
	if seg == "" {
		return "empty"
	}
	return seg
}
