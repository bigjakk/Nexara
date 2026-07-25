package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// threeHostOSDs models the common small-cluster shape the pre-flight exists to
// protect: 3 hosts, one OSD each, all up and in.
func threeHostOSDs() []cephOSDResponse {
	return []cephOSDResponse{
		{ID: 0, Name: "osd.0", Host: "pve1", Up: 1, In: 1, Status: "up"},
		{ID: 1, Name: "osd.1", Host: "pve2", Up: 1, In: 1, Status: "up"},
		{ID: 2, Name: "osd.2", Host: "pve3", Up: 1, In: 1, Status: "up"},
	}
}

// size3Pool is the default Proxmox replicated pool: 3 copies, I/O blocks below 2.
func size3Pool() []cephPoolConstraint {
	return []cephPoolConstraint{{PoolName: "vmdata", Size: 3, MinSize: 2}}
}

func TestEvaluateOSDPreflight_HealthyClusterOutIsDegradedNotStalled(t *testing.T) {
	// Taking one of three hosts out leaves 2 serving: at min_size, below size.
	// Guest I/O keeps working, so this must warn rather than read as critical.
	pf := evaluateOSDPreflight(0, "out", threeHostOSDs(), size3Pool(), false)

	if pf.Severity != cephPreflightWarning {
		t.Errorf("severity = %q, want %q", pf.Severity, cephPreflightWarning)
	}
	if !pf.Disruptive {
		t.Error("out should be flagged disruptive")
	}
	if pf.HostsServing != 3 || pf.HostsServingAfter != 2 {
		t.Errorf("hosts serving = %d→%d, want 3→2", pf.HostsServing, pf.HostsServingAfter)
	}
	if pf.OSDsServing != 3 || pf.OSDsServingAfter != 2 {
		t.Errorf("OSDs serving = %d→%d, want 3→2", pf.OSDsServing, pf.OSDsServingAfter)
	}
	if pf.MaxPoolSize != 3 || pf.MaxPoolMinSize != 2 {
		t.Errorf("pool bounds = size %d / min_size %d, want 3 / 2", pf.MaxPoolSize, pf.MaxPoolMinSize)
	}
	if len(pf.Warnings) != 1 || !strings.Contains(pf.Warnings[0], "highest pool size (3)") {
		t.Errorf("warnings = %#v, want one mentioning pool size", pf.Warnings)
	}
}

func TestEvaluateOSDPreflight_SecondOutStallsIO(t *testing.T) {
	// osd.2 is already out; taking osd.0 out too drops to 1 host — under
	// min_size=2, which is the case that actually stalls guest I/O.
	osds := threeHostOSDs()
	osds[2].In = 0

	pf := evaluateOSDPreflight(0, "out", osds, size3Pool(), false)

	if pf.Severity != cephPreflightCritical {
		t.Fatalf("severity = %q, want %q", pf.Severity, cephPreflightCritical)
	}
	if pf.HostsServingAfter != 1 {
		t.Errorf("hosts after = %d, want 1", pf.HostsServingAfter)
	}
	joined := strings.Join(pf.Warnings, "\n")
	if !strings.Contains(joined, "min_size (2)") {
		t.Errorf("warnings should cite min_size, got %#v", pf.Warnings)
	}
	// The pre-existing degradation is the context that explains why this is
	// critical rather than merely degraded, so it must be surfaced too.
	if !strings.Contains(joined, "osd.2 (out)") {
		t.Errorf("warnings should report the already-out OSD, got %#v", pf.Warnings)
	}
}

func TestEvaluateOSDPreflight_TargetAlreadyDegradedIsNotSelfReported(t *testing.T) {
	// The OSD being acted on must not appear in its own "already degraded"
	// list — that is what the operator is fixing.
	osds := threeHostOSDs()
	osds[0].Up = 0
	osds[0].Status = "down"

	pf := evaluateOSDPreflight(0, "stop", osds, size3Pool(), false)

	for _, w := range pf.Warnings {
		if strings.Contains(w, "osd.0") {
			t.Errorf("target OSD reported as pre-existing degradation: %q", w)
		}
	}
	// It was already not serving, so stopping it changes nothing.
	if pf.HostsServing != 2 || pf.HostsServingAfter != 2 {
		t.Errorf("hosts serving = %d→%d, want 2→2", pf.HostsServing, pf.HostsServingAfter)
	}
}

func TestEvaluateOSDPreflight_NoOpActionDoesNotInheritExistingDegradation(t *testing.T) {
	// Drain then swap the disk: the operator marked osd.0 out, and now stops
	// the daemon. The cluster is genuinely degraded, but stopping a daemon that
	// already holds no live data costs nothing — grading it as a redundancy
	// loss would train operators to click through the warning that matters.
	osds := threeHostOSDs()
	osds[0].In = 0

	pf := evaluateOSDPreflight(0, "stop", osds, size3Pool(), false)

	if pf.HostsServing != 2 || pf.HostsServingAfter != 2 {
		t.Fatalf("hosts serving = %d→%d, want 2→2", pf.HostsServing, pf.HostsServingAfter)
	}
	if pf.Severity != cephPreflightOK {
		t.Errorf("severity = %q, want %q for a no-op action", pf.Severity, cephPreflightOK)
	}
	if len(pf.Warnings) != 0 {
		t.Errorf("warnings = %#v, want none for a no-op action", pf.Warnings)
	}
}

func TestEvaluateOSDPreflight_RepeatedOutIsNotCritical(t *testing.T) {
	// Marking an already-out OSD out again is literally a no-op, even on a
	// cluster that is down to one serving host.
	osds := threeHostOSDs()
	osds[0].In = 0
	osds[1].In = 0

	pf := evaluateOSDPreflight(0, "out", osds, size3Pool(), false)

	if pf.Severity != cephPreflightOK {
		t.Errorf("severity = %q, want %q", pf.Severity, cephPreflightOK)
	}
	if len(pf.Warnings) != 0 {
		t.Errorf("warnings = %#v, want none", pf.Warnings)
	}
}

func TestEvaluateOSDPreflight_UnreportedHostsCountSeparately(t *testing.T) {
	// Ceph did not report a host for any OSD. They cannot be shown to share a
	// failure domain, so they must not collapse into a single bucket — that
	// would read as "1 host serving" and fire a critical on a healthy cluster.
	osds := []cephOSDResponse{
		{ID: 0, Name: "osd.0", Up: 1, In: 1, Status: "up"},
		{ID: 1, Name: "osd.1", Up: 1, In: 1, Status: "up"},
		{ID: 2, Name: "osd.2", Up: 1, In: 1, Status: "up"},
		{ID: 3, Name: "osd.3", Up: 1, In: 1, Status: "up"},
	}

	pf := evaluateOSDPreflight(0, "out", osds, size3Pool(), false)

	if pf.HostsServing != 4 {
		t.Errorf("hosts serving = %d, want 4 (one bucket per unreported host)", pf.HostsServing)
	}
	if pf.Severity == cephPreflightCritical {
		t.Errorf("severity = %q, want no critical on a fully healthy cluster", pf.Severity)
	}
}

func TestEvaluateOSDPreflight_UnusablePoolBoundsReadAsUnknown(t *testing.T) {
	// Pools decoded but carry no size/min_size. A read that succeeds and tells
	// us nothing must not pass as "ok" — that is the one direction that
	// reassures wrongly.
	pools := []cephPoolConstraint{{PoolName: "vmdata", Size: 0, MinSize: 0}}

	pf := evaluateOSDPreflight(0, "out", threeHostOSDs(), pools, false)

	if pf.Severity != cephPreflightWarning {
		t.Errorf("severity = %q, want %q", pf.Severity, cephPreflightWarning)
	}
	if len(pf.Warnings) != 1 || !strings.Contains(pf.Warnings[0], "could not be read") {
		t.Errorf("warnings = %#v, want one about unreadable pool bounds", pf.Warnings)
	}
}

func TestEvaluateOSDPreflight_NoPoolsIsNotAWarning(t *testing.T) {
	// A cluster with no pools has no data at risk, so there is nothing to warn
	// about — distinct from pools we failed to read.
	pf := evaluateOSDPreflight(0, "out", threeHostOSDs(), nil, false)

	if pf.Severity != cephPreflightOK {
		t.Errorf("severity = %q, want %q", pf.Severity, cephPreflightOK)
	}
	if len(pf.Warnings) != 0 {
		t.Errorf("warnings = %#v, want none", pf.Warnings)
	}
}

func TestEvaluateOSDPreflight_MultipleOSDsPerHost(t *testing.T) {
	// With 2 OSDs per host, taking one out leaves its host serving, so
	// host-domain redundancy is untouched and nothing should be flagged.
	osds := []cephOSDResponse{
		{ID: 0, Name: "osd.0", Host: "pve1", Up: 1, In: 1, Status: "up"},
		{ID: 1, Name: "osd.1", Host: "pve1", Up: 1, In: 1, Status: "up"},
		{ID: 2, Name: "osd.2", Host: "pve2", Up: 1, In: 1, Status: "up"},
		{ID: 3, Name: "osd.3", Host: "pve2", Up: 1, In: 1, Status: "up"},
		{ID: 4, Name: "osd.4", Host: "pve3", Up: 1, In: 1, Status: "up"},
		{ID: 5, Name: "osd.5", Host: "pve3", Up: 1, In: 1, Status: "up"},
	}

	pf := evaluateOSDPreflight(0, "out", osds, size3Pool(), false)

	if pf.Severity != cephPreflightOK {
		t.Errorf("severity = %q, want %q", pf.Severity, cephPreflightOK)
	}
	if pf.HostsServingAfter != 3 {
		t.Errorf("hosts after = %d, want 3", pf.HostsServingAfter)
	}
	if pf.OSDsServingAfter != 5 {
		t.Errorf("OSDs after = %d, want 5", pf.OSDsServingAfter)
	}
	if len(pf.Warnings) != 0 {
		t.Errorf("warnings = %#v, want none", pf.Warnings)
	}
}

func TestEvaluateOSDPreflight_RestartCountsAsDown(t *testing.T) {
	pf := evaluateOSDPreflight(0, "restart", threeHostOSDs(), size3Pool(), false)

	if !pf.Disruptive {
		t.Error("restart should be flagged disruptive")
	}
	if pf.HostsServingAfter != 2 {
		t.Errorf("hosts after = %d, want 2", pf.HostsServingAfter)
	}
	joined := strings.Join(pf.Warnings, "\n")
	if !strings.Contains(joined, "briefly") {
		t.Errorf("restart warnings should note the outage is brief, got %#v", pf.Warnings)
	}
}

func TestEvaluateOSDPreflight_NonDisruptiveActions(t *testing.T) {
	// in/start only add capacity back, so they never warn — but they still
	// report counts so the dialog can show what the cluster looks like.
	osds := threeHostOSDs()
	osds[0].In = 0
	osds[0].Up = 0
	osds[0].Status = "down"

	for _, action := range []string{"in", "start"} {
		pf := evaluateOSDPreflight(0, action, osds, size3Pool(), false)
		if pf.Disruptive {
			t.Errorf("%s: should not be flagged disruptive", action)
		}
		if pf.Severity != cephPreflightOK {
			t.Errorf("%s: severity = %q, want %q", action, pf.Severity, cephPreflightOK)
		}
		if len(pf.Warnings) != 0 {
			t.Errorf("%s: warnings = %#v, want none", action, pf.Warnings)
		}
		if pf.HostsServing != 2 {
			t.Errorf("%s: hosts serving = %d, want 2", action, pf.HostsServing)
		}
	}
}

func TestEvaluateOSDPreflight_PoolsUnavailable(t *testing.T) {
	// No pool constraints means no redundancy verdict is possible; say so
	// rather than silently reporting "ok".
	pf := evaluateOSDPreflight(0, "out", threeHostOSDs(), nil, true)

	if pf.Severity != cephPreflightWarning {
		t.Errorf("severity = %q, want %q", pf.Severity, cephPreflightWarning)
	}
	if len(pf.Warnings) != 1 || !strings.Contains(pf.Warnings[0], "could not be read") {
		t.Errorf("warnings = %#v, want one about unreadable pool bounds", pf.Warnings)
	}
}

func TestEvaluateOSDPreflight_WarningsSerializeAsEmptyArray(t *testing.T) {
	// The UI maps over warnings; a JSON null would crash it.
	raw, err := json.Marshal(evaluateOSDPreflight(0, "in", threeHostOSDs(), size3Pool(), false))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"warnings":[]`) {
		t.Errorf("warnings should serialize as [], got %s", raw)
	}
}

func TestFlattenOSDTree_PropagatesHostAndInState(t *testing.T) {
	in0 := proxmox.FlexBool(false)
	tree := proxmox.CephOSDTreeNode{
		Type: "root", Name: "default", ID: -1,
		Children: []proxmox.CephOSDTreeNode{{
			Type: "host", Name: "pve1", ID: -2,
			Children: []proxmox.CephOSDTreeNode{
				// Host omitted: must be inherited from the enclosing bucket,
				// since daemon actions are addressed by host.
				{Type: "osd", Name: "osd.0", ID: 0, Status: "up", In: &in0, CrushWeight: 0.5},
				{Type: "osd", Name: "osd.1", ID: 1, Status: "down"},
			},
		}},
	}

	osds := flattenOSDTree(&tree)

	if len(osds) != 2 {
		t.Fatalf("got %d OSDs, want 2", len(osds))
	}
	for _, o := range osds {
		if o.Host != "pve1" {
			t.Errorf("osd.%d host = %q, want pve1", o.ID, o.Host)
		}
	}
	if osds[0].In != 0 {
		t.Errorf("osd.0 in = %d, want 0 (Proxmox reported it out)", osds[0].In)
	}
	if osds[0].Up != 1 {
		t.Errorf("osd.0 up = %d, want 1", osds[0].Up)
	}
	// osd.1 reports no "in" field: fall back to in, the behaviour that held
	// before the field was parsed at all.
	if osds[1].In != 1 {
		t.Errorf("osd.1 in = %d, want 1 (unreported defaults to in)", osds[1].In)
	}
	if osds[1].Up != 0 {
		t.Errorf("osd.1 up = %d, want 0", osds[1].Up)
	}
}

func TestFlattenOSDTree_PrefersExplicitHost(t *testing.T) {
	tree := proxmox.CephOSDTreeNode{
		Type: "host", Name: "bucket-name", ID: -2,
		Children: []proxmox.CephOSDTreeNode{
			{Type: "osd", Name: "osd.0", ID: 0, Status: "up", Host: "pve7"},
		},
	}

	osds := flattenOSDTree(&tree)

	if len(osds) != 1 || osds[0].Host != "pve7" {
		t.Errorf("got %#v, want host pve7 from the OSD's own field", osds)
	}
}

func TestFlexBool_ProxmoxShapes(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{"1", true},
		{"0", false},
		{"true", true},
		{"false", false},
		{`"1"`, true},
		{`"0"`, false},
		{`"nonsense"`, false},
	}

	for _, tt := range tests {
		var got proxmox.FlexBool
		if err := json.Unmarshal([]byte(tt.raw), &got); err != nil {
			t.Errorf("unmarshal %s: %v", tt.raw, err)
			continue
		}
		if bool(got) != tt.want {
			t.Errorf("unmarshal %s = %v, want %v", tt.raw, bool(got), tt.want)
		}
	}
}
