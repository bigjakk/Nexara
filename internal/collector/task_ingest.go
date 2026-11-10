package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/auditkey"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// syncTasks fetches active + finished tasks from every node in a cluster and
// records external (non-Nexara) tasks. Running→done transitions are owned by
// the separate reconcileRunningTasks pass, not by re-ingestion here.
//
// It returns the nodes whose task lists this pass read and deduplicated, and
// separately the members reported offline, whose lists cannot load.
// classifyStatusChanges decides the status changes on listed nodes — a task
// from a node that failed to list is missing, and the change it explains
// would be filed as one no task explains — and the rest wait for a pass that
// lists them, a day at most (statusChangeGiveUp). Per node, so one node that
// cannot list holds back only its own guests. On an offline member only the
// changes to and from "unknown" are decided, which no task explains: its
// other changes — the shutdowns of a node going down for a reboot — wait for
// the tasks it has not handed over yet. (An offline member still holds the
// watermark back, as before.) A task that failed to ingest is logged and does
// not hold its node back; a change only it explains then shows as one no task
// explains, the side to err on.
func (s *Syncer) syncTasks(ctx context.Context, client ProxmoxClient, cluster db.Cluster) (listed, offline []string) {
	// Load high-water mark (default: 24h ago on first run).
	since, err := s.queries.GetTaskSyncState(ctx, cluster.ID)
	if err != nil {
		since = time.Now().Add(-24 * time.Hour).Unix()
	}

	nodes, err := client.GetNodes(ctx)
	if err != nil {
		s.logger.Warn("task sync: failed to get nodes",
			"cluster_id", cluster.ID,
			"error", err,
		)
		return nil, nil
	}

	var maxStartTime int64
	var held []taskEvents
	dedupFailed := false
	fetchFailed := false
	listed = make([]string, 0, len(nodes))
	for _, node := range nodes {
		tasks, err := client.GetNodeTasks(ctx, node.Node, since, 500)
		if err != nil {
			s.logger.Warn("task sync: failed to get tasks",
				"cluster_id", cluster.ID,
				"node", node.Node,
				"error", err,
			)
			// Hold the cluster watermark back (below): the healthy nodes'
			// maxStartTime would otherwise advance `since` past tasks that
			// started on THIS node before the new watermark, and the
			// since-filter would skip them forever once the node recovers.
			fetchFailed = true
			if node.Status == "offline" {
				offline = append(offline, node.Node)
			}
			continue
		}

		// Resolve which UPIDs are already recorded in one batch per node (two
		// indexed lookups) instead of a per-task SELECT pair.
		candidates := make([]string, 0, len(tasks))
		for _, task := range tasks {
			if !skipTaskTypes[task.Type] {
				candidates = append(candidates, task.UPID)
			}
		}
		seen, dedupErr := s.seenTaskUPIDs(ctx, cluster, candidates)
		if dedupErr != nil {
			// Can't dedup this node's tasks safely this tick. Skip ingestion and
			// hold the cluster watermark back (below) so these tasks are retried
			// next tick instead of being skipped past forever by the since-filter.
			// A later successful tick re-lists them and seenTaskUPIDs skips any we
			// already recorded (InsertExternalTaskHistory is ON CONFLICT DO NOTHING).
			dedupFailed = true
			continue
		}

		for _, task := range tasks {
			if task.StartTime > maxStartTime {
				maxStartTime = task.StartTime
			}
			ev, err := s.ingestTask(ctx, cluster, node.Node, task, seen)
			if err != nil {
				s.logger.Warn("task sync: failed to ingest task",
					"cluster_id", cluster.ID,
					"upid", task.UPID,
					"error", err,
				)
			}
			if ev != nil {
				held = append(held, *ev)
			}
		}
		listed = append(listed, node.Node)
	}

	// An SDN apply reloads each node's network as a task of its own: mark those,
	// whichever node's list they came from, now that every list is in.
	s.linkSDNApplyTasks(ctx, cluster)

	// And link the tasks Proxmox ran as parts of one action, then announce the
	// ones held for it.
	s.linkRelatedTasks(ctx, cluster, linkSince(time.Now(), since), held)

	// Advance the high-water mark only when every node was fetched AND deduped
	// cleanly. If any node failed we processed a partial set, so leaving `since`
	// where it is forces a full retry next tick rather than silently skipping
	// the tasks we could not list or ingest (re-listing is cheap and idempotent:
	// seenTaskUPIDs + ON CONFLICT DO NOTHING absorb the replays).
	if !dedupFailed && !fetchFailed && maxStartTime > since {
		if err := s.queries.UpsertTaskSyncState(ctx, db.UpsertTaskSyncStateParams{
			ClusterID:    cluster.ID,
			LastSyncedAt: maxStartTime,
		}); err != nil {
			s.logger.Warn("task sync: failed to update sync state",
				"cluster_id", cluster.ID,
				"error", err,
			)
		}
	}
	return listed, offline
}

// seenTaskUPIDs returns the subset of the candidate UPIDs ingestTask must skip:
// those already recorded — in task_history (Nexara-dispatched or a prior
// external ingest) or in this cluster's audit_log entries (any source) — plus those belonging to a
// cross-cluster migration, which this collector must never record at all.
// Three indexed batch lookups replace the old per-task point SELECTs. Returns
// an error if any lookup fails so the caller can skip ingestion rather than
// risk duplicate rows, or an unscrubbed credential, from a partial set.
//
// The third lookup is not a dedup layer. A cross-cluster migration's die
// message can carry the target cluster's API token, and ingestTask would write
// it verbatim into task_history.exit_status — a Viewer-readable column — with
// no way to scrub it. The first two lookups cannot cover that, because the
// window where it happens is exactly the window where the row is NOT yet
// recorded: startAndPollMigration sets migration_jobs.upid before it inserts
// the task_history and audit_log rows, so a tick landing in between finds
// nothing and ingests the task itself. See queries/proxmox_task_sync.sql for
// the invariant this shares with ListRunningTaskHistoryByCluster's anti-join,
// and for why the two are separate guards rather than copies.
func (s *Syncer) seenTaskUPIDs(ctx context.Context, cluster db.Cluster, upids []string) (map[string]bool, error) {
	seen := make(map[string]bool, len(upids))
	if len(upids) == 0 {
		return seen, nil
	}
	thUPIDs, err := s.queries.ListExistingTaskHistoryUPIDs(ctx, upids)
	if err != nil {
		s.logger.Warn("task sync: batch task_history UPID check failed",
			"cluster_id", cluster.ID, "error", err)
		return seen, err
	}
	for _, u := range thUPIDs {
		seen[u] = true
	}
	alUPIDs, err := s.queries.ListExistingAuditLogUPIDs(ctx, db.ListExistingAuditLogUPIDsParams{ClusterID: cluster.ID, Upids: upids})
	if err != nil {
		s.logger.Warn("task sync: batch audit_log UPID check failed",
			"cluster_id", cluster.ID, "error", err)
		return seen, err
	}
	for _, u := range alUPIDs {
		seen[u] = true
	}
	// Returning the error rather than carrying on is load-bearing here in a way
	// it is not for the two above: a swallowed failure would not merely risk a
	// duplicate row, it would drop the credential guard for this tick and let
	// ingestTask persist PVE's unscrubbed text.
	mjUPIDs, err := s.queries.ListCrossClusterMigrationUPIDs(ctx, upids)
	if err != nil {
		s.logger.Warn("task sync: batch cross-cluster migration UPID check failed",
			"cluster_id", cluster.ID, "error", err)
		return seen, err
	}
	for _, u := range mjUPIDs {
		seen[u] = true
	}
	return seen, nil
}

// ingestTask records a single external (non-Nexara) Proxmox task: a task_history
// row (status from the task — running or classified terminal — so the reconciler
// and the Tasks page see it) plus an audit_log entry (so the activity feed shows
// it), attributed to the system user. Nexara-initiated tasks and tasks already
// seen on a prior tick are skipped.
//
// The task's events are published here, except those of a task linkRelatedTasks
// may link to another (mayLink): they are returned for it to publish once it has,
// so a list draws the row with its marker and does not gain it a moment later.
func (s *Syncer) ingestTask(ctx context.Context, cluster db.Cluster, nodeName string, task proxmox.NodeTask, seen map[string]bool) (held *taskEvents, err error) {
	// Skip noisy task types.
	if skipTaskTypes[task.Type] {
		return nil, nil
	}

	// Skip anything in the seen set, resolved once per node in syncTasks. Two
	// of its three lookups are dedup — task_history (Nexara-dispatched or a
	// prior external ingest) and audit_log (any source); once a task_history
	// row exists, the collector reconciler owns its running→done transition, so
	// re-ingesting would be redundant. The third is not dedup: a cross-cluster
	// migration's UPID is withheld even when nothing has recorded it, because
	// the insert below would persist PVE's die message verbatim and that text
	// can carry the target cluster's API token. See seenTaskUPIDs.
	if seen[task.UPID] {
		return nil, nil
	}

	// The identity comes from the task's type and id and the guest it is stamped
	// with from its UPID (queries/audit_log.sql): a task whose two disagree would
	// file one kind of object under another's guest. A legitimate task never does,
	// but it is still recorded — a slip of ours must never drop a real task, and a
	// skipped one would be listed and warned about again every pass — as the
	// generic task it would be had nothing mapped it, with the type and id its UPID
	// carries (the ones the guest is stamped from), flagged identity_mismatch.
	// Recorded, it is in the seen set from the next pass on, so it warns once.
	reportedType, reportedID := task.Type, task.ID
	mismatch := !upidAgrees(task)
	if mismatch {
		task.Type, task.ID, _ = upidTypeAndID(task.UPID)
		s.logger.Warn("task sync: a task's type or id disagrees with its UPID; recorded as a generic task",
			"cluster_id", cluster.ID, "node", nodeName, "upid", task.UPID,
			"reported_type", cleanTaskText(reportedType), "reported_id", cleanTaskText(reportedID))
	}

	// Map task type to audit log resource_type + action + resource_id.
	var (
		mapping    taskMapping
		resourceID string
		ok         bool
	)
	if !mismatch {
		mapping, resourceID, ok = mapTask(task.Type, task.ID, nodeName)
	}
	if !ok {
		// Unmapped types fall through as generic proxmox_task.
		mapping = taskMapping{
			ResourceType: "proxmox_task",
			Action:       task.Type,
		}
		resourceID = task.ID
		if !auditkey.ValidID(resourceID) {
			resourceID = nodeName
		}
	}

	// Resolve the guest name from the database when the task targets a guest,
	// and record it in the entry.
	//
	// Keyed on the UPID's worker type rather than mapping.ResourceType: the
	// types that carry a VMID but have no entry in proxmoxTaskMap — hamigrate,
	// hastart, hastop, resize — fall through to resource_type "proxmox_task",
	// and those were exactly the entries that reached the audit log with no
	// name at all. Recording it here is what lets the read side resolve the
	// name from the entry instead of re-deriving it from a live vms row: VMIDs
	// are reused, so a lookup done later can return whichever guest holds the
	// id *now*, which for a destroy entry is a different guest entirely.
	resourceName := ""
	if !mismatch && isGuestTaskType(task.Type) {
		if vmid, err := strconv.ParseInt(task.ID, 10, 32); err == nil {
			if vm, err := s.queries.GetVMByClusterAndVmid(ctx, db.GetVMByClusterAndVmidParams{
				ClusterID: cluster.ID,
				Vmid:      int32(vmid),
			}); err == nil {
				resourceName = vm.Name
			}
		}
	}

	// Record the task in task_history so the collector reconciler tracks its
	// running→done lifecycle and the activity feed + Tasks page show live,
	// server-authoritative status — exactly like a Nexara-dispatched task.
	// An end time is PVE's authoritative "finished" signal: with source=all the
	// task list returns in-flight tasks with status="RUNNING" and no endtime, so
	// a non-empty status is NOT a reliable finished marker (it would misfile a
	// running task as failed). Store finished tasks fully-formed; everything else
	// as "running" so the reconciler can flip it when it actually completes.
	taskStatus := "running"
	var taskExit string
	var finishedAt pgtype.Timestamptz
	if task.EndTime > 0 {
		taskStatus, taskExit = classifyTaskExit(task.Status)
		finishedAt = pgtype.Timestamptz{Time: time.Unix(task.EndTime, 0), Valid: true}
	}
	taskDescription := describeTask(mapping, resourceID, resourceName, task.ID)
	if err := s.queries.InsertExternalTaskHistory(ctx, db.InsertExternalTaskHistoryParams{
		ClusterID:   cluster.ID,
		UserID:      auth.SystemUserID,
		Upid:        task.UPID,
		Description: taskDescription,
		Status:      taskStatus,
		ExitStatus:  taskExit,
		Node:        nodeName,
		TaskType:    task.Type,
		StartedAt:   time.Unix(task.StartTime, 0),
		FinishedAt:  finishedAt,
	}); err != nil {
		return nil, fmt.Errorf("insert external task_history: %w", err)
	}

	// Build details JSON.
	detailMap := map[string]interface{}{
		"upid":         task.UPID,
		"proxmox_user": proxmoxUser(task),
		"node":         nodeName,
		"status":       task.Status,
		"task_type":    task.Type,
		"resource_id":  cleanTaskText(task.ID),
	}
	if resourceName != "" {
		detailMap["resource_name"] = resourceName
	}
	if mismatch {
		detailMap["identity_mismatch"] = true
		detailMap["reported_type"] = cleanTaskText(reportedType)
		detailMap["reported_id"] = cleanTaskText(reportedID)
	}
	if mapping.ResourceType == "storage" {
		// A storage row's resource_id is the storage's name. The key a
		// Nexara-dispatched storage task carries too, so one reader finds both.
		detailMap["storage"] = resourceID
	}
	details, _ := json.Marshal(detailMap)

	if err := s.queries.InsertAuditLogWithSource(ctx, db.InsertAuditLogWithSourceParams{
		ClusterID:    pgtype.UUID{Bytes: cluster.ID, Valid: true},
		UserID:       pgtype.UUID{Bytes: auth.SystemUserID, Valid: true},
		ResourceType: mapping.ResourceType,
		ResourceID:   resourceID,
		Action:       mapping.Action,
		Details:      details,
		Source:       "proxmox",
		CreatedAt:    time.Unix(task.StartTime, 0),
	}); err != nil {
		return nil, fmt.Errorf("insert audit log: %w", err)
	}

	ev := taskEvents{upid: task.UPID, taskType: task.Type, resourceType: mapping.ResourceType, resourceID: resourceID, action: mapping.Action}
	if mayLink(task) {
		return &ev, nil
	}
	s.publishTaskEvents(ctx, cluster, ev)
	return nil, nil
}

// taskEvents are the two events an ingested task is announced with.
type taskEvents struct {
	upid, taskType, resourceType, resourceID, action string
}

// publishTaskEvents publishes them for real-time WS updates: KindTaskCreated
// drives the Tasks page + activity feed; KindAuditEntry drives the audit-log page
// + feed. The reconciler later fires KindTaskUpdate when a running task finishes.
func (s *Syncer) publishTaskEvents(ctx context.Context, cluster db.Cluster, ev taskEvents) {
	if s.eventPub == nil {
		return
	}
	s.eventPub.ClusterEvent(ctx, cluster.ID.String(), events.KindTaskCreated, "task", ev.upid, ev.taskType)
	s.eventPub.ClusterEvent(ctx, cluster.ID.String(), events.KindAuditEntry, ev.resourceType, ev.resourceID, ev.action)
}

// linkWindow is how far back a pass looks for tasks to link, at least.
const linkWindow = 24 * time.Hour

// linkSince is where a pass's links start: a day back, or a quarter of an hour
// before the task watermark when that is earlier, so the tasks a pass ingests
// after an outage are still linked to the ones that caused them.
func linkSince(now time.Time, watermark int64) time.Time {
	since := now.Add(-linkWindow)
	if w := time.Unix(watermark, 0).Add(-15 * time.Minute); w.Before(since) {
		since = w
	}
	return since
}

// linkableWorkers are the task types the two link queries may link to the task
// that caused them, when root@pam itself ran one. Only which events are held
// depends on this copy of the queries' lists: a type missing here is linked all
// the same, its row drawn without its marker for a moment first.
var linkableWorkers = map[string]bool{
	"qmstart": true, "vzstart": true,
	"qmshutdown": true, "qmstop": true, "vzshutdown": true, "vzstop": true,
	"qmigrate": true, "vzmigrate": true,
}

// mayLink reports whether a task is one the link queries may link.
func mayLink(task proxmox.NodeTask) bool {
	return linkableWorkers[task.Type] && proxmoxUser(task) == "root@pam"
}

// linkRelatedTasks records which of the tasks Proxmox ran belong to one action —
// a live migration's start on its target node, an HA request and the task that
// does its work — so the lists can mark them (queries/proxmox_task_sync.sql),
// then publishes the events held for it. A failed link is only logged: the rows
// stay as they were ingested, unmarked, and the next pass tries again.
//
// The rows recorded before the links existed get them from backfillLinks, once
// per cluster and process. While it runs the pass links nothing itself: two
// statements pairing the same rows at once could each give a request a
// different worker.
func (s *Syncer) linkRelatedTasks(ctx context.Context, cluster db.Cluster, since time.Time, held []taskEvents) {
	v, _ := s.linkBackfill.LoadOrStore(cluster.ID, &linkBackfillState{})
	st, _ := v.(*linkBackfillState)
	st.mu.Lock()
	if !st.running && !st.done && time.Since(st.lastTry) >= linkBackfillRetry {
		st.running, st.lastTry = true, time.Now()
		s.linkBackfills.Add(1)
		// Its own lifetime: a pass's context ends with the pass.
		go s.backfillLinks(context.WithoutCancel(ctx), cluster, st)
	}
	running := st.running
	st.mu.Unlock()

	var linked int64
	if !running {
		linked, _ = s.runLinks(ctx, cluster, since)
	}
	for _, ev := range held {
		s.publishTaskEvents(ctx, cluster, ev)
	}
	s.announceLinks(ctx, cluster, linked)
}

// runLinks runs the two link queries from since on and reports how many rows
// they linked and whether both ran. The starts first, so a request that the HA
// manager had nothing to do for cannot take a migration's start as its worker.
func (s *Syncer) runLinks(ctx context.Context, cluster db.Cluster, since time.Time) (linked int64, ok bool) {
	starts, err := s.queries.LinkMigrationTargetStarts(ctx, db.LinkMigrationTargetStartsParams{ClusterID: cluster.ID, Since: since})
	if err != nil {
		s.logger.Warn("task sync: failed to link the target starts of migrations", "cluster_id", cluster.ID, "error", err)
		return 0, false
	}
	workers, err := s.queries.LinkHAWorkerTasks(ctx, db.LinkHAWorkerTasksParams{ClusterID: cluster.ID, Since: since})
	if err != nil {
		s.logger.Warn("task sync: failed to link HA requests to their tasks", "cluster_id", cluster.ID, "error", err)
		return starts, false
	}
	return starts + workers, true
}

// announceLinks tells the lists that rows they may already have drawn changed.
func (s *Syncer) announceLinks(ctx context.Context, cluster db.Cluster, linked int64) {
	if linked == 0 || s.eventPub == nil {
		return
	}
	s.eventPub.ClusterEvent(ctx, cluster.ID.String(), events.KindTaskUpdate, "task", "", "linked")
	s.eventPub.ClusterEvent(ctx, cluster.ID.String(), events.KindAuditEntry, "task", "", "linked")
}

// linkBackfillState is one cluster's backfillLinks: whether it is running or
// done, and when it was last started.
type linkBackfillState struct {
	mu            sync.Mutex
	running, done bool
	lastTry       time.Time
}

const (
	// linkBackfillStep and linkBackfillSteps are how backfillLinks walks back:
	// three years a month at a time, then everything older at once.
	linkBackfillStep  = 30 * 24 * time.Hour
	linkBackfillSteps = 36
	// linkBackfillTimeout bounds one attempt, and linkBackfillRetry spaces
	// them when one fails.
	linkBackfillTimeout = 30 * time.Minute
	linkBackfillRetry   = 15 * time.Minute
)

// backfillLinks links a cluster's whole recorded history. Not a migration and not
// inside a pass, because it rewrites a row per pair, seconds per ten thousand: a
// migration that long outlasts the healthcheck and one stopped midway leaves the
// database dirty, and a pass that long holds up every other part of the sync. It
// goes back a month at a time, each step its own statements, so a stop loses one
// step and the next start finds the rest already linked. A step that fails ends
// the attempt; the passes link their own window again and a later one retries.
func (s *Syncer) backfillLinks(ctx context.Context, cluster db.Cluster, st *linkBackfillState) {
	defer s.linkBackfills.Done()
	ctx, cancel := context.WithTimeout(ctx, linkBackfillTimeout)
	defer cancel()

	var linked int64
	ok := true
	now := time.Now()
	for step := 1; ok && step <= linkBackfillSteps+1; step++ {
		since := time.Unix(0, 0)
		if step <= linkBackfillSteps {
			since = now.Add(-time.Duration(step) * linkBackfillStep)
		}
		var n int64
		n, ok = s.runLinks(ctx, cluster, since)
		linked += n
	}
	st.mu.Lock()
	st.running, st.done = false, ok
	st.mu.Unlock()
	if linked > 0 {
		s.logger.Info("task sync: linked the tasks recorded as parts of one action", "cluster_id", cluster.ID, "linked", linked)
	}
	s.announceLinks(ctx, cluster, linked)
}

// upidTypeAndID is the worker type and id a UPID carries (fields 6 and 7 of
// UPID:node:pid:pstart:starttime:type:id:user:). ok is false for a UPID that is
// not in the full form.
func upidTypeAndID(upid string) (taskType, id string, ok bool) {
	parts := strings.Split(upid, ":")
	if len(parts) < 8 || parts[0] != "UPID" {
		return "", "", false
	}
	return parts[5], parts[6], true
}

// upidAgrees reports whether a task's type and id are the ones its UPID carries.
// A UPID that is not in the full form carries none to disagree with.
func upidAgrees(task proxmox.NodeTask) bool {
	taskType, id, ok := upidTypeAndID(task.UPID)
	return !ok || (taskType == task.Type && id == task.ID)
}

// cleanTaskText bounds text taken from a task for an audit detail or a
// description: capped, and control characters made visible.
func cleanTaskText(s string) string {
	r := []rune(s)
	if len(r) > auditkey.MaxIDLen {
		r = r[:auditkey.MaxIDLen]
	}
	for i, c := range r {
		if unicode.IsControl(c) {
			r[i] = '?'
		}
	}
	return string(r)
}

// describeTask is the Tasks page's description of an ingested task: the
// mapping's wording where it has one, then the object it was done to.
func describeTask(mapping taskMapping, resourceID, resourceName, taskID string) string {
	description := mapping.Action
	if mapping.Describe != "" {
		description = mapping.Describe
	}
	switch {
	case resourceName != "":
		return description + " " + resourceName
	case mapping.Describe != "" && mapping.Fixed == "":
		return description + " " + cleanTaskText(resourceID)
	case mapping.Describe == "" && taskID != "":
		return description + " " + cleanTaskText(taskID)
	}
	return description
}

// sdnApplyLinkWindow is how far back an ingest pass looks for node reloads of an
// SDN apply it has not marked yet: a pass that follows an outage still finds them.
const sdnApplyLinkWindow = 24 * time.Hour

// linkSDNApplyTasks marks the per-node network reloads of an SDN apply as parts
// of it (LinkSDNApplyNodeTasks). A failure is only logged: the rows stay as the
// node network applies they were ingested as, and a later pass tries again.
func (s *Syncer) linkSDNApplyTasks(ctx context.Context, cluster db.Cluster) {
	if _, err := s.queries.LinkSDNApplyNodeTasks(ctx, db.LinkSDNApplyNodeTasksParams{
		ClusterID: cluster.ID,
		Since:     time.Now().Add(-sdnApplyLinkWindow),
	}); err != nil {
		s.logger.Warn("task sync: failed to mark the node reloads of an SDN apply", "cluster_id", cluster.ID, "error", err)
	}
}

// proxmoxUser is the user a task ran as, token included: user@realm!tokenid for a
// task run by an API token. The node task list reports the token apart from the
// user (NodeTask.TokenID), so a finished task would record the bare user while a
// running one, listed whole, records the token — one task, two spellings.
func proxmoxUser(task proxmox.NodeTask) string {
	if task.TokenID != "" && !strings.Contains(task.User, "!") {
		return task.User + "!" + task.TokenID
	}
	return task.User
}
