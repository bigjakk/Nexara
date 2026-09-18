package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — clusterScope,
// clusterCheck, clusterParams, withParams and optString — lives in
// registry_vms.go, where the first migrated domain defined it.

// poolIDParam is the REQUIRED pool id, in the one form that can be
// expressed as a single path segment.
//
// The charset is PVE's own pve-poolid (verify_poolname, pve-access-control):
// [A-Za-z0-9._-], which allows a leading digit, dot or dash. It is
// deliberately LOOSER than pve-configid, because a pool created outside
// Nexara can carry a name our own create would refuse and an unaddressable
// pool cannot be edited or deleted — the same trap snapshotNameParam
// (registry_vms.go) documents. The 100-character bound matches the
// storage-id ceiling Proxmox applies to section-config ids generally;
// pve-poolid itself imposes none.
//
// What it cannot express is pve-poolid's NESTING. A nested id
// ("infra/prod") contains a slash, so it neither matches a Fiber path
// segment nor survives the "/pools/{poolid}" form the client builds — a
// nested pool is unreachable through the three per-pool routes whatever
// pattern this carries, and the fix is the query form PVE moved to
// ("PUT /pools?poolid=…"), not a looser rule here. Hence poolCreateIDParam
// below, which is not bound by either constraint.
func poolIDParam(source apischema.Source, description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		Source:      source,
		Pattern:     `^[A-Za-z0-9._-]+$`,
		MaxLength:   apischema.Ptr(100),
		Typetext:    "<poolid>",
		Description: description,
	}
}

// poolCreateIDParam is `poolid` on POST /pools, which is a BODY parameter
// and reaches Proxmox as a form field — form.Set("poolid", …) in
// CreateResourcePool — so nothing between here and PVE has to carry it as a
// path segment.
//
// It therefore takes pve-poolid whole, nesting included. Sharing
// poolIDParam here would refuse "infra/prod", a pool name PVE's own
// POST /pools accepts, purely because of a restriction that belongs to the
// OTHER routes' URL shape. That is the invented-strictness mistake
// optPoolID's comment describes, and it is easy to make precisely because
// one helper looks like it should serve both.
func poolCreateIDParam(description string) apischema.Property {
	p := poolIDParam(apischema.SourceAuto, description)
	p.Pattern = `^[A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+){0,2}$`
	return p
}

// poolPathParams is the pair every per-pool route carries.
func poolPathParams(extra apischema.Properties) apischema.Properties {
	return clusterParams(withParams(apischema.Properties{
		"pool_id": poolIDParam(apischema.SourcePath, "Proxmox resource pool id."),
	}, extra))
}

// registerPoolEndpoints declares PoolHandler's four routes.
//
// The LIST of pools is not here: it is VMHandler's ListResourcePools, declared
// in registry_vms.go, which is why this file has a create, a get, an update and
// a delete but no listing. All four are a plain cluster-scoped Check —
// manage:pool for the three writes, view:pool for the read — and nothing about
// them is conditional.
//
// Every one of them proxies to Proxmox, so the parameter schema closes the
// request set without trying to restate Proxmox's own vocabulary: `vms`,
// `storage` and `delete` are comma-separated member lists that Proxmox parses,
// versions and rejects with a message of its own.
func registerPoolEndpoints(reg *Registry, h *handlers.PoolHandler) {
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        clusterScope + "/pools",
		Description: "Create a Proxmox resource pool.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("manage", "pool"),
		Parameters: clusterParams(apischema.Properties{
			"poolid":  poolCreateIDParam("Id for the new pool. May be nested, e.g. \"infra/prod\"."),
			"comment": optString(1024, "<string>", "Free-text comment stored on the pool."),
		}),
		Handler: h.CreatePool,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        clusterScope + "/pools/:pool_id",
		Description: "Get one resource pool with its member guests and storages.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("view", "pool"),
		Parameters:  poolPathParams(nil),
		Handler:     h.GetPool,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   clusterScope + "/pools/:pool_id",
		Description: "Update a resource pool: change its comment, add guests or storages, or remove " +
			"members with delete.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("manage", "pool"),
		Parameters: poolPathParams(apischema.Properties{
			// Optional with NO default, so p.Has tells "leave the comment
			// alone" from "clear it": the handler forwards a *string, and a
			// default here would start writing an empty comment onto every
			// pool whose editor never touched the field.
			"comment": optString(1024, "<string>", "Replacement comment. Omitted, the stored one is left alone; sent empty, it is cleared."),
			"vms":     optString(4096, "<vmid[,vmid...]>", "Guests to add to the pool, as Proxmox's comma-separated VMID list."),
			"storage": optString(4096, "<storage[,storage...]>", "Storages to add to the pool, comma-separated."),
			"delete":  optString(4096, "<member[,member...]>", "Members to REMOVE, comma-separated. Proxmox reads it alongside vms and storage."),
		}),
		Handler: h.UpdatePool,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        clusterScope + "/pools/:pool_id",
		Description: "Delete a resource pool. Proxmox refuses while the pool still has members.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("manage", "pool"),
		Parameters:  poolPathParams(nil),
		Handler:     h.DeletePool,
	})
}
