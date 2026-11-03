package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// UPS monitoring: the NUT servers Nexara reads, and the UPS devices, readings
// and power events the poller collects from them. Global scope throughout: a
// UPS powers machines rather than a cluster's objects, and one NUT server can
// feed the nodes of several clusters.
const (
	nutServerScope = "/api/v1/nut-servers"
	upsDeviceScope = "/api/v1/ups-devices"
)

var nutServerIDParam = apischema.Property{
	Type:        apischema.String,
	Format:      "uuid",
	Source:      apischema.SourcePath,
	Typetext:    "<uuid>",
	Description: "Nexara NUT server identifier.",
}

var upsDeviceIDParam = apischema.Property{
	Type:        apischema.String,
	Format:      "uuid",
	Source:      apischema.SourcePath,
	Typetext:    "<uuid>",
	Description: "Nexara UPS device identifier.",
}

// nutConnectionParams are the fields that say how to reach a NUT server.
// forCreate adds the defaults create and probe read through plain accessors.
// An update declares none: it reads every field through an Opt accessor, and
// an omitted field keeps the stored value.
func nutConnectionParams(forCreate bool) apischema.Properties {
	host := apischema.Property{
		Type:        apischema.String,
		MinLength:   apischema.Ptr(1),
		MaxLength:   apischema.Ptr(253),
		Typetext:    "<hostname|ip>",
		Description: "Hostname or IP address of the NUT server, with no scheme or port.",
	}
	port := apischema.Property{
		Type:        apischema.Integer,
		Optional:    true,
		Minimum:     apischema.Ptr(1.0),
		Maximum:     apischema.Ptr(65535.0),
		Typetext:    "<1-65535>",
		Description: "upsd's port.",
	}
	tlsMode := apischema.Property{
		Type:     apischema.String,
		Optional: true,
		Enum:     []string{"off", "starttls"},
		Typetext: "<off|starttls>",
		Description: "starttls upgrades the connection before any credential is sent, and fails rather " +
			"than falling back to plain text if the server does not offer it.",
	}
	verifyTLS := apischema.Property{
		Type:     apischema.Boolean,
		Optional: true,
		Typetext: "<boolean>",
		Description: "With starttls and no tls_fingerprint, verify the server's certificate against the " +
			"system CA pool. A pinned fingerprint always wins over this.",
	}
	if forCreate {
		port.Default = 3493
		port.Description += " Defaults to 3493."
		tlsMode.Default = "off"
		verifyTLS.Default = true
	} else {
		host.Optional = true
	}
	return apischema.Properties{
		"host":       host,
		"port":       port,
		"tls_mode":   tlsMode,
		"verify_tls": verifyTLS,
		"username": optString(255, "<user>",
			"An account from upsd.users. Reads need none on most servers. Returned only to manage:ups holders."),
		// Declared so checkMisplaced refuses it as a query parameter, where
		// proxies and access logs would record it. Never returned, never audited.
		"password": {
			Type:        apischema.String,
			Optional:    true,
			MaxLength:   apischema.Ptr(1024),
			Typetext:    "<secret>",
			Description: "Password for that account. Stored encrypted and never returned.",
		},
		"tls_fingerprint": optString(128, "<SHA256 fingerprint>",
			"SHA-256 fingerprint of the leaf certificate upsd presents after STARTTLS, to pin."),
		"allow_private_address": optFlag(
			"Confirm a host that resolves to a private or loopback address, as a NUT server on the LAN does."),
		"acknowledge_cleartext_credentials": optFlag(
			"Confirm storing a password for a server reached without TLS, where it crosses the network in plain text."),
		"acknowledge_insecure_tls": optFlag(
			"Confirm storing a password for a STARTTLS server whose certificate is neither pinned nor verified."),
	}
}

func nutServerNameParam(optional bool) apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		Optional:    optional,
		MinLength:   apischema.Ptr(1),
		MaxLength:   apischema.Ptr(64),
		Typetext:    "<string>",
		Description: "Display name for the server, unique among NUT servers.",
	}
}

// registerUPSEndpoints declares the routes served by NutHandler. connect is
// one limiter instance shared by every route that dials a NUT server, so the
// budget is per caller across all of them.
func registerUPSEndpoints(reg *Registry, h *handlers.NutHandler, connect fiber.Handler) {
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   nutServerScope,
		Description: "Register a NUT server. It is probed before it is stored, so an address that does " +
			"not answer is a failed create.",
		Group:       "Power",
		Permissions: globalCheck("manage", "ups"),
		RateLimiter: connect,
		Parameters:  withParams(nutConnectionParams(true), apischema.Properties{"name": nutServerNameParam(false)}),
		Handler:     h.Create,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   nutServerScope + "/probe",
		Description: "Test a connection without saving it, and report the server's versions and the UPS " +
			"devices it serves. Grants nothing creating the server would not.",
		Group:       "Power",
		Permissions: globalCheck("manage", "ups"),
		RateLimiter: connect,
		Parameters:  nutConnectionParams(true),
		Handler:     h.Probe,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        nutServerScope,
		Description: "List the registered NUT servers. The password is never returned, and the username only to a manage:ups holder.",
		Group:       "Power",
		Permissions: globalCheck("view", "ups"),
		Handler:     h.List,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        nutServerScope + "/:id",
		Description: "Get one NUT server's connection details and poll status.",
		Group:       "Power",
		Permissions: globalCheck("view", "ups"),
		Parameters:  apischema.Properties{"id": nutServerIDParam},
		Handler:     h.Get,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   nutServerScope + "/:id",
		Description: "Change a NUT server. Every parameter is optional and an omitted one is left as it " +
			"is. A change to how Nexara connects re-probes; renaming or toggling enabled does not. Moving " +
			"the address of a server with a stored password requires re-entering the password.",
		Group:       "Power",
		Permissions: globalCheck("manage", "ups"),
		RateLimiter: connect,
		Parameters: withParams(nutConnectionParams(false), apischema.Properties{
			"id":                nutServerIDParam,
			"name":              nutServerNameParam(true),
			"enabled":           {Type: apischema.Boolean, Optional: true, Typetext: "<boolean>", Description: "Poll this server."},
			"clear_credentials": optFlag("Remove the stored username and password."),
		}),
		Handler: h.Update,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        nutServerScope + "/:id",
		Description: "Remove a NUT server, its stored credential, and every UPS reading and power event collected from it.",
		Group:       "Power",
		Permissions: globalCheck("delete", "ups"),
		Parameters:  apischema.Properties{"id": nutServerIDParam},
		Handler:     h.Delete,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   nutServerScope + "/:id/test",
		Description: "Test the stored connection and report what the server serves. Persists nothing. On " +
			"manage rather than view because it connects with the stored credential.",
		Group:       "Power",
		Permissions: globalCheck("manage", "ups"),
		RateLimiter: connect,
		Parameters:  apischema.Properties{"id": nutServerIDParam},
		Handler:     h.Test,
	})

	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   upsDeviceScope,
		Description: "List the UPS devices the NUT servers report, with their state, latest reading and " +
			"health issues. A device Nexara could not read recently says so in reachability and " +
			"reading_stale rather than showing an old reading as current.",
		Group:       "Power",
		Permissions: globalCheck("view", "ups"),
		Handler:     h.ListDevices,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        upsDeviceScope + "/:id",
		Description: "Get one UPS with every variable upsd reported and upsd's description of each.",
		Group:       "Power",
		Permissions: globalCheck("view", "ups"),
		Parameters:  apischema.Properties{"id": upsDeviceIDParam},
		Handler:     h.GetDevice,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   upsDeviceScope + "/:id/metrics",
		Description: "A UPS's readings over a window ending now, in at most 360 buckets. Each bucket keeps " +
			"the lowest charge, runtime and input voltage, the highest load and temperature, and whether " +
			"the UPS was on battery at any point in it.",
		Group:       "Power",
		Permissions: globalCheck("view", "ups"),
		Parameters: apischema.Properties{
			"id": upsDeviceIDParam,
			"range": {
				Type:     apischema.String,
				Optional: true,
				Default:  "1h",
				Enum:     []string{"1h", "6h", "24h", "7d", "30d"},
				Typetext: "<1h|6h|24h|7d|30d>",
				Description: "Window to report. 1h and 6h come from the raw readings, 24h from the 5-minute " +
					"rollup, 7d and 30d from the hourly one.",
			},
		},
		Handler: h.GetDeviceMetrics,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   "/api/v1/ups-events",
		Description: "Power events, newest first: outages, low battery, overload, bypass, voltage " +
			"correction, a battery needing replacement, lost contact and self-test results. An event " +
			"whose end Nexara did not see has end_observed false: after a gap in contact, or, with " +
			"details.ended_by polling_off or removed, because Nexara stopped watching while it was still going.",
		Group:       "Power",
		Permissions: globalCheck("view", "ups"),
		Parameters: apischema.Properties{
			"ups_id": {Type: apischema.String, Optional: true, Format: "uuid", Typetext: "<uuid>", Description: "Only this UPS's events."},
			"kind": {
				Type:     apischema.String,
				Optional: true,
				Enum: []string{"on_battery", "low_battery", "overload", "bypass", "voltage_correction",
					"replace_battery", "unreachable", "stale", "self_test"},
				Typetext:    "<kind>",
				Description: "Only events of this kind.",
			},
			"limit": {
				Type: apischema.Integer, Optional: true, Default: 50,
				Minimum: apischema.Ptr(1.0), Maximum: apischema.Ptr(200.0),
				Typetext: "<1-200>", Description: "Page size. Defaults to 50.",
			},
			"before": {
				Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(64), Typetext: "<RFC 3339 timestamp>",
				Description: "With before_id, return the page after the event with this started_at.",
			},
			"before_id": {Type: apischema.String, Optional: true, Format: "uuid", Typetext: "<uuid>", Description: "The id of the last event on the previous page."},
		},
		Handler: h.ListEvents,
	})

	// What a UPS powers, by hand. Deferred: the global manage:ups and, for a
	// node, view:node on its cluster are checked in the handler, so a node is
	// looked up only for a caller allowed to know whether it exists.
	feedReason := "manage:ups, and for a node view:node on its cluster, checked in the handler before the node is looked up"
	nodeParam := func(description string) apischema.Property {
		p := apischema.StdOption("node-name")
		p.Optional, p.Description = true, description
		return p
	}
	clusterParam := func(description string) apischema.Property {
		return apischema.Property{Type: apischema.String, Optional: true, Format: "uuid", Typetext: "<uuid>", Description: description}
	}
	feedIDParam := apischema.Property{
		Type: apischema.String, Format: "uuid", Source: apischema.SourcePath, Typetext: "<uuid>",
		Description: "A feed's identifier, as the device lists it.",
	}
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   upsDeviceScope + "/:id/feeds",
		Description: "Add something the UPS powers by hand: a node (feed_cluster_id and node_name), or a load that is not " +
			"a node (label), such as a switch. A node is found on its own once a upsmon on it logs in. A node " +
			"already powered is a 409. Requires manage:ups, and view:node on the node's cluster.",
		Group:       "Power",
		Permissions: Permissions{Deferred: feedReason},
		NodesCheckedByHandler: map[string]string{
			"node_name": "checked against feed_cluster_id after view:node on that cluster is authorized",
		},
		Parameters: apischema.Properties{
			"id":              upsDeviceIDParam,
			"feed_cluster_id": clusterParam("The node's cluster."),
			"node_name":       nodeParam("The node the UPS powers."),
			"label": {
				Type: apischema.String, Optional: true, MinLength: apischema.Ptr(1), MaxLength: apischema.Ptr(64),
				Typetext: "<text>", Description: "A load that is not a node, such as a switch.",
			},
		},
		Handler: h.AddFeed,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   upsDeviceScope + "/:id/feeds/:feed_id",
		Description: "Say whether a node the UPS powers shuts down some way Nexara cannot see: its upsmon is behind " +
			"NAT or a proxy, or another tool shuts it down. Its verdict then reads checked elsewhere instead of a " +
			"warning. Requires manage:ups, and view:node on the node's cluster.",
		Group:       "Power",
		Permissions: Permissions{Deferred: feedReason},
		Parameters: apischema.Properties{
			"id":      upsDeviceIDParam,
			"feed_id": feedIDParam,
			"protected_elsewhere": {
				Type: apischema.Boolean, Typetext: "<boolean>",
				Description: "True when the node shuts down some way Nexara cannot see.",
			},
		},
		Handler: h.UpdateFeed,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   upsDeviceScope + "/:id/feeds/:feed_id",
		Description: "Remove something the UPS powers. A node is kept as removed, so a login from it does not add it " +
			"again; only adding it back by hand does. Requires manage:ups, and view:node on the node's cluster.",
		Group:       "Power",
		Permissions: Permissions{Deferred: feedReason},
		Parameters:  apischema.Properties{"id": upsDeviceIDParam, "feed_id": feedIDParam},
		Handler:     h.RemoveFeed,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   nutServerScope + "/:id/host-node",
		Description: "Say which node the NUT server runs on, so its own upsmon (a loopback login) counts for that " +
			"node; or set detect to hand it back to the poller, which finds it from the address it reaches. A " +
			"change of the server's address forgets it. Requires manage:ups, and view:node on every cluster: a " +
			"server has one host node, which may be in any cluster.",
		Group:       "Power",
		Permissions: Permissions{Deferred: feedReason},
		NodesCheckedByHandler: map[string]string{
			"node_name": "checked against host_cluster_id after view:node on that cluster is authorized",
		},
		Parameters: apischema.Properties{
			"id":              nutServerIDParam,
			"host_cluster_id": clusterParam("The node's cluster."),
			"node_name":       nodeParam("The node the NUT server runs on."),
			"detect":          optFlag("Forget the host node and let the poller find it."),
		},
		Handler: h.SetHostNode,
	})
}
