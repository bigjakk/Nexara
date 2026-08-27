package handlers

import "testing"

// The coverage verdict is the sentence this whole feature exists to say, and
// every branch of it is a claim about whether someone's data is recoverable.
func TestCoverageVerdict(t *testing.T) {
	const (
		now   = int64(1_000_000)
		stale = int64(24 * 3600)
	)
	fresh := now - 3600    // an hour ago
	old := now - 5*24*3600 // five days ago

	tests := []struct {
		name           string
		eligibility    string
		pbs, veeam     bool
		freshest       int64
		wantProtection string
		wantStatus     string
	}{
		{
			name:        "protected by both, recently",
			eligibility: coverageEligible, pbs: true, veeam: true, freshest: fresh,
			wantProtection: protectionBoth, wantStatus: "recent",
		},
		{
			// The case that was simply wrong before Veeam data reached this
			// function: a guest Veeam protects reported "none".
			name:        "Veeam only still counts as protected",
			eligibility: coverageEligible, pbs: false, veeam: true, freshest: fresh,
			wantProtection: protectionVeeam, wantStatus: "recent",
		},
		{
			name:        "PBS only",
			eligibility: coverageEligible, pbs: true, veeam: false, freshest: fresh,
			wantProtection: protectionPBS, wantStatus: "recent",
		},
		{
			name:        "protected but overdue",
			eligibility: coverageEligible, pbs: true, veeam: false, freshest: old,
			wantProtection: protectionPBS, wantStatus: "stale",
		},
		{
			// "stale" would read as "there is an old backup" — there is not.
			name:        "unprotected is never stale, even with an ancient timestamp",
			eligibility: coverageEligible, pbs: false, veeam: false, freshest: old,
			wantProtection: protectionNone, wantStatus: "none",
		},
		{
			// A Veeam worker appliance with no backup is not an alarm, and
			// rendering it as one is how a coverage view teaches operators to
			// ignore it.
			name:        "a Veeam worker is not an alarm",
			eligibility: coverageVeeamWorker, pbs: false, veeam: false, freshest: 0,
			wantProtection: protectionNotEligible, wantStatus: "not_eligible",
		},
		{
			// Ineligibility wins even when a backup exists: the VBR server
			// being backed up does not make it a workload guest.
			name:        "ineligibility outranks an existing backup",
			eligibility: coverageVeeamBackupServer, pbs: true, veeam: true, freshest: fresh,
			wantProtection: protectionNotEligible, wantStatus: "not_eligible",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			protection, status := coverageVerdict(tt.eligibility, tt.pbs, tt.veeam, tt.freshest, now, stale)
			if protection != tt.wantProtection {
				t.Errorf("protection = %q, want %q", protection, tt.wantProtection)
			}
			if status != tt.wantStatus {
				t.Errorf("status = %q, want %q", status, tt.wantStatus)
			}
		})
	}
}
