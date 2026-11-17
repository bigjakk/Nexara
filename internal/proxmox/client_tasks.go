package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
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
// and it puts a separator back as "%2F", which pveproxy decodes before it
// routes (see validatePathSegment) — so the value re-shapes the path there even
// with nothing in between, though pveproxy resolves none of the dots: the UPID
// becomes "UPID:pve-01:a" and the ".." after it names no child. A proxy in
// front of pveproxy that decodes %2F and removes dot segments is where the
// request moves, and how far is the caller's to choose, one level per "..":
// GetTaskStatus appends "/status", so a UPID of "A/../.." resolves exactly onto
// /nodes/{node}/status. StopNodeTask appends NOTHING, which makes it the worst
// of the three — the caller owns the whole tail, so
// "A/../../certificates/custom" resolves onto DELETE
// /nodes/{node}/certificates/custom, PVE's "remove the custom certificate". The
// two GETs are reachable from a route that grants only view:task, which the
// built-in Viewer role holds; and POST /api/v1/tasks (manage:task) stores a
// caller-supplied upid that reconcileRunningTasks later replays through
// GetTaskStatus unattended, so the traversal need not even be issued by the
// caller who wrote it. That route now anchors the upid itself (registry_tasks.go:
// "UPID:" and no "/", "\" or control character) and its node to
// node-name-or-empty, but neither held for rows stored before those anchors
// landed, and the collector's replays of those rows reach only the client's
// guards — this one for the upid, validateNodeName for the node — so those
// are what hold them.
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

// upidNodePattern is a UPID's node field as pve-common's PVE::UPID::decode
// matches it: "UPID:([a-zA-Z0-9]([a-zA-Z0-9\-]*[a-zA-Z0-9])?):…".
var upidNodePattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$`)

// TaskNode is the node a UPID names, the one that ran the task. Only that node
// holds the task's status and log: decode puts both under the node-local
// /var/log/pve/tasks, and pve-manager's Tasks.pm answers "no such task" for a
// UPID whose file is not there. It is not always the node the request was
// sent for: pve-storage's upload (Status.pm) has no proxyto, so its imgcopy
// worker runs where the request landed and scp's the file to the target.
//
// Only the node field is read, not the whole shape (see validateTaskUPID for
// why a full-shape pattern would be too strict).
func TaskNode(upid string) (string, error) {
	rest, ok := strings.CutPrefix(upid, "UPID:")
	node, _, terminated := strings.Cut(rest, ":")
	if !ok || !terminated || !upidNodePattern.MatchString(node) {
		return "", fmt.Errorf("%w: the UPID names no node", ErrInvalidInput)
	}
	return node, nil
}

// validateTaskRequest guards the node-task calls: node and upid as path
// segments, and node as the one TaskNode reads from upid. It is a check, not
// a substitution: the client dials only the node its caller passed, so the
// caller's membership check on that node (nodemember) still covers what is
// dialled, and a caller that passes another node is refused here, before any
// request, instead of being answered "no such task" by Proxmox.
func validateTaskRequest(node, upid string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateTaskUPID(upid); err != nil {
		return err
	}
	taskNode, err := TaskNode(upid)
	if err != nil {
		return err
	}
	if taskNode != node {
		return fmt.Errorf("%w: the task ran on node %q, not %q, and only that node holds it", ErrInvalidInput, taskNode, node)
	}
	return nil
}

func (c *Client) GetTaskStatus(ctx context.Context, node string, upid string) (*TaskStatus, error) {
	if err := validateTaskRequest(node, upid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/tasks/" + url.PathEscape(upid) + "/status"
	var status TaskStatus
	if err := c.do(ctx, path, &status); err != nil {
		return nil, fmt.Errorf("get task status on %s: %w", node, err)
	}
	return &status, nil
}

// TaskLogPageLines is the most lines one GetTaskLog call returns. A reader
// that pages through a log knows it has reached the end by a shorter page.
const TaskLogPageLines = 5000

func (c *Client) GetTaskLog(ctx context.Context, node string, upid string, start int) ([]TaskLogEntry, error) {
	if err := validateTaskRequest(node, upid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/tasks/" + url.PathEscape(upid) + "/log?start=" + strconv.Itoa(start) +
		"&limit=" + strconv.Itoa(TaskLogPageLines)
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
	if err := validateTaskRequest(node, upid); err != nil {
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

// FindNodeTask returns the node task list's entry for one task, which carries
// the task's own end time (the task status call carries none). PVE filters
// since/until on a task's start time, so the list is narrowed to the start
// second and type the UPID carries. nil, nil when the list holds no such task
// or the UPID does not carry them.
func (c *Client) FindNodeTask(ctx context.Context, node, upid string) (*NodeTask, error) {
	if err := validateTaskRequest(node, upid); err != nil {
		return nil, err
	}
	parts := strings.Split(upid, ":")
	if len(parts) < 8 || parts[0] != "UPID" {
		return nil, nil
	}
	start, err := strconv.ParseInt(parts[4], 16, 64)
	if err != nil {
		return nil, nil
	}
	q := url.Values{}
	q.Set("source", "all")
	q.Set("since", strconv.FormatInt(start, 10))
	q.Set("until", strconv.FormatInt(start, 10))
	q.Set("limit", "50")
	if parts[5] != "" {
		q.Set("typefilter", parts[5])
	}
	var tasks []NodeTask
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/tasks?"+q.Encode(), &tasks); err != nil {
		return nil, fmt.Errorf("find task on %s: %w", node, err)
	}
	for i := range tasks {
		if tasks[i].UPID == upid {
			return &tasks[i], nil
		}
	}
	return nil, nil
}

// activeTaskLimit is set because PVE would otherwise cap the list at 50 (and a
// limit of 0 returns nothing). A node's active list is far shorter than this.
const activeTaskLimit = 500

// GetActiveNodeTasks lists the tasks on a node's active list: the running ones,
// which have no EndTime, and those that ended moments ago and are not archived
// yet, which do. Unlike GetNodeTasks it reads no archive, so it stays cheap enough
// to call on every DRS evaluation.
func (c *Client) GetActiveNodeTasks(ctx context.Context, node string) ([]NodeTask, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/tasks?source=active&limit=" + strconv.Itoa(activeTaskLimit)
	var tasks []NodeTask
	if err := c.do(ctx, path, &tasks); err != nil {
		return nil, fmt.Errorf("get active tasks on %s: %w", node, err)
	}
	return tasks, nil
}
