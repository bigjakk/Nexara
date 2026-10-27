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
	// the path's cluster. Empty gates on view:audit alone — the guest, whose
	// own permission depends on its type (view:vm or view:container), filters
	// what is the inventory's on it instead.
	Resource string
	// Parameters are the path's own; the filters every History takes are
	// added.
	Parameters apischema.Properties
}

// historyRoutes is every History route. A new kind adds its one line here.
func historyRoutes() []historyRoute {
	return []historyRoute{
		guestHistoryRoute(),
	}
}

// historyRouteCount is not a hand-kept number like the other domains': each
// route is one line of historyRoutes, which is what registerHistoryEndpoints
// registers.
var historyRouteCount = len(historyRoutes())

// registerHistoryEndpoints declares every kind's History route.
func registerHistoryEndpoints(reg *Registry, h *handlers.HistoryHandler) {
	for _, r := range historyRoutes() {
		reg.Register(historyEndpoint(h, r))
	}
}

// historyEndpoint builds one kind's route, refusing at startup a kind whose
// type filter could not work (HistoryKind.Validate) or a route that is not a
// cluster's.
func historyEndpoint(h *handlers.HistoryHandler, r historyRoute) Endpoint {
	if err := r.Kind.Validate(); err != nil {
		panic(fmt.Sprintf("history route %s: %v", r.Path, err))
	}
	if !strings.HasPrefix(r.Path, clusterScope+"/") || !strings.HasSuffix(r.Path, "/history") {
		panic(fmt.Sprintf("history route %s: a History route is %s/…/history", r.Path, clusterScope))
	}
	audit := Check{Action: "view", Resource: "audit", Scope: ScopeCluster}
	perms := Permissions{Check: &audit}
	if r.Resource != "" {
		perms = Permissions{AllOf: []Check{audit, {Action: "view", Resource: r.Resource, Scope: ScopeCluster}}}
	}
	return Endpoint{
		Method:      fiber.MethodGet,
		Path:        r.Path,
		Description: r.Description,
		Group:       "Audit Log",
		Permissions: perms,
		Parameters:  withParams(r.Parameters, historyFilterParams(r.Kind)),
		Handler:     h.Route(r.Kind).List,
	}
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
		"source": {
			Type:     apischema.String,
			Optional: true,
			Enum:     []string{"", "nexara", "proxmox", "detected"},
			Typetext: "<nexara|proxmox|detected>",
			Description: "Narrow to what was done in Nexara, what was done in Proxmox, or the status " +
				"changes no task explains (detected). Empty or omitted returns every source.",
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
