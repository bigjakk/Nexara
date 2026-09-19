package api

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// Every endpoint carries a Group, and until this file nothing said which
// Groups exist.
//
// Group is not a label, it is the section structure of the generated API
// documentation: api_docs.go copies it onto each APIEndpoint, the handler
// sorts the catalog by it, and /settings/api-docs renders one collapsible
// card per distinct value. Two spellings of the same concept therefore do
// not merge into one section — they produce two, each holding half the
// routes, and an operator hunting for an endpoint opens the wrong card and
// concludes it does not exist. Nothing 500s, nothing logs, and the docs look
// perfectly healthy from the inside.
//
// Register (registry.go) refuses a BLANK Group, which is the only part of
// this that was ever checked. A misspelt one — "Node Mgmt", "Backups",
// "Authentication " with a trailing space — registers happily.
//
// What the first run of this guard found, worth recording because neither
// fact is visible from reading any single registry file:
//
//   - 37 distinct Groups over 535 declarations, and exactly ONE genuine
//     duplicate among them: "Node Management" (3 routes, the APT repository
//     trio in registry_apt_repositories.go) alongside "Nodes" (45). The
//     tempting reading — that "Nodes" is read-only node data and "Node
//     Management" is operations on a node — does not survive looking: Nodes
//     already held reboot, shutdown, evacuate, maintenance, the ZFS/LVM disk
//     lifecycle, PUT dns and PUT time. The three were merged into "Nodes".
//   - The near-duplicates that are NOT duplicates, each deliberately
//     distinct and each left alone: "Roles & Permissions" (Nexara's own
//     RBAC) vs "Proxmox Access Control" (PVE-side users, roles and ACLs on a
//     cluster) vs "User Management" (Nexara's own user accounts); "API Keys"
//     (a caller's own keys, manage:api_key) vs the two admin key routes
//     filed under "User Management" (every user's keys, manage:user — the
//     gating difference is the whole point of the split, and
//     registry_api_keys.go says so); "Authentication" vs "Two-Factor
//     Authentication"; "Networks" (a node's interface CONFIG — bridges,
//     bonds, VLANs, OVS ports) vs "SDN"
//     (Proxmox's software-defined zones, vnets and subnets).
//
// SCOPE. A Group reaches the docs payload from one of three places, and this
// guard bounds the first two:
//
//  1. A registry DECLARATION (registry_*.go). This is where new endpoints
//     are added, so it is the source with the most traffic.
//  2. An OVERLAY entry — handlers.endpointMeta, the curated map for the
//     legacy routes still mounted in router.go, which have no declaration to
//     carry a Group. It was unguarded until this file learned to read it,
//     and the gap was not theoretical: four of its entries exist precisely
//     BECAUSE their section was wrong, and a fifth misspelling added
//     alongside them would have rendered its own one-route card with the
//     whole package still green.
//  3. DERIVATION by handlers.groupFromPath, for a route in neither. A
//     derived value cannot be held to a vocabulary — it is whatever the
//     second path segment title-cases to — so the check that applies to it
//     is a different one: TestGuard_NoRouteFallsThroughToADerivedGroup
//     (api_docs_drift_test.go) asserts that only a reviewed list of routes
//     is allowed to derive at all. The two guards meet there: every route
//     that guard says states a section, this guard says states a CANONICAL
//     one.
//
// The overlay is read through handlers.EndpointMetaGroups rather than
// re-declared here, for the reason the whole file exists: a second copy of a
// vocabulary is the thing being guarded against, and a second copy of the
// INPUT to the vocabulary is the same mistake one level down.

// canonicalGroups is the Group vocabulary. A route may name one of these and
// nothing else, whether it says so in a declaration or in the overlay.
//
// The value is a one-line note on what belongs in the section. It is there
// so that adding a group costs a sentence of thought about whether the
// routes really need a section of their own, and so that a reader deciding
// where a new endpoint goes has something to decide against. The guard only
// reads the keys.
//
// TO ADD A GROUP: add one line here. That is deliberately the whole
// procedure — one line, in one place, visible in the diff, and reviewable
// on the question the line itself asks ("is this really a new section, or
// is it a second spelling of one of the 37 below?"). The alternative, which
// is what this file replaces, is that a new spelling costs nothing and
// nobody finds out.
//
// Synthetic Groups declared by tests ("Test", "Widgets", "Auth" in
// registry_request_test.go, registry_chain_test.go and
// api_docs_payload_test.go) are absent on purpose. Those endpoints are
// registered against a test-local Registry and never reach the Server's,
// which is the only one this guard reads — so they cost nothing here, and
// listing them would let a real endpoint borrow one.
var canonicalGroups = map[string]string{
	// The one section no DECLARATION names, and the reason it is here rather
	// than folded into "Settings" — which does act as the app-level
	// catch-all, holding /version, /changelog, /search and branding.
	//
	// It is not a second spelling of "Settings", which is the question this
	// map asks of a new entry. Settings holds what an operator CONFIGURES,
	// plus two build-identity reads. GET /api/v1/api-docs is the index of
	// the document it appears in — the one endpoint a reader reaches for in
	// order to find every other endpoint — and filing the index inside a
	// section is the single placement that makes it least findable, which is
	// the entire justification for Group existing.
	//
	// Keeping it also changes nothing that ships. Extending a guard is not
	// an occasion to move a rendered docs section; that would be a guard
	// writing its own baseline.
	//
	// Its presence is load-bearing beyond the route itself:
	// TestGuard_APIDocumentationIsNamedOnlyByTheOverlay pins that no
	// declaration names it, which makes this line a standing witness, over
	// production data, that the "every canonical entry is used" direction
	// counts OVERLAY entries and not just declarations. Fold it into
	// Settings and every canonical entry becomes witnessable from
	// declarations alone — at which point that half could silently revert to
	// declaration-only and the suite would stay green.
	"API Documentation":   "The generated API reference itself.",
	"API Keys":            "A caller's own API keys: mint, list, revoke.",
	"Alerts":              "Nexara's alert rules and the alerts they raise.",
	"Audit Log":           "The audit trail and its syslog forwarding.",
	"Authentication":      "Login, sessions, profile, and the LDAP and OIDC identity providers.",
	"Backup":              "Proxmox backup jobs, PBS servers and datastores, and Veeam.",
	"Ceph":                "A cluster's Ceph status, OSDs, pools, monitors, CephFS and CRUSH rules.",
	"Certificates":        "ACME accounts and plugins, and per-node certificate ordering, renewal and revocation.",
	"Clusters":            "Cluster registration, connection config, options, tags and description.",
	"Containers":          "LXC container lifecycle, config, snapshots, disks and migration.",
	"DRS":                 "The Distributed Resource Scheduler: config, rules, evaluation and history.",
	"Favorites":           "A user's pinned resources.",
	"Firewall":            "Cluster-, VM- and template-level firewall rules, aliases, IP sets and options.",
	"Guest Snapshots":     "The cross-cluster snapshot inventory and its resync.",
	"Guest Tools":         "Windows guest-agent detection, policy and updates.",
	"High Availability":   "HA resources, groups, rules, manager status, and cluster-wide arm/disarm.",
	"Maintenance Windows": "Windows during which alerting is suppressed.",
	"Metrics":             "Collected cluster, node and guest metrics, and external metric servers.",
	"Migrations":          "Nexara's orchestrated cross-cluster migration jobs.",
	// NOT "physical interfaces": the create/edit routes accept only virtual
	// types (creatableNetworkInterfaceTypes, internal/proxmox/client_network.go
	// — bridge, bond, vlan, OVS*), "eth" is editable but never creatable, and
	// the read-only physical listings (…/network-interfaces, …/bridges) live in
	// "Nodes". A reader with a new bridge route who believed "physical" would
	// conclude it does not fit here and invent a 38th group — the exact outcome
	// this map exists to prevent.
	"Networks":               "A node's network interface configuration: bridges, bonds, VLANs and OVS ports, and applying or reverting the config.",
	"Nodes":                  "Everything scoped to one node: hardware, disks, services, logs, time, DNS, APT repositories, firewall, and power and maintenance actions.",
	"Notification Channels":  "Notification delivery targets and the dead-letter queue.",
	"Proxmox Access Control": "PVE-side users, tokens, groups, roles, realms and ACLs on a cluster.",
	"Replication":            "Proxmox storage replication jobs.",
	"Reports":                "Report generation, schedules and runs.",
	"Roles & Permissions":    "Nexara's own RBAC: roles, permissions and role assignment.",
	// The packages listing is here rather than in "Nodes" because it is filed by
	// consumer, not by resource — worth knowing, since it is the sibling of the
	// apt-repository routes that DID move to "Nodes".
	"Rolling Updates":           "Rolling node updates, a node's available packages, and the SSH credentials and known hosts they run over.",
	"SDN":                       "Proxmox software-defined networking: zones, vnets, subnets, controllers, IPAMs and DNS.",
	"Security":                  "CVE scanning, its schedule and notifications, and the security posture rollup.",
	"Settings":                  "Instance-wide settings, branding, search, version and changelog.",
	"Storage":                   "Storage pool config and content, uploads, downloads and scans.",
	"Tasks":                     "Proxmox task status and logs, Nexara's task history, and scheduled tasks.",
	"Two-Factor Authentication": "TOTP enrolment, verification and recovery codes.",
	"User Management":           "Nexara user accounts, and the admin view of every user's API keys.",
	"VM Import":                 "Importing guests from ESXi, OVA or a raw disk.",
	"Virtual Machines":          "QEMU guest lifecycle, config, snapshots, disks, media, migration, pools and folders.",
	"virtio-win":                "The virtio-win driver ISO: releases, mirror and per-cluster downloads.",
}

// The two places a Group value can be written. They are separate strings
// because a finding has to send the reader to the right file, and because
// the remedies are not the same: a declaration's Group can be corrected in
// place, or the route moved to the section it belongs in, while an overlay
// entry generally has only the first of those — it exists because its route
// is one the registry cannot express.
const (
	groupSourceDeclaration = "a declaration"
	groupSourceOverlay     = "an overlay entry"
)

// groupSourceRemedy is the half of an unknown-group finding that depends on
// where the value was written. Splitting it out is what keeps the two
// halves of the message from being written as one vague sentence covering
// neither case.
var groupSourceRemedy = map[string]string{
	groupSourceDeclaration: "The value is on a DECLARATION, in internal/api/registry_*.go. " +
		"Correct the Group there to an entry that already exists, or, if this really is a new " +
		"section, add one line to canonicalGroups saying what belongs in it.",
	groupSourceOverlay: "The value is on an OVERLAY entry, in endpointMeta " +
		"(internal/api/handlers/api_docs.go). Correct the Group there. Note that migrating the route " +
		"into the registry is generally NOT one of the options — an overlay entry exists because the " +
		"route is one of the legacy ones the registry cannot express, and legacy_route_ratchet_test.go " +
		"records which limitation holds each — so either the spelling matches an entry that already " +
		"exists, or canonicalGroups grows one line saying what belongs in the new section.",
}

// groupUse is one route's claim on a Group value, plus where the claim was
// written. The source travels with the value because it is not recoverable
// afterwards: the two sources produce the same kind of string, and
// "Virtual machines" reads identically whichever file it came from while
// the fix lives in a different one.
type groupUse struct {
	source string
	route  string
	group  string
}

// groupUses collects every Group value that can reach the docs payload from
// a source with a vocabulary — declarations and the curated overlay.
//
// An overlay entry with a BLANK Group is skipped rather than reported,
// because a blank one states nothing: GetDocs falls through to
// groupFromPath for it, exactly as if the entry carried no Group field at
// all, so there is no spelling here to hold to a vocabulary. That is also
// why this file needs no floor on how many overlay entries it saw —
// blanking them all would empty this walk silently, but it would light up
// TestGuard_NoRouteFallsThroughToADerivedGroup for every one of those
// routes, which is the guard that owns the "states nothing" case.
//
// An overlay entry for a route the registry ALSO declares is included even
// though GetDocs would never render it (it takes a declaration whole and
// never consults the overlay for a declared route). A vocabulary is a rule
// about what may be written, and an inert entry is one deleted declaration
// away from being the value an operator reads. The cost of including it is
// that such an entry also counts toward the "some route names this section"
// direction, so in principle it could keep a canonical entry nominally alive
// while nothing renders it — which needs the overlay and the declaration to
// name DIFFERENT groups for the same route, and
// TestGuard_EndpointMetaKeysAreExactlyTheSurvivingSet (api_docs_drift_test.go)
// already pins the overlay's key set against exactly that kind of unreviewed
// growth.
func groupUses(eps []Endpoint, overlay map[string]string) []groupUse {
	uses := make([]groupUse, 0, len(eps)+len(overlay))

	for _, e := range eps {
		uses = append(uses, groupUse{
			source: groupSourceDeclaration,
			route:  e.Method + " " + e.Path,
			group:  e.Group,
		})
	}

	// Sorted so that a findings list is stable run to run; the overlay is a
	// map and ranges in a different order every time.
	for _, key := range slices.Sorted(maps.Keys(overlay)) {
		if overlay[key] == "" {
			continue
		}
		uses = append(uses, groupUse{
			source: groupSourceOverlay,
			route:  key,
			group:  overlay[key],
		})
	}

	return uses
}

// groupVocabularyFindings checks Group in both directions and reports what
// it finds, plus how many uses it actually looked at.
//
// Both directions matter, and the second is the one that decays quietly. A
// route naming a group nobody canonicalised is the obvious failure. A
// canonical entry NO route names is the residue of a rename — the merge
// happened, the old spelling stopped being used, and the list kept
// advertising a section that renders empty and invites the next author to
// file something under it. Neither direction is checkable from the other.
//
// canonical is a parameter rather than a package reference so that a
// synthetic vocabulary can prove the detector fails when it should; uses is
// a parameter for the same reason. examined is returned for the
// anti-vacuity assertions the callers make — a guard over an empty input
// reports zero findings and is indistinguishable, from the outside, from a
// clean one.
func groupVocabularyFindings(uses []groupUse, canonical map[string]string) (findings []string, examined int) {
	counts := make(map[string]int, len(canonical))

	// Keyed on the value AND the source, so one bad spelling written in both
	// places produces two findings rather than one listing routes from two
	// files under a remedy that is right for only half of them.
	type unknownKey struct{ group, source string }
	unknown := make(map[unknownKey][]string)

	for _, u := range uses {
		examined++
		if _, ok := canonical[u.group]; ok {
			counts[u.group]++
			continue
		}
		k := unknownKey{group: u.group, source: u.source}
		unknown[k] = append(unknown[k], u.route)
	}

	badKeys := slices.Collect(maps.Keys(unknown))
	slices.SortFunc(badKeys, func(a, b unknownKey) int {
		if c := strings.Compare(a.group, b.group); c != 0 {
			return c
		}
		return strings.Compare(a.source, b.source)
	})

	for _, k := range badKeys {
		routes := unknown[k]
		sort.Strings(routes)
		findings = append(findings, fmt.Sprintf(
			"Group %q is not in canonicalGroups. It comes from %s, on %d route(s): %s. "+
				"A second spelling of an existing concept does not merge into one docs section — it "+
				"produces two, each holding half the routes, and an operator hunting for an endpoint "+
				"opens the wrong card and concludes it does not exist. %s",
			k.group, k.source, len(routes), strings.Join(routes, ", "), groupSourceRemedy[k.source]))
	}

	for _, g := range slices.Sorted(maps.Keys(canonical)) {
		if counts[g] > 0 {
			continue
		}
		findings = append(findings, fmt.Sprintf(
			"canonicalGroups lists %q, but no declaration and no overlay entry names it. Either the "+
				"routes that used to be in that section were renamed into another one and this entry is "+
				"the leftover — delete the line — or the section is real and its routes are spelling it "+
				"differently, which the findings above would name.",
			g))
	}

	return findings, examined
}

// TestGuard_EveryRouteGroupIsCanonical is the production guard, over every
// Group buildRegistry declares and every one the curated overlay states.
//
// It reads Registry.Endpoints() rather than app.GetRoutes() because the
// declaration is what api_docs.go reads, and Fiber's own route table carries
// no Group at all — there is nothing to check on that side. The overlay half
// reads handlers.EndpointMetaGroups for the mirror-image reason: the rendered
// payload cannot tell a stated section from a derived one, because the two
// are frequently the same string.
func TestGuard_EveryRouteGroupIsCanonical(t *testing.T) {
	eps := newRouteStubServer(t).registry.Endpoints()
	overlay := handlers.EndpointMetaGroups()

	findings, examined := groupVocabularyFindings(groupUses(eps, overlay), canonicalGroups)

	// Anti-vacuity, part one: the registry. A stub server that registered
	// nothing, or an Endpoints() that returned an empty slice, produces zero
	// findings over a vocabulary check that compared nothing and reports
	// PASS — which is exactly what a healthy registry reports. This package
	// has shipped guards that passed while incapable of failing; refuse to be
	// the next one.
	//
	// A floor on the registry is safe to write as an absolute, because the
	// registry is where every new endpoint is declared and
	// TestGuard_LegacyRouteSetOnlyShrinks pushes routes INTO it. It cannot
	// legitimately reach zero.
	if len(eps) == 0 {
		t.Fatal("the registry declares 0 endpoints; the guard would pass vacuously over an empty registry")
	}

	// Anti-vacuity, part two: the sources. The floor that matters here is
	// not a COUNT — the overlay is meant to shrink as legacy routes are
	// migrated, and a floor that fails when the last entry goes is a floor
	// somebody deletes — but the RELATION between the inputs and what the
	// walk looked at. Every declaration is one use, every overlay entry that
	// states a section is one more, and nothing else is. If groupUses ever
	// stops reading a source, this arithmetic stops holding while both the
	// registry and the findings list look exactly as healthy as they do now.
	want := len(eps)
	for _, group := range overlay {
		if group != "" {
			want++
		}
	}
	if examined != want {
		t.Fatalf("the walk examined %d group uses, but the registry declares %d endpoints and the "+
			"overlay states %d sections — a source is being skipped, and every route it covers is a "+
			"route this guard silently stops checking while still reporting a clean vocabulary",
			examined, len(eps), want-len(eps))
	}

	for _, f := range findings {
		t.Error(f)
	}
}

// TestGuard_EveryRouteGroupIsCanonical_RejectsAnInventedDeclaredSpelling
// runs the production detector over the REAL inputs with one extra
// declaration spliced in, naming a group nothing canonicalised.
//
// It exists because the registry is clean, and a clean guard is where
// vacuity hides: a detector that had stopped comparing anything would report
// zero findings over 548 uses and look identical to the test above.
// Running the production function over the production input plus one
// known-bad declaration is what tells the two apart, permanently — a later
// refactor that breaks the comparison fails HERE even though the registry
// itself is still clean.
func TestGuard_EveryRouteGroupIsCanonical_RejectsAnInventedDeclaredSpelling(t *testing.T) {
	eps := newRouteStubServer(t).registry.Endpoints()
	overlay := handlers.EndpointMetaGroups()

	baseline, examined := groupVocabularyFindings(groupUses(eps, overlay), canonicalGroups)
	if examined == 0 {
		t.Fatal("examined 0 group uses over the real registry and overlay")
	}
	if len(baseline) != 0 {
		t.Fatalf("the real inputs already have %d group finding(s) (%v); this test measures the "+
			"delta from a clean baseline, so fix those first", len(baseline), baseline)
	}

	// "Node Mgmt" is the shape this whole file is about: not a typo a
	// compiler could catch, but a plausible second spelling of a section
	// that already exists.
	bogus := Endpoint{
		Method:      "GET",
		Path:        "/api/v1/clusters/:cluster_id/nodes/:node_name/synthetic-probe",
		Description: "Synthetic probe endpoint.",
		Group:       "Node Mgmt",
	}
	findings, mutated := groupVocabularyFindings(groupUses(append(slices.Clone(eps), bogus), overlay), canonicalGroups)

	if mutated != examined+1 {
		t.Fatalf("examined %d group uses, want %d", mutated, examined+1)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want exactly 1: %v", len(findings), findings)
	}
	// The finding has to be actionable on its own: the value that is wrong,
	// the route that carries it, and — because the fix lives in a different
	// file for each source — which source wrote it.
	if !strings.Contains(findings[0], `"Node Mgmt"`) {
		t.Errorf("the finding does not name the offending group value: %s", findings[0])
	}
	if !strings.Contains(findings[0], bogus.Path) {
		t.Errorf("the finding does not name the offending route: %s", findings[0])
	}
	if !strings.Contains(findings[0], groupSourceDeclaration) {
		t.Errorf("the finding does not identify the source as a declaration: %s", findings[0])
	}
	if !strings.Contains(findings[0], "registry_") {
		t.Errorf("the finding does not name the files a declaration lives in: %s", findings[0])
	}
	// The "and not the other source" half is checked against the other
	// REMEDY rather than the other LABEL. Scanning for the two-word label
	// would couple this test to the prose of a paragraph it is not about:
	// reword the overlay remedy to mention "a declaration" in passing and
	// this would fire on a finding that is perfectly correct.
	if strings.Contains(findings[0], groupSourceRemedy[groupSourceOverlay]) {
		t.Errorf("the finding carries the overlay remedy as well as the declaration one: %s", findings[0])
	}
	// And the attribution itself is checked where it is decided, not by
	// reading it back out of a rendered sentence.
	for _, u := range groupUses(append(slices.Clone(eps), bogus), overlay) {
		if u.route == bogus.Method+" "+bogus.Path && u.source != groupSourceDeclaration {
			t.Errorf("groupUses labelled the spliced declaration %q, want %q", u.source, groupSourceDeclaration)
		}
	}
}

// TestGuard_EveryRouteGroupIsCanonical_RejectsAnInventedOverlaySpelling is
// the same proof for the source that had no guard at all until this change.
//
// The mutation is the one that was live an hour before this test was
// written: four routes had just been given overlay entries BECAUSE their
// derived section was wrong, and at that moment a fifth entry misspelling
// "Virtual Machines" as "Virtual machines" would have rendered its own
// one-route card with every test in the package still green. The overlay is
// also the source where this is most likely: its entries are hand-typed
// strings with no declaration next door to copy from.
//
// The entry to mutate is CHOSEN rather than named, for the reason the
// production guard's second floor gives: the overlay exists only for the
// legacy routes and the legacy set only shrinks, so a test that hard-codes
// one of its keys fails on the day that route migrates — and on the day the
// last entry goes there is no "pick another one", only deleting the test,
// which is how a check gets deleted instead of understood. Skipping on an
// empty overlay is the same answer TestGuard_GroupVocabularyAntiVacuityTriggerIsReachable
// gives to the same day.
func TestGuard_EveryRouteGroupIsCanonical_RejectsAnInventedOverlaySpelling(t *testing.T) {
	eps := newRouteStubServer(t).registry.Endpoints()
	overlay := handlers.EndpointMetaGroups()

	baseline, examined := groupVocabularyFindings(groupUses(eps, overlay), canonicalGroups)
	if examined == 0 {
		t.Fatal("examined 0 group uses over the real registry and overlay")
	}
	if len(baseline) != 0 {
		t.Fatalf("the real inputs already have %d group finding(s) (%v); this test measures the "+
			"delta from a clean baseline, so fix those first", len(baseline), baseline)
	}

	// Two criteria, both of them things the assertions below need rather than
	// preferences. The Group must be named by at least one OTHER use, so that
	// misspelling this one leaves the section populated, the stale direction
	// stays quiet and exactly one finding survives. And it must contain an
	// upper-case letter, so that lower-casing it is a real mutation — a
	// section like "virtio-win" is already lower case and would mutate to
	// itself. Sorted iteration so the choice is the same every run — when the
	// test was written it picked the storage-content delete, DELETE
	// /api/v1/clusters/:cluster_id/storage/:storage_id/content/*, "Storage"
	// -> "storage". The vm-folders case the doc comment tells the story of is
	// the same shape, one key further down the sorted list.
	counts := make(map[string]int)
	for _, u := range groupUses(eps, overlay) {
		counts[u.group]++
	}
	var key, real string
	for _, k := range slices.Sorted(maps.Keys(overlay)) {
		g := overlay[k]
		if g != "" && counts[g] > 1 && strings.ToLower(g) != g {
			key, real = k, g
			break
		}
	}
	if key == "" {
		t.Skip("no overlay entry states a mixed-case Group that another route also names, so there " +
			"is no entry a case mutation would flip into exactly one finding; nothing to prove here " +
			"until a legacy route states one again")
	}

	// Case, not spelling: the shape a reviewer's eye slides over.
	misspelt := strings.ToLower(real)
	mutatedOverlay := maps.Clone(overlay)
	mutatedOverlay[key] = misspelt

	findings, mutated := groupVocabularyFindings(groupUses(eps, mutatedOverlay), canonicalGroups)

	if mutated != examined {
		t.Fatalf("examined %d group uses, want %d — the mutation changed the size of the walk, "+
			"so it is not the mutation this test describes", mutated, examined)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want exactly 1: %v", len(findings), findings)
	}
	if !strings.Contains(findings[0], `"`+misspelt+`"`) {
		t.Errorf("the finding does not name the offending group value: %s", findings[0])
	}
	if !strings.Contains(findings[0], key) {
		t.Errorf("the finding does not name the offending route: %s", findings[0])
	}
	if !strings.Contains(findings[0], groupSourceOverlay) {
		t.Errorf("the finding does not identify the source as an overlay entry: %s", findings[0])
	}
	if !strings.Contains(findings[0], "internal/api/handlers/api_docs.go") {
		t.Errorf("the finding does not name the file the overlay lives in: %s", findings[0])
	}
	// Against the other REMEDY, not the other LABEL — see the same check in
	// the declaration test for why the label version is prose-coupled.
	if strings.Contains(findings[0], groupSourceRemedy[groupSourceDeclaration]) {
		t.Errorf("the finding carries the declaration remedy as well as the overlay one: %s", findings[0])
	}
	for _, u := range groupUses(eps, mutatedOverlay) {
		if u.route == key && u.source != groupSourceOverlay {
			t.Errorf("groupUses labelled the mutated overlay entry %q, want %q", u.source, groupSourceOverlay)
		}
	}
}

// TestGuard_EveryCanonicalGroupHasRoutes_RejectsAStaleEntry proves the other
// direction can fail, by adding a vocabulary entry nothing names.
//
// The stale-entry check is the half with no natural pressure on it: nothing
// breaks when it stops working, and the symptom is an empty docs section
// that looks like a feature nobody has shipped yet.
func TestGuard_EveryCanonicalGroupHasRoutes_RejectsAStaleEntry(t *testing.T) {
	eps := newRouteStubServer(t).registry.Endpoints()
	overlay := handlers.EndpointMetaGroups()

	stale := maps.Clone(canonicalGroups)
	stale["Node Management"] = "The spelling the APT repository routes carried before the merge."

	findings, examined := groupVocabularyFindings(groupUses(eps, overlay), stale)
	if examined == 0 {
		t.Fatal("examined 0 group uses over the real registry and overlay")
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want exactly 1: %v", len(findings), findings)
	}
	if !strings.Contains(findings[0], `"Node Management"`) {
		t.Errorf("the finding does not name the stale entry: %s", findings[0])
	}
}

// TestGuard_EveryCanonicalGroupHasRoutes_CountsOverlayEntries is the
// anti-vacuity proof for the counting half of the second direction, and the
// one assertion here that must not rest on production data.
//
// "API Documentation" is currently named only by an overlay entry, so the
// production guard would already fail if counting had stayed
// declaration-only — but that is a fact about today's routes, and the fix
// has to stay proven after a future change moves it. Both sides are
// synthetic: zero declarations, one overlay entry, one canonical group. If
// the counting loop ever stops seeing overlay uses, the entry reads as stale
// and this fails, whatever the real registry happens to contain.
func TestGuard_EveryCanonicalGroupHasRoutes_CountsOverlayEntries(t *testing.T) {
	canonical := map[string]string{"Overlay Only": "A section no declaration names."}
	uses := groupUses(nil, map[string]string{"GET /api/v1/legacy-thing": "Overlay Only"})

	findings, examined := groupVocabularyFindings(uses, canonical)
	if examined != 1 {
		t.Fatalf("examined %d group uses, want 1; groupUses is not producing the overlay half", examined)
	}
	if len(findings) != 0 {
		t.Fatalf("a group named only by an overlay entry was reported: %v — the 'every canonical "+
			"entry is used' direction is counting declarations only, so a section the overlay keeps "+
			"alive reads as a leftover and the next author is told to delete it", findings)
	}
}

// TestGuard_APIDocumentationIsNamedOnlyByTheOverlay records the decision
// taken when this guard learned to read the overlay: "API Documentation"
// was added to canonicalGroups rather than folded into "Settings".
//
// The comment on that entry carries the argument. This pins the fact the
// argument's last step depends on — that no declaration names it — so that
// the entry keeps working as a standing, production-data witness that the
// counting direction reads overlay entries.
//
// If this ever fails because GET /api/v1/api-docs became declarable (see
// TestAPIDocsIsStillLegacy for why it is not), nothing is broken: delete
// this test and update the comment. The proof that does NOT move is
// TestGuard_EveryCanonicalGroupHasRoutes_CountsOverlayEntries, which is
// synthetic on both sides for exactly that reason.
func TestGuard_APIDocumentationIsNamedOnlyByTheOverlay(t *testing.T) {
	const group = "API Documentation"
	if _, ok := canonicalGroups[group]; !ok {
		t.Fatalf("canonicalGroups no longer lists %q; if it was folded into another section, "+
			"delete this test and the comment recording the decision", group)
	}

	eps := newRouteStubServer(t).registry.Endpoints()
	for _, e := range eps {
		if e.Group == group {
			t.Errorf("%s %s declares Group %q, which the decision recorded on that canonicalGroups "+
				"entry says no declaration does — the entry is no longer a witness that the counting "+
				"direction reads overlay entries", e.Method, e.Path, group)
		}
	}

	named := false
	for _, g := range handlers.EndpointMetaGroups() {
		if g == group {
			named = true
			break
		}
	}
	if !named {
		t.Errorf("no overlay entry names %q, so canonicalGroups lists a section nothing uses; the "+
			"production guard's stale direction should have said so too", group)
	}
}

// TestGuard_GroupUsesLabelsEverySourceItProduces pins the join between the
// source labels and the per-source remedies.
//
// groupVocabularyFindings looks the remedy up by label, so a third source
// added to groupUses without a matching remedy would not fail to compile —
// it would produce a finding that stops mid-sentence, naming the bad value
// and then saying nothing about where to fix it. That is the quiet half of
// "the message must identify the source", and it is checkable without
// depending on what the registry or the overlay currently hold.
func TestGuard_GroupUsesLabelsEverySourceItProduces(t *testing.T) {
	uses := groupUses(
		[]Endpoint{{Method: "GET", Path: "/api/v1/declared-thing", Group: "Declared"}},
		map[string]string{
			"GET /api/v1/overlaid-thing": "Overlaid",
			"GET /api/v1/blank-thing":    "",
		},
	)

	// The remedy loop runs FIRST, and deliberately. The scenario this test
	// exists for — a third source added to groupUses — also breaks the
	// exact-shape check below, because `want` is an exact list; if that check
	// ran first and aborted, the new label would never reach the loop that is
	// the whole point of the test. Order, not extra assertions, is what makes
	// the loop able to fire on the case it is named for.
	for _, u := range uses {
		if strings.TrimSpace(groupSourceRemedy[u.source]) == "" {
			t.Errorf("groupUses labels a use %q, but groupSourceRemedy has no entry for it — a "+
				"finding from that source would name the bad value and then stop, without saying "+
				"which file to open", u.source)
		}
	}
	if len(groupSourceRemedy) != len(groupSourcesOf(uses)) {
		t.Errorf("groupSourceRemedy has %d entries but groupUses produces %d distinct sources; a "+
			"remedy for a source nothing produces is dead text a reader will trust, and a source "+
			"with no remedy renders a finding that stops mid-sentence",
			len(groupSourceRemedy), len(groupSourcesOf(uses)))
	}

	// The exact shape, second, and reporting rather than aborting. It pins
	// both source labels, the order they are produced in, and the blank-Group
	// skip: an overlay entry with a blank Group states no section (GetDocs
	// derives one), so it has no spelling to hold to a vocabulary and must not
	// be reported as an unknown group.
	want := []groupUse{
		{source: groupSourceDeclaration, route: "GET /api/v1/declared-thing", group: "Declared"},
		{source: groupSourceOverlay, route: "GET /api/v1/overlaid-thing", group: "Overlaid"},
	}
	if !slices.Equal(uses, want) {
		t.Errorf("groupUses produced %+v, want %+v — a source was added or dropped, the blank-Group "+
			"skip changed, or the labels did", uses, want)
	}
}

// groupSourcesOf is the distinct set of source labels a run of groupUses produced.
// It is a helper rather than an inline count so that the remedy-map size
// check compares against what groupUses ACTUALLY produces, instead of a
// hard-coded 2 that a third source would have to remember to update.
func groupSourcesOf(uses []groupUse) map[string]bool {
	out := make(map[string]bool, 2)
	for _, u := range uses {
		out[u.source] = true
	}
	return out
}

// TestGuard_GroupVocabularyAntiVacuityTriggerIsReachable pins the inputs that
// make the file's anti-vacuity t.Fatals fire, because the only way to know a
// branch is reachable — rather than dead code that reads like a safeguard —
// is to produce the input that reaches it.
//
// Be exact about WHICH ones, because the obvious reading overstates it. The
// first half produces no uses at all, which is the trigger for the
// `examined == 0` fatals in the three sibling tests that measure a delta
// from a clean baseline (_RejectsAnInventedDeclaredSpelling,
// _RejectsAnInventedOverlaySpelling and _RejectsAStaleEntry). It does NOT
// reach the production guard's first floor, `len(eps) == 0`: nothing here
// builds a stub server, and a registry that returned nothing would be caught
// there rather than here.
//
// The second half is the one that covers the production guard, and it covers
// the floor that the count alone cannot: `examined != want` catching ONE
// source being dropped while the other still fills the walk.
func TestGuard_GroupVocabularyAntiVacuityTriggerIsReachable(t *testing.T) {
	findings, examined := groupVocabularyFindings(nil, canonicalGroups)
	if examined != 0 {
		t.Fatalf("examined = %d over no group uses, want 0; the `examined == 0` fatals in the "+
			"delta-measuring sibling tests can never fire", examined)
	}
	// Every canonical entry is unused when there are no uses, which is what
	// makes the zero-use case indistinguishable from a real failure without
	// the examined count — and therefore why the production test checks
	// examined rather than trusting len(findings).
	if len(findings) != len(canonicalGroups) {
		t.Fatalf("got %d findings over no group uses, want %d", len(findings), len(canonicalGroups))
	}

	// The relation floor is the other assertion, and it catches what the
	// count alone cannot: a walk that reads declarations and silently skips
	// the overlay still examines hundreds of uses and still reports zero
	// findings. What makes that floor able to fire is that the overlay
	// actually contributes uses — so pin it, over the real inputs. If
	// groupUses ever ignores its overlay argument, the floor's two sides move
	// together and it stops being able to tell a dropped source from a
	// healthy one.
	eps := newRouteStubServer(t).registry.Endpoints()
	overlay := handlers.EndpointMetaGroups()
	stated := 0
	for _, group := range overlay {
		if group != "" {
			stated++
		}
	}
	if stated == 0 {
		// Legitimate, eventually: the overlay exists only for the legacy
		// routes, and the legacy set only shrinks. Skipping rather than
		// failing is the difference between a floor that survives that day
		// and one somebody deletes on it.
		t.Skip("the overlay states no sections, so there is no second source for the relation floor " +
			"to distinguish; nothing to prove until a legacy route states one again")
	}

	both := len(groupUses(eps, overlay))
	declaredOnly := len(groupUses(eps, nil))
	if both != declaredOnly+stated {
		t.Fatalf("groupUses produced %d uses from both sources and %d from declarations alone, but "+
			"the overlay states %d sections — the overlay half is not reaching the walk, and the "+
			"production test's relation floor would be comparing a number against itself",
			both, declaredOnly, stated)
	}
}
