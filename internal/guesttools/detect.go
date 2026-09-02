// Package guesttools tracks the virtio-win drivers and QEMU guest agent
// installed inside Windows guests, and stages updates to them.
//
// The central constraint, which shapes everything here: installing the
// virtio-win guest tools restarts the QEMU-GA service — the very channel the
// install would be driven through. Running the installer directly via
// guest-exec kills its own session mid-install and loses the PID with it.
// Compounding that, upstream's driver upgrade fails with error 1603 when the
// drivers are in use (a viostor boot disk always is), and the fix defers INF
// cleanup to a reboot.
//
// So nothing here runs the installer through guest-exec. A script is written
// into the guest and registered as a scheduled task running as SYSTEM, which
// executes detached — at the next boot, or immediately on request. Nexara then
// reads a result file the script leaves behind. That is both the workaround for
// the agent restart and the natural implementation of "update on reboot".
package guesttools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bigjakk/nexara/internal/virtiowin"
)

// IsWindowsOSType reports whether a guest is Windows.
//
// configOstype is the authoritative Proxmox setting (win11, w2k19, wxp, ...);
// ostype is what the guest agent reported, which is "mswindows" when present.
// Either is sufficient. This mirrors the rule the frontend has always used in
// os-classify.ts — kept in step deliberately, so the fleet list and the UI
// never disagree about which guests are in scope.
func IsWindowsOSType(configOstype, ostype string) bool {
	if strings.EqualFold(ostype, "mswindows") {
		return true
	}
	v := strings.ToLower(strings.TrimSpace(configOstype))
	switch {
	case v == "":
		return false
	case strings.HasPrefix(v, "win"), strings.HasPrefix(v, "w2k"):
		return true
	case v == "wxp", v == "wvista":
		return true
	default:
		return false
	}
}

// detectScript reads the installed virtio-win packages out of the uninstall
// registry and reports the guest agent service state.
//
// Both registry views are searched: the installer is 64-bit on x64 but the
// WOW6432Node view is where a 32-bit install lands, and a guest that was
// upgraded across architectures can have traces in either.
//
// Verified against a live Server 2022 guest, which answers:
//
//	{"tools":"0.1.285","drivers":"0.1.285","agent":"110.0.2","service":"Running"}
const detectScript = `$ErrorActionPreference = 'SilentlyContinue'
$keys = @(
  'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*',
  'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*'
)
$all = Get-ItemProperty $keys
function Ver($pattern) {
  # Sort NUMERICALLY, not lexically. A guest that kept a superseded uninstall
  # entry can hold both "0.1.96" and "0.1.302", and a string sort puts 0.1.96
  # first because '9' > '3' - reporting a version years older than reality.
  ($all | Where-Object { $_.DisplayName -like $pattern -and $_.DisplayVersion } |
     Sort-Object @{Expression = {
        $parts = ([string]$_.DisplayVersion -split '[.-]')
        $n = 0
        foreach ($p in $parts) { $v = 0; [void][int]::TryParse($p, [ref]$v); $n = $n * 100000 + $v }
        $n
     }} -Descending | Select-Object -First 1).DisplayVersion
}
[pscustomobject]@{
  tools   = Ver 'Virtio-win-guest-tools*'
  drivers = Ver 'Virtio-win-driver-installer*'
  agent   = Ver 'QEMU guest agent*'
  service = [string](Get-Service QEMU-GA).Status
} | ConvertTo-Json -Compress`

// Detection is what a guest reports about its own guest tools.
type Detection struct {
	Tools   string `json:"tools"`
	Drivers string `json:"drivers"`
	Agent   string `json:"agent"`
	Service string `json:"service"`
}

// InstalledVersion is the version to compare against a target.
//
// The guest-tools bundle is the authority: it is what the updater installs, and
// what its DisplayVersion reports ("0.1.285") is exactly the form used in ISO
// filenames. The driver-installer package is the fallback for guests where only
// the drivers were ever installed, without the bundle.
func (d Detection) InstalledVersion() string {
	if d.Tools != "" {
		return d.Tools
	}
	return d.Drivers
}

// AgentRunning reports whether the QEMU-GA service is running.
func (d Detection) AgentRunning() bool {
	return strings.EqualFold(strings.TrimSpace(d.Service), "Running")
}

// Installed reports whether any virtio-win package was found at all.
func (d Detection) Installed() bool { return d.InstalledVersion() != "" }

// ParseDetection reads the JSON the detect script writes to stdout.
//
// PowerShell's ConvertTo-Json emits `null` for an absent registry value and,
// for a single object, no enclosing array — both handled here. Empty output is
// not an error: it means the script ran but found nothing, which is a real and
// reportable state ("Windows guest with no virtio-win installed").
func ParseDetection(out []byte) (Detection, error) {
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return Detection{}, nil
	}
	var d Detection
	if err := json.Unmarshal([]byte(trimmed), &d); err != nil {
		return Detection{}, fmt.Errorf("guesttools: unparseable detection output %q: %w", truncate(trimmed, 200), err)
	}
	return d, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// NeedsUpdate reports whether an installed version is behind a target.
//
// Both sides are normalised to the suffix-less form before comparing, because
// they arrive in different shapes: the catalog carries upstream's directory
// version ("0.1.302-1") while Windows reports the installer's DisplayVersion
// ("0.1.285"). Comparing those raw would make every guest look out of date.
//
// An unknown installed version is NOT treated as needing an update. "We could
// not read the registry" and "this guest is out of date" are different
// statements, and acting on the first as if it were the second would push an
// installer at every guest whose detection merely failed.
func NeedsUpdate(installed, target string) bool {
	if installed == "" || target == "" {
		return false
	}
	_, installedISO := virtiowin.SplitVersion(installed)
	_, targetISO := virtiowin.SplitVersion(target)
	return virtiowin.Compare(installedISO, targetISO) < 0
}

// UpToDate reports whether a guest is at or ahead of the target. Distinct from
// !NeedsUpdate, which is also true when the installed version is unknown.
func UpToDate(installed, target string) bool {
	if installed == "" || target == "" {
		return false
	}
	_, installedISO := virtiowin.SplitVersion(installed)
	_, targetISO := virtiowin.SplitVersion(target)
	return virtiowin.Compare(installedISO, targetISO) >= 0
}
