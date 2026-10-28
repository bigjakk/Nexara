// Package pvefield is how the fields of Proxmox VE's objects are compared: the
// one canonical form a value read is put in, so the same value read twice
// compares equal however Proxmox happened to encode it, and, for the kinds an
// update handler reads just before it writes (handlers' auditSent.changed),
// which of their fields hold an unordered list or other objects' ids and how
// Proxmox stores a value sent. The change observer (internal/collector) files
// a change when a field's canonical form moves; an update's audit row names a
// field when its write moves it. Both compare here, so a row names no field
// its write left as it was: a name too many would explain an edit made to that
// field in Proxmox in the meantime.
package pvefield

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
)

// Value renders one JSON value as it is compared: a string's text, a number's
// digits, a boolean as 1 or 0 — Perl's JSON writes the same option as "1", 1 or
// true depending on how the scalar was last used — and an array or object as
// JSON with its keys sorted and its scalars rendered the same way. A null is
// no value: the field is absent.
func Value(raw json.RawMessage) (string, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil || v == nil {
		return "", false
	}
	return canonicalAny(v), true
}

func canonicalAny(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "1"
		}
		return "0"
	default:
		b, err := json.Marshal(normalizeJSON(t))
		if err != nil {
			return ""
		}
		return string(b)
	}
}

// normalizeJSON turns every scalar inside an array or object into its canonical
// string and drops nulls; json.Marshal sorts the map keys.
func normalizeJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, x := range t {
			if x != nil {
				m[k] = normalizeJSON(x)
			}
		}
		return m
	case []any:
		out := make([]any, 0, len(t))
		for _, x := range t {
			if x != nil {
				out = append(out, normalizeJSON(x))
			}
		}
		return out
	default:
		return canonicalAny(t)
	}
}

// Set renders a list whose order means nothing: a comma list, or a JSON array,
// split, trimmed, deduplicated and sorted. Proxmox re-encodes many of these
// from a Perl hash on every request — join(',', keys %h) — so their order
// changes with nobody editing anything (HA group nodes, storage content and
// nodes, SDN zone nodes: Kind.Sets).
func Set(raw json.RawMessage) (string, bool) {
	var items []string
	if arr, ok := decodeArray(raw); ok {
		for _, item := range arr {
			if s, ok := Value(item); ok {
				items = append(items, s)
			}
		}
	} else {
		s, ok := Value(raw)
		if !ok {
			return "", false
		}
		for _, part := range strings.Split(s, ",") {
			if part = strings.TrimSpace(part); part != "" {
				items = append(items, part)
			}
		}
	}
	slices.Sort(items)
	items = slices.Compact(items)
	b, _ := json.Marshal(items)
	return string(b), true
}

func decodeArray(raw json.RawMessage) ([]json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, false
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(trimmed, &arr); err != nil {
		return nil, false
	}
	return arr, true
}

// memberID is the shape a member must have to be kept as it is: a guest's
// VMID, a storage, group or user id, an HA resource id, an SDN subnet's CIDR,
// an ACL grant (its role and propagate, then its grantee) — no space, no list
// separator, nothing a value could hide in at length. A user or a token, alone
// or as a grantee, takes every name pve-access-control takes for one
// (Auth/Plugin.pm user_regex [^\s:/]+ and realm_regex, AccessControl.pm
// token_subid_regex) but one holding ',' or ';': Proxmox's own lists split on
// those (PVE::Tools::split_list), so no list of Proxmox's can hold it either.
var memberID = regexp.MustCompile(`^(?:[A-Za-z0-9_-][A-Za-z0-9@.:!/_-]{0,191}|` +
	`(?:[A-Za-z0-9._-]{1,64}:[01]?:)?[^\s:/,;\p{Cc}\p{Cf}]{1,64}@[A-Za-z][A-Za-z0-9._-]{1,63}(?:![A-Za-z][A-Za-z0-9._-]{1,63})?)$`)

// IsMemberID reports whether id has the shape a member is kept in.
func IsMemberID(id string) bool { return memberID.MatchString(id) }

// MemberIDs is a member list's ids, sorted and once each: a JSON array, or a
// list separated as Proxmox separates them (PVE::Tools::split_list). False
// when it holds anything that is no id.
func MemberIDs(raw json.RawMessage) ([]string, bool) {
	s, ok := Set(raw)
	if !ok {
		return nil, false
	}
	var ids []string
	if err := json.Unmarshal([]byte(s), &ids); err != nil {
		return nil, false
	}
	out := []string{}
	for _, id := range ids {
		for _, part := range strings.FieldsFunc(id, func(r rune) bool { return r == ';' || r == ' ' }) {
			if !memberID.MatchString(part) {
				return nil, false
			}
			out = append(out, part)
		}
	}
	slices.Sort(out)
	return slices.Compact(out), true
}

// SplitList splits a list as Proxmox does (PVE::Tools::split_list, pve-common
// ParseUtils.pm): on NUL alone when it holds one, else on commas, semicolons
// and white space. Perl's split drops the empty fields at the end.
func SplitList(s string) []string {
	if strings.Contains(s, "\x00") {
		parts := strings.Split(s, "\x00")
		for len(parts) > 0 && parts[len(parts)-1] == "" {
			parts = parts[:len(parts)-1]
		}
		return parts
	}
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || isPerlSpace(r) })
}

// isPerlSpace is Perl's \s on bytes: space, tab, newline, carriage return,
// form feed and vertical tab.
func isPerlSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v'
}
