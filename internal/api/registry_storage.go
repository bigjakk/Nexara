package api

import (
	"slices"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary — clusterScope, clusterCheck,
// clusterParams, withParams, optFlag, optString and optTristateBool —
// lives in registry_vms.go, where the first migrated domain defined it.

// storageScope is the collection every per-storage route hangs off.
// :cluster_id is the FIRST path parameter, which namesACluster
// (permissions.go) requires of a cluster-scoped Check.
const storageScope = clusterScope + "/storage"

// storageUploadPath is the storage upload route. The 10 MiB body-size guard,
// the refusal of chunked bodies and the body read deadline exempt a streamed
// multipart body sent to this route alone — one UploadFile streams, and
// nothing reads before it has checked the caller's grants — and
// isStreamedUpload (body_framing.go) decides that by matching a request
// against this same constant, so a change to the route's path moves its
// exemptions with it.
const storageUploadPath = storageScope + "/:storage_id/upload"

// storageRowParams is the pair every per-storage route carries: the
// cluster and the storage pool's NEXARA row id.
//
// The description says which identifier it is, and that is not padding: an
// external consumer specifically complained that :storage_id is
// unguessable, because every other storage endpoint in the Proxmox world
// takes the pool NAME ("store01") and this one takes a uuid Nexara
// assigned. The listing that yields it is named so the docs answer "where
// do I get one" without a support round trip.
func storageRowParams(extra apischema.Properties) apischema.Properties {
	return withParams(apischema.Properties{
		"cluster_id": apischema.StdOption("cluster-id"),
		"storage_id": {
			Type:     apischema.String,
			Format:   "uuid",
			Typetext: "<uuid>",
			Description: "Nexara storage identifier — the `id` field of a row from " +
				"GET /clusters/{cluster_id}/storage. This is NOT the Proxmox storage name " +
				"(\"store01\"), which appears on the same row as `storage`.",
		},
	}, extra)
}

// generatedKeyDoc is the half of the storage create and update descriptions
// that documents their one optional response field, generated_encryption_key.
// The Endpoint has no response schema, so the description is where
// /api/v1/api-docs publishes it.
//
// Proxmox generates the key when params.encryption-key is "autogen" (on_add_hook
// and on_update_hook in pve-storage src/PVE/Storage/PBSPlugin.pm) and returns it
// in that one answer; see handlers.storageWriteResponse for why Nexara hands it
// on and keeps no copy.
const generatedKeyDoc = "For a pbs pool, params.encryption-key set to autogen makes Proxmox generate a new " +
	"client encryption key, and only then does the response carry it, once, as " +
	"generated_encryption_key — a key the caller supplied is not echoed back. Nexara keeps no copy " +
	"of the key and Proxmox keeps one only on the cluster, so save it elsewhere: without it, " +
	"backups encrypted with it cannot be restored if the cluster is lost."

// storageUploadReason is the Deferred justification for the one route in
// this domain that cannot hoist.
//
// UploadFile resolves BOTH manage:storage and manage:vm_import up front
// with hasClusterPerm, refuses a caller holding neither, and then enforces
// per-content once the multipart stream reveals what is being uploaded (see
// uploadContentAllowed). The content type does not exist until the body is
// being read, so no middleware could make that decision.
const storageUploadReason = "the permission depends on what the multipart stream turns out to carry, " +
	"which does not exist until the body is being read: the handler resolves manage:storage and " +
	"manage:vm_import up front with hasClusterPerm and then enforces per-content — an ISO or CT " +
	"template needs manage:storage, an OVA (import) either grant (see uploadContentAllowed)"

// registerStorageEndpoints declares the 12 storage routes served by
// StorageHandler that the registry can express.
//
// Eleven are plain cluster-scoped Checks — one static requireClusterPerm
// each, hoisted into middleware. The twelfth, the upload, is Deferred; see
// storageUploadReason.
//
// A THIRTEENTH route stays in router.go and is deliberately not declared
// here: DELETE .../storage/:storage_id/content/* takes the volume id as a
// greedy WILDCARD, and checkPathParams refuses one outright — a wildcard
// segment is the one piece of a path that reaches a handler unvalidated
// and un-normalized, which is exactly where path traversal lives, and the
// parameter schema has no way to describe it. Making it declarable means
// changing the route's shape (a :volume segment, which every caller's
// percent-encoding would have to agree on), which is a compatibility
// decision rather than a migration. It is flagged for separate scoping and
// keeps its hand-placed delete:storage check.
//
// Nothing here is Advisory: no listing in this domain filters through
// accessibleClusters — every one resolves one cluster from its own path
// and gates on it.
func registerStorageEndpoints(reg *Registry, h *handlers.StorageHandler) {
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        storageScope,
		Description: "List the storage pools Nexara has collected for this cluster, with their type, content kinds and capacity.",
		Group:       "Storage",
		Permissions: clusterCheck("view", "storage"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListByCluster,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   storageScope,
		Description: "Add a storage pool to the cluster. The plugin-specific settings go in params, whose " +
			"accepted keys are Proxmox's own for the chosen type. " + generatedKeyDoc,
		Group:       "Storage",
		Permissions: clusterCheck("manage", "storage"),
		Parameters:  clusterParams(createStorageParams()),
		Handler:     h.Create,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        storageScope + "/:storage_id/config",
		Description: "Read a storage pool's Proxmox-level configuration — its backing paths, servers and options.",
		Group:       "Storage",
		Permissions: clusterCheck("view", "storage"),
		Parameters:  storageRowParams(nil),
		Handler:     h.GetConfig,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   storageScope + "/:storage_id",
		Description: "Change a storage pool's settings. Keys absent from params are left as they are; " +
			"delete names the ones to clear. " + generatedKeyDoc,
		Group:       "Storage",
		Permissions: clusterCheck("manage", "storage"),
		Parameters:  storageRowParams(updateStorageParams()),
		Handler:     h.Update,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   storageScope + "/:storage_id",
		Description: "Remove a storage pool from the cluster. Proxmox detaches the storage; the data on the " +
			"backing device is left alone.",
		Group:       "Storage",
		Permissions: clusterCheck("delete", "storage"),
		Parameters:  storageRowParams(nil),
		Handler:     h.Delete,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        storageScope + "/:storage_id/content",
		Description: "List what a storage pool holds — ISOs, templates, disk images and backups — with each volume's id, size and format.",
		Group:       "Storage",
		Permissions: clusterCheck("view", "storage"),
		Parameters:  storageRowParams(nil),
		Handler:     h.GetContent,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   storageUploadPath,
		// Deferred renders as the bare word "deferred", so the permission an
		// operator building a role needs is spelled out here.
		Description: "Upload an ISO, CT template or OVA to a storage pool as multipart/form-data, with the " +
			"fields in the order content, filesize, file. The body is streamed straight to Proxmox rather " +
			"than buffered. Requires manage:storage for an ISO or CT template, and either manage:storage or " +
			"manage:vm_import for an OVA (content=import).",
		Group:       "Storage",
		Permissions: Permissions{Deferred: storageUploadReason},
		// NO body parameters, deliberately. Fiber runs with StreamRequestBody
		// and DisablePreParseMultipartForm so an ISO is never buffered, and
		// c.Body() defeats both — fasthttp drains the whole stream into one
		// buffer, however large (BodyLimit caps no drain of a streamed body),
		// AND closes it, so the handler, which reads the stream itself, would
		// find none and fall back to that buffer — the whole upload held in
		// memory. bodyValues gates on the Content-Type header BEFORE asking
		// for the body, and multipart/form-data is not JSON, so nothing here
		// touches it. The three form fields are read out of the multipart
		// stream by the handler, which is the only place they exist. A JSON
		// body sent here IS read — up to 64 KiB, before UploadFile checks any
		// grant — and so is a +json type such as multipart/form-data+json,
		// which bodyValues reads as JSON whatever comes before the suffix.
		// That is why the body bounds exempt a multipart body of no JSON
		// type sent to this route, and nothing else (isStreamedUpload).
		Parameters: storageRowParams(nil),
		Handler:    h.UploadFile,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   storageScope + "/:storage_id/oci-pull",
		Description: "Pull an OCI image into a storage pool as a CT template. Runs as a background skopeo " +
			"task on the node, and needs Proxmox VE 9.1 or newer with skopeo installed.",
		Group:       "Storage",
		Permissions: clusterCheck("manage", "storage"),
		Parameters: storageRowParams(apischema.Properties{
			"reference": {
				Type:      apischema.String,
				MinLength: apischema.Ptr(1),
				// The client's own cap, restated so the rejection names the
				// field instead of arriving as a bare upstream sentence.
				MaxLength: apischema.Ptr(512),
				Typetext:  "<registry/repo:tag>",
				Description: "OCI image reference to pull, e.g. docker.io/library/alpine:3.20. " +
					"The character vocabulary is enforced by the Proxmox client, which owns it.",
			},
			"file_name": optString(64, "<filename>",
				"Name to store the template under. Omitted lets Proxmox derive one from the reference."),
		}),
		Handler: h.PullOCI,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   storageScope + "/:storage_id/download-url",
		Description: "Download a URL into a storage pool as an ISO, CT template or OVA. The NODE makes the " +
			"outbound request, and it runs as a background task.",
		Group:       "Storage",
		Permissions: clusterCheck("manage", "storage"),
		Parameters:  storageRowParams(downloadURLParams()),
		Handler:     h.DownloadURL,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   storageScope + "/:storage_id/appliances",
		Description: "Download one of Proxmox's catalogue appliance templates into a storage pool. The " +
			"storage must have vztmpl content enabled.",
		Group:       "Storage",
		Permissions: clusterCheck("manage", "storage"),
		Parameters: storageRowParams(apischema.Properties{
			"template": {
				Type:      apischema.String,
				MinLength: apischema.Ptr(1),
				// The client's own cap, restated so the rejection names the field.
				MaxLength:   apischema.Ptr(255),
				Typetext:    "<template>",
				Description: "Template identifier, as the `template` field of a row from GET /clusters/{cluster_id}/appliances.",
			},
		}),
		Handler: h.DownloadAppliance,
	})

	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   clusterScope + "/appliances",
		Description: "List Proxmox's official appliance template catalogue. Identical on every node, so " +
			"Nexara reads it from whichever one is online.",
		Group:       "Storage",
		Permissions: clusterCheck("view", "storage"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListAppliances,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   clusterScope + "/scan/iscsi",
		Description: "Discover the target IQNs an iSCSI portal advertises, so the storage dialog can offer " +
			"them instead of making the operator type one. Requires manage:storage rather than view:storage: " +
			"discovery makes a node open an outbound connection to a caller-supplied address.",
		Group:       "Storage",
		Permissions: clusterCheck("manage", "storage"),
		Parameters: clusterParams(apischema.Properties{
			"portal": {
				Type:      apischema.String,
				MinLength: apischema.Ptr(1),
				// The client's own cap. The rest of the rule — no whitespace,
				// no control characters, no "/?#" — stays in
				// proxmox.ValidateISCSIPortal, which owns it and answers with a
				// message of its own; a pattern here could only re-state half
				// of it and would then drift.
				MaxLength:   apischema.Ptr(255),
				Typetext:    "<host|host:port>",
				Description: "Portal address to run discovery against, as a host or host:port.",
			},
		}),
		Handler: h.ScanISCSI,
	})
}

// createStorageParams is the body of POST /clusters/:cluster_id/storage.
//
// Two parameters are required, matching exactly what the handler refused:
// an empty `storage`, and a `type` outside validStorageTypes. `params` is
// optional — a plugin with no required settings is created with none, and
// the handler's loop over a nil map does nothing.
func createStorageParams() apischema.Properties {
	return apischema.Properties{
		"storage": {
			Type: apischema.String,
			// The Proxmox storage id the pool will be known by, so it takes
			// the same format every other storage-id parameter in this API
			// does rather than a bespoke rule.
			Format:      "storage-id",
			Typetext:    "<storage>",
			Description: "Proxmox storage id for the new pool, e.g. store01.",
		},
		"type": {
			Type: apischema.String,
			// Cloned rather than aliased, for the reason the VM status
			// route's enum gives: Property.Enum is only deep-copied on the
			// StdOption path, so sharing the package-level slice would give
			// every Server's schema the same backing array.
			//
			// An Enum here is exactly the membership check the handler made —
			// this vocabulary is the plugin set Nexara's own storage dialog
			// knows how to fill in, not a value Proxmox versions underneath
			// us, so unlike a raidlevel or an image format it is ours to
			// close.
			Enum:        slices.Clone(handlers.StorageTypes),
			Typetext:    "<dir|nfs|cifs|lvm|lvmthin|zfspool|iscsi|iscsidirect|rbd|cephfs|glusterfs|btrfs|pbs>",
			Description: "Proxmox storage plugin to create the pool with.",
		},
		"params": storagePluginParams(
			"Plugin-specific settings, as a flat object of string values — path for dir, server and " +
				"export for nfs, vgname for lvm, and so on. An empty value is dropped rather than sent, " +
				"and \"storage\" and \"type\" are ignored here because they are named above."),
	}
}

// updateStorageParams is the body of PUT /clusters/:cluster_id/storage/:storage_id.
//
// BOTH parameters are optional, which is what the handler enforced: it
// bound the body and required nothing, so a PUT with an empty object is a
// no-op rather than an error. That is worth stating rather than tidying
// into a required `params`, because a caller sending only `delete` is a
// working request.
//
// `delete` stays a string, the shape the storage dialog has always sent: a
// list separated by commas, semicolons or spaces, as Proxmox's own lists are
// (proxmox.SplitStorageDeleteList). Its declaration is a bound, not a
// vocabulary: which settings may be cleared is Proxmox's to decide per plugin,
// and it refuses the rest itself, as the released handler relied on when it
// forwarded the list untouched. What proxmox.UpdateStorage adds is the one
// cross-field rule apischema cannot state — a setting params also writes is
// refused — and the refusal of a "delete" smuggled in among the settings.
func updateStorageParams() apischema.Properties {
	return apischema.Properties{
		"params": storagePluginParams(
			"Settings to write, as a flat object of string values. Keys absent from it are left as they " +
				"are, an empty value is dropped rather than sent, and \"storage\" and \"type\" are ignored: " +
				"Proxmox marks the backend-identifying options of several plugins fixed and refuses a PUT " +
				"that carries them. A \"delete\" key is refused: clearing goes through delete."),
		"delete": optString(1024, "<key>[,<key>...]",
			"Settings to clear, separated by commas — or semicolons or spaces, as in Proxmox's own lists. "+
				"Proxmox refuses a name that is not a setting of the pool's type, and one it requires or "+
				"fixes. encryption-key removes a pbs pool's client encryption key, so its backups from then "+
				"on are not encrypted — and the ones already made can only be restored with a copy of the "+
				"removed key. Clearing a setting params also sets is refused. Omitted or empty clears nothing."),
	}
}

// storagePluginParams is the free-form settings object both storage bodies
// carry.
//
// It is an Object with no nested schema on purpose: the accepted keys are
// Proxmox's, they differ per plugin type, and they grow with every PVE
// release — STORAGE_TYPE_FIELDS in the frontend is a rendering hint, not a
// contract this API could enforce without dating. What the declaration
// does buy is that the settings can only arrive HERE: a caller cannot
// spread them across the top level of the body, because an undeclared
// top-level key is now rejected by name instead of being silently dropped.
func storagePluginParams(description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.Object,
		Optional:    true,
		Typetext:    "<object>",
		Description: description,
	}
}

// downloadURLParams is the body of POST .../storage/:storage_id/download-url.
//
// Two parameters are required, and the second one is the reason to say so:
// `content` was refused outside its three values by BOTH the handler and
// the client, so the Enum is exactly the check that was there. `url` was
// never checked by the handler but IS required by the client, which
// answers ErrInvalidInput for an empty one — so it is required here, and
// the rejection now names the field instead of arriving as a bare upstream
// sentence.
//
// `filename` is NOT required, and that is deliberate rather than an
// oversight: an OVA import may omit it and the handler derives one from
// the URL path, which is the whole point of deriveURLFilename. The
// remaining rules — the extension Proxmox requires per content type, and
// everything ValidateStorageFilename refuses — stay where they are.
func downloadURLParams() apischema.Properties {
	return apischema.Properties{
		"url": {
			Type:      apischema.String,
			MinLength: apischema.Ptr(1),
			// The client's own cap, restated so the rejection names the field.
			MaxLength: apischema.Ptr(2048),
			Typetext:  "<url>",
			Description: "URL the NODE will fetch. No format: Proxmox accepts what its own downloader " +
				"accepts, and this endpoint has never narrowed it.",
		},
		"content": {
			Type: apischema.String,
			// Cloned rather than aliased; see createStorageParams' type.
			Enum:        slices.Clone(handlers.StorageDownloadContents),
			Typetext:    "<iso|vztmpl|import>",
			Description: "What the download is. The storage must have this content kind enabled.",
		},
		"filename": optString(255, "<filename>",
			"Name to store the file under. Omitted or empty derives one from the URL's path, which is "+
				"what the OVA import wizard relies on."),
		"checksum": {
			Type:      apischema.String,
			Optional:  true,
			MaxLength: apischema.Ptr(256),
			// Deliberately NOT declared with Requires:
			// []string{"checksum_algorithm"}, tempting as that is. Requires
			// asks whether the caller SUPPLIED the parameter, and apischema
			// counts the empty string as supplied — while the rule the client
			// actually enforces is `if params.Checksum != ""`. So a caller
			// sending checksum:"" as a placeholder, which this API has always
			// ignored, would start getting a 400 for a request that worked.
			// The pairing stays in DownloadURLToStorage, which owns it and
			// whose message already names both halves.
			Typetext: "<hex digest>",
			Description: "Expected digest of the downloaded file. Sending a non-empty one requires " +
				"checksum_algorithm as well; an empty value is ignored.",
		},
		"checksum_algorithm": optString(16, "<md5|sha1|sha224|sha256|sha384|sha512>",
			"Digest algorithm for checksum. Proxmox owns the vocabulary and rejects one it does not know."),
		"decompression_algorithm": optString(16, "<gz|lzo|zst|bz2>",
			"Decompress the download on arrival. Omitted or empty stores it as fetched."),
		// A tristate rather than a flag: the client sends the key only when
		// the caller chose, and Proxmox's own default is to verify. A Default
		// of false here would start disabling certificate verification on
		// every download whose requester never mentioned it.
		"verify_certificates": optTristateBool(
			"Verify the TLS certificate of the URL's host. Omitted leaves Proxmox's default, which verifies."),
	}
}
