package guesttools

import (
	"fmt"
	"strings"
)

// Paths inside the guest. Windows\Temp rather than a user profile: the task
// runs as SYSTEM, which has no user profile to write into, and the directory
// survives the reboot that a boot-triggered install happens across.
const (
	GuestScriptPath = `C:\Windows\Temp\nexara-guest-tools-update.ps1`
	GuestResultPath = `C:\Windows\Temp\nexara-guest-tools-result.json`
	GuestTaskName   = "NexaraGuestToolsUpdate"
)

// installScriptTemplate runs the virtio-win guest tools installer from a
// mounted ISO and records the outcome.
//
// Design notes, each load-bearing:
//
//   - The CD is found by VOLUME LABEL, not by drive letter. The virtio-win ISO
//     labels itself "virtio-win-<version>" (verified: virtio-win-0.1.302), so
//     the label both locates the drive and proves it is the version we meant to
//     install. Guessing D: breaks on any guest with a second optical drive —
//     the test guest has two.
//   - Exit code 3010 is success-pending-reboot, not failure. Treating it as an
//     error would report every install that defers driver cleanup as broken,
//     which upstream's in-use driver handling makes the common case.
//   - The result file is written in a finally block, so a crash mid-install
//     still leaves something for Nexara to read. A staged update that vanishes
//     without a trace is the one outcome with no recovery path.
//   - The task deletes itself last. It is registered ONSTART and would
//     otherwise re-run the installer on every subsequent boot.
//   - The script is STRICTLY ASCII. Proxmox's agent/file-write dies with
//     "Wide character in subroutine entry" (PVE::API2::Qemu::Agent) on any
//     non-ASCII byte, so a single em dash in a comment breaks staging on every
//     guest. TestInstallScriptIsASCII guards this.
const installScriptTemplate = `# Nexara guest tools updater - generated, do not edit.
# Runs detached as SYSTEM via a scheduled task, because installing the guest
# tools restarts QEMU-GA and would kill an exec session driving it.
$ErrorActionPreference = 'Stop'
$result = [ordered]@{
  version    = '%s'
  startedAt  = (Get-Date).ToUniversalTime().ToString('o')
  exitCode   = -1
  status     = 'failed'
  rebootRequired = $false
  message    = ''
  installedVersion = ''
  finishedAt = ''
}

try {
  $label = '%s'
  $drive = Get-Volume |
    Where-Object { $_.DriveType -eq 'CD-ROM' -and $_.FileSystemLabel -eq $label } |
    Select-Object -First 1
  if (-not $drive -or -not $drive.DriveLetter) {
    throw "virtio-win ISO with label '$label' is not mounted"
  }

  $installer = "$($drive.DriveLetter):\virtio-win-guest-tools.exe"
  if (-not (Test-Path $installer)) {
    throw "installer not found at $installer"
  }

  $proc = Start-Process -FilePath $installer ` + "`" + `
    -ArgumentList '/install','/quiet','/norestart' ` + "`" + `
    -Wait -PassThru
  $result.exitCode = $proc.ExitCode

  # 0 = installed. 3010 = installed, reboot required to finish swapping drivers
  # that were in use - the normal outcome on a guest booting from viostor.
  if ($proc.ExitCode -eq 0) {
    $result.status = 'succeeded'
  } elseif ($proc.ExitCode -eq 3010) {
    # ERROR_SUCCESS_REBOOT_REQUIRED. The install worked, but Windows could not
    # finish replacing a driver that was in use and will complete at the next
    # restart. Reported as its own state, not as an error and not as plain
    # success: the operator has something left to do.
    $result.status = 'succeeded'
    $result.rebootRequired = $true
    $result.message = 'installed; a reboot is needed to finish replacing drivers that were in use'
  } elseif ($proc.ExitCode -eq 1603) {
    $result.status = 'failed'
    $result.message = 'drivers were in use (1603); a reboot before retrying usually clears this'
  } else {
    $result.status = 'failed'
    $result.message = "installer exited with $($proc.ExitCode)"
  }

  $keys = @(
    'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*',
    'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*'
  )
  $pkg = Get-ItemProperty $keys -ErrorAction SilentlyContinue |
    Where-Object { $_.DisplayName -like 'Virtio-win-guest-tools*' } |
    Sort-Object DisplayVersion -Descending | Select-Object -First 1
  if ($pkg) { $result.installedVersion = [string]$pkg.DisplayVersion }
}
catch {
  $result.status  = 'failed'
  $result.message = $_.Exception.Message
}
finally {
  # Always leave something behind: a staged update that disappears silently has
  # no recovery path on the Nexara side.
  $result.finishedAt = (Get-Date).ToUniversalTime().ToString('o')
  try {
    $result | ConvertTo-Json -Compress | Set-Content -Path '%s' -Encoding ASCII -Force
  } catch { }
  # Registered at startup - without this it reinstalls on every boot.
  try { schtasks /Delete /TN %s /F | Out-Null } catch { }
  # Clean up after ourselves. The result file is deliberately NOT removed here:
  # Nexara still has to read it, and deletes it once the outcome is recorded.
  # A running .ps1 is not locked on Windows, so this is safe from inside it.
  try { Remove-Item -LiteralPath $PSCommandPath -Force -ErrorAction SilentlyContinue } catch { }
}
`

// BuildInstallScript renders the in-guest updater for one target version.
// isoLabel is the ISO's volume label, which is "virtio-win-<iso version>".
//
// The result is guaranteed ASCII: see NonASCIIAt and the note above about
// Proxmox's file-write rejecting wide characters.
func BuildInstallScript(version, isoLabel string) string {
	return fmt.Sprintf(installScriptTemplate,
		psEscape(version), psEscape(isoLabel), GuestResultPath, GuestTaskName)
}

// NonASCIIAt returns the index of the first non-ASCII byte in s, or -1.
//
// Proxmox's agent/file-write is a Perl endpoint that dies with "Wide character
// in subroutine entry" rather than returning a useful error, so callers check
// before writing and report something an operator can act on.
func NonASCIIAt(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return i
		}
	}
	return -1
}

// ISOVolumeLabel returns the volume label the virtio-win ISO carries for a
// given version. Upstream labels the image with the suffix-less version, so
// "0.1.302-1" is labelled "virtio-win-0.1.302" (read off the real image).
// The prefix is deliberately spelled out rather than shared with
// virtiowin.ISOPrefix: the label and the ISO filename are two independent
// upstream facts that happen to coincide, and binding them would let a
// filename change silently break the in-guest search for the mounted disk.
func ISOVolumeLabel(isoVersion string) string {
	return "virtio-win-" + isoVersion
}

// psEscape makes a value safe inside a PowerShell single-quoted string, where
// the only metacharacter is the quote itself and doubling it escapes it.
//
// Versions and labels are already validated upstream, so this is defence in
// depth rather than the primary control — but it is the last point before a
// value is executed inside someone's guest.
func psEscape(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// buildRegisterTaskScript returns PowerShell that registers the updater to run
// at boot as SYSTEM.
//
// GuestScriptPath is baked in rather than taken as an argument: it has to be
// the same path Stage writes the script to, and a parameter here is a way for
// the two to drift into a task that points at nothing.
//
// Register-ScheduledTask rather than schtasks.exe, because the settings that
// matter cannot be expressed on the schtasks command line. A task created with
// "schtasks /SC ONSTART" gets DisallowStartIfOnBatteries=true, no execution
// time limit, and — the one that actually bit — no StartWhenAvailable. Its boot
// trigger fires once or not at all: observed on a live Server 2022 guest that
// rebooted with the task Ready and a valid BootTrigger, and came back with
// "Last Run Time: 11/30/1999" and "Last Result: 267011"
// (SCHED_S_TASK_HAS_NOT_RUN). The install simply never happened, silently.
//
// The settings here are each load-bearing:
//   - StartWhenAvailable: a missed boot trigger runs late instead of never.
//     This is the difference between "update applied a few minutes after boot"
//     and "update silently never applied".
//   - Delay PT1M: at boot the guest agent, storage and the CD-ROM are still
//     settling; starting an installer into that is asking for a flaky install.
//   - AllowStartIfOnBatteries / DontStopIfGoingOnBatteries: a VM reports no
//     battery, but a host passing through power state should not be able to
//     veto or kill a driver install halfway.
//   - ExecutionTimeLimit PT1H: bounds a hung installer instead of leaving the
//     task running forever and blocking the next run.
func buildRegisterTaskScript() string {
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$action    = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument '-NoProfile -NonInteractive -ExecutionPolicy Bypass -File "%s"'
$trigger   = New-ScheduledTaskTrigger -AtStartup
$trigger.Delay = 'PT1M'
$principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
$settings  = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -ExecutionTimeLimit (New-TimeSpan -Hours 1)
Register-ScheduledTask -TaskName '%s' -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
'OK'`, GuestScriptPath, GuestTaskName)
}

// buildRunTaskCommand starts the registered task immediately. The Task
// Scheduler service owns the process, so it is not a child of the guest agent
// and survives the agent restart the installer causes.
func buildRunTaskCommand() []string {
	return []string{"schtasks.exe", "/Run", "/TN", GuestTaskName}
}

// buildDeleteTaskCommand removes the scheduled task, for cancelling a staged
// update that has not run yet.
func buildDeleteTaskCommand() []string {
	return []string{"schtasks.exe", "/Delete", "/TN", GuestTaskName, "/F"}
}

// taskPresence is what the guest says about the updater's scheduled task.
type taskPresence int

const (
	// taskUnknown means the guest could not be asked — the cmdlet is missing,
	// the Task Scheduler service is down, or the query errored. Distinct from
	// taskAbsent on purpose, and the difference is load bearing: "I looked and
	// it is gone" permits a withdrawal, "I could not look" must not.
	taskUnknown taskPresence = iota
	// taskAbsent means the lookup worked and no such task is registered: either
	// it never was, or it ran and deleted itself.
	taskAbsent
	// taskReady means the task is registered and waiting for its trigger.
	taskReady
	// taskRunning means the installer is executing RIGHT NOW. Nothing may take
	// the ISO away from a guest in this state.
	taskRunning
)

func (p taskPresence) String() string {
	switch p {
	case taskAbsent:
		return "absent"
	case taskReady:
		return "ready"
	case taskRunning:
		return "running"
	default:
		return "unknown"
	}
}

// buildTaskStateScript returns PowerShell reporting the updater task's state.
//
// This is the only reliable way to tell "staged, waiting for a reboot" from
// "staged, and the installer is running this second". The database cannot tell
// them apart: a boot-triggered install spends its whole runtime in stage
// 'staged', and the result file it will eventually write does not exist yet, so
// both states look identical from outside the guest.
//
// Get-ScheduledTask rather than schtasks /Query, because its State is a plain
// enum value ("Ready", "Running") instead of a localised table that would have
// to be parsed out of console output in whatever language the guest runs.
//
// The shape below exists to keep "absent" honest, and every branch is load
// bearing. Get-ScheduledTask is CIM-backed, served by a provider the Task
// Scheduler service hosts, so a stopped service or an unhealthy CIM repository
// yields $null for a task that is sitting right there. Under SilentlyContinue
// that is indistinguishable from a task which genuinely is not there, and
// reporting both as "Absent" would make both callers pass vacuously: the
// mid-install guard could never fire, and the post-delete verification would
// confirm a removal that never happened — then record task_removed: true about
// a guest still armed to install.
//
// So absence has to be earned. The cmdlet is checked for first, and a null
// lookup then has to survive an enumeration that proves the subsystem answers:
// under 'Stop' a CIM fault there terminates into the catch, and a successful
// enumeration returning nothing at all is itself impossible on a real Windows
// install (there are always tasks under \Microsoft\Windows\), so it is read as
// a broken answer rather than an empty one. That keeps the guarantee structural
// instead of resting on the enumeration happening to throw.
func buildTaskStateScript() string {
	return `$ErrorActionPreference = 'Stop'
try {
  if (-not (Get-Command Get-ScheduledTask -ErrorAction SilentlyContinue)) {
    'Unknown'
  } else {
    $t = Get-ScheduledTask -TaskName '` + GuestTaskName + `' -ErrorAction SilentlyContinue
    if ($t) {
      [string]$t.State
    } else {
      # Only call it gone once the lookup has proved it works. Every Windows
      # install carries built-in tasks, so an enumeration that comes back empty
      # did not answer rather than answering "none".
      if (@(Get-ScheduledTask).Count -eq 0) { 'Unknown' } else { 'Absent' }
    }
  }
} catch {
  'Unknown'
}`
}

// parseTaskPresence reads what buildTaskStateScript printed.
//
// Queued counts as running: the Task Scheduler has committed to starting it and
// the installer may be underway by the time we act on the answer.
//
// Everything that is not a recognised state reads as "still there" in one form
// or another — never as absent. Empty output means the script produced nothing
// at all, which is a failure to ask rather than an answer, so it maps to
// taskUnknown; an unrecognised state name maps to taskReady. Both keep a guest
// out of the withdrawable set, which is the only safe direction: treating a
// task we could not see as gone is exactly what lets a withdrawal proceed
// against one that is still armed.
func parseTaskPresence(out string) taskPresence {
	switch strings.ToLower(strings.TrimSpace(out)) {
	case "":
		return taskUnknown
	case "unknown":
		return taskUnknown
	case "absent":
		return taskAbsent
	case "running", "queued":
		return taskRunning
	default:
		return taskReady
	}
}

// GuestUpdateResult is what the in-guest script leaves in the result file.
//
// The script also records startedAt/finishedAt. They are not decoded here
// because nothing reads them: the row's own timestamps are what the UI and the
// task history show.
type GuestUpdateResult struct {
	Version string `json:"version"`
	// RebootRequired is set when the installer returned 3010: the install
	// succeeded but a driver that was in use will only be swapped at the next
	// restart. Distinct from both success and failure, because it is the one
	// outcome that leaves the operator with an action.
	RebootRequired   bool   `json:"rebootRequired"`
	ExitCode         int    `json:"exitCode"`
	Status           string `json:"status"`
	Message          string `json:"message"`
	InstalledVersion string `json:"installedVersion"`
}

// Succeeded reports whether the install completed, including the
// reboot-required outcome.
func (r GuestUpdateResult) Succeeded() bool { return r.Status == "succeeded" }
