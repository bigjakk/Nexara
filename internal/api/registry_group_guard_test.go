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

// Group is the section structure of the generated API docs: one collapsible card per
// distinct value. Two spellings of one concept do not merge, they make two cards each
// holding half the routes, and nothing 500s or logs; Register refuses only a BLANK Group.
// This guard holds both places a Group is stated (a declaration, and handlers.endpointMeta
// for the legacy routes, read through handlers.EndpointMetaGroups) to canonicalGroups. A
// route that states nothing derives its Group, which is TestGuard_NoRouteFallsThroughToADerivedGroup's.

// canonicalGroups is the Group vocabulary: a route may name one of these and nothing else.
// The values are one-line notes for whoever files the next route; the guard reads the keys.
// To add a group, add one line here: reviewable on the question it asks, whether this is a
// new section or a second spelling of one below. Synthetic Groups in tests are absent on
// purpose: they never reach the Server's registry, and listing them would let a real
// endpoint borrow one.
var canonicalGroups = map[string]string{
	// Named only by the overlay (GET /api/v1/api-docs), which is what makes this entry a
	// standing witness over production data that the "every canonical entry is used"
	// direction counts overlay entries too. Not folded into "Settings": the index of the
	// document is the one endpoint that placement makes least findable.
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
	// NOT "physical interfaces": create/edit accept only virtual types
	// (creatableNetworkInterfaceTypes, internal/proxmox/client_network.go) and the
	// read-only physical listings live in "Nodes".
	"Networks":               "A node's network interface configuration: bridges, bonds, VLANs and OVS ports, and applying or reverting the config.",
	"Nodes":                  "Everything scoped to one node: hardware, disks, services, logs, time, DNS, APT repositories, firewall, and power and maintenance actions.",
	"Notification Channels":  "Notification delivery targets and the dead-letter queue.",
	"Proxmox Access Control": "PVE-side users, tokens, groups, roles, realms and ACLs on a cluster.",
	"Replication":            "Proxmox storage replication jobs.",
	"Reports":                "Report generation, schedules and runs.",
	"Roles & Permissions":    "Nexara's own RBAC: roles, permissions and role assignment.",
	// The packages listing is filed by consumer, not by resource, unlike the
	// apt-repository routes, which moved to "Nodes".
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

// The two places a Group value can be written; the remedies differ, so a finding says which.
const (
	groupSourceDeclaration = "a declaration"
	groupSourceOverlay     = "an overlay entry"
)

// groupSourceRemedy is the half of an unknown-group finding that depends on the source.
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

// groupUse is one route's claim on a Group value and where it was written, which is
// not recoverable afterwards.
type groupUse struct {
	source string
	route  string
	group  string
}

// groupUses collects every Group value a source with a vocabulary can send to the docs
// payload: declarations and the overlay. A BLANK overlay Group is skipped, since GetDocs
// derives one for it and there is no spelling to hold. An overlay entry for a declared
// route is included although GetDocs never renders it: a vocabulary is a rule about what
// may be written, and an inert entry is one deleted declaration from being read.
func groupUses(eps []Endpoint, overlay map[string]string) []groupUse {
	uses := make([]groupUse, 0, len(eps)+len(overlay))

	for _, e := range eps {
		uses = append(uses, groupUse{
			source: groupSourceDeclaration,
			route:  e.Method + " " + e.Path,
			group:  e.Group,
		})
	}

	// Sorted: the overlay is a map.
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

// groupVocabularyFindings checks Group in both directions and reports how many uses it
// looked at. The second direction decays quietly: a canonical entry no route names is
// the residue of a rename, a section that renders empty and invites the next author to
// file something under it. canonical and uses are parameters so a synthetic input can
// prove the detector fires.
func groupVocabularyFindings(uses []groupUse, canonical map[string]string) (findings []string, examined int) {
	counts := make(map[string]int, len(canonical))

	// Keyed on value AND source: one bad spelling in both places is two findings,
	// each under the remedy that is right for it.
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

// TestGuard_EveryRouteGroupIsCanonical is the production guard, over every Group
// buildRegistry declares and every one the overlay states.
func TestGuard_EveryRouteGroupIsCanonical(t *testing.T) {
	eps := sharedRouteStub(t).registry.Endpoints()
	overlay := handlers.EndpointMetaGroups()

	findings, examined := groupVocabularyFindings(groupUses(eps, overlay), canonicalGroups)

	// Anti-vacuity: an empty registry reports zero findings, as a clean one does, and the
	// relation between inputs and uses is what notices a source being skipped. The overlay
	// shrinks as legacy routes migrate, so it gets this relation and not a count floor.
	if len(eps) == 0 {
		t.Fatal("the registry declares 0 endpoints; the guard would pass vacuously over an empty registry")
	}
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

// TestGuard_EveryRouteGroupIsCanonical_RejectsAnInventedDeclaredSpelling plants one fault
// of each kind groupVocabularyFindings reports into the REAL inputs, which are clean and
// so cannot tell a working detector from one that stopped comparing: an unknown Group
// from a declaration and from the overlay (each under its own remedy), a canonical entry
// nothing names, and — the ones that must NOT be reported — a section only the overlay names
// and a blank overlay Group (GetDocs derives one, so there is no spelling to hold).
func TestGuard_EveryRouteGroupIsCanonical_RejectsAnInventedDeclaredSpelling(t *testing.T) {
	eps := sharedRouteStub(t).registry.Endpoints()
	overlay := handlers.EndpointMetaGroups()
	baseline, examined := groupVocabularyFindings(groupUses(eps, overlay), canonicalGroups)
	if examined == 0 || len(baseline) != 0 {
		t.Fatalf("the real inputs must be clean and non-empty: examined %d, findings %v", examined, baseline)
	}

	bogus := Endpoint{Method: "GET", Path: "/api/v1/clusters/:cluster_id/synthetic-probe", Group: "Node Mgmt"}
	for _, tt := range []struct {
		name     string
		eps      []Endpoint
		overlay  map[string]string
		canon    map[string]string
		wantUses int
		// finding is what the one finding must carry; absent is what it must not.
		finding, absent []string
	}{
		{"an unknown Group in a declaration", append(slices.Clone(eps), bogus), overlay, canonicalGroups, examined + 1,
			[]string{`"Node Mgmt"`, bogus.Path, groupSourceRemedy[groupSourceDeclaration]},
			[]string{groupSourceRemedy[groupSourceOverlay]}},
		{"an unknown Group in the overlay", eps, withEntry(overlay, "GET /api/v1/synthetic-overlay", "virtual machines"),
			canonicalGroups, examined + 1,
			[]string{`"virtual machines"`, "GET /api/v1/synthetic-overlay", groupSourceRemedy[groupSourceOverlay]},
			[]string{groupSourceRemedy[groupSourceDeclaration]}},
		{"a canonical entry nothing names", eps, overlay,
			withEntry(canonicalGroups, "Node Management", "The spelling the APT routes carried before the merge."),
			examined, []string{`"Node Management"`}, nil},
		{"a section only the overlay names, beside a blank overlay Group, which states none",
			eps, withEntry(withEntry(overlay, "GET /api/v1/synthetic-overlay", "Overlay Only"), "GET /api/v1/synthetic-blank", ""),
			withEntry(canonicalGroups, "Overlay Only", "A section no declaration names."), examined + 1, nil, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			findings, got := groupVocabularyFindings(groupUses(tt.eps, tt.overlay), tt.canon)
			if got != tt.wantUses {
				t.Fatalf("examined %d group uses, want %d", got, tt.wantUses)
			}
			if tt.finding == nil {
				if len(findings) != 0 {
					t.Fatalf("findings %v, want none", findings)
				}
				return
			}
			if len(findings) != 1 {
				t.Fatalf("got %d findings, want exactly 1: %v", len(findings), findings)
			}
			for _, f := range tt.finding {
				if !strings.Contains(findings[0], f) {
					t.Errorf("the finding does not carry %q: %s", f, findings[0])
				}
			}
			for _, f := range tt.absent {
				if strings.Contains(findings[0], f) {
					t.Errorf("the finding carries %q, which belongs to the other source: %s", f, findings[0])
				}
			}
		})
	}
}

// withEntry is m with one more entry.
func withEntry(m map[string]string, key, value string) map[string]string {
	out := maps.Clone(m)
	out[key] = value
	return out
}
