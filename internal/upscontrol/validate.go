package upscontrol

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/bigjakk/nexara/internal/nut"
)

// Confirm is what a run asks of the person before it goes ahead.
type Confirm string

const (
	ConfirmNone  Confirm = ""
	ConfirmPlain Confirm = "plain"
	// ConfirmTyped: the UPS's name typed, re-checked on the server.
	ConfirmTyped Confirm = "typed"
)

// CommandConfirm is the confirmation a command's tier needs.
func CommandConfirm(cmd string) Confirm {
	switch CommandTier(cmd) {
	case TierEveryday:
		return ConfirmNone
	case TierCutsPower:
		return ConfirmTyped
	}
	return ConfirmPlain
}

// harmlessVars only describe the UPS, or set the driver's logging or the
// beeper (whose commands are everyday). Setting any other variable can change
// how the UPS supplies power or when upsmon shuts machines down:
// battery.charge.low at 0 means no low-battery warning before the battery is
// flat. So every other one, a variable this list does not know included, is
// gated as cutting power is.
var harmlessVars = map[string]bool{
	"device.contact": true, "device.description": true, "device.location": true, "ups.id": true,
	"battery.date": true, "battery.mfr.date": true, "ups.beeper.status": true, "driver.debug": true,
}

// VarAffectsPower says whether setting a variable is gated as cutting power.
func VarAffectsPower(name string) bool { return !harmlessVars[name] }

// VarConfirm is the confirmation setting a variable needs (P7): the flag that
// lets the driver cut the UPS's power, the typed name; any other that can
// change the power the UPS supplies, a plain one.
func VarConfirm(name string) Confirm {
	switch {
	case name == "driver.flag.allow_killpower":
		return ConfirmTyped
	case VarAffectsPower(name):
		return ConfirmPlain
	}
	return ConfirmNone
}

// ErrNotEditable means Nexara does not offer the variable for editing.
var ErrNotEditable = errors.New("this variable cannot be set from Nexara")

// Editable says whether Nexara offers a variable for editing, and if not why.
func (v Var) Editable() (ok bool, reason string) {
	switch {
	case v.Type == "":
		return false, "Its type has not been read yet"
	case v.Type == TypeEnum && len(v.Enum) < 2:
		// snmp-ups declares battery.date an ENUM of the one value "dummy":
		// upsd would refuse a real date, and a one-item choice sets nothing.
		return false, "upsd lists fewer than two values for it"
	}
	return true, ""
}

// Validate checks a value for the variable name as upsd and the driver will
// (P7), so a refusal comes back from the API rather than as a failed run.
func (v Var) Validate(name, value string) error {
	if ok, why := v.Editable(); !ok {
		return fmt.Errorf("%w: %s", ErrNotEditable, why)
	}
	if err := nut.CheckSetValue(name, value); err != nil {
		return err
	}
	if v.MaxLen > 0 && len(value) > v.MaxLen {
		return fmt.Errorf("the value is longer than the %d characters upsd allows", v.MaxLen)
	}
	switch v.Type {
	case TypeEnum:
		if !slices.Contains(v.Enum, value) {
			return fmt.Errorf("the value is not one upsd lists")
		}
	case TypeRange:
		n := atoi(value)
		if !slices.ContainsFunc(v.Ranges, func(r [2]int) bool { return n >= r[0] && n <= r[1] }) {
			return fmt.Errorf("the value is outside the ranges upsd lists")
		}
	}
	return nil
}

// atoi reads a number as C's atoi does, which is how upsd checks a RANGE:
// leading digits after optional space and sign, 0 for none, so "12.7" is 12.
func atoi(v string) int {
	v = strings.TrimLeft(v, " \t\n\v\f\r")
	end := 0
	if end < len(v) && (v[end] == '-' || v[end] == '+') {
		end++
	}
	for end < len(v) && v[end] >= '0' && v[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(v[:end])
	return n
}
