package handlers

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// taskLogTailPages bounds taskLogTail: 100,000 lines, past any real task's log.
const taskLogTailPages = 20

// errTaskLogTooLong is taskLogTail giving up before a log's end.
var errTaskLogTooLong = errors.New("task log longer than the pages read for progress")

// taskLogTailKeep is how many of the lines a poll read the next poll reads
// again, ahead of the new ones: the newest progress line is among them unless
// a task printed none for that long. Half a page, so a poll is one request.
const taskLogTailKeep = proxmox.TaskLogPageLines / 2

// taskLogTail is the end of a task's log: its last page and the one before,
// which between them hold the newest progress line. Proxmox serves a log a page
// at a time and says nothing of its length, so a long one is read to its end,
// as the collector reads a backup job's (backupJobLog.readFrom): a backup over
// many guests runs past one page, and its first page's last percentage would
// stand for the rest of the job.
//
// A status poll comes every few seconds per open view, so where the log ended
// is remembered under key (taskLogEnds) and the next poll reads from just
// before it, one request, instead of the whole log again. A log shorter than
// remembered is read again from its start.
func taskLogTail(ctx context.Context, client *proxmox.Client, key, node, upid string) ([]string, error) {
	if known, ok := taskLogEnds.get(key); ok {
		lines, end, err := readTaskLogTail(ctx, client, node, upid, max(0, known-taskLogTailKeep))
		if err != nil {
			taskLogEnds.forget(key)
			return nil, err
		}
		if end >= known {
			taskLogEnds.put(key, end)
			return lines, nil
		}
	}
	lines, end, err := readTaskLogTail(ctx, client, node, upid, 0)
	if err != nil {
		taskLogEnds.forget(key)
		return nil, err
	}
	taskLogEnds.put(key, end)
	return lines, nil
}

// readTaskLogTail reads a task's log from line start to its end and returns
// the last page and the one before it, and the line count the log ends at.
func readTaskLogTail(ctx context.Context, client *proxmox.Client, node, upid string, start int) (lines []string, end int, err error) {
	var previous, page []proxmox.TaskLogEntry
	for range taskLogTailPages {
		entries, err := client.GetTaskLog(ctx, node, upid, start)
		if err != nil {
			return nil, 0, err
		}
		previous, page = page, entries
		start += len(entries)
		if len(entries) < proxmox.TaskLogPageLines {
			lines = make([]string, 0, len(previous)+len(page))
			for _, entry := range append(previous, page...) {
				lines = append(lines, entry.T)
			}
			return lines, start, nil
		}
	}
	return nil, 0, errTaskLogTooLong
}

// taskLogEnds is where each polled task's log ended at its last poll. Bounded
// in size and age: a poll that stops coming (a closed tab) leaves an entry that
// expires, and a full map drops its oldest entry first.
var taskLogEnds = newTaskLogEndCache(1024, 10*time.Minute, time.Now)

type taskLogEnd struct {
	end  int
	seen time.Time
}

type taskLogEndCache struct {
	mu      sync.Mutex
	entries map[string]taskLogEnd
	size    int
	maxAge  time.Duration
	now     func() time.Time
}

func newTaskLogEndCache(size int, maxAge time.Duration, now func() time.Time) *taskLogEndCache {
	return &taskLogEndCache{entries: map[string]taskLogEnd{}, size: size, maxAge: maxAge, now: now}
}

func (c *taskLogEndCache) get(key string) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || c.now().Sub(e.seen) > c.maxAge {
		delete(c.entries, key)
		return 0, false
	}
	return e.end, true
}

func (c *taskLogEndCache) put(key string, end int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if _, ok := c.entries[key]; !ok && len(c.entries) >= c.size {
		oldest := ""
		for k, e := range c.entries {
			if now.Sub(e.seen) > c.maxAge {
				delete(c.entries, k)
			} else if oldest == "" || e.seen.Before(c.entries[oldest].seen) {
				oldest = k
			}
		}
		if len(c.entries) >= c.size {
			delete(c.entries, oldest)
		}
	}
	c.entries[key] = taskLogEnd{end: end, seen: now}
}

func (c *taskLogEndCache) forget(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
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
