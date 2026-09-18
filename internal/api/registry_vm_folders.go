package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — clusterScope,
// clusterCheck, clusterParams, vmParams and withParams — lives in
// registry_vms.go, where the first migrated domain defined it.

// vmFolderScope is the collection the folder routes hang off.
const vmFolderScope = clusterScope + "/vm-folders"

// vmFolderIDParam is a folder's row id as a PATH parameter.
var vmFolderIDParam = apischema.Property{
	Type:        apischema.String,
	Format:      "uuid",
	Source:      apischema.SourcePath,
	Typetext:    "<uuid>",
	Description: "Folder identifier.",
}

// registerVMFolderEndpoints declares 4 of VMFoldersHandler's 5 routes.
//
// All four are a plain cluster-scoped Check — view:vm_folder for the listing,
// manage:vm_folder for the three writes — and none of them is conditional.
//
// The FIFTH, PATCH /vm-folders/:folder_id, stays in router.go. It is blocked by
// a gap in apischema rather than by a permission: its `parent_id` is a
// THREE-state field (absent = leave the folder where it is, explicit null =
// move it to the top level, a uuid = move it under that folder), which the
// handler decodes with a custom jsonNullUUID. apischema's present() reads an
// explicit JSON null as ABSENT (validate.go), so a declaration would silently
// turn "move this folder to the top level" into a no-op — a request that
// answers 200 and changes nothing. See TestVMFolderReparentIsStillLegacy, which
// pins that as a decision, and the report accompanying this change, which asks
// for the vocabulary.
//
// What stays in the handlers is everything a declaration cannot see: the
// cluster-membership check on every folder and guest the request names (a
// folder from another cluster answers 404, not 403), the cycle check on a
// re-parent, and the unique-violation mapping onto 409.
func registerVMFolderEndpoints(reg *Registry, h *handlers.VMFoldersHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   vmFolderScope,
		Description: "List every folder in the cluster plus every (vm_id, folder_id) membership, so the " +
			"tree can be rendered from one round trip.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("view", "vm_folder"),
		Parameters:  clusterParams(nil),
		Handler:     h.List,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        vmFolderScope,
		Description: "Create a folder, optionally under an existing one. A duplicate name at the same level is refused.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("manage", "vm_folder"),
		Parameters: clusterParams(apischema.Properties{
			"name": {
				Type:      apischema.String,
				MinLength: apischema.Ptr(1),
				// The handler trims the name and then applies its own 1..128
				// rule, which is the authority — this bound is here to close
				// the parameter rather than to restate it. The one shape the
				// two disagree on is a name padded past 128 characters with
				// whitespace, which the schema now refuses and the handler
				// would have trimmed; that is a narrowing of a value nobody
				// sends, and the message names the field either way.
				MaxLength: apischema.Ptr(128),
				Typetext:  "<string>",
				Description: "Folder name, unique among its siblings. Leading and trailing whitespace is " +
					"trimmed, and a name that is only whitespace is refused.",
			},
			// Absent and an explicit null mean the same thing HERE — "top
			// level" — which is exactly how the handler reads its *uuid.UUID,
			// so apischema's present() treating null as absent costs this route
			// nothing. It is the PATCH, where the two must differ, that cannot
			// be declared.
			"parent_id": {
				Type:        apischema.String,
				Optional:    true,
				Format:      "uuid",
				Typetext:    "<uuid>",
				Description: "Folder to create this one under. Omitted, or sent as null, creates it at the top level.",
			},
		}),
		Handler: h.Create,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   vmFolderScope + "/:folder_id",
		Description: "Delete a folder. Child folders and memberships cascade away; the guests themselves " +
			"are untouched and fall back to the implicit \"unassigned\" group.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("manage", "vm_folder"),
		Parameters:  clusterParams(apischema.Properties{"folder_id": vmFolderIDParam}),
		Handler:     h.Delete,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   clusterScope + "/vms/:vm_id/folder",
		Description: "Move a guest into a folder, or out of the one it is in. The guest and the folder must " +
			"both belong to the cluster in the path.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("manage", "vm_folder"),
		Parameters: vmParams(apischema.Properties{
			// As on the create: absent and null both mean "unassign", which is
			// how the handler already reads its *uuid.UUID.
			"folder_id": {
				Type:        apischema.String,
				Optional:    true,
				Format:      "uuid",
				Typetext:    "<uuid>",
				Description: "Folder to move the guest into. Omitted, or sent as null, removes it from its current folder.",
			},
		}),
		Handler: h.AssignVM,
	})
}
