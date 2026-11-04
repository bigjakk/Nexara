package api

import (
	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// upsDeviceHistoryRoute is a UPS's History. Global, gated on global view:audit
// and global view:ups, as every UPS route is: a UPS is read through a NUT
// server no one cluster owns. It opens on the window and reads no more rows
// than a node's does (handlers.WithObjectWindow): a UPS that keeps losing
// contact records an episode each time.
func upsDeviceHistoryRoute() historyRoute {
	return historyRoute{
		Kind: handlers.WithObjectWindow(handlers.UpsDeviceHistory()),
		Path: upsDeviceScope + "/:id/history",
		Description: "List what happened to one UPS, newest first: its power events (on battery, low battery, " +
			"overload, on bypass, voltage correction, battery to replace, contact lost, no fresh data, a " +
			"self-test), each command run and variable set on it through Nexara, and each change to what it " +
			"powers. A power event is listed at its start, with its end and readings. Requires global " +
			"view:audit and global view:ups.",
		Resource:   "ups",
		Parameters: apischema.Properties{"id": upsDeviceIDParam},
	}
}
