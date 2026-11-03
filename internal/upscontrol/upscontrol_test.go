package upscontrol

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bigjakk/nexara/internal/nut"
)

// 2.8 says STRING:n or NUMBER after ENUM and RANGE, so the list wins.
func TestFromType(t *testing.T) {
	t.Parallel()
	enum := []string{"yes", "no"}
	ranges := []nut.Range{{Min: 0, Max: 600}}
	for _, tc := range []struct {
		name string
		t    nut.VarType
		want Var
	}{
		{"RW ENUM NUMBER", nut.VarType{Writable: true, Enum: true, Number: true}, Var{Type: TypeEnum, Enum: enum}},
		{"RW ENUM STRING:3", nut.VarType{Writable: true, Enum: true, MaxLen: 3}, Var{Type: TypeEnum, MaxLen: 3, Enum: enum}},
		{"RW RANGE NUMBER", nut.VarType{Writable: true, Range: true, Number: true}, Var{Type: TypeRange, Ranges: [][2]int{{0, 600}}}},
		{"RW STRING:10", nut.VarType{Writable: true, MaxLen: 10}, Var{Type: TypeString, MaxLen: 10}},
		{"RW NUMBER", nut.VarType{Writable: true, Number: true}, Var{Type: TypeNumber}},
	} {
		if got := FromType(tc.t, enum, ranges); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: FromType = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// Rows from the operator's two real command lists (§15), and the rules' edges.
func TestCommandTier(t *testing.T) {
	t.Parallel()
	for cmd, want := range map[string]Tier{
		"beeper.mute":             TierEveryday,
		"test.panel.start":        TierEveryday,
		"shutdown.stop":           TierEveryday,
		"test.battery.stop":       TierEveryday,
		"test.battery.start.deep": TierTest,
		"calibrate.start":         TierTest,
		"load.on.delay":           TierPowersOn,
		"outlet.1.load.on":        TierPowersOn,
		"driver.reload-or-exit":   TierDriver,
		"shutdown.reboot":         TierCutsPower,
		"shutdown.default":        TierCutsPower,
		"outlet.1.load.off.delay": TierCutsPower,
		"load.off":                TierCutsPower,
		"driver.killpower":        TierCutsPower,
		"bypass.start":            TierCutsPower,
		"test.failure.start":      TierCutsPower, // not recognised
	} {
		if got := CommandTier(cmd); got != want {
			t.Errorf("CommandTier(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestVar_Validate(t *testing.T) {
	t.Parallel()
	enum := Var{Type: TypeEnum, Enum: []string{"low", "normal", "high"}}
	for _, tc := range []struct {
		name  string
		v     Var
		value string
		ok    bool
	}{
		{"not read yet", Var{}, "1", false},
		{"one-value enum", Var{Type: TypeEnum, Enum: []string{"dummy"}}, "dummy", false},
		{"enum value", enum, "normal", true},
		{"off the enum", enum, "Normal", false},
		{"within STRING:10", Var{Type: TypeString, MaxLen: 10}, "0123456789", true},
		{"past STRING:10", Var{Type: TypeString, MaxLen: 10}, "01234567890", false},
		{"range, as atoi reads it", Var{Type: TypeRange, Ranges: [][2]int{{10, 600}}}, "12.7", true},
		{"outside the range", Var{Type: TypeRange, Ranges: [][2]int{{10, 600}}}, "601", false},
		{"number upsd does not check", Var{Type: TypeNumber}, "abc", true},
		{"byte upsd drops", Var{Type: TypeNumber}, "café", false},
		{"past a driver's 255 bytes", Var{Type: TypeNumber}, strings.Repeat("1", 256), false},
	} {
		err := tc.v.Validate("x", tc.value)
		if (err == nil) != tc.ok {
			t.Errorf("%s: Validate(%q) = %v", tc.name, tc.value, err)
		}
	}
	if err := (Var{Type: TypeNumber}).Validate("x", "café"); !errors.Is(err, nut.ErrValueChanged) {
		t.Errorf("a byte upsd drops: %v, want nut.ErrValueChanged", err)
	}
}

// Only what describes the UPS is harmless; anything else, unknown included,
// is gated as cutting power, and the flag that lets the driver cut it takes
// the typed name.
func TestVarConfirm(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]Confirm{
		"driver.flag.allow_killpower": ConfirmTyped,
		"battery.charge.low":          ConfirmPlain,
		"ups.delay.shutdown":          ConfirmPlain,
		"input.transfer.low":          ConfirmPlain,
		"device.location":             ConfirmNone,
		"ups.beeper.status":           ConfirmNone,
	} {
		if got := VarConfirm(name); got != want {
			t.Errorf("VarConfirm(%q) = %q, want %q", name, got, want)
		}
		if affects := VarAffectsPower(name); affects != (want != ConfirmNone) {
			t.Errorf("VarAffectsPower(%q) = %v", name, affects)
		}
	}
}
