package rolling

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// fakeAPT stands in for the node's apt tools, as apt 2.6.2 (PVE 8) and 3.0.4
// (PVE 9) behave on the points the script relies on: apt-get refuses --exclude
// with exit 100, dist-upgrade's outcome is FAKE_UPGRADE_RC, holds live in
// FAKE_HOLDS one per line, dpkg-query knows only FAKE_INSTALLED, and the
// apt-mark commands named in FAKE_FAIL exit 100, as they do on the dpkg lock.
// apt-get logs its calls, and the holds in force during the dist-upgrade, to
// FAKE_LOG. sleep returns at once, so the script's retries take no time.
var fakeAPT = map[string]string{
	"apt-get": `echo "apt-get $*" >> "$FAKE_LOG"
for a; do [ "$a" = --exclude ] && { echo 'E: Command line option --exclude is not understood in combination with the other options'; exit 100; }; done
case " $* " in *" dist-upgrade "*) echo "held during the upgrade: $(tr '\n' ' ' < "$FAKE_HOLDS")" >> "$FAKE_LOG"; exit "$FAKE_UPGRADE_RC";; esac`,
	"apt-mark": `cmd=$1; shift
case " $FAKE_FAIL " in *" $cmd "*) echo 'E: dpkg frontend lock was locked by another process'; exit 100 ;; esac
case $cmd in
showhold) cat "$FAKE_HOLDS" ;;
hold) for p; do grep -qxF "$p" "$FAKE_HOLDS" || echo "$p" >> "$FAKE_HOLDS"; done ;;
unhold) for p; do grep -vxF "$p" "$FAKE_HOLDS" > "$FAKE_HOLDS.new"; mv "$FAKE_HOLDS.new" "$FAKE_HOLDS"; done ;;
*) exit 100 ;;
esac`,
	"dpkg-query": `for p; do :; done
case " $FAKE_INSTALLED " in *" $p "*) echo 'install ok installed' ;; *) exit 1 ;; esac`,
	"dpkg":  `exit 0`,
	"sleep": `exit 0`,
}

// TestUpgradeCommand_HoldsTheExcludedPackagesAroundTheUpgrade runs the script an
// automated upgrade sends to a node. apt-get has no --exclude, so excluded
// packages are put on hold for the upgrade and released after it, whatever it
// did; a hold the operator set stays, and holds a cut-off run left behind are
// released before the next upgrade. A hold that cannot be made, or a list of
// holds that cannot be read, stops the run before the upgrade, and a release
// that fails fails it with the record kept. A wildcard is refused outright.
func TestUpgradeCommand_HoldsTheExcludedPackagesAroundTheUpgrade(t *testing.T) {
	t.Parallel()
	if _, err := upgradeCommand([]string{"pve-kernel-*"}, "", ""); err == nil {
		t.Error("a wildcard exclude was accepted")
	}
	for _, tt := range []struct {
		name              string
		excludes          []string
		installed         string
		operatorHolds     string // in force before the run
		leftover          string // what a cut-off run recorded
		fail              string // apt-mark commands that fail
		upgradeRC         int
		wantExit          int
		wantHeldInUpgrade string // "-": no upgrade ran
		wantHoldsAfter    string
		wantRecord        string // left in the record file
	}{
		{name: "an excluded package is held through the upgrade, then released",
			excludes: []string{"pkg-a"}, installed: "pkg-a", wantHeldInUpgrade: "pkg-a "},
		{name: "a failed upgrade still releases it",
			excludes: []string{"pkg-a"}, installed: "pkg-a", upgradeRC: 100, wantExit: 100, wantHeldInUpgrade: "pkg-a "},
		{name: "a hold the operator set stays",
			excludes: []string{"pkg-a"}, installed: "pkg-a", operatorHolds: "pkg-a\n",
			wantHeldInUpgrade: "pkg-a ", wantHoldsAfter: "pkg-a\n"},
		{name: "a cut-off run's holds are released before the upgrade",
			operatorHolds: "pkg-b\n", leftover: "pkg-b\n"},
		{name: "a package that is not installed is not held",
			excludes: []string{"pkg-z"}},
		{name: "a hold that cannot be made stops the upgrade",
			excludes: []string{"pkg-a"}, installed: "pkg-a", fail: "hold", wantExit: 1, wantHeldInUpgrade: "-"},
		{name: "a list of holds that cannot be read stops the upgrade",
			excludes: []string{"pkg-a"}, installed: "pkg-a", fail: "showhold", wantExit: 1, wantHeldInUpgrade: "-"},
		{name: "a release that fails fails the run and keeps the record",
			excludes: []string{"pkg-a"}, installed: "pkg-a", fail: "unhold", wantExit: 1,
			wantHeldInUpgrade: "pkg-a ", wantHoldsAfter: "pkg-a\n", wantRecord: "pkg-a\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for name, body := range fakeAPT {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			holds, held, log := filepath.Join(dir, "holds"), filepath.Join(dir, "held"), filepath.Join(dir, "log")
			if err := os.WriteFile(holds, []byte(tt.operatorHolds), 0o644); err != nil {
				t.Fatal(err)
			}
			if tt.leftover != "" {
				if err := os.WriteFile(held, []byte(tt.leftover), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			script, err := upgradeCommand(tt.excludes, held, filepath.Join(dir, "lock"))
			if err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command("sh", "-c", script)
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "FAKE_LOG="+log, "FAKE_HOLDS="+holds,
				"FAKE_INSTALLED="+tt.installed, "FAKE_FAIL="+tt.fail, "FAKE_UPGRADE_RC="+strconv.Itoa(tt.upgradeRC))
			out, runErr := cmd.CombinedOutput()
			exit := 0
			if exitErr := (*exec.ExitError)(nil); errors.As(runErr, &exitErr) {
				exit = exitErr.ExitCode()
			} else if runErr != nil {
				t.Fatal(runErr)
			}

			calls, _ := os.ReadFile(log)
			after, _ := os.ReadFile(holds)
			record, _ := os.ReadFile(held)
			heldInUpgrade := "-"
			if _, line, ok := strings.Cut(string(calls), "held during the upgrade: "); ok {
				heldInUpgrade, _, _ = strings.Cut(line, "\n")
			}
			if exit != tt.wantExit || heldInUpgrade != tt.wantHeldInUpgrade || string(after) != tt.wantHoldsAfter ||
				string(record) != tt.wantRecord {
				t.Errorf("exit %d, held during the upgrade %q, holds after %q, record %q\napt-get calls:\n%s\noutput:\n%s",
					exit, heldInUpgrade, after, record, calls, out)
			}
		})
	}
}

// TestStartNode_SkipsANodeWhoseOnlyUpdatesAreHeldBack: an automated upgrade
// holds the excluded packages back, so a node with nothing else pending would be
// drained (and, with Reboot after update, rebooted) to install nothing; it is
// skipped. The listing is a kernel point release as Proxmox reports one: the
// series package, and the new build it pulls in. A manual upgrade holds nothing
// back, and neither does one with another upgrade pending, so those nodes start.
func TestStartNode_SkipsANodeWhoseOnlyUpdatesAreHeldBack(t *testing.T) {
	t.Parallel()
	kernel := `{"Package":"proxmox-kernel-6.17","OldVersion":"6.17.13-12","Version":"6.17.13-13"},` +
		`{"Package":"proxmox-kernel-6.17.13-13-pve-signed","Version":"6.17.13-13"}`
	hostKey := testHostKey(t)
	for _, tt := range []struct {
		name       string
		auto       bool
		pending    string
		wantWrites []string
	}{
		{"automated", true, kernel, []string{"SkipRollingUpdateNodeAny"}},
		{"automated, with another upgrade", true, kernel + `,{"Package":"pve-manager","OldVersion":"9.0.10","Version":"9.0.11"}`,
			[]string{"SetNodePackagesJSON", "SetNodeInPlaceUpgradeAuto"}},
		{"manual", false, kernel, []string{"SetNodePackagesJSON", "SetNodeInPlaceUpgradeManual"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cluster := uuid.New()
			d := &memberDB{t: t, cluster: cluster, members: []string{"pve-01"}, accept: map[string]int64{
				"SkipRollingUpdateNodeAny": 1, "SetNodePackagesJSON": 1,
				"SetNodeInPlaceUpgradeAuto": 1, "SetNodeInPlaceUpgradeManual": 1},
				// Reachable over SSH, which an automated node is checked for before it starts.
				ssh: &sshAnswers{creds: &db.ClusterSshCredential{ClusterID: cluster, Port: 22}, address: "192.0.2.11", key: hostKey}}
			// The index refresh is refused, so there is no task to wait out.
			o, client, _ := engineOn(t, d, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/api2/json/nodes/pve-01/apt/update" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"data":[` + tt.pending + `]}`))
					return
				}
				http.Error(w, "refused", http.StatusInternalServerError)
			})
			// In place, so the node that starts stops at its first step.
			job := db.RollingUpdateJob{ID: uuid.New(), ClusterID: cluster, Status: "running",
				AutoUpgrade: tt.auto, PackageExcludes: []string{"proxmox-kernel-6.17", "proxmox-default-kernel"}}
			node := db.RollingUpdateNode{ID: uuid.New(), JobID: job.ID, NodeName: "pve-01", Step: "pending"}

			o.startNode(context.Background(), client, job, node)

			if !slices.Equal(d.writes, tt.wantWrites) {
				t.Errorf("wrote %v, want %v", d.writes, tt.wantWrites)
			}
		})
	}
}
