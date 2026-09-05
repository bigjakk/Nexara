package guesttools

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bigjakk/nexara/internal/proxmox"
)

func TestIsWindowsOSType(t *testing.T) {
	tests := []struct {
		name         string
		configOstype string
		ostype       string
		want         bool
	}{
		{"agent reports mswindows", "", "mswindows", true},
		{"agent wins over a non-windows config", "l26", "mswindows", true},
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
// message that says nothing about the cause. Learned the hard way. guest-exec
// carries the constraint too, so every script Nexara sends a guest is here.
func TestGeneratedScriptsAreASCII(t *testing.T) {
	for _, tt := range []struct{ name, script string }{
		{"install", BuildInstallScript("0.1.302-1", ISOVolumeLabel("0.1.302"))},
		{"register task", buildRegisterTaskScript()},
		{"task state", buildTaskStateScript()},
		{"detect", detectScript},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if i := NonASCIIAt(tt.script); i != -1 {
				start := max(0, i-40)
				t.Errorf("script has a non-ASCII byte at offset %d, which Proxmox file-write rejects: ...%q...",
					i, tt.script[start:min(len(tt.script), i+40)])
			}
		})
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
	reg := buildRegisterTaskScript()
	// StartWhenAvailable is the one that matters most: without it a missed boot
	// trigger means the update never happens rather than happening late. A live
	// Server 2022 guest reproduced exactly that with a schtasks-created task.
	for _, want := range []string{
		"-AtStartup", "StartWhenAvailable", "AllowStartIfOnBatteries",
		"DontStopIfGoingOnBatteries", "ExecutionTimeLimit",
		"-UserId 'SYSTEM'", "-RunLevel Highest",
		// The path and the task name are two %s args on one Sprintf, so pin
		// which slot each lands in. Swapped, the task would be named after the
		// script path and -File would point at the task name, and every other
		// assertion here would still pass.
		`-File "` + GuestScriptPath + `"`,
		"-TaskName '" + GuestTaskName + "'",
	} {
		if !strings.Contains(reg, want) {
			t.Errorf("register script missing %q", want)
		}
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
//
// The restore retry sweep leans on the same split from the other side: it looks
// for terminal rows that still name a drive, so terminal and "still holds a
// prior_cdrom_key" have to be able to coexist — and an in-flight row must stay
// out of the sweep, or a restore would race a running install for the same
// drive. That is only half of it: this mirrors the sweep's SQL predicate rather
// than being it, and cannot catch a caller that clears prior_cdrom_key on the
// way to a terminal stage, since the row then never reaches the sweep at all.
// TestNoPathClaimsARestoreItDidNotPerform covers that half.
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

// Changing the target has to change what installs, including on guests that
// were already staged for the old one — a staged install fires at the guest's
// next boot, which can be weeks out.
func TestSupersededStaging(t *testing.T) {
	tests := []struct {
		name   string
		stage  string
		staged string
		target string
		want   bool
	}{
		// The case this exists for: 0.1.302 went out, broke something, and the
		// operator pinned back to 0.1.285. Every guest staged for 0.1.302 is
		// still armed with it until this withdraws them.
		{"target moved back", "staged", "0.1.302-1", "0.1.285-1", true},
		{"target moved forward", "staged", "0.1.302-1", "0.1.310-1", true},
		{"target moved sideways", "staged", "0.1.302-1", "0.1.290-1", true},
		{"target unchanged", "staged", "0.1.302-1", "0.1.302-1", false},

		// A release suffix is a real difference: 0.1.302-2 is not 0.1.302-1.
		{"release suffix differs", "staged", "0.1.302-1", "0.1.302-2", true},

		// Withdrawing means deleting the in-guest task and taking the ISO back
		// off. Neither means anything once the installer is running, and
		// 'staging' is stage() still writing this very row.
		{"running is past withdrawal", "running", "0.1.302-1", "0.1.285-1", false},
		{"staging is mid-flight", "staging", "0.1.302-1", "0.1.285-1", false},
		{"idle has nothing staged", "idle", "0.1.302-1", "0.1.285-1", false},
		{"succeeded already ran", "succeeded", "0.1.302-1", "0.1.285-1", false},
		{"failed already ran", "failed", "0.1.302-1", "0.1.285-1", false},

		// "We could not resolve a target" must never read as "the target
		// changed" — that would unstage the fleet the moment a pin named a
		// version the catalog had not caught up with.
		{"unresolvable target", "staged", "0.1.302-1", "", false},
		{"nothing staged", "staged", "", "0.1.285-1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := supersededStaging(tt.stage, tt.staged, tt.target); got != tt.want {
				t.Errorf("supersededStaging(%q, %q, %q) = %v, want %v",
					tt.stage, tt.staged, tt.target, got, tt.want)
			}
		})
	}
}

// The three in-flight stages wait on entirely different things, and giving them
// one ceiling is what let a row stranded inside stage() sit for thirty days holding a
// concurrency slot.
func TestStaleCeilingFor(t *testing.T) {
	tests := []struct {
		name  string
		stage string
		want  time.Duration
	}{
		// stage() writes this on its way past and overwrites it seconds later, so
		// a row still wearing it is stuck, not working.
		{"staging is bounded work", "staging", stagingUpdateMaxAge},
		// Waiting on a human to reboot. A monthly reboot cycle is not a fault.
		{"staged waits for a reboot", "staged", stagedUpdateMaxAge},
		// The installer was actually started; it takes minutes.
		{"running was started", "running", runningUpdateMaxAge},

		// Nothing else reaches expireIfStale today, but a stage added later
		// must fail safe by waiting too long rather than tearing down an
		// update somebody is relying on.
		{"unknown stage falls back to the longest", "somethingelse", stagedUpdateMaxAge},
		{"empty stage falls back to the longest", "", stagedUpdateMaxAge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := staleCeilingFor(tt.stage)
			if got != tt.want {
				t.Errorf("staleCeilingFor(%q) = %v, want %v", tt.stage, got, tt.want)
			}
			// The reason lands in last_error and is the only explanation an
			// operator gets for a row that vanished out from under them.
			if reason == "" {
				t.Errorf("staleCeilingFor(%q) returned no reason", tt.stage)
			}
			if !strings.Contains(reason, tt.want.String()) {
				t.Errorf("staleCeilingFor(%q) reason %q does not name its own ceiling %v",
					tt.stage, reason, tt.want)
			}
		})
	}

	// Ordering is the property that matters, not the exact values: staging is
	// bounded work, running is an install in progress, staged is waiting on a
	// person. Inverting any of these silently restores the original bug.
	if stagingUpdateMaxAge >= runningUpdateMaxAge || runningUpdateMaxAge >= stagedUpdateMaxAge {
		t.Errorf("ceilings out of order: staging=%v running=%v staged=%v",
			stagingUpdateMaxAge, runningUpdateMaxAge, stagedUpdateMaxAge)
	}
	// A live stage() must never be expired out from under itself.
	//
	// Asserted against stageWorstCase rather than a hand-rolled estimate,
	// because the estimate is what went wrong the first time: budgeting guest
	// execs alone, at the uncached 60s client timeout, produced a ceiling three
	// times smaller than a legitimate stage() can take. stageWorstCase is built
	// from proxmox.CachedClientTimeout, so raising that timeout now fails this
	// test instead of silently shrinking the margin.
	if stagingUpdateMaxAge <= stageWorstCase {
		t.Errorf("staging ceiling %v is inside stage()'s own worst case %v — a live stage() could be expired mid-flight",
			stagingUpdateMaxAge, stageWorstCase)
	}
}

// A boot-triggered install spends its whole runtime in stage 'staged' and has
// not written its result file yet, so the database cannot tell "waiting for a
// reboot" from "installing right now". Asking the guest is the only way, and
// misreading the answer means pulling the ISO out from under a live driver
// install.
func TestParseTaskPresence(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want taskPresence
	}{
		{"running", "Running", taskRunning},
		// The scheduler has committed to starting it; by the time we act on
		// this answer the installer may already be going.
		{"queued counts as running", "Queued", taskRunning},
		{"ready", "Ready", taskReady},
		{"disabled is still registered", "Disabled", taskReady},
		{"absent", "Absent", taskAbsent},

		// PowerShell casing is not guaranteed across Windows versions.
		{"lower case", "running", taskRunning},
		{"surrounding space", "  Running  ", taskRunning},

		// The distinction the whole probe exists for. "I could not look" must
		// never reduce to "it is gone": a Task Scheduler service or CIM
		// provider that will not answer yields no task object for one that is
		// sitting right there, and reading that as absent would let a
		// withdrawal proceed against a task still armed — and would let the
		// post-delete verification pass without the delete having worked.
		{"cannot look", "Unknown", taskUnknown},
		{"no output at all", "", taskUnknown},
		{"only whitespace", "  \r\n", taskUnknown},

		// An unrecognised state name is still a state name, so the task exists.
		{"unrecognised state is not absence", "SomethingNew", taskReady},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseTaskPresence(tt.out); got != tt.want {
				t.Errorf("parseTaskPresence(%q) = %v, want %v", tt.out, got, tt.want)
			}
		})
	}
}

func TestTaskStateScriptProvesItCouldLook(t *testing.T) {
	script := buildTaskStateScript()

	if !strings.Contains(script, GuestTaskName) {
		t.Error("task state script does not name the updater task")
	}

	// A guest that cannot answer must say so rather than report an absence.
	// Without these two, a guest whose Task Scheduler or CIM provider is sick
	// reports "Absent" for a task sitting right there, and both callers pass
	// vacuously: the mid-install guard can never fire and the post-delete
	// verification confirms a removal that never happened.
	if !strings.Contains(script, "Get-Command Get-ScheduledTask") {
		t.Error("script does not check the cmdlet exists before relying on it")
	}
	// A null lookup has to survive an enumeration before it counts as absence,
	// and an empty enumeration is a broken answer, not "no tasks" — every
	// Windows install carries built-in ones.
	if !strings.Contains(script, "@(Get-ScheduledTask).Count -eq 0") {
		t.Error("script calls the task absent without proving the lookup works")
	}
	if strings.Count(script, "'Unknown'") < 2 {
		t.Error("script does not report Unknown for both failure routes")
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
			config:    map[string]any{"ide3": "nas:iso/virtio-win-0.1.285.iso,media=cdrom"},
			want:      cdromEject,
		},
		{
			name:      "drive already holding the target ISO is still emptied",
			placement: cdromPlacement{Key: "ide3", AlreadyAttached: true},
			config:    map[string]any{"ide3": "nas:iso/virtio-win-0.1.302.iso,media=cdrom"},
			want:      cdromEject,
		},
		{
			name:      "an operator's own media is put back verbatim",
			placement: cdromPlacement{Key: "ide2"},
			config:    map[string]any{"ide2": "nas:iso/Win2022.iso,media=cdrom"},
			want:      "nas:iso/Win2022.iso,media=cdrom",
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
// CdromRestored tells FinishGuestToolsUpdate whether the borrowed drive has
// actually been put back; a true CLEARS prior_cdrom_key, and
// ListGuestToolsPendingCDROMRestore selects on that column being non-empty. So a
// path that claims a restore it never performed does not just mislabel the row —
// it deletes the record the retry sweep needs, and the virtio-win ISO stays
// mounted on the guest for good, re-pinned against pruning.
//
// markFailed did exactly that. It is reached from a staging error and from an
// expiry, neither of which touches the guest, and it passed true.
//
// A literal true is therefore always wrong here: the two paths that really do
// restore (finishCancel, reconcileOne) pass a variable tracking what happened.
//
// Parsed rather than string-matched, and that distinction is not pedantry — the
// first version of this test searched for "CdromRestored: true" with one space,
// while gofmt aligns the two multi-line literals with two, so it silently
// covered only one of the three call sites. It passed a reintroduction check
// purely because the bug was reinstated at the site that happened to match.
// Following the AST precedent in internal/api/handlers/tracktask_guard_test.go.
func TestNoPathClaimsARestoreItDidNotPerform(t *testing.T) {
	// Every non-test file in the package, so a fourth call site added later is
	// covered on the day it appears rather than the day someone remembers.
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || key.Name != "CdromRestored" {
				return true
			}
			checked++
			if lit, ok := kv.Value.(*ast.Ident); ok && lit.Name == "true" {
				t.Errorf("%s:%d passes CdromRestored: true — that clears prior_cdrom_key, so it is "+
					"only correct where restoreCDROM actually ran, and those paths pass a variable",
					name, fset.Position(kv.Pos()).Line)
			}
			return true
		})
	}

	// A guard that stops finding its subject stops guarding. If the field is
	// renamed or the calls move, this fails rather than passing vacuously.
	if checked == 0 {
		t.Error("found no CdromRestored fields to check — has the field been renamed?")
	}
}

// planCDROM decides which of an operator's drives Nexara is allowed to touch,
// so every branch of that decision is pinned here.
func TestPlanCDROM(t *testing.T) {
	const isoVolid = "local:iso/virtio-win-0.1.302.iso"

	tests := []struct {
		name    string
		config  proxmox.VMConfig
		want    cdromPlacement
		wantErr error
	}{
		{
			name:   "reuses the drive already holding the target ISO",
			config: proxmox.VMConfig{"ide2": isoVolid + ",media=cdrom"},
			want:   cdromPlacement{Key: "ide2", AlreadyAttached: true},
		},
		{
			name:   "reuses a drive holding a superseded virtio-win ISO",
			config: proxmox.VMConfig{"ide2": "local:iso/virtio-win-0.1.266.iso,media=cdrom"},
			want:   cdromPlacement{Key: "ide2"},
		},
		{
			name:   "recognises a virtio-win ISO on a storage with no content directory",
			config: proxmox.VMConfig{"ide2": "local:virtio-win-0.1.266.iso,media=cdrom"},
			want:   cdromPlacement{Key: "ide2"},
		},
		{
			// Ranging a Go map picks an arbitrary drive on a guest with two,
			// so the choice has to be pinned rather than observed once.
			name: "picks the same drive every time when two hold virtio-win ISOs",
			config: proxmox.VMConfig{
				"sata0": "local:iso/virtio-win-0.1.266.iso,media=cdrom",
				"ide2":  "local:iso/virtio-win-0.1.240.iso,media=cdrom",
			},
			want: cdromPlacement{Key: "ide2"},
		},
		{
			name: "prefers the virtio-win drive over a free slot",
			config: proxmox.VMConfig{
				"sata0": "local:iso/virtio-win-0.1.266.iso,media=cdrom",
				"scsi0": "local-lvm:vm-100-disk-0,size=32G",
			},
			want: cdromPlacement{Key: "sata0"},
		},
		{
			name:   "never borrows a drive holding the operator's own media",
			config: proxmox.VMConfig{"ide2": "local:iso/Win2022.iso,media=cdrom"},
			want:   cdromPlacement{Key: "ide0", Eject: true},
		},
		{
			// Two separate things are pinned here. Intended: an empty drive is
			// not mistaken for a virtio-win one. Merely current: ide2 is then
			// passed over by the free-slot loop, which tests key presence, so
			// an empty and perfectly usable drive goes unused and ide0 is added
			// beside it. A change that makes ide2 reusable should update this
			// expectation — that is the follow-up landing, not a regression.
			name:   "an empty drive is not mistaken for a virtio-win one, and is not reused either",
			config: proxmox.VMConfig{"ide2": "none,media=cdrom"},
			want:   cdromPlacement{Key: "ide0", Eject: true},
		},
		{
			// CDROMDrives orders by config key, while cdromSlots is ordered by
			// Proxmox's own UI preference and puts ide2 first. The two disagree
			// only on ide0 vs ide2, and this is that case: the key order wins,
			// so the target already mounted at ide2 is passed over in favour of
			// a media change on ide0. Recorded current behaviour — the pre-
			// refactor code sorted keys the same way — not a claim that it is
			// the better answer.
			name: "the key order decides, even when another drive already holds the target",
			config: proxmox.VMConfig{
				"ide0": "local:iso/virtio-win-0.1.240.iso,media=cdrom",
				"ide2": isoVolid + ",media=cdrom",
			},
			want: cdromPlacement{Key: "ide0"},
		},
		{
			name:   "takes a free slot and marks it for ejection",
			config: proxmox.VMConfig{"scsi0": "local-lvm:vm-100-disk-0,size=32G"},
			want:   cdromPlacement{Key: "ide2", Eject: true},
		},
		{
			name:    "no drive and no free slot is a refusal, not a guess",
			config:  occupiedCDROMSlots(),
			wantErr: ErrNoCDROMSlot,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := planCDROM(tt.config, isoVolid)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("planCDROM() error = %v, want %v", err, tt.wantErr)
				}
				if got != (cdromPlacement{}) {
					t.Errorf("planCDROM() returned %+v alongside an error, want the zero placement", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("planCDROM() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("planCDROM() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// occupiedCDROMSlots fills every slot planCDROM may claim with a disk, so the
// only honest answer is ErrNoCDROMSlot.
func occupiedCDROMSlots() proxmox.VMConfig {
	config := proxmox.VMConfig{}
	for _, slot := range cdromSlots {
		config[slot] = "local-lvm:vm-100-disk-0,size=32G"
	}
	return config
}

// planCDROM's drive choice has to be the same on every call, not merely sorted
// on the call a test happens to make: Go randomises map iteration, so an
// unsorted implementation returns the expected drive often enough to pass a
// single-call assertion by luck. Only repetition can tell the two apart.
func TestPlanCDROMIsRepeatable(t *testing.T) {
	config := proxmox.VMConfig{
		"ide0":  "local:iso/virtio-win-0.1.240.iso,media=cdrom",
		"ide1":  "local:iso/virtio-win-0.1.248.iso,media=cdrom",
		"ide2":  "local:iso/virtio-win-0.1.266.iso,media=cdrom",
		"sata0": "local:iso/virtio-win-0.1.271.iso,media=cdrom",
		"sata1": "local:iso/virtio-win-0.1.285.iso,media=cdrom",
	}
	first, err := planCDROM(config, "local:iso/virtio-win-0.1.302.iso")
	if err != nil {
		t.Fatalf("planCDROM() unexpected error: %v", err)
	}
	for i := range 100 {
		got, err := planCDROM(config, "local:iso/virtio-win-0.1.302.iso")
		if err != nil {
			t.Fatalf("planCDROM() call %d unexpected error: %v", i, err)
		}
		if got != first {
			t.Fatalf("planCDROM() call %d = %+v, first call = %+v; the choice is not stable", i, got, first)
		}
	}
}
