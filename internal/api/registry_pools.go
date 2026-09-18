package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — clusterScope,
// clusterCheck, clusterParams, withParams and optString — lives in
// registry_vms.go, where the first migrated domain defined it.

// poolIDParam is a Proxmox resource pool id.
//
// It is deliberately LOOSER than the pve-configid format: Proxmox's own
// pve-poolid allows a leading digit and a dot, and a pool created outside
// Nexara can therefore carry a name our own create would refuse. Tightening it
// here would make such a pool un-gettable, un-editable and un-deletable through
// this API, which is the same trap snapshotNameParam (registry_vms.go)
// documents. The bound is 100 characters, matching the storage-id ceiling
// Proxmox applies to section-config ids generally.
func poolIDParam(source apischema.Source, description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		Source:      source,
		Pattern:     `^[A-Za-z0-9][A-Za-z0-9._-]*$`,
		MaxLength:   apischema.Ptr(100),
		Typetext:    "<poolid>",
		Description: description,
	}
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
			"poolid":  poolIDParam(apischema.SourceAuto, "Id for the new pool."),
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
