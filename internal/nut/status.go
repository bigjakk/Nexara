package nut

import "strings"

// State is a UPS's power state, derived from ups.status.
type State string

const (
	StateOnline     State = "online"
	StateOnBattery  State = "on_battery"
	StateLowBattery State = "low_battery"
	StateOff        State = "off"
	StateUnknown    State = "unknown"
)

// maxFlags and maxFlagLen bound a ups.status value from an untrusted server.
const (
	maxFlags   = 32
	maxFlagLen = 32
)

// Status is ups.status decoded: the derived State plus every flag, in the
// order upsd sent them, duplicates dropped.
type Status struct {
	State State
	Flags []string
}

// ParseStatus decodes ups.status. The precedence copies upsmon, which treats a
// UPS as critical only when it is on battery and low (OB LB) or being forced
// down (FSD). LB while on line power is a flag, not an emergency.
func ParseStatus(raw string) Status {
	seen := make(map[string]bool)
	var flags []string
	for _, f := range strings.Fields(raw) {
		if len(flags) == maxFlags {
			break
		}
		f = clip(f, maxFlagLen)
		if !seen[f] {
			seen[f] = true
			flags = append(flags, f)
		}
	}
	state := StateUnknown
	switch {
	case seen["FSD"], seen["OB"] && seen["LB"]:
		state = StateLowBattery
	case seen["OB"]:
		state = StateOnBattery
	case seen["OFF"]:
		state = StateOff
	case seen["OL"]:
		state = StateOnline
	}
	return Status{State: state, Flags: flags}
}

// Has reports whether the status carries flag.
func (s Status) Has(flag string) bool {
	for _, f := range s.Flags {
		if f == flag {
			return true
		}
	}
	return false
}

// OnBattery reports whether the UPS is running its load from the battery. It
// reads OB rather than the State: upsd puts FSD in front of the driver's OL
// during `upsmon -c fsd`, NUT's documented shutdown test, which is a forced
// shutdown on mains power and no outage.
func (s Status) OnBattery() bool {
	return s.Has("OB")
}
