package handlers

// How to add a kind of object with a History (the guest is guest_history.go):
//  1. Declare its HistoryKind in handlers/history_<kind>.go: Name, Categories in filter order,
//     the Rules that derive them (first match wins, else Default), and Select — which rows are
//     the object's. SelectObjects is the generic selector: audit rows by resource_type + id.
//  2. Declare its historyRoute in internal/api/registry_history_<kind>.go — path, description,
//     its own view permission — and add one line for it to historyRoutes() (registry_history.go).
//  3. Frontend: locales/en/history-<Name>.json with the same categories in the same order, a
//     HistoryKind in your feature's lib/ (see features/history/types/history.ts), and a
//     <HistoryTimeline> or <HistoryDrawer>. TestHistoryCategoryVocabulary holds the two together.

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
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
}

// NewHistoryHandler creates the handler every History route shares.
func NewHistoryHandler(queries *db.Queries) *HistoryHandler {
	return &HistoryHandler{queries: queries}
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
}

// HistoryField is what a HistoryRule matches on.
type HistoryField string

// The fields a rule can match. They are the only four ListHistory derives a
// category from.
const (
	// HistoryFieldEntry is the entry's kind: audit, task, status or backup.
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
// route's gate authorized, and the request's validated parameters.
type HistoryRequest struct {
	Queries   *db.Queries
	ClusterID uuid.UUID
	Params    *apischema.Params
}

// HistorySelection is which rows are the object's, through ListHistory's two
// families of selector; either or both.
type HistorySelection struct {
	// Guests are the guest-shaped sources: the audit rows naming each guest,
	// a clone into it and its HA resource; its tasks with no audit row, its
	// status changes no task explains and its parts of backup jobs.
	Guests []HistoryGuest
	// Objects are the generic selector's audit rows.
	Objects []HistoryObject
}

// HistoryGuest is one guest a History covers, from its own cutoff.
type HistoryGuest struct {
	VMID int32
	// After, when valid, starts the guest's timeline: from it when Inclusive,
	// after it otherwise (guestHistoryCutoff).
	After     pgtype.Timestamptz
	Inclusive bool
}

// HistoryObject selects every audit row in the cluster of ResourceType whose
// resource_id — or, when DetailKey is set, whose details->>DetailKey — is
// Value.
type HistoryObject struct {
	ResourceType string
	DetailKey    string
	Value        string
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

var historyKindNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

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
	ID         string `json:"id"`
	Kind       string `json:"kind"`
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
	// FromStatus: the change happened after it and by OccurredAt.
	LastSeenAt *string `json:"last_seen_at,omitempty"`
	// LogStart and LogEnd are, for a backup entry, the lines of the job's
	// task log that are this guest's part, by Proxmox's line numbers.
	LogStart int32 `json:"log_start,omitempty"`
	LogEnd   int32 `json:"log_end,omitempty"`
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
// can is hasClusterPerm on the path's cluster. Any lookup that fails fails
// the request, rather than serving a page that silently left rows out.
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

// ListParams builds ListHistory's arguments for one selection of this kind:
// the selectors and the rules. The caller adds the scope, filters and page,
// as List does. A guest named twice is one guest — a folder's memberships can
// name a VMID twice — unless its two cutoffs differ: ListHistory looks each
// one's cutoff up by VMID, and would hold it to the first only, so that is
// refused.
func (k HistoryKind) ListParams(clusterID uuid.UUID, sel HistorySelection) (db.ListHistoryParams, error) {
	params := db.ListHistoryParams{
		DefaultCategory: k.Default,
		ClusterID:       clusterID,
		Vmids:           make([]int32, 0, len(sel.Guests)),
		CutoffAfter:     make([]time.Time, 0, len(sel.Guests)),
		CutoffInclusive: make([]bool, 0, len(sel.Guests)),
		ObjectTypes:     make([]string, 0, len(sel.Objects)),
		ObjectKeys:      make([]string, 0, len(sel.Objects)),
		ObjectValues:    make([]string, 0, len(sel.Objects)),
	}
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
	}
	for _, o := range sel.Objects {
		params.ObjectTypes = append(params.ObjectTypes, o.ResourceType)
		params.ObjectKeys = append(params.ObjectKeys, o.DetailKey)
		params.ObjectValues = append(params.ObjectValues, o.Value)
	}
	params.RuleCategories, params.RuleFields, params.RulePatterns = k.sqlRules()
	return params, nil
}

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
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	includeTasks, statusTypes, err := historyScope(func(action, resource string) (bool, error) {
		return hasClusterPerm(c, action, resource, clusterID)
	})
	if err != nil {
		return err
	}
	visible, err := reservedSettingVisibility(c)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(c.Context(), historyQueryTimeout)
	defer cancel()
	sel, err := r.kind.Select(ctx, HistoryRequest{Queries: r.h.queries, ClusterID: clusterID, Params: p})
	if err != nil {
		return err
	}
	params, err := r.kind.ListParams(clusterID, sel)
	if err != nil {
		slog.Error("history selection refused", "kind", r.kind.Name, "cluster_id", clusterID, "error", err)
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to read the history")
	}
	params.IncludeTasks = includeTasks
	params.StatusGuestTypes = statusTypes
	params.RowLimit = safeconv.Int32(int(p.Int("limit")))
	params.RowOffset = safeconv.Int32(int(p.Int("offset")))
	// An empty filter is no filter, as on the audit and task listings.
	if since := p.String("start_time"); since != "" {
		t, parseErr := time.Parse(time.RFC3339, since)
		if parseErr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid start_time (use RFC3339)")
		}
		params.Since = pgtype.Timestamptz{Time: t, Valid: true}
	}
	if category := p.String("category"); category != "" {
		params.Category = pgtype.Text{String: category, Valid: true}
	}
	if source := p.String("source"); source != "" {
		params.Source = pgtype.Text{String: source, Valid: true}
	}

	rows, err := r.h.queries.ListHistory(ctx, params)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to read the history")
	}

	// Every row carries the same total; a page past the end carries none, and
	// reports 0 — the client snaps back to a page that exists.
	var total int64
	items := make([]historyEntry, 0, len(rows))
	for _, row := range rows {
		total = row.Total
		items = append(items, toHistoryEntry(row, visible, includeTasks))
	}
	return RespondList(c, items, total)
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
		UPID:        r.Upid,
		Node:        r.Node,
		Description: r.TaskDescription,
		TaskType:    r.TaskType,
		FromStatus:  r.FromStatus,
		ToStatus:    r.ToStatus,
		LogStart:    r.LogStart,
		LogEnd:      r.LogEnd,
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
