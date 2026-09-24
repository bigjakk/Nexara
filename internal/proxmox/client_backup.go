package proxmox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http/httptrace"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

func (c *Client) TriggerBackup(ctx context.Context, node string, params BackupParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if params.VMID == "" {
		return "", fmt.Errorf("vzdump requires vmid")
	}
	form := url.Values{}
	form.Set("vmid", params.VMID)
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}
	if params.Mode != "" {
		form.Set("mode", params.Mode)
	}
	if params.Compress != "" {
		form.Set("compress", params.Compress)
	}
	path := "/nodes/" + url.PathEscape(node) + "/vzdump"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("trigger backup on %s: %w", node, err)
	}
	return upid, nil
}
func (c *Client) ListBackupJobs(ctx context.Context) ([]BackupJob, error) {
	var jobs []BackupJob
	if err := c.do(ctx, "/cluster/backup", &jobs); err != nil {
		return nil, fmt.Errorf("list backup jobs: %w", err)
	}
	return jobs, nil
}
func (c *Client) GetBackupJob(ctx context.Context, id string) (*BackupJob, error) {
	if id == "" {
		return nil, fmt.Errorf("backup job ID is required")
	}
	var job BackupJob
	if err := c.do(ctx, "/cluster/backup/"+url.PathEscape(id), &job); err != nil {
		return nil, fmt.Errorf("get backup job %s: %w", id, err)
	}
	return &job, nil
}
func (c *Client) CreateBackupJob(ctx context.Context, params BackupJobParams) error {
	form := url.Values{}
	if params.Schedule != "" {
		form.Set("schedule", params.Schedule)
	}
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}
	if params.Node != "" {
		form.Set("node", params.Node)
	}
	if params.VMID != "" {
		form.Set("vmid", params.VMID)
	}
	if params.All != nil {
		form.Set("all", strconv.Itoa(*params.All))
	}
	if params.Exclude != "" {
		form.Set("exclude", params.Exclude)
	}
	if params.Pool != "" {
		form.Set("pool", params.Pool)
	}
	if params.Mode != "" {
		form.Set("mode", params.Mode)
	}
	if params.Compress != "" {
		form.Set("compress", params.Compress)
	}
	if params.Enabled != nil {
		form.Set("enabled", strconv.Itoa(*params.Enabled))
	}
	if params.MailNotification != "" {
		form.Set("mailnotification", params.MailNotification)
	}
	if params.MailTo != "" {
		form.Set("mailto", params.MailTo)
	}
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if params.Type != "" {
		form.Set("type", params.Type)
	}
	if err := c.doPost(ctx, "/cluster/backup", form, nil); err != nil {
		return fmt.Errorf("create backup job: %w", err)
	}
	return nil
}
func (c *Client) UpdateBackupJob(ctx context.Context, id string, params BackupJobParams) error {
	if id == "" {
		return fmt.Errorf("backup job ID is required")
	}
	form := url.Values{}
	if params.Schedule != "" {
		form.Set("schedule", params.Schedule)
	}
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}
	if params.Node != "" {
		form.Set("node", params.Node)
	}
	if params.VMID != "" {
		form.Set("vmid", params.VMID)
	}
	if params.All != nil {
		form.Set("all", strconv.Itoa(*params.All))
	}
	if params.Exclude != "" {
		form.Set("exclude", params.Exclude)
	}
	if params.Pool != "" {
		form.Set("pool", params.Pool)
	}
	if params.Mode != "" {
		form.Set("mode", params.Mode)
	}
	if params.Compress != "" {
		form.Set("compress", params.Compress)
	}
	if params.Enabled != nil {
		form.Set("enabled", strconv.Itoa(*params.Enabled))
	}
	if params.MailNotification != "" {
		form.Set("mailnotification", params.MailNotification)
	}
	if params.MailTo != "" {
		form.Set("mailto", params.MailTo)
	}
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if params.Type != "" {
		form.Set("type", params.Type)
	}
	if len(params.Delete) > 0 {
		form.Set("delete", strings.Join(params.Delete, ","))
	}
	if err := c.doPut(ctx, "/cluster/backup/"+url.PathEscape(id), form, nil); err != nil {
		return fmt.Errorf("update backup job %s: %w", id, err)
	}
	return nil
}

// RunBackupJob runs a scheduled backup job now, outside its schedule, the way
// the Proxmox GUI's "Run now" does, and reports what happened on every node it
// considered. Every task in the result is a running vzdump worker the caller
// must record (handlers.TrackTask).
//
// PVE registers no endpoint for this. POST /cluster/backup/{id}/run — what this
// method used to send — matches no handler, and pve-manager PVE/HTTPServer.pm,
// rest_handler, answers an unmatched path with 501 "Method … not implemented",
// so the button could never have worked. The GUI does the run itself, in the
// browser — pve-manager www/manager6/dc/Backup.js, run_backup_now — and this is
// that procedure:
//
//  1. Read the job: GET /cluster/backup/{id} (PVE/API2/Backup.pm, read_job).
//     It answers with the job as stored, its id injected, and with its
//     property strings — fleecing, performance, prune-backups — already parsed
//     into objects: the config parser runs decode_value over every key
//     (pve-common src/PVE/SectionConfig.pm, check_config), and the vzdump job
//     plugin's decode_value parses each key in PROPERTY_STRINGS
//     (pve-guest-common src/PVE/VZDump/JobBase.pm).
//  2. Turn the job into vzdump parameters: backupJobRunForm.
//  3. Choose the nodes: backupJobRunTargets.
//  4. POST the parameters to /nodes/{node}/vzdump on each (PVE/API2/VZDump.pm,
//     vzdump), all at once, as the GUI does.
//
// vzdump answers with a UPID when it forks a worker and with the literal "OK"
// when it starts nothing — at three points, the everyday one being a node that
// holds none of the job's guests on a job that is not an all-guests one. PVE's
// scheduler reads the reply the same way (PVE/Jobs.pm, run_jobs: `elsif ($upid
// eq 'OK') { # some jobs return OK immediately`). So a reply is a task only
// when it carries the "UPID:" prefix every PVE task id starts with (pve-common
// src/PVE/UPID.pm, encode) and passes validateTaskUPID, and "OK" is a node with
// nothing to back up. Anything else leaves that node unconfirmed: whether it
// started a backup cannot be told, and calling it either would be a guess —
// the dangerous guess being "failed", which invites a second run on top of one
// that is going. A failed request is sorted the same way, by vzdumpRefused.
func (c *Client) RunBackupJob(ctx context.Context, id string) (*BackupJobRun, error) {
	if err := validatePathSegment("backup job ID", id); err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := c.do(ctx, "/cluster/backup/"+url.PathEscape(id), &raw); err != nil {
		return nil, fmt.Errorf("run backup job %s: read the job: %w", id, err)
	}
	plan, err := backupJobRunForm(raw)
	if err != nil {
		return nil, fmt.Errorf("run backup job %s: %w", id, err)
	}
	nodes, err := c.GetNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("run backup job %s: %w", id, err)
	}
	targets, offline, err := backupJobRunTargets(plan.pinned, nodes)
	if err != nil {
		return nil, fmt.Errorf("run backup job %s: %w", id, err)
	}

	type outcome struct {
		reply string
		err   error
	}
	outcomes := make([]outcome, len(targets))
	var wg sync.WaitGroup
	for i, node := range targets {
		wg.Go(func() {
			reply, err := c.startVzdump(ctx, node, plan.form)
			outcomes[i] = outcome{reply: reply, err: err}
		})
	}
	wg.Wait()

	run := &BackupJobRun{StopsRunningBackups: plan.stops}
	failed := offline
	for i, node := range targets {
		o := outcomes[i]
		switch {
		case errors.Is(o.err, ErrInvalidInput) || errors.Is(o.err, ErrRequestNotSent):
			// Never sent: startVzdump's own node-name guard (the only
			// ErrInvalidInput either of its calls returns), or a request that
			// was never written. Nothing reached Proxmox.
			failed = append(failed, BackupJobRunFailure{Node: node, Err: o.err})
			continue
		case o.err != nil && vzdumpRefused(o.err):
			failed = append(failed, BackupJobRunFailure{Node: node, Err: o.err})
		case o.err != nil:
			run.Unconfirmed = append(run.Unconfirmed, BackupJobRunFailure{Node: node, Err: o.err})
		case o.reply == "OK":
			run.Skipped = append(run.Skipped, node)
		case strings.HasPrefix(o.reply, "UPID:"):
			if err := validateTaskUPID(o.reply); err != nil {
				// A worker almost certainly forked — the reply has a task id's
				// shape — but one that cannot be polled or recorded. %v, not
				// %w: the guard's error wraps ErrInvalidInput, which means "the
				// caller's own input" to mapProxmoxError, and this is
				// Proxmox's reply.
				run.Unconfirmed = append(run.Unconfirmed, BackupJobRunFailure{Node: node, Err: fmt.Errorf(
					"%w: vzdump on %s answered with a task id that cannot be tracked: %v",
					ErrInvalidResponse, node, err)})
			} else {
				run.Tasks = append(run.Tasks, BackupJobRunTask{Node: node, UPID: o.reply})
			}
		default:
			run.Unconfirmed = append(run.Unconfirmed, BackupJobRunFailure{Node: node, Err: fmt.Errorf(
				"%w: vzdump on %s answered %q, which is neither a task id nor \"OK\"",
				ErrInvalidResponse, node, o.reply)})
		}
		run.Sent++
	}
	slices.SortFunc(failed, func(a, b BackupJobRunFailure) int { return strings.Compare(a.Node, b.Node) })
	run.Failed = failed
	return run, nil
}

// vzdumpRefused reports whether Proxmox answered a vzdump request with a
// refusal, which means no worker was forked on that node.
//
// A 595 is one. pveproxy forwards the request to the node that runs it with
// AnyEvent::HTTP and relays that library's own failure statuses as they are
// (pve-http-server src/PVE/APIServer/AnyEvent.pm, proxy_request, passes Status
// and Reason through), and AnyEvent::HTTP sets 595 while it is still
// connecting: `my $ae_error = 595; # connecting` in http_request, switched to
// 596 at the top of handle_actual_request, before push_write sends a byte of
// the request. So a 595 never reached that node.
//
// A 4xx is one. vzdump (PVE/API2/VZDump.pm) forks its worker in its last
// statement, `return $rpcenv->fork_worker(...)`, and its parameter schema
// (400) and every permission check (403) come before it — as does the
// authentication a 401 fails, and any 4xx a proxy in front of pveproxy
// answers without passing the request on. So is a 501: the answer
// pve-manager PVE/HTTPServer.pm, rest_handler, gives when no handler matched
// at all.
//
// A 500 is NOT, although most of vzdump's dies come before the fork. Once
// fork_worker has released the child, its parent still takes the task-list
// lock in active_workers — with a 10-second timeout and `die $@ if $@`
// (pve-common src/PVE/RESTEnvironment.pm) — so a 500 can arrive with the
// backup running. Nor is anything above 501: a proxy in front of pveproxy
// answers 502–504 when its own leg fails, which may be after the request
// arrived, and pveproxy relays the failure statuses of the HTTP library it
// forwards the request to the node with, as they are (pve-http-server
// src/PVE/APIServer/AnyEvent.pm, proxy_request, passes Status and Reason
// through; AnyEvent::HTTP reserves 590–599 for its own failures, 596 being
// "errors during TLS negotiation, request sending and header processing" —
// a timeout waiting on a node that did fork is one). 599 is not a 595 either,
// although its documentation reads "garbled URL etc.": read_response raises
// it too, as "Invalid server response" and "Garbled response headers", after
// the request has gone. Those, a connection that failed once the request was
// written, and an unreadable reply leave the node unconfirmed.
func vzdumpRefused(err error) bool {
	if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
		return true
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return (apiErr.StatusCode >= 400 && apiErr.StatusCode < 500) || apiErr.StatusCode == 501 ||
		apiErr.StatusCode == 595
}

// startVzdump sends one node its share of a backup job run and returns
// vzdump's reply as it came: a UPID or "OK" (see RunBackupJob).
//
// A failure before the request was written to a connection — the dial
// refused or timed out, the TLS handshake or its fingerprint check failed —
// comes back wrapping ErrRequestNotSent, because nothing left this process
// and Proxmox cannot have acted on it. That is read off net/http/httptrace's
// WroteRequest, which the transport calls once it has written the request to
// the connection (its buffer; the flush follows) — with an error if that write
// failed, which still counts as written: some of it may have gone out. So a
// request can count as written without having reached the socket, as on a
// pooled connection that died; that reports the node unconfirmed, the safe
// direction. It is read off the hook rather than off the error because
// apiClient flattens the transport's error into text (%s, not %w), so a
// *net.OpError is not reachable through it — and changing that wrapping would
// change what rolling.Orchestrator.interrupted, which is written against it,
// treats as a cancellation.
func (c *Client) startVzdump(ctx context.Context, node string, form url.Values) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	var written atomic.Bool
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteRequest: func(httptrace.WroteRequestInfo) { written.Store(true) },
	})
	var reply string
	if err := c.doPost(ctx, "/nodes/"+url.PathEscape(node)+"/vzdump", form, &reply); err != nil {
		if !written.Load() {
			return "", fmt.Errorf("start backup on %s: %w: %w", node, ErrRequestNotSent, err)
		}
		return "", fmt.Errorf("start backup on %s: %w", node, err)
	}
	return reply, nil
}

// backupJobRunTargets chooses the nodes a job runs on, as run_backup_now does.
// It returns them sorted by name, with every other node recorded as a failure.
//
// The source is GET /nodes (PVE/API2/Nodes.pm, the index of PVE::API2::Nodes),
// read at the moment of the run. The GUI filters its resource store, which it
// fills from GET /cluster/resources (www/manager6/data/ResourceStore.js,
// getNodes); both endpoints set a node's status with the same function,
// extract_node_stats (PVE/API2Tools.pm), so "online" means here exactly what it
// means there: the node has an RRD entry and is an online cluster member (or
// there is no cluster) — an online member with no RRD entry reads "unknown".
// GET /cluster/status would not do: its `online` is the membership flag alone
// (PVE/API2/Cluster.pm, get_status), so it would send vzdump to nodes the GUI
// leaves out. Nor would Nexara's own node table, which is the collector's last
// snapshot rather than the state when the run starts.
//
// A job pinned to a node runs there alone, and only when that node is online:
// otherwise run_backup_now refuses the whole run ("Node '…' from backup job
// isn't online!"), and so does this, with a *NodeNotOnlineError. An unpinned
// job runs on every online node, and each other node becomes a failure — the
// GUI reports each as "Node is offline".
func backupJobRunTargets(pinned *string, nodes []NodeListEntry) ([]string, []BackupJobRunFailure, error) {
	if len(nodes) == 0 {
		// run_backup_now would post to nothing and wait forever for the
		// replies; answering "nothing to back up" would be as wrong.
		return nil, nil, fmt.Errorf("%w: Proxmox listed no nodes", ErrInvalidResponse)
	}
	sorted := slices.Clone(nodes)
	slices.SortFunc(sorted, func(a, b NodeListEntry) int { return strings.Compare(a.Node, b.Node) })

	if pinned != nil {
		for _, n := range sorted {
			if n.Node != *pinned {
				continue
			}
			if n.Status != "online" {
				return nil, nil, &NodeNotOnlineError{Node: n.Node, Status: n.Status}
			}
			return []string{n.Node}, nil, nil
		}
		return nil, nil, &NodeNotOnlineError{Node: *pinned}
	}

	var targets []string
	var offline []BackupJobRunFailure
	for _, n := range sorted {
		if n.Status == "online" {
			targets = append(targets, n.Node)
			continue
		}
		offline = append(offline, BackupJobRunFailure{
			Node: n.Node,
			Err:  &NodeNotOnlineError{Node: n.Node, Status: n.Status},
		})
	}
	return targets, offline, nil
}

// backupJobRunDropped are the job properties run_backup_now deletes before it
// posts the job to vzdump (pve-manager www/manager6/dc/Backup.js). They
// schedule, identify or describe the job and are not vzdump parameters — all
// but node, which run_backup_now reads first and turns into the target instead:
// vzdump's node IS its path parameter (proxyto => 'node', PVE/API2/VZDump.pm),
// and a body value that differs from the path's is refused ("duplicate
// parameter (already defined in URI) with conflicting values!",
// PVE/HTTPServer.pm, rest_handler).
//
// It is the GUI's DENY-list — everything else in the job is forwarded — and
// deliberately not an allow-list of vzdump's own parameters, because of the way
// each fails once Proxmox has moved on and the list has not:
//
//   - The deny-list meets a new JOB-ONLY key by forwarding it, and vzdump refuses
//     the request and names the key, because its parameter schema is
//     additionalProperties => 0. The run fails loudly, on every node, until the
//     key is added here.
//   - An allow-list meets a new VZDUMP key by dropping it, silently. The run
//     succeeds and writes a backup that is not the job's: whatever the key
//     excludes, protects or retains is ignored — and a retention setting that
//     goes missing hands pruning to the storage's prune-backups
//     (PVE/VZDump.pm, new), which can delete archives the job meant to keep.
//
// For a backup, the loud failure is the safe one. PVE's own scheduler can keep
// an allow-list (PVE/Jobs/VZDump.pm, run, keeps what $class->properties()
// names) only because it reads it from the schema of the PVE that is running; a
// copy transcribed into Nexara would go stale with the next release that adds
// an option.
var backupJobRunDropped = []string{
	"enabled", "starttime", "dow", "id", "schedule", "type", "node", "comment", "next-run", "repeat-missed",
}

// backupJobRunPlan is a job read back and turned into what a run sends.
type backupJobRunPlan struct {
	// pinned is the job's node when it names one.
	pinned *string
	// form is what every target node's vzdump receives.
	form url.Values
	// stops is the job's stop flag — BackupJobRun.StopsRunningBackups.
	stops bool
}

// backupJobRunForm turns the job GET /cluster/backup/{id} returned into the
// form run_backup_now posts to vzdump, and reads the two properties that shape
// the run rather than the backup: node (where it goes) and stop (what it does
// before answering).
//
// all is always sent, "1" or "0", because run_backup_now always sends it:
// `job.all = job.all === true ? 1 : 0`, over a model that types all as a
// boolean (pve-cluster-backup, same file). The digits because a boolean API
// parameter accepts no other spelling: check_type in pve-common
// src/PVE/JSONSchema.pm takes "1" and "0" and refuses "true", so the GUI's
// boolean would be refused as it stands. What reads as true is what PVE's own
// parse_boolean (same file) reads as true; the stored job carries a number,
// because SectionConfig's check_value turns a boolean into one.
//
// Every other value keeps its meaning and changes only its spelling — see
// vzdumpFormValues.
func backupJobRunForm(raw json.RawMessage) (backupJobRunPlan, error) {
	var job map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	// Numbers keep the digits Proxmox sent rather than passing through a
	// float64: a bwlimit or a pbs-entries-max goes back exactly as it came.
	dec.UseNumber()
	if err := dec.Decode(&job); err != nil {
		return backupJobRunPlan{}, fmt.Errorf("%w: the job is not a JSON object: %w", ErrInvalidResponse, err)
	}
	if job == nil {
		return backupJobRunPlan{}, fmt.Errorf("%w: Proxmox returned no job", ErrInvalidResponse)
	}

	plan := backupJobRunPlan{form: url.Values{}, stops: backupJobFlagIsTrue(job["stop"])}
	if v, ok := job["node"]; ok && v != nil {
		node, isString := v.(string)
		if !isString {
			return backupJobRunPlan{}, fmt.Errorf("%w: the job's node is %v, not a node name", ErrInvalidResponse, v)
		}
		plan.pinned = &node
	}

	for key, value := range job {
		if key == "all" || slices.Contains(backupJobRunDropped, key) {
			continue
		}
		values, err := vzdumpFormValues(value)
		if err != nil {
			return backupJobRunPlan{}, fmt.Errorf("%w: job property %q: %w", ErrInvalidResponse, key, err)
		}
		for _, v := range values {
			plan.form.Add(key, v)
		}
	}
	plan.form.Set("all", "0")
	if backupJobFlagIsTrue(job["all"]) {
		plan.form.Set("all", "1")
	}
	return plan, nil
}

// backupJobFlagIsTrue is PVE's parse_boolean (pve-common src/PVE/JSONSchema.pm)
// over a decoded JSON value: 1, on, yes or true, in any case.
func backupJobFlagIsTrue(v any) bool {
	var text string
	switch t := v.(type) {
	case bool:
		return t
	case json.Number:
		text = t.String()
	case string:
		text = t
	default:
		return false
	}
	switch strings.ToLower(text) {
	case "1", "on", "yes", "true":
		return true
	}
	return false
}

// vzdumpFormValues spells one job property the way vzdump's form parser reads
// it, or returns nothing for a null.
//
// A string or a number goes as it came, and a boolean as "1" or "0" (see
// backupJobRunForm for why not "true"). An array — exclude-path is one — is
// sent as the key repeated, once per item, which is what vzdump reads as a
// list: pve-http-server src/PVE/APIServer/AnyEvent.pm, decode_urlencoded,
// gathers a repeated key into an array.
//
// An object is a property string the read handed back parsed (RunBackupJob, step
// 1). vzdump takes those as strings only — fleecing and performance are
// type => 'string' with a format in pve-guest-common src/PVE/VZDump/Common.pm,
// and prune-backups is pve-storage's standard option of the same shape
// (src/PVE/Storage/Plugin.pm) — so it is printed back. Every object is, not just
// the three keys the GUI names: decode_value makes an object of exactly the keys
// in PROPERTY_STRINGS and of nothing else, so the two are the same set, and PVE's
// scheduler re-prints them by walking that table rather than by name
// (PVE/Jobs/VZDump.pm, run). A key added to it later then reaches vzdump as the
// string it expects instead of as an object it cannot read.
func vzdumpFormValues(v any) ([]string, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			text, present, err := vzdumpScalar(item)
			if err != nil {
				return nil, err
			}
			if present {
				out = append(out, text)
			}
		}
		return out, nil
	case map[string]any:
		s, err := printPropertyString(t)
		if err != nil || s == "" {
			return nil, err
		}
		return []string{s}, nil
	default:
		text, present, err := vzdumpScalar(t)
		if err != nil || !present {
			return nil, err
		}
		return []string{text}, nil
	}
}

// vzdumpScalar spells one scalar; present is false for a null.
func vzdumpScalar(v any) (text string, present bool, err error) {
	switch t := v.(type) {
	case nil:
		return "", false, nil
	case string:
		return t, true, nil
	case json.Number:
		return t.String(), true, nil
	case bool:
		if t {
			return "1", true, nil
		}
		return "0", true, nil
	default:
		return "", false, fmt.Errorf("a nested %T has no form spelling", v)
	}
}

// printPropertyString prints a property-string object back into a string vzdump
// parses: every key as key=value, sorted. That is what the GUI's
// PVE.Parser.printPropertyString sends for run_backup_now (pve-manager
// www/manager6/Parser.js; called without a default key, it writes every key
// out and sorts the pairs), and PVE's own print_property_string (pve-common
// src/PVE/JSONSchema.pm) sorts its keys too — it only moves a default key to the
// front as a bare value and required keys after it, a shorthand
// parse_property_string does not need: it takes key=value for any key.
//
// An empty value is left out, as the GUI leaves it out: parse_property_string
// refuses "key=" ("missing key in comma-separated list property"). A value
// holding a comma is refused, as print_property_string refuses it ("illegal
// value with commas"), because it would come back as two properties.
func printPropertyString(obj map[string]any) (string, error) {
	keys := slices.Sorted(maps.Keys(obj))
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		text, present, err := vzdumpScalar(obj[k])
		if err != nil {
			return "", fmt.Errorf("%s: %w", k, err)
		}
		if !present || text == "" {
			continue
		}
		if strings.Contains(text, ",") {
			return "", fmt.Errorf("%s: value %q holds a comma, which a property string cannot carry", k, text)
		}
		parts = append(parts, k+"="+text)
	}
	return strings.Join(parts, ","), nil
}
func (c *Client) DeleteBackupJob(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("backup job ID is required")
	}
	if err := c.doDelete(ctx, "/cluster/backup/"+url.PathEscape(id), nil); err != nil {
		return fmt.Errorf("delete backup job %s: %w", id, err)
	}
	return nil
}
