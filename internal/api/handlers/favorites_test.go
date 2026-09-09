package handlers

import (
	"regexp"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/migrations"
)

func TestParseFavoriteTarget(t *testing.T) {
	cluster := uuid.New()

	tests := []struct {
		name    string
		req     favoriteRequest
		wantRef string
		wantErr int // 0 = expect success
	}{
		{
			name:    "cluster ignores any ref",
			req:     favoriteRequest{ResourceType: "cluster", ClusterID: cluster.String(), Ref: "junk"},
			wantRef: "",
		},
		{
			name:    "cluster with empty ref",
			req:     favoriteRequest{ResourceType: "cluster", ClusterID: cluster.String()},
			wantRef: "",
		},
		{
			name:    "node keeps its name",
			req:     favoriteRequest{ResourceType: "node", ClusterID: cluster.String(), Ref: "pve-01"},
			wantRef: "pve-01",
		},
		{
			name:    "node ref is trimmed",
			req:     favoriteRequest{ResourceType: "node", ClusterID: cluster.String(), Ref: "  pve-01  "},
			wantRef: "pve-01",
		},
		{
			name:    "node needs a name",
			req:     favoriteRequest{ResourceType: "node", ClusterID: cluster.String(), Ref: "   "},
			wantErr: fiber.StatusBadRequest,
		},
		{
			// resource_ref is part of the primary key, so an oversized ref
			// fails the INSERT with an opaque index-size error rather than
			// anything the caller can act on.
			name:    "node ref is length bounded",
			req:     favoriteRequest{ResourceType: "node", ClusterID: cluster.String(), Ref: strings.Repeat("a", maxNodeRefBytes+1)},
			wantErr: fiber.StatusBadRequest,
		},
		{
			name:    "node ref at the bound is accepted",
			req:     favoriteRequest{ResourceType: "node", ClusterID: cluster.String(), Ref: strings.Repeat("a", maxNodeRefBytes)},
			wantRef: strings.Repeat("a", maxNodeRefBytes),
		},
		{
			name:    "vm ref is a vmid",
			req:     favoriteRequest{ResourceType: "vm", ClusterID: cluster.String(), Ref: "101"},
			wantRef: "101",
		},
		{
			// The primary key is (user, cluster, type, ref), so an unnormalised
			// "0101" would be a second row naming the same guest — starrable
			// twice and unstarrable never, since the client only ever sends the
			// canonical form back.
			name:    "vm ref is canonicalised",
			req:     favoriteRequest{ResourceType: "vm", ClusterID: cluster.String(), Ref: "0101"},
			wantRef: "101",
		},
		{
			name:    "vm ref rejects a name",
			req:     favoriteRequest{ResourceType: "vm", ClusterID: cluster.String(), Ref: "pve-01"},
			wantErr: fiber.StatusBadRequest,
		},
		{
			name:    "vm ref rejects a negative vmid",
			req:     favoriteRequest{ResourceType: "vm", ClusterID: cluster.String(), Ref: "-1"},
			wantErr: fiber.StatusBadRequest,
		},
		{
			// ListFavoriteVMs casts resource_ref to int; anything that would
			// overflow must never reach the column.
			name:    "vm ref rejects an out-of-range vmid",
			req:     favoriteRequest{ResourceType: "vm", ClusterID: cluster.String(), Ref: "99999999999"},
			wantErr: fiber.StatusBadRequest,
		},
		{
			// THE BOUNDARY. ParseInt(ref, 10, 32) accepts up to 2147483647, but
			// 000101's CHECK is `^[0-9]{1,9}$`. Anything in between passes Go
			// and violates the constraint, which is a 500 for a client error.
			// The two ceilings have to be the same number.
			name:    "vm ref rejects the ten-digit range the CHECK forbids",
			req:     favoriteRequest{ResourceType: "vm", ClusterID: cluster.String(), Ref: "1000000000"},
			wantErr: fiber.StatusBadRequest,
		},
		{
			name:    "vm ref accepts the largest VMID the CHECK allows",
			req:     favoriteRequest{ResourceType: "vm", ClusterID: cluster.String(), Ref: "999999999"},
			wantRef: "999999999",
		},
		{
			// A NUL cannot be stored in a Postgres text column at all, so it
			// fails at encode time rather than as anything actionable.
			name:    "node ref rejects a control character",
			req:     favoriteRequest{ResourceType: "node", ClusterID: cluster.String(), Ref: "pve-01\x00"},
			wantErr: fiber.StatusBadRequest,
		},
		{
			name:    "unknown resource type",
			req:     favoriteRequest{ResourceType: "storage", ClusterID: cluster.String(), Ref: "store01"},
			wantErr: fiber.StatusBadRequest,
		},
		{
			name:    "empty resource type",
			req:     favoriteRequest{ClusterID: cluster.String()},
			wantErr: fiber.StatusBadRequest,
		},
		{
			name:    "invalid cluster id",
			req:     favoriteRequest{ResourceType: "node", ClusterID: "not-a-uuid", Ref: "pve-01"},
			wantErr: fiber.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFavoriteTarget(tt.req)

			if tt.wantErr != 0 {
				if err == nil {
					t.Fatalf("parseFavoriteTarget(%+v) = %+v, want error", tt.req, got)
				}
				fe, ok := err.(*fiber.Error)
				if !ok {
					t.Fatalf("error is %T, want *fiber.Error so Fiber renders the right status", err)
				}
				if fe.Code != tt.wantErr {
					t.Errorf("status = %d, want %d", fe.Code, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("parseFavoriteTarget(%+v) returned %v, want success", tt.req, err)
			}
			if got.Ref != tt.wantRef {
				t.Errorf("Ref = %q, want %q", got.Ref, tt.wantRef)
			}
			if got.ResourceType != tt.req.ResourceType {
				t.Errorf("ResourceType = %q, want %q", got.ResourceType, tt.req.ResourceType)
			}
			if got.ClusterID.String() != tt.req.ClusterID {
				t.Errorf("ClusterID = %q, want %q", got.ClusterID, tt.req.ClusterID)
			}
		})
	}
}

// TestFavoriteTypesAreRBACResources pins the resource_type values to the RBAC
// permission catalog, read out of the migrations that seed it.
//
// AddFavorite passes the resource_type straight to requireClusterPerm as the
// resource name. A constant edited to something that is not a seeded resource
// resolves to a permission nobody holds, so every star 403s — including for an
// admin — with nothing in the code reading as wrong.
//
// The catalog is parsed from migrations.FS rather than listed here: a list
// written next to the constants it checks agrees with them by construction and
// can never fail.
//
// What this does NOT catch, deliberately stated so nobody trusts it further
// than it goes: a migration that RENAMES a resource. A rename ships as a new
// UPDATE or INSERT while the original seeding text stays in 000016 forever, so
// the old name still parses out of the file set. Catching that would mean
// executing the migrations, which is internal/db's job, not this test's.
func TestFavoriteTypesAreRBACResources(t *testing.T) {
	seeded := seededViewResources(t)

	for _, typ := range []string{favoriteCluster, favoriteNode, favoriteVM} {
		if !seeded[typ] {
			t.Errorf("favorite resource_type %q has no seeded view permission; "+
				"AddFavorite would check a permission that cannot be granted", typ)
		}
	}
}

var (
	// permissionInsertPattern isolates one INSERT INTO permissions statement.
	// Both column orders are in use — (action, resource, description) in the
	// early migrations and (id, action, resource, description) with a
	// gen_random_uuid() first in everything from 000020 on — so the tuple shape
	// is matched inside the block rather than anchored to the start of a tuple.
	permissionInsertPattern = regexp.MustCompile(`(?is)INSERT\s+INTO\s+permissions\b.*?;`)

	// viewPermissionPattern matches a seeded view permission inside such a
	// block. 'view' is always immediately followed by its resource, whichever
	// column order the statement uses.
	viewPermissionPattern = regexp.MustCompile(`'view'\s*,\s*'([a-z_]+)'`)
)

// seededViewResources returns every resource with a seeded view permission,
// across all migrations — later ones add resources the initial catalog lacked.
//
// Scoped to INSERT INTO permissions blocks rather than run over whole files:
// migrations also mention ('view', 'generate') inside role-grant WHERE clauses,
// which would otherwise be read back as a resource named "generate".
func seededViewResources(t *testing.T) map[string]bool {
	t.Helper()

	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}

	out := map[string]bool{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".up.sql") {
			continue
		}
		body, readErr := migrations.FS.ReadFile(e.Name())
		if readErr != nil {
			t.Fatalf("read %s: %v", e.Name(), readErr)
		}

		for _, block := range permissionInsertPattern.FindAllString(string(body), -1) {
			matches := viewPermissionPattern.FindAllStringSubmatch(block, -1)
			// A block that seeds permissions but yields nothing means the
			// pattern has drifted from how they are written. Without this the
			// test degrades silently into checking a shrinking subset of the
			// catalog, which is how it was vacuous before.
			if len(matches) == 0 && strings.Contains(block, "'view'") {
				t.Errorf("%s: an INSERT INTO permissions block mentions 'view' but no "+
					"resource parsed out of it — viewPermissionPattern has drifted:\n%s",
					e.Name(), block)
			}
			for _, m := range matches {
				out[m[1]] = true
			}
		}
	}
	return out
}
