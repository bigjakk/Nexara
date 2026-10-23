package proxmox

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// BlockJob is one of a running VM's QEMU block jobs: a mirror (a disk move, a
// live migration with local disks, a clone of a running VM), a stream (a live
// restore from PBS, or removing a snapshot on Proxmox VE 9's volume-chain
// storage), a commit, or a backup. Device is QEMU's job id, which PVE names
// after the drive (e.g. mirror-scsi0).
type BlockJob struct {
	Type       string `json:"type"`
	Device     string `json:"device"`
	Completed  int64  `json:"completed"`
	Total      int64  `json:"total"`
	SpeedLimit int64  `json:"speed_limit"`
}

// BlockJobs is a VM's block jobs as QEMU's `info block-jobs` lists them.
// Unparsed keeps any line in a shape this does not know, verbatim, so a
// change in QEMU's wording shows as such instead of reading as "no jobs".
type BlockJobs struct {
	Jobs     []BlockJob `json:"jobs"`
	Unparsed []string   `json:"unparsed"`
}

// The two line shapes of QEMU's hmp_info_block_jobs
// (block/monitor/block-hmp-cmds.c); with no job it prints "No active jobs".
// Lines in any other shape are kept, up to a bound a misbehaving answer
// cannot blow past.
const (
	maxUnparsedLines     = 32
	maxUnparsedLineBytes = 512
)

var (
	blockJobLine = regexp.MustCompile(
		`^Type (\S+), device (.+?): Completed (\d+) of (\d+) bytes, speed limit (\d+) bytes/s$`)
	streamJobLine = regexp.MustCompile(
		`^Streaming device (.+?): Completed (\d+) of (\d+) bytes, speed limit (\d+) bytes/s$`)
)

func parseBlockJobs(out string) BlockJobs {
	res := BlockJobs{Jobs: []BlockJob{}, Unparsed: []string{}}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "No active jobs" {
			continue
		}
		var job BlockJob
		var nums []string
		if m := blockJobLine.FindStringSubmatch(line); m != nil {
			job.Type, job.Device, nums = m[1], m[2], m[3:]
		} else if m := streamJobLine.FindStringSubmatch(line); m != nil {
			job.Type, job.Device, nums = "stream", m[1], m[2:]
		} else {
			res.Unparsed = keepUnparsed(res.Unparsed, line)
			continue
		}
		var err error
		for i, dst := range []*int64{&job.Completed, &job.Total, &job.SpeedLimit} {
			if *dst, err = strconv.ParseInt(nums[i], 10, 64); err != nil {
				break
			}
		}
		if err != nil {
			res.Unparsed = keepUnparsed(res.Unparsed, line)
			continue
		}
		res.Jobs = append(res.Jobs, job)
	}
	return res
}

func keepUnparsed(kept []string, line string) []string {
	if len(kept) >= maxUnparsedLines {
		return kept
	}
	if len(line) > maxUnparsedLineBytes {
		line = strings.ToValidUTF8(line[:maxUnparsedLineBytes], "")
	}
	return append(kept, line)
}

// GetBlockJobs reads a running VM's QEMU block jobs through PVE's monitor
// endpoint, with the fixed HMP command `info block-jobs`. Proxmox VE 9 runs
// `info` commands for a holder of Sys.Audit or Sys.Modify on the VM, Proxmox
// VE 8 for VM.Monitor; other commands need more, up to root, and some amount
// to a shell on the node. There is deliberately no method taking a command:
// the endpoint runs whatever it is given (TestOnlyOneMonitorCommand).
//
// The endpoint answers a failure — a stopped VM, a QEMU that did not answer —
// with 200 and text starting "ERROR: " (qemu-server: `$res = "ERROR: $@" if
// $@`); that comes back as the *APIError any other refusal would be.
func (c *Client) GetBlockJobs(ctx context.Context, node string, vmid int) (BlockJobs, error) {
	if err := validateNodeName(node); err != nil {
		return BlockJobs{}, err
	}
	if err := validateVMID(vmid); err != nil {
		return BlockJobs{}, err
	}
	form := url.Values{}
	form.Set("command", "info block-jobs")
	var out string
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/monitor"
	if err := c.doPost(ctx, path, form, &out); err != nil {
		return BlockJobs{}, fmt.Errorf("read VM %d block jobs on %s: %w", vmid, node, err)
	}
	if msg, failed := strings.CutPrefix(strings.TrimSpace(out), "ERROR: "); failed {
		return BlockJobs{}, fmt.Errorf("read VM %d block jobs on %s: %w", vmid, node,
			&APIError{StatusCode: http.StatusInternalServerError, Message: msg})
	}
	return parseBlockJobs(out), nil
}
