package upscontrol

import (
	"reflect"
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
