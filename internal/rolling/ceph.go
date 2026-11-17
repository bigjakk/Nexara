package rolling

import (
	"context"
	"fmt"
	"strings"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// CephOutcome is what one look at Ceph found.
type CephOutcome string

const (
	// CephClean: every PG is active and clean, and as many OSDs are up as when
	// the job took its baseline.
	CephClean CephOutcome = "clean"
	// CephNotClean: Ceph answered and something is still recovering.
	CephNotClean CephOutcome = "not_clean"
	// CephUnknown: the status could not be read. Never read as clean.
	CephUnknown CephOutcome = "unknown"
)

// CephCheck is the verdict of cephCheck, in a form the job view can show.
type CephCheck struct {
	Outcome      CephOutcome `json:"outcome"`
	Reason       string      `json:"reason"`
	PGsNotClean  int         `json:"pgs_not_clean"`
	PGsTotal     int         `json:"pgs_total"`
	OSDsUp       int         `json:"osds_up"`
	OSDsExpected int         `json:"osds_expected"`
}

// pgStateClean reports whether a pgs_by_state entry ("active+clean+scrubbing")
// names both active and clean among its states.
func pgStateClean(state string) bool {
	var active, clean bool
	for _, s := range strings.Split(state, "+") {
		switch strings.TrimSpace(s) {
		case "active":
			active = true
		case "clean":
			clean = true
		}
	}
	return active && clean
}

// cephCheck decides whether Ceph is clean enough to take a node down, or to
// call a node back. Health status and health checks are deliberately not read:
// the job's own noout raises HEALTH_WARN, and an unrelated warning must not
// hold a job. osdBaseline is how many OSDs were up when the job took its
// baseline (0 for none), so an OSD already down before the job is not waited
// for. readErr is a status read that failed: could not look, never clean.
func cephCheck(status *proxmox.CephStatus, readErr error, osdBaseline int) CephCheck {
	if readErr != nil || status == nil {
		return CephCheck{Outcome: CephUnknown, Reason: "Could not read the Ceph status from Proxmox"}
	}
	c := CephCheck{
		Outcome:      CephClean,
		PGsTotal:     status.PGMap.NumPGs,
		OSDsUp:       status.OSDMap.NumUpOSDs,
		OSDsExpected: osdBaseline,
	}
	var reasons []string
	sum := 0
	for _, s := range status.PGMap.PGsPerState {
		sum += s.Count
		if !pgStateClean(s.StateName) {
			c.PGsNotClean += s.Count
		}
	}
	if c.PGsTotal == 0 {
		c.PGsTotal = sum
	}
	switch {
	case len(status.PGMap.PGsPerState) == 0 && (status.PGMap.NumPGs > 0 || status.PGMap.NumPools > 0):
		reasons = append(reasons, "Ceph reports no placement group states")
	case c.PGsNotClean > 0:
		reasons = append(reasons, fmt.Sprintf("%d of %d placement groups are not active+clean", c.PGsNotClean, c.PGsTotal))
	}
	if c.OSDsUp < osdBaseline {
		reasons = append(reasons, fmt.Sprintf("%d OSDs up, %d expected", c.OSDsUp, osdBaseline))
	}
	if len(reasons) > 0 {
		c.Outcome = CephNotClean
		c.Reason = strings.Join(reasons, "; ")
	}
	return c
}

// CephPreflight is the pre-flight's answer about Ceph: Status is "absent" (the
// cluster has no Ceph), "clean", "not_clean" or "unknown" (could not look).
type CephPreflight struct {
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty"`
	PGsNotClean int    `json:"pgs_not_clean"`
	PGsTotal    int    `json:"pgs_total"`
	OSDsUp      int    `json:"osds_up"`
	OSDsTotal   int    `json:"osds_total"`
}

// AnalyzeCeph reads the cluster's Ceph status for the pre-flight: whether it
// runs Ceph, whether Ceph is clean now as the job's first gate would judge it,
// and warnings (never errors: warn and confirm, not block) for a Ceph that is
// not clean or could not be read, and for more than one node down at a time.
func AnalyzeCeph(ctx context.Context, client *proxmox.Client, parallelism int32) (CephPreflight, []HAConflict) {
	status, presence, err := client.ReadCephStatus(ctx)
	if presence == proxmox.CephAbsent {
		return CephPreflight{Status: "absent"}, []HAConflict{}
	}
	check := cephCheck(status, err, 0)
	out := CephPreflight{Status: string(check.Outcome), Reason: check.Reason,
		PGsNotClean: check.PGsNotClean, PGsTotal: check.PGsTotal, OSDsUp: check.OSDsUp}
	if status != nil {
		out.OSDsTotal = status.OSDMap.NumOSDs
	}
	conflicts := []HAConflict{}
	warn := func(typ, msg string) {
		conflicts = append(conflicts, HAConflict{Source: "ceph", Type: typ, Severity: "warning", Message: msg})
	}
	switch check.Outcome {
	case CephNotClean:
		warn("ceph_not_clean", "Ceph is not clean: "+check.Reason+". With Wait for Ceph on, the job waits for it before taking the first node down.")
	case CephUnknown:
		warn("ceph_unknown", "Could not read the Ceph status. With Wait for Ceph on, the job waits until it can.")
	}
	if parallelism > 1 {
		warn("ceph_parallelism", fmt.Sprintf("Parallelism is %d on a Ceph cluster. With Wait for Ceph on, Nexara takes the nodes one at a time whatever the parallelism; with it off, taking more than one node down at a time can leave placement groups without enough copies.", parallelism))
	}
	return out, conflicts
}
