package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// upsWindowPerms is a UPS's maintenance-window gate: the UPS's view with the
// action on maintenance windows, both instance-wide, as every UPS route is.
func upsWindowPerms(action string) Permissions {
	return Permissions{AllOf: []Check{
		{Action: "view", Resource: "ups", Scope: ScopeGlobal},
		{Action: action, Resource: "maintenance_window", Scope: ScopeGlobal},
	}}
}

// upsWindowBody is a cluster window's body without node_id: a UPS's window
// names no node.
func upsWindowBody(required bool) apischema.Properties {
	p := maintenanceWindowParams(required)
	delete(p, "node_id")
	return p
}

// registerUpsMaintenanceWindowEndpoints declares a UPS's maintenance windows.
func registerUpsMaintenanceWindowEndpoints(reg *Registry, h *handlers.NutHandler) {
	path := upsDeviceScope + "/:id/maintenance-windows"
	ups := apischema.Properties{"id": upsDeviceIDParam}
	window := withParams(ups, apischema.Properties{"window_id": alertIDParam("Maintenance window identifier.")})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   path,
		Description: "List one UPS's maintenance windows, newest first. While one is open the UPS drops out " +
			"of the global UPS alert rules, and its own rules hold. Requires global view:ups and global " +
			"view:maintenance_window.",
		Group:       "Maintenance Windows",
		Permissions: upsWindowPerms("view"),
		Parameters:  withParams(ups, alertPageParams("windows")),
		Handler:     h.ListUpsMaintenanceWindows,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   path,
		Description: "Open a maintenance window on one UPS, for a battery swap or other work on it. Requires " +
			"global view:ups and global manage:maintenance_window; recorded in the UPS's History.",
		Group:       "Maintenance Windows",
		Permissions: upsWindowPerms("manage"),
		Parameters:  withParams(ups, upsWindowBody(true)),
		Handler:     h.CreateUpsMaintenanceWindow,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   path + "/:window_id",
		Description: "Change one of a UPS's maintenance windows. Every parameter is optional and an omitted " +
			"or EMPTY one leaves the stored value alone. A window of another UPS, or a cluster's, answers " +
			"404. Requires global view:ups and global manage:maintenance_window.",
		Group:       "Maintenance Windows",
		Permissions: upsWindowPerms("manage"),
		Parameters:  withParams(window, upsWindowBody(false)),
		Handler:     h.UpdateUpsMaintenanceWindow,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   path + "/:window_id",
		Description: "Remove one of a UPS's maintenance windows; its rules are judged again at the next " +
			"evaluation. A window of another UPS, or a cluster's, answers 404. Requires global view:ups " +
			"and global manage:maintenance_window.",
		Group:       "Maintenance Windows",
		Permissions: upsWindowPerms("manage"),
		Parameters:  window,
		Handler:     h.DeleteUpsMaintenanceWindow,
	})
}
