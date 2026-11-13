package handlers

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// taskLogTailPages bounds taskLogTail: 100,000 lines, past any real task's log.
const taskLogTailPages = 20

// errTaskLogTooLong is taskLogTail giving up before a log's end.
var errTaskLogTooLong = errors.New("task log longer than the pages read for progress")

// taskLogTail is the end of a task's log: its last page and the one before,
// which between them hold the newest progress line. Proxmox serves a log a page
// at a time and says nothing of its length, so a long one is read to its end,
// as the collector reads a backup job's (backupJobLog.readFrom): a backup over
// many guests runs past one page, and its first page's last percentage would
// stand for the rest of the job.
func taskLogTail(ctx context.Context, client *proxmox.Client, node, upid string) ([]string, error) {
	var previous, page []proxmox.TaskLogEntry
	start := 0
	for range taskLogTailPages {
		entries, err := client.GetTaskLog(ctx, node, upid, start)
		if err != nil {
			return nil, err
		}
		previous, page = page, entries
		if len(entries) < proxmox.TaskLogPageLines {
			lines := make([]string, 0, len(previous)+len(page))
			for _, entry := range append(previous, page...) {
				lines = append(lines, entry.T)
			}
			return lines, nil
		}
		start += len(entries)
	}
	return nil, errTaskLogTooLong
}

// taskLogProgress is how far a running task is, read from its log lines, or
// nil when they say nothing about it. Proxmox emits progress in several
// formats:
//
//  1. "progress 0.50"                                                             (generic tasks)
//  2. "drive-ide0: transferred 1.0 GiB of 32.0 GiB (1.00%) in 5s"                (move disk)
//  3. "transferred 1.0 GiB of 100.0 GiB (1.00%)"                                 (clone)
//  4. "migration active, transferred 5.2 GiB of 16.0 GiB VM-state, 833.4 MiB/s"  (live migration)
//
// and a backup its own, which vzdumpProgress reads.
func taskLogProgress(taskType string, lines []string) *float64 {
	if taskType == "vzdump" {
		return vzdumpProgress(lines)
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if strings.HasPrefix(line, "progress ") {
			if pct, err := strconv.ParseFloat(strings.TrimPrefix(line, "progress "), 64); err == nil {
				return &pct
			}
			return nil
		}
		// "(Z%)" anywhere in the line: the disk move and clone formats.
		if pctEnd := strings.Index(line, "%)"); pctEnd != -1 {
			if pctStart := strings.LastIndex(line[:pctEnd], "("); pctStart != -1 {
				if pct, err := strconv.ParseFloat(line[pctStart+1:pctEnd], 64); err == nil {
					p := pct / 100.0
					return &p
				}
				return nil
			}
		}
		// "transferred X <unit> of Y <unit>" with no percentage: live migrations.
		if p := parseTransferredProgress(line); p != nil {
			return p
		}
	}
	return nil
}

// vzdumpPercent is a backup's progress line, as vzdump prints it while it
// reads a guest's disks: "INFO:  73% (59.9 GiB of 81.0 GiB) in 1m 24s, …".
var vzdumpPercent = regexp.MustCompile(`^INFO:\s+(\d+(?:\.\d+)?)% \(`)

// vzdumpProgress is how far a backup task's current guest is. vzdump starts
// at 0% again for each guest, and the guests it will reach are not in the log:
// its job line names every guest of the job, those on other nodes included,
// since Run now starts one task per node. The summaries after a guest's 100%
// ("backup is sparse: 60.69 GiB (74%)", "reused 79.11 GiB (97%)") are not
// progress, which is why the generic "(Z%)" reading must not see this log. A
// container prints no percentage: the PBS client reports only when it is done.
func vzdumpProgress(lines []string) *float64 {
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if m := vzdumpPercent.FindStringSubmatch(line); m != nil {
			pct, err := strconv.ParseFloat(m[1], 64)
			if err != nil {
				return nil
			}
			p := min(pct/100, 1)
			return &p
		}
		// A guest begun since the last percentage: nothing of it read yet.
		if strings.Contains(line, "Starting Backup of VM ") {
			if strings.HasSuffix(strings.TrimSpace(line), "(lxc)") {
				return nil
			}
			zero := 0.0
			return &zero
		}
	}
	return nil
}
