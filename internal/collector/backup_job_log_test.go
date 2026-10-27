package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// jobLog is a backup job's log as vzdump writes it (PVE/VZDump.pm), on a node
// two hours ahead of UTC: VM 100 backed up, VM 101 failed with a two-line
// error, VM 105 named by the job but gone. Lines 6, 8 and 13 are a failure's
// shape inside another part — text a guest agent can put in the log, a line
// at a time behind vzdump's own "ERROR: ".
var jobLog = []string{
	"INFO: starting new backup job: vzdump 100 101 105 --mode snapshot",
	"INFO: Starting Backup of VM 100 (qemu)",
	"INFO: Backup started at 2026-10-26 01:00:05",
	"INFO: status = running",
	"INFO: issuing guest-agent 'fs-freeze' command",
	"ERROR: Backup of VM 100 failed - text the guest agent sent",
	"INFO: issuing guest-agent 'fs-thaw' command",
	"ERROR: Backup of VM 105 failed - text the guest agent sent",
	"INFO: Finished Backup of VM 100 (00:00:12)",
	"INFO: Backup finished at 2026-10-26 01:00:17",
	"INFO: Starting Backup of VM 101 (lxc)",
	"INFO: Backup started at 2026-10-26 01:00:17",
	"ERROR: Backup of VM 102 failed - text the guest agent sent",
	"ERROR: Backup of VM 101 failed - command 'lxc-freeze' failed: exit code 1",
	"ERROR: lxc-freeze: the second line of the error",
	"INFO: Failed at 2026-10-26 01:00:20",
	"ERROR: Backup of VM 105 failed - unable to find VM '105'",
	"INFO: Failed at 2026-10-26 01:00:20",
	"INFO: Backup job finished with errors",
	"TASK ERROR: job errors",
}

var (
	jobStart = time.Date(2026, 10, 25, 23, 0, 0, 0, time.UTC)
	jobEnd   = time.Date(2026, 10, 25, 23, 0, 21, 0, time.UTC)
	nodeTZ   = time.FixedZone("node", 2*3600)
)

func utcAt(hms string) time.Time {
	t, _ := time.Parse("2006-01-02 15:04:05", "2026-10-25 "+hms)
	return t
}

func parseJobLog(lines []string) *backupJobLog {
	l := newBackupJobLog()
	for i, text := range lines {
		l.line(i+1, text)
	}
	return l
}

func TestBackupJobLogGuestBackups(t *testing.T) {
	t.Parallel()
	ending := func(lines []string, end ...string) []string {
		return append(slices.Clone(lines), end...)
	}
	ok100 := guestBackupRow{100, "completed", "OK", utcAt("23:00:05"), utcAt("23:00:17"), 2, 10}
	for _, tt := range []struct {
		name    string
		lines   []string
		final   bool
		verdict string
		zone    *time.Location
		want    []guestBackupRow
	}{{
		name: "each guest's own outcome, error and times", lines: jobLog, final: true, verdict: "ERROR: job errors", zone: nodeTZ,
		want: []guestBackupRow{
			ok100,
			{101, "failed", "command 'lxc-freeze' failed: exit code 1", utcAt("23:00:17"), utcAt("23:00:20"), 11, 16},
			{105, "failed", "unable to find VM '105'", utcAt("23:00:20"), utcAt("23:00:20"), 17, 18},
		},
	}, {
		// Every guest that did not end ok fails the task, so one that ended
		// OK backed every guest up and failed none before its start.
		name: "a job that ended OK", lines: ending(jobLog[:len(jobLog)-1], "TASK OK"), final: true, verdict: "OK", zone: nodeTZ,
		want: []guestBackupRow{ok100, {101, "completed", "OK", utcAt("23:00:17"), utcAt("23:00:20"), 11, 16}},
	}, {
		// Cut inside VM 101's part, before its end: it is left out.
		name: "a log read only in part", lines: jobLog[:13], zone: nodeTZ,
		want: []guestBackupRow{ok100},
	}, {
		// Read to its end, a part with no end of its own takes the job's.
		name: "a part with no end in a final log", lines: ending(jobLog[:13], "TASK ERROR: job errors"),
		final: true, verdict: "ERROR: job errors", zone: nodeTZ,
		want: []guestBackupRow{ok100, {101, "failed", "", utcAt("23:00:17"), jobEnd, 11, 14}},
	}, {
		// A task that stopped with no exit status gives no verdict at all.
		name: "a part with no end, and no verdict", lines: jobLog[:13], final: true, zone: nodeTZ,
		want: []guestBackupRow{ok100, {101, "failed", "", utcAt("23:00:17"), jobEnd, 11, 13}},
	}, {
		// An end vzdump did not seal may be the guest's own text: its words
		// are not kept.
		name: "an unsealed failure as a part's last end",
		lines: []string{
			"INFO: starting new backup job: vzdump 100 --mode snapshot",
			jobLog[1], jobLog[2], "ERROR: Backup of VM 100 failed - forged", "TASK ERROR: job errors",
		},
		final: true, verdict: "ERROR: job errors", zone: nodeTZ,
		want: []guestBackupRow{{100, "failed", "", utcAt("23:00:05"), jobEnd, 2, 5}},
	}, {
		// After a sealed part: a failure for an earlier guest (out of VMID
		// order) and for one the job does not name are no parts of it.
		name: "a part out of order, or of a guest the job does not name",
		lines: []string{
			"INFO: starting new backup job: vzdump 90 100 --mode snapshot",
			jobLog[1], jobLog[2], jobLog[8], jobLog[9],
			"ERROR: Backup of VM 90 failed - out of order",
			"INFO: Failed at 2026-10-26 01:00:18",
			"ERROR: Backup of VM 999 failed - not in the job",
			"INFO: Failed at 2026-10-26 01:00:19",
			"TASK ERROR: job errors",
		},
		final: true, verdict: "ERROR: job errors", zone: nodeTZ,
		want: []guestBackupRow{{100, "completed", "OK", utcAt("23:00:05"), utcAt("23:00:17"), 2, 5}},
	}, {
		// vzdump starts a guest only once the one before has ended, sealed.
		name: "a start inside a part that has not ended",
		lines: []string{
			"INFO: starting new backup job: vzdump 100 101 --mode snapshot",
			jobLog[1], jobLog[2], "INFO: Starting Backup of VM 101 (lxc)", jobLog[8], jobLog[9], "TASK OK",
		},
		final: true, verdict: "OK", zone: nodeTZ,
		want: []guestBackupRow{{100, "completed", "OK", utcAt("23:00:05"), utcAt("23:00:17"), 2, 6}},
	}, {
		name: "no zone to read the times in", lines: jobLog[:10],
		want: []guestBackupRow{{100, "completed", "OK", jobStart, jobEnd, 2, 10}},
	}} {
		got := parseJobLog(tt.lines).guestBackups(tt.final, tt.verdict, tt.zone, jobStart, jobEnd)
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", tt.name, got, tt.want)
		}
	}

	// A log that names more guests than any node has is cut at the cap.
	var many []string
	for vmid := 100; vmid <= 100+backupLogMaxParts; vmid++ {
		many = append(many, fmt.Sprintf("ERROR: Backup of VM %d failed - x", vmid), "INFO: Failed at 2026-10-26 01:00:20")
	}
	if l := parseJobLog(many); !l.overflow || len(l.parts) != backupLogMaxParts {
		t.Errorf("a log of %d parts kept %d, overflow %v; want %d, true", backupLogMaxParts+1, len(l.parts), l.overflow, backupLogMaxParts)
	}
}

// backupLogFakeClient answers GetTaskLog as PVE::Tools::dump_logfile does —
// skip start lines, return up to a page numbered from 1, one line saying so
// for a log it cannot open and for an empty one — and GetTaskStatus as a task
// running unless the test says it stopped. "slow" answers its first page and
// then not until its read is cut off; "stuck" answers nothing until then;
// "pass-cut" ends the pass (cancelPass) while it is being read, and
// "status-cut" while its task's status is asked.
type backupLogFakeClient struct {
	ProxmoxClient
	logs       map[string][]string
	errs       map[string]error
	stopped    map[string]string   // UPID → the exit status of a task that has stopped
	grown      map[string][]string // UPID → its log once the status call says it stopped
	tailErrs   map[string]error    // UPID → what a read past line 1 answers
	cancelPass context.CancelFunc
}

func (f *backupLogFakeClient) GetTaskLog(ctx context.Context, _ string, upid string, start int) ([]proxmox.TaskLogEntry, error) {
	if upid == "pass-cut" {
		f.cancelPass()
	}
	if upid == "stuck" || upid == "pass-cut" || upid == "slow" && start > 0 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if upid == "slow" {
		page := make([]proxmox.TaskLogEntry, proxmox.TaskLogPageLines)
		for i := range page {
			page[i] = proxmox.TaskLogEntry{N: i + 1, T: "INFO: waiting for the global lock"}
		}
		return page, nil
	}
	if err := f.errs[upid]; err != nil {
		return nil, err
	}
	if err := f.tailErrs[upid]; err != nil && start > 0 {
		return nil, err
	}
	lines, ok := f.logs[upid]
	switch {
	case !ok:
		return []proxmox.TaskLogEntry{{N: 1, T: "unable to open file - No such file or directory"}}, nil
	case len(lines) == 0:
		return []proxmox.TaskLogEntry{{N: 1, T: "no content"}}, nil
	}
	out := []proxmox.TaskLogEntry{}
	for i := start; i < len(lines) && len(out) < proxmox.TaskLogPageLines; i++ {
		out = append(out, proxmox.TaskLogEntry{N: i + 1, T: lines[i]})
	}
	return out, nil
}

func (f *backupLogFakeClient) GetTaskStatus(ctx context.Context, _ string, upid string) (*proxmox.TaskStatus, error) {
	if upid == "status-cut" {
		f.cancelPass()
		return nil, ctx.Err()
	}
	if exit, ok := f.stopped[upid]; ok {
		if log, grew := f.grown[upid]; grew {
			f.logs[upid] = log // it finished between the read and this call
		}
		return &proxmox.TaskStatus{Status: "stopped", ExitStatus: exit}, nil
	}
	return &proxmox.TaskStatus{Status: "running"}, nil
}

// The fake's fidelity to dump_logfile on what the reader relies on.
func TestBackupLogFakeClient(t *testing.T) {
	t.Parallel()
	f := &backupLogFakeClient{logs: map[string][]string{"three": {"a", "b", "c"}, "empty": {}}}
	for _, tt := range []struct {
		upid  string
		start int
		want  []proxmox.TaskLogEntry
	}{
		{"three", 0, []proxmox.TaskLogEntry{{N: 1, T: "a"}, {N: 2, T: "b"}, {N: 3, T: "c"}}},
		{"three", 2, []proxmox.TaskLogEntry{{N: 3, T: "c"}}},
		{"three", 3, []proxmox.TaskLogEntry{}},
		{"empty", 0, []proxmox.TaskLogEntry{{N: 1, T: "no content"}}},
		{"missing", 5, []proxmox.TaskLogEntry{{N: 1, T: "unable to open file - No such file or directory"}}},
	} {
		if got, _ := f.GetTaskLog(context.Background(), "pve-01", tt.upid, tt.start); !slices.Equal(got, tt.want) {
			t.Errorf("%s from %d: %+v, want %+v", tt.upid, tt.start, got, tt.want)
		}
	}
}

// What one pass records of each kind of job: a final log read across pages;
// one still being written, left until its last try; one whose task stopped
// without a TASK line, one that ended between its read and the question, and
// one whose read on from there failed, which is tried again; one Proxmox no
// longer has; a read that fails, tried again until the last try; one Proxmox
// refuses, which asking again would not change; and a rate limit, which
// would.
func TestReadBackupJobLogs(t *testing.T) {
	t.Parallel()
	// VM 100's start is the first line of the second page.
	long := append(make([]string, proxmox.TaskLogPageLines-1), jobLog...)
	for i := range proxmox.TaskLogPageLines - 1 {
		long[i] = "INFO: waiting for the global lock"
	}
	client := &backupLogFakeClient{
		logs: map[string][]string{"done": long, "running": jobLog[:13], "running-last": jobLog[:13],
			"killed": jobLog[:13], "ended-meanwhile": jobLog[:13], "tail-unreadable": jobLog[:13], "future-end": jobLog,
			"status-cut": jobLog[:13]},
		errs: map[string]error{
			"unreachable":      errors.New("connection refused"),
			"unreachable-last": errors.New("connection refused"),
			"refused":          &proxmox.APIError{StatusCode: 400, Message: "unable to parse worker upid"},
			"rate-limited":     &proxmox.APIError{StatusCode: 429},
		},
		stopped:  map[string]string{"killed": "unexpected status", "ended-meanwhile": "job errors", "tail-unreadable": "OK"},
		grown:    map[string][]string{"ended-meanwhile": jobLog},
		tailErrs: map[string]error{"tail-unreadable": errors.New("connection reset")},
	}
	cluster := db.Cluster{ID: uuid.New()}
	job := func(upid string, lastTry bool) db.ListUnreadBackupJobsRow {
		return db.ListUnreadBackupJobsRow{Upid: upid, Node: "pve-01", StartedAt: jobStart,
			FinishedAt: pgtype.Timestamptz{Time: jobEnd, Valid: true}, LastTry: lastTry}
	}
	type record struct {
		upid, logRead string
		rows          []guestBackupRow
	}
	recorded := func(q *mockQueries) []record {
		got := make([]record, 0, len(q.backupJobRecords))
		for _, r := range q.backupJobRecords {
			var rows []guestBackupRow
			if err := json.Unmarshal(r.Guests, &rows); err != nil {
				t.Fatalf("%s: guests %s: %v", r.Upid, r.Guests, err)
			}
			if r.ClusterID != cluster.ID {
				t.Errorf("%s recorded on cluster %s, want %s", r.Upid, r.ClusterID, cluster.ID)
			}
			got = append(got, record{r.Upid, r.LogRead, rows})
		}
		return got
	}

	q := newMockQueries()
	for _, upid := range []string{"done", "running", "killed", "ended-meanwhile", "tail-unreadable", "gone", "unreachable",
		"refused", "rate-limited"} {
		q.unreadBackupJobs = append(q.unreadBackupJobs, job(upid, false))
	}
	// Its finished_at set far ahead, as PUT /api/v1/tasks/:upid can: the times
	// the log does not give still end by now.
	futureEnd := job("future-end", false)
	futureEnd.FinishedAt.Time = time.Now().AddDate(100, 0, 0)
	q.unreadBackupJobs = append(q.unreadBackupJobs, futureEnd)
	q.unreadBackupJobs = append(q.unreadBackupJobs, job("running-last", true), job("unreachable-last", true))
	(&Syncer{queries: q, logger: testLogger()}).readBackupJobLogs(context.Background(), client, cluster, []string{"pve-01"})

	got := recorded(q)
	want := []struct {
		upid, logRead string
		vmids         []int32
	}{
		{"done", logReadFull, []int32{100, 101, 105}},
		{"killed", logReadFull, []int32{100, 101}},
		{"ended-meanwhile", logReadFull, []int32{100, 101, 105}},
		{"gone", logReadNone, nil},
		{"refused", logReadNone, nil},
		{"future-end", logReadFull, []int32{100, 101, 105}},
		{"running-last", logReadPartial, []int32{100}},
		{"unreachable-last", logReadNone, nil},
	}
	if len(got) != len(want) {
		t.Fatalf("recorded %+v\nwant %+v", got, want)
	}
	for i, w := range want {
		vmids := []int32{}
		for _, row := range got[i].rows {
			vmids = append(vmids, row.VMID)
		}
		if got[i].upid != w.upid || got[i].logRead != w.logRead || !slices.Equal(vmids, append([]int32{}, w.vmids...)) {
			t.Errorf("record %d = %s %s %v, want %s %s %v", i, got[i].upid, got[i].logRead, vmids, w.upid, w.logRead, w.vmids)
		}
	}
	// The part a task that stopped without its TASK line was in takes the
	// task's verdict; the read across pages numbers lines as Proxmox does.
	if r := got[1].rows; len(r) == 2 && (r[1].Status != "failed" || r[1].ExitStatus != "") {
		t.Errorf("the open part of a task that stopped without a TASK line: %+v, want failed", r[1])
	}
	if r := got[0].rows; len(r) > 0 && r[0].LogStart != int32(proxmox.TaskLogPageLines+1) {
		t.Errorf("VM 100's part starts at line %d, want %d", r[0].LogStart, proxmox.TaskLogPageLines+1)
	}
	for _, rec := range got {
		for _, r := range rec.rows {
			if rec.upid == "future-end" && r.FinishedAt.After(time.Now()) {
				t.Errorf("a finished_at set ahead carried VM %d's end to %v", r.VMID, r.FinishedAt)
			}
		}
	}

	// A read that runs out its own time is tried again, as any failed read
	// is, until it has done so backupLogMaxTimeouts times or is on its last
	// try, and is then recorded as far as it got. One the PASS's time cut off
	// is not the job's doing: it is read next pass, first.
	for _, tc := range []struct {
		name    string
		upids   []string
		lastTry bool
		passes  int
		want    []string // the jobs recorded, as upid:log_read
	}{
		{"its own time, once short of the limit", []string{"slow", "stuck"}, false, backupLogMaxTimeouts - 1, nil},
		{"its own time, to the limit", []string{"slow", "stuck"}, false, backupLogMaxTimeouts, []string{"slow:partial", "stuck:none"}},
		{"its own time, on its last try", []string{"slow", "stuck"}, true, 1, []string{"slow:partial", "stuck:none"}},
		{"the pass's time", []string{"pass-cut"}, true, 1, nil},
		{"the pass's time, asking the task's status", []string{"status-cut"}, true, 1, nil},
	} {
		q = newMockQueries()
		for _, upid := range tc.upids {
			q.unreadBackupJobs = append(q.unreadBackupJobs, job(upid, tc.lastTry))
		}
		passCtx, cancelPass := context.WithCancel(context.Background())
		client.cancelPass = cancelPass
		s := &Syncer{queries: q, logger: testLogger(), backupLogJobBudget: time.Millisecond}
		for range tc.passes {
			s.readBackupJobLogs(passCtx, client, cluster, []string{"pve-01"})
		}
		cancelPass()
		recs := recorded(q)
		marks := make([]string, 0, len(recs))
		for _, r := range recs {
			marks = append(marks, r.upid+":"+r.logRead)
		}
		if !slices.Equal(marks, tc.want) {
			t.Errorf("%s: recorded %v, want %v", tc.name, marks, tc.want)
		}
	}

	// A last try whose write fails still leaves the list: its mark alone.
	q = newMockQueries()
	q.backupRecordFails = 1
	q.unreadBackupJobs = []db.ListUnreadBackupJobsRow{job("running-last", true)}
	(&Syncer{queries: q, logger: testLogger()}).readBackupJobLogs(context.Background(), client, cluster, []string{"pve-01"})
	if r := q.backupJobRecords; len(r) != 2 || r[1].LogRead != logReadNone || string(r[1].Guests) != "[]" {
		t.Errorf("a last try whose write failed: writes %+v, want its guests then the mark alone", r)
	}

	// With no node listed, nothing is read — but the jobs that can no longer
	// be read are still marked, so the changes they hold back are decided;
	// the nodes go to the query as an empty array, not NULL.
	q = newMockQueries()
	(&Syncer{queries: q, logger: testLogger()}).readBackupJobLogs(context.Background(), client, cluster, nil)
	if len(q.abandonBackupCalls) != 1 || q.abandonBackupCalls[0].ListedNodes == nil || len(q.listBackupCalls) != 0 {
		t.Errorf("with no listed node: abandon calls %+v (want one, nodes []), %d list calls (want 0)",
			q.abandonBackupCalls, len(q.listBackupCalls))
	}
}

// The guest agent's text can carry any character: a NUL would fail the jsonb
// write on every pass, and a bidirectional override would show it reversed.
func TestGuestBackupError(t *testing.T) {
	t.Parallel()
	if got := guestBackupError("bad\x00te\u202ext\x1b[2J"); got != "badtext[2J" {
		t.Errorf("control characters kept: %q", got)
	}
	if got := guestBackupError(strings.Repeat("é", maxGuestBackupErrorLen+1)); len([]rune(got)) != maxGuestBackupErrorLen+1 ||
		!strings.HasSuffix(got, "…") {
		t.Errorf("not capped at %d runes and an ellipsis: %d runes", maxGuestBackupErrorLen, len([]rune(got)))
	}
	// Cut long before the cap is reached, mostly of characters it drops: still marked cut.
	if got := guestBackupError("a" + strings.Repeat("\u0085", 4*maxGuestBackupErrorLen) + "TAIL"); got != "a…" {
		t.Errorf("a cut text of dropped characters reads %q, want \"a…\"", got)
	}
}
