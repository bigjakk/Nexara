package handlers

import (
	"context"
	"encoding/json"
	"maps"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/pvefield"
)

// auditFieldNames is the names, never the values, of the Proxmox fields an
// update sends, for its audit row's fields: the change observer explains only
// those, and the ones Proxmox derives from them, as changed by the row
// (internal/collector observer_explain.go). view:audit is held by every
// Viewer, so a name is all a row may carry. also names more: a field it
// clears beside its params.
func auditFieldNames(params any, also ...string) []string {
	return auditSentOf(params).names(also...)
}

// auditSent is what an update sends Proxmox: each field's value by its name,
// as the client writes it, and the names its delete list clears. It is
// compared, by changed, and never kept.
type auditSent struct {
	values  map[string]string
	cleared []string
}

// auditSentOf reads params, a request struct of the proxmox package whose
// fields are tagged with Proxmox's names — each that is set counts, a non-nil
// pointer, a non-empty string or list, a non-zero number, embedded structs
// included — or a form, every key of it. always names a field the client
// sends whatever its value (an interface's autostart). digest, the config's
// checksum, is no field.
func auditSentOf(params any, always ...string) auditSent {
	s := auditSent{values: map[string]string{}}
	if form, ok := params.(url.Values); ok {
		for key, values := range form {
			switch key {
			case "delete":
				for _, v := range values {
					s.cleared = append(s.cleared, pvefield.SplitList(v)...)
				}
			case "digest":
			default:
				s.values[key] = strings.Join(values, ",")
			}
		}
		return s
	}
	forced := map[string]bool{}
	for _, name := range always {
		forced[name] = true
	}
	auditStructSent(reflect.ValueOf(params), forced, &s)
	return s
}

func auditStructSent(v reflect.Value, forced map[string]bool, s *auditSent) {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	for i := range v.NumField() {
		field, value := v.Type().Field(i), v.Field(i)
		if !field.IsExported() {
			continue
		}
		if field.Anonymous {
			auditStructSent(value, forced, s)
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		switch {
		case name == "" || name == "-" || name == "digest" || value.IsZero() && !forced[name]:
		case name == "delete" && value.Kind() == reflect.String:
			s.cleared = append(s.cleared, pvefield.SplitList(value.String())...)
		case name == "delete" && value.Kind() == reflect.Slice:
			for j := range value.Len() {
				s.cleared = append(s.cleared, value.Index(j).String())
			}
		default:
			s.values[name] = auditValueText(value)
		}
	}
}

// auditValueText is a field's value as the proxmox clients write it in a form:
// a number in decimal, a flag as 1 or 0, a list joined by commas.
func auditValueText(v reflect.Value) string {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	case reflect.Bool:
		if v.Bool() {
			return "1"
		}
		return "0"
	case reflect.Slice:
		items := make([]string, 0, v.Len())
		for j := range v.Len() {
			items = append(items, auditValueText(v.Index(j)))
		}
		return strings.Join(items, ",")
	}
	return v.String()
}

// names is every name sent and cleared, and each of also.
func (s auditSent) names(also ...string) []string {
	names := map[string]bool{}
	for name := range s.values {
		names[name] = true
	}
	for _, name := range slices.Concat(s.cleared, also) {
		if name != "" {
			names[name] = true
		}
	}
	return slices.Sorted(maps.Keys(names))
}

// changed is the names of what the write changes, beside before, the object as
// its handler read it just before writing it: a name sent whose value Proxmox
// stores otherwise than the one read, compared as the change observer compares
// them (kind, pvefield.Kind.Changed), and a name cleared that the object had,
// so a form sent whole names only what it changes. before nil — the read
// failed — is every name (auditFields marks it so).
func (s auditSent) changed(before map[string]json.RawMessage, kind pvefield.Kind) []string {
	if before == nil {
		return s.names()
	}
	names := map[string]bool{}
	for name := range s.values {
		if kind.Changed(name, before, s.values) {
			names[name] = true
		}
	}
	for _, name := range s.cleared {
		if raw := before[name]; len(raw) > 0 && string(raw) != "null" {
			names[name] = true
		}
	}
	return slices.Sorted(maps.Keys(names))
}

// auditFields is details with an update's fields: what its write changes
// (changed) or, when its object could not be read first, every field it sent,
// and fields_unverified, by which the observer's classifier takes the row to
// explain no field (internal/collector observer_explain.go, FieldsFrom).
func auditFields(details map[string]any, sent auditSent, before map[string]json.RawMessage, kind pvefield.Kind) map[string]any {
	return auditChanges(details, "fields", sent.changed(before, kind), before)
}

// auditChanges is details with names under key, and fields_unverified when the
// object could not be read first and names is every one sent.
func auditChanges(details map[string]any, key string, names []string, before map[string]json.RawMessage) map[string]any {
	details[key] = names
	if before == nil {
		details["fields_unverified"] = true
	}
	return details
}

// guestConfigDetails is a guest config write's details: fields, the values it
// sent, which a History shows (AuditLogAs records a secret among them as
// "(set)"), and changed, what the write changes beside before, the config as
// read just before (auditSent.changed), with what it reverts: a pending
// change dropped, which the config is read with. The observer's config rule
// explains only changed. skiplock is no key.
func guestConfigDetails(fields map[string]string, before map[string]json.RawMessage, kind pvefield.Kind) map[string]any {
	form := url.Values{}
	for k, v := range fields {
		if k != "revert" && k != "skiplock" {
			form.Set(k, v)
		}
	}
	changed := slices.Concat(auditSentOf(form).changed(before, kind), pvefield.SplitList(fields["revert"]))
	slices.Sort(changed)
	return auditChanges(map[string]any{"fields": fields}, "changed", slices.Compact(changed), before)
}

// readBefore reads the object an update is about to write, raw, for
// auditSent.changed — readBefore(ctx, client)(proxmox.ObjectSDN(kind, id)) —
// nil when it cannot, and the row then names every field the update sends.
func readBefore(ctx context.Context, client *proxmox.Client) func(proxmox.ObserveTarget, error) map[string]json.RawMessage {
	return func(target proxmox.ObserveTarget, err error) map[string]json.RawMessage {
		if err != nil {
			return nil
		}
		raw, err := client.ObserveRead(ctx, target)
		if err != nil {
			return nil
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			return nil
		}
		return obj
	}
}
