package handlers

import (
	"reflect"
	"testing"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// An edit names only what it sets to something other than the job has: the dialog
// sends schedule and comment whatever was touched, and a field named without a
// change would let the edit stand for a later one made in Proxmox.
func TestChangedReplicationFields(t *testing.T) {
	t.Parallel()
	job := &proxmox.ReplicationJob{Schedule: "*/15", Rate: "10", Comment: "nightly", Disable: 1}
	disable := func(n int) *int { return &n }
	for _, tc := range []struct {
		name   string
		before *proxmox.ReplicationJob
		req    proxmox.UpdateReplicationJobParams
		want   []string
	}{
		{"a job that could not be read", nil, proxmox.UpdateReplicationJobParams{Schedule: "*/5", Disable: disable(0)}, []string{}},
		{"everything sent as it was", job, proxmox.UpdateReplicationJobParams{Schedule: "*/15", Rate: "10", Comment: "nightly", Disable: disable(1)}, []string{}},
		{"nothing sent", job, proxmox.UpdateReplicationJobParams{}, []string{}},
		{"every field changed", job, proxmox.UpdateReplicationJobParams{Schedule: "*/5", Rate: "20", Comment: "weekly", Disable: disable(0)}, []string{"schedule", "rate", "comment", "disable"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := changedReplicationFields(tc.before, tc.req); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("fields = %v, want %v", got, tc.want)
			}
		})
	}
}
