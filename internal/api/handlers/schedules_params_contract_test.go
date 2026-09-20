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

// scheduled_tasks.params is a jsonb column with no schema: the endpoint
// declaration carries it through as a bare apischema.Object, and two packages
// read it back with their own struct. internal/scheduler's snapshotParams is
// the one that decides what the task DOES; snapshotScheduleParams over in
// schedules.go is a subset this layer reads so it can refuse a bad snap_name
// while the caller is still on the form.
//
// Two copies of one contract, and the drift FAILS OPEN — which is the whole
// reason this guard exists rather than a comment saying "keep these in sync".
// Rename the key on the scheduler's side and the handler goes on unmarshalling
// a field nobody writes: sp.SnapName is then always "", the
// `if sp.SnapName == ""` branch treats it as "no name given", and the
// synchronous 400 quietly stops happening. Nothing observable changes. The
// client still refuses at fire time, which is exactly the per-fire failure
// this change was written to remove.
//
// The pattern is the one this file's neighbour already uses: validScheduleActions
// is a copy of the declaration's Enum, and ScheduleActionKeys exists purely so
// TestScheduleActionVocabulary can pin the two from the other side. This is the
// same move for the params blob, done with the AST because the scheduler's
// struct is unexported and importing that package here would drag the whole
// engine — drs, rolling, reports, notifications — into the handler layer for
// one field.
//
// WHAT IT DOES NOT CATCH: it compares the json tags of the fields this layer
// declares. A key the scheduler adds and this layer ignores is out of scope by
// design — the subset is deliberate.
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
