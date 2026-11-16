package audit

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/audit/audittest"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	proxsyslog "github.com/bigjakk/nexara/internal/syslog"
)

type fakeInserter struct {
	rows []db.InsertAuditLogParams
	err  error
}

func (f *fakeInserter) InsertAuditLog(_ context.Context, p db.InsertAuditLogParams) error {
	f.rows = append(f.rows, p)
	return f.err
}

// recorder is a real publisher (so its forwarder is used) that also records
// the events it is handed.
type recorder struct {
	*events.Publisher
	events []string
}

func (r *recorder) ClusterEvent(_ context.Context, clusterID, kind, resourceType, resourceID, action string) {
	r.events = append(r.events, strings.Join([]string{clusterID, kind, resourceType, resourceID, action}, " "))
}

// TestWrite is the one path's contract: the row, the audit_entry event and the
// syslog record of one entry, redacted the same way in all three, whatever the
// insert does.
func TestWrite(t *testing.T) {
	t.Parallel()
	cluster, user, key := uuid.New(), uuid.New(), uuid.New()

	tests := []struct {
		name      string
		details   string
		apiKey    uuid.UUID
		insertErr error
		wantRow   string   // the details the row gets
		wantLine  []string // in the syslog record
	}{
		{name: "untouched details reach the row byte for byte", details: `{"vmid":121,  "resource_name":"linux01"}`,
			wantRow:  `{"vmid":121,  "resource_name":"linux01"}`,
			wantLine: []string{`action="migrate"`, `user="` + user.String() + `"`, `cluster="` + cluster.String() + `"`, `vmid="121"`, `resource_name="linux01"`}},
		{name: "a secret field is recorded as set", details: `{"fields":{"cipassword":"hunter2"}}`,
			wantRow:  `{"fields":{"cipassword":"(set)"}}`,
			wantLine: []string{`(set)`}},
		{name: "an API key request names the key", details: `{}`, apiKey: key,
			wantRow:  `{"api_key_id":"` + key.String() + `"}`,
			wantLine: []string{key.String()}},
		{name: "a failed insert is returned and still announced and forwarded", insertErr: errors.New("db down"),
			wantRow:  `{}`,
			wantLine: []string{`action="migrate"`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pub, next := audittest.Collector(t)
			rec := &recorder{Publisher: pub}
			q := &fakeInserter{err: tc.insertErr}
			var details json.RawMessage
			if tc.details != "" {
				details = json.RawMessage(tc.details)
			}
			p := db.InsertAuditLogParams{
				ClusterID: pgtype.UUID{Bytes: cluster, Valid: true}, UserID: pgtype.UUID{Bytes: user, Valid: true},
				ResourceType: "vm", ResourceID: "121", Action: "migrate", Details: details,
			}

			err := WriteForKey(context.Background(), q, rec, p, tc.apiKey)

			if !errors.Is(err, tc.insertErr) {
				t.Errorf("err = %v, want %v", err, tc.insertErr)
			}
			if len(q.rows) != 1 || string(q.rows[0].Details) != tc.wantRow {
				t.Fatalf("rows = %+v, want one with details %s", q.rows, tc.wantRow)
			}
			if want := cluster.String() + " audit_entry vm 121 migrate"; len(rec.events) != 1 || rec.events[0] != want {
				t.Errorf("events = %q, want [%q]", rec.events, want)
			}
			line := next(5 * time.Second)
			for _, w := range tc.wantLine {
				if !strings.Contains(line, w) {
					t.Errorf("syslog record %q lacks %q", line, w)
				}
			}
			if strings.Contains(line, "hunter2") {
				t.Errorf("syslog record carries the secret: %q", line)
			}
		})
	}
}

// orderPub is a real publisher that closes forwarded when Write asks it for its
// forwarder, and reports on order whether that had happened when the publish
// began.
type orderPub struct {
	*events.Publisher
	once      sync.Once
	forwarded chan struct{}
	order     chan bool
}

func (p *orderPub) SyslogForwarder() *proxsyslog.Forwarder {
	p.once.Do(func() { close(p.forwarded) })
	return p.Publisher.SyslogForwarder()
}

func (p *orderPub) ClusterEvent(ctx context.Context, clusterID, kind, resourceType, resourceID, action string) {
	select {
	case <-p.forwarded:
		p.order <- true
	default:
		p.order <- false
	}
	p.Publisher.ClusterEvent(ctx, clusterID, kind, resourceType, resourceID, action)
}

// TestWriteDoesNotWaitOnAStalledRedis: against a Redis that accepts and never
// answers, the syslog copy is handed over before the publish begins and Write
// returns near publishTimeout, not after the client's 30 s read timeout.
func TestWriteDoesNotWaitOnAStalledRedis(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var conns []net.Conn
	var mu sync.Mutex
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})

	collector, next := audittest.Collector(t)
	rdb := redis.NewClient(&redis.Options{Addr: ln.Addr().String(), ReadTimeout: 30 * time.Second, MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	inner := events.NewPublisher(rdb, slog.New(slog.DiscardHandler))
	inner.SetSyslogForwarder(collector.SyslogForwarder())
	pub := &orderPub{Publisher: inner, forwarded: make(chan struct{}), order: make(chan bool, 1)}

	done := make(chan error, 1)
	go func() {
		done <- Write(context.Background(), &fakeInserter{}, pub, db.InsertAuditLogParams{ResourceType: "vm", ResourceID: "100", Action: "vm_start"})
	}()
	select {
	case forwardedFirst := <-pub.order:
		if !forwardedFirst {
			t.Errorf("the publish began before the syslog copy was handed over, so the copy waits behind Redis")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Write never published")
	}
	if line := next(5 * time.Second); !strings.Contains(line, `action="vm_start"`) {
		t.Errorf("collector received %q, want the vm_start record", line)
	}
	select {
	case <-done:
	case <-time.After(10 * publishTimeout):
		t.Fatalf("Write still waiting on Redis after %v; the publish bound is not applied", 10*publishTimeout)
	}
}
