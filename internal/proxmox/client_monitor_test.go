package proxmox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestParseBlockJobs pins the readings of QEMU's `info block-jobs` lines
// (block/monitor/block-hmp-cmds.c hmp_info_block_jobs), and that a line of
// any other shape is kept rather than dropped.
func TestParseBlockJobs(t *testing.T) {
	t.Parallel()
	mirror := BlockJob{Type: "mirror", Device: "mirror-scsi0", Completed: 1073741824, Total: 34359738368}
	for _, tt := range []struct {
		name string
		out  string
		want BlockJobs
	}{
		{"none", "No active jobs\n", BlockJobs{Jobs: []BlockJob{}, Unparsed: []string{}}},
		{"empty", "", BlockJobs{Jobs: []BlockJob{}, Unparsed: []string{}}},
		{"mirror", "Type mirror, device mirror-scsi0: Completed 1073741824 of 34359738368 bytes, speed limit 0 bytes/s\n",
			BlockJobs{Jobs: []BlockJob{mirror}, Unparsed: []string{}}},
		{"several, CRLF", "Type mirror, device mirror-scsi0: Completed 1073741824 of 34359738368 bytes, speed limit 0 bytes/s\r\n" +
			"Streaming device drive-virtio1: Completed 5 of 10 bytes, speed limit 1048576 bytes/s\r\n" +
			"Type backup, device drive-scsi1: Completed 0 of 20 bytes, speed limit 0 bytes/s\r\n",
			BlockJobs{Jobs: []BlockJob{mirror,
				{Type: "stream", Device: "drive-virtio1", Completed: 5, Total: 10, SpeedLimit: 1048576},
				{Type: "backup", Device: "drive-scsi1", Total: 20}}, Unparsed: []string{}}},
		{"unknown line", "A line QEMU has never printed\n",
			BlockJobs{Jobs: []BlockJob{}, Unparsed: []string{"A line QEMU has never printed"}}},
	} {
		if got := parseBlockJobs(tt.out); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: parseBlockJobs = %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

// TestGetBlockJobsSendsTheFixedCommand pins the one request GetBlockJobs
// makes: the VM's monitor, with `info block-jobs` and nothing else.
func TestGetBlockJobsSendsTheFixedCommand(t *testing.T) {
	t.Parallel()
	got := requireSentOnce(t, func(c *Client) error {
		_, err := c.GetBlockJobs(context.Background(), "pve-01", 101)
		return err
	})
	if got.method != "POST" || got.path != "/api2/json/nodes/pve-01/qemu/101/monitor" ||
		len(got.form) != 1 || got.form.Get("command") != "info block-jobs" {
		t.Errorf("sent %+v, want one POST of command=info block-jobs to the VM's monitor", got)
	}
}

// TestGetBlockJobsReadsPVEsErrorText pins how a failure comes back: qemu-server
// answers it with 200 and "ERROR: ..." text, which must read as the refusal it
// is — a stopped VM as not running — and never as a line QEMU printed.
func TestGetBlockJobsReadsPVEsErrorText(t *testing.T) {
	t.Parallel()
	srv, _ := newRecordingServer(t, `{"data":"ERROR: VM 101 not running\n"}`)
	_, err := newTestClient(t, srv.URL).GetBlockJobs(context.Background(), "pve-01", 101)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !IsGuestNotRunningError(err) {
		t.Errorf("err = %v, want an *APIError that reads as not running", err)
	}
}

// TestOnlyOneMonitorCommand pins the one place this package reaches PVE's QEMU
// monitor, and that it sends a read-only `info` command. The endpoint runs any
// HMP command it is given, and some (migrate "exec:...") are a shell on the
// node for a token allowed to send them.
func TestOnlyOneMonitorCommand(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for range strings.Count(string(src), `"/monitor"`) {
			calls = append(calls, f)
		}
		if f == "client_monitor.go" && !strings.Contains(string(src), `form.Set("command", "info block-jobs")`) {
			t.Error("client_monitor.go no longer sends the fixed `info block-jobs` command")
		}
	}
	if len(calls) != 1 || calls[0] != "client_monitor.go" {
		t.Errorf("the monitor path appears in %v, want once, in client_monitor.go", calls)
	}
}
