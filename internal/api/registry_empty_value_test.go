package api

import (
	"errors"
	"maps"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// Two rulings on the empty string live here, pointing opposite ways on purpose. A listing FILTER the handler read as
// `if x != ""` keeps that meaning: ?x= is "no filter", as before the registry. The task, audit-log and CVE tests hold
// the filters carrying a rule or enum with assertEmptyFilterSentinel and assertEmptyOrUUIDDeclaration; the six carrying
// neither (the audit log's resource_type, action, source and two time bounds, and the vmids both listings share) are
// held by the 204 rows in TestAuditListBoundsArePinned and TestTaskListBoundsArePinned and the handler tests; kev, a
// boolean, by its EmptyIsAbsent declaration and the twin in TestVulnerabilityFiltersAreDeclared. A QUERY parameter whose
// old handler substituted a DEFAULT for an empty value, and whose declaration closes its vocabulary with an enum, does
// not: ?x= is a 400 (TestEmptyTextDefaultsAreRefused). BODY parameters of that shape go the other way (task create's
// status, ha_policy, a report schedule's format and vm-import's source_acquisition keep "" in their enums and the
// handlers substitute the default) and nothing here covers them.

// assertEmptyOrUUIDDeclaration holds a uuid filter to the declaration that
// keeps its empty value: the empty-or-uuid rule rather than the uuid format,
// which rejects "" as every registered format does, with the uuid's own
// length as its cap.
func assertEmptyOrUUIDDeclaration(t *testing.T, e Endpoint, name string) {
	t.Helper()
	prop := e.Parameters[name]
	if prop.Format != "" {
		t.Errorf("%s declares format %q; every registered format rejects the empty \"no filter\" value",
			name, prop.Format)
	}
	if prop.Pattern != emptyOrUUID {
		t.Errorf("%s declares pattern %q, want the empty-or-uuid one", name, prop.Pattern)
	}
	if prop.MaxLength == nil {
		t.Errorf("%s declares no MaxLength, want 36 — a uuid's length", name)
	} else if *prop.MaxLength != 36 {
		t.Errorf("%s declares MaxLength %d, want 36 — a uuid's length", name, *prop.MaxLength)
	}
}

// emptyFilterRefusals are the values a parameter that reads "" as "no filter"
// must still refuse. The widening admits the empty string and nothing else, so
// a traversal segment — bare, or percent-encoded as a caller expecting
// something downstream to decode it would send it — a separator and a newline
// all stay out. There are two newlines: a bare one, aimed at the empty branch
// itself (a multi-line ^$ would match it), and one trailing a valid value.
func emptyFilterRefusals(valid string) []string {
	return []string{"..", ".", "%2e%2e", "/", "\n", valid + "\n"}
}

// assertEmptyFilterSentinel drives one "" = no-filter parameter through its endpoint's own declaration:
// "" must be accepted and read back as "", valid accepted, and every emptyFilterRefusals value refused by a
// validation error naming the parameter (base carries the required parameters, which Validate would
// otherwise report missing first). A refusal is evidence about the RULE only if the rule refuses it, so each
// value is first run through a twin of the property with its pattern and enum stripped and every other facet
// kept, where it must pass. The exception is a value longer than MaxLength (the uuid plus a newline, 37 vs
// 36), which the twin refuses too: held by the cap and the rule together, and the twin must refuse it so
// the exception cannot hide another reason.
func assertEmptyFilterSentinel(t *testing.T, e Endpoint, name, valid string, base map[string]any) {
	t.Helper()
	prop, ok := e.Parameters[name]
	if !ok {
		t.Fatalf("%s %s declares no %s", e.Method, e.Path, name)
	}
	validate := func(props apischema.Properties, v string) (*apischema.Params, error) {
		in := map[string]any{name: v}
		maps.Copy(in, base)
		return props.Validate(in)
	}

	params, err := validate(e.Parameters, "")
	if err != nil {
		t.Fatalf("%s: the empty value was refused (%v); it has always meant \"no filter\" here", name, err)
	}
	if got, supplied := params.OptString(name); got != "" || !supplied {
		t.Errorf("%s: the empty value reads back as (%q, supplied=%v), want (\"\", true)", name, got, supplied)
	}
	if _, err := validate(e.Parameters, valid); err != nil {
		t.Fatalf("%s: the valid value %q was refused (%v); the fixture is wrong", name, valid, err)
	}

	stripped := prop
	stripped.Pattern, stripped.Enum = "", nil
	twin := maps.Clone(e.Parameters)
	twin[name] = stripped

	for _, v := range emptyFilterRefusals(valid) {
		overLength := prop.MaxLength != nil && utf8.RuneCountInString(v) > *prop.MaxLength
		if _, err := validate(twin, v); (err != nil) != overLength {
			t.Fatalf("precondition: %s = %q without its rule: err = %v, want refused=%v — the case below "+
				"would not show the rule refusing it", name, v, err, overLength)
		}

		_, err := validate(e.Parameters, v)
		var verr *apischema.ValidationError
		if !errors.As(err, &verr) {
			t.Errorf("%s = %q was not refused by validation (err = %v)", name, v, err)
			continue
		}
		if verr.Field != name {
			t.Errorf("%s = %q was refused as %q, which names %q", name, v, err, verr.Field)
		}
	}
}

// TestEmptyFilterListingsDeclareWhatTheHandlerTestsCover holds the three
// listings' declarations to the parameter sets their handler tests are built
// on. TestTaskListTreatsAnEmptyFilterAsNone,
// TestAuditReadsTreatAnEmptyFilterAsNone and
// TestCVEVulnerabilityListTreatsAnEmptyFilterAsNone, in package handlers,
// send every filter their mirrors declare empty, and fail when a mirror
// declares one they do not send. They cannot read the declarations — package
// api imports package handlers — so this is the other half: a parameter added
// to a declaration fails here until its mirror, and so its test, has it too.
func TestEmptyFilterListingsDeclareWhatTheHandlerTestsCover(t *testing.T) {
	for _, tt := range []struct {
		path string
		want []string
	}{
		{taskHistoryScope, []string{"filter_cluster_id", "limit", "offset", "order", "sort", "status", "vmids"}},
		{auditScope, []string{"action", "end_time", "filter_cluster_id", "limit", "offset", "resource_type",
			"source", "start_time", "user_id", "vmids"}},
		{auditScope + "/export", []string{"action", "end_time", "filter_cluster_id", "format", "limit", "offset",
			"resource_type", "source", "start_time", "user_id", "vmids"}},
		{clusterScope + "/audit-log", []string{"action", "cluster_id", "end_time", "limit", "offset",
			"resource_type", "source", "start_time", "user_id", "vmids"}},
		{cveScanScope + "/:scan_id/vulnerabilities", []string{"cluster_id", "kev", "node_id", "scan_id", "severity"}},
	} {
		got := slices.Sorted(maps.Keys(declaredEndpoint(t, fiber.MethodGet, tt.path).Parameters))
		if !slices.Equal(got, tt.want) {
			t.Errorf("GET %s declares %v, but its handler test is built on %v — add the new parameter to the "+
				"mirror and the filter list there, then here", tt.path, got, tt.want)
		}
	}
}

// TestEmptyTextDefaultsAreRefused pins a decision, not a regression: an EMPTY value for each of these is
// a 400 on purpose (the operator's ruling of 2026-09-23, the same as for an empty integer ?limit=,
// TestDRSHistoryLimitIsBounded). Before the registry each was read with c.Query(name, default) (the task
// sort pair with parseTaskSort's own `if x == ""`), substituting the default for an empty value as well as
// an absent one, so ?range= meant 1h. A declared Default applies only to a key not sent, and each of these
// carries an enum "" is not in; restoring the substitution means putting "" in an enum, which fails its
// row. Absent on purpose: the PBS RRD route's timeframe and cf (no enum, still accept "", which
// GetDatastoreRRD turns into the same defaults), and the settings reads and OIDC callback (still legacy).
func TestEmptyTextDefaultsAreRefused(t *testing.T) {
	fill := strings.NewReplacer(
		":cluster_id", testClusterID,
		":vm_id", testVMID,
		":node_id", testNodeID,
		":osd_id", "3",
		":pbs_id", testPBSServerID,
		":key", "dashboard.layout",
	)
	for _, tt := range []struct {
		method, path, name string
		// def is the value the old handler substituted, which the
		// declaration's Default states for an omitted key.
		def string
	}{
		{fiber.MethodGet, clusterScope + "/metrics", "range", "1h"},
		{fiber.MethodGet, clusterScope + "/vms/:vm_id/metrics", "range", "1h"},
		{fiber.MethodGet, clusterScope + "/nodes/:node_id/metrics", "range", "1h"},
		{fiber.MethodGet, cephScope + "/metrics", "timeframe", "1h"},
		{fiber.MethodGet, cephScope + "/osds/:osd_id/preflight", "action", "out"},
		{fiber.MethodGet, pbsBackupScope + "/metrics", "timeframe", "latest"},
		{fiber.MethodGet, auditScope + "/export", "format", "json"},
		{fiber.MethodDelete, settingsScope + "/:key", "scope", "user"},
		{fiber.MethodGet, taskHistoryScope, "sort", "started"},
		{fiber.MethodGet, taskHistoryScope, "order", "desc"},
	} {
		t.Run(tt.method+" "+tt.path+" "+tt.name, func(t *testing.T) {
			if got := declaredEndpoint(t, tt.method, tt.path).Parameters[tt.name].Default; got != tt.def {
				t.Fatalf("%s declares default %#v, want %q — the value the old handler substituted", tt.name, got, tt.def)
			}
			target := fill.Replace(tt.path)

			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeEndpoint(t, tt.method, tt.path, cap))
			status, env := send(t, app, httptest.NewRequest(tt.method, target, nil))
			if status != fiber.StatusNoContent {
				t.Fatalf("omitted: status = %d (%q), want 204", status, env.Message)
			}
			if got := cap.params.String(tt.name); got != tt.def {
				t.Errorf("omitted: %s reached the handler as %q, want the declared default %q", tt.name, got, tt.def)
			}
			if cap.params.Has(tt.name) {
				t.Errorf("omitted: %s reads as supplied; a default is not something the caller sent", tt.name)
			}

			cap = &capture{}
			app = newRegistryApp(t, noAuth(), probeEndpoint(t, tt.method, tt.path, cap))
			status, env = send(t, app, httptest.NewRequest(tt.method, target+"?"+tt.name+"=", nil))
			if status != fiber.StatusBadRequest {
				t.Fatalf("?%s=: status = %d (%q), want 400 — empty is refused, not read as %q",
					tt.name, status, env.Message, tt.def)
			}
			if !strings.HasPrefix(env.Message, tt.name+":") {
				t.Errorf("?%s=: message = %q, want it to name %s", tt.name, env.Message, tt.name)
			}
			if cap.called {
				t.Errorf("?%s=: the handler ran for a request the schema refused", tt.name)
			}
		})
	}
}
