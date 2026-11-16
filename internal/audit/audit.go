// Package audit is the one path an audit entry takes: the audit_log row, the
// audit_entry event that shows it live, and the copy the syslog forwarder sends.
// Request handlers reach it through handlers.AuditLogAs; background code (the
// collector, the scheduler, the engines) calls Write. Both go through write, so
// an action recorded by either looks the same in audit_log, on the WS stream and
// in syslog. TestGuard_AuditRowsGoThroughWrite keeps a bare InsertAuditLog out
// of production code.
package audit

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/pvefield"
	"github.com/bigjakk/nexara/internal/redact"
	proxsyslog "github.com/bigjakk/nexara/internal/syslog"
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

// publishTimeout bounds the wait for Redis when it is unreachable: the event is
// a notice, and the action it records has already happened.
const publishTimeout = time.Second

// Write records one audit entry made by background code: it inserts the row,
// publishes the audit_entry event and hands the entry to the syslog forwarder.
// A nil Details is recorded as {}; a zero UserID as no user. pub may be nil.
// Only the insert can fail, and its error is returned for the caller to handle
// as it did before; the event and the forward never block on the network and
// never fail the write.
func Write(ctx context.Context, q Inserter, pub Publisher, p db.InsertAuditLogParams) error {
	return write(ctx, q, pub, p, uuid.Nil)
}

// WriteForKey is Write for an entry a request authenticated by an API key
// made: the key's id is added to the details as api_key_id. Only
// handlers.AuditLogAs calls it.
func WriteForKey(ctx context.Context, q Inserter, pub Publisher, p db.InsertAuditLogParams, apiKeyID uuid.UUID) error {
	return write(ctx, q, pub, p, apiKeyID)
}

func write(ctx context.Context, q Inserter, pub Publisher, p db.InsertAuditLogParams, apiKeyID uuid.UUID) error {
	if p.Details == nil {
		p.Details = json.RawMessage(`{}`)
	}

	// One decode serves three purposes: redacting secrets, injecting
	// api_key_id, and lifting the guest identity out for the syslog
	// forwarder. Re-marshalled only when something was actually changed, so
	// an untouched details blob reaches the row byte-for-byte as the caller
	// wrote it.
	var m map[string]any
	if err := json.Unmarshal(p.Details, &m); err != nil {
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
		p.Details = enriched
	}

	var cidStr string
	if p.ClusterID.Valid {
		cidStr = uuid.UUID(p.ClusterID.Bytes).String()
	}

	err := q.InsertAuditLog(ctx, p)

	if pub != nil {
		pctx, cancel := context.WithTimeout(ctx, publishTimeout)
		pub.ClusterEvent(pctx, cidStr, events.KindAuditEntry, p.ResourceType, p.ResourceID, p.Action)
		cancel()
	}

	// Forward runs off the caller's goroutine and drops rather than waits when
	// its queue is full. vmid and resource_name are promoted to first-class
	// fields so a SIEM decoder can key a rule on the guest without re-parsing
	// the details JSON. A row with no user is sent as the nil UUID.
	if src, ok := pub.(forwarderSource); ok {
		if fwd := src.SyslogForwarder(); fwd != nil {
			fwd.Forward(proxsyslog.Message{
				Timestamp:    time.Now().UTC(),
				UserID:       uuid.UUID(p.UserID.Bytes).String(),
				ClusterID:    cidStr,
				ResourceType: p.ResourceType,
				ResourceID:   p.ResourceID,
				Action:       p.Action,
				VMID:         detailString(m, "vmid"),
				ResourceName: detailString(m, "resource_name"),
				Details:      string(p.Details),
			})
		}
	}
	return err
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
