package api

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"testing"
)

// Every declared endpoint carries a Group, and until this file nothing said
// which Groups exist.
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
// SCOPE. This guard bounds the REGISTRY. The docs payload has one other
// source of Group values — the curated overlay for the handful of routes
// still declared in router.go, plus handlers.groupFromPath's derived
// fallback — and those live in internal/api/handlers/api_docs.go, outside
// both this file and the registry. A value introduced there is not checked
// here. That is a real gap, not an oversight: the registry is where new
// endpoints are added, and it is the half whose vocabulary can be pinned to
// a list without pinning the legacy set too.

// canonicalGroups is the Group vocabulary. A declared endpoint may name one
// of these and nothing else.
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
// is it a second spelling of one of the 36 below?"). The alternative, which
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
	// conclude it does not fit here and invent a 37th group — the exact outcome
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

// groupVocabularyFindings checks Group in both directions and reports what
// it finds, plus how many endpoints it actually looked at.
//
// Both directions matter, and the second is the one that decays quietly. An
// endpoint naming a group nobody canonicalised is the obvious failure. A
// canonical entry NO endpoint names is the residue of a rename — the merge
// happened, the old spelling stopped being declared, and the list kept
// advertising a section that renders empty and invites the next author to
// file something under it. Neither direction is checkable from the other.
//
// canonical is a parameter rather than a package reference so that a
// synthetic vocabulary can prove the detector fails when it should; eps is
// a parameter for the same reason. examined is returned for the anti-vacuity
// assertion the callers make — a guard over an empty registry reports zero
// findings and is indistinguishable, from the outside, from a clean one.
func groupVocabularyFindings(eps []Endpoint, canonical map[string]string) (findings []string, examined int) {
	counts := make(map[string]int, len(canonical))
	unknown := make(map[string][]string)

	for _, e := range eps {
		examined++
		if _, ok := canonical[e.Group]; ok {
			counts[e.Group]++
			continue
		}
		unknown[e.Group] = append(unknown[e.Group], e.Method+" "+e.Path)
	}

	for _, g := range slices.Sorted(maps.Keys(unknown)) {
		routes := unknown[g]
		sort.Strings(routes)
		findings = append(findings, fmt.Sprintf(
			"Group %q is not in canonicalGroups, declared by %d route(s): %s. "+
				"Either correct the spelling to an entry that already exists — a second spelling "+
				"of an existing concept splits one docs section into two, each holding half the "+
				"routes — or, if this really is a new section, add one line to canonicalGroups "+
				"saying what belongs in it.",
			g, len(routes), strings.Join(routes, ", ")))
	}

	for _, g := range slices.Sorted(maps.Keys(canonical)) {
		if counts[g] > 0 {
			continue
		}
		findings = append(findings, fmt.Sprintf(
			"canonicalGroups lists %q, but no declared endpoint names it. Either the routes that "+
				"used to be in that section were renamed into another one and this entry is the "+
				"leftover — delete the line — or the section is real and its declarations are "+
				"spelling it differently, which the findings above would name.",
			g))
	}

	return findings, examined
}

// TestGuard_EveryDeclaredGroupIsCanonical is the production guard, over
// every endpoint buildRegistry declares.
//
// It reads Registry.Endpoints() rather than app.GetRoutes() for the reason
// registry_order_guard_test.go gives: the declaration is what api_docs.go
// reads, and Fiber's own table carries no Group at all.
func TestGuard_EveryDeclaredGroupIsCanonical(t *testing.T) {
	eps := newRouteStubServer(t).registry.Endpoints()

	findings, examined := groupVocabularyFindings(eps, canonicalGroups)

	// Anti-vacuity. A stub server that registered nothing, or an Endpoints()
	// that returned an empty slice, produces zero findings over an empty
	// vocabulary check and reports PASS — which is exactly what a healthy
	// registry reports. This package has shipped guards that passed while
	// incapable of failing; refuse to be the next one.
	if examined == 0 {
		t.Fatal("examined 0 endpoints; the guard would pass vacuously over an empty registry")
	}

	for _, f := range findings {
		t.Error(f)
	}
}

// TestGuard_EveryDeclaredGroupIsCanonical_RejectsAnInventedSpelling runs the
// production detector over the REAL declarations with one extra route
// spliced in, naming a group nothing canonicalised.
//
// It exists because the registry is clean, and a clean guard is where
// vacuity hides: a detector that had stopped comparing anything would report
// zero findings over 535 endpoints and look identical to the test above.
// Running the production function over the production input plus one
// known-bad declaration is what tells the two apart, permanently — a later
// refactor that breaks the comparison fails HERE even though the registry
// itself is still clean.
func TestGuard_EveryDeclaredGroupIsCanonical_RejectsAnInventedSpelling(t *testing.T) {
	eps := newRouteStubServer(t).registry.Endpoints()

	baseline, examined := groupVocabularyFindings(eps, canonicalGroups)
	if examined == 0 {
		t.Fatal("examined 0 endpoints over the real registry")
	}
	if len(baseline) != 0 {
		t.Fatalf("the real registry already has %d group finding(s) (%v); this test measures the "+
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
	findings, examined := groupVocabularyFindings(append(slices.Clone(eps), bogus), canonicalGroups)

	if examined != len(eps)+1 {
		t.Fatalf("examined %d endpoints, want %d", examined, len(eps)+1)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want exactly 1: %v", len(findings), findings)
	}
	// The finding has to be actionable on its own: the value that is wrong
	// and the route that declares it, or the reader has to go looking.
	if !strings.Contains(findings[0], `"Node Mgmt"`) {
		t.Errorf("the finding does not name the offending group value: %s", findings[0])
	}
	if !strings.Contains(findings[0], bogus.Path) {
		t.Errorf("the finding does not name the offending route: %s", findings[0])
	}
}

// TestGuard_EveryCanonicalGroupHasRoutes_RejectsAStaleEntry proves the other
// direction can fail, by adding a vocabulary entry nothing declares.
//
// The stale-entry check is the half with no natural pressure on it: nothing
// breaks when it stops working, and the symptom is an empty docs section
// that looks like a feature nobody has shipped yet.
func TestGuard_EveryCanonicalGroupHasRoutes_RejectsAStaleEntry(t *testing.T) {
	eps := newRouteStubServer(t).registry.Endpoints()

	stale := maps.Clone(canonicalGroups)
	stale["Node Management"] = "The spelling the APT repository routes carried before the merge."

	findings, examined := groupVocabularyFindings(eps, stale)
	if examined == 0 {
		t.Fatal("examined 0 endpoints over the real registry")
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want exactly 1: %v", len(findings), findings)
	}
	if !strings.Contains(findings[0], `"Node Management"`) {
		t.Errorf("the finding does not name the stale entry: %s", findings[0])
	}
}

// TestGuard_GroupVocabularyAntiVacuityTriggerIsReachable pins the input that
// makes the production test's t.Fatal fire.
//
// The assertion it protects is `examined == 0`, and the only way to know
// that branch is reachable — rather than dead code that reads like a
// safeguard — is to produce the input that reaches it.
func TestGuard_GroupVocabularyAntiVacuityTriggerIsReachable(t *testing.T) {
	findings, examined := groupVocabularyFindings(nil, canonicalGroups)
	if examined != 0 {
		t.Fatalf("examined = %d over no endpoints, want 0; the production test's anti-vacuity "+
			"assertion can never fire", examined)
	}
	// Every canonical entry is unused when there are no endpoints, which is
	// what makes the zero-endpoint case indistinguishable from a real
	// failure without the examined count — and therefore why the production
	// test checks examined rather than trusting len(findings).
	if len(findings) != len(canonicalGroups) {
		t.Fatalf("got %d findings over no endpoints, want %d", len(findings), len(canonicalGroups))
	}
}
