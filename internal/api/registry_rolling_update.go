package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — clusterScope,
// clusterCheck, clusterParams, withParams, optString and optFlag — lives in
// registry_vms.go; emptyOrUUID lives in registry_pbs.go.

// The three prefixes this domain hangs off. A rolling update job and the SSH
// material it needs are both per-cluster, but they are separate collections
// because they have separate lifetimes: credentials and pinned host keys outlive
// every job that used them, and deleting a job must not take them with it.
//
// The package preview is the odd one out — it hangs off /nodes/:node, and it
// spells the parameter :node rather than :node_name, matching what router.go
// registered. See acmeNodeScope in registry_acme.go, which makes the same choice
// for the same reason.
const (
	rollingScope        = clusterScope + "/rolling-updates"
	sshCredentialScope  = clusterScope + "/ssh-credentials"
	sshKnownHostScope   = clusterScope + "/ssh-known-hosts"
	rollingNodeScope    = clusterScope + "/nodes/:node"
	rollingMaxNodeCount = 64
)

// The two RBAC resources this domain uses, and the split is the point rather
// than an accident of history.
//
//   - rolling_update gates the jobs: who may see one and who may run one.
//   - ssh_credentials gates the material a job upgrades WITH. All seven of its
//     routes are manage, including the reads: GetSSHCredentials returns the
//     username, port and auth type of a stored root credential and
//     ListSSHKnownHosts returns the addresses and fingerprints of every node
//     Nexara can reach over SSH. Neither is something a view grant should buy,
//     which is why there is no view:ssh_credentials at all.
const (
	rollingUpdateResource = "rolling_update"
	sshCredentialResource = "ssh_credentials"
)

func rollingView() Permissions   { return clusterCheck("view", rollingUpdateResource) }
func rollingManage() Permissions { return clusterCheck("manage", rollingUpdateResource) }
func sshManage() Permissions     { return clusterCheck("manage", sshCredentialResource) }

// rollingIDParam is the :id a per-job route carries. It is Nexara's own row id,
// not anything Proxmox assigns.
//
// It is spelled :id because that is what every route carrying one already
// registers. The name is one of the two clusterIDFromParam reads (see
// gateParamNames in registry.go), which is safe here on both counts: it resolves
// to the PATH, and :cluster_id is the FIRST placeholder on every one of these
// paths, so the gate resolves the cluster and never reaches the :id fallback.
func rollingIDParam(description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		Format:      "uuid",
		Typetext:    "<uuid>",
		Description: description,
	}
}

// rollingJobParams is the pair every per-job route carries.
func rollingJobParams(extra apischema.Properties) apischema.Properties {
	return clusterParams(withParams(apischema.Properties{
		"id": rollingIDParam("Rolling update job identifier."),
	}, extra))
}

// rollingNodeNameParam is a Proxmox node NAME inside a request body.
//
// The format is apischema's node-name, which is the TIGHTEST of the four
// spellings of "is this a node name" in the tree. handlers.validateNodeName —
// the third of those four — stays exactly where it is on the two SSH routes: it
// guards the value at the point where it is about to be resolved to an address
// and dialled, and an exported validator with one caller is precisely the
// opt-in-guard shape this codebase has been bitten by. This states the same rule
// one layer earlier, where it can name the parameter.
var rollingNodeNameParam = func() apischema.Property {
	p := apischema.StdOption("node-name")
	p.Description = "Proxmox node name, as the cluster's node listing reports it."
	return p
}()

// rollingNodeListParam is the `nodes` array both the create and the pre-flight
// take.
//
// The bounds are the handler's own, restated: at least one node (the create
// answered "At least one node is required" and the pre-flight "nodes array is
// required"), at most rollingMaxNodeCount ("Too many nodes (max 64)"). The
// pre-flight had no upper bound; it gets the create's, because it exists to
// answer a question ABOUT that create and an answer for a job that cannot be
// created is not worth computing.
//
// The ELEMENT rule is a tightening, and a deliberate one. The create checked
// only `n == "" || len(n) > 128`, so a node name carrying a slash or a control
// character reached GetNodeAptUpdates and the rolling orchestrator's SSH layer.
// Proxmox's own node names are DNS labels, which the node-name format is a
// superset of, so nothing a real cluster can contain is refused here.
func rollingNodeListParam(description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.Array,
		MinLength:   apischema.Ptr(1),
		MaxLength:   apischema.Ptr(rollingMaxNodeCount),
		Items:       &apischema.Property{Type: apischema.String, Format: "node-name", Typetext: "<name>"},
		Typetext:    "<name>[,<name>…]",
		Description: description,
	}
}

// rollingParallelismParam is how many nodes a job works on at once.
//
// The handler CLAMPED this: `<= 0` became 1 and a value above the node count
// became the node count. The first half is now a refusal instead — a
// parallelism the caller did not choose is indistinguishable from one they did,
// which is the trade the migration, CVE and alert listings already made — and
// the second half stays in the handler, because "at most as many as you named"
// is a cross-field rule apischema cannot state. Nexara's own wizard already
// clamps to [1, selected nodes] before it sends.
func rollingParallelismParam() apischema.Property {
	return apischema.Property{
		Type:     apischema.Integer,
		Optional: true,
		Default:  1,
		Minimum:  apischema.Ptr(1.0),
		Maximum:  apischema.Ptr(float64(rollingMaxNodeCount)),
		Typetext: "<integer>",
		Description: "How many nodes to work on at once. Capped at the number of nodes named, so a " +
			"higher value is lowered rather than refused.",
	}
}

// registerRollingUpdateEndpoints declares all 19 of RollingUpdateHandler's
// routes.
//
// Every one is a plain cluster-scoped Check — 5 view:rolling_update, 7
// manage:rolling_update and 7 manage:ssh_credentials — and none is Deferred,
// Advisory or global: each handler resolved the cluster from its own path and
// made exactly one static requireClusterPerm call.
//
// That uniformity hid something, and it is worth stating here rather than only
// in the commit message. SEVEN of these routes load a job or a node row BY ID
// and act on it, while the permission they check is resolved from the cluster in
// the PATH — so a caller holding manage:rolling_update on cluster A could name a
// job belonging to cluster B and start, pause, resume, confirm or skip it.
// CancelJob alone carried the `job.ClusterID != clusterID` check. The other six
// now reach the row through handlers.RollingUpdateHandler.jobInCluster, and
// TestGuard_RollingUpdateJobLookupsAreClusterScoped (in
// internal/api/handlers/rolling_job_scope_guard_test.go) holds every one of them
// there AND refuses a direct lookup that routes around it. The check belongs in
// the handler because the cluster a job belongs to is a column on the row, and
// middleware runs before any query.
func registerRollingUpdateEndpoints(reg *Registry, h *handlers.RollingUpdateHandler) {
	// ── Jobs ──────────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   rollingScope,
		Description: "List one cluster's rolling update jobs, newest first. A job that is still holding " +
			"cluster state after finishing is reported with its terminal status; the cleanup runs in the " +
			"background.",
		Group:       "Rolling Updates",
		Permissions: rollingView(),
		Parameters:  clusterParams(rollingPageParams()),
		Handler:     h.ListJobs,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   rollingScope,
		Description: "Create a rolling update job. Refused while another job for the same cluster is " +
			"pending, running or paused, and while a previous one still holds cluster state it could not " +
			"release. A DRAINED job migrates each node's guests away before upgrading it; an in-place one " +
			"(drain_guests=false) leaves them running and skips the HA and capacity pre-flight entirely, " +
			"because neither has an answer worth acting on when nothing moves.",
		Group:       "Rolling Updates",
		Permissions: rollingManage(),
		Parameters:  clusterParams(createRollingUpdateParams()),
		Handler:     h.CreateJob,
	})
	// BEFORE the :id routes. Fiber matches in registration order, and although
	// no GET or POST on /rolling-updates/:id would capture this today — the
	// literal and the placeholder are the same width but different methods —
	// the ordering is what keeps that true after the next route is added. The
	// alert summary carries the same note for the same reason.
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   rollingScope + "/preflight-ha",
		Description: "Report what would block a proposed rolling update: HA and DRS rules that would stop " +
			"guests leaving a node, and whether the remaining nodes could absorb them. Gated on view " +
			"rather than manage — it computes an answer and changes nothing — and a check that could not " +
			"run is reported as an error-severity conflict rather than omitted, so a strict job refuses " +
			"rather than proceeding on a verdict nobody reached.",
		Group:       "Rolling Updates",
		Permissions: rollingView(),
		Parameters: clusterParams(apischema.Properties{
			"nodes":       rollingNodeListParam("The nodes the proposed job would update."),
			"parallelism": rollingParallelismParam(),
		}),
		Handler: h.PreflightHA,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   rollingScope + "/:id",
		Description: "Get one rolling update job. A job belonging to another cluster answers 404 rather " +
			"than being read through this one.",
		Group:       "Rolling Updates",
		Permissions: rollingView(),
		Parameters:  rollingJobParams(nil),
		Handler:     h.GetJob,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   rollingScope + "/:id/start",
		Description: "Start a pending rolling update job. Only a job in the pending state can be started, " +
			"and one belonging to another cluster answers 404.",
		Group:       "Rolling Updates",
		Permissions: rollingManage(),
		Parameters:  rollingJobParams(nil),
		Handler:     h.StartJob,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   rollingScope + "/:id/cancel",
		Description: "Cancel a rolling update job and release everything it still holds — the DRS pause, " +
			"the native CRS pause, and the HA rules disabled for in-flight nodes. Guests already migrated " +
			"away are NOT migrated back. A job belonging to another cluster answers 404.",
		Group:       "Rolling Updates",
		Permissions: rollingManage(),
		Parameters:  rollingJobParams(nil),
		Handler:     h.CancelJob,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   rollingScope + "/:id/pause",
		Description: "Pause a running rolling update job. The node in flight finishes its current step; " +
			"nothing new is started. Cluster state the job holds — a CRS pause, disabled HA rules — stays " +
			"held. A job belonging to another cluster answers 404.",
		Group:       "Rolling Updates",
		Permissions: rollingManage(),
		Parameters:  rollingJobParams(nil),
		Handler:     h.PauseJob,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        rollingScope + "/:id/resume",
		Description: "Resume a paused rolling update job. A job belonging to another cluster answers 404.",
		Group:       "Rolling Updates",
		Permissions: rollingManage(),
		Parameters:  rollingJobParams(nil),
		Handler:     h.ResumeJob,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   rollingScope + "/:id/nodes",
		Description: "List a job's nodes with the step each one has reached and the timestamps behind it. " +
			"A job belonging to another cluster answers 404.",
		Group:       "Rolling Updates",
		Permissions: rollingView(),
		Parameters:  rollingJobParams(nil),
		Handler:     h.ListNodes,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   rollingScope + "/:id/nodes/:node_id/confirm-upgrade",
		Description: "Confirm that a node awaiting a MANUAL upgrade has been upgraded, so the job moves it " +
			"on. A node that belongs to a different job answers 400, and a job belonging to another " +
			"cluster answers 404.",
		Group:       "Rolling Updates",
		Permissions: rollingManage(),
		Parameters: rollingJobParams(apischema.Properties{
			"node_id": rollingIDParam("Rolling update NODE identifier — the job's own row for that node, not the Proxmox node name."),
		}),
		Handler: h.ConfirmUpgrade,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   rollingScope + "/:id/nodes/:node_id/skip",
		Description: "Skip a node the job has not started yet. A node that has started may hold drained " +
			"guests and disabled HA rules, so it answers 400 naming the step it is in; one belonging to a " +
			"different job answers 400 too, and a job belonging to another cluster answers 404.",
		Group:       "Rolling Updates",
		Permissions: rollingManage(),
		Parameters: rollingJobParams(apischema.Properties{
			"node_id": rollingIDParam("Rolling update NODE identifier."),
		}),
		Handler: h.SkipNode,
	})

	// ── Package preview ───────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   rollingNodeScope + "/packages",
		Description: "List the apt updates pending on one node, read live from Proxmox. This is what the " +
			"node-update panel shows before a job is created, which is why it is gated on " +
			"view:rolling_update rather than view:node.",
		Group:       "Rolling Updates",
		Permissions: rollingView(),
		Parameters: clusterParams(apischema.Properties{
			"node": rollingNodeNameParam,
		}),
		Handler: h.PreviewPackages,
	})

	// ── SSH credentials ───────────────────────────────────────────────
	//
	// All four are MANAGE, including the read. GetSSHCredentials reports the
	// username, port and auth type of a stored root credential, which is half of
	// what an attacker needs and none of what a viewer does.
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   sshCredentialScope,
		Description: "Read the SSH credential Nexara uses to run upgrades on this cluster's nodes — its " +
			"username, port and auth type, and whether a private key is stored. The password and the key " +
			"are never returned. Answers null when none is configured.",
		Group:       "Rolling Updates",
		Permissions: sshManage(),
		Parameters:  clusterParams(nil),
		Handler:     h.GetSSHCredentials,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   sshCredentialScope,
		Description: "Store or replace the cluster's SSH credential. The password and private key are " +
			"encrypted at rest and never read back. Password auth requires a password and key auth " +
			"requires a key; sending neither for the chosen type is refused.",
		Group:       "Rolling Updates",
		Permissions: sshManage(),
		Parameters:  clusterParams(upsertSSHCredentialParams()),
		Handler:     h.UpsertSSHCredentials,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   sshCredentialScope,
		Description: "Delete the cluster's SSH credential. Automatic upgrades stop working; pinned host " +
			"keys are kept, because they describe the nodes rather than the credential.",
		Group:       "Rolling Updates",
		Permissions: sshManage(),
		Parameters:  clusterParams(nil),
		Handler:     h.DeleteSSHCredentials,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   sshCredentialScope + "/test",
		Description: "Test the stored credential against one node, running the trust-on-first-use flow. " +
			"With no pinned host key it SCANS and returns the fingerprint to confirm, without attempting " +
			"authentication; with one, it connects and reports a mismatch as a mismatch rather than as a " +
			"generic failure. Answers 200 with success=false for every outcome the operator has to act on.",
		Group:       "Rolling Updates",
		Permissions: sshManage(),
		Parameters: clusterParams(apischema.Properties{
			"node_name": rollingNodeNameParam,
		}),
		Handler: h.TestSSHConnection,
	})

	// ── SSH known hosts ───────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   sshKnownHostScope,
		Description: "List the host keys pinned for this cluster's nodes, with the address, port and " +
			"fingerprint of each. Manage rather than view: this is the map of what Nexara can reach over SSH.",
		Group:       "Rolling Updates",
		Permissions: sshManage(),
		Parameters:  clusterParams(nil),
		Handler:     h.ListSSHKnownHosts,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   sshKnownHostScope,
		Description: "Pin a node's SSH host key. The key is re-scanned and the fresh fingerprint compared " +
			"against the one the operator confirmed, which closes the window between the test response " +
			"and this call: a key that changed in between answers 409 rather than being pinned.",
		Group:       "Rolling Updates",
		Permissions: sshManage(),
		Parameters: clusterParams(apischema.Properties{
			"node_name": rollingNodeNameParam,
			"expected_fingerprint": {
				Type:      apischema.String,
				MinLength: apischema.Ptr(1),
				MaxLength: apischema.Ptr(128),
				// No format: an SSH host-key fingerprint is "SHA256:" plus
				// base64, which apischema's fingerprint-sha256 — 32 colon-
				// separated hex pairs, the shape a TLS certificate prints —
				// would refuse outright. The length bound is what the handler
				// enforced and is all that belongs here; the real check is the
				// comparison against the freshly scanned key.
				Typetext: "<SHA256:…>",
				Description: "The fingerprint the operator confirmed, exactly as the test response " +
					"reported it. The pin is refused if a fresh scan disagrees with it.",
			},
		}),
		Handler: h.PinSSHHostKey,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   sshKnownHostScope + "/:id",
		Description: "Remove a pinned host key. The next connection to that node fails closed until it is " +
			"pinned again. An entry belonging to another cluster is not removed.",
		Group:       "Rolling Updates",
		Permissions: sshManage(),
		Parameters: clusterParams(apischema.Properties{
			"id": rollingIDParam("Pinned host key identifier."),
		}),
		Handler: h.DeleteSSHKnownHost,
	})
}

// rollingPageParams is the limit/offset pair the job listing takes.
//
// Both were previously UNDECLARED — read straight off c.Query with
// strconv.Atoi and a silent fallback — so an out-of-range or unparseable value
// came back as page one of fifty with no indication. The bounds now REFUSE it,
// which is the trade the alert, migration, CVE and PBS-task listings already
// made: a page size the caller did not choose is indistinguishable from one they
// did, so a paging bug reads as missing data.
func rollingPageParams() apischema.Properties {
	return apischema.Properties{
		"limit": {
			Type:     apischema.Integer,
			Optional: true,
			// The value the handler substituted for a missing, empty,
			// unparseable or out-of-range ?limit=.
			Default:     20,
			Minimum:     apischema.Ptr(1.0),
			Maximum:     apischema.Ptr(100.0),
			Typetext:    "<integer>",
			Description: "Maximum jobs to return.",
		},
		"offset": {
			Type:     apischema.Integer,
			Optional: true,
			Default:  0,
			Minimum:  apischema.Ptr(0.0),
			// A sanity ceiling rather than a limit the handler had: it bounded
			// nothing above, and an offset in the tens of millions is a typo.
			Maximum:     apischema.Ptr(1000000.0),
			Typetext:    "<integer>",
			Description: "How many jobs to skip before the first one returned.",
		},
	}
}

// createRollingUpdateParams is the body of POST .../rolling-updates.
//
// The four booleans carry the handler's own defaults rather than being left as
// tristates, because the handler collapsed each of them to a default the moment
// it read them — there was never a third state for a caller to express. Stating
// them is what makes the two that default to TRUE visible: a job with no
// drain_guests drains, and a job with no auto_restore_guests migrates its guests
// back afterwards.
func createRollingUpdateParams() apischema.Properties {
	return apischema.Properties{
		"nodes":       rollingNodeListParam("The nodes to update, in the order they will be worked through."),
		"parallelism": rollingParallelismParam(),
		"reboot_after_update": {
			Type: apischema.Boolean, Optional: true, Default: false, Typetext: "<boolean>",
			Description: "Reboot each node after its upgrade even when apt did not ask for one. " +
				"An in-place job defers any reboot regardless and flags the node instead.",
		},
		"auto_restore_guests": {
			Type: apischema.Boolean, Optional: true, Default: true, Typetext: "<boolean>",
			Description: "Migrate each node's guests back after it comes up. Only meaningful for a " +
				"drained job — an in-place one moved nothing.",
		},
		"auto_upgrade": {
			Type: apischema.Boolean, Optional: true, Default: false, Typetext: "<boolean>",
			Description: "Run the upgrade over SSH instead of waiting for an operator to confirm each " +
				"node by hand. Requires an SSH credential to be configured for the cluster; without one " +
				"the create is refused.",
		},
		"drain_guests": {
			Type: apischema.Boolean, Optional: true, Default: true, Typetext: "<boolean>",
			Description: "Migrate each node's guests away before upgrading it. FALSE upgrades in place " +
				"with the guests running, which is the only shape a single-node cluster can run — and it " +
				"skips the HA and capacity pre-flight, because neither has an answer when nothing moves.",
		},
		"package_excludes": {
			Type:     apischema.Array,
			Optional: true,
			// A bound on the list rather than a rule about apt: the handler had
			// none, and an unbounded list of package names is a JSONB column
			// nobody meant to fill.
			MaxLength: apischema.Ptr(256),
			Items:     &apischema.Property{Type: apischema.String, MaxLength: apischema.Ptr(128), Typetext: "<package>"},
			Typetext:  "<package>[,<package>…]",
			Description: "Packages to hold back during the upgrade. Omitted, nothing is held back — " +
				"which is what an empty list means too.",
		},
		"ha_policy": {
			Type:     apischema.String,
			Optional: true,
			Default:  "warn",
			// The EMPTY string is a member because the handler read it as
			// "unspecified" and substituted "warn" — `if haPolicy == ""` — so
			// dropping it would 400 a request that has always worked. The
			// handler keeps that one normalisation; the enum owns the rest of
			// the vocabulary, which it did not before.
			Enum:     []string{"", "strict", "warn"},
			Typetext: "<strict|warn>",
			Description: "What a failed pre-flight does. \"strict\" refuses the job with the conflict " +
				"list; \"warn\" records the conflicts on the job and proceeds. Empty or omitted is warn.",
		},
		"notify_channel_id": {
			Type:     apischema.String,
			Optional: true,
			// The EMPTY string is the sentinel the handler read
			// (`if *req.NotifyChannelID != ""`), and every registered format
			// rejects it — so the rule is a pattern rather than the uuid format.
			// Same reasoning as emptyOrNodeName in registry_vms.go.
			Pattern:   emptyOrUUID,
			MaxLength: apischema.Ptr(36),
			Typetext:  "<uuid>",
			Description: "Notification channel to report the job's outcome through. Empty or omitted " +
				"sends nothing.",
		},
	}
}

// upsertSSHCredentialParams is the body of PUT .../ssh-credentials.
//
// auth_type is the only required field, and the two rules that depend on it —
// password auth needs a password, key auth needs a key — stay in the handler.
// apischema's Requires names a companion a parameter ALWAYS needs, not one it
// needs only when a sibling holds a particular value, so half-stating them here
// would refuse the working request rather than the broken one.
func upsertSSHCredentialParams() apischema.Properties {
	return apischema.Properties{
		"username": {
			Type:     apischema.String,
			Optional: true,
			// The handler's own default, stated rather than left implicit. It
			// ALSO keeps normalising an explicitly EMPTY username to root,
			// because a default only fills an ABSENT value and the form sends
			// "" when the field is cleared.
			Default:     "root",
			MaxLength:   apischema.Ptr(64),
			Typetext:    "<string>",
			Description: "Account to connect as. Empty or omitted is root.",
		},
		"port": {
			Type:     apischema.Integer,
			Optional: true,
			Default:  22,
			// Minimum 0, not 1, and that is deliberate: the form's Port field is
			// a number input, so CLEARING it sends 0 — which the handler has
			// always read as "use 22". Refusing it would turn a cleared field
			// into a 400 on a form that has worked for years. The upper bound is
			// a refusal rather than the handler's silent fallback to 22, because
			// nobody clears a field to 70000 and answering "we used 22 instead"
			// without saying so is the substitution this migration removes.
			Minimum:     apischema.Ptr(0.0),
			Maximum:     apischema.Ptr(65535.0),
			Typetext:    "<integer>",
			Description: "TCP port. 0, empty or omitted is 22.",
		},
		"auth_type": {
			Type:        apischema.String,
			Enum:        []string{"password", "key"},
			Typetext:    "<password|key>",
			Description: "How to authenticate. Nexara's own vocabulary, not Proxmox's.",
		},
		"password": optString(1024, "<string>",
			"Password for password auth. Encrypted at rest and never returned. Required when auth_type "+
				"is password."),
		"private_key": optString(32768, "<PEM>",
			"PEM private key for key auth. Encrypted at rest and never returned. Required when auth_type "+
				"is key."),
	}
}
