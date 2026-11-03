package nut

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/bigjakk/nexara/internal/nut/nuttest"
)

func TestClient_Commands(t *testing.T) {
	t.Parallel()
	s, cfg := newFake(t)
	s.SetDevice("ups01", nuttest.Device{
		Vars:     map[string]string{"battery.charge.low": "10", "input.sensitivity": "normal", "ups.delay.shutdown": "20", "device.location": ""},
		Types:    map[string]string{"battery.charge.low": "RW NUMBER", "input.sensitivity": "RW ENUM STRING:10", "ups.delay.shutdown": "RW RANGE NUMBER", "device.location": "RW STRING:1024"},
		Enums:    map[string][]string{"input.sensitivity": {"low", "normal", "high"}},
		Ranges:   map[string][][2]int{"ups.delay.shutdown": {{0, 600}}},
		Commands: []string{"test.panel.start", "beeper.mute", "load.off", "test.battery.start.quick", "driver.reload"},
		CmdDescs: map[string]string{"beeper.mute": "Temporarily mute the UPS beeper"},
		Outcomes: map[string]string{"load.off": "ERR UNKNOWN", "test.battery.start.quick": "ERR FAILED", "test.panel.start": "ERR INVALID-ARGUMENT", "driver.reload": "PENDING"},
	})
	s.SetUser("nexara", nuttest.User{Password: "pw", SET: true, Instcmds: []string{"ALL"}})
	cfg.Username, cfg.Password, cfg.Commands = "nexara", "pw", true
	c := mustDial(t, cfg)
	ctx := context.Background()

	if got, err := c.ListCommands(ctx, "ups01"); err != nil || !slices.Equal(got, []string{"beeper.mute", "driver.reload", "load.off", "test.battery.start.quick", "test.panel.start"}) {
		t.Errorf("ListCommands = %q, %v", got, err)
	}
	if d, err := c.CommandDescription(ctx, "ups01", "beeper.mute"); err != nil || d != "Temporarily mute the UPS beeper" {
		t.Errorf("CommandDescription = %q, %v", d, err)
	}
	if rw, err := c.ListRW(ctx, "ups01"); err != nil || len(rw) != 4 || rw["ups.delay.shutdown"] != "20" {
		t.Errorf("ListRW = %v, %v", rw, err)
	}
	for name, want := range map[string]VarType{
		"battery.charge.low": {Writable: true, Number: true},
		"input.sensitivity":  {Writable: true, Enum: true, MaxLen: 10},
		"ups.delay.shutdown": {Writable: true, Range: true, Number: true},
		"device.location":    {Writable: true, MaxLen: 1024},
	} {
		if got, err := c.VarType(ctx, "ups01", name); err != nil || got != want {
			t.Errorf("VarType(%s) = %+v, %v; want %+v", name, got, err, want)
		}
	}
	if got, err := c.ListEnum(ctx, "ups01", "input.sensitivity"); err != nil || !slices.Equal(got, []string{"low", "normal", "high"}) {
		t.Errorf("ListEnum = %q, %v", got, err)
	}
	if got, err := c.ListRange(ctx, "ups01", "ups.delay.shutdown"); err != nil || !slices.Equal(got, []Range{{0, 600}}) {
		t.Errorf("ListRange = %v, %v", got, err)
	}

	// The driver's answers come back through tracking, as outcomes.
	if err := c.SetTracking(ctx, true); err != nil {
		t.Fatal(err)
	}
	for cmd, want := range map[string]TrackingStatus{
		"beeper.mute":              TrackingSuccess,
		"load.off":                 TrackingUnknown,
		"test.battery.start.quick": TrackingFailed,
		"test.panel.start":         TrackingInvalid,
		"driver.reload":            TrackingPending,
	} {
		id, err := c.InstantCommand(ctx, "ups01", cmd, "")
		if err != nil || id == "" {
			t.Fatalf("InstantCommand(%s) = %q, %v", cmd, id, err)
		}
		if got, err := c.Tracking(ctx, id); err != nil || got != want {
			t.Errorf("Tracking(%s) = %q, %v; want %q", cmd, got, err, want)
		}
	}
	if _, err := c.InstantCommand(ctx, "ups01", "load.off", "30"); err != nil || !slices.Contains(s.Received(), "INSTCMD ups01 load.off 30") {
		t.Errorf("InstantCommand with a parameter: %v; received %q", err, s.Received())
	}
	// Every byte quoteArg escapes arrives unchanged.
	value := `Rack 2 "B" # = \`
	id, err := c.SetVar(ctx, "ups01", "device.location", value)
	if err != nil {
		t.Fatalf("SetVar: %v", err)
	}
	if status, err := c.Tracking(ctx, id); err != nil || status != TrackingSuccess {
		t.Errorf("Tracking(SET VAR) = %q, %v", status, err)
	}
	if got, err := c.GetVar(ctx, "ups01", "device.location"); err != nil || got != value {
		t.Errorf("read back %q, %v; want %q", got, err, value)
	}
	// The longest value a driver keeps whole.
	long := strings.Repeat("a", 255)
	if _, err := c.SetVar(ctx, "ups01", "device.location", long); err != nil {
		t.Errorf("SetVar(255 bytes) = %v", err)
	} else if got, err := c.GetVar(ctx, "ups01", "device.location"); err != nil || got != long {
		t.Errorf("read back %q, %v", got, err)
	}
	if _, err := c.SetVar(ctx, "ups01", "input.sensitivity", "medium"); errCode(err) != "INVALID-VALUE" {
		t.Errorf("SetVar off the ENUM err = %v", err)
	}
	// upsd or the driver would change these without a word to us: bytes its
	// tokenizer drops, a value past the driver's 255 bytes, a line past the
	// 512 upsd forwards (255 quotes, each escaped), and a parameter upsd
	// forwards unquoted, so the driver would split it.
	before := len(s.Received())
	for _, value := range []string{"café", "a\x01b", strings.Repeat("a", 256), strings.Repeat(`"`, 255)} {
		if _, err := c.SetVar(ctx, "ups01", "device.location", value); !errors.Is(err, ErrValueChanged) {
			t.Errorf("SetVar(%.20q…) err = %v, want ErrValueChanged", value, err)
		}
	}
	for _, param := range []string{"3\x000", "30 s", "delay=30"} {
		if _, err := c.InstantCommand(ctx, "ups01", "load.off", param); !errors.Is(err, ErrValueChanged) {
			t.Errorf("InstantCommand(%q) err = %v, want ErrValueChanged", param, err)
		}
	}
	if after := s.Received()[before:]; len(after) != 0 {
		t.Errorf("refused values reached upsd: %q", after)
	}

	if err := c.SetTracking(ctx, false); err != nil {
		t.Fatal(err)
	}
	if id, err := c.InstantCommand(ctx, "ups01", "beeper.mute", ""); err != nil || id != "" {
		t.Errorf("untracked InstantCommand = %q, %v", id, err)
	}
	if _, err := c.Tracking(ctx, "00000000-0000-4000-8000-000000000001"); errCode(err) != "FEATURE-NOT-CONFIGURED" {
		t.Errorf("Tracking with tracking off err = %v", err)
	}
}
