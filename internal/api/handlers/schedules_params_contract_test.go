package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// scheduled_tasks.params has no schema, and two structs read it: the scheduler's
// snapshotParams (what the task does) and this package's snapshotScheduleParams (to refuse a
// bad snap_name on the form). Drift fails open: rename the scheduler's key and SnapName is
// always "", so the synchronous 400 silently stops. Compared by AST (that struct is
// unexported); keys the scheduler has and this layer ignores are out of scope by design.
const schedulerPackageDir = "../../scheduler"

// schedulerSnapshotParamsType is the struct in internal/scheduler that decodes
// the column when the task actually fires.
const schedulerSnapshotParamsType = "snapshotParams"

// jsonTagsOf reads the `json:"…"` key of every field in an AST struct, by Go
// field name. A field with no json tag maps to "".
func jsonTagsOf(st *ast.StructType) map[string]string {
	tags := map[string]string{}
	for _, field := range st.Fields.List {
		key := ""
		if field.Tag != nil {
			if unquoted, err := strconv.Unquote(field.Tag.Value); err == nil {
				key, _, _ = strings.Cut(reflect.StructTag(unquoted).Get("json"), ",")
			}
		}
		for _, name := range field.Names {
			tags[name.Name] = key
		}
	}
	return tags
}

func TestGuard_SnapshotScheduleParamsMatchesTheScheduler(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(schedulerPackageDir, "*.go"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	fset := token.NewFileSet()
	var schedulerTags map[string]string
	var foundIn string

	for _, path := range paths {
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok || ts.Name.Name != schedulerSnapshotParamsType {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			schedulerTags = jsonTagsOf(st)
			foundIn = path
			return false
		})
	}

	// The non-vacuity floor. Without it, moving or renaming the scheduler's
	// struct makes this test green by finding nothing to compare — the failure
	// mode being guarded against, reported as success.
	if schedulerTags == nil {
		t.Fatalf("no %s struct found under %s; this guard is now passing because it has "+
			"nothing to compare, which is the drift it exists to catch",
			schedulerSnapshotParamsType, schedulerPackageDir)
	}

	// Reflect, not the AST, for our own side: it is the same type the handler
	// actually unmarshals into, so there is no second reading of it to be wrong.
	ours := reflect.TypeOf(snapshotScheduleParams{})
	if ours.NumField() == 0 {
		t.Fatal("snapshotScheduleParams has no fields; nothing is being checked")
	}

	for i := range ours.NumField() {
		field := ours.Field(i)
		wantKey, _, _ := strings.Cut(field.Tag.Get("json"), ",")

		theirKey, present := schedulerTags[field.Name]
		if !present {
			t.Errorf("snapshotScheduleParams.%s has no counterpart in %s.%s (%s). This layer would "+
				"keep validating a key the scheduler no longer reads, and an unreadable key decodes "+
				"to the zero value — which the empty-name branch treats as \"no name given\" and "+
				"waves through.", field.Name, schedulerSnapshotParamsType, field.Name, foundIn)
			continue
		}
		if theirKey != wantKey {
			t.Errorf("snapshotScheduleParams.%s reads json key %q but %s.%s reads %q (%s). "+
				"The handler would validate a key nothing writes and let the real one through "+
				"unchecked — silently, because an absent key is indistinguishable from an empty one.",
				field.Name, wantKey, schedulerSnapshotParamsType, field.Name, theirKey, foundIn)
		}
	}
}
