package api

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// ScopeKind says what a permission is resolved against.
type ScopeKind string

// The two scopes the RBAC engine knows. There is deliberately no zero
// value among them: a Check that forgot to name its scope is a
// declaration bug, and defaulting it — to either one — would make the
// omission invisible. Register refuses it instead.
const (
	ScopeCluster ScopeKind = "cluster"
	ScopeGlobal  ScopeKind = "global"
)

// Check is one permission requirement: an action on a resource, resolved
// in a scope. ScopeCluster resolves it against the cluster named in the
// request path; ScopeGlobal requires the grant to be held instance-wide.
type Check struct {
	Action   string
	Resource string
	Scope    ScopeKind
}

func (c Check) String() string { return c.Action + ":" + c.Resource }

// ref converts a Check into the handlers-package shape the middleware
// constructors take.
func (c Check) ref() handlers.PermissionRef {
	return handlers.PermissionRef{
		Action:        c.Action,
		Resource:      c.Resource,
		ClusterScoped: c.Scope == ScopeCluster,
	}
}

// AdvisoryCheck is a permission an endpoint applies as a FILTER rather
// than as a gate: a list endpoint that serves every authenticated caller
// but returns only the clusters they can see (accessibleClusters).
//
// It carries a Reason for the same purpose the exemption maps in
// rbac_route_guard_test.go do, and for the same reason their doc comment
// gives: a list of exceptions with no stated justification rots into a
// rubber stamp. Here the reason must name what does the filtering, so the
// next reader can go and check that it still does.
type AdvisoryCheck struct {
	Check
	Reason string
}

// Permissions declares how one endpoint is authorized. EXACTLY ONE field
// may be set — Register rejects zero (a route nobody decided about) and
// rejects two (a route whose declaration contradicts itself).
//
// The six shapes are not stylistic alternatives; they are the six answers
// that a survey of the existing 544 hand-placed checks actually produced,
// and each one means something different to a reader deciding whether a
// route is safe:
//
//	Check        the gate runs as middleware, before the handler
//	Alternatives the gate passes if ANY of them holds
//	Deferred     the handler computes the gate itself; the reason says why
//	Advisory     the handler FILTERS on it; there is no gate
//	Public       no session at all
//	SelfService  authenticated, acting solely on the caller's own identity
//
// Only Check and Alternatives install middleware. The other four install
// none, which is the point of making each of them say so out loud.
type Permissions struct {
	// Check is the declarative gate: the common case, and the one the
	// whole registry exists to make possible.
	Check *Check

	// Alternatives passes if the caller holds any one of them — the
	// "view:vm or view:container" endpoints that serve both guest kinds
	// through one path.
	Alternatives []Check

	// Deferred means the handler decides for itself, because what to
	// check depends on something only the handler knows (the content type
	// of an upload, which of several objects the body names). The string
	// is the reason, and it must say what the handler checks, not merely
	// that it checks something.
	Deferred string

	// Advisory documents a permission the handler applies as a filter
	// rather than as a gate. See AdvisoryCheck.
	Advisory *AdvisoryCheck

	// Public means the route is served with no session. The string is the
	// reason it must be reachable anonymously.
	Public string

	// SelfService means the route acts solely on the caller's own
	// identity, taken from the session rather than from user input — a
	// permission check would be meaningless because a user cannot be
	// denied access to their own profile. The string is the reason, and
	// it must establish that the subject comes from the session: an IDOR
	// (acting on a subject named in the path) is exactly what would
	// otherwise hide here.
	SelfService string
}

// authenticated reports whether the route needs a session. Everything but
// Public does.
func (p Permissions) authenticated() bool { return p.Public == "" }

// middleware returns the permission gate to attach to the route, or nil
// when this declaration installs none.
func (p Permissions) middleware() fiber.Handler {
	switch {
	case p.Check != nil:
		if p.Check.Scope == ScopeCluster {
			return handlers.RequireClusterPermission(p.Check.Action, p.Check.Resource)
		}
		return handlers.RequirePermission(p.Check.Action, p.Check.Resource)
	case len(p.Alternatives) > 0:
		refs := make([]handlers.PermissionRef, 0, len(p.Alternatives))
		for _, alt := range p.Alternatives {
			refs = append(refs, alt.ref())
		}
		return handlers.RequireAnyPermission(refs...)
	default:
		return nil
	}
}

// Describe renders the declaration the way the API documentation and a
// role-building operator want to read it: "view:cluster", "view:vm |
// view:container", "deferred", "public".
func (p Permissions) Describe() string {
	switch {
	case p.Check != nil:
		return p.Check.String()
	case len(p.Alternatives) > 0:
		names := make([]string, 0, len(p.Alternatives))
		for _, alt := range p.Alternatives {
			names = append(names, alt.String())
		}
		return strings.Join(names, " | ")
	case p.Advisory != nil:
		return p.Advisory.String() + " (filtered)"
	case p.Deferred != "":
		return "deferred"
	case p.Public != "":
		return "public"
	case p.SelfService != "":
		return "self-service"
	default:
		return ""
	}
}

// validate reports whether the declaration is usable. It is called from
// Register, so every finding here is a startup panic rather than a
// per-request surprise.
//
// pathParams is the route's placeholder names, used to reject a
// cluster-scoped check on a path that names no cluster — a gate that
// would 400 every caller instead of authorizing any of them.
func (p Permissions) validate(path string, pathParams []string) error {
	set := p.setFields()
	switch len(set) {
	case 1:
	case 0:
		return errors.New("declares no Permissions field; " +
			"set exactly one of Check, Alternatives, Deferred, Advisory, Public or SelfService")
	default:
		return fmt.Errorf("declares %d Permissions fields (%s); exactly one may be set",
			len(set), strings.Join(set, ", "))
	}

	switch {
	case p.Check != nil:
		return validateCheck("Check", *p.Check, path, pathParams)
	case len(p.Alternatives) > 0:
		if len(p.Alternatives) < 2 {
			// One alternative is not an alternative to anything. It is
			// almost certainly a Check that was written into the wrong
			// field, and accepting it would silently lose the "any of"
			// meaning that the field's whole existence documents.
			return errors.New("declares a single Alternatives entry; use Check for one requirement")
		}
		for i, alt := range p.Alternatives {
			if err := validateCheck(fmt.Sprintf("Alternatives[%d]", i), alt, path, pathParams); err != nil {
				return err
			}
		}
		return nil
	case p.Advisory != nil:
		return validateAdvisory(*p.Advisory, path, pathParams)
	case p.Deferred != "":
		return requireReason("Deferred", p.Deferred)
	case p.Public != "":
		return requireReason("Public", p.Public)
	case p.SelfService != "":
		return requireReason("SelfService", p.SelfService)
	}
	return nil
}

// validateAdvisory checks the one shape that carries both a Check and a
// reason: the permission is real and documented, but nothing enforces it
// except the handler's own filtering, so the reason has to say what does.
//
// It deliberately does NOT require the path to name a cluster. Advisory
// installs no middleware, so nothing ever calls clusterIDFromParam and
// the Scope has no runtime effect — it is documentation of what the
// handler filters on. Requiring the path would make the shape
// undeclarable on the very endpoints it exists for: GET /api/v1/clusters,
// the accessibleClusters listing named in AdvisoryCheck's own doc, has
// neither a :cluster_id nor a /clusters/:id. Authors pushed away from
// ScopeCluster would write ScopeGlobal instead, and Describe() would then
// tell an operator the route needs an instance-wide grant when it does
// not.
func validateAdvisory(a AdvisoryCheck, _ string, _ []string) error {
	const field = "Advisory"
	if strings.TrimSpace(a.Reason) == "" {
		return fmt.Errorf("%s has no Reason; it must name what does the filtering", field)
	}
	return validateVocabulary(field, a.Check)
}

// requireReason rejects a whitespace-only justification. An exemption
// whose reason is a space is an exemption nobody will re-read, which is
// the failure mode the exemption maps in rbac_route_guard_test.go are
// written to avoid.
func requireReason(field, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("%s has a blank reason; state why this route needs no permission gate", field)
	}
	return nil
}

// setFields names the Permissions fields this declaration populates. The
// reason-carrying fields count as set the moment they are non-empty, so
// that a blank reason is reported as a blank reason rather than as a
// route that declared nothing.
func (p Permissions) setFields() []string {
	var set []string
	if p.Check != nil {
		set = append(set, "Check")
	}
	if len(p.Alternatives) > 0 {
		set = append(set, "Alternatives")
	}
	if p.Deferred != "" {
		set = append(set, "Deferred")
	}
	if p.Advisory != nil {
		set = append(set, "Advisory")
	}
	if p.Public != "" {
		set = append(set, "Public")
	}
	if p.SelfService != "" {
		set = append(set, "SelfService")
	}
	return set
}

// validateVocabulary checks the parts of a Check that mean something
// whether or not a gate is installed: it has to name an action, a
// resource, and one of the two scopes the engine knows.
func validateVocabulary(field string, c Check) error {
	if strings.TrimSpace(c.Action) == "" {
		return fmt.Errorf("%s has no Action", field)
	}
	if strings.TrimSpace(c.Resource) == "" {
		return fmt.Errorf("%s has no Resource", field)
	}
	if c.Scope != ScopeGlobal && c.Scope != ScopeCluster {
		return fmt.Errorf("%s has scope %q; use ScopeCluster or ScopeGlobal", field, c.Scope)
	}
	return nil
}

// validateCheck rejects the shapes of Check that cannot authorize anyone.
// It is for the two shapes that INSTALL a gate, so it additionally
// requires the path to name the cluster a cluster-scoped gate will
// resolve. See validateAdvisory for the shape that installs none.
func validateCheck(field string, c Check, path string, pathParams []string) error {
	if err := validateVocabulary(field, c); err != nil {
		return err
	}
	switch c.Scope {
	case ScopeGlobal:
	case ScopeCluster:
		if !namesACluster(pathParams, path) {
			return fmt.Errorf("%s is %s-scoped but the path does not name the cluster it acts on; "+
				"it needs a required :cluster_id as its FIRST path parameter, or to start with %q and name no later :cluster_id",
				field, ScopeCluster, legacyClusterPrefix)
		}
	}
	return nil
}

// legacyClusterPrefix is the one route shape where the path parameter
// :id is a cluster id, matching clusters.Get("/:id") in router.go.
const legacyClusterPrefix = pathPrefix + "clusters/:id"

// namesACluster reports whether the cluster clusterIDFromParam resolves
// out of this route's path is the cluster the route ACTS ON.
//
// "Is a cluster named anywhere" is the wrong question twice over, and
// both wrong answers fail closed, which is why neither would survive
// review on its own:
//
//   - clusterIDFromParam falls back to :id, so on /api/v1/vms/:id a
//     cluster-scoped check resolves the GUEST's uuid and asks the engine
//     about a cluster that does not exist. The grant lookup
//     short-circuits on a global grant (internal/auth/rbac.go), so the
//     gate passes every admin and refuses every cluster-scoped operator
//     — reading as cluster enforcement while enforcing nothing.
//   - A :cluster_id that is not the subject is worse, because it
//     authorizes the wrong object. On
//     POST /api/v1/vms/:vm_id/migrate/:cluster_id the gate would check
//     the DESTINATION, so a caller holding manage:vm on cluster B could
//     migrate a guest out of cluster A.
//
// So :cluster_id must be the FIRST placeholder in the path — the subject
// the rest of the route hangs off — and the :id fallback is accepted only
// at the anchored legacy prefix, as a whole segment, and only when no later
// placeholder is :cluster_id. clusterIDFromParam reads :cluster_id BEFORE
// it falls back to :id, so on /api/v1/clusters/:id/migrate-to/:cluster_id
// the later one wins the lookup and the gate authorizes the destination —
// the same escalation as the second bullet, reached through the legacy :id
// exemption. A parameter naming some other cluster must be called something
// else (target_cluster_id), which checkPathParams enforces from the other
// direction.
//
// An OPTIONAL :cluster_id? — constrained or not — is not this function's to
// refuse: checkPathParams refuses "?" in any registry path, because a caller
// can leave such a segment empty (/api/v1/clusters//vms/<id>) and
// clusterIDFromParam then falls back to :id. (It refuses "<" too, for the
// separate reason its own comment gives.)
//
// EqualFold because Fiber's own matching is case-insensitive; see
// checkPathParams.
func namesACluster(pathParams []string, path string) bool {
	if len(pathParams) > 0 && strings.EqualFold(pathParams[0], "cluster_id") {
		return true
	}
	if !strings.HasPrefix(strings.ToLower(path), legacyClusterPrefix) {
		return false
	}
	// A whole segment, so that /clusters/:idx is not read as /clusters/:id.
	rest := path[len(legacyClusterPrefix):]
	if rest != "" && rest[0] != '/' {
		return false
	}
	for _, name := range pathParams {
		if strings.EqualFold(name, "cluster_id") {
			return false
		}
	}
	return true
}
