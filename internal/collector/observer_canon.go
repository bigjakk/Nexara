package collector

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/crypto"
	"github.com/bigjakk/nexara/internal/pvefield"
)

// What the observer (observer.go) keeps of a reading, and how two readings are
// compared: never a value. Each field Proxmox reports is put in one canonical
// form, so the same value read twice compares equal however Proxmox happened to
// encode it, and the form is kept only as an HMAC tag (observeTagger), bound to
// the cluster, the object and the field.

// observeFieldName is the shape every Proxmox configuration key has. A key of
// any other shape — nothing Proxmox returns today — is folded into one field
// named "other", so a value can never reach the changes table dressed as a field
// name. Names are shape-checked, not held to a list: reading raw JSON exists to
// catch the fields Nexara does not model. That is safe because every endpoint
// read validates its keys against its JSON schema (PVE::JSONSchema), so a key
// is a property name Proxmox defines, never text a user typed.
var observeFieldName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// observeOtherField is where keys of an unexpected shape are compared, together.
const observeOtherField = "other"

// A field's canonical form, and a member list's ids, are pvefield's: an update
// handler that reads its object before writing it compares the two the same
// way (handlers' auditSent.changed).
var (
	canonicalValue = pvefield.Value
	canonicalSet   = pvefield.Set
)

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

// digest names the spec, for a kind's signature: a release that ignores
// another field, or compares one another way, re-baselines that kind once
// rather than reading the new form against the old as an edit.
func (spec fieldSpec) digest() string {
	part := func(tag string, set map[string]bool) string {
		keys := slices.Sorted(maps.Keys(set))
		return tag + "=" + strings.Join(keys, ",")
	}
	return observeSignature(part("ignore", spec.ignore), part("sets", spec.sets), part("members", spec.members))
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

// memberIDs is a member list's ids, sorted and once each (pvefield.MemberIDs).
var memberIDs = pvefield.MemberIDs

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
	made  atomic.Int64 // tags made, which a reading unchanged since the last pass makes none of (tagMemo)
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
	t.made.Add(1)
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
		if t := bytes.TrimSpace(v); len(t) > 0 && t[0] == '[' {
			var list []string
			if err := json.Unmarshal(t, &list); err != nil {
				return objectTags{}, err
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
// ones that are gone, counted as multisets, and of those kept how many are out
// of their old order. A rule edited in place is one in and one out; a rule
// moved is one moved — firewall rules apply in order, so a move is a change a
// create elsewhere must not explain.
type listCount struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
	Moved   int `json:"moved,omitempty"`
}

// tagDiff is how two readings of one object differ: the fields, sorted; for
// each list, how many items came and went; and the members that joined or
// left, as "<field>:<id>", which say who — a guest destroyed leaves its pool,
// and only that guest's destroy may explain it.
type tagDiff struct {
	fields       []string
	appeared     []string // of fields, the ones the last reading did not have
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
			if !ok {
				d.appeared = append(d.appeared, k)
			}
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
	slices.Sort(d.appeared)
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
	inCur := make(map[string]int, len(cur))
	for _, t := range cur {
		if left[t] > 0 {
			left[t]--
			inCur[t]++
			continue
		}
		c.Added++
	}
	for _, n := range left {
		c.Removed += n
	}
	// The kept items in their two orders: those not on the longest common
	// subsequence moved.
	var prevKept, curKept []string
	kept := maps.Clone(inCur)
	for _, t := range prev {
		if kept[t] > 0 {
			kept[t]--
			prevKept = append(prevKept, t)
		}
	}
	for _, t := range cur {
		if inCur[t] > 0 {
			inCur[t]--
			curKept = append(curKept, t)
		}
	}
	c.Moved = len(curKept) - lcsLen(prevKept, curKept)
	return c
}

// lcsLen is the length of a longest common subsequence of a and b, the kept
// items of a list in their two orders (multisetDiff).
func lcsLen(a, b []string) int { return lcsLenWithin(a, b, lcsMatchLimit) }

// lcsMatchLimit bounds the pairs lcsLenWithin matches exactly: two lists of one
// rule repeated n times hold n² of them.
const lcsMatchLimit = 1 << 20

// lcsLenWithin is Hunt–Szymanski: each item of b is matched with its places
// in a, latest first, and the longest strictly increasing run of places is the
// subsequence — O((n + r) log n) for r matching pairs, where the table is
// O(n·m) for every list. Past limit pairs each item of b takes the next unused
// place of its value in a: exact for distinct items, and for repeated ones at
// worst a moved duplicate counted again.
func lcsLenWithin(a, b []string, limit int) int {
	places := make(map[string][]int, len(a))
	for i, t := range a {
		places[t] = append(places[t], i)
	}
	pairs := 0
	for _, t := range b {
		pairs += len(places[t])
	}
	var run []int // run[k]: the least place ending an increasing run of k+1
	extend := func(place int) {
		k, _ := slices.BinarySearch(run, place)
		if k == len(run) {
			run = append(run, place)
		} else {
			run[k] = place
		}
	}
	if pairs <= limit {
		for _, t := range b {
			ps := places[t]
			for i := len(ps) - 1; i >= 0; i-- {
				extend(ps[i])
			}
		}
		return len(run)
	}
	next := make(map[string]int, len(places))
	for _, t := range b {
		if i := next[t]; i < len(places[t]) {
			next[t] = i + 1
			extend(places[t][i])
		}
	}
	return len(run)
}
