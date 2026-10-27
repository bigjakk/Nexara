package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

const historyRouteCount = 1

// The guest is the one kind gated on view:audit alone; any other kind that
// reaches this list forgot its own view permission.
func TestHistoryAuditOnlyIsTheGuestAlone(t *testing.T) {
	t.Parallel()
	var auditOnly []string
	for _, r := range historyRoutes() {
		if r.AuditOnly != "" {
			auditOnly = append(auditOnly, r.Kind.Name)
		}
	}
	if !slices.Equal(auditOnly, []string{"guest"}) {
		t.Errorf("kinds gated on view:audit alone: %v, want [guest]", auditOnly)
	}
}

// A start_time that does not parse is the caller's 400 before any query
// runs: this handler has no database, so reading one would panic.
func TestHistoryRefusesABadStartTimeFirst(t *testing.T) {
	t.Parallel()
	app := newRegistryApp(t, stubAuth(map[string]bool{"view:audit": true}),
		historyEndpoint(&handlers.HistoryHandler{}, guestHistoryRoute()))
	if status, env := send(t, app, authedRequest(http.MethodGet,
		"/api/v1/clusters/"+testClusterID+"/guests/100/history?start_time=yesterday")); status != fiber.StatusBadRequest {
		t.Errorf("status = %d (%q), want 400", status, env.Message)
	}
}

// A route's gate is decided at registration: neither or both of Resource and
// AuditOnly is refused, so no kind can fall back to view:audit alone.
func TestHistoryEndpointRefusesAnUndecidedGate(t *testing.T) {
	t.Parallel()
	for name, r := range map[string]historyRoute{
		"neither": {},
		"both":    {Resource: "node", AuditOnly: "because"},
	} {
		r.Kind = handlers.GuestHistory()
		r.Path = clusterScope + "/probe/:vmid/history"
		r.Parameters = apischema.Properties{"cluster_id": apischema.StdOption("cluster-id")}
		func() {
			defer func() {
				if msg, _ := recover().(string); !strings.Contains(msg, "exactly one of Resource") {
					t.Errorf("%s: historyEndpoint = %q, want it refused for an undecided gate", name, msg)
				}
			}()
			historyEndpoint(&handlers.HistoryHandler{}, r)
		}()
	}
}

// TestHistoryCategoryVocabulary holds each kind's type filter to its labels:
// the categories the route declares (which HistoryKind.Validate holds to the
// rules that produce them) against the keys of the kind's frontend namespace,
// in order. A category with no label shows its raw name; a label with no
// category is a filter option the route refuses with a 400.
func TestHistoryCategoryVocabulary(t *testing.T) {
	t.Parallel()
	for _, r := range historyRoutes() {
		path := "../../frontend/src/locales/en/history-" + r.Kind.Name + ".json"
		labels, err := jsonObjectKeys(path, "categories")
		if err != nil {
			t.Errorf("%s: %v — every kind keeps its strings in its own namespace, history-<Name>", r.Path, err)
			continue
		}
		declared := declaredEndpoint(t, fiber.MethodGet, r.Path).Parameters["category"].Enum
		if want := append([]string{""}, labels...); !slices.Equal(declared, want) {
			t.Errorf("%s: the ?category= enum is %v but %s labels %v", r.Path, declared, path, labels)
		}
	}
}

// jsonObjectKeys returns the keys of the object at the top-level key field of
// the JSON file at path, in the order the file lists them.
func jsonObjectKeys(path, field string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, err
	}
	obj, ok := top[field]
	if !ok {
		return nil, fmt.Errorf("%s has no %q object", path, field)
	}
	dec := json.NewDecoder(bytes.NewReader(obj))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("%s: %q is not an object", path, field)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return keys, nil
}
