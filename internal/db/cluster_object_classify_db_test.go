package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

var (
	classifyCluster = uuid.MustParse("124c0000-0000-4000-8000-00000000000a")
	classifyOther   = uuid.MustParse("124c0000-0000-4000-8000-00000000000b")
)

// classifyAudit is an audit row beside a change. rid "" is the case's own object,
// "node" its host, "dev" a new OSD's device on its host, "dev-elsewhere" on another,
// "job" the case's rolling update job.
type classifyAudit struct {
	other      bool    // on the other cluster
	at         float64 // minutes ago
	rtype, rid string
	action     string
	details    map[string]any
	taskID     string // the id field of the UPID of the task behind the row ({oid}: the case's object); none if empty
	running    bool   // with a task row that has not finished
}

// ClassifyClusterObjectChanges explains a change only by an audit row whose action
// does what changed, for each thing that changed, inside a window capped at 30
// minutes before it was seen; a row that explains it is named in explained_by.
// Every case has its own object and node, so that cases cannot explain one another.
func TestClassifyClusterObjectChanges(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)
	q := gen.New(pool)

	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, []uuid.UUID{classifyCluster, classifyOther})
	}
	purge()
	defer purge()
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	for i, id := range []uuid.UUID{classifyCluster, classifyOther} {
		exec("cluster", `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, $2, 'https://192.0.2.10:8006', 'tok', 'enc')`, id, "cluster0"+string(rune('1'+i)))
	}

	now := time.Now().UTC().Truncate(time.Second)
	ago := func(m float64) time.Time { return now.Add(-time.Duration(m * float64(time.Minute))) }

	const (
		explained = "explained"
		shown     = "shown"
		waiting   = "waiting"
	)
	osdDown := `{"up":{"from":true,"to":false}}`
	osdUp := `{"up":{"from":false,"to":true}}`
	cases := []struct {
		name     string
		rtype    string
		change   string
		fields   []string
		detail   string  // JSON, default {}
		node     bool    // the case owns a node (an OSD's host)
		observed float64 // minutes ago, default 10
		lastSeen float64 // minutes ago, default observed + 0.5
		unlisted bool    // the node's task list was not read
		observer bool    // filed by the observer, which decides its own rows
		rolling  string  // a rolling update job working on the case's node ("host") or another ("other")
		drained  float64 // minutes ago that job began draining it, default 13
		rebooted float64 // and rebooting it, default 12
		audits   []classifyAudit
		want     string
		wantBy   int // the audit that explains it, -1 for none
	}{
		// An OSD going down or coming up, in or out, appearing or going.
		{name: "down, stopped", rtype: "ceph_osd", change: "changed", fields: []string{"up"}, detail: osdDown, node: true,
			audits: []classifyAudit{{at: 10.3, rtype: "ceph_osd", action: "osd_stop", taskID: "osd.{oid}"}}, want: explained, wantBy: 0},
		{name: "down, the stop's task was another OSD's", rtype: "ceph_osd", change: "changed", fields: []string{"up"}, detail: osdDown, node: true,
			audits: []classifyAudit{{at: 10.3, rtype: "ceph_osd", action: "osd_stop", taskID: "osd.999"}}, want: shown, wantBy: -1},
		{name: "down after an osd_in, which explains nothing", rtype: "ceph_osd", change: "changed", fields: []string{"up"}, detail: osdDown, node: true,
			audits: []classifyAudit{{at: 10.3, rtype: "ceph_osd", action: "osd_in"}}, want: shown, wantBy: -1},
		{name: "up, though the row says it was stopped", rtype: "ceph_osd", change: "changed", fields: []string{"up"}, detail: osdUp, node: true,
			audits: []classifyAudit{{at: 10.3, rtype: "ceph_osd", action: "osd_stop"}}, want: shown, wantBy: -1},
		{name: "up, started", rtype: "ceph_osd", change: "changed", fields: []string{"up"}, detail: osdUp, node: true,
			audits: []classifyAudit{{at: 10.3, rtype: "ceph_osd", action: "osd_start"}}, want: explained, wantBy: 0},
		{name: "down, its host rebooted", rtype: "ceph_osd", change: "changed", fields: []string{"up"}, detail: osdDown, node: true,
			audits: []classifyAudit{{at: 11, rtype: "node", rid: "node", action: "reboot"}}, want: explained, wantBy: 0},
		{name: "down, another host rebooted", rtype: "ceph_osd", change: "changed", fields: []string{"up"}, detail: osdDown, node: true,
			audits: []classifyAudit{{at: 11, rtype: "node", rid: "pve-99", action: "reboot"}}, want: shown, wantBy: -1},
		{name: "down and out, only the stop is on record", rtype: "ceph_osd", change: "changed", fields: []string{"in", "up"},
			detail: `{"up":{"from":true,"to":false},"in":{"from":true,"to":false}}`, node: true,
			audits: []classifyAudit{{at: 10.3, rtype: "ceph_osd", action: "osd_stop"}}, want: shown, wantBy: -1},
		{name: "down and out, both on record", rtype: "ceph_osd", change: "changed", fields: []string{"in", "up"},
			detail: `{"up":{"from":true,"to":false},"in":{"from":true,"to":false}}`, node: true,
			audits: []classifyAudit{{at: 10.3, rtype: "ceph_osd", action: "osd_stop"}, {at: 10.2, rtype: "ceph_osd", action: "osd_out"}}, want: explained, wantBy: 1},
		{name: "reweighted: no action explains it", rtype: "ceph_osd", change: "changed", fields: []string{"crush_weight"}, node: true,
			detail: `{"crush_weight":{"from":1.8,"to":0.9}}`,
			audits: []classifyAudit{{at: 10.3, rtype: "ceph_osd", action: "osd_out"}}, want: shown, wantBy: -1},
		{name: "down, a rolling update was working on its host", rtype: "ceph_osd", change: "changed", fields: []string{"up"}, detail: osdDown, node: true, rolling: "host",
			audits: []classifyAudit{{at: 15, rtype: "rolling_update", rid: "job", action: "rolling_update_started"}}, want: explained, wantBy: 0},
		{name: "down, a rolling update was working on another host", rtype: "ceph_osd", change: "changed", fields: []string{"up"}, detail: osdDown, node: true, rolling: "other",
			audits: []classifyAudit{{at: 15, rtype: "rolling_update", rid: "job", action: "rolling_update_started"}}, want: shown, wantBy: -1},
		{name: "down, a rolling update began rebooting its host hours ago and has not finished", rtype: "ceph_osd", change: "changed", fields: []string{"up"}, detail: osdDown, node: true,
			rolling: "host", drained: 310, rebooted: 300,
			audits: []classifyAudit{{at: 310, rtype: "rolling_update", rid: "job", action: "rolling_update_started"}}, want: shown, wantBy: -1},
		{name: "down, a rolling update that drained its host for hours rebooted it minutes ago", rtype: "ceph_osd", change: "changed", fields: []string{"up"}, detail: osdDown, node: true,
			rolling: "host", drained: 200, rebooted: 20,
			audits: []classifyAudit{{at: 200, rtype: "rolling_update", rid: "job", action: "rolling_update_started"}}, want: explained, wantBy: 0},
		{name: "a new OSD, created on its host (keyed by the device); the task still runs", rtype: "ceph_osd", change: "added", node: true,
			audits: []classifyAudit{{at: 14, rtype: "ceph_osd", rid: "dev", action: "osd_create", taskID: "sdb", running: true}}, want: explained, wantBy: 0},
		{name: "a new OSD, created on another host", rtype: "ceph_osd", change: "added", node: true,
			audits: []classifyAudit{{at: 14, rtype: "ceph_osd", rid: "dev-elsewhere", action: "osd_create"}}, want: shown, wantBy: -1},
		{name: "an OSD gone, destroyed", rtype: "ceph_osd", change: "removed", node: true,
			audits: []classifyAudit{{at: 10.3, rtype: "ceph_osd", action: "osd_destroy"}}, want: explained, wantBy: 0},
		{name: "an OSD gone, only stopped", rtype: "ceph_osd", change: "removed", node: true,
			audits: []classifyAudit{{at: 10.3, rtype: "ceph_osd", action: "osd_stop"}}, want: shown, wantBy: -1},

		// A pool.
		{name: "a pool resized, updated", rtype: "ceph_pool", change: "changed", fields: []string{"size"},
			audits: []classifyAudit{{at: 10.3, rtype: "ceph_pool", action: "update", taskID: "{oid}"}}, want: explained, wantBy: 0},
		{name: "a pool resized, the update's task was another pool's", rtype: "ceph_pool", change: "changed", fields: []string{"size"},
			audits: []classifyAudit{{at: 10.3, rtype: "ceph_pool", action: "update", taskID: "pool-elsewhere"}}, want: shown, wantBy: -1},
		{name: "a pool resized, only created", rtype: "ceph_pool", change: "changed", fields: []string{"size"},
			audits: []classifyAudit{{at: 10.3, rtype: "ceph_pool", action: "create"}}, want: shown, wantBy: -1},
		{name: "a pool gone, deleted", rtype: "ceph_pool", change: "removed",
			audits: []classifyAudit{{at: 10.3, rtype: "ceph_pool", action: "delete"}}, want: explained, wantBy: 0},

		// A replication job.
		{name: "a job edited, the update names the field", rtype: "replication", change: "changed", fields: []string{"schedule"},
			audits: []classifyAudit{{at: 10.2, rtype: "replication", action: "updated", details: map[string]any{"fields": []string{"schedule", "rate"}}}}, want: explained, wantBy: 0},
		{name: "a job edited, the update names another field", rtype: "replication", change: "changed", fields: []string{"schedule"},
			audits: []classifyAudit{{at: 10.2, rtype: "replication", action: "updated", details: map[string]any{"fields": []string{"comment"}}}}, want: shown, wantBy: -1},
		{name: "a job edited in the GUI, a sync triggered beside it", rtype: "replication", change: "changed", fields: []string{"schedule"},
			audits: []classifyAudit{{at: 10.2, rtype: "replication", action: "triggered"}}, want: shown, wantBy: -1},
		{name: "a job appeared, created", rtype: "replication", change: "added",
			audits: []classifyAudit{{at: 10.2, rtype: "replication", action: "created"}}, want: explained, wantBy: 0},
		{name: "a job gone, deleted", rtype: "replication", change: "removed",
			audits: []classifyAudit{{at: 10.2, rtype: "replication", action: "deleted"}}, want: explained, wantBy: 0},

		// The window, the cluster, the kind, the settle and the node's list.
		{name: "a row from before the object was last seen", rtype: "ceph_pool", change: "changed", fields: []string{"size"},
			audits: []classifyAudit{{at: 15, rtype: "ceph_pool", action: "update"}}, want: shown, wantBy: -1},
		{name: "a row from after the change was seen", rtype: "ceph_pool", change: "changed", fields: []string{"size"},
			audits: []classifyAudit{{at: 5, rtype: "ceph_pool", action: "update"}}, want: shown, wantBy: -1},
		{name: "after an outage, a row older than the cap", rtype: "ceph_pool", change: "changed", fields: []string{"size"}, lastSeen: 300,
			audits: []classifyAudit{{at: 120, rtype: "ceph_pool", action: "update"}}, want: shown, wantBy: -1},
		{name: "after an outage, a row inside the cap", rtype: "ceph_pool", change: "changed", fields: []string{"size"}, lastSeen: 300,
			audits: []classifyAudit{{at: 25, rtype: "ceph_pool", action: "update"}}, want: explained, wantBy: 0},
		{name: "a task that started hours ago and still runs", rtype: "ceph_pool", change: "changed", fields: []string{"size"},
			audits: []classifyAudit{{at: 120, rtype: "ceph_pool", action: "update", taskID: "{oid}", running: true}}, want: shown, wantBy: -1},
		{name: "a change that names no field", rtype: "ceph_pool", change: "changed", want: shown, wantBy: -1},
		{name: "another cluster's row", rtype: "ceph_pool", change: "changed", fields: []string{"size"},
			audits: []classifyAudit{{other: true, at: 10.3, rtype: "ceph_pool", action: "update"}}, want: shown, wantBy: -1},
		{name: "the same id under another kind", rtype: "ceph_pool", change: "changed", fields: []string{"size"},
			audits: []classifyAudit{{at: 10.3, rtype: "ha_resource", action: "update"}}, want: shown, wantBy: -1},
		{name: "seen too recently to decide", rtype: "ceph_pool", change: "changed", fields: []string{"size"}, observed: 1, lastSeen: 1.5,
			audits: []classifyAudit{{at: 1.2, rtype: "ceph_pool", action: "update"}}, want: waiting, wantBy: -1},
		{name: "the observer's own row is left to the observer", rtype: "vm", change: "changed", fields: []string{"memory"}, observer: true,
			audits: []classifyAudit{{at: 10.3, rtype: "vm", action: "config_update"}}, want: waiting, wantBy: -1},
		{name: "a node whose task list was not read", rtype: "ceph_osd", change: "changed", fields: []string{"up"}, detail: osdDown, node: true, unlisted: true,
			audits: []classifyAudit{{at: 10.3, rtype: "ceph_osd", action: "osd_stop"}}, want: waiting, wantBy: -1},
		{name: "a node whose list stayed unread past the give-up", rtype: "ceph_osd", change: "changed", fields: []string{"up"}, detail: osdDown, node: true, unlisted: true,
			observed: 26 * 60, lastSeen: 26*60 + 1, want: shown, wantBy: -1},
	}

	type seeded struct {
		id     uuid.UUID
		audits []uuid.UUID
	}
	seededCases := make([]seeded, len(cases))
	var listed []string
	upidSeq := 0
	for i, tc := range cases {
		oid := fmt.Sprintf("%d", 300+i)
		node := ""
		if tc.node {
			node = fmt.Sprintf("pve-%02d", i+10)
			if !tc.unlisted {
				listed = append(listed, node)
			}
		}
		observed, lastSeen := tc.observed, tc.lastSeen
		if observed == 0 {
			observed = 10
		}
		if lastSeen == 0 {
			lastSeen = observed + 0.5
		}
		seededCases[i].id = uuid.New()
		exec(tc.name, `INSERT INTO cluster_object_changes
			(id, cluster_id, resource_type, resource_id, change, fields, detail, node, observed_by, last_seen_at, observed_at)
			VALUES ($1, $2, $3, $4, $5, COALESCE($6::text[], '{}'), COALESCE(NULLIF($7, ''), '{}')::jsonb, $8, $11, $9, $10)`,
			seededCases[i].id, classifyCluster, tc.rtype, oid, tc.change, tc.fields, tc.detail, node, ago(lastSeen), ago(observed),
			map[bool]string{false: "ceph", true: "observer"}[tc.observer])
		jobID := uuid.New()
		if tc.rolling != "" {
			worked := node
			if tc.rolling == "other" {
				worked = "pve-97"
			}
			exec("rolling job", `INSERT INTO rolling_update_jobs (id, cluster_id, created_by) VALUES ($1, $2, $3)`, jobID, classifyCluster, systemUserID)
			drained, rebooted := tc.drained, tc.rebooted
			if drained == 0 {
				drained, rebooted = 13, 12
			}
			exec("rolling node", `INSERT INTO rolling_update_nodes (job_id, node_name, drain_started_at, reboot_started_at) VALUES ($1, $2, $3, $4)`,
				jobID, worked, ago(drained), ago(rebooted))
		}
		for _, a := range tc.audits {
			auditID := uuid.New()
			seededCases[i].audits = append(seededCases[i].audits, auditID)
			cluster, rid := classifyCluster, a.rid
			if a.other {
				cluster = classifyOther
			}
			details := map[string]any{}
			for k, v := range a.details {
				details[k] = v
			}
			switch rid {
			case "":
				rid = oid
			case "node":
				rid = node
			case "job":
				rid = jobID.String()
			case "dev", "dev-elsewhere":
				// A new OSD is keyed by its device, and the create names the node it ran on.
				rid, details["node"] = "sdb", node
				if a.rid == "dev-elsewhere" {
					details["node"] = "pve-98"
				}
			}
			if a.running && a.taskID == "" {
				t.Fatalf("%s: a running task needs a taskID", tc.name)
			}
			if a.taskID != "" {
				upidSeq++
				upid := fmt.Sprintf("UPID:%s:%08X:00000001:6A8CE407:task:%s:root@pam:", node, upidSeq, strings.ReplaceAll(a.taskID, "{oid}", oid))
				details["upid"] = upid
				if a.running {
					exec("running task", `INSERT INTO task_history (cluster_id, user_id, upid, status, node, task_type, started_at, source)
						VALUES ($1, $2, $3, 'running', $4, 'task', $5, 'proxmox')`, cluster, systemUserID, upid, node, ago(a.at))
				}
			}
			raw, _ := json.Marshal(details)
			exec("audit "+a.action, `INSERT INTO audit_log (id, cluster_id, resource_type, resource_id, action, details, source, created_at)
				VALUES ($1, $2, $3, $4, $5, $6::jsonb, 'proxmox', $7)`, auditID, cluster, a.rtype, rid, a.action, raw, ago(a.at))
		}
	}

	if _, err := q.ClassifyClusterObjectChanges(ctx, gen.ClassifyClusterObjectChangesParams{
		ClusterID: classifyCluster, SettledBefore: ago(5), ListedNodes: listed, GiveUpBefore: now.Add(-24 * time.Hour),
	}); err != nil {
		t.Fatalf("classify: %v", err)
	}
	// Decided once: a row that would explain a change shown a moment ago comes too
	// late, and nothing is left to decide.
	for i, tc := range cases {
		if tc.name == "a pool resized, only created" {
			exec("a late update", `INSERT INTO audit_log (id, cluster_id, resource_type, resource_id, action, details, source, created_at)
				SELECT gen_random_uuid(), cluster_id, 'ceph_pool', resource_id, 'update', '{}', 'proxmox', now() - interval '10 minutes'
				FROM cluster_object_changes WHERE id = $1`, seededCases[i].id)
		}
	}
	if n, err := q.ClassifyClusterObjectChanges(ctx, gen.ClassifyClusterObjectChangesParams{
		ClusterID: classifyCluster, SettledBefore: ago(5), ListedNodes: listed, GiveUpBefore: now.Add(-24 * time.Hour),
	}); err != nil || n != 0 {
		t.Fatalf("a second pass decided %d changes (err %v), want none: a decided change is not decided again", n, err)
	}
	for i, tc := range cases {
		var e pgtype.Bool
		var by pgtype.UUID
		if err := pool.QueryRow(ctx, `SELECT explained, explained_by FROM cluster_object_changes WHERE id = $1`, seededCases[i].id).
			Scan(&e, &by); err != nil {
			t.Fatalf("%s: read: %v", tc.name, err)
		}
		got := waiting
		switch {
		case e.Valid && e.Bool:
			got = explained
		case e.Valid:
			got = shown
		}
		var wantBy pgtype.UUID
		if tc.wantBy >= 0 {
			wantBy = pgtype.UUID{Bytes: seededCases[i].audits[tc.wantBy], Valid: true}
		}
		if got != tc.want || by != wantBy {
			t.Errorf("%s: %s, explained by %v; want %s, by %v", tc.name, got, by, tc.want, wantBy)
		}
	}
}
