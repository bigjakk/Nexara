package handlers

// How to add a kind of object with a History (the guest's: guest_history.go, registry_guest_history.go):
//  1. handlers/<kind>_history.go: its HistoryKind — Name, Categories in filter order, the Rules that
//     derive them (first match wins, else Default), Select (which rows are the object's) and the
//     ObjectTypes it names, an observed type's read under that type's own view. SelectObjects(param, …)
//     reads audit rows by resource_type and id, or a DetailKey; param must be a declared String path
//     parameter (a node's: StdOption("node-name"), which 404s a non-member). SelectTypes(…) reads every
//     row of the types in the cluster: a tab's History. Global only for ids unique
//     across Nexara (globalHistoryResourceTypes): it reads every cluster's rows.
//  2. api/registry_<kind>_history.go: its historyRoute (path, description, its own view permission), and
//     one line in historyRoutes(). 3. Frontend: locales/en/history-<Name>.json with the same categories,
//     in order (TestHistoryCategoryVocabulary); a HistoryKind (features/history/types/history.ts).

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// HistoryHandler serves every History route: what happened to one object,
// newest first. Each route is one HistoryKind, declared in
// internal/api/registry_history.go; the timeline is assembled in SQL
// (queries/history.sql, ListHistory), which says what each entry is and
// where it comes from.
type HistoryHandler struct {
	queries *db.Queries
	// history runs ListHistory alone, through HistoryDBTX.
	history *db.Queries
}

// NewHistoryHandler creates the handler every History route shares; pool is
// the one queries reads through, which ListHistory reads through too.
func NewHistoryHandler(pool db.DBTX, queries *db.Queries) *HistoryHandler {
	return &HistoryHandler{queries: queries, history: db.New(HistoryDBTX(pool))}
}

// HistoryDBTX runs each query with a plan made for its own arguments, for
// ListHistory: pgx.QueryExecModeCacheDescribe, an unnamed statement planned at
// Bind with the values bound, its parameters typed as the server described
// them and sent in binary, in one round trip once described. Under pgx's
// statement cache Postgres may move a statement to one generic plan after
// five runs, and ListHistory is one statement for every kind, its arms
// switched on and off by its parameters: judged at average values, on a table
// of one cluster that plan once read a cluster's whole audit index and sorted
// it, 0.4-1.9 s against 50-130 ms. Each read plans in 14-18 ms, and parsing
// it each time adds 4-8 ms. Never the simple protocol: its arguments would be
// spliced into the SQL. Only Query changes: ListHistory is :many.
func HistoryDBTX(dbtx db.DBTX) db.DBTX { return historyDBTX{dbtx} }

// historyDBTX is HistoryDBTX's.
type historyDBTX struct{ db.DBTX }

func (d historyDBTX) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return d.DBTX.Query(ctx, sql, append([]any{pgx.QueryExecModeCacheDescribe}, args...)...)
}

// HistoryKind is one kind of object with a History: which rows are its own,
// and the categories its type filter offers.
type HistoryKind struct {
	// Name names the kind. Its frontend strings are the i18n namespace
	// history-<Name> (frontend/src/locales/en/history-<Name>.json).
	Name string
	// Categories is the type filter's vocabulary, in the order it offers them.
	Categories []string
	// Default is the category of an entry no rule matches.
	Default string
	// Rules derive an entry's category, in SQL, before the page is cut: the
	// first rule that matches decides it.
	Rules []HistoryRule
	// Select names the rows that are the object's, from the request's
	// validated parameters. An error it returns is the response.
	Select HistorySelector
	// ObjectTypes are the resource types Select may name in Objects, declared
	// so that what a kind reads is checked at startup — a global kind's
	// against globalHistoryResourceTypes (Validate). ListParams refuses an
	// object of any other type.
	ObjectTypes []string
	// ObservedTypes are those of ObjectTypes whose changes seen in Proxmox
	// (cluster_object_changes) the kind shows beside their audit rows, each
	// with the view permission it is read under (Validate). A request reads
	// only those of them its caller may view so — a pool's under view:pool
	// whatever the kind's — and no type undeclared.
	ObservedTypes []HistoryObservedType
	// GuestFromPath names the integer path parameter whose VMID is the
	// selection's one guest — List refuses any other selection — and the
	// caller asked for it by that VMID, so listing it discloses nothing. A
	// name rather than a flag: List reads it whatever the route, and the
	// registry checks the route declares it. Every other kind's guests — a
	// folder's members, a node's guests — are kept only where the caller may
	// open them (viewableGuests).
	GuestFromPath string
	// Global reads the kind's objects in every cluster and in none, for an
	// object that belongs to no one cluster: its route sits outside
	// /clusters/:cluster_id and is gated on view:audit and its own view
	// permission, both GLOBAL. Only for ids unique across Nexara — the
	// resource types in globalHistoryResourceTypes, which ListParams holds a
	// global kind to — since audit keys are unique only within a cluster.
	// A global kind has no guests.
	Global bool
	// ObjectsIgnoreGuestCutoff leaves an object's row that names one of the
	// selection's guests out of that guest's cutoff. Unset, such a row — one
	// of the guest's that GuestWorkers turned away and an object took — is
	// held to it, so a previous holder of the VMID is not the object's either:
	// right for an object that is the guest's, as its HA resource is. Set it
	// where an object's rows stand on their own, as a node's tasks do.
	ObjectsIgnoreGuestCutoff bool
	// WholeCluster reads every row of every source filed under the path's
	// cluster — a cluster's own History — and no guests or objects: Select
	// returns an empty selection, which List holds it to.
	WholeCluster bool
	// Collapse is the (resource type, action) pairs a whole-cluster kind reads
	// as runs: one entry per burst of them, a gap longer than Gap ending it.
	Collapse []HistoryCollapse
	// GuestToggle names the route's boolean parameter that brings guest
	// activity into a node's History — the guests' tasks that ran on it,
	// and any other row naming a guest but its arrivals and departures — left
	// out unless it is set. The registry declares it; a kind without one shows
	// guest rows always.
	GuestToggle string
	// RowCap bounds an unfiltered read: each source newest first up to RowCap
	// rows, so a page of a busy cluster's, folder's or node's History costs
	// RowCap rows a source, not its whole trail. A read in which a source
	// reaches it lists only what it is whole for, back to its searched_from,
	// and counts only that; end_time set to searched_from reads the stretch
	// before it. The caller's guest types are applied inside each capped
	// source, so the cap counts only rows the caller may see.
	RowCap int
	// FilteredRowCap is RowCap for a read narrowed by category or source, set
	// with it and no smaller: the narrowing is applied after each source's
	// cap, so a narrowed read searches each source's newest FilteredRowCap
	// rows — of its window, a week unless asked otherwise.
	FilteredRowCap int
	// DefaultWindow is how far back a read that names no start_time looks,
	// unless it asks for all_time: the window the page opens on, held by the
	// server too, so a client naming none cannot read a busy object's whole
	// trail by accident. Zero reads the whole History (the guest's).
	DefaultWindow time.Duration
	// Hide is shapes of row the kind never shows whatever its objects select:
	// a row that is another row's copy, listed beside it otherwise. Only the
	// objects' rows are hidden, and only of a type the kind reads.
	Hide []HistoryHide
}

// HistoryHide is a shape of audit row: those of ResourceType with Action and,
// when DetailKey is set, whose details hold DetailValue under it.
type HistoryHide struct {
	ResourceType string
	Action       string
	DetailKey    string
	DetailValue  string
}

// HistoryCollapse is one kind of repeated row a whole-cluster History reads
// as runs (HistoryKind.Collapse).
type HistoryCollapse struct {
	ResourceType, Action string
	// Gap is the longest quiet spell within one run.
	Gap time.Duration
}

// HistoryObservedType is one type whose changes seen in Proxmox a kind shows,
// read under View — or, when that is empty, the type's own permission in
// observedTypePermission. A View other than the type's own is refused unless
// observedViewOverrides allows it for the kind (Validate), so no declaration
// can read a type under a weaker permission than its own pages need.
type HistoryObservedType struct {
	Type, View string
}

// observedViewOverrides are the kinds allowed to read a type's observed
// changes under another view than the type's own, keyed "kind/type", each with
// that view. None yet: a type is read under its own everywhere, and a kind
// that needs otherwise is reviewed here.
var observedViewOverrides = map[string]string{}

// observedView is the view permission t's changes are read under, and
// whether there is one.
func (t HistoryObservedType) observedView() (string, bool) {
	if t.View != "" {
		return t.View, true
	}
	view, mapped := observedTypePermission[t.Type]
	return view, mapped
}

// permittedObservedTypes are the kind's ObservedTypes whose view the caller
// holds, each under the permission it is declared with — its own, else the
// type's in the map — in the request's scope (can): the observed changes a
// request reads. A type with neither is not among them, whatever the kind
// declares. Each view is asked once: a whole cluster's forty types share
// thirteen.
func permittedObservedTypes(k HistoryKind, can func(action, resource string) (bool, error)) ([]string, error) {
	permitted := []string{}
	granted := map[string]bool{}
	for _, t := range k.ObservedTypes {
		view, viewed := t.observedView()
		if !viewed || slices.Contains(permitted, t.Type) {
			continue
		}
		allowed, asked := granted[view]
		if !asked {
			var err error
			if allowed, err = can("view", view); err != nil {
				return nil, err
			}
			granted[view] = allowed
		}
		if allowed {
			permitted = append(permitted, t.Type)
		}
	}
	return permitted, nil
}

// clusterObservedTypes is every type an observed row of a cluster may be
// filed under, each under its own view: a whole cluster's History shows them
// all, as its audit arm reads every row filed under the cluster. Not a global
// kind's type, whose rows name no cluster.
func clusterObservedTypes() []HistoryObservedType {
	types := make([]HistoryObservedType, 0, len(observedTypePermission))
	for _, t := range slices.Sorted(maps.Keys(observedTypePermission)) {
		if !slices.Contains(globalHistoryResourceTypes, t) {
			types = append(types, HistoryObservedType{Type: t})
		}
	}
	return types
}

// observedTypePermission is the view permission of each resource type whose
// changes seen in Proxmox cluster_object_changes records — its trigger, the
// Ceph pass and the observer. An observed row carries what the type's own
// view shows (its detail, its field names), so a kind reads it only under
// that permission, unless its declaration names another
// (HistoryObservedType). A type missing here and not given one is read as an
// audit row alone.
var observedTypePermission = map[string]string{
	"vm":                           "vm",
	"container":                    "container",
	"ha_resource":                  "ha",
	"ha_group":                     "ha",
	"ha_rule":                      "ha",
	"pool":                         "pool",
	"network":                      "network",
	"sdn_zone":                     "network",
	"sdn_vnet":                     "network",
	"sdn_subnet":                   "network",
	"sdn_controller":               "network",
	"sdn_ipam":                     "network",
	"sdn_dns":                      "network",
	"firewall_options":             "network",
	"firewall_rule":                "network",
	"firewall_alias":               "network",
	"firewall_ipset":               "network",
	"firewall_ipset_entry":         "network",
	"firewall_security_group":      "network",
	"firewall_security_group_rule": "network",
	"cluster":                      "cluster", // the observer's rows of the cluster as a whole
	"cluster_options":              "cluster",
	"metric_server":                "cluster",
	"usb_mapping":                  "cluster",
	"pci_mapping":                  "cluster",
	"pve_user":                     "access",
	"pve_token":                    "access",
	"pve_group":                    "access",
	"pve_role":                     "access",
	"pve_acl":                      "access",
	"acme_account":                 "certificate",
	"acme_plugin":                  "certificate",
	"acme_config":                  "certificate",
	"storage":                      "storage",
	"backup":                       "backup",
	"node":                         "node",
	"replication":                  "replication",
	"ceph_osd":                     "ceph",
	"ceph_pool":                    "ceph",
	"pbs_server":                   "pbs",
}

// ObservedTypePermission is the view permission an observed row of
// resourceType is read under, and whether the type has observed rows at all.
func ObservedTypePermission(resourceType string) (permission string, observed bool) {
	permission, observed = observedTypePermission[resourceType]
	return permission, observed
}

// ObservedTypePermissions is every type's in observedTypePermission, a copy:
// what the registry's test holds to the routes that show each type.
func ObservedTypePermissions() map[string]string {
	return maps.Clone(observedTypePermission)
}

// globalHistoryResourceTypes are the audit resource types whose ids are unique
// across Nexara, so a global kind may select them in every cluster: a PBS
// server's and a firewall template's uuid, and Veeam's own ids. Any other
// type's id is unique only within its cluster — node pve-01, VM 100 — and
// read in every cluster would mix the objects of the same name.
var globalHistoryResourceTypes = []string{"pbs_server", "veeam_server", "veeam_job", "veeam_session", "firewall_template"}

// HistoryField is what a HistoryRule matches on.
type HistoryField string

// The fields a rule can match. They are the only four ListHistory derives a
// category from.
const (
	// HistoryFieldEntry is the entry's kind: audit, task, status or backup —
	// and observed, a change Nexara saw in Proxmox that nothing on record
	// explains, once its source is read (queries/history.sql says how).
	HistoryFieldEntry HistoryField = "entry"
	// HistoryFieldAction is the audit row's action; a task's type for a task
	// with no audit row.
	HistoryFieldAction HistoryField = "action"
	// HistoryFieldResourceType is the audit row's resource_type.
	HistoryFieldResourceType HistoryField = "resource_type"
	// HistoryFieldWorker is the Proxmox worker type the entry's UPID names.
	HistoryFieldWorker HistoryField = "worker"
)

// HistoryRule puts an entry in Category when its Field equals one of Equals
// or is LIKE one of Like (SQL LIKE, backslash the escape: `drs\_%`).
type HistoryRule struct {
	Category string
	Field    HistoryField
	Equals   []string
	Like     []string
}

// HistorySelector names the rows of one object's History.
type HistorySelector func(ctx context.Context, r HistoryRequest) (HistorySelection, error)

// HistoryRequest is what a selector reads: the path's cluster, which the
// route's gate authorized, the request's validated parameters, and the guest
// types the caller may open (historyScope: qemu with view:vm, lxc with
// view:container), so that a selector choosing among many guests chooses
// among those, as viewableGuests will keep only those.
type HistoryRequest struct {
	Queries    *db.Queries
	ClusterID  uuid.UUID
	Params     *apischema.Params
	GuestTypes []string
}

// HistorySelection is which rows are the object's, through ListHistory's two
// families of selector; either or both.
type HistorySelection struct {
	// Guests are the guest-shaped sources: the audit rows naming each guest,
	// a clone into it and its HA resource; its tasks with no audit row, its
	// status changes no task explains and its parts of backup jobs.
	Guests []HistoryGuest
	// GuestWorkers, when set, keeps only the guests' rows whose UPID names a
	// Proxmox worker type LIKE one of them — an HA resource's History takes
	// its guest's "ha%" tasks and nothing else of the guest's.
	GuestWorkers []string
	// Objects are the generic selector's audit rows.
	Objects []HistoryObject
	// Node names a node of the path's cluster whose own records are the
	// kind's: the tasks it ran, its status changes, the guests that arrived
	// on it or left it (ListHistory's node arms).
	Node string
	// GuestsTotal is, for a selection that follows only some of the guests
	// it chose from — a folder past folderHistoryGuestCap — how many there
	// were: the caller's to open, as Guests are. Zero otherwise.
	GuestsTotal int
}

// HistoryGuest is one guest a History covers, from its own cutoff.
type HistoryGuest struct {
	VMID int32
	// After, when valid, starts the guest's timeline: from it when Inclusive,
	// after it otherwise (guestHistoryCutoff).
	After     pgtype.Timestamptz
	Inclusive bool
}

// HistoryObject selects the audit rows in the cluster of ResourceType that are
// one object's:
//
//   - no DetailKey: those whose resource_id is Value;
//   - a DetailKey: those whose details->>DetailKey is Value, a string or an
//     integer — and whose resource_id is ResourceID, when one is set: a PBS
//     datastore is one name within its server's rows;
//   - a DetailKey and InList: those whose DetailKey is a list holding Value,
//     such as an ACL's users, split as Proxmox splits one (split_list): on
//     commas, semicolons and white space. Read by ResourceType alone, so for
//     a type with few rows;
//   - WholeType: every row of ResourceType, whatever its id — a tab's History,
//     of all the objects of its kinds. It names no id, key or value, and is
//     read by the type alone, so for a type of configuration objects, which
//     are edited rarely. Never a guest's type: those are the guests' own
//     (ListParams).
type HistoryObject struct {
	ResourceType string
	ResourceID   string
	DetailKey    string
	Value        string
	InList       bool
	WholeType    bool
}

// SelectObjects is the generic selector, for any object with a stable audit
// identity: the audit rows of each of objects, whose Value is the path
// parameter param.
func SelectObjects(param string, objects ...HistoryObject) HistorySelector {
	return func(_ context.Context, r HistoryRequest) (HistorySelection, error) {
		value := r.Params.String(param)
		sel := HistorySelection{Objects: make([]HistoryObject, len(objects))}
		for i, o := range objects {
			o.Value = value
			sel.Objects[i] = o
		}
		return sel, nil
	}
}

// SelectTypes is the selector of a tab's History: every row of each of the
// resource types in the path's cluster (HistoryObject.WholeType).
func SelectTypes(types ...string) HistorySelector {
	return func(context.Context, HistoryRequest) (HistorySelection, error) {
		sel := HistorySelection{Objects: make([]HistoryObject, len(types))}
		for i, t := range types {
			sel.Objects[i] = HistoryObject{ResourceType: t, WholeType: true}
		}
		return sel, nil
	}
}

var (
	historyKindNameRe  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	historyParamNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// Validate refuses a kind whose type filter could not work: a rule's category
// the filter would refuse, a category no rule or default produces, which
// would be a filter that always comes back empty, a field ListHistory cannot
// match on, or a pattern Postgres refuses — one ending in a lone backslash
// fails every request for the kind. The registry calls it at startup.
func (k HistoryKind) Validate() error {
	if !historyKindNameRe.MatchString(k.Name) {
		return fmt.Errorf("history kind %q: Name must be lower-case letters, digits and hyphens", k.Name)
	}
	if k.Select == nil {
		return fmt.Errorf("history kind %q has no Select", k.Name)
	}
	if k.Global && k.GuestFromPath != "" {
		return fmt.Errorf("history kind %q is global, and a global kind has no guests", k.Name)
	}
	if k.GuestToggle != "" && !historyParamNameRe.MatchString(k.GuestToggle) {
		return fmt.Errorf("history kind %q: GuestToggle %q is not a parameter name", k.Name, k.GuestToggle)
	}
	if k.WholeCluster && (k.Global || k.GuestFromPath != "" || len(k.ObjectTypes) > 0) {
		return fmt.Errorf("history kind %q reads its whole cluster, so it names no guest or object and is no "+
			"global kind", k.Name)
	}
	if !k.WholeCluster && len(k.Collapse) > 0 {
		return fmt.Errorf("history kind %q collapses rows, which only a whole-cluster read does", k.Name)
	}
	if k.RowCap < 0 || k.DefaultWindow < 0 || (k.RowCap == 0) != (k.FilteredRowCap == 0) || k.FilteredRowCap < k.RowCap {
		return fmt.Errorf("history kind %q has a negative window or cap, or caps one read and not the other, or a "+
			"narrowed one below an unfiltered one", k.Name)
	}
	for _, c := range k.Collapse {
		if c.ResourceType == "" || c.Action == "" || c.Gap <= 0 {
			return fmt.Errorf("history kind %q collapses %+v, which needs a resource type, an action and a gap",
				k.Name, c)
		}
	}
	for _, t := range k.ObjectTypes {
		if k.Global && !slices.Contains(globalHistoryResourceTypes, t) {
			return fmt.Errorf("history kind %q is global and reads %q, whose ids are unique only within a cluster", k.Name, t)
		}
	}
	for i, h := range k.Hide {
		if h.ResourceType == "" || h.Action == "" || (h.DetailKey == "") != (h.DetailValue == "") {
			return fmt.Errorf("history kind %q hide %d needs a resource type and an action, and a details key with "+
				"its value or neither", k.Name, i)
		}
		if !slices.Contains(k.ObjectTypes, h.ResourceType) {
			return fmt.Errorf("history kind %q hides rows of %q, which it does not read", k.Name, h.ResourceType)
		}
	}
	for i, t := range k.ObservedTypes {
		if slices.ContainsFunc(k.ObservedTypes[:i], func(o HistoryObservedType) bool { return o.Type == t.Type }) {
			return fmt.Errorf("history kind %q lists the observed type %q twice", k.Name, t.Type)
		}
		if own := observedTypePermission[t.Type]; t.View != "" && t.View != own &&
			observedViewOverrides[k.Name+"/"+t.Type] != t.View {
			return fmt.Errorf("history kind %q reads the observed changes of %q under view:%s, not the type's own "+
				"(view:%s), which only observedViewOverrides allows", k.Name, t.Type, t.View, own)
		}
		// A guest's changes are read by its VMID, a whole cluster's by type:
		// neither is an object the kind selects.
		guestType := !k.Global && (t.Type == "vm" || t.Type == "container")
		if _, viewed := t.observedView(); !viewed ||
			!k.WholeCluster && !guestType && !slices.Contains(k.ObjectTypes, t.Type) ||
			k.Global && !slices.Contains(globalHistoryResourceTypes, t.Type) {
			return fmt.Errorf("history kind %q shows the observed changes of %q, which is not one of its ObjectTypes "+
				"or has no view permission, its own or in observedTypePermission", k.Name, t.Type)
		}
	}
	if !slices.Contains(k.Categories, k.Default) {
		return fmt.Errorf("history kind %q: Default %q is not one of its Categories", k.Name, k.Default)
	}
	produced := map[string]bool{k.Default: true}
	for i, r := range k.Rules {
		switch r.Field {
		case HistoryFieldEntry, HistoryFieldAction, HistoryFieldResourceType, HistoryFieldWorker:
		default:
			return fmt.Errorf("history kind %q rule %d matches the unknown field %q", k.Name, i, r.Field)
		}
		if len(r.Equals)+len(r.Like) == 0 {
			return fmt.Errorf("history kind %q rule %d matches nothing", k.Name, i)
		}
		for _, pattern := range r.Like {
			if !likePatternOK(pattern) {
				return fmt.Errorf("history kind %q rule %d: the LIKE pattern %q ends in a lone backslash", k.Name, i, pattern)
			}
		}
		if !slices.Contains(k.Categories, r.Category) {
			return fmt.Errorf("history kind %q rule %d produces %q, which is not one of its Categories",
				k.Name, i, r.Category)
		}
		produced[r.Category] = true
	}
	for i, c := range k.Categories {
		if slices.Index(k.Categories, c) != i {
			return fmt.Errorf("history kind %q lists the category %q twice", k.Name, c)
		}
		if !produced[c] {
			return fmt.Errorf("history kind %q lists the category %q, which no rule produces", k.Name, c)
		}
	}
	return nil
}

// likePatternOK reports whether Postgres takes p as a LIKE pattern under the
// default escape, the backslash: it refuses one that ends in an escape with
// nothing after it.
func likePatternOK(p string) bool {
	return (len(p)-len(strings.TrimRight(p, `\`)))%2 == 0
}

// likeEscaper makes a literal match itself under LIKE's default escape.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// sqlRules flattens the rules into ListHistory's parallel arrays, in order:
// one row per value, Equals escaped into patterns that match only
// themselves.
func (k HistoryKind) sqlRules() (categories, fields, patterns []string) {
	for _, r := range k.Rules {
		add := func(pattern string) {
			categories = append(categories, r.Category)
			fields = append(fields, string(r.Field))
			patterns = append(patterns, pattern)
		}
		for _, v := range r.Equals {
			add(likeEscaper.Replace(v))
		}
		for _, p := range r.Like {
			add(p)
		}
	}
	return categories, fields, patterns
}

// The outcomes an entry can show. Empty is "not a task", or a task whose end
// was never recorded — a Nexara task pruned from task_history before
// migrations/000115 kept them.
const (
	historyOutcomeRunning = "running"
	historyOutcomeOK      = "ok"
	historyOutcomeFailed  = "failed"
)

type historyEntry struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// ClusterID is the entry's own cluster: the path's on a cluster's
	// History; on a global kind's, the row's, or none.
	ClusterID  string `json:"cluster_id,omitempty"`
	OccurredAt string `json:"occurred_at"`
	Category   string `json:"category"`
	Source     string `json:"source"`
	Action     string `json:"action"`
	// ResourceType and ResourceID are the audit row's, for an audit entry;
	// ResourceType is "task", "status" or "backup" otherwise.
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	// VMID is the guest the row itself names. On a guest's History it
	// differs from SubjectVMID only on a clone INTO the guest, which names
	// its source.
	VMID int32 `json:"vmid"`
	// SubjectVMID is the guest the entry is filed under; 0 for an object's
	// own row.
	SubjectVMID     int32    `json:"subject_vmid,omitempty"`
	UserID          string   `json:"user_id"`
	UserEmail       string   `json:"user_email"`
	UserDisplayName string   `json:"user_display_name"`
	Details         string   `json:"details"`
	UPID            string   `json:"upid"`
	Node            string   `json:"node"`
	Description     string   `json:"description"`
	TaskType        string   `json:"task_type"`
	Outcome         string   `json:"outcome"`
	ExitStatus      string   `json:"exit_status"`
	Progress        *float64 `json:"progress,omitempty"`
	FinishedAt      *string  `json:"finished_at"`
	FromStatus      string   `json:"from_status,omitempty"`
	ToStatus        string   `json:"to_status,omitempty"`
	// LastSeenAt is, for a status change, when the guest was last seen in
	// FromStatus, and for an observed change when the object was last seen as
	// it was: the change happened after it and by OccurredAt.
	LastSeenAt *string `json:"last_seen_at,omitempty"`
	// Fields is, for an observed change, the names of the fields that
	// changed.
	Fields []string `json:"fields,omitempty"`
	// AlsoChanged is, for an audit entry, what its edit changed in Proxmox
	// beyond the fields it names, object by object (attachAlsoChanged).
	AlsoChanged []historyAlsoChanged `json:"also_changed,omitempty"`
	// LogStart and LogEnd are, for a backup entry, the lines of the job's
	// task log that are this guest's part, by Proxmox's line numbers.
	LogStart int32 `json:"log_start,omitempty"`
	LogEnd   int32 `json:"log_end,omitempty"`
	// RunCount is, for a run — a cluster's sync failures, or a node's status
	// changes or a guest's moves past the hourly cap — how many it counts:
	// from LastSeenAt, its first, to OccurredAt, its last; Details are the
	// last one's.
	RunCount int32 `json:"run_count,omitempty"`
}

// historyTaskOutcome is how a task ended, from the task row when there is
// one and otherwise from the status the collector recorded when it ingested
// the task. That one is Proxmox's own status field: "RUNNING" while the task
// ran, then its exit status — so "RUNNING" or nothing means the end was never
// seen.
//
// proxmox.TaskSucceeded is the single success rule ("OK", "OK (with
// warnings)" and "WARNINGS: N" succeed); task_history's own completed/failed
// were derived from it already.
func historyTaskOutcome(taskStatus, taskExit, recorded string) string {
	switch taskStatus {
	case "running":
		return historyOutcomeRunning
	case "completed":
		return historyOutcomeOK
	case "failed":
		return historyOutcomeFailed
	case "stopped":
		// Some writers store Proxmox's raw state rather than its verdict.
		if proxmox.TaskSucceeded(taskExit) {
			return historyOutcomeOK
		}
		return historyOutcomeFailed
	case "":
		r := strings.TrimSpace(recorded)
		if r == "" || strings.EqualFold(r, "running") {
			return ""
		}
		if proxmox.TaskSucceeded(r) {
			return historyOutcomeOK
		}
		return historyOutcomeFailed
	}
	return ""
}

// historyQueryTimeout bounds one request's queries. Every view:audit holder
// can ask for any object, a busy object's history is the whole of its audit
// trail, and Fiber's request context is never cancelled by a client that went
// away.
const historyQueryTimeout = 15 * time.Second

// historyScope decides which of the timeline's parts a caller sees beyond the
// audit rows the route's gate already admits them to. Two parts belong to
// other permissions, and are FILTERED on them rather than gated, so a caller
// is shown what they may see instead of being refused the rest:
//
//   - a task with no audit row comes from task_history alone, and a guest's
//     part of a backup job from the job's task log — both view:task's;
//   - a status change is the inventory's run state, which is view:vm's for a
//     VM (qemu) and view:container's for a container (lxc).
//
// can is hasClusterPerm on the path's cluster — hasGlobalPerm for a global
// kind, whose rows span clusters. Any lookup that fails fails the request,
// rather than serving a page that silently left rows out.
func historyScope(can func(action, resource string) (bool, error)) (includeTasks bool, statusTypes []string, err error) {
	if includeTasks, err = can("view", "task"); err != nil {
		return false, nil, err
	}
	statusTypes = []string{}
	for _, kind := range []struct{ resource, guestType string }{{"vm", "qemu"}, {"container", "lxc"}} {
		allowed, permErr := can("view", kind.resource)
		if permErr != nil {
			return false, nil, permErr
		}
		if allowed {
			statusTypes = append(statusTypes, kind.guestType)
		}
	}
	return includeTasks, statusTypes, nil
}

// viewableGuests keeps the guests the caller may open, at the scope the
// guest's own pages use: a VM (qemu) with view:vm, a container (lxc) with
// view:container on the cluster — viewable is historyScope's statusTypes. The
// rest are dropped whole, rows and membership alike: subject_vmid would
// otherwise tell the caller which guests a folder holds or a node runs. A
// guest the inventory no longer holds, whose type is unknown, needs both.
func viewableGuests(guests []HistoryGuest, types []db.ListGuestTypesRow, viewable []string) []HistoryGuest {
	typeOf := make(map[int32]string, len(types))
	for _, t := range types {
		typeOf[t.Vmid] = t.Type
	}
	kept := make([]HistoryGuest, 0, len(guests))
	for _, g := range guests {
		if t, known := typeOf[g.VMID]; known && slices.Contains(viewable, t) ||
			!known && slices.Contains(viewable, "qemu") && slices.Contains(viewable, "lxc") {
			kept = append(kept, g)
		}
	}
	return kept
}

// ListParams builds ListHistory's arguments for one selection of this kind:
// the selectors and the rules. The caller adds the scope, filters and page,
// as List does. A guest named twice is one guest — a folder's memberships can
// name a VMID twice — unless its two cutoffs differ: ListHistory looks each
// one's cutoff up by VMID, and would hold it to the first only, so that is
// refused. So is a selection ListHistory would read wrong: an object that
// names nothing, a ResourceID or list with no details key to go with it, and
// a worker pattern Postgres refuses, an object of a type the kind does not
// declare — and, for a global kind, which has no cluster (clusterID is
// ignored), any guest, and a details key with no parent id.
func (k HistoryKind) ListParams(clusterID uuid.UUID, sel HistorySelection) (db.ListHistoryParams, error) {
	if k.Global {
		if len(sel.Guests)+len(sel.GuestWorkers) > 0 {
			return db.ListHistoryParams{}, fmt.Errorf("history kind %q is global and selected guests, "+
				"which are a cluster's", k.Name)
		}
		for _, o := range sel.Objects {
			// A details value — a datastore's name — repeats on every server:
			// only its parent's id is unique across Nexara.
			if o.DetailKey != "" && o.ResourceID == "" {
				return db.ListHistoryParams{}, fmt.Errorf("history kind %q is global and selected %+v by a "+
					"details key with no resource id", k.Name, o)
			}
		}
		clusterID = uuid.Nil
	}
	params := db.ListHistoryParams{
		DefaultCategory:     k.Default,
		ClusterID:           clusterID,
		Global:              k.Global,
		ObjectsIgnoreCutoff: k.ObjectsIgnoreGuestCutoff,
		Vmids:               make([]int32, 0, len(sel.Guests)),
		CutoffAfter:         make([]time.Time, 0, len(sel.Guests)),
		CutoffInclusive:     make([]bool, 0, len(sel.Guests)),
		GuestWorkers:        make([]string, 0, len(sel.GuestWorkers)),
		ObjectTypes:         make([]string, 0, len(sel.Objects)),
		ObjectResourceIds:   make([]string, 0, len(sel.Objects)),
		ObjectKeys:          make([]string, 0, len(sel.Objects)),
		ObjectValues:        make([]string, 0, len(sel.Objects)),
		ObjectInList:        make([]bool, 0, len(sel.Objects)),
		ObjectWhole:         make([]bool, 0, len(sel.Objects)),
		HideTypes:           make([]string, 0, len(k.Hide)),
		HideActions:         make([]string, 0, len(k.Hide)),
		HideKeys:            make([]string, 0, len(k.Hide)),
		HideValues:          make([]string, 0, len(k.Hide)),
		// The request's to grant (permittedObservedTypes): none until then.
		ObservedTypes:     []string{},
		ExcludeCategories: []string{},
		WholeCluster:      k.WholeCluster,
		CollapseTypes:     make([]string, 0, len(k.Collapse)),
		CollapseActions:   make([]string, 0, len(k.Collapse)),
		CollapseGaps:      make([]int32, 0, len(k.Collapse)),
	}
	if k.WholeCluster && len(sel.Guests)+len(sel.GuestWorkers)+len(sel.Objects) > 0 || (k.WholeCluster || k.Global) && sel.Node != "" {
		return db.ListHistoryParams{}, fmt.Errorf("history kind %q reads its whole cluster, or every cluster, and "+
			"selected more", k.Name)
	}
	params.NodeName = sel.Node
	params.IncludeGuests = k.GuestToggle == ""
	for _, c := range k.Collapse {
		params.CollapseTypes = append(params.CollapseTypes, c.ResourceType)
		params.CollapseActions = append(params.CollapseActions, c.Action)
		params.CollapseGaps = append(params.CollapseGaps, safeconv.Int32(int(c.Gap/time.Second)))
	}
	for _, h := range k.Hide {
		params.HideTypes = append(params.HideTypes, h.ResourceType)
		params.HideActions = append(params.HideActions, h.Action)
		params.HideKeys = append(params.HideKeys, h.DetailKey)
		params.HideValues = append(params.HideValues, h.DetailValue)
	}
	for _, w := range sel.GuestWorkers {
		if w == "" || !likePatternOK(w) {
			return db.ListHistoryParams{}, fmt.Errorf("history worker pattern %q is empty or ends in a lone backslash", w)
		}
		params.GuestWorkers = append(params.GuestWorkers, w)
	}
	floorless := false
	for _, g := range sel.Guests {
		// The zero time is ListHistory's "no cutoff": everything is after it.
		after := time.Time{}
		if g.After.Valid {
			after = g.After.Time
		}
		if i := slices.Index(params.Vmids, g.VMID); i >= 0 {
			if !params.CutoffAfter[i].Equal(after) || params.CutoffInclusive[i] != g.Inclusive {
				return db.ListHistoryParams{}, fmt.Errorf("history selection names guest %d twice, with different cutoffs", g.VMID)
			}
			continue
		}
		params.Vmids = append(params.Vmids, g.VMID)
		params.CutoffAfter = append(params.CutoffAfter, after)
		params.CutoffInclusive = append(params.CutoffInclusive, g.Inclusive)
		// No guest's row comes before the earliest cutoff, so every arm reads
		// from it; none when any guest has none.
		switch {
		case !g.After.Valid:
			floorless = true
		case len(params.Vmids) == 1 || g.After.Time.Before(params.CutoffFloor.Time):
			params.CutoffFloor = g.After
		}
	}
	if floorless {
		params.CutoffFloor = pgtype.Timestamptz{}
	}
	for _, o := range sel.Objects {
		if o.ResourceType == "" || (o.Value == "") != o.WholeType {
			return db.ListHistoryParams{}, fmt.Errorf("history object %+v names no resource type or value, "+
				"or a value beside WholeType", o)
		}
		if !slices.Contains(k.ObjectTypes, o.ResourceType) {
			return db.ListHistoryParams{}, fmt.Errorf("history kind %q selected %q, which its ObjectTypes do not declare",
				k.Name, o.ResourceType)
		}
		if o.DetailKey == "" && (o.ResourceID != "" || o.InList) {
			return db.ListHistoryParams{}, fmt.Errorf("history object %+v has a resource id or list but no details key", o)
		}
		if o.WholeType && (o.DetailKey != "" || o.ResourceID != "" || o.InList) {
			return db.ListHistoryParams{}, fmt.Errorf("history object %+v is a whole type and names an id, key or list", o)
		}
		// A guest's own rows are the guests family's, which the caller's view
		// permission (viewableGuests) and the guest's cutoff hold to: a guest
		// typed object reads them past both unless it is one of the guests —
		// and a whole guest type, which has no value to parse, is none of them.
		if o.DetailKey == "" && (o.ResourceType == "vm" || o.ResourceType == "container") {
			vmid, parseErr := strconv.ParseInt(o.Value, 10, 32)
			if parseErr != nil || !slices.Contains(params.Vmids, int32(vmid)) {
				return db.ListHistoryParams{}, fmt.Errorf("history object %+v is a guest the selection does not hold", o)
			}
		}
		params.ObjectTypes = append(params.ObjectTypes, o.ResourceType)
		params.ObjectResourceIds = append(params.ObjectResourceIds, o.ResourceID)
		params.ObjectKeys = append(params.ObjectKeys, o.DetailKey)
		params.ObjectValues = append(params.ObjectValues, o.Value)
		params.ObjectInList = append(params.ObjectInList, o.InList)
		params.ObjectWhole = append(params.ObjectWhole, o.WholeType)
	}
	params.RuleCategories, params.RuleFields, params.RulePatterns = k.sqlRules()
	return params, nil
}

// HistoryClusterParam is the path parameter that names the cluster of a
// cluster-scoped History. List reads it by this name so that a global kind's
// route, which declares no cluster, does not trip the guard that holds every
// literal parameter key to the route's declaration (registry_paramkey_guard_test.go):
// the registry checks the declaration itself, for the kind it belongs to.
const HistoryClusterParam = "cluster_id"

// HistoryRoute is the handler bound to one kind: its List serves that kind's
// route. A bound method rather than a closure, so the guards that walk a
// handler's declaration can find this one.
type HistoryRoute struct {
	h    *HistoryHandler
	kind HistoryKind
}

// Route binds the handler to one kind.
func (h *HistoryHandler) Route(kind HistoryKind) *HistoryRoute {
	return &HistoryRoute{h: h, kind: kind}
}

// List returns one page of an object's History. The route's gate is
// view:audit, because the audit log is what the timeline is made of, with the
// kind's own view permission beside it; historyScope says what else the
// caller sees. A task's verdict is served at view:audit, as the activity feed
// serves it (ListRecentAuditLogEnriched); the exit text from its task record
// is Proxmox's own words, task data, and only reaches a view:task holder. (An
// ingested task's audit details carry the status the collector saw, at
// view:audit, exactly as the audit page shows them.)
//
// The kind's selector reads the parameters it names, which the registry walk
// in registry_paramkey_guard_test.go cannot follow through a function value;
// the route sweep dispatches every History route, selector included.
func (r *HistoryRoute) List(c fiber.Ctx, p *apischema.Params) error {
	// A global kind's scope is global, as its gate is; every other kind's is
	// the path's cluster.
	clusterID := uuid.Nil
	can := func(action, resource string) (bool, error) { return hasGlobalPerm(c, action, resource) }
	if !r.kind.Global {
		id, err := parseParamUUID(p.String(HistoryClusterParam))
		if err != nil {
			return err
		}
		clusterID = id
		can = func(action, resource string) (bool, error) { return hasClusterPerm(c, action, resource, clusterID) }
	}
	includeTasks, statusTypes, err := historyScope(can)
	if err != nil {
		return err
	}
	visible, err := reservedSettingVisibility(c)
	if err != nil {
		return err
	}

	// The caller's own mistake is a 400 before anything is read. An empty
	// filter is no filter, as on the audit and task listings — but no window
	// is the kind's default one, unless the caller asks for all of it.
	var since pgtype.Timestamptz
	switch v := p.String("start_time"); {
	case v != "":
		t, parseErr := time.Parse(time.RFC3339, v)
		if parseErr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid start_time (use RFC3339)")
		}
		since = pgtype.Timestamptz{Time: t, Valid: true}
	case r.kind.DefaultWindow > 0 && !p.Bool("all_time"):
		since = pgtype.Timestamptz{Time: time.Now().Add(-r.kind.DefaultWindow), Valid: true}
	}
	// end_time lowers the window's top, the start kept — start_time's, or the
	// default window's: the next stretch of a read that stopped at
	// searched_from. One at or before that start is refused, either way.
	var until pgtype.Timestamptz
	if v := p.String("end_time"); v != "" {
		t, parseErr := time.Parse(time.RFC3339, v)
		if parseErr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid end_time (use RFC3339)")
		}
		if since.Valid && !t.After(since.Time) {
			return fiber.NewError(fiber.StatusBadRequest,
				"end_time must be later than start_time, or than the default window's start without one")
		}
		until = pgtype.Timestamptz{Time: t, Valid: true}
	}

	ctx, cancel := context.WithTimeout(c.Context(), historyQueryTimeout)
	defer cancel()
	// The audit rows of a removed cluster are kept (000122), so the route has to ask
	// whether the path's cluster is still there: after the caller's own mistakes, which
	// answer before any query, and before the first read.
	if !r.kind.Global {
		if err := requireCluster(ctx, r.h.queries, clusterID); err != nil {
			return err
		}
	}
	sel, err := r.kind.Select(ctx, HistoryRequest{Queries: r.h.queries, ClusterID: clusterID, Params: p,
		GuestTypes: statusTypes})
	if err != nil {
		return err
	}
	// GuestFromPath exempts the path's guest, and that guest alone, from the
	// filter below.
	if r.kind.GuestFromPath != "" && (len(sel.Guests) != 1 ||
		int64(sel.Guests[0].VMID) != p.Int(r.kind.GuestFromPath) || len(sel.Objects) > 0) {
		slog.Error("history selection refused: a guest-from-path kind selected more than the path's guest", "kind", r.kind.Name)
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to read the history")
	}
	if r.kind.GuestFromPath == "" && len(sel.Guests) > 0 {
		vmids := make([]int32, 0, len(sel.Guests))
		for _, g := range sel.Guests {
			vmids = append(vmids, g.VMID)
		}
		types, typesErr := r.h.queries.ListGuestTypes(ctx, db.ListGuestTypesParams{ClusterID: clusterID, Vmids: vmids})
		if typesErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to read the history")
		}
		sel.Guests = viewableGuests(sel.Guests, types, statusTypes)
		// A guest dropped here takes its own objects with it, rather than
		// leaving them for ListParams to refuse the whole History over.
		sel.Objects = slices.DeleteFunc(sel.Objects, func(o HistoryObject) bool {
			return o.DetailKey == "" && (o.ResourceType == "vm" || o.ResourceType == "container") &&
				!slices.ContainsFunc(sel.Guests, func(g HistoryGuest) bool { return strconv.Itoa(int(g.VMID)) == o.Value })
		})
	}
	params, err := r.kind.ListParams(clusterID, sel)
	if err != nil {
		slog.Error("history selection refused", "kind", r.kind.Name, "cluster_id", clusterID, "error", err)
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to read the history")
	}
	params.IncludeTasks = includeTasks
	params.StatusGuestTypes = statusTypes
	if params.ObservedTypes, err = permittedObservedTypes(r.kind, can); err != nil {
		return err
	}
	params.RowLimit = safeconv.Int32(int(p.Int("limit")))
	params.RowOffset = safeconv.Int32(int(p.Int("offset")))
	params.Since, params.Until = since, until
	if category := p.String("category"); category != "" {
		params.Category = pgtype.Text{String: category, Valid: true}
	}
	if excluded := p.String("exclude_category"); excluded != "" {
		params.ExcludeCategories = strings.Split(excluded, ",")
	}
	if source := p.String("source"); source != "" {
		params.Source = pgtype.Text{String: source, Valid: true}
	}
	if r.kind.GuestToggle != "" {
		params.IncludeGuests = p.Bool(r.kind.GuestToggle)
	}
	params.RowCap = safeconv.Int32(r.kind.RowCap)
	if params.Category.Valid || len(params.ExcludeCategories) > 0 || params.Source.Valid {
		params.RowCap = safeconv.Int32(r.kind.FilteredRowCap)
	}
	rows, err := r.h.history.ListHistory(ctx, params)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to read the history")
	}

	// Every row carries the same total and horizon; a page past the end
	// carries no entry, and reports 0 — the client snaps back to a page that
	// exists — but for a capped read's one row with none (EntryID ""), there
	// only to say how far back it searched.
	page := historyPage{Items: make([]historyEntry, 0, len(rows))}
	if sel.GuestsTotal > 0 {
		page.Guests = &historyGuestCount{Followed: len(sel.Guests), Total: sel.GuestsTotal}
	}
	for _, row := range rows {
		if row.SearchedFrom.Valid && page.SearchedFrom == nil {
			s := row.SearchedFrom.Time.Format(time.RFC3339Nano)
			page.SearchedFrom = &s
		}
		if row.EntryID == "" {
			continue
		}
		page.Total = row.Total
		page.Items = append(page.Items, toHistoryEntry(row, visible, includeTasks))
	}
	if err := r.attachAlsoChanged(ctx, &page, params); err != nil {
		return err
	}
	return c.JSON(page)
}

// historyAlsoChanged is one object an audit entry's edit changed in Proxmox
// beyond the fields the entry names, and those fields.
type historyAlsoChanged struct {
	ResourceType string   `json:"resource_type"`
	ResourceID   string   `json:"resource_id"`
	Node         string   `json:"node,omitempty"`
	Fields       []string `json:"fields"`
}

// attachAlsoChanged names on each audit entry of the page what the changes it
// explained covered without its naming them (explained_unnamed, recorded by the
// observer's classifiers): one read for the page, of the types the caller may
// view in its cluster.
func (r *HistoryRoute) attachAlsoChanged(ctx context.Context, page *historyPage, params db.ListHistoryParams) error {
	if len(params.ObservedTypes) == 0 {
		return nil
	}
	ids := make([]string, 0, len(page.Items))
	at := make(map[string]int, len(page.Items))
	for i, e := range page.Items {
		if id, audit := strings.CutPrefix(e.ID, "audit:"); audit {
			ids = append(ids, id)
			at[id] = i
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := r.h.queries.ListHistoryAlsoChanged(ctx, db.ListHistoryAlsoChangedParams{
		AuditIds: ids, ObservedTypes: params.ObservedTypes, Global: params.Global, ClusterID: params.ClusterID,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to read the history")
	}
	for _, row := range rows {
		if i, ok := at[row.AuditID.String()]; ok {
			var objects []historyAlsoChanged
			if json.Unmarshal(row.Objects, &objects) == nil {
				page.Items[i].AlsoChanged = objects
			}
		}
	}
	return nil
}

// historyPage is a History route's response: the list envelope every
// collection returns (ListResponse), and how far back a capped read searched.
type historyPage struct {
	Items []historyEntry `json:"items"`
	Total int64          `json:"total"`
	// SearchedFrom is, for a read in which a source reached its cap, the point
	// back to which it is whole: it lists only what is at or after it, and the
	// same read with end_time set to it lists the next stretch. Absent when the
	// read is whole.
	SearchedFrom *string `json:"searched_from,omitempty"`
	// Guests is, for a History that follows only some of its guests, how
	// many it follows of how many (HistorySelection.GuestsTotal).
	Guests *historyGuestCount `json:"guests,omitempty"`
}

// historyGuestCount is historyPage.Guests.
type historyGuestCount struct {
	Followed int `json:"followed"`
	Total    int `json:"total"`
}

// toHistoryEntry maps one row. showExitText is view:task — see List.
func toHistoryEntry(r db.ListHistoryRow, visible map[string]bool, showExitText bool) historyEntry {
	e := historyEntry{
		ID:              r.EntryID,
		Kind:            r.Kind,
		OccurredAt:      r.OccurredAt.Format(time.RFC3339Nano),
		Category:        r.Category,
		Source:          r.Source,
		Action:          r.Action,
		ResourceType:    r.ResourceType,
		ResourceID:      r.ResourceID,
		VMID:            r.Vmid,
		SubjectVMID:     r.SubjectVmid,
		UserEmail:       r.UserEmail,
		UserDisplayName: r.UserDisplayName,
		// The same redaction every audit read applies. No row a History
		// selects names a reserved setting today; the rule belongs to the
		// read, not to what the rows happen to hold.
		Details:     auditDetailsFor(r.ResourceType, r.ResourceID, string(r.Details), visible),
		Fields:      r.Fields,
		UPID:        r.Upid,
		Node:        r.Node,
		Description: r.TaskDescription,
		TaskType:    r.TaskType,
		FromStatus:  r.FromStatus,
		ToStatus:    r.ToStatus,
		LogStart:    r.LogStart,
		LogEnd:      r.LogEnd,
		RunCount:    r.RunCount,
	}
	if r.ClusterID.Valid {
		e.ClusterID = uuid.UUID(r.ClusterID.Bytes).String()
	}
	if r.UserID.Valid {
		e.UserID = uuid.UUID(r.UserID.Bytes).String()
	}
	if e.Node == "" {
		e.Node = r.TaskNode
	}
	if e.TaskType == "" {
		e.TaskType = taskTypeFromUPID(r.Upid)
	}
	if r.Upid != "" {
		e.Outcome = historyTaskOutcome(r.TaskStatus, r.TaskExitStatus, r.RecordedStatus)
		e.ExitStatus = r.TaskExitStatus
		if r.TaskStatus == "" && e.Outcome != "" {
			e.ExitStatus = r.RecordedStatus
		}
	}
	if r.TaskProgress.Valid {
		v := r.TaskProgress.Float64
		e.Progress = &v
	}
	// A backup entry ended when the guest's own backup did, not the job.
	finished := r.TaskFinishedAt
	if r.BackupFinishedAt.Valid {
		finished = r.BackupFinishedAt
	}
	if finished.Valid {
		s := finished.Time.Format(time.RFC3339Nano)
		e.FinishedAt = &s
	}
	if r.LastSeenAt.Valid {
		s := r.LastSeenAt.Time.Format(time.RFC3339Nano)
		e.LastSeenAt = &s
	}
	if !showExitText {
		e.ExitStatus = ""
	}
	// upid, node and the ingested status are lifted out of details in SQL, so
	// a redacted row's would pass the redaction by — the reason
	// auditGuestIdentity exists for the audit page's derived fields.
	if auditDetailsRedacted(r.ResourceType, r.ResourceID, visible) {
		e.UPID, e.Node, e.TaskType, e.Description = "", "", "", ""
		e.Outcome, e.ExitStatus, e.Progress, e.FinishedAt = "", "", nil, nil
	}
	return e
}
