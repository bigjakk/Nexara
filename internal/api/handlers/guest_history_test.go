package handlers

import (
	"testing"
	"time"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The cutoff decides whose history a reused VMID shows. Events are newest
// first, as ListGuestLifecycleEvents returns them.
func TestGuestHistoryCutoff(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(h int) time.Time { return t0.Add(time.Duration(h) * time.Hour) }
	birth := func(h int) db.ListGuestLifecycleEventsRow { return db.ListGuestLifecycleEventsRow{CreatedAt: at(h)} }
	birthEnded := func(h int, status, exit string) db.ListGuestLifecycleEventsRow {
		return db.ListGuestLifecycleEventsRow{CreatedAt: at(h), TaskStatus: status, TaskExitStatus: exit}
	}
	destroy := func(h int, status, exit, recorded string) db.ListGuestLifecycleEventsRow {
		return db.ListGuestLifecycleEventsRow{CreatedAt: at(h), IsDestroy: true,
			TaskStatus: status, TaskExitStatus: exit, RecordedStatus: recorded}
	}
	for _, tt := range []struct {
		name          string
		events        []db.ListGuestLifecycleEventsRow
		wantAt        time.Time
		wantInclusive bool
	}{
		{"no lifecycle events: the whole history", nil, time.Time{}, false},
		{"newest is a birth: from it, inclusive", []db.ListGuestLifecycleEventsRow{birth(5), destroy(3, "completed", "OK", "")}, at(5), true},
		{"newest is the previous guest's destroy: after it", []db.ListGuestLifecycleEventsRow{destroy(5, "completed", "OK", ""), birth(1)}, at(5), false},
		{"a failed destroy ended nothing", []db.ListGuestLifecycleEventsRow{destroy(5, "failed", "VM is locked", ""), birth(1)}, at(1), true},
		{"a running destroy has ended nothing yet", []db.ListGuestLifecycleEventsRow{destroy(5, "running", "", ""), destroy(4, "", "", "OK")}, at(4), false},
		{"an old destroy whose outcome was pruned counts", []db.ListGuestLifecycleEventsRow{destroy(5, "", "", "")}, at(5), false},
		// A failed create, restore or clone made no guest: the earlier one's
		// History stays.
		{"a failed birth made nothing", []db.ListGuestLifecycleEventsRow{birthEnded(5, "failed", "VM 100 already exists"), destroy(3, "completed", "OK", "")}, at(3), false},
		{"a running birth has made nothing yet", []db.ListGuestLifecycleEventsRow{birthEnded(5, "running", ""), birth(2)}, at(2), true},
	} {
		got, inclusive := guestHistoryCutoff(tt.events)
		if got.Valid != !tt.wantAt.IsZero() || !got.Time.Equal(tt.wantAt) || inclusive != tt.wantInclusive {
			t.Errorf("%s: cutoff = (%v, valid %v, inclusive %v), want (%v, inclusive %v)",
				tt.name, got.Time, got.Valid, inclusive, tt.wantAt, tt.wantInclusive)
		}
	}
}
