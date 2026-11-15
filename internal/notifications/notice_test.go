package notifications

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

const testKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// recordingDispatcher hands every payload it is sent to sent, and fails with err.
type recordingDispatcher struct {
	sent chan AlertPayload
	err  error
}

func (recordingDispatcher) Type() string { return "webhook" }
func (d recordingDispatcher) Send(_ context.Context, _ json.RawMessage, p AlertPayload) error {
	d.sent <- p
	return d.err
}

// newDispatchEngine is an engine over engineDB whose one webhook channel,
// returned, sends through d.
func newDispatchEngine(t *testing.T, d Dispatcher) (*Engine, *engineDB, db.NotificationChannel) {
	t.Helper()
	enc, err := crypto.Encrypt(`{"url":"https://hooks.example.com/x"}`, testKey)
	if err != nil {
		t.Fatal(err)
	}
	ch := db.NotificationChannel{ID: uuid.New(), Name: "hook01", ChannelType: "webhook", ConfigEncrypted: enc, Enabled: true}
	fake := &engineDB{rows: map[string]any{"GetNotificationChannelEnabled": ch}}
	reg := NewRegistry()
	reg.Register(d)
	return NewEngine(context.Background(), db.New(fake), slog.New(slog.DiscardHandler), nil, reg, testKey), fake, ch
}

// A notice no alert raised (a CVE notice) goes the way an alert's does: sent
// through the channel's dispatcher, and dead-lettered under its cluster, with
// no alert or rule, when it cannot be.
func TestSendNotice(t *testing.T) {
	t.Parallel()
	cluster := uuid.New()
	notice := AlertPayload{RuleName: "CVE: action required on cluster01", DedupKey: "nexara-cve-k"}

	sent := make(chan AlertPayload, 1)
	e, _, ch := newDispatchEngine(t, recordingDispatcher{sent: sent})
	if !e.SendNotice(context.Background(), ch.ID, cluster, notice) {
		t.Fatal("SendNotice did not hand the notice over")
	}
	select {
	case p := <-sent:
		if p.DedupKey != notice.DedupKey {
			t.Errorf("sent %+v, want the notice", p)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the notice was never sent")
	}

	// A channel whose rate limit is spent dead-letters at once.
	e, fake, ch := newDispatchEngine(t, recordingDispatcher{sent: make(chan AlertPayload, 1)})
	e.rateLimiter = &channelRateLimiter{buckets: map[uuid.UUID]*channelBucket{}, now: time.Now}
	if e.SendNotice(context.Background(), ch.ID, cluster, notice) {
		t.Error("a rate-limited notice reported as handed over")
	}
	dl := fake.callsOf("InsertNotificationDLQ")
	if len(dl) != 1 {
		t.Fatalf("dead letters = %d, want 1", len(dl))
	}
	// channel_id, channel_type, channel_name, alert_id, rule_id, cluster_id, …
	if a, r, c := dl[0][3].(pgtype.UUID), dl[0][4].(pgtype.UUID), dl[0][5].(pgtype.UUID); a.Valid || r.Valid ||
		c != (pgtype.UUID{Bytes: cluster, Valid: true}) {
		t.Errorf("dead letter filed under alert %v rule %v cluster %v, want only the cluster", a, r, c)
	}
}
