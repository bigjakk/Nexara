package guesttools

import (
	"strings"
	"testing"
)

func TestIsWindowsOSType(t *testing.T) {
	tests := []struct {
		name         string
		configOstype string
		ostype       string
		want         bool
	}{
		{"agent reports mswindows", "", "mswindows", true},
		{"agent wins even when config is blank", "", "mswindows", true},
		{"win11", "win11", "win11", true},
		{"win10", "win10", "", true},
		{"server via w2k prefix", "w2k19", "", true},
		{"wxp", "wxp", "", true},
		{"wvista", "wvista", "", true},
		{"case insensitive", "WIN11", "", true},

		{"linux", "l26", "linux", false},
		{"solaris", "solaris", "", false},
		{"other", "other", "", false},
		{"empty", "", "", false},
		// "win" is a prefix of nothing else in Proxmox's ostype vocabulary, but
		// guard against a substring match rather than a prefix match.
		{"not a prefix match", "darwin", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsWindowsOSType(tt.configOstype, tt.ostype); got != tt.want {
				t.Errorf("IsWindowsOSType(%q, %q) = %v, want %v", tt.configOstype, tt.ostype, got, tt.want)
			}
		})
	}
}

// The two sides arrive in different shapes: the catalog carries upstream's
// directory version ("0.1.302-1") and Windows reports the installer's
// DisplayVersion ("0.1.285"). Comparing them raw would misreport every guest.
func TestNeedsUpdate(t *testing.T) {
	tests := []struct {
		name      string
		installed string
		target    string
		want      bool
	}{
		{"behind, mixed forms", "0.1.285", "0.1.302-1", true},
		{"at target, mixed forms", "0.1.302", "0.1.302-1", false},
		{"ahead of target", "0.1.302", "0.1.285-1", false},
		{"numeric not lexical", "0.1.96", "0.1.302-1", true},

		// Unknown is not the same as out of date. Treating a failed detection
		// as "needs update" would push an installer at every guest we simply
		// could not read.
		{"unknown installed", "", "0.1.302-1", false},
		{"unknown target", "0.1.285", "", false},
		{"both unknown", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NeedsUpdate(tt.installed, tt.target); got != tt.want {
				t.Errorf("NeedsUpdate(%q, %q) = %v, want %v", tt.installed, tt.target, got, tt.want)
			}
		})
	}
}

func TestUpToDate(t *testing.T) {
	// UpToDate is not simply !NeedsUpdate: both are false when a version is
	// unknown, which is what keeps "unknown" reportable as its own state.
	if UpToDate("", "0.1.302-1") {
		t.Error("an unknown installed version must not read as up to date")
	}
	if !UpToDate("0.1.302", "0.1.302-1") {
		t.Error("matching versions should be up to date across the suffix difference")
	}
	if !UpToDate("0.1.310", "0.1.302-1") {
		t.Error("a newer installed version should count as up to date")
	}
	if UpToDate("0.1.285", "0.1.302-1") {
		t.Error("an older installed version is not up to date")
	}
}

func TestParseDetection(t *testing.T) {
	// The exact payload a live Server 2022 guest returns.
	const live = `{"tools":"0.1.285","drivers":"0.1.285","agent":"110.0.2","service":"Running"}`
	d, err := ParseDetection([]byte(live))
	if err != nil {
		t.Fatalf("ParseDetection: %v", err)
	}
	if d.InstalledVersion() != "0.1.285" {
		t.Errorf("InstalledVersion = %q, want 0.1.285", d.InstalledVersion())
	}
	if d.Agent != "110.0.2" {
		t.Errorf("Agent = %q", d.Agent)
	}
	if !d.AgentRunning() {
		t.Error("AgentRunning = false, want true")
	}
	if !d.Installed() {
		t.Error("Installed = false, want true")
	}
}

func TestParseDetectionEdgeCases(t *testing.T) {
	t.Run("nulls when nothing is installed", func(t *testing.T) {
		d, err := ParseDetection([]byte(`{"tools":null,"drivers":null,"agent":null,"service":"Stopped"}`))
		if err != nil {
			t.Fatalf("ParseDetection: %v", err)
		}
		if d.Installed() {
			t.Error("Installed = true for a guest with no virtio-win")
		}
		if d.AgentRunning() {
			t.Error("AgentRunning = true for a stopped service")
		}
	})

	t.Run("empty output is a state, not an error", func(t *testing.T) {
		d, err := ParseDetection(nil)
		if err != nil {
			t.Fatalf("empty output should not error: %v", err)
		}
		if d.Installed() {
			t.Error("empty output should not report an install")
		}
	})

	t.Run("drivers alone are used when the bundle is absent", func(t *testing.T) {
		d, err := ParseDetection([]byte(`{"tools":null,"drivers":"0.1.271","agent":"108.0.0","service":"Running"}`))
		if err != nil {
			t.Fatalf("ParseDetection: %v", err)
		}
		if d.InstalledVersion() != "0.1.271" {
			t.Errorf("InstalledVersion = %q, want the driver package version", d.InstalledVersion())
		}
	})

	t.Run("garbage errors", func(t *testing.T) {
		if _, err := ParseDetection([]byte("Access is denied.")); err == nil {
			t.Error("expected an error for non-JSON output")
		}
	})
}

// Proxmox's agent/file-write is a Perl endpoint that dies with "Wide character
// in subroutine entry" on any non-ASCII byte. A single em dash in a comment is
// enough to break staging on every guest, and it fails at write time with a
// message that says nothing about the cause. Learned the hard way.
func TestInstallScriptIsASCII(t *testing.T) {
	script := BuildInstallScript("0.1.302-1", ISOVolumeLabel("0.1.302"))
	if i := NonASCIIAt(script); i != -1 {
		start := max(0, i-40)
		t.Errorf("generated script has a non-ASCII byte at offset %d, which Proxmox file-write rejects: ...%q...",
			i, script[start:min(len(script), i+40)])
	}
}

func TestBuildInstallScript(t *testing.T) {
	script := BuildInstallScript("0.1.302-1", ISOVolumeLabel("0.1.302"))

	// The label is how the script finds the drive AND confirms the version.
	if !strings.Contains(script, `$label = 'virtio-win-0.1.302'`) {
		t.Error("script does not pin the ISO volume label")
	}
	// Silent, and explicitly not rebooting the guest on its own.
	if !strings.Contains(script, `'/install','/quiet','/norestart'`) {
		t.Error("script does not run the installer silently without rebooting")
	}
	// 3010 is success-pending-reboot; treating it as failure would report the
	// common case on a viostor boot disk as broken.
	if !strings.Contains(script, "3010") {
		t.Error("script does not handle the reboot-required exit code")
	}
	// ONSTART re-fires every boot, so the task must remove itself.
	if !strings.Contains(script, "schtasks /Delete /TN "+GuestTaskName) {
		t.Error("script does not delete its own scheduled task")
	}
	if !strings.Contains(script, GuestResultPath) {
		t.Error("script does not write the result file")
	}
}

func TestISOVolumeLabel(t *testing.T) {
	// Read off the real image: virtio-win-0.1.302.iso is labelled
	// "virtio-win-0.1.302" — the suffix-less form, not the directory version.
	if got, want := ISOVolumeLabel("0.1.302"), "virtio-win-0.1.302"; got != want {
		t.Errorf("ISOVolumeLabel = %q, want %q", got, want)
	}
}

func TestPSEscape(t *testing.T) {
	if got := psEscape("0.1.302'; whoami; '"); strings.Count(got, "''") == 0 {
		t.Errorf("psEscape did not double the quote: %q", got)
	}
}

func TestTaskCommandsAreArgv(t *testing.T) {
	// Proxmox takes the command as a repeated parameter, one element per
	// argument. A single shell-style string would be treated as a program name.
	reg := buildRegisterTaskScript(GuestScriptPath)
	// StartWhenAvailable is the one that matters most: without it a missed boot
	// trigger means the update never happens rather than happening late. A live
	// Server 2022 guest reproduced exactly that with a schtasks-created task.
	for _, want := range []string{
		"-AtStartup", "StartWhenAvailable", "AllowStartIfOnBatteries",
		"DontStopIfGoingOnBatteries", "ExecutionTimeLimit",
		"-UserId 'SYSTEM'", "-RunLevel Highest", GuestScriptPath,
	} {
		if !strings.Contains(reg, want) {
			t.Errorf("register script missing %q", want)
		}
	}
	if i := NonASCIIAt(reg); i != -1 {
		t.Errorf("register script has a non-ASCII byte at %d", i)
	}
	if got := buildRunTaskCommand(); got[1] != "/Run" {
		t.Errorf("run command = %v", got)
	}
	if got := buildDeleteTaskCommand(); got[1] != "/Delete" {
		t.Errorf("delete command = %v", got)
	}
}

func TestGuestUpdateResultSucceeded(t *testing.T) {
	if !(GuestUpdateResult{Status: "succeeded"}).Succeeded() {
		t.Error("succeeded status should report success")
	}
	if (GuestUpdateResult{Status: "failed"}).Succeeded() {
		t.Error("failed status should not report success")
	}
}

// A guest that updates successfully must remain eligible for the NEXT release.
// Blocking on any non-idle stage silently retired a guest after its first
// successful update, and parked a guest permanently on one transient failure.
func TestIsInFlightStage(t *testing.T) {
	inFlight := []string{"staging", "staged", "running"}
	terminal := []string{"idle", "succeeded", "failed", ""}

	for _, stage := range inFlight {
		if !isInFlightStage(stage) {
			t.Errorf("stage %q should count as in flight", stage)
		}
	}
	for _, stage := range terminal {
		if isInFlightStage(stage) {
			t.Errorf("stage %q must NOT block a future update", stage)
		}
	}
}

// The detection script must order versions numerically. A guest carrying a
// superseded uninstall entry can hold both 0.1.96 and 0.1.302, and a lexical
// sort picks 0.1.96 because '9' > '3'.
func TestDetectScriptSortsNumerically(t *testing.T) {
	if strings.Contains(detectScript, "Sort-Object DisplayVersion") {
		t.Error("detection sorts DisplayVersion as a string; 0.1.96 would beat 0.1.302")
	}
	if !strings.Contains(detectScript, "TryParse") {
		t.Error("detection does not parse version components numerically")
	}
	if i := NonASCIIAt(detectScript); i != -1 {
		t.Errorf("detection script has a non-ASCII byte at %d; guest-exec carries it too", i)
	}
}

// The updater must not litter the guest. It removes its own scheduled task and
// its own script; the result file is Nexara's to delete once the outcome is
// recorded, so it must NOT be removed from inside the script.
func TestInstallScriptCleansUpAfterItself(t *testing.T) {
	script := BuildInstallScript("0.1.302-1", ISOVolumeLabel("0.1.302"))

	if !strings.Contains(script, "schtasks /Delete /TN "+GuestTaskName) {
		t.Error("script does not remove its own scheduled task")
	}
	if !strings.Contains(script, "Remove-Item -LiteralPath $PSCommandPath") {
		t.Error("script does not remove itself")
	}
	if strings.Contains(script, "Remove-Item -LiteralPath '"+GuestResultPath) {
		t.Error("script deletes the result file; Nexara would never see the outcome")
	}
}

// 3010 is ERROR_SUCCESS_REBOOT_REQUIRED: the install worked, but a driver that
// was in use only swaps at the next restart. It must be reported as its own
// state — folding it into the error field renders a red failure under a success
// badge, and folding it into plain success hides an outstanding action.
func TestRebootRequiredIsItsOwnOutcome(t *testing.T) {
	script := BuildInstallScript("0.1.302-1", ISOVolumeLabel("0.1.302"))
	if !strings.Contains(script, "$result.rebootRequired = $true") {
		t.Error("script does not flag the 3010 case explicitly")
	}

	rebooting := GuestUpdateResult{Status: "succeeded", RebootRequired: true, ExitCode: 3010}
	if !rebooting.Succeeded() {
		t.Error("3010 must still count as a successful install")
	}
	if !rebooting.RebootRequired {
		t.Error("3010 must be distinguishable from a clean install")
	}

	clean := GuestUpdateResult{Status: "succeeded", ExitCode: 0}
	if clean.RebootRequired {
		t.Error("a clean install must not ask for a reboot")
	}
}

// The virtio-win ISO must not stay mounted after an update. Beyond tidiness, a
// mounted ISO is exactly what prune's in-use check protects, so leaving it
// would make every updated guest permanently block cleanup of the release it
// just moved off.
func TestRestoreActionAlwaysRemovesTheISO(t *testing.T) {
	tests := []struct {
		name      string
		placement cdromPlacement
		config    map[string]any
		want      string
	}{
		{
			name:      "drive Nexara added is removed entirely",
			placement: cdromPlacement{Key: "sata0", Eject: true},
			config:    map[string]any{},
			want:      cdromRemove,
		},
		{
			name:      "drive that already held a virtio-win ISO is emptied, not removed",
			placement: cdromPlacement{Key: "ide3"},
			config:    map[string]any{"ide3": "synology:iso/virtio-win-0.1.285.iso,media=cdrom"},
			want:      cdromEject,
		},
		{
			name:      "drive already holding the target ISO is still emptied",
			placement: cdromPlacement{Key: "ide3", AlreadyAttached: true},
			config:    map[string]any{"ide3": "synology:iso/virtio-win-0.1.302.iso,media=cdrom"},
			want:      cdromEject,
		},
		{
			name:      "an operator's own media is put back verbatim",
			placement: cdromPlacement{Key: "ide2"},
			config:    map[string]any{"ide2": "synology:iso/Win2022.iso,media=cdrom"},
			want:      "synology:iso/Win2022.iso,media=cdrom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := restoreActionFor(tt.placement, tt.config)
			if got != tt.want {
				t.Errorf("restoreActionFor = %q, want %q", got, tt.want)
			}
		})
	}

	// The sentinels must never be mistaken for a volume id, which always
	// contains a colon.
	for _, sentinel := range []string{cdromEject, cdromRemove} {
		if strings.Contains(sentinel, ":") {
			t.Errorf("sentinel %q could collide with a Proxmox volume id", sentinel)
		}
	}
}

// A finished update must not forget which drive to put back until it HAS been
// put back. Clearing unconditionally stranded the virtio-win ISO on the guest
// forever after one transient Proxmox error: the row goes terminal, the
// in-flight sweep never revisits it, and the record is gone.
func TestPendingRestoreIsRetryable(t *testing.T) {
	// The retry sweep looks for terminal rows that still name a drive, so the
	// two conditions have to be able to coexist.
	for _, stage := range []string{"succeeded", "failed", "idle"} {
		if isInFlightStage(stage) {
			t.Errorf("stage %q must be terminal for the retry sweep to see it", stage)
		}
	}
	// And an in-flight row must NOT be swept, or a restore would race a running
	// install for the same drive.
	for _, stage := range []string{"staging", "staged", "running"} {
		if !isInFlightStage(stage) {
			t.Errorf("stage %q must be excluded from the retry sweep", stage)
		}
	}
}
