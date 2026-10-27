package collector

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// A backup JOB is a vzdump that backs up more than one of a node's guests. It
// runs as one Proxmox task whose UPID names no guest — Proxmox sets the worker
// id only when exactly one of the node's guests is in the job
// (PVE/API2/VZDump.pm) — so the task alone puts it in no guest's History. Its
// log does: vzdump backs the guests up one after another, in VMID order, and
// logs each one's part between a line that starts it and one that ends it
// (PVE/VZDump.pm, exec_backup_task). The collector reads a job's log once the
// task has finished and records each guest's outcome and times
// (guest_backups, migrations/000116).

const (
	// backupLogJobsPerPass bounds how many jobs' logs one slow pass reads for
	// a cluster. A job that just ended is read in the pass after; a backlog —
	// the jobs that ran while Nexara was down — drains a few a pass rather
	// than stretching one.
	backupLogJobsPerPass = 4
	// backupLogPassBudget bounds the time one pass spends reading them, well
	// inside the cluster's sync deadline (clusterSyncTimeout, two minutes at
	// the least). A job it cuts off is read again, from the start, next pass.
	backupLogPassBudget = 30 * time.Second
	// backupLogJobBudget bounds one job's read, so one slow node or one huge
	// log cannot spend more than half a pass: a read it cuts off is tried
	// again next pass, like any failed read, until it has been cut off
	// backupLogMaxTimeouts times — every page asks the node to scan the whole
	// log file — or it is on its last try. Then it is recorded as far as it
	// got.
	backupLogJobBudget   = 15 * time.Second
	backupLogMaxTimeouts = 3
	// backupLogMaxPages bounds how many pages of a log are read:
	// 200,000 lines, far past a job of hundreds of guests. A longer log is
	// recorded as far as it was read, and as not read in full.
	backupLogMaxPages = 40
	// backupLogMaxParts bounds the guests one log may name, far past a node's
	// worth: only a node that is not what it seems writes more, and it must
	// not fill memory or the table. A log over it is recorded as far as that.
	backupLogMaxParts = 2000
	// backupLogGiveUp is how long a job is tried for (ListUnreadBackupJobs):
	// a day, as long as a status change waits for its node's tasks.
	backupLogGiveUp = 24 * time.Hour
	// maxGuestBackupErrorLen caps the error kept for one guest: Proxmox caps
	// no log line, and this one can carry the guest agent's text.
	maxGuestBackupErrorLen = 1000
)

// How much of a job's log its backup_job_logs mark says was read
// (migrations/000116), which is what ClassifyGuestStatusChanges decides by.
const (
	logReadFull    = "full"    // all Proxmox will write: guest_backups has every guest the job backed up
	logReadPartial = "partial" // some of it: the guests found, but perhaps not all
	logReadNone    = "none"    // none of it: gone, refused, or never readable
)

// The lines vzdump logs for each guest, transcribed from exec_backup_task in
// PVE/VZDump.pm with the prefix debugmsg in PVE/VZDump/Plugin.pm gives each
// line it writes to the task log. The times are the node's local clock,
// strftime("%F %H:%M:%S", localtime()).
//
// A guest's part begins with "Starting Backup of VM N (qemu|lxc)" and the
// time it started, and ends with "Finished Backup of VM N (HH:MM:SS)" or
// "Backup of VM N failed - <error>", each followed by its time. A guest that
// fails before its start is logged — one the job names that Proxmox cannot
// find, a stop-mode backup of a guest HA keeps running, a storage over its
// backup limit — has no start, and its failure line is all of its part.
var (
	backupStartRe      = regexp.MustCompile(`^INFO: Starting Backup of VM (\d{1,9}) \([a-z]+\)$`)
	backupStartedAtRe  = regexp.MustCompile(`^INFO: Backup started at (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})$`)
	backupFinishRe     = regexp.MustCompile(`^INFO: Finished Backup of VM (\d{1,9}) \(\d+:\d{2}:\d{2}\)$`)
	backupFinishedAtRe = regexp.MustCompile(`^INFO: Backup finished at (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})$`)
	backupFailRe       = regexp.MustCompile(`^ERROR: Backup of VM (\d{1,9}) failed - (.*)$`)
	backupFailedAtRe   = regexp.MustCompile(`^INFO: Failed at (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})$`)
	// backupJobRe is exec_backup's first line. Its command line names the
	// guests when the job lists them (command_line in PVE/VZDump/Common.pm);
	// a job of all guests, or of a pool, names none.
	backupJobRe = regexp.MustCompile(`^INFO: starting new backup job: vzdump((?: \d{1,9})+)(?: |$)`)
	// taskEndRe is the line Proxmox closes a task's log with once its worker
	// has exited (fork_worker in PVE/RESTEnvironment.pm).
	taskEndRe = regexp.MustCompile(`^TASK (?:OK|WARNINGS: \d+|ERROR: .*)$`)
)

// vzdumpTimeLayout is the strftime("%F %H:%M:%S") of a guest's start and end.
const vzdumpTimeLayout = "2006-01-02 15:04:05"

// backupLogExpect is the line that may follow the one just read.
type backupLogExpect int

const (
	expectNothing backupLogExpect = iota
	expectStartedAt
	expectFinishedAt
	expectFailedAt
)

// guestBackupPart is one guest's part of a job's log.
type guestBackupPart struct {
	vmid int32
	// started is that vzdump logged the part's start.
	started bool
	// ended and ok are the part's last end; sealed, that the end was
	// vzdump's own. A "Finished" line is an INFO line, which nothing a guest
	// says becomes. A failure line is an ERROR line, and so is every line of
	// a guest agent's error — vzdump logs those a line at a time — so one is
	// vzdump's only when vzdump's own "Failed at" follows it.
	ended, ok, sealed  bool
	errText            string
	startedAt, endedAt string // the node's local time, as logged
	logStart, logEnd   int    // Proxmox's line numbers
}

// backupJobLog reads a job's log a line at a time. Which guest a line belongs
// to is decided by position, as vzdump writes them: a guest's part runs from
// its start until its end is sealed. Within it, a line naming another guest
// is the guest's own text and counts for nothing, and of its ends the last
// counts — vzdump writes its own after everything the guest's backup logged.
// A part begins only once the one before it is sealed — one with no start,
// also only in VMID order and never over a part already read, which is where
// vzdump writes them — and when the job's first line names its guests, a part
// for any other is not this job's.
type backupJobLog struct {
	parts  []*guestBackupPart
	byVMID map[int32]int
	cur    *guestBackupPart // the part being read, until the next one begins
	expect backupLogExpect
	named  map[int32]bool // the guests the job's first line names; nil if none
	lines  int            // lines read
	// overflow is that the log named more than backupLogMaxParts guests;
	// startInPart, that a guest's start came inside another's part.
	overflow, startInPart bool
	// taskEnd is the closing TASK line when it is the last line read.
	taskEnd string
}

func newBackupJobLog() *backupJobLog {
	return &backupJobLog{byVMID: map[int32]int{}}
}

func (l *backupJobLog) line(n int, text string) {
	l.lines++
	l.taskEnd = ""
	if taskEndRe.MatchString(text) {
		l.taskEnd = text
	}
	if l.followingLine(n, text) {
		return
	}
	if l.named == nil && len(l.parts) == 0 {
		if m := backupJobRe.FindStringSubmatch(text); m != nil {
			l.named = map[int32]bool{}
			for _, id := range strings.Fields(m[1]) {
				l.named[parseBackupVMID(id)] = true
			}
			return
		}
	}
	if m := backupStartRe.FindStringSubmatch(text); m != nil {
		// vzdump starts a guest only once the one before it has ended, sealed.
		switch {
		case l.cur != nil && !l.cur.sealed:
			l.cur.logEnd, l.startInPart = n, true
		case l.begin(parseBackupVMID(m[1]), n, true):
			l.expect = expectStartedAt
		}
		return
	}
	if m := backupFinishRe.FindStringSubmatch(text); m != nil {
		if l.cur != nil && !l.cur.sealed && l.cur.vmid == parseBackupVMID(m[1]) {
			l.end(n, true, "")
			l.expect = expectFinishedAt
		}
		return
	}
	if m := backupFailRe.FindStringSubmatch(text); m != nil {
		vmid := parseBackupVMID(m[1])
		switch {
		case l.cur != nil && !l.cur.sealed && l.cur.vmid == vmid:
			// An end of the part being read.
		case l.cur == nil || (l.cur.sealed && vmid > l.cur.vmid):
			if !l.begin(vmid, n, false) { // failed before it started
				return
			}
		default:
			if !l.cur.sealed {
				l.cur.logEnd = n // another guest named inside this one's part
			}
			return
		}
		l.end(n, false, m[2])
		l.expect = expectFailedAt
		return
	}
	if l.cur != nil && !l.cur.sealed {
		l.cur.logEnd = n
	}
}

// followingLine takes the time vzdump logs right after a guest's start or
// end, and between a failure and its time the rest of a multi-line error:
// debugmsg logs each line of one with an "ERROR: " of its own. It reports
// whether it took the line. A failure of the same guest is not taken: it is
// a later end, which line() records.
func (l *backupJobLog) followingLine(n int, text string) bool {
	expect := l.expect
	l.expect = expectNothing
	var re *regexp.Regexp
	switch expect {
	case expectNothing:
		return false
	case expectStartedAt:
		re = backupStartedAtRe
	case expectFinishedAt:
		re = backupFinishedAtRe
	case expectFailedAt:
		re = backupFailedAtRe
	}
	if m := re.FindStringSubmatch(text); m != nil {
		if expect == expectStartedAt {
			l.cur.startedAt = m[1]
		} else {
			// vzdump's own time after an end: the end was vzdump's.
			l.cur.endedAt, l.cur.sealed = m[1], true
		}
		l.cur.logEnd = n
		return true
	}
	if expect == expectFailedAt && strings.HasPrefix(text, "ERROR: ") && !l.failsCurrent(text) {
		l.cur.logEnd = n
		l.expect = expectFailedAt
		return true
	}
	return false
}

// failsCurrent reports whether text is a failure of the guest whose part is
// being read.
func (l *backupJobLog) failsCurrent(text string) bool {
	m := backupFailRe.FindStringSubmatch(text)
	return len(m) == 3 && parseBackupVMID(m[1]) == l.cur.vmid
}

// begin starts a part, and reports whether it did.
func (l *backupJobLog) begin(vmid int32, n int, started bool) bool {
	if l.named != nil && !l.named[vmid] {
		return false // not a guest this job names
	}
	p := &guestBackupPart{vmid: vmid, started: started, logStart: n, logEnd: n}
	if i, seen := l.byVMID[vmid]; seen {
		if !started {
			return false // only a start takes a part over
		}
		l.parts[i] = p
	} else {
		if len(l.parts) >= backupLogMaxParts {
			l.overflow = true
			return false
		}
		l.byVMID[vmid] = len(l.parts)
		l.parts = append(l.parts, p)
	}
	l.cur = p
	return true
}

func (l *backupJobLog) end(n int, ok bool, errText string) {
	// A "Finished" line is sealed as it stands; a failure, by the time after
	// it. Either way the time logged after THIS end is the one that counts.
	l.cur.ended, l.cur.ok, l.cur.sealed = true, ok, ok
	l.cur.errText, l.cur.endedAt, l.cur.logEnd = guestBackupError(errText), "", n
}

func parseBackupVMID(s string) int32 {
	v, _ := strconv.ParseInt(s, 10, 32) // \d{1,9}: always fits
	return int32(v)
}

// guestBackupRow is one guest_backups row, as RecordBackupJobLog takes it.
type guestBackupRow struct {
	VMID       int32     `json:"vmid"`
	Status     string    `json:"status"`
	ExitStatus string    `json:"exit_status"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	LogStart   int32     `json:"log_start"`
	LogEnd     int32     `json:"log_end"`
}

// guestBackups turns the parts into rows. final is that the log is all
// Proxmox will write, and verdict how the job ended: its TASK line, or the
// exit status of a task that stopped without one.
//
// Final, a started part with no sealed end takes the job's verdict, and so
// does every part when the job succeeded: vzdump counts each guest that did
// not end ok (exec_backup's errcount) and fails the task for any, so a job
// that ended OK backed up every guest it started, whatever a line in a
// failure's shape said — and failed none before its start. Not final, a part
// with no sealed end is left out: its end may be in the lines not read.
//
// A time vzdump logged is read in the node's zone. One it did not log, or
// with no zone to read it in, is the job's start or end; a guest failed
// before it started is given its failure's time for both. Every time is held
// inside the job's start and end.
func (l *backupJobLog) guestBackups(final bool, verdict string, zone *time.Location, jobStart, jobEnd time.Time) []guestBackupRow {
	// TaskSucceeded passes an empty status, which is no verdict at all here.
	jobOK := final && verdict != "" && proxmox.TaskSucceeded(verdict)
	rows := make([]guestBackupRow, 0, len(l.parts))
	for _, p := range l.parts {
		switch {
		case !p.started && (!p.sealed || jobOK):
			continue
		case !p.sealed && !final:
			continue
		}
		row := guestBackupRow{VMID: p.vmid, Status: "completed", ExitStatus: "OK",
			LogStart: safeconv.Int32(p.logStart), LogEnd: safeconv.Int32(p.logEnd)}
		if !jobOK && (!p.sealed || !p.ok) {
			row.Status, row.ExitStatus = "failed", ""
			if p.sealed {
				row.ExitStatus = p.errText
			}
		}
		start, startKnown := vzdumpTime(p.startedAt, zone)
		end, endKnown := vzdumpTime(p.endedAt, zone)
		switch {
		case !startKnown && endKnown:
			start = end
		case !startKnown:
			start = jobStart
		}
		if !endKnown {
			end = jobEnd
		}
		row.StartedAt = clampTime(start, jobStart, jobEnd).UTC()
		row.FinishedAt = clampTime(end, jobStart, jobEnd).UTC()
		if row.FinishedAt.Before(row.StartedAt) {
			row.FinishedAt = row.StartedAt
		}
		rows = append(rows, row)
	}
	return rows
}

func vzdumpTime(local string, zone *time.Location) (time.Time, bool) {
	if local == "" || zone == nil {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation(vzdumpTimeLayout, local, zone)
	return t, err == nil
}

func clampTime(t, lo, hi time.Time) time.Time {
	if t.Before(lo) {
		return lo
	}
	if t.After(hi) {
		return hi
	}
	return t
}

// guestBackupError is the error kept for a guest: capped, and without control
// or bidirectional-override characters. The guest agent's text can carry any:
// a NUL would fail the job's jsonb write on every pass, and the others would
// let the text be shown as something it does not say.
func guestBackupError(s string) string {
	// Cut first, at a length no capped text exceeds: a node can send a line
	// of any length, and a split rune at the cut is dropped below.
	limit := 4*maxGuestBackupErrorLen + 4
	cut := len(s) > limit
	if cut {
		s = s[:limit]
	}
	r := []rune(strings.Map(func(c rune) rune {
		if unicode.IsControl(c) || unicode.Is(unicode.Bidi_Control, c) {
			return -1
		}
		return c
	}, strings.ToValidUTF8(s, "")))
	switch {
	case len(r) > maxGuestBackupErrorLen:
		return string(r[:maxGuestBackupErrorLen]) + "…"
	case cut:
		return string(r) + "…"
	}
	return string(r)
}

// nodeZone is a node's time zone, which the times in its task logs are in
// (nodes.timezone, from GET /nodes/{node}/time), or nil when it is not known.
// "Local" would be Nexara's own zone, not the node's.
func nodeZone(name string) *time.Location {
	if name == "" || name == "Local" {
		return nil
	}
	zone, err := time.LoadLocation(name)
	if err != nil {
		return nil
	}
	return zone
}

// backupLogRefused reports whether Proxmox refused the read itself rather
// than failing it this once: a UPID it cannot parse — a task row filed by
// hand through POST /api/v1/tasks can name any — a node or task it does not
// have, a token it will not answer. Asking again next pass gets the same
// answer, and a job that keeps one of a pass's few reads for a day holds the
// other jobs back. A timeout or a rate limit, which a proxy in front of
// Proxmox can answer, is no refusal.
func backupLogRefused(err error) bool {
	var apiErr *proxmox.APIError
	return errors.Is(err, proxmox.ErrInvalidInput) || errors.Is(err, proxmox.ErrNotFound) ||
		errors.Is(err, proxmox.ErrForbidden) ||
		(errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 &&
			apiErr.StatusCode != http.StatusRequestTimeout && apiErr.StatusCode != http.StatusTooManyRequests)
}

// backupLogRead is how far a job's log was read.
type backupLogRead int

const (
	backupLogFinal      backupLogRead = iota // all Proxmox will write: to its closing TASK line
	backupLogUnfinished                      // to a last line that closes nothing, or until a read failed
	backupLogTooLong                         // backupLogMaxPages of it, or backupLogMaxParts guests
	backupLogGone                            // Proxmox has no log for the task
)

// readBackupJobLog reads a job's log a page at a time.
func readBackupJobLog(ctx context.Context, client ProxmoxClient, node, upid string) (*backupJobLog, backupLogRead, error) {
	l := newBackupJobLog()
	read, err := l.readFrom(ctx, client, node, upid, 0)
	return l, read, err
}

// errBackupLogGone is a log that went while it was being read.
var errBackupLogGone = errors.New("the task log is gone")

// readFrom reads the log on from line start+1, a page at a time.
func (l *backupJobLog) readFrom(ctx context.Context, client ProxmoxClient, node, upid string, start int) (backupLogRead, error) {
	for range backupLogMaxPages {
		entries, err := client.GetTaskLog(ctx, node, upid, start)
		if err != nil {
			return backupLogUnfinished, err
		}
		// A log Proxmox cannot open — rotated away — is answered with one
		// line saying so, whatever start asked, not an error
		// (PVE::Tools::dump_logfile).
		if len(entries) == 1 && entries[0].N == 1 && strings.HasPrefix(entries[0].T, "unable to open file - ") {
			if start == 0 {
				return backupLogGone, nil
			}
			return backupLogUnfinished, errBackupLogGone
		}
		for _, e := range entries {
			l.line(e.N, e.T)
		}
		if l.overflow {
			return backupLogTooLong, nil
		}
		if len(entries) < proxmox.TaskLogPageLines {
			if l.taskEnd != "" {
				return backupLogFinal, nil
			}
			return backupLogUnfinished, nil
		}
		start += len(entries)
	}
	return backupLogTooLong, nil
}

// finishStoppedLog is for a log read to its end with no TASK line yet. A
// worker that exits without one — a lock it could not get, a kill, a node
// gone down mid-job — never writes it, so once Proxmox calls the task stopped
// its log is final all the same. It can have grown since it was read, and
// grows no more, so the read goes on from where it stopped first. While the
// task runs, the log is unfinished; err is a failed status call or read.
func finishStoppedLog(ctx context.Context, client ProxmoxClient, job db.ListUnreadBackupJobsRow, l *backupJobLog) (backupLogRead, string, error) {
	st, err := client.GetTaskStatus(ctx, job.Node, job.Upid)
	if err != nil || st.Status != "stopped" {
		return backupLogUnfinished, "", err
	}
	read, err := l.readFrom(ctx, client, job.Node, job.Upid, l.lines)
	switch {
	case err != nil:
		return backupLogUnfinished, "", err
	case read == backupLogUnfinished:
		return backupLogFinal, st.ExitStatus, nil
	}
	return read, strings.TrimPrefix(l.taskEnd, "TASK "), nil
}

// backupLogTimedOut counts a read of the job that ran out its own time, and
// reports whether that makes backupLogMaxTimeouts.
func (s *Syncer) backupLogTimedOut(upid string) bool {
	n := 1
	if v, ok := s.backupLogTimeouts.Load(upid); ok {
		was, _ := v.(int)
		n += was
	}
	s.backupLogTimeouts.Store(upid, n)
	return n >= backupLogMaxTimeouts
}

// readBackupJobLogs reads the logs of the cluster's finished backup jobs on
// the nodes this pass listed the tasks of (syncTasks), and records which
// guests each backed up. A log with no TASK line yet is final only once
// Proxmox calls the task stopped (finishStoppedLog): task_history can call a
// task done that Proxmox is still writing — PUT /api/v1/tasks/:upid takes any
// status. A read that fails is tried again next pass, and so is one that runs
// out its own time, until backupLogMaxTimeouts; one Proxmox refuses is
// recorded as it stands; and anything is recorded on its last try, a day on —
// except a read the pass's own time cut off, which is no fault of the job's:
// the job is read next pass, first if it is on its last try. The jobs that can no
// longer be read are marked so first, so the status changes they hold back
// (ClassifyGuestStatusChanges) stop waiting.
func (s *Syncer) readBackupJobLogs(ctx context.Context, client ProxmoxClient, cluster db.Cluster, listedNodes []string) {
	// An empty array, not NULL, so ANY() in the queries is false, not null.
	if listedNodes == nil {
		listedNodes = []string{}
	}
	giveUpBefore := time.Now().Add(-backupLogGiveUp)
	if _, err := s.queries.AbandonUnreadBackupJobs(ctx, db.AbandonUnreadBackupJobsParams{
		ClusterID:    cluster.ID,
		GiveUpBefore: giveUpBefore,
		ListedNodes:  listedNodes,
	}); err != nil {
		s.logger.Warn("backup job logs: failed to give up on unreadable jobs",
			"cluster_id", cluster.ID, "error", err)
	}
	if len(listedNodes) == 0 {
		return
	}
	jobs, err := s.queries.ListUnreadBackupJobs(ctx, db.ListUnreadBackupJobsParams{
		ClusterID:    cluster.ID,
		GiveUpBefore: giveUpBefore,
		ListedNodes:  listedNodes,
		MaxJobs:      backupLogJobsPerPass,
	})
	if err != nil {
		s.logger.Warn("backup job logs: failed to list unread jobs",
			"cluster_id", cluster.ID, "error", err)
		return
	}

	jobBudget := s.backupLogJobBudget
	if jobBudget == 0 {
		jobBudget = backupLogJobBudget
	}
	passCtx, cancel := context.WithTimeout(ctx, backupLogPassBudget)
	defer cancel()
	for _, job := range jobs {
		if passCtx.Err() != nil {
			return // out of time this pass: the rest are read next pass
		}
		jobCtx, jobCancel := context.WithTimeout(passCtx, jobBudget)
		l, read, err := readBackupJobLog(jobCtx, client, job.Node, job.Upid)
		verdict := strings.TrimPrefix(l.taskEnd, "TASK ")
		if err == nil && read == backupLogUnfinished {
			read, verdict, err = finishStoppedLog(jobCtx, client, job, l)
		}
		ownTime := jobCtx.Err() != nil && passCtx.Err() == nil
		jobCancel()
		if err != nil && passCtx.Err() != nil {
			return // cut off by the pass's own time
		}
		if err != nil {
			s.logger.Warn("backup job logs: failed to read a job's log",
				"cluster_id", cluster.ID, "upid", job.Upid, "error", err)
		}
		if l.startInPart {
			// vzdump never writes one, so either a guest put it there or a
			// Proxmox upgrade reworded how a part ends — which would fold every
			// later guest into the part still open.
			s.logger.Warn("backup job logs: a guest's start inside another guest's part",
				"cluster_id", cluster.ID, "upid", job.Upid)
		}
		settled := err == nil && read != backupLogUnfinished || backupLogRefused(err) ||
			ownTime && s.backupLogTimedOut(job.Upid)
		if !settled && !job.LastTry {
			continue
		}
		s.backupLogTimeouts.Delete(job.Upid)
		s.recordBackupJobLog(ctx, cluster, job, l, err == nil && read == backupLogFinal, verdict)
	}
}

// recordBackupJobLog writes a job's guests and its mark. If the write fails
// on the job's last try, the mark alone is written, so the job leaves the
// list rather than taking a pass's read every pass.
func (s *Syncer) recordBackupJobLog(ctx context.Context, cluster db.Cluster, job db.ListUnreadBackupJobsRow, l *backupJobLog, final bool, verdict string) {
	// finished_at may narrow the window the guests' times are held in, never
	// widen or invert it: PUT /api/v1/tasks/:upid writes it, and these rows
	// are kept for good.
	jobStart, jobEnd := job.StartedAt, time.Now()
	if f := job.FinishedAt; f.Valid && !f.Time.Before(jobStart) && f.Time.Before(jobEnd) {
		jobEnd = f.Time
	}
	if jobStart.After(jobEnd) {
		jobStart = jobEnd
	}
	logRead := logReadNone
	switch {
	case final:
		logRead = logReadFull
	case l.lines > 0:
		logRead = logReadPartial
	}
	guests, err := json.Marshal(l.guestBackups(final, verdict, nodeZone(job.Timezone), jobStart, jobEnd))
	if err != nil {
		guests, logRead = []byte("[]"), logReadNone
	}
	params := db.RecordBackupJobLogParams{Upid: job.Upid, ClusterID: cluster.ID, Guests: guests, LogRead: logRead}
	if err := s.queries.RecordBackupJobLog(ctx, params); err != nil {
		s.logger.Warn("backup job logs: failed to record a job's guests",
			"cluster_id", cluster.ID, "upid", job.Upid, "error", err)
		if !job.LastTry || logRead == logReadNone {
			return
		}
		params.Guests, params.LogRead = []byte("[]"), logReadNone
		if err := s.queries.RecordBackupJobLog(ctx, params); err != nil {
			s.logger.Warn("backup job logs: failed to give up on a job",
				"cluster_id", cluster.ID, "upid", job.Upid, "error", err)
		}
	}
}
