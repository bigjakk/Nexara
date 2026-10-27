package api

import (
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The History routes: what happened to one object, newest first. Each kind of
// object is its own route, at its own path — one route per kind rather than
// one generic route, so that each declares the gate it needs (AllOf
// view:audit and the kind's own view permission) where the registry, the API
// docs and the guards can read it, rather than deferring it to a handler that
// looks the kind up, and each validates its own object's identity — a node
// name is checked against the cluster's nodes before the handler runs, as on
// every other node route. They share one handler (handlers.HistoryHandler)
// and the filters below. How to add a kind: handlers/history.go.

// historyRoute is one kind's History route.
type historyRoute struct {
	Kind        handlers.HistoryKind
	Path        string
	Description string
	// Resource is the kind's own view permission, held with view:audit on
	// the path's cluster — or globally, for a global kind (HistoryKind.Global).
	// Exactly one of Resource and AuditOnly is set; a global kind sets
	// Resource.
	Resource string
	// AuditOnly is why the gate is view:audit alone: the guest, whose own
	// permission depends on its type (view:vm or view:container) and so
	// filters what is the inventory's instead. A route that forgets Resource
	// is refused, never quietly gated on view:audit alone.
	AuditOnly string
	// Parameters are the path's own; the filters every History takes are
	// added.
	Parameters apischema.Properties
}

// historyRoutes is every History route. A new kind adds its one line here.
func historyRoutes() []historyRoute {
	return []historyRoute{
		guestHistoryRoute(),
		folderHistoryRoute(),
		clusterHistoryRoute(),
		nodeHistoryRoute(),
	}
}

// registerHistoryEndpoints declares every kind's History route.
func registerHistoryEndpoints(reg *Registry, h *handlers.HistoryHandler) {
	for _, r := range historyRoutes() {
		reg.Register(historyEndpoint(h, r))
	}
}

// historyEndpoint builds one kind's route, refusing at startup a kind whose
// type filter could not work (HistoryKind.Validate), a route that is not a
// cluster's — or, for a global kind, one that is — a guest-from-path kind
// whose path does not name its guest, and a gate left undecided: exactly one
// of Resource and AuditOnly, and Resource for a global kind. A global kind's
// gate is all GLOBAL, since it reads every cluster's rows.
func historyEndpoint(h *handlers.HistoryHandler, r historyRoute) Endpoint {
	if err := r.Kind.Validate(); err != nil {
		panic(fmt.Sprintf("history route %s: %v", r.Path, err))
	}
	scope, inCluster := ScopeCluster, strings.HasPrefix(r.Path, clusterScope+"/")
	if r.Kind.Global {
		scope = ScopeGlobal
	}
	if inCluster == r.Kind.Global || !strings.HasPrefix(r.Path, pathPrefix) || !strings.HasSuffix(r.Path, "/history") {
		panic(fmt.Sprintf("history route %s: a History route is %s/…/history, and a global kind's is "+
			"%s…/history outside it", r.Path, clusterScope, pathPrefix))
	}
	if g := r.Kind.GuestFromPath; g != "" &&
		(r.Parameters[g].Type != apischema.Integer || !strings.Contains(r.Path+"/", "/:"+g+"/")) {
		panic(fmt.Sprintf("history route %s: the guest's path parameter %q is not one of its integer path "+
			"parameters", r.Path, g))
	}
	resource, auditOnly := strings.TrimSpace(r.Resource), strings.TrimSpace(r.AuditOnly)
	if (resource == "") == (auditOnly == "") || r.Kind.Global && auditOnly != "" {
		panic(fmt.Sprintf("history route %s: set exactly one of Resource (the kind's own view permission) "+
			"and AuditOnly (why view:audit alone is enough), and Resource for a global kind", r.Path))
	}
	audit := Check{Action: "view", Resource: "audit", Scope: scope}
	perms := Permissions{Check: &audit}
	if resource != "" {
		perms = Permissions{AllOf: []Check{audit, {Action: "view", Resource: resource, Scope: scope}}}
	}
	params := historyFilterParams(r.Kind)
	if g := r.Kind.GuestToggle; g != "" {
		params[g] = apischema.Property{
			Type:     apischema.Boolean,
			Optional: true,
			Default:  false,
			Typetext: "<bool>",
			Description: "Include guest activity: the guests' tasks that ran here, and anything else naming a " +
				"guest. Omitted or false leaves them out; the guests' arrivals and departures are listed " +
				"either way.",
		}
	}
	return Endpoint{
		Method:      fiber.MethodGet,
		Path:        r.Path,
		Description: r.Description,
		Group:       "Audit Log",
		Permissions: perms,
		Parameters:  withParams(r.Parameters, params),
		Handler:     h.Route(r.Kind).List,
	}
}

// categoryListPattern accepts "" or a comma-separated list of the kind's
// categories: an unknown one is a 400, as a typo in category is.
func categoryListPattern(categories []string) string {
	one := "(?:" + strings.Join(categories, "|") + ")"
	return "^$|^" + one + "(?:," + one + ")*$"
}

// historyFilterParams are the paging and filters every History takes. The
// type filter's vocabulary is the kind's: exactly the categories its rules
// produce (HistoryKind.Validate), so a typo is a 400 rather than an empty
// page.
func historyFilterParams(kind handlers.HistoryKind) apischema.Properties {
	return apischema.Properties{
		"limit": {
			Type:        apischema.Integer,
			Optional:    true,
			Default:     50,
			Minimum:     apischema.Ptr(1.0),
			Maximum:     apischema.Ptr(200.0),
			Typetext:    "<integer>",
			Description: "Page size.",
		},
		"offset": {
			Type:        apischema.Integer,
			Optional:    true,
			Default:     0,
			Minimum:     apischema.Ptr(0.0),
			Typetext:    "<integer>",
			Description: "Entries to skip.",
		},
		// The EMPTY string is "no filter", as on the task listing's status;
		// it is not a category.
		"category": {
			Type:        apischema.String,
			Optional:    true,
			Enum:        append([]string{""}, kind.Categories...),
			Typetext:    "<" + strings.Join(kind.Categories, "|") + ">",
			Description: "Narrow to one kind of entry. Empty or omitted returns every kind.",
		},
		// A list, so the routine checks a node runs daily can be left out
		// while every other kind stays in.
		"exclude_category": {
			Type:     apischema.String,
			Optional: true,
			Pattern:  categoryListPattern(kind.Categories),
			Typetext: "<category[,category...]>",
			Description: "Leave out these kinds of entry, comma-separated, from the same vocabulary as " +
				"category. Empty or omitted leaves nothing out.",
		},
		"source": {
			Type:     apischema.String,
			Optional: true,
			Enum:     []string{"", "nexara", "proxmox", "detected"},
			Typetext: "<nexara|proxmox|detected>",
			Description: "Narrow to what was done in Nexara, what was done in Proxmox, or what Nexara saw " +
				"change that no task explains (detected). Empty or omitted returns every source.",
		},
		// Named as the audit listing names it, and parsed the same way.
		"start_time": {
			Type:        apischema.String,
			Optional:    true,
			MaxLength:   apischema.Ptr(64),
			Typetext:    "<RFC3339 timestamp>",
			Description: "Oldest entry to return, inclusive. Empty or omitted sets no lower bound.",
		},
	}
}
