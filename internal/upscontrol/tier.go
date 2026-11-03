package upscontrol

import "path"

// Tier is how much a command can disturb what a UPS powers (P6).
type Tier string

const (
	// TierEveryday: the beeper, the front-panel test, and stopping something.
	TierEveryday Tier = "everyday"
	// TierTest: a battery test or calibration, which drains the battery.
	TierTest Tier = "test"
	// TierPowersOn switches power on.
	TierPowersOn Tier = "powers_on"
	// TierDriver reloads the NUT driver.
	TierDriver Tier = "driver"
	// TierCutsPower switches power off, now or later, and is what any command
	// not recognised here counts as.
	TierCutsPower Tier = "cuts_power"
)

// tierRules are matched in order; the first match wins. A * spans dots too
// ("*.stop" matches test.battery.stop), as path.Match has no "/" to stop at.
var tierRules = []struct {
	pattern string
	tier    Tier
}{
	// Every stop first: cancelling a shutdown in progress must never need the
	// typed name that starting one does.
	{"*.stop", TierEveryday},
	{"beeper.*", TierEveryday},
	{"test.panel.*", TierEveryday},
	{"test.battery.*", TierTest},
	{"calibrate.*", TierTest},
	{"load.on*", TierPowersOn},
	{"outlet.*.load.on*", TierPowersOn},
	{"driver.reload*", TierDriver},
}

// IsStop says whether a command stops something, such as a shutdown in
// progress: one runs even when every watch is taken, unwatched.
func IsStop(cmd string) bool {
	ok, _ := path.Match("*.stop", cmd)
	return ok
}

// CommandTier classifies an instant command by its NUT name.
func CommandTier(cmd string) Tier {
	for _, r := range tierRules {
		if ok, _ := path.Match(r.pattern, cmd); ok {
			return r.tier
		}
	}
	return TierCutsPower
}
