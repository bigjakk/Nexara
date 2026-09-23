package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — globalCheck, optString
// and upidParam — lives in registry_vms.go, where the first migrated domain
// defined it. upidParam in particular is shared with the two PROXMOX task
// routes there, which is the point: a UPID is one thing, and the two domains
// that carry it in a path must agree on what one looks like.

// taskHistoryScope is the collection all four routes hang off. It is Nexara's
// own task_history table, not Proxmox's task list — the per-cluster Proxmox
// task routes live under /clusters/:cluster_id/tasks and are declared in
// registry_vms.go.
const taskHistoryScope = pathPrefix + "tasks"

// taskFilterClusterParam is the optional ?cluster_id= on the listing, and
// taskBodyClusterParam is the required cluster on the create.
//
// Both are declared under a name of their own with "cluster_id" as an ALIAS,
// for the reason consoleTokenParams (registry_auth.go) gives: checkPathParams
// refuses a parameter NAMED cluster_id that resolves to anything but the path,
// because clusterIDFromParam reads that name to decide which cluster a
// permission gate authorizes. Neither route runs such a gate — the listing is
// Advisory and the create is Deferred — but the refusal is deliberately about
// the NAME rather than about whether today's shape happens to make it safe.
// The alias keeps every existing caller working: both send {"cluster_id": …}.
var (
	taskFilterClusterParam = apischema.Property{
		Type:     apischema.String,
		Alias:    "cluster_id",
		Optional: true,
		Format:   "uuid",
		Typetext: "<uuid>",
		Description: "Narrow the listing to one cluster. Also accepted as \"cluster_id\". The caller must " +
			"hold view:task on it, or the request is refused rather than silently emptied.",
	}
	taskBodyClusterParam = apischema.Property{
		Type:     apischema.String,
		Alias:    "cluster_id",
		Format:   "uuid",
		Typetext: "<uuid>",
		Description: "Cluster the task belongs to. Also accepted as \"cluster_id\", which is what the SPA " +
			"sends. The permission is checked against THIS cluster, so a caller holding manage:task on " +
			"one cluster cannot file a record claiming another.",
	}
)

// taskVmidsParam is the per-guest filter both this domain and the audit log
// carry.
//
// Declared as a bounded STRING rather than as an array, and validated in the
// handler, for two reasons that both point the same way. The wire form is one
// comma-separated value ("100,101"), which apischema would read as a
// single-element array and then fail to coerce to an integer; and
// parseVmidsParam already owns the rule — including the 500-entry cap that
// bounds the SQL ANY() array — for the two endpoints that share it, so a second
// copy here would be one that drifts.
//
// The length bound is what the declaration adds: 500 VMIDs of up to nine digits
// plus separators is under 5 KiB, and nothing else capped the raw string.
var taskVmidsParam = apischema.Property{
	Type:      apischema.String,
	Optional:  true,
	MaxLength: apischema.Ptr(6000),
	Typetext:  "<vmid[,vmid...]>",
	Description: "Narrow the listing to these Proxmox VMIDs, comma-separated. At most 500, and " +
		"whitespace around each entry is ignored.",
}

// registerTaskEndpoints declares TaskHandler's four routes.
//
// They take three shapes, and the split is the domain's own:
//
//	Advisory     the listing spans every cluster, so there is none for a gate
//	             to resolve; accessibleClusters builds the SQL scope instead.
//	Deferred     create and update both resolve their cluster at request time —
//	             the create from the BODY, the update from the task row the
//	             :upid names — so neither is knowable to middleware.
//	Check        the bulk clear is global, deliberately: the underlying DELETE
//	             is unscoped, so a caller holding manage:task on one cluster
//	             must not be able to wipe history that includes another's.
func registerTaskEndpoints(reg *Registry, h *handlers.TaskHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   taskHistoryScope,
		Description: "List Nexara's task history — every Proxmox task this install dispatched, plus its " +
			"own DRS and system tasks — with server-side sorting and offset pagination. The status is " +
			"served from the reconciled row, so a client need not poll Proxmox per entry.",
		Group: "Tasks",
		Permissions: Permissions{Advisory: &AdvisoryCheck{
			Check: Check{Action: "view", Resource: "task", Scope: ScopeCluster},
			Reason: "accessibleClusters(\"view\", \"task\") builds the scope, applyTaskListScope stamps it " +
				"on BOTH the listing and the count query — a Total computed under a wider scope than the " +
				"Items is the leak that shape exists to prevent — and PermitsCluster re-checks each row; " +
				"the listing spans every cluster, so there is none for a gate to resolve",
		}},
		Parameters: apischema.Properties{
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
				Description: "Rows to skip.",
			},
			"sort": {
				Type:     apischema.String,
				Optional: true,
				Default:  "started",
				// The handler's own taskSortColumns, restated one layer earlier.
				// Safe to restate — these are Nexara's own ORDER BY keys, matched
				// on the string in queries/tasks.sql, so an unlisted one would
				// fall through to the default order and silently ignore the
				// caller. TestTaskSortVocabulary pins the two lists together.
				Enum:        []string{"started", "cluster", "type", "description", "vm", "node", "progress", "status"},
				Typetext:    "<started|cluster|type|description|vm|node|progress|status>",
				Description: "Column to order by.",
			},
			"order": {
				Type:        apischema.String,
				Optional:    true,
				Default:     "desc",
				Enum:        []string{"asc", "desc"},
				Typetext:    "<asc|desc>",
				Description: "Order direction. Lower case only — the SQL matches on the string.",
			},
			"filter_cluster_id": taskFilterClusterParam,
			"status": {
				Type:        apischema.String,
				Optional:    true,
				Enum:        []string{"running", "completed", "failed", "stopped"},
				Typetext:    "<running|completed|failed|stopped>",
				Description: "Narrow the listing to one task state. Omitted, every state is returned.",
			},
			"vmids": taskVmidsParam,
		},
		Handler: h.List,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   taskHistoryScope,
		Description: "Record a Proxmox task Nexara dispatched, so it appears in the activity feed and is " +
			"reconciled by the collector. Requires manage:task on the cluster named in the body.",
		Group: "Tasks",
		Permissions: Permissions{Deferred: "the cluster comes from the request BODY, since the path names " +
			"none, so middleware cannot resolve which cluster to authorize; the handler parses it and then " +
			"calls requireClusterPerm(c, \"manage\", \"task\", clusterID) — per-cluster deliberately, so a " +
			"caller holding manage:task on cluster X cannot insert a record claiming cluster Y"},
		Parameters: apischema.Properties{
			"task_cluster_id": taskBodyClusterParam,
			// Patterned, unlike upidParam (registry_vms.go) — READ THAT
			// COMMENT BEFORE TOUCHING EITHER. The two carry the same kind
			// of value and cannot carry the same declaration, because
			// upidParam is a PATH parameter: the frontend percent-encodes
			// the UPID's colons, Fiber does not decode path parameters, and
			// a pattern written against the decoded form would reject every
			// real request while one written against the encoded form would
			// depend on which client did the encoding. Here the UPID is a
			// BODY value. It arrives as itself, so there is no second form
			// to be wrong about and a pattern is possible.
			//
			// It is worth having for the reason the `node` beside it is:
			// this value does not stay in the row. reconcileRunningTasks
			// (internal/collector/task_reconcile.go) reads it back off
			// every row still marked running and replays it through
			// GetTaskStatus on each sync tick, with the server's own
			// credentials and nobody watching, and the task listing hands
			// it to any view:task holder.
			//
			// # What the rule is, and why it is not more
			//
			// Two facts, both read off the tree rather than reasoned from
			// what a UPID "should" look like:
			//
			//   - Every UPID starts "UPID:". That holds for every row in
			//     the development task_history and every fixture in this
			//     repo — stated without a count, because a count taken once
			//     is stale by the next sync — and frontend/src/lib/upid.ts
			//     reads a value whose first colon-separated field is
			//     anything else as naming no guest at all.
			//   - proxmox.validateTaskUPID — the client-side guard that
			//     actually closes the traversal, and closes it for the
			//     collector and the scheduler too — refuses an empty value,
			//     a bare "." or "..", a path separator and a control
			//     character, and nothing else. Its comment records at
			//     length why nothing else: PVE mints 8 colon-separated
			//     fields and PBS 9, an API-token user's half carries a "!",
			//     and the worker id is legitimately empty
			//     ("aptupdate::root@pam:"), non-numeric ("vzdump:local"),
			//     dotted ("osd.1") and "@"-bearing ("imgdel:105@store02").
			//     This route itself round-trips values with fewer fields
			//     than PVE mints.
			//
			// So: the anchor and validateTaskUPID's refusals, and no field
			// count, no charset for the fields and no minimum length beyond
			// the one non-empty character the anchor implies. An over-tight
			// rule here would fail in the direction that hides: a real UPID
			// refused at creation is a task Nexara dispatched and then did
			// not record, which nothing reports at all.
			//
			// The C1 range is excluded alongside the C0 controls because
			// proxmox.hasControlChar excludes it, and the value reaches an
			// audit row and the activity feed where a smuggled escape is
			// text other people read.
			//
			// MinLength is gone rather than kept at 1: the pattern already
			// requires six characters, so the bound could only ever be
			// looser than the rule beside it and would state nothing a
			// reader could use. (This parameter's own Typetext has always
			// been accurate. The one that advertised a value the system
			// rejects was resource_type's "<vm|lxc>" in
			// registry_schedules.go, against a scheduler that only ever
			// handled "vm" and "ct" — a different parameter in a different
			// file, fixed in the same change as this.)
			"upid": {
				Type:      apischema.String,
				Pattern:   `^UPID:[^/\\[:cntrl:]\x{80}-\x{9f}]+$`,
				MaxLength: apischema.Ptr(512),
				Typetext:  "<UPID>",
				Description: "Proxmox task id, as Proxmox returned it — starting \"UPID:\", with no path " +
					"separator and no control character. The collector replays it against the cluster " +
					"until the task finishes, so a value Proxmox did not mint files a row nothing can " +
					"reconcile.",
			},
			"description": optString(512, "<string>", "What the task is, for the activity feed."),
			"status": {
				Type:        apischema.String,
				Optional:    true,
				Default:     "running",
				Enum:        []string{"running", "completed", "failed", "stopped"},
				Typetext:    "<running|completed|failed|stopped>",
				Description: "Initial state. Omitted, the row is filed as running, which is what a just-dispatched task is.",
			},
			// Not a bare optString, unlike its neighbours: this value does
			// not stay in the row. reconcileRunningTasks
			// (internal/collector/task_reconcile.go) reads it back off every
			// row still marked running and replays it through
			// GetTaskStatus on each sync tick, with the server's own
			// credentials and nobody watching — so a name stored here is a
			// name the collector will keep sending to Proxmox, long after
			// the request that filed it. It is also handed back to any
			// view:task holder by the task listing.
			//
			// The client refuses a name that could leave its path segment
			// (proxmox.validateNodeName), so the traversal is closed there
			// rather than here. What this stops is the row existing at all,
			// and the cost of not stopping it is worth stating precisely
			// rather than dramatically: the write succeeds, every tick for
			// the next 24 hours calls GetTaskStatus and throws the error
			// away SILENTLY — task_reconcile.go's error branch has no log
			// line, it just continues — and at staleTaskGrace the row is
			// flipped to failed/"vanished" and stops being read. So the
			// damage is a day of futile calls and then a bogus failure in
			// the activity feed for good, not an unbounded loop. Note also
			// which layer refuses what: a traversal never reaches Proxmox
			// at all (validatePathSegment answers locally), while a merely
			// ill-formed name like "-pve-01" does reach the wire and comes
			// back as a Proxmox error.
			//
			// Every other parameter in the registry that holds ONE node name
			// already carries this rule; this one was the exception. The
			// plural ones (haNodesParam, the SDN and vm-import node lists)
			// carry no rule because they are comma-separated, which a
			// single-value format cannot express.
			//
			// emptyOrNodeName rather than the format, because "" is what the
			// column has always stored for a task with no node
			// (migrations/000008_task_history.up.sql defaults it) and
			// apischema counts "" as a value the caller supplied, which
			// every format rejects.
			"node": {
				Type:      apischema.String,
				Optional:  true,
				Pattern:   emptyOrNodeName,
				MaxLength: apischema.Ptr(63),
				Typetext:  "<name>",
				// Says what "" does, which every other -or-empty site states
				// and which matters more here than at most of them: the
				// client refuses an empty node outright, so a row filed with
				// one can never be reconciled — it sits running until
				// staleTaskGrace and is then marked failed.
				Description: "Node the task runs on, as Proxmox names it. Empty or omitted files the row " +
					"with no node, which the collector cannot then reconcile.",
			},
			"task_type": optString(128, "<type>", "Proxmox task type, e.g. qmstart."),
		},
		Handler: h.Create,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   taskHistoryScope + "/:upid",
		Description: "Update a recorded task's state and progress. Requires manage:task on the CLUSTER the " +
			"recorded task belongs to, which is read from the row rather than supplied.",
		Group: "Tasks",
		Permissions: Permissions{Deferred: "the cluster is read from the task ROW the :upid resolves to, " +
			"not from the request, so middleware has nothing to resolve; the handler loads the row and then " +
			"calls requireClusterPerm(c, \"manage\", \"task\", task.ClusterID)"},
		Parameters: apischema.Properties{
			"upid": upidParam,
			"status": {
				Type:        apischema.String,
				Optional:    true,
				Enum:        []string{"running", "completed", "failed", "stopped"},
				Typetext:    "<running|completed|failed|stopped>",
				Description: "New state. \"stopped\" with no finished_at stamps the finish time as now.",
			},
			"exit_status": optString(512, "<string>", "Proxmox's exit status string for the task."),
			"progress": {
				Type:     apischema.Number,
				Optional: true,
				Minimum:  apischema.Ptr(0.0),
				Maximum:  apischema.Ptr(100.0),
				Typetext: "<number>",
				// No Default, so p.OptFloat reports whether the caller chose:
				// the handler writes the column only when they did, and the SPA
				// sends an explicit null for "unknown", which apischema reads
				// as absent — the same thing.
				Description: "Percentage complete. Omitted, or sent as null, leaves the stored value alone.",
			},
			"finished_at": {
				Type:      apischema.String,
				Optional:  true,
				MaxLength: apischema.Ptr(64),
				Typetext:  "<RFC3339 timestamp>",
				Description: "When the task finished. Omitted, or sent as null, the finish time is stamped " +
					"only when status is \"stopped\".",
			},
		},
		Handler: h.Update,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   taskHistoryScope,
		Description: "Clear every completed, failed and stopped task older than the retention window, " +
			"across every cluster. Global manage:task, deliberately: the DELETE is unscoped, so a caller " +
			"holding the grant on one cluster must not be able to wipe another's history.",
		Group:       "Tasks",
		Permissions: globalCheck("manage", "task"),
		Parameters:  apischema.Properties{},
		Handler:     h.ClearCompleted,
	})
}
