package handlers

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bigjakk/nexara/internal/rolling"
)

// ptr is a local helper for the pointer-valued threshold fields.
func ptr(f float64) *float64 { return &f }

// realWorldHwmon is the shape a `grep -H .` actually returns: files arrive in
// glob order, so every `name` comes before every `_label`, which comes before
// every `_input` — the readings are never contiguous per device, which is the
// thing the parser has to reassemble.
const realWorldHwmon = `/sys/class/hwmon/hwmon0/name:acpitz
/sys/class/hwmon/hwmon1/name:coretemp
/sys/class/hwmon/hwmon2/name:nvme
/sys/class/hwmon/hwmon1/temp1_label:Package id 0
/sys/class/hwmon/hwmon1/temp2_label:Core 0
/sys/class/hwmon/hwmon2/temp1_label:Composite
/sys/class/hwmon/hwmon0/temp1_input:27800
/sys/class/hwmon/hwmon1/temp1_input:45000
/sys/class/hwmon/hwmon1/temp2_input:43000
/sys/class/hwmon/hwmon2/temp1_input:38850
/sys/class/hwmon/hwmon1/temp1_max:80000
/sys/class/hwmon/hwmon1/temp2_max:80000
/sys/class/hwmon/hwmon1/temp1_crit:100000
/sys/class/hwmon/hwmon2/temp1_crit:84850
`

func TestParseHwmonTemps(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []nodeSensorReading
	}{
		{
			name: "realistic multi-chip host",
			raw:  realWorldHwmon,
			want: []nodeSensorReading{
				{Chip: "acpitz", Device: "hwmon0", Key: "temp1", TempC: 27.8},
				{Chip: "coretemp", Device: "hwmon1", Key: "temp1", Label: "Package id 0", TempC: 45, HighC: ptr(80), CritC: ptr(100)},
				{Chip: "coretemp", Device: "hwmon1", Key: "temp2", Label: "Core 0", TempC: 43, HighC: ptr(80)},
				{Chip: "nvme", Device: "hwmon2", Key: "temp1", Label: "Composite", TempC: 38.9, CritC: ptr(84.9)},
			},
		},
		{
			// The normal outcome on a virtualised node: the glob matches
			// nothing, grep exits 1, `|| true` swallows it, stdout is empty.
			name: "no hwmon devices at all",
			raw:  "",
			want: nil,
		},
		{
			name: "whitespace-only output",
			raw:  "\n\n  \n",
			want: nil,
		},
		{
			name: "a sensor with a label and a limit but no reading is dropped",
			raw: `/sys/class/hwmon/hwmon0/name:nct6798
/sys/class/hwmon/hwmon0/temp5_label:AUXTIN1
/sys/class/hwmon/hwmon0/temp5_crit:120000
`,
			want: nil,
		},
		{
			name: "unparseable and out-of-range readings are dropped",
			raw: `/sys/class/hwmon/hwmon0/name:flaky
/sys/class/hwmon/hwmon0/temp1_input:n/a
/sys/class/hwmon/hwmon0/temp2_input:
/sys/class/hwmon/hwmon0/temp3_input:2147483647
/sys/class/hwmon/hwmon0/temp4_input:-273150
/sys/class/hwmon/hwmon0/temp5_input:41000
`,
			want: []nodeSensorReading{
				{Chip: "flaky", Device: "hwmon0", Key: "temp5", TempC: 41},
			},
		},
		{
			// A zero crit would otherwise mark every healthy reading on the
			// chip as critical — the one failure mode that actively misleads.
			name: "zero and negative thresholds are rejected, the reading survives",
			raw: `/sys/class/hwmon/hwmon0/name:k10temp
/sys/class/hwmon/hwmon0/temp1_input:52000
/sys/class/hwmon/hwmon0/temp1_max:0
/sys/class/hwmon/hwmon0/temp1_crit:-1000
`,
			want: []nodeSensorReading{
				{Chip: "k10temp", Device: "hwmon0", Key: "temp1", TempC: 52},
			},
		},
		{
			// Two NVMe drives both publish name "nvme"; only the device tells
			// them apart, and the UI needs that to avoid two identical rows.
			name: "two devices sharing a chip name stay distinct and ordered",
			raw: `/sys/class/hwmon/hwmon10/name:nvme
/sys/class/hwmon/hwmon2/name:nvme
/sys/class/hwmon/hwmon10/temp1_input:44000
/sys/class/hwmon/hwmon2/temp1_input:38000
`,
			want: []nodeSensorReading{
				{Chip: "nvme", Device: "hwmon2", Key: "temp1", TempC: 38},
				{Chip: "nvme", Device: "hwmon10", Key: "temp1", TempC: 44},
			},
		},
		{
			name: "sensor indices sort numerically, not lexically",
			raw: `/sys/class/hwmon/hwmon0/name:coretemp
/sys/class/hwmon/hwmon0/temp10_input:50000
/sys/class/hwmon/hwmon0/temp2_input:48000
/sys/class/hwmon/hwmon0/temp1_input:47000
`,
			want: []nodeSensorReading{
				{Chip: "coretemp", Device: "hwmon0", Key: "temp1", TempC: 47},
				{Chip: "coretemp", Device: "hwmon0", Key: "temp2", TempC: 48},
				{Chip: "coretemp", Device: "hwmon0", Key: "temp10", TempC: 50},
			},
		},
		{
			// The path never contains a colon but a label may, so the split has
			// to be on the first one only.
			name: "a label containing a colon survives intact",
			raw: `/sys/class/hwmon/hwmon0/name:ipmi
/sys/class/hwmon/hwmon0/temp1_label:CPU: Inlet
/sys/class/hwmon/hwmon0/temp1_input:31000
`,
			want: []nodeSensorReading{
				{Chip: "ipmi", Device: "hwmon0", Key: "temp1", Label: "CPU: Inlet", TempC: 31},
			},
		},
		{
			name: "a device with no name file falls back to its directory",
			raw:  "/sys/class/hwmon/hwmon7/temp1_input:36000\n",
			want: []nodeSensorReading{
				{Chip: "hwmon7", Device: "hwmon7", Key: "temp1", TempC: 36},
			},
		},
		{
			name: "non-temperature attributes and foreign paths are ignored",
			raw: `/sys/class/hwmon/hwmon0/name:nct6798
/sys/class/hwmon/hwmon0/in0_input:1024
/sys/class/hwmon/hwmon0/fan1_input:1200
/sys/class/hwmon/hwmon0/temp1_type:4
/sys/class/hwmon/hwmon0/temp1_input:35000
/etc/passwd:root:x:0:0
garbage without a colon
`,
			want: []nodeSensorReading{
				{Chip: "nct6798", Device: "hwmon0", Key: "temp1", TempC: 35},
			},
		},
		{
			name: "CRLF line endings are tolerated",
			raw:  "/sys/class/hwmon/hwmon0/name:coretemp\r\n/sys/class/hwmon/hwmon0/temp1_input:49000\r\n",
			want: []nodeSensorReading{
				{Chip: "coretemp", Device: "hwmon0", Key: "temp1", TempC: 49},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseHwmonTemps(tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseHwmonTemps() mismatch\n got: %s\nwant: %s", formatReadings(got), formatReadings(tt.want))
			}
		})
	}
}

// TestParseHwmonTempsIsDeterministic guards the sort. The parser accumulates
// into maps, whose iteration order Go randomises per run *and* per range
// statement, so an unsorted result would reshuffle the panel on every poll
// while still passing a single-shot equality test often enough to look fine.
func TestParseHwmonTempsIsDeterministic(t *testing.T) {
	first := formatReadings(parseHwmonTemps(realWorldHwmon))
	for i := range 50 {
		if got := formatReadings(parseHwmonTemps(realWorldHwmon)); got != first {
			t.Fatalf("iteration %d differs from the first parse\n got: %s\nwant: %s", i, got, first)
		}
	}
}

func formatReadings(readings []nodeSensorReading) string {
	if len(readings) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(readings))
	for _, r := range readings {
		part := fmt.Sprintf("%s/%s/%s label=%q %.1f", r.Chip, r.Device, r.Key, r.Label, r.TempC)
		if r.HighC != nil {
			part += fmt.Sprintf(" high=%.1f", *r.HighC)
		}
		if r.CritC != nil {
			part += fmt.Sprintf(" crit=%.1f", *r.CritC)
		}
		parts = append(parts, part)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func TestSensorsUnavailableReason(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		wantClassified bool
		wantContains   string
	}{
		{
			name:           "missing credentials points at the settings page",
			err:            fmt.Errorf("%w: no rows in result set", rolling.ErrSSHNotConfigured),
			wantClassified: true,
			wantContains:   "SSH Credentials",
		},
		{
			name:           "unpinned host key asks for Test Connection",
			err:            fmt.Errorf("%w (192.0.2.10) — open Settings", rolling.ErrHostKeyNotPinned),
			wantClassified: true,
			wantContains:   "Test Connection",
		},
		{
			name:           "unknown node address blames the collector",
			err:            fmt.Errorf("%w (node %q)", rolling.ErrNodeAddressUnknown, "pve-01"),
			wantClassified: true,
			wantContains:   "collector",
		},
		{
			name:           "anything else is generic",
			err:            errors.New("dial 192.0.2.10:22: connect: connection refused"),
			wantClassified: false,
			wantContains:   "Could not read sensors",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, classified := sensorsUnavailableReason(tt.err)
			if classified != tt.wantClassified {
				t.Errorf("classified = %v, want %v", classified, tt.wantClassified)
			}
			if !strings.Contains(reason, tt.wantContains) {
				t.Errorf("reason %q does not contain %q", reason, tt.wantContains)
			}
		})
	}
}

// TestSensorsUnavailableReasonLeaksNothing is the security half of the
// classification: RunNodeCommand errors embed node IPs, host-key fingerprints
// and decrypt detail, and none of that may reach an API response. The reasons
// are written by hand precisely so that a future edit cannot slip `err.Error()`
// into one without this failing.
func TestSensorsUnavailableReasonLeaksNothing(t *testing.T) {
	secrets := []string{
		"192.0.2.10",
		"SHA256:0DwGkQ1kQ0N3Xd0Z7xk8h1tGq0mB2v9c4tE6yLp3sVk",
		"cipher: message authentication failed",
	}
	errs := []error{
		fmt.Errorf("%w: %s", rolling.ErrSSHNotConfigured, secrets[2]),
		fmt.Errorf("%w (%s) fingerprint %s", rolling.ErrHostKeyNotPinned, secrets[0], secrets[1]),
		fmt.Errorf("%w (node %q at %s)", rolling.ErrNodeAddressUnknown, "pve-01", secrets[0]),
		fmt.Errorf("SSH handshake with %s: %s", secrets[0], secrets[1]),
	}
	for _, err := range errs {
		reason, _ := sensorsUnavailableReason(err)
		for _, secret := range secrets {
			if strings.Contains(reason, secret) {
				t.Errorf("reason %q leaked %q from error %v", reason, secret, err)
			}
		}
	}
}

func TestNodeSensorsCache(t *testing.T) {
	cache := newNodeSensorsCache()
	stored := availableSensors([]nodeSensorReading{{Chip: "coretemp", Device: "hwmon0", Key: "temp1", TempC: 45}})

	if _, ok := cache.get("cluster/pve-01"); ok {
		t.Fatal("empty cache returned a hit")
	}

	cache.put("cluster/pve-01", stored, nodeSensorsTTL)
	got, ok := cache.get("cluster/pve-01")
	if !ok {
		t.Fatal("stored entry was not returned")
	}
	if !reflect.DeepEqual(got, stored) {
		t.Errorf("got %+v, want %+v", got, stored)
	}
	if _, ok := cache.get("cluster/pve-02"); ok {
		t.Error("a different node read another node's entry")
	}

	// A past expiry stands in for elapsed time, so the test needs no clock
	// injection and no sleep.
	cache.put("cluster/pve-03", stored, -time.Second)
	if _, ok := cache.get("cluster/pve-03"); ok {
		t.Error("an expired entry was served")
	}
}

// TestNodeSensorsCacheSweepsExpired covers the bound on the map: without the
// sweep, a renamed or removed node leaves its entry behind for the lifetime of
// the process.
func TestNodeSensorsCacheSweepsExpired(t *testing.T) {
	cache := newNodeSensorsCache()
	for i := range 20 {
		cache.put(fmt.Sprintf("cluster/gone-%d", i), availableSensors(nil), -time.Second)
	}
	cache.put("cluster/pve-01", availableSensors(nil), nodeSensorsTTL)

	cache.mu.Lock()
	size := len(cache.entries)
	cache.mu.Unlock()
	if size != 1 {
		t.Errorf("cache holds %d entries after the sweep, want 1 (the live one)", size)
	}
}

// TestNodeSensorsCacheNilSafe covers a NodeHandler built as a zero value rather
// than through NewNodeHandler: it must degrade to "no caching", not panic.
func TestNodeSensorsCacheNilSafe(t *testing.T) {
	var cache *nodeSensorsCache
	if _, ok := cache.get("cluster/pve-01"); ok {
		t.Error("nil cache reported a hit")
	}
	cache.put("cluster/pve-01", availableSensors(nil), nodeSensorsTTL)
}

// TestSensorsResponseEnvelope pins the two shape guarantees the SPA relies on:
// items is always a JSON array and never null, and total agrees with it.
func TestSensorsResponseEnvelope(t *testing.T) {
	unavailable := unavailableSensors("nope")
	if unavailable.Items == nil {
		t.Error("unavailable response has a nil Items; it must marshal as []")
	}
	if unavailable.Available {
		t.Error("unavailable response is marked available")
	}
	if unavailable.Reason == "" {
		t.Error("unavailable response carries no reason")
	}

	empty := availableSensors(nil)
	if empty.Items == nil {
		t.Error("available-but-empty response has a nil Items; it must marshal as []")
	}
	if !empty.Available || empty.Reason != "" {
		t.Errorf("available-but-empty response should be available with no reason, got %+v", empty)
	}

	full := availableSensors(parseHwmonTemps(realWorldHwmon))
	if full.Total != int64(len(full.Items)) {
		t.Errorf("Total = %d, want %d", full.Total, len(full.Items))
	}
}

// TestNodeSensorsCacheCollapsesConcurrentMisses is the regression test for the
// stampede: without singleflight, every goroutine that misses a cold key opens
// its own SSH session to the same node. A host that stalls the SSH banner holds
// each of those for the handshake timeout, so the pile-up feeds itself.
func TestNodeSensorsCacheCollapsesConcurrentMisses(t *testing.T) {
	cache := newNodeSensorsCache()
	want := availableSensors([]nodeSensorReading{
		{Chip: "coretemp", Device: "hwmon0", Key: "temp1", TempC: 45},
	})

	var calls atomic.Int64
	release := make(chan struct{})
	start := make(chan struct{})
	read := func() nodeSensorsResponse {
		calls.Add(1)
		// Hold the flight open so every caller is in-flight at once; a leader
		// that returned immediately would let the others hit the cache instead
		// and the test would pass even with no singleflight.
		<-release
		return want
	}

	const callers = 25
	var wg sync.WaitGroup
	got := make([]nodeSensorsResponse, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got[i] = cache.fetch("cluster/pve-01", read)
		}()
	}
	close(start)

	// Give the followers time to join the leader's flight before it completes.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if n := calls.Load(); n != 1 {
		t.Errorf("read was called %d times, want 1 — the SSH stampede is back", n)
	}
	for i, resp := range got {
		if !reflect.DeepEqual(resp, want) {
			t.Errorf("caller %d got %+v, want %+v", i, resp, want)
		}
	}

	// The flight's result must land in the cache, not just be handed back.
	if _, ok := cache.get("cluster/pve-01"); !ok {
		t.Error("the completed flight did not populate the cache")
	}
}

// TestNodeSensorsCacheFetchNilSafe covers the zero-value NodeHandler that
// internal/api's route guard constructs: fetch must still read, just not cache.
func TestNodeSensorsCacheFetchNilSafe(t *testing.T) {
	var cache *nodeSensorsCache
	want := availableSensors(nil)
	if got := cache.fetch("k", func() nodeSensorsResponse { return want }); !reflect.DeepEqual(got, want) {
		t.Errorf("nil cache fetch returned %+v, want %+v", got, want)
	}
}
