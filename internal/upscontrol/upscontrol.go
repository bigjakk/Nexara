// Package upscontrol is what Nexara knows about a UPS's instant commands and
// writable variables: the lists the poller stores for each UPS and the API
// checks a request against. The poller and the API share it.
package upscontrol

import "github.com/bigjakk/nexara/internal/nut"

// Command is one instant command, as ups_devices.commands stores it.
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Var types, from GET TYPE.
const (
	TypeString = "string"
	TypeNumber = "number"
	TypeEnum   = "enum"
	TypeRange  = "range"
)

// Var is one writable variable's type, as ups_devices.rw_vars stores it. Type
// is "" until the poller has read it, and nothing sets it until then.
type Var struct {
	Type string `json:"type"`
	// MaxLen is STRING:n's n, 0 when upsd did not say.
	MaxLen int      `json:"max_len,omitempty"`
	Enum   []string `json:"enum,omitempty"`
	Ranges [][2]int `json:"ranges,omitempty"`
}

// FromType is the Var for a GET TYPE answer and, for an ENUM or a RANGE, its
// list. Writability comes from LIST RW, not from t.Writable.
func FromType(t nut.VarType, enum []string, ranges []nut.Range) Var {
	v := Var{MaxLen: t.MaxLen}
	switch {
	case t.Enum:
		v.Type, v.Enum = TypeEnum, enum
	case t.Range:
		v.Type = TypeRange
		for _, r := range ranges {
			v.Ranges = append(v.Ranges, [2]int{r.Min, r.Max})
		}
	case t.MaxLen > 0:
		v.Type = TypeString
	default:
		v.Type = TypeNumber
	}
	return v
}
