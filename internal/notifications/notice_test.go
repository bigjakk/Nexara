package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
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

// A manual resolve sends what an automatic one does: "resolved", for an alert
// that fired, to the step it reached; a pending one sends nothing.
func TestNotifyResolved(t *testing.T) {
	t.Parallel()
	sent := make(chan AlertPayload, 2)
	e, fake, ch := newDispatchEngine(t, recordingDispatcher{sent: sent})
	chain, _ := json.Marshal([]escalationStep{{ChannelID: uuid.New()}, {ChannelID: ch.ID}})
	fake.rows["GetAlertRule"] = db.AlertRule{ID: uuid.New(), Name: "rule01", EscalationChain: chain}
	alert := db.AlertHistory{ID: uuid.New(), State: "pending", EscalationLevel: 1}

	e.NotifyResolved(context.Background(), alert)
	if n := len(fake.callsOf("GetNotificationChannelEnabled")); n != 0 {
		t.Fatalf("a pending alert's resolve looked up %d channels, want none", n)
	}
	alert.State = "acknowledged"
	e.NotifyResolved(context.Background(), alert)
	select {
	case p := <-sent:
		if p.State != "resolved" || p.AlertID != alert.ID.String() || p.EscalationLevel != 1 {
			t.Errorf("sent %+v, want the alert's resolution at step 1", p)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no resolution was sent")
	}
	if got := fake.callsOf("GetNotificationChannelEnabled"); len(got) != 1 || got[0][0] != ch.ID {
		t.Errorf("channel lookups %v, want step 1's channel only", got)
	}
}

// funcDispatcher sends by calling itself.
type funcDispatcher func(context.Context, AlertPayload) error

func (funcDispatcher) Type() string { return "webhook" }
func (f funcDispatcher) Send(ctx context.Context, _ json.RawMessage, p AlertPayload) error {
	return f(ctx, p)
}

// A dead letter reads "retrying" while its replay is in flight, and its
// outcome after.
func TestReplayDLQMarksRetrying(t *testing.T) {
	t.Parallel()
	var fake *engineDB
	stateDuringSend := ""
	e, fake, ch := newDispatchEngine(t, funcDispatcher(func(context.Context, AlertPayload) error {
		if u := fake.callsOf("UpdateNotificationDLQState"); len(u) > 0 {
			stateDuringSend = u[len(u)-1][1].(string)
		}
		return nil
	}))
	id := uuid.New()
	fake.rows["GetNotificationDLQ"] = db.NotificationDlq{ID: id, ChannelID: pgtype.UUID{Bytes: ch.ID, Valid: true},
		Payload: json.RawMessage(`{}`), State: "pending"}
	if err := e.ReplayDLQ(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if stateDuringSend != "retrying" || len(fake.callsOf("MarkNotificationDLQResolved")) != 1 {
		t.Errorf("state during the send %q, resolved %d times: want retrying, then resolved once",
			stateDuringSend, len(fake.callsOf("MarkNotificationDLQResolved")))
	}
}

// Every send through a channel carries the channel's private-address
// allowance to the address checks (ChannelContext): an engine send and a
// dead-letter replay alike.
func TestSendsCarryTheChannelsAllowance(t *testing.T) {
	t.Parallel()
	allowed := make(chan bool, 2)
	var fake *engineDB
	e, fake, ch := newDispatchEngine(t, funcDispatcher(func(ctx context.Context, _ AlertPayload) error {
		allowed <- privateAllowed(ctx)
		return nil
	}))
	ch.AllowPrivateAddress = true
	fake.rows["GetNotificationChannelEnabled"] = ch
	fake.rows["GetNotificationDLQ"] = db.NotificationDlq{ChannelID: pgtype.UUID{Bytes: ch.ID, Valid: true},
		Payload: json.RawMessage(`{}`), State: "pending"}

	e.SendNotice(context.Background(), ch.ID, uuid.New(), AlertPayload{})
	if err := e.ReplayDLQ(context.Background(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"send", "replay"} {
		select {
		case got := <-allowed:
			if !got {
				t.Errorf("a %s through an allowed channel carried no allowance", path)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("no %s", path)
		}
	}
}

// A dead letter, which every Viewer reads, and a replay's answer keep the
// failure's class, not what the target said (FailureClass).
func TestDeadLettersKeepTheFailureClass(t *testing.T) {
	t.Parallel()
	banner := fmt.Errorf("send request: %w", &url.Error{Op: "Post", URL: "http://192.0.2.1:22/",
		Err: errors.New(`malformed HTTP response "SSH-2.0-OpenSSH_9.6"`)})
	e, fake, ch := newDispatchEngine(t, funcDispatcher(func(context.Context, AlertPayload) error { return banner }))
	e.retrySchedule = []time.Duration{}
	inserted := make(chan struct{}, 1)
	fake.onCall = func(name string) {
		if name == "InsertNotificationDLQ" {
			inserted <- struct{}{}
		}
	}
	fake.rows["GetNotificationDLQ"] = db.NotificationDlq{ChannelID: pgtype.UUID{Bytes: ch.ID, Valid: true},
		Payload: json.RawMessage(`{}`), State: "pending"}

	e.SendNotice(context.Background(), ch.ID, uuid.New(), AlertPayload{})
	select {
	case <-inserted:
	case <-time.After(10 * time.Second):
		t.Fatal("no dead letter")
	}
	replayErr := e.ReplayDLQ(context.Background(), uuid.New())
	updates := fake.callsOf("UpdateNotificationDLQState")
	got := []string{fake.callsOf("InsertNotificationDLQ")[0][7].(string), updates[len(updates)-1][2].(string), fmt.Sprint(replayErr)}
	for _, g := range got {
		if strings.Contains(g, "SSH") || !strings.Contains(g, "the request failed") {
			t.Errorf("stored or answered %q, want the class alone", g)
		}
	}
}
