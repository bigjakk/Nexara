package collector

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/crypto"
)

// What the observer (observer.go) keeps of a reading, and how two readings are
// compared: never a value. Each field Proxmox reports is put in one canonical
// form, so the same value read twice compares equal however Proxmox happened to
// encode it, and the form is kept only as an HMAC tag (observeTagger), bound to
// the cluster, the object and the field.

// observeFieldName is the shape every Proxmox configuration key has. A key of
// any other shape — nothing Proxmox returns today — is folded into one field
// named "other", so a value can never reach the changes table dressed as a field
// name.
var observeFieldName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// observeOtherField is where keys of an unexpected shape are compared, together.
const observeOtherField = "other"

// canonicalValue renders one JSON value as the tag sees it: a string's text, a
// number's digits, a boolean as 1 or 0 — Perl's JSON writes the same option as
// "1", 1 or true depending on how the scalar was last used — and an array or
// object as JSON with its keys sorted and its scalars rendered the same way.
// A null is no value: the field is absent.
func canonicalValue(raw json.RawMessage) (string, bool) {
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

// canonicalSet renders a list whose order means nothing: a comma list, or a
// JSON array, split, trimmed, deduplicated and sorted. Proxmox re-encodes many
// of these from a Perl hash on every request — join(',', keys %h) — so their
// order changes with nobody editing anything (HA group nodes, storage content
// and nodes, SDN zone nodes: see the kinds in observer_kinds.go).
func canonicalSet(raw json.RawMessage) (string, bool) {
	var items []string
	if arr, ok := decodeArray(raw); ok {
		for _, item := range arr {
			if s, ok := canonicalValue(item); ok {
				items = append(items, s)
			}
		}
	} else {
		s, ok := canonicalValue(raw)
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

// fieldSpec says how one kind's fields are compared: which are dropped — run
// state, derived values, and Proxmox's digest of the whole file — which are
// unordered lists, and which are lists of other objects' ids (members).
type fieldSpec struct {
	ignore  map[string]bool
	sets    map[string]bool
	members map[string]bool
}

func newFieldSpec(ignore, sets []string) fieldSpec {
	spec := fieldSpec{ignore: map[string]bool{"digest": true}, sets: map[string]bool{}, members: map[string]bool{}}
	for _, f := range ignore {
		spec.ignore[f] = true
	}
	for _, f := range sets {
		spec.sets[f] = true
	}
	return spec
}

// withMembers is spec with fields that list other objects — a backup job's
// guests, an HA rule's resources, a user's groups — kept as the ids
// (observedObject.members) rather than as one tag, so a change can say which
// joined and which left.
func (spec fieldSpec) withMembers(fields ...string) fieldSpec {
	out := fieldSpec{ignore: spec.ignore, sets: spec.sets, members: map[string]bool{}}
	for _, f := range fields {
		out.members[f] = true
	}
	return out
}

// split is one object's compared fields, by name, in canonical form, and its
// member lists. A member list holding something that is no id is compared as a
// plain set instead, so nothing but ids is ever kept as it is.
func (spec fieldSpec) split(item map[string]json.RawMessage) (fields map[string]string, members map[string][]string) {
	rest := item
	for f := range spec.members {
		raw, ok := item[f]
		if !ok {
			continue
		}
		ids, ok := memberIDs(raw)
		if !ok {
			continue
		}
		if members == nil {
			members = map[string][]string{}
			rest = make(map[string]json.RawMessage, len(item))
			for k, v := range item {
				rest[k] = v
			}
		}
		members[f] = ids
		delete(rest, f)
	}
	return spec.canonicalFields(rest), members
}

// memberID is the shape a member must have to be kept as it is: a guest's
// VMID, a storage, group or user id, an HA resource id — no separator, no
// space, nothing a value could hide in at length.
var memberID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@._:!-]{0,127}$`)

// memberIDs is a member list's ids, sorted and once each: a JSON array, or a
// list separated as Proxmox separates them (PVE::Tools::split_list).
func memberIDs(raw json.RawMessage) ([]string, bool) {
	s, ok := canonicalSet(raw)
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

// canonicalFields is one object's compared fields, by name, in canonical form.
// Keys of an unexpected shape are folded into observeOtherField, as one object.
func (spec fieldSpec) canonicalFields(item map[string]json.RawMessage) map[string]string {
	out := make(map[string]string, len(item))
	var other map[string]json.RawMessage
	for k, raw := range item {
		if spec.ignore[k] {
			continue
		}
		if !observeFieldName.MatchString(k) {
			if other == nil {
				other = map[string]json.RawMessage{}
			}
			other[k] = raw
			continue
		}
		var (
			v  string
			ok bool
		)
		if spec.sets[k] {
			v, ok = canonicalSet(raw)
		} else {
			v, ok = canonicalValue(raw)
		}
		if ok {
			out[k] = v
		}
	}
	if other != nil {
		b, _ := json.Marshal(other)
		if v, ok := canonicalValue(b); ok {
			out[observeOtherField] = v
		}
	}
	return out
}

// Tag purposes (crypto.Tag): the key id names the key the stored tags were made
// under, so a new ENCRYPTION_KEY re-baselines instead of reporting every field
// of every object as changed.
const (
	observeFieldPurpose = "nexara observed object field v1"
	observeKeyIDPurpose = "nexara observed object key id v1"
)

// observeTagLen is how much of the HMAC is kept: 128 bits says "this changed"
// with a collision chance no reading will ever meet.
const observeTagLen = 16

// observeTagger makes the tags. The key is ENCRYPTION_KEY, through the HKDF
// subkey crypto.Tag derives for the purpose; it is not in the database, so a
// tag cannot be tested against a guessed value from a dump, as a plain digest
// of a short password could.
type observeTagger struct {
	key   string
	keyID string
}

func newObserveTagger(encryptionKey string) (*observeTagger, error) {
	id, err := crypto.Tag(encryptionKey, observeKeyIDPurpose, nil)
	if err != nil {
		return nil, fmt.Errorf("observer key: %w", err)
	}
	return &observeTagger{key: encryptionKey, keyID: hex.EncodeToString(id[:8])}, nil
}

// tag is the tag of one field's canonical value. The cluster, kind, object and
// field are part of the message, so one password set on two guests, or one
// value in two fields, gives unrelated tags.
func (t *observeTagger) tag(cluster uuid.UUID, kind, objectID, field, value string) (string, error) {
	mac, err := crypto.Tag(t.key, observeFieldPurpose, lengthPrefixed(cluster.String(), kind, objectID, field, value))
	if err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(mac[:observeTagLen]), nil
}

// lengthPrefixed encodes the parts so that no two different tuples encode to the
// same bytes (crypto.Tag's contract): each part is preceded by its length.
func lengthPrefixed(parts ...string) []byte {
	var buf bytes.Buffer
	for _, p := range parts {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(p)))
		buf.Write(n[:])
		buf.WriteString(p)
	}
	return buf.Bytes()
}

// observeSignature condenses what a reading was taken under — the key id, the
// token's privileges, the Proxmox versions that answered — into the short id
// stored with each object. Not secret; hashed only to keep it short.
func observeSignature(parts ...string) string {
	sum := sha256.Sum256(lengthPrefixed(parts...))
	return hex.EncodeToString(sum[:8])
}

// objectTags is one object's stored reading: each scalar field's tag, each
// ordered list's tags in order (a rule set, whose rules have no id of their
// own), and each member list's ids, which are other objects' names, not values
// (fieldSpec.withMembers).
type objectTags struct {
	scalar  map[string]string
	lists   map[string][]string
	members map[string][]string
}

// storedMembers is how a member list is stored: {"members": [ids]}, which no
// tag or tag list can be mistaken for.
type storedMembers struct {
	Members []string `json:"members"`
}

func (o objectTags) MarshalJSON() ([]byte, error) {
	m := make(map[string]any, len(o.scalar)+len(o.lists)+len(o.members))
	for k, v := range o.scalar {
		m[k] = v
	}
	for k, v := range o.lists {
		m[k] = v
	}
	for k, v := range o.members {
		m[k] = storedMembers{Members: v}
	}
	return json.Marshal(m)
}

func parseObjectTags(raw json.RawMessage) (objectTags, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return objectTags{}, err
	}
	out := objectTags{scalar: map[string]string{}, lists: map[string][]string{}, members: map[string][]string{}}
	for k, v := range m {
		if t := bytes.TrimSpace(v); len(t) > 0 && t[0] == '{' {
			var sm storedMembers
			if err := json.Unmarshal(t, &sm); err != nil {
				return objectTags{}, err
			}
			out.members[k] = sm.Members
			continue
		}
		if arr, ok := decodeArray(v); ok {
			list := make([]string, 0, len(arr))
			for _, item := range arr {
				var s string
				if err := json.Unmarshal(item, &s); err != nil {
					return objectTags{}, err
				}
				list = append(list, s)
			}
			out.lists[k] = list
			continue
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return objectTags{}, err
		}
		out.scalar[k] = s
	}
	return out, nil
}

// listCount is how an ordered list changed: the items that are new, and the
// ones that are gone, counted as multisets. A rule edited in place is one of
// each; a list only reordered is neither, and still a change.
type listCount struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

// tagDiff is how two readings of one object differ: the fields, sorted; for
// each list, how many items came and went; and the members that joined or
// left, as "<field>:<id>", which say who — a guest destroyed leaves its pool,
// and only that guest's destroy may explain it.
type tagDiff struct {
	fields       []string
	counts       map[string]listCount
	joined, left []string
}

// diffTags compares two readings of one object: added, removed or changed
// fields alike. Lists are counted; member lists are counted and named.
func diffTags(prev, cur objectTags) tagDiff {
	var d tagDiff
	for k, v := range cur.scalar {
		if old, ok := prev.scalar[k]; !ok || old != v {
			d.fields = append(d.fields, k)
		}
	}
	for k := range prev.scalar {
		if _, ok := cur.scalar[k]; !ok {
			d.fields = append(d.fields, k)
		}
	}
	for _, k := range unionKeys(prev.lists, cur.lists) {
		if slices.Equal(prev.lists[k], cur.lists[k]) {
			continue
		}
		d.fields = append(d.fields, k)
		d.count(k, multisetDiff(prev.lists[k], cur.lists[k]))
	}
	for _, k := range unionKeys(prev.members, cur.members) {
		joined, left := setDiff(prev.members[k], cur.members[k])
		if len(joined) == 0 && len(left) == 0 {
			continue
		}
		d.fields = append(d.fields, k)
		d.count(k, listCount{Added: len(joined), Removed: len(left)})
		d.joined = append(d.joined, prefixed(k, joined)...)
		d.left = append(d.left, prefixed(k, left)...)
	}
	slices.Sort(d.fields)
	d.fields = slices.Compact(d.fields)
	slices.Sort(d.joined)
	slices.Sort(d.left)
	return d
}

func (d *tagDiff) count(field string, c listCount) {
	if d.counts == nil {
		d.counts = map[string]listCount{}
	}
	d.counts[field] = c
}

// allMembers is every member of a reading, as "<field>:<id>": what joins with
// an object added, and what leaves with one removed.
func allMembers(members map[string][]string) []string {
	n := 0
	for _, ids := range members {
		n += len(ids)
	}
	out := make([]string, 0, n)
	for k, ids := range members {
		out = append(out, prefixed(k, ids)...)
	}
	slices.Sort(out)
	return out
}

func prefixed(field string, ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, field+":"+id)
	}
	return out
}

// setDiff is what is in cur and not in prev, and what is in prev and not in
// cur; both are sorted sets.
func setDiff(prev, cur []string) (joined, left []string) {
	for _, id := range cur {
		if _, found := slices.BinarySearch(prev, id); !found {
			joined = append(joined, id)
		}
	}
	for _, id := range prev {
		if _, found := slices.BinarySearch(cur, id); !found {
			left = append(left, id)
		}
	}
	return joined, left
}

func unionKeys(a, b map[string][]string) []string {
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	return keys
}

func multisetDiff(prev, cur []string) listCount {
	left := make(map[string]int, len(prev))
	for _, t := range prev {
		left[t]++
	}
	var c listCount
	for _, t := range cur {
		if left[t] > 0 {
			left[t]--
			continue
		}
		c.Added++
	}
	for _, n := range left {
		c.Removed += n
	}
	return c
}
