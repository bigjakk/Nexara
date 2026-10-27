package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/gofiber/fiber/v3"
)

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
