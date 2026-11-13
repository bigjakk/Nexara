package handlers

import "testing"

// How far a running task is, read from its log: a backup's own percentage
// lines, and the formats every other task uses, which must read as before.
func TestTaskLogProgress(t *testing.T) {
	t.Parallel()
	at := func(v float64) *float64 { return &v }
	backup := func(lines ...string) []string {
		return append([]string{
			"INFO: starting new backup job: vzdump 100 121 --mode snapshot --storage store01 --node pve-02",
			"INFO: Starting Backup of VM 121 (qemu)",
		}, lines...)
	}
	const done = "INFO: 100% (81.0 GiB of 81.0 GiB) in 1m 33s, read: 2.0 GiB/s, write: 0 B/s"
	tests := []struct {
		name     string
		taskType string
		lines    []string
		want     *float64
	}{
		{"a backup reads its latest percentage", "vzdump", backup(
			"INFO:  42% (34.0 GiB of 81.0 GiB) in 1m, read: 333.3 MiB/s, write: 76.0 MiB/s",
			"INFO:  73% (59.9 GiB of 81.0 GiB) in 1m 24s, read: 333.3 MiB/s, write: 76.0 MiB/s",
		), at(0.73)},
		{"a backup's summaries after its 100% are not progress", "vzdump", backup(done,
			"INFO: backup is sparse: 60.69 GiB (74%) total zero data",
			"INFO: backup was done incrementally, reused 79.11 GiB (97%)",
		), at(1)},
		{"a backup starts over for its next guest", "vzdump", backup(done,
			"INFO: Finished Backup of VM 121 (00:01:40)",
			"INFO: Starting Backup of VM 100 (qemu)",
		), at(0)},
		{"a container backup has no percentage", "vzdump",
			[]string{"INFO: Starting Backup of VM 105 (lxc)", "INFO: creating Proxmox Backup Server archive 'ct/105'"}, nil},
		{"a disk move", "qmmove", []string{"drive-scsi0: transferred 4.0 GiB of 32.0 GiB (12.50%) in 5s"}, at(0.125)},
		{"a live migration", "qmigrate",
			[]string{"migration active, transferred 4.0 GiB of 16.0 GiB VM-state, 833.4 MiB/s"}, at(0.25)},
		{"a generic progress line", "imgcopy", []string{"progress 0.5"}, at(0.5)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := taskLogProgress(tt.taskType, tt.lines)
			switch {
			case got == nil && tt.want == nil:
			case got == nil || tt.want == nil || *got != *tt.want:
				t.Errorf("progress = %v, want %v", progressValue(got), progressValue(tt.want))
			}
		})
	}
}

func progressValue(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}
