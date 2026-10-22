package handlers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The response says what the stored read says: "never" before any read, with
// items [] rather than null for the SPA's {items,total} reader, and after a
// failed attempt the newest reason beside the last good read's modules.
func TestNodeMemoryResponse(t *testing.T) {
	t.Parallel()
	never, err := json.Marshal(neverReadMemory(true))
	if err != nil || !strings.Contains(string(never), `"items":[]`) || !strings.Contains(string(never), `"status":"never"`) || !strings.Contains(string(never), `"auto_read":true`) {
		t.Errorf("never read = %s (%v)", never, err)
	}

	read := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	failed := read.Add(24 * time.Hour)
	resp, err := nodeMemoryResponseFrom(db.NodeMemoryInventory{
		Status: "error", Reason: "dmidecode failed on this node.", AttemptedAt: failed,
		ReadAt:         pgtype.Timestamptz{Time: read, Valid: true},
		InstalledBytes: 32 << 30, SlotsUsed: 1, SlotsTotal: 2, MaxCapacityBytes: 1 << 40, Ecc: "Multi-bit ECC",
		Modules: json.RawMessage(`[{"locator":"A1","size_bytes":34359738368,"type":"DDR4"},{"locator":"A2","size_bytes":0}]`),
	}, 31<<30, false)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if resp.Status != "error" || resp.Reason == "" || resp.ReadAt == nil || !resp.ReadAt.Equal(read) || !resp.AttemptedAt.Equal(failed) ||
		resp.Total != 2 || resp.Items[0].Locator != "A1" || resp.Items[0].SizeBytes != 32<<30 || resp.Items[1].SizeBytes != 0 || resp.AutoRead ||
		resp.NodeMemTotal != 31<<30 {
		t.Errorf("failed attempt after a good read = %+v", resp)
	}
}
