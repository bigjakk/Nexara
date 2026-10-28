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
					s.cleared = append(s.cleared, auditSplitList(v)...)
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
			s.cleared = append(s.cleared, auditSplitList(value.String())...)
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
// its handler read it just before writing it: a name sent whose value is not
// the one read, and a name cleared that the object had, so a form sent whole
// names only what it changes. absent is the value a field the object does not
// list has (Proxmox's default). before nil — the read failed — is every name.
func (s auditSent) changed(before map[string]json.RawMessage, absent map[string]string) []string {
	if before == nil {
		return s.names()
	}
	names := map[string]bool{}
	for name, sent := range s.values {
		read, listed := auditReadText(before[name])
		if !listed {
			read, listed = absent[name]
		}
		if !listed || auditNormal(read) != auditNormal(sent) {
			names[name] = true
		}
	}
	for _, name := range s.cleared {
		if _, had := auditReadText(before[name]); had {
			names[name] = true
		}
	}
	return slices.Sorted(maps.Keys(names))
}

// auditReadText is a field of a read as text: a string as it is, a number as
// written, a flag as 1 or 0; false when the field is missing or null.
func auditReadText(raw json.RawMessage) (string, bool) {
	var v any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if len(raw) == 0 || dec.Decode(&v) != nil || v == nil {
		return "", false
	}
	switch t := v.(type) {
	case string:
		return t, true
	case json.Number:
		return t.String(), true
	case bool:
		if t {
			return "1", true
		}
		return "0", true
	}
	return string(raw), true
}

// auditNormal is a value as compared: without the white space around it (a
// read comment ends in a newline), and a comma list without white space in it
// sorted, as Proxmox lists a node list in no order (Zones/Plugin.pm
// encode_value).
func auditNormal(v string) string {
	v = strings.TrimSpace(v)
	if !strings.Contains(v, ",") || strings.ContainsAny(v, " \t\n") {
		return v
	}
	items := strings.Split(v, ",")
	slices.Sort(items)
	return strings.Join(items, ",")
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

// auditSplitList splits a list as Proxmox does (PVE::Tools::split_list): on
// commas, semicolons and white space.
func auditSplitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' })
}
