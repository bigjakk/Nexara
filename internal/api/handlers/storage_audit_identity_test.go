package handlers

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

// A storage's audit rows name the storage, not the storage_pools row the request
// addressed. That row is one node's view of the storage and the collector
// prunes and re-inserts it, so an audit row keyed by its id stops resolving —
// it is how the update, upload and OCI-pull rows on a dev install came to
// match no row at all. The pool the harness serves has a UUID of its own, so
// a handler still keying on it fails here.
//
// Sequential: the harness redirects the process-wide default logger.
func TestStorageAuditRowsNameTheirStorageNotThePoolRow(t *testing.T) {
	h := newStorageKeyHarness(t)

	tests := []struct {
		name, method, body string
		action             string
		details            map[string]any
	}{
		{"update", http.MethodPut, `{"params":{"content":"backup"}}`, "update",
			map[string]any{"storage": "store01"}},
		{"delete", http.MethodDelete, "", "delete",
			map[string]any{"storage": "store01", "type": "pbs"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ex := h.do(t, tt.method, tt.body)
			if ex.status != http.StatusOK {
				t.Fatalf("status = %d (%s), want 200", ex.status, ex.body)
			}
			if len(ex.audits) != 1 {
				t.Fatalf("the action wrote %d audit rows, want exactly 1", len(ex.audits))
			}
			got := ex.audits[0]
			if got.ResourceType != "storage" || got.ResourceID != "store01" || got.Action != tt.action {
				t.Errorf("row is (%q, %q, %q), want (storage, store01, %s) — the pool row's id is %s",
					got.ResourceType, got.ResourceID, got.Action, tt.action, h.poolID)
			}
			var details map[string]any
			if err := json.Unmarshal(got.Details, &details); err != nil {
				t.Fatalf("details are not a JSON object: %v (%s)", err, got.Details)
			}
			if !reflect.DeepEqual(details, tt.details) {
				t.Errorf("details = %s\nwant %v", got.Details, tt.details)
			}
		})
	}
}
