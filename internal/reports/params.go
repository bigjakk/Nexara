package reports

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/bigjakk/nexara/internal/backupcoverage"
)

// Params are the per-report options a schedule or an on-demand request may
// set. They ride in report_schedules.parameters and report_runs.parameters
// as JSON; unknown keys are ignored so a newer frontend can send options an
// older binary does not know, and every field has a documented default so an
// empty object means "the report as it has always been".
type Params struct {
	// StaleAfterHours is how old a guest's newest restore point may be before
	// the backup compliance report calls it stale. Default 24, matching the
	// coverage page.
	StaleAfterHours int `json:"stale_after_hours,omitempty"`
	// TopN is how many guests the "top consumers" tables list. Default 10.
	TopN int `json:"top_n,omitempty"`
	// SnapshotWarnDays is the age past which the snapshot inventory report
	// flags a snapshot as forgotten. Default 7.
	SnapshotWarnDays int `json:"snapshot_warn_days,omitempty"`
	// Sections switches optional sections off by name: {"runs": false}.
	// Absent or true means shown.
	Sections map[string]bool `json:"sections,omitempty"`
}

// Limits on the numeric parameters, shared by the API validation and the
// generator so a value that validates also renders.
const (
	MaxStaleAfterHours  = 24 * 365
	MaxTopN             = 100
	MaxSnapshotWarnDays = 3650
)

// DefaultParams is what an empty parameters object means.
func DefaultParams() Params {
	return Params{
		StaleAfterHours:  int(backupcoverage.DefaultStaleAfter / time.Hour),
		TopN:             10,
		SnapshotWarnDays: 7,
	}
}

// ParseParams decodes a parameters JSON object, fills in defaults, and
// rejects values outside the documented ranges. nil or empty raw JSON is the
// default set.
func ParseParams(raw json.RawMessage) (Params, error) {
	p := DefaultParams()
	if len(raw) == 0 || string(raw) == "null" {
		return p, nil
	}
	var in Params
	if err := json.Unmarshal(raw, &in); err != nil {
		return p, fmt.Errorf("parameters must be a JSON object: %w", err)
	}
	if in.StaleAfterHours != 0 {
		if in.StaleAfterHours < 1 || in.StaleAfterHours > MaxStaleAfterHours {
			return p, fmt.Errorf("stale_after_hours must be between 1 and %d", MaxStaleAfterHours)
		}
		p.StaleAfterHours = in.StaleAfterHours
	}
	if in.TopN != 0 {
		if in.TopN < 1 || in.TopN > MaxTopN {
			return p, fmt.Errorf("top_n must be between 1 and %d", MaxTopN)
		}
		p.TopN = in.TopN
	}
	if in.SnapshotWarnDays != 0 {
		if in.SnapshotWarnDays < 1 || in.SnapshotWarnDays > MaxSnapshotWarnDays {
			return p, fmt.Errorf("snapshot_warn_days must be between 1 and %d", MaxSnapshotWarnDays)
		}
		p.SnapshotWarnDays = in.SnapshotWarnDays
	}
	p.Sections = in.Sections
	return p, nil
}

// NormalizeParams validates raw parameters and returns both the effective
// Params (defaults filled) and the canonical JSON to store: only the keys the
// caller set, unknown keys dropped, so a stored row never carries something
// the validator did not see. Empty input stores "{}".
func NormalizeParams(raw json.RawMessage) (Params, json.RawMessage, error) {
	p, err := ParseParams(raw)
	if err != nil {
		return p, nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return p, json.RawMessage(`{}`), nil
	}
	var in Params
	if err := json.Unmarshal(raw, &in); err != nil {
		return p, nil, fmt.Errorf("parameters must be a JSON object: %w", err)
	}
	out, err := json.Marshal(in)
	if err != nil {
		return p, nil, fmt.Errorf("encode parameters: %w", err)
	}
	return p, out, nil
}

// WithDefaults fills every zero field with its documented default, field by
// field, so a caller that set only top_n still gets the 24 h stale rule.
func (p Params) WithDefaults() Params {
	d := DefaultParams()
	if p.StaleAfterHours <= 0 {
		p.StaleAfterHours = d.StaleAfterHours
	}
	if p.TopN <= 0 {
		p.TopN = d.TopN
	}
	if p.SnapshotWarnDays <= 0 {
		p.SnapshotWarnDays = d.SnapshotWarnDays
	}
	return p
}

// SectionEnabled reports whether an optional section is switched on.
func (p Params) SectionEnabled(name string) bool {
	if p.Sections == nil {
		return true
	}
	on, ok := p.Sections[name]
	return !ok || on
}

// StaleAfter is StaleAfterHours as a duration.
func (p Params) StaleAfter() time.Duration {
	return time.Duration(p.StaleAfterHours) * time.Hour
}
