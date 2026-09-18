package api

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// This file closes the gap that let the node firewall routes 403 every
// caller for six months.
//
// The RBAC engine resolves a permission by joining user_roles →
// role_permissions → permissions and matching on (action, resource). The
// permissions table is seeded ONLY by migrations — no Go code and no query
// in queries/ ever inserts into it — so a resource the migrations never
// seed cannot be granted to any role, cannot be held by any user, and
// cannot be matched by HasPermission. A route declaring one is not
// "restrictively gated"; it is unreachable, and it fails the same way for
// Admin as for an anonymous caller, which is why nobody read the 403 as a
// bug.
//
// rbac_route_guard_test.go already pins the ACTION vocabulary (knownActions)
// and proves every route reaches a permission check. Neither catches a
// well-formed check against a resource that does not exist. This does.

// permissionInsertRe finds the head of one INSERT INTO permissions
// statement. Only the head: where the statement ENDS is decided by
// scanning, not by a lazy match to the next semicolon, because a
// description containing one would otherwise truncate the statement and
// silently drop every tuple after it.
var permissionInsertRe = regexp.MustCompile(`(?is)INSERT\s+INTO\s+permissions\s*\([^)]*\)\s*VALUES`)

// permissionTupleRe pulls the quoted literals out of one VALUES tuple.
var permissionTupleRe = regexp.MustCompile(`'([^']*)'`)

// stripSQLComments removes -- line comments and /* */ block comments,
// leaving string literals alone.
//
// It is not decoration. Without it the parser reads commented-out SQL as
// live, so a migration that documents a permission it deliberately did NOT
// add — or one that comments an insert out during a revert — would teach
// the catalogue that the permission exists and silently re-open the very
// bug TestGuard_DeclaredPermissionsExistInTheCatalogue was written to
// catch. That is the vacuous direction: the guard keeps passing while no
// longer able to fail.
//
// BLOCK comments are handled for a second-order version of the same
// failure. An apostrophe inside one — "/* Nexara's catalogue */" — would
// otherwise flip the in-string flag ON outside any literal, and every --
// comment after it in the file would survive stripping. migrations/ already
// contains block comments; it is one apostrophe away from mattering.
func stripSQLComments(sql string) string {
	var out strings.Builder
	out.Grow(len(sql))
	inString := false
	for i := 0; i < len(sql); i++ {
		switch {
		case inString:
			if sql[i] == '\'' {
				inString = false
			}
		case sql[i] == '\'':
			inString = true
		case sql[i] == '-' && i+1 < len(sql) && sql[i+1] == '-':
			// Skip to end of line, keeping the newline so line
			// structure (and any following statement) survives.
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			if i < len(sql) {
				out.WriteByte('\n')
			}
			continue
		case sql[i] == '/' && i+1 < len(sql) && sql[i+1] == '*':
			end := strings.Index(sql[i+2:], "*/")
			if end < 0 {
				// Unterminated: the rest of the file is comment.
				return out.String()
			}
			// Keep the newlines so a -- on a later line is still
			// recognised as starting its own line.
			out.WriteString(strings.Repeat("\n",
				strings.Count(sql[i:i+2+end+2], "\n")))
			i += 2 + end + 1
			continue
		}
		out.WriteByte(sql[i])
	}
	return out.String()
}

// statementBody returns the text of the statement starting at sql[0], up
// to the first semicolon that is NOT inside a string literal.
func statementBody(sql string) string {
	inString := false
	for i := 0; i < len(sql); i++ {
		switch {
		case inString:
			if sql[i] == '\'' {
				inString = false
			}
		case sql[i] == '\'':
			inString = true
		case sql[i] == ';':
			return sql[:i]
		}
	}
	return sql
}

// parsePermissionInserts extracts the "action:resource" pairs one SQL
// document seeds. Split out from the file walk so it can be exercised on
// synthetic input — a parser that can only be run against the repo's own
// 22 migrations cannot be shown to reject anything.
func parsePermissionInserts(sql string) map[string]bool {
	out := map[string]bool{}
	clean := stripSQLComments(sql)
	for _, loc := range permissionInsertRe.FindAllStringIndex(clean, -1) {
		body := statementBody(clean[loc[1]:])
		for _, tuple := range strings.Split(body, "),") {
			var idents []string
			for _, m := range permissionTupleRe.FindAllStringSubmatch(tuple, -1) {
				if isBareIdentifier(m[1]) {
					idents = append(idents, m[1])
				}
			}
			if len(idents) >= 2 {
				out[idents[0]+":"+idents[1]] = true
			}
		}
	}
	return out
}

// seededPermissions parses the permission catalogue out of migrations/,
// returning the set of "action:resource" pairs an install can actually
// hold, mapped to the migration that seeds each. Parsing the SQL rather
// than querying a database keeps this a unit test: internal/db's DB-backed
// tests skip without NEXARA_TEST_DB_URL, and a guard that silently skips
// is no guard at all.
func seededPermissions(t *testing.T) map[string]string {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.up.sql"))
	if err != nil {
		t.Fatalf("globbing migrations: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no migrations found; the guard would pass vacuously")
	}

	out := map[string]string{}
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		for pair := range parsePermissionInserts(string(body)) {
			out[pair] = filepath.Base(path)
		}
	}
	return out
}

// isBareIdentifier reports whether s looks like an action or resource name
// rather than a description: lowercase letters, digits and underscores only.
func isBareIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

// TestParsePermissionInsertsHandlesTheAwkwardSQL is the parser's own
// bite-proof, on synthetic input.
//
// Some of these shapes are transcribed from real migrations — the plain
// seed from 000016, the gen_random_uuid() form that 19 of the 22 seeding
// files use, the parenthesised description from 000078 — and the rest are
// the ones that are NOT in migrations/ yet: a commented-out insert, a
// semicolon inside a description, a block comment containing an
// apostrophe. Those are the point. A parser that got every one of them
// wrong would still agree with the database today and look correct, which
// is how a guard ends up unable to fail.
func TestParsePermissionInsertsHandlesTheAwkwardSQL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		sql     string
		want    []string
		notWant []string
	}{
		{
			name: "the plain three-column seed",
			sql: `INSERT INTO permissions (action, resource, description) VALUES
			    ('view',   'cluster', 'View clusters'),
			    ('manage', 'cluster', 'Create, update clusters');`,
			want: []string{"view:cluster", "manage:cluster"},
		},
		{
			name: "a leading gen_random_uuid() is not mistaken for the action",
			sql: `INSERT INTO permissions (id, action, resource, description) VALUES
			    (gen_random_uuid(), 'console', 'node', 'Open node shell consoles')
			ON CONFLICT (action, resource) DO NOTHING;`,
			want:    []string{"console:node"},
			notWant: []string{"action:resource"},
		},
		{
			name: "a commented-out insert seeds nothing",
			sql: `-- INSERT INTO permissions (action, resource, description) VALUES
			--     ('view', 'firewall', 'View firewall rules');`,
			notWant: []string{"view:firewall"},
		},
		{
			name: "a trailing comment does not eat the statement after it",
			sql: `-- we deliberately did not add view:firewall here
			INSERT INTO permissions (action, resource, description) VALUES
			    ('view', 'network', 'View networks, firewall, SDN');`,
			want:    []string{"view:network"},
			notWant: []string{"view:firewall"},
		},
		{
			name: "a semicolon inside a description does not truncate the tuples",
			sql: `INSERT INTO permissions (action, resource, description) VALUES
			    ('view',   'sdn', 'View SDN; zones and vnets'),
			    ('manage', 'sdn', 'Configure SDN');`,
			want: []string{"view:sdn", "manage:sdn"},
		},
		{
			name: "a role_permissions insert seeds no permission",
			sql: `INSERT INTO role_permissions (role_id, permission_id)
			SELECT 'a0000000-0000-0000-0000-000000000001'::uuid, id FROM permissions
			WHERE resource = 'guest_tools' ON CONFLICT DO NOTHING;`,
			notWant: []string{"a0000000:id", "resource:guest_tools"},
		},
		{
			name: "an apostrophe in a block comment does not unstrip the rest of the file",
			sql: `/* Nexara's permission catalogue */
			-- INSERT INTO permissions (action, resource, description) VALUES ('view', 'ghost', 'x');
			INSERT INTO permissions (action, resource, description) VALUES ('view', 'real', 'y');`,
			want:    []string{"view:real"},
			notWant: []string{"view:ghost"},
		},
		{
			name: "parentheses in a description do not split the tuple",
			sql: `INSERT INTO permissions (action, resource, description) VALUES
			    ('console', 'node', 'Open node shell consoles (root shell on the host)'),
			    ('console', 'vm',   'Open VM consoles');`,
			want: []string{"console:node", "console:vm"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parsePermissionInserts(tt.sql)
			for _, w := range tt.want {
				if !got[w] {
					t.Errorf("%s not parsed; got %v", w, keysOf(got))
				}
			}
			for _, w := range tt.notWant {
				if got[w] {
					t.Errorf("%s was parsed but must not be; got %v", w, keysOf(got))
				}
			}
		})
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestPermissionCatalogueParsesTheKnownSeed is the bite-proof for the
// parser: a guard built on a regex that silently matched nothing would
// report every declaration as valid. It pins entries from the original
// seed, from a later migration that inserts with gen_random_uuid(), and
// the count floor.
func TestPermissionCatalogueParsesTheKnownSeed(t *testing.T) {
	t.Parallel()

	seeded := seededPermissions(t)

	// 86 is what the dev database holds; the parser must not find fewer.
	if len(seeded) < 86 {
		t.Errorf("parsed %d permissions from migrations/, want at least 86", len(seeded))
	}
	for _, want := range []string{
		"view:cluster",        // 000016, the original three-column seed
		"manage:network",      // 000016, the resource the firewall routes moved to
		"console:node",        // 000078, inserted with gen_random_uuid()
		"execute:guest_tools", // 000098, the most recent
	} {
		if _, ok := seeded[want]; !ok {
			t.Errorf("%s is seeded by a migration but the parser did not find it", want)
		}
	}
	// The negative case, and the whole point of this file.
	for _, absent := range []string{"view:firewall", "manage:firewall", "delete:firewall"} {
		if from, ok := seeded[absent]; ok {
			t.Errorf("%s is not supposed to exist, but %s seeds it", absent, from)
		}
	}
}

// TestGuard_DeclaredPermissionsExistInTheCatalogue is the production
// guard: every (action, resource) any route declares must be seeded.
//
// It covers all three declaration shapes that name a resource — Check,
// Alternatives and Advisory. Deferred, Public and SelfService name none,
// and a Deferred handler's own check is out of reach of a static test;
// rbac_route_guard_test.go carries that limitation already.
func TestGuard_DeclaredPermissionsExistInTheCatalogue(t *testing.T) {
	seeded := seededPermissions(t)

	type site struct{ route, shape string }
	missing := map[string][]site{}
	checked := 0

	for _, e := range newRouteStubServer(t).registry.Endpoints() {
		route := e.Method + " " + e.Path
		var sites []site
		if c := e.Permissions.Check; c != nil {
			sites = append(sites, site{route, "Check"})
		}
		for range e.Permissions.Alternatives {
			sites = append(sites, site{route, "Alternatives"})
		}
		if e.Permissions.Advisory != nil {
			sites = append(sites, site{route, "Advisory"})
		}

		var checks []Check
		if c := e.Permissions.Check; c != nil {
			checks = append(checks, *c)
		}
		checks = append(checks, e.Permissions.Alternatives...)
		if a := e.Permissions.Advisory; a != nil {
			checks = append(checks, a.Check)
		}
		for i, c := range checks {
			checked++
			if _, ok := seeded[c.String()]; !ok {
				missing[c.String()] = append(missing[c.String()], sites[i])
			}
		}
	}

	if checked == 0 {
		t.Fatal("no declared permissions were examined; the guard would pass vacuously")
	}

	keys := make([]string, 0, len(missing))
	for k := range missing {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		routes := make([]string, 0, len(missing[k]))
		for _, s := range missing[k] {
			routes = append(routes, s.shape+" on "+s.route)
		}
		sort.Strings(routes)
		t.Errorf("%d route(s) declare %q, which no migration seeds into the permissions table — "+
			"HasPermission can only match a seeded row, so every one of them answers 403 to every "+
			"caller including Admin. Either seed it in a new migration and grant it to the built-in "+
			"roles, or point the route at a resource that exists.\n\t%s",
			len(missing[k]), k, strings.Join(routes, "\n\t"))
	}
}

// TestGuard_DeclaredActionsAreInTheCatalogue is the same check one axis
// over. It is separate because a misspelled ACTION on a real resource and
// a misspelled RESOURCE fail identically at runtime but are different
// mistakes to read in a failure message.
func TestGuard_DeclaredActionsAreInTheCatalogue(t *testing.T) {
	seeded := seededPermissions(t)

	actions := map[string]bool{}
	for pair := range seeded {
		actions[strings.SplitN(pair, ":", 2)[0]] = true
	}

	if len(actions) == 0 {
		t.Fatal("no actions parsed from migrations/; the guard would pass vacuously")
	}

	checked := 0
	seen := map[string][]string{}
	for _, e := range newRouteStubServer(t).registry.Endpoints() {
		route := e.Method + " " + e.Path
		var checks []Check
		if c := e.Permissions.Check; c != nil {
			checks = append(checks, *c)
		}
		checks = append(checks, e.Permissions.Alternatives...)
		if a := e.Permissions.Advisory; a != nil {
			checks = append(checks, a.Check)
		}
		for _, c := range checks {
			checked++
			if !actions[c.Action] {
				seen[c.Action] = append(seen[c.Action], route)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no declared actions were examined; the guard would pass vacuously")
	}

	for action, routes := range seen {
		sort.Strings(routes)
		t.Errorf("action %q is not in the seeded permission catalogue; declared by:\n\t%s",
			action, strings.Join(routes, "\n\t"))
	}
}
