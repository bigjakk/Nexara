package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// favoritesRouteCount is how many endpoints registerFavoritesEndpoints
// declares.
const favoritesRouteCount = 3

// favoritesRoutesOutsideTheClusterCheckShape records why none of the three is a
// plain cluster-scoped Check. It is folded into
// routesOutsideTheClusterCheckShape in registry_vms_test.go.
var favoritesRoutesOutsideTheClusterCheckShape = map[string]string{
	"GET /api/v1/favorites": "Advisory: three accessibleClusters reads build one scope per resource type; " +
		"the listing spans every cluster and every type, so there is neither a single cluster nor a single " +
		"permission a gate could resolve",
	"POST /api/v1/favorites": "Deferred: resource_type selects the resource and the body carries the " +
		"cluster, because the path names neither",
	"DELETE /api/v1/favorites": "SelfService: the DELETE is keyed by the authenticated user id, and gating " +
		"it on view access would strand rows a user can no longer see",
}

func declaredFavoritesEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if e.Path == favoritesScope {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestFavoritesRoutesDeclareWhatTheyEnforced is this domain's tally.
//
// From `git show HEAD:internal/api/handlers/favorites.go` at commit eaeafa7 —
// three handlers, ONE require*Perm call between them:
//
//	ListFavorites   three accessibleClusters reads, no gate.
//	AddFavorite     requireClusterPerm(c, "view", target.ResourceType,
//	                target.ClusterID) — both halves out of the body.
//	RemoveFavorite  nothing at all, deliberately; it was already listed in
//	                selfServiceRoutes with that reason.
//
// The three declarations say exactly that, and nothing more.
func TestFavoritesRoutesDeclareWhatTheyEnforced(t *testing.T) {
	declared := declaredFavoritesEndpoints(t)
	if len(declared) != favoritesRouteCount {
		t.Fatalf("the registry declares %d favorites routes, want %d", len(declared), favoritesRouteCount)
	}

	list := declared["GET /api/v1/favorites"]
	if list.Permissions.Advisory == nil {
		t.Errorf("the listing declares %q, want Advisory — nothing gates it", list.Permissions.Describe())
	} else if !strings.Contains(list.Permissions.Advisory.Reason, "accessibleClusters") {
		t.Errorf("the Advisory reason does not name what does the filtering: %q", list.Permissions.Advisory.Reason)
	}

	add := declared["POST /api/v1/favorites"]
	if add.Permissions.Deferred == "" {
		t.Errorf("the add declares %q, want Deferred — both halves of its permission are in the body",
			add.Permissions.Describe())
	} else {
		for _, phrase := range []string{"view:cluster", "view:node", "view:vm", "requireClusterPerm"} {
			if !strings.Contains(add.Permissions.Deferred, phrase) {
				t.Errorf("the Deferred reason does not name %q, so it does not say what the handler checks: %q",
					phrase, add.Permissions.Deferred)
			}
		}
	}

	remove := declared["DELETE /api/v1/favorites"]
	if remove.Permissions.SelfService == "" {
		t.Errorf("the remove declares %q, want SelfService", remove.Permissions.Describe())
	}
}

// TestFavoritesRemoveReasonMatchesTheReviewedList keeps one exemption to one
// justification: SelfService is the riskier of the two middleware-free shapes,
// so the inline reason and selfServiceRoutes' reason must be the same sentence.
// TestGuard_RegistrySelfServiceRoutesAreReviewed already requires the route to
// be listed; this requires the two reasons to agree.
func TestFavoritesRemoveReasonMatchesTheReviewedList(t *testing.T) {
	const key = "DELETE /api/v1/favorites"
	e := declaredEndpoint(t, fiber.MethodDelete, favoritesScope)
	reviewed, listed := selfServiceRoutes[key]
	if !listed {
		t.Fatalf("%s declares SelfService but is not in selfServiceRoutes", key)
	}
	if e.Permissions.SelfService != reviewed {
		t.Errorf("%s: the declared reason is %q but selfServiceRoutes says %q — one exemption, one justification",
			key, e.Permissions.SelfService, reviewed)
	}
}

// TestFavoriteResourceTypeVocabulary pins the declared Enum against the
// handler's own accepted set. The direction that bites is a schema accepting a
// type parseFavoriteTarget has no branch for: the row would be written against
// a CHECK constraint that refuses it, which surfaces as a 500.
func TestFavoriteResourceTypeVocabulary(t *testing.T) {
	want := handlers.FavoriteResourceTypes()
	slices.Sort(want)
	for _, method := range []string{fiber.MethodPost, fiber.MethodDelete} {
		got := slices.Clone(declaredEndpoint(t, method, favoritesScope).Parameters["resource_type"].Enum)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s declares the resource_type enum %v but the handler accepts %v", method, got, want)
		}
	}
}

// TestFavoriteClusterIsNotNamedClusterID is the escalation guard these two
// declarations had to route around, recorded so the workaround is not mistaken
// for an arbitrary name — and driven on BOTH spellings, because the sidebar
// sends the alias.
func TestFavoriteClusterIsNotNamedClusterID(t *testing.T) {
	for _, method := range []string{fiber.MethodPost, fiber.MethodDelete} {
		e := declaredEndpoint(t, method, favoritesScope)
		if _, declared := e.Parameters["cluster_id"]; declared {
			t.Fatalf("%s declares a parameter NAMED cluster_id; the permission middleware reads that name "+
				"out of the path, so Register would have refused it", method)
		}
		if got := e.Parameters["favorite_cluster_id"].Alias; got != "cluster_id" {
			t.Errorf("%s: favorite_cluster_id declares alias %q, want cluster_id — that is the spelling "+
				"the sidebar sends", method, got)
		}
	}

	// End to end on both spellings, through the POST body.
	for _, spelling := range []string{"cluster_id", "favorite_cluster_id"} {
		cap := &capture{}
		e := declaredEndpoint(t, fiber.MethodPost, favoritesScope)
		e.Handler = cap.handler()
		e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
		app := newRegistryApp(t, noAuth(), e)

		body := `{"resource_type":"vm","` + spelling + `":"` + testClusterID + `","ref":"101"}`
		status, env := send(t, app, jsonRequest(http.MethodPost, favoritesScope, body))
		if status != fiber.StatusNoContent {
			t.Fatalf("spelling %q: status = %d (%q), want 204", spelling, status, env.Message)
		}
		if got := cap.params.String("favorite_cluster_id"); got != testClusterID {
			t.Errorf("spelling %q reached the handler as favorite_cluster_id=%q", spelling, got)
		}
	}
}

// TestFavoriteRefStaysOptional is the cross-field assertion this domain needed
// a decision about.
//
// What a valid ref is depends on resource_type — empty for a cluster, a node
// name for a node, a decimal VMID for a guest — which no parameter schema can
// express. Declaring it REQUIRED would break starring a cluster, which the
// sidebar does with no ref at all; declaring it with a format would break one
// of the other two. It stays optional and bounded, and parseFavoriteTarget
// keeps the rule for both routes at once.
func TestFavoriteRefStaysOptional(t *testing.T) {
	for _, method := range []string{fiber.MethodPost, fiber.MethodDelete} {
		prop := declaredEndpoint(t, method, favoritesScope).Parameters["ref"]
		if !prop.Optional {
			t.Errorf("%s: ref is required; starring a CLUSTER sends none", method)
		}
		if prop.Format != "" {
			t.Errorf("%s: ref declares format %q — it is a node name on one type and a decimal VMID on "+
				"another, so a single format would reject one of them", method, prop.Format)
		}
		if prop.MaxLength == nil {
			t.Errorf("%s: ref declares no maximum length; it is part of the favorites primary key, and an "+
				"oversized one fails the INSERT with an opaque index-size error", method)
		}
	}

	// Starring a cluster: no ref, and the request is accepted.
	cap := &capture{}
	e := declaredEndpoint(t, fiber.MethodDelete, favoritesScope)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	app := newRegistryApp(t, noAuth(), e)

	target := favoritesScope + "?resource_type=cluster&cluster_id=" + testClusterID
	if status, env := send(t, app, httptest.NewRequest(http.MethodDelete, target, nil)); status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — unstarring a cluster carries no ref", status, env.Message)
	}
	if cap.params.Has("ref") {
		t.Error("ref reads as supplied on a request that omitted it")
	}
}

// TestEveryFavoritesEndpointIsDocumented holds the declarations to the standard
// that makes this whole effort worth doing.
func TestEveryFavoritesEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredFavoritesEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Favorites" {
			t.Errorf("%s is in group %q, want Favorites", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
