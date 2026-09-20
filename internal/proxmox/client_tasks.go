package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// validateTaskUPID guards a caller-supplied UPID that becomes exactly one
// segment of /nodes/{node}/tasks/{upid}[/status|/log].
//
// It exists because the API layer percent-DECODES this path parameter (taskUPID
// in internal/api/handlers/vms.go). Before that decode the value could not carry
// a traversal — "%2E%2E" was escaped a second time and named a task literally
// called "%2E%2E" — and after it, "UPID:pve-01:a%2F..%2F..%2F..%2Fstatus"
// arrives here as the real string "UPID:pve-01:a/../../../status".
// url.PathEscape does NOT defuse that. It leaves "." and ".." alone entirely,
// so ".." travels raw and resolves upward the moment pveproxy normalises the
// path; and it puts a separator back as "%2F", which Proxmox decodes before it
// resolves the path — the capture-server run recorded on
// forbiddenVolumeIDChars (client_storage.go) is the evidence for that far-side
// behaviour, having watched "%2e%2e%2f" arrive byte-for-byte and become "../"
// upstream. How far the request then moves is the caller's to choose, one level
// per "..": GetTaskStatus appends "/status", so a UPID of "A/../.." resolves
// exactly onto /nodes/{node}/status. StopNodeTask appends NOTHING, which makes
// it the worst of the three — the caller owns the whole tail, so
// "A/../../certificates/custom" resolves onto DELETE
// /nodes/{node}/certificates/custom, PVE's "remove the custom certificate". The
// two GETs are reachable from a route that grants only view:task, which the
// built-in Viewer role holds; and POST /api/v1/tasks (manage:task) stores a
// caller-supplied upid and node that reconcileRunningTasks later replays
// through GetTaskStatus unattended, so the traversal need not even be issued by
// the caller who wrote it.
//
// The check belongs HERE rather than at the handler, and rather than in the
// route declaration, for the two reasons validatePBSTaskUPID records for the
// PBS pair: the declaration matches the value as it ARRIVES, where "%2E%2E"
// carries no dot, so it cannot see through an escape; and this client is the
// choke point every caller goes through — eight packages at the time of
// writing: the collector, the scheduler, the DRS executor, the rolling,
// migration, virtiowin and guesttools engines, and four files in the handler
// package — so a route added later inherits the guard instead of having to
// remember it.
//
// validatePathSegment and not a full-shape UPID pattern, deliberately. PVE
// mints 8 colon-separated fields and PBS 9; an API-token user's half carries a
// "!"; the worker id is legitimately empty ("aptupdate::root@pam:"),
// legitimately non-numeric ("vzdump:local"), legitimately dotted ("osd.1", a
// ceph mgr id) and legitimately carries an "@"; and this repo's own task-create
// route round-trips a value with fewer fields than PVE mints. The frontend's
// own parser (frontend/src/lib/upid.ts) asserts no more than "at least 8 fields
// and the first is UPID". A guard that is too strict here fails SILENTLY rather
// than loudly: reconcileRunningTasks (internal/collector/task_reconcile.go)
// polls these unattended and swallows the error, and after staleTaskGrace marks
// the task failed — so an over-tight pattern would surface as healthy tasks
// recorded as having vanished, not as a 400 anyone could trace. What a UPID
// never contains is the vocabulary validatePathSegment refuses: no recorded
// UPID in this repo's fixtures or in task_history carries a "/", a "\" or a
// control character, and a UPID that did could not be addressed through one
// path segment anyway, so refusing it is a clearer failure than a request that
// silently resolves somewhere else.
func validateTaskUPID(upid string) error {
	return validatePathSegment("UPID", upid)
}

func (c *Client) GetTaskStatus(ctx context.Context, node string, upid string) (*TaskStatus, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateTaskUPID(upid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/tasks/" + url.PathEscape(upid) + "/status"
	var status TaskStatus
	if err := c.do(ctx, path, &status); err != nil {
		return nil, fmt.Errorf("get task status on %s: %w", node, err)
	}
	return &status, nil
}
func (c *Client) GetTaskLog(ctx context.Context, node string, upid string, start int) ([]TaskLogEntry, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateTaskUPID(upid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/tasks/" + url.PathEscape(upid) + "/log?start=" + strconv.Itoa(start) + "&limit=5000"
	var entries []TaskLogEntry
	if err := c.do(ctx, path, &entries); err != nil {
		return nil, fmt.Errorf("get task log on %s: %w", node, err)
	}
	return entries, nil
}

// StopNodeTask aborts a running task via DELETE /nodes/{node}/tasks/{upid}. Best-effort;
// returns an error if the task can't be signalled (e.g. already finished). Does not
// return a UPID.
func (c *Client) StopNodeTask(ctx context.Context, node string, upid string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateTaskUPID(upid); err != nil {
		return err
	}
	path := "/nodes/" + url.PathEscape(node) + "/tasks/" + url.PathEscape(upid)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("stop task on %s: %w", node, err)
	}
	return nil
}
func (c *Client) GetNodeTasks(ctx context.Context, node string, since int64, limit int) ([]NodeTask, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 500
	}
	// source=all returns both active (running) and archived (finished) tasks;
	// PVE's default is archive-only, which would hide in-progress tasks the
	// collector ingests for live status tracking.
	path := "/nodes/" + url.PathEscape(node) + "/tasks?limit=" + strconv.Itoa(limit) + "&since=" + strconv.FormatInt(since, 10) + "&start=0&source=all"
	var tasks []NodeTask
	if err := c.do(ctx, path, &tasks); err != nil {
		return nil, fmt.Errorf("get tasks on %s: %w", node, err)
	}
	return tasks, nil
}
