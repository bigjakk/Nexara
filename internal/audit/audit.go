// Package audit is the one path an audit entry takes: the audit_log row, the
// audit_entry event that shows it live, and the copy the syslog forwarder sends.
// Request handlers reach it through handlers.AuditLogAs; background code (the
// collector, the scheduler, the engines) calls Write, and the collector files
// the tasks it observes on Proxmox with WriteSourced. Both go through write, so
// an action recorded by either looks the same in audit_log, on the WS stream and
// in syslog. TestGuard_AuditRowsGoThroughWrite keeps a bare InsertAuditLog out
// of production code.
package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/pvefield"
	"github.com/bigjakk/nexara/internal/redact"
	proxsyslog "github.com/bigjakk/nexara/internal/syslog"
	"github.com/bigjakk/nexara/pkg/redisutil"
)

// Inserter is the query Write records the row with; *db.Queries and the
// packages' query interfaces satisfy it.
type Inserter interface {
	InsertAuditLog(ctx context.Context, arg db.InsertAuditLogParams) error
}

// Publisher is what Write announces the entry through. *events.Publisher
// satisfies it, and through its SyslogForwarder the entry is also forwarded; a
// publisher without that method (a test's recorder) only receives the event.
type Publisher interface {
	ClusterEvent(ctx context.Context, clusterID, kind, resourceType, resourceID, action string)
}

type forwarderSource interface {
	SyslogForwarder() *proxsyslog.Forwarder
}

// publishTimeout is how long a writer waits for the audit_entry publish. The
// Redis client does not honour a context deadline on the socket, so the wait is
// cut by redisutil.BoundedByContext: past it the event is dropped (counted in
// publishTimeouts, logged first and every 100th), never retried, and the
// abandoned call ends on the client's own timeout. The event is a notice; the
// row and the syslog copy do not depend on it.
const publishTimeout = time.Second

var publishTimeouts atomic.Uint64

// Write records one audit entry made by background code: it inserts the row,
// publishes the audit_entry event and hands the entry to the syslog forwarder.
// A nil Details is recorded as {}; a zero UserID as no user. pub may be nil.
// Only the insert can fail, and its error is returned for the caller to handle
// as it did before. The forward never blocks; the publish waits at most
// publishTimeout; neither fails the write.
func Write(ctx context.Context, q Inserter, pub Publisher, p db.InsertAuditLogParams) error {
	return write(ctx, q, pub, p, uuid.Nil)
}

// WriteForKey is Write for an entry a request authenticated by an API key
// made: the key's id is added to the details as api_key_id. Only
// handlers.AuditLogAs calls it.
func WriteForKey(ctx context.Context, q Inserter, pub Publisher, p db.InsertAuditLogParams, apiKeyID uuid.UUID) error {
	return write(ctx, q, pub, p, apiKeyID)
}

// SourcedInserter is the query WriteSourced records the row with.
type SourcedInserter interface {
	InsertAuditLogWithSource(ctx context.Context, arg db.InsertAuditLogWithSourceParams) error
}

// WriteSourced records an entry filed from what another system reports, such
// as a task observed on Proxmox: redacted as Write redacts, inserted with its
// source and CreatedAt, and forwarded stamped with CreatedAt (the time the row
// carries), not the time of the pass. It publishes nothing; the caller
// announces the entry when it is ready to. Its error is the insert's.
func WriteSourced(ctx context.Context, q SourcedInserter, pub Publisher, p db.InsertAuditLogWithSourceParams) error {
	var m map[string]any
	p.Details, m = prepare(p.Details, uuid.Nil)
	err := q.InsertAuditLogWithSource(ctx, p)
	forward(pub, message(p.ClusterID, p.UserID, p.ResourceType, p.ResourceID, p.Action, p.Details, p.CreatedAt), m)
	return err
}

func write(ctx context.Context, q Inserter, pub Publisher, p db.InsertAuditLogParams, apiKeyID uuid.UUID) error {
	var m map[string]any
	p.Details, m = prepare(p.Details, apiKeyID)

	err := q.InsertAuditLog(ctx, p)

	// The syslog copy is handed over before the publish, so it never waits
	// behind Redis.
	msg := message(p.ClusterID, p.UserID, p.ResourceType, p.ResourceID, p.Action, p.Details, time.Now())
	forward(pub, msg, m)

	if pub != nil {
		pctx, cancel := context.WithTimeout(ctx, publishTimeout)
		if _, perr := redisutil.BoundedByContext(pctx, func(c context.Context) (struct{}, error) {
			pub.ClusterEvent(c, msg.ClusterID, events.KindAuditEntry, p.ResourceType, p.ResourceID, p.Action)
			return struct{}{}, nil
		}); perr != nil {
			if n := publishTimeouts.Add(1); n == 1 || n%100 == 0 {
				slog.Warn("audit: the audit_entry event was dropped, Redis did not answer in time",
					"dropped_total", n, "bound", publishTimeout)
			}
		}
		cancel()
	}
	return err
}

// prepare returns the details the row and the syslog copy get, and their
// decoded form. One decode serves three purposes: redacting secrets, injecting
// api_key_id (when apiKeyID is set), and lifting the guest identity out for the
// syslog forwarder. Re-marshalled only when something was actually changed, so
// an untouched details blob reaches the row byte-for-byte as the caller wrote
// it. nil details are recorded as {}.
func prepare(details json.RawMessage, apiKeyID uuid.UUID) (out json.RawMessage, decoded map[string]any) {
	if details == nil {
		details = json.RawMessage(`{}`)
	}
	var m map[string]any
	if err := json.Unmarshal(details, &m); err != nil {
		m = nil
	}
	rewrite := redactSecrets(m)
	if apiKeyID != uuid.Nil {
		if m == nil {
			m = map[string]any{}
		}
		m["api_key_id"] = apiKeyID.String()
		rewrite = true
	}
	if rewrite {
		enriched, err := json.Marshal(m)
		if err != nil {
			enriched = json.RawMessage(`{}`) // never the bytes a redaction was to change
		}
		details = enriched
	}
	return details, m
}

// message is the syslog copy of one row. A row with no user is sent as the
// nil UUID, a row with no cluster with an empty cluster id.
func message(cluster, user pgtype.UUID, resourceType, resourceID, action string, details json.RawMessage, at time.Time) proxsyslog.Message {
	var cidStr string
	if cluster.Valid {
		cidStr = uuid.UUID(cluster.Bytes).String()
	}
	return proxsyslog.Message{
		Timestamp:    at.UTC(),
		UserID:       uuid.UUID(user.Bytes).String(),
		ClusterID:    cidStr,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Action:       action,
		Details:      string(details),
	}
}

// forward hands msg, with the guest identity lifted out of the decoded
// details, to pub's syslog forwarder when it has one. vmid and resource_name
// are promoted to first-class fields so a SIEM decoder can key a rule on the
// guest without re-parsing the details JSON. Forward runs off the caller's
// goroutine and drops rather than waits when its queue is full.
func forward(pub Publisher, msg proxsyslog.Message, m map[string]any) {
	src, ok := pub.(forwarderSource)
	if !ok {
		return
	}
	fwd := src.SyslogForwarder()
	if fwd == nil {
		return
	}
	msg.VMID = detailString(m, "vmid")
	msg.ResourceName = detailString(m, "resource_name")
	fwd.Forward(msg)
}

// redactSecrets replaces, in decoded audit details, the secrets a request
// carried, and reports whether it changed anything. It runs where the row, the
// syslog copy and (through the row) the History all read details, so no caller
// has to remember it. A fields object is how a row records the values it sent
// Proxmox, by parameter (the guest config writes): each secret one
// (pvefield.Secret) keeps its key, so the row still says the request set it, and
// is recorded as "(set)", or "(cleared)" when sent empty. A URL in any string of
// the details keeps no userinfo, query or fragment (redact.URLs).
func redactSecrets(m map[string]any) bool {
	changed := false
	if fields, ok := m["fields"].(map[string]any); ok {
		for k, v := range fields {
			if !pvefield.Secret(k) {
				continue
			}
			r := redact.Set
			if s, isString := v.(string); v == nil || isString && s == "" {
				r = redact.Cleared
			}
			if v != r {
				fields[k], changed = r, true
			}
		}
	}
	var walk func(v any) (any, bool)
	walk = func(v any) (any, bool) {
		moved := false
		switch t := v.(type) {
		case string:
			r := redact.URLs(t)
			return r, r != t
		case map[string]any:
			for k, e := range t {
				if r, ok := walk(e); ok {
					t[k], moved = r, true
				}
			}
		case []any:
			for i, e := range t {
				if r, ok := walk(e); ok {
					t[i], moved = r, true
				}
			}
		}
		return v, moved
	}
	if _, ok := walk(m); ok {
		changed = true
	}
	return changed
}

// detailString reads one detail field as a string for the syslog message.
//
// json.Unmarshal into map[string]any decodes every JSON number as float64, so
// a vmid written as 121 arrives as 121.0 and would render "121.000000" under
// %v. Integral floats are formatted without a fractional part; anything else
// falls back to its natural rendering.
func detailString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e15 {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return ""
	}
}
