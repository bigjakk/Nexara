package guesttools

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// TestLiveGuestAgentProbe exercises the guest-agent primitives against a real
// Windows guest. It is skipped unless NEXARA_LIVE_PROBE=1 along with
// DATABASE_URL, ENCRYPTION_KEY, PROBE_CLUSTER and PROBE_VM, so it never runs in
// CI or during a normal `make test`.
//
// It exists because the whole staged-update design rests on assumptions that
// only a real Windows guest can confirm: that guest-exec is reachable at all,
// that Proxmox's array-shaped command parameter reaches the guest as argv, and
// that reading the uninstall registry actually yields a virtio-win version.
func TestLiveGuestAgentProbe(t *testing.T) {
	ctx, client, node, vmid := liveGuest(t, 3*time.Minute)

	// 1. Is the agent answering at all?
	osInfo, err := client.GetGuestAgentOSInfo(ctx, node, vmid)
	if err != nil {
		t.Fatalf("get-osinfo: %v", err)
	}
	if osInfo == nil {
		t.Fatal("guest agent is not running")
	}
	t.Logf("osinfo: id=%q name=%q version=%q kernel=%q", osInfo.ID, osInfo.Name, osInfo.Version, osInfo.KernelRelease)

	// 2. Does guest-exec work, and does the array parameter arrive as argv?
	detectPS := `$ErrorActionPreference='SilentlyContinue'
$p = Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*',` +
		`'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*' |
     Where-Object { $_.DisplayName -match 'Virtio|QEMU guest agent' } |
     Select-Object DisplayName,DisplayVersion
$svc = Get-Service QEMU-GA
[pscustomobject]@{
  packages = @($p | ForEach-Object { "$($_.DisplayName)=$($_.DisplayVersion)" })
  service  = "$($svc.Status)"
} | ConvertTo-Json -Compress`

	out := runGuestScript(ctx, t, client, node, vmid, detectPS)
	t.Logf("installed packages / service: %s", out)

	// 3. Can we write a file and read it back? The staged flow depends on both.
	const probeFile = `C:\Windows\Temp\nexara-probe.json`
	payload := []byte(`{"nexara":"probe","ok":true}`)
	if err := client.GuestAgentFileWrite(ctx, node, vmid, probeFile, payload); err != nil {
		t.Fatalf("file-write: %v", err)
	}
	readBack, err := client.GuestAgentFileRead(ctx, node, vmid, probeFile)
	if err != nil {
		t.Fatalf("file-read: %v", err)
	}
	if string(readBack) != string(payload) {
		t.Errorf("file round-trip mismatch: wrote %q, read %q", payload, readBack)
	} else {
		t.Logf("file round-trip OK (%d bytes)", len(readBack))
	}

	// 4. Is the virtio-win ISO visible as a CD-ROM, and what is in the config?
	logCDROMs(ctx, t, client, node, vmid, "")

	// 5. Can a detached scheduled task outlive its exec session? This is the
	// mechanism the whole staged install depends on.
	detach := `schtasks /Create /TN NexaraProbe /TR "cmd /c echo probe > C:\Windows\Temp\nexara-probe-task.txt" /SC ONCE /ST 23:59 /RU SYSTEM /RL HIGHEST /F | Out-Null
schtasks /Run /TN NexaraProbe | Out-Null
Start-Sleep -Seconds 3
$r = Get-Content C:\Windows\Temp\nexara-probe-task.txt -ErrorAction SilentlyContinue
schtasks /Delete /TN NexaraProbe /F | Out-Null
if ($r) { "TASK_OK: $r" } else { "TASK_FAILED" }`
	t.Logf("detached scheduled task: %s", runGuestScript(ctx, t, client, node, vmid, detach))

	// 6. Does the generated installer script actually PARSE as PowerShell?
	// Cheaper to learn here than from a scheduled task that fails at 3am with
	// nothing but a result file saying "unexpected token".
	script := BuildInstallScript("0.1.302-1", ISOVolumeLabel("0.1.302"))
	if err := client.GuestAgentFileWrite(ctx, node, vmid, GuestScriptPath, []byte(script)); err != nil {
		t.Fatalf("write install script: %v", err)
	}
	check := `$errs = $null
[void][System.Management.Automation.Language.Parser]::ParseFile('` + GuestScriptPath + `', [ref]$null, [ref]$errs)
if ($errs -and $errs.Count -gt 0) { "PARSE_ERRORS: " + ($errs | ForEach-Object { $_.Message }) -join '; ' }
else { "PARSE_OK" }`
	parseResult := runGuestScript(ctx, t, client, node, vmid, check)
	t.Logf("install script syntax: %s", parseResult)
	if !strings.Contains(parseResult, "PARSE_OK") {
		t.Errorf("generated install script does not parse in the guest: %s", parseResult)
	}
}

// runGuestScript runs a PowerShell script in the guest and returns its stdout,
// failing the test on a non-zero exit.
func runGuestScript(ctx context.Context, t *testing.T, client *proxmox.Client, node string, vmid int, script string) string {
	t.Helper()
	status, err := client.GuestAgentExecWait(ctx, node, vmid,
		[]string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script},
		guestExecTimeout, guestExecPollEvery)
	// Both streams are worth reporting here even on success: these probes exist
	// to learn what a real Windows guest does, and a script that exits 0 while
	// warning on stderr is exactly the kind of thing they are looking for.
	if status != nil && status.ErrData != "" {
		t.Logf("stderr: %s", status.ErrData)
	}
	if err != nil {
		if status != nil {
			t.Fatalf("guest script: %v (stdout=%q)", err, status.OutData)
		}
		t.Fatalf("guest script: %v", err)
	}
	return strings.TrimSpace(status.OutData)
}

// TestLiveVerifyStaged inspects a guest that Nexara has already staged an
// update on: the ISO actually attached, the updater on disk, and the scheduled
// task registered. Same env guard as the probe above.
func TestLiveVerifyStaged(t *testing.T) {
	ctx, client, node, vmid := liveGuest(t, 2*time.Minute)
	logCDROMs(ctx, t, client, node, vmid, "")

	check := `$out = [ordered]@{}
$out.scriptPresent = Test-Path '` + GuestScriptPath + `'
$out.scriptBytes   = if (Test-Path '` + GuestScriptPath + `') { (Get-Item '` + GuestScriptPath + `').Length } else { 0 }
$task = schtasks /Query /TN ` + GuestTaskName + ` /FO LIST 2>$null
$out.taskRegistered = [bool]$task
$out.taskTrigger = (($task | Select-String 'Schedule Type|Next Run Time') -join ' | ')
$out.resultPresent = Test-Path '` + GuestResultPath + `'
$vol = Get-Volume | Where-Object { $_.DriveType -eq 'CD-ROM' -and $_.FileSystemLabel -like 'virtio-win-*' } | Select-Object -First 1
$out.isoLabel = [string]$vol.FileSystemLabel
$out.isoDrive = [string]$vol.DriveLetter
$out | ConvertTo-Json -Compress`
	t.Logf("guest state: %s", runGuestScript(ctx, t, client, node, vmid, check))
}

// liveGuest is the preamble every probe in this file shares: it skips unless
// NEXARA_LIVE_PROBE=1, then builds a Proxmox client and resolves the guest
// named by PROBE_CLUSTER/PROBE_VM. The returned context is cancelled, and the
// pool closed, when the test ends.
func liveGuest(t *testing.T, timeout time.Duration) (context.Context, *proxmox.Client, string, int) {
	t.Helper()
	if os.Getenv("NEXARA_LIVE_PROBE") != "1" {
		t.Skip("set NEXARA_LIVE_PROBE=1 to run the live guest probes")
	}
	dsn, key := os.Getenv("DATABASE_URL"), os.Getenv("ENCRYPTION_KEY")
	clusterName, vmName := os.Getenv("PROBE_CLUSTER"), os.Getenv("PROBE_VM")
	if dsn == "" || key == "" || clusterName == "" || vmName == "" {
		t.Fatal("DATABASE_URL, ENCRYPTION_KEY, PROBE_CLUSTER and PROBE_VM are all required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	q := db.New(pool)

	clusters, err := q.ListClusters(ctx)
	if err != nil {
		t.Fatalf("list clusters: %v", err)
	}
	var cluster db.Cluster
	for _, c := range clusters {
		if c.Name == clusterName {
			cluster = c
			break
		}
	}
	if cluster.Name == "" {
		t.Fatalf("cluster %q not found", clusterName)
	}
	secret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, key)
	if err != nil {
		t.Fatalf("decrypt token: %v", err)
	}
	client, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL: cluster.ApiUrl, TokenID: cluster.TokenID,
		TokenSecret: secret, TLSFingerprint: cluster.TlsFingerprint, Timeout: 60 * time.Second,
	})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	vms, err := q.ListVMsByCluster(ctx, cluster.ID)
	if err != nil {
		t.Fatalf("list vms: %v", err)
	}
	nodes, err := q.ListNodesByCluster(ctx, cluster.ID)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	nodeName := map[string]string{}
	for _, n := range nodes {
		nodeName[n.ID.String()] = n.Name
	}
	for _, vm := range vms {
		if vm.Name != vmName {
			continue
		}
		node := nodeName[vm.NodeID.String()]
		t.Logf("guest %s = vmid %d on %s (config_ostype=%s ostype=%s status=%s)",
			vm.Name, vm.Vmid, node, vm.ConfigOstype, vm.Ostype, vm.Status)
		return ctx, client, node, int(vm.Vmid)
	}
	t.Fatalf("vm %q not found in cluster %q", vmName, clusterName)
	return nil, nil, "", 0
}

// logCDROMs logs every CD-ROM device in the guest's config, prefixed by label.
// A config Nexara cannot read is a failure, not a missing log line: two callers
// are asserting the ISO is attached, and the reboot probe wants to know if the
// guest stopped answering. Everything already logged survives the failure.
func logCDROMs(ctx context.Context, t *testing.T, client *proxmox.Client, node string, vmid int, label string) {
	t.Helper()
	cfg, err := client.GetVMConfig(ctx, node, vmid)
	if err != nil {
		t.Fatalf("%sget config: %v", label, err)
	}
	for k, v := range cfg {
		if s, ok := v.(string); ok && strings.Contains(s, "media=cdrom") {
			t.Logf("%scdrom %s = %s", label, k, s)
		}
	}
}

// TestLiveDetectScript runs the REAL detection script (not a hand-written
// approximation) against a live guest, so a change to it is validated where it
// actually executes rather than only against a Go string.
func TestLiveDetectScript(t *testing.T) {
	ctx, client, node, vmid := liveGuest(t, 2*time.Minute)
	out := runGuestScript(ctx, t, client, node, vmid, detectScript)
	t.Logf("raw: %s", out)

	d, err := ParseDetection([]byte(out))
	if err != nil {
		t.Fatalf("ParseDetection: %v", err)
	}
	t.Logf("parsed: installed=%q tools=%q drivers=%q agent=%q running=%v",
		d.InstalledVersion(), d.Tools, d.Drivers, d.Agent, d.AgentRunning())
	if d.InstalledVersion() == "" {
		t.Error("no virtio-win version detected on a guest known to have one")
	}
}

// TestLiveRebootAndObserve reboots a staged guest and watches the whole update
// happen: whether it installs, what it leaves behind, and whether the guest
// comes back intact. Destructive by design; same env guard as the other probes.
func TestLiveRebootAndObserve(t *testing.T) {
	if os.Getenv("NEXARA_LIVE_PROBE") != "1" || os.Getenv("NEXARA_ALLOW_REBOOT") != "1" {
		t.Skip("set NEXARA_LIVE_PROBE=1 and NEXARA_ALLOW_REBOOT=1 to reboot the guest")
	}
	ctx, client, node, vmid := liveGuest(t, 25*time.Minute)

	inspect := `$out = [ordered]@{}
$out.scriptPresent = Test-Path '` + GuestScriptPath + `'
$out.resultPresent = Test-Path '` + GuestResultPath + `'
$out.taskRegistered = [bool](schtasks /Query /TN ` + GuestTaskName + ` /FO LIST 2>$null)
$k = @('HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*','HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*')
$p = Get-ItemProperty $k -EA SilentlyContinue | Where-Object { $_.DisplayName -like 'Virtio-win-guest-tools*' } | Select-Object -First 1
$out.installed = [string]$p.DisplayVersion
$out.gaService = [string](Get-Service QEMU-GA -EA SilentlyContinue).Status
$out.nics = @(Get-NetAdapter -EA SilentlyContinue | Where-Object Status -eq 'Up' | ForEach-Object { $_.Name }) -join ','
$out.ips = @(Get-NetIPAddress -AddressFamily IPv4 -EA SilentlyContinue | Where-Object { $_.IPAddress -notlike '169.254*' -and $_.IPAddress -ne '127.0.0.1' } | ForEach-Object { $_.IPAddress }) -join ','
$out.uptimeMin = [int]((Get-Date) - (Get-CimInstance Win32_OperatingSystem).LastBootUpTime).TotalMinutes
$out | ConvertTo-Json -Compress`

	t.Logf("BEFORE: %s", runGuestScript(ctx, t, client, node, vmid, inspect))
	logCDROMs(ctx, t, client, node, vmid, "BEFORE ")

	upid, err := client.RebootVM(ctx, node, vmid)
	if err != nil {
		t.Fatalf("reboot: %v", err)
	}
	t.Logf("reboot dispatched: %s", upid)

	// Wait for the agent to answer again. It goes away twice: once for the
	// reboot, and again when the installer restarts QEMU-GA.
	deadline := time.Now().Add(20 * time.Minute)
	var lastRaw string
	settled := 0
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Second)
		info, err := client.GetGuestAgentOSInfo(ctx, node, vmid)
		if err != nil || info == nil {
			t.Logf("  [%s] agent not answering yet", time.Now().Format("15:04:05"))
			continue
		}
		raw, err := runScript(ctx, client, node, vmid, inspect)
		if err != nil {
			t.Logf("  [%s] agent up, exec not ready: %v", time.Now().Format("15:04:05"), err)
			continue
		}
		if raw != lastRaw {
			t.Logf("  [%s] %s", time.Now().Format("15:04:05"), raw)
			lastRaw = raw
		}
		if strings.Contains(raw, `"resultPresent":true`) {
			settled++
			if settled >= 2 { // one extra poll so a mid-write file isn't read
				break
			}
		}
	}

	if raw, err := client.GuestAgentFileRead(ctx, node, vmid, GuestResultPath); err == nil {
		t.Logf("RESULT FILE: %s", string(raw))
	} else {
		t.Logf("RESULT FILE unreadable: %v", err)
	}
	t.Logf("AFTER: %s", lastRaw)
	logCDROMs(ctx, t, client, node, vmid, "AFTER ")
}

// TestLiveTaskDiagnostic reports whether the scheduled task has run, its last
// result, and whether an installer process is currently alive.
func TestLiveTaskDiagnostic(t *testing.T) {
	ctx, client, node, vmid := liveGuest(t, 4*time.Minute)

	q := `$t = schtasks /Query /TN ` + GuestTaskName + ` /FO LIST /V 2>$null | Out-String
$fields = ($t -split [Environment]::NewLine) | Where-Object { $_ -match 'Last Run Time|Last Result|Status:' }
$ev = @()
try {
  $ev = Get-WinEvent -FilterHashtable @{LogName='Microsoft-Windows-TaskScheduler/Operational'} -MaxEvents 40 -EA SilentlyContinue |
        Where-Object { $_.Message -match 'Nexara' } |
        ForEach-Object { "$($_.TimeCreated.ToString('HH:mm:ss')) id=$($_.Id) $(($_.Message -split [Environment]::NewLine)[0])" }
} catch {}
[pscustomobject]@{
  task      = ($fields -join ' || ').Trim()
  events    = @($ev) -join ' ;; '
  bootTime  = (Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToString('HH:mm:ss')
  now       = (Get-Date).ToString('HH:mm:ss')
  schedSvc  = [string](Get-Service Schedule -EA SilentlyContinue).Status
  installed = [string](Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*','HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*' -EA SilentlyContinue | Where-Object { $_.DisplayName -like 'Virtio-win-guest-tools*' } | Select-Object -First 1).DisplayVersion
} | ConvertTo-Json -Compress`
	t.Logf("DIAG: %s", runGuestScript(ctx, t, client, node, vmid, q))
}

// TestLiveTaskXML dumps the registered task definition so a trigger that did
// not fire can be diagnosed from what Windows actually stored.
func TestLiveTaskXML(t *testing.T) {
	ctx, client, node, vmid := liveGuest(t, 3*time.Minute)
	// No backticks: one would terminate Go's raw string literal.
	q := `$raw = (schtasks /Query /TN ` + GuestTaskName + ` /XML 2>$null | Out-String)
$i = $raw.IndexOf('<Task')
$x = [xml]$raw.Substring($i)
$bat = @(Get-CimInstance Win32_Battery -EA SilentlyContinue).Count
[pscustomobject]@{
  settings   = [string]$x.Task.Settings.InnerXml
  triggers   = [string]$x.Task.Triggers.InnerXml
  batteries  = $bat
  osCaption  = [string](Get-CimInstance Win32_OperatingSystem).Caption
} | ConvertTo-Json -Compress`
	t.Logf("TASK SETTINGS: %s", runGuestScript(ctx, t, client, node, vmid, q))
}

// TestLiveCleanGuest removes leftovers from probing so a subsequent run starts
// from a clean guest. Not part of the feature; a tidy-up for the test harness.
func TestLiveCleanGuest(t *testing.T) {
	ctx, client, node, vmid := liveGuest(t, 3*time.Minute)
	q := `Get-ChildItem 'C:\Windows\Temp\nexara-*' -EA SilentlyContinue | Remove-Item -Force -EA SilentlyContinue
schtasks /Delete /TN ` + GuestTaskName + ` /F 2>$null | Out-Null
schtasks /Delete /TN NexaraProbe /F 2>$null | Out-Null
@(Get-ChildItem 'C:\Windows\Temp\nexara-*' -EA SilentlyContinue).Count`
	t.Logf("remaining nexara-* files after cleanup: %s", runGuestScript(ctx, t, client, node, vmid, q))
}
