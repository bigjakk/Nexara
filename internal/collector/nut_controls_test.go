package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/nut/nuttest"
	"github.com/bigjakk/nexara/internal/upscontrol"
)

// A UPS's commands and writable variables are listed every ten minutes, and
// their descriptions and types read over the following polls within each
// poll's budget. A list upsd refuses waits for the next listing rather than
// being asked every poll, an ENUM whose values upsd refused stays unread
// rather than stored empty, and a new driver, seen after a restart too, reads
// everything again.
func TestNutPoller_ListsCommandsAndWritableVariables(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	device := func(outlets int, version, beeperText string) nuttest.Device {
		cmds := append(make([]string, 0, 1+outlets), "beeper.mute")
		for i := range outlets { // with beeper.mute, past one poll's budget
			cmds = append(cmds, fmt.Sprintf("outlet.%02d.load.off", i))
		}
		return nuttest.Device{
			Vars: map[string]string{"ups.status": "OL", "driver.name": "usbhid-ups", "driver.version": version,
				"input.sensitivity": "normal", "battery.charge.low": "10"},
			Types:    map[string]string{"input.sensitivity": "RW ENUM STRING:10", "battery.charge.low": "RW STRING:10"},
			Enums:    map[string][]string{"input.sensitivity": {"low", "normal", "high"}},
			Commands: cmds,
			CmdDescs: map[string]string{"beeper.mute": beeperText},
		}
	}
	fake := nuttest.NewServer(t)
	fake.SetDevice("ups01", device(20, "2.8.1", "Temporarily mute the UPS beeper"))
	host, port := fake.Addr()
	q := newFakeNutQueries(db.NutServer{ID: uuid.New(), Name: "nut01", Host: host, Port: int32(port), TlsMode: "off", Enabled: true})
	p := NewNutPoller(q, "", NutPollerConfig{Interval: time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	clock := time.Date(2026, 11, 3, 3, 12, 0, 0, time.UTC)
	p.now = func() time.Time { return clock }
	st := p.loadServerState(ctx, q.server)
	defer func() { st.closeConn() }()
	tick := func(d time.Duration) { clock = clock.Add(d); p.poll(ctx, st) }
	stored := func() ([]upscontrol.Command, map[string]upscontrol.Var) {
		dev := q.device("ups01")
		var cmds []upscontrol.Command
		var vars map[string]upscontrol.Var
		if json.Unmarshal(dev.Commands, &cmds) != nil || json.Unmarshal(dev.RwVars, &vars) != nil || !dev.CommandsListedAt.Valid {
			t.Fatalf("stored lists: %s %s listed %v", dev.Commands, dev.RwVars, dev.CommandsListedAt)
		}
		return cmds, vars
	}
	sent := func(line string) int {
		return len(slices.DeleteFunc(fake.Received(), func(l string) bool { return l != line }))
	}

	fake.Fail("LIST CMD ups01", "DATA-STALE")
	tick(0)
	tick(time.Second)
	if n := sent("LIST CMD ups01"); n != 1 || q.device("ups01").CommandsListedAt.Valid {
		t.Fatalf("a refused LIST CMD was sent %d times in two polls; want once, and nothing stored", n)
	}

	fake.Fail("LIST CMD ups01", "")
	fake.Fail("LIST ENUM ups01 input.sensitivity", "VAR-NOT-SUPPORTED")
	tick(nutControlsInterval)
	cmds, vars := stored()
	if len(cmds) != 21 || cmds[0] != (upscontrol.Command{Name: "beeper.mute", Description: "Temporarily mute the UPS beeper"}) {
		t.Fatalf("listed: %d commands, first %+v", len(cmds), cmds[0])
	}
	if len(vars) != 2 || vars["input.sensitivity"].Type != "" {
		t.Fatalf("listed: variables %+v; want both, not typed yet", vars)
	}
	tick(time.Second)
	if _, vars = stored(); vars["input.sensitivity"].Type != "" || vars["battery.charge.low"].Type != upscontrol.TypeString {
		t.Fatalf("with LIST ENUM refused: %+v; want input.sensitivity left unread", vars)
	}

	fake.Fail("LIST ENUM ups01 input.sensitivity", "")
	fake.SetDevice("ups01", device(19, "2.8.1", "Temporarily mute the UPS beeper"))
	tick(nutControlsInterval)
	cmds, vars = stored()
	if got := vars["input.sensitivity"]; len(cmds) != 20 || got.Type != upscontrol.TypeEnum || !slices.Equal(got.Enum, []string{"low", "normal", "high"}) {
		t.Fatalf("relisted: %d commands, input.sensitivity %+v; want 20 and its ENUM read", len(cmds), got)
	}

	restart := func() { st.closeConn(); st = p.loadServerState(ctx, q.server); tick(time.Second) }
	restart()
	if n := sent("GET CMDDESC ups01 beeper.mute"); n != 1 {
		t.Errorf("after a restart under the same driver: beeper.mute described %d times; want once", n)
	}
	fake.SetDevice("ups01", device(19, "2.8.2", "Mute the beeper"))
	restart()
	if cmds, _ := stored(); cmds[0].Description != "Mute the beeper" {
		t.Errorf("after a restart under a new driver: %+v; want the description read again", cmds[0])
	}
}
