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
// What it cannot express is pve-poolid's NESTING. A nested id contains a
// slash. A caller can send one only percent-encoded — "infra%2Fprod", which a
// browser sends intact and Fiber routes as one undecoded segment — and this
// rule refuses the "%", so as declared these routes cannot reach a nested
// pool. Admitting an encoded slash, and decoding it in the handler, would
// carry the id as far as the client, but no route rule gets it further: the
// client's "/pools/{poolid}" form cannot carry a slash (validatePathSegment
// refuses one, pveproxy would split an escaped one, and PVE's own {poolid}
// forms say "no support for nested pools"). So reaching a nested pool needs
// the client switched to the query forms PVE moved to AND the route changed —
// either an encoded-slash rule plus a decode, or, as the dot limit requires
// anyway, the id carried outside the path. Hence poolCreateIDParam below,
// which carries the id in the body and so is bound by neither constraint; it
// refuses only the two names no browser could address afterwards.
//
// The rule it carries is NOT a pool rule, and the name says so.
// path-safe-dotted-name is that charset minus exactly {".", ".."} — the two
// relative segments url.PathEscape leaves intact and a normalising proxy in
// front of pveproxy resolves upward (pveproxy itself takes them literally; see
// proxmox.validatePathSegment) — and registry_access.go's accessNamePattern
// reads the same entry for PVE group and role ids, which are the same charset
// in the same kind of path slot and needed the identical carve-out. One entry
// rather than two copies: a second spelling of it would be invisible to both
// inline duplication guards, and neither copy would be individually killable.
//
// Being stricter than verify_poolname costs one pair of names, knowingly, and
// more than the pools themselves. POST /pools no longer creates such a pool
// (poolCreateIDParam below), but one made outside Nexara can exist, and the
// VM, container and import create routes can create a guest INTO one, since
// they send `pool` as a form field; but no guest can be moved in or out of it
// afterwards, because SetVMPool goes through proxmox.UpdateResourcePool, so a
// guest created there can never be moved out through Nexara. Switching the
// client to PVE's non-deprecated forms, which take poolid as a parameter,
// would fix only the Nexara-to-PVE leg: this route still carries the id in
// its own path, and a browser resolves a "." or ".." segment before the
// request leaves, so lifting the dot limit also needs the id moved out of
// this route's path. The catalogue entry has the full account.
//
// pveproxy takes a dot segment literally, so sent straight to it
// "/pools/." addresses a pool NAMED "." — a name verify_poolname admits —
// while behind a normalising proxy the same request lands on the pool
// collection, and ".." on the API root. proxmox.validatePathSegment has
// refused both on all three addressing methods since the client guard was
// added, so a pool of either name was already unaddressable through Nexara,
// and this declaration is the same refusal one layer earlier with a message
// that names the parameter. Note that nothing on THIS side resolves it: Fiber
// routes a raw ".." straight through to this parameter, so the pattern is
// what turns it away — for a caller that sends the segment raw. A BROWSER
// never sends it: it resolves the segment first, so the SPA's request for a
// pool named ".." never reached this parameter at all, and landed on the
// cluster instead. frontend/src/lib/api-path.ts refuses to build such a path,
// and refuseTrailingSlashWrites (middleware.go) refuses the write it
// becomes. The create side now refuses the pair as well; see
// poolCreateIDParam.
func poolIDParam(source apischema.Source, description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		Source:      source,
		Pattern:     apischema.Rule("path-safe-dotted-name"),
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
// It therefore takes a pool id nesting included. Sharing poolIDParam here
// would refuse "infra/prod", a pool name PVE's own POST /pools accepts,
// purely because of a restriction that belongs to the OTHER routes' URL
// shape. That is the invented-strictness mistake optPoolID's comment
// describes, and it is easy to make precisely because one helper looks like
// it should serve both.
//
// It does NOT take pve-poolid whole, though: it carries pve-poolid-new, which
// is pve-poolid minus a value that is exactly "." or "..". That refusal is
// NEXARA's — verify_poolname admits both — and the catalogue entry says so.
// It exists because no browser can address such a pool: the URL parser every
// browser shares resolves a "." or ".." path segment before the request
// leaves, so the SPA's DELETE of a pool named ".." went out as
// DELETE /api/v1/clusters/<id>/ and removed the CLUSTER. The SPA now refuses
// to build that path and the server refuses the write it would become, and
// this stops Nexara creating the pool in the first place. A pool made outside
// Nexara under either name is still listed, and simply cannot be read,
// edited or deleted here.
//
// Only the WHOLE id is refused. "a/.." and "./b" pass: the hazard needs a
// "." or ".." SEGMENT, the SPA encodes a pool id as one segment slash and
// all ("a%2F.."), and no nested id is addressable through the per-pool routes
// anyway, which the paragraph on poolIDParam above explains.
//
// On a cluster whose pve-manager has commit 7eadbed6, Proxmox's own
// create_pool refuses both names too — it requires a new pool's name to
// start with a letter — so there this changes only which layer answers. On
// an older one it is the only thing that does.
func poolCreateIDParam(description string) apischema.Property {
	p := poolIDParam(apischema.SourceAuto, description)
	p.Pattern = apischema.Rule("pve-poolid-new")
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
			"poolid": poolCreateIDParam("Id for the new pool. May be nested, e.g. \"infra/prod\". An id that is " +
				"exactly \".\" or \"..\" is refused: no browser can send either as a path segment, so the " +
				"pool could not be read, edited or deleted here afterwards."),
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
