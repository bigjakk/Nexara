package db

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every statement that inserts a session must be the conditional one.
//
// A session is minted for a sign-in whose credential check may be minutes old, and
// the one thing that ties it to the revoke-alls that have happened since is the
// condition on the user's row — auth_epoch the check read, is_active, and the FOR
// SHARE lock that orders the insert against a revoke-all (queries/sessions.sql,
// CreateSessionAtEpoch). A second statement that inserts a session without them —
// the plain INSERT this replaced, written again by someone who did not know — would
// be a path around all of it that nothing else notices: every test of the
// conditional one would still pass.
//
// So this reads the SQL, three ways: the queries, the generated copy of them that
// actually runs, and the Go code, where no raw INSERT INTO sessions has any
// business being. Static, no database, so it runs in the normal `go test` pass.

// sessInsertRe finds a statement that writes the sessions table, however it is
// spelled: INSERT INTO or MERGE INTO, with any whitespace (a newline included)
// between the words; the table quoted ("sessions"); any schema before it, bare or
// quoted — public.sessions, nexara.sessions, "nexara"."sessions", "my schema".sessions,
// even three parts, with or without spaces around the dots; and the quotes escaped the
// way a Go interpreted string has to write them (\"nexara\".\"sessions\"), because the
// Go half of the guard reads source. It does not match another table whose name
// merely contains the word (veeam_sessions, sessions_archive), in any schema.
//
// What it cannot see is a statement assembled at run time — `"INSERT INTO " + table`,
// fmt.Sprintf with the table name — and the two other ways into the table, which
// sessCopySQLRe and sessCopyRe find.
//
// The guard is a tripwire for the realistic mistake, someone writing the plain insert
// again, and not a defence against someone who is hiding one. It deliberately does
// not chase what no honest spelling needs: a block comment between the words
// (`INSERT /* x */ INTO sessions`), a \n or \t escape between them in an interpreted
// string (the source holds a backslash and a letter, not whitespace), a name with
// four parts or more, a table named through a variable. Whoever goes to those lengths
// is not reading a guard; review is what reads them.
var sessInsertRe = regexp.MustCompile(`(?is)\b(?:insert|merge)\s+into\s+(?:(?:\\?"[^"]+"|\w+)\s*\.\s*){0,2}(?:\\?"sessions\\?"|sessions\b)`)

// sessCopySQLRe finds a COPY statement that loads the sessions table — COPY sessions
// [(columns)] FROM STDIN, or FROM PROGRAM, or FROM a file's quoted path — in any
// schema, quoted or not. It asks for the FROM and what follows it so that prose does
// not match ("copy sessions from the old table") and neither does a COPY that only
// reads the table out (COPY (SELECT … FROM sessions) TO STDOUT).
var sessCopySQLRe = regexp.MustCompile(`(?is)\bcopy\s+(?:(?:\\?"[^"]+"|\w+)\s*\.\s*){0,2}(?:\\?"sessions\\?"|sessions\b)\s*(?:\([^)]*\)\s*)?from\s+(?:stdin\b|program\b|')`)

// sessCopyRe finds the other way into the table that is not a SQL statement of ours:
// the COPY protocol, as pgx exposes it, naming the sessions table by its identifier.
// pgx.Identifier(parts), built from a variable, is not chased.
var sessCopyRe = regexp.MustCompile(`(?s)pgx\.Identifier\{[^}]*"sessions"[^}]*\}`)

// sessGoFileProblems is what is wrong with the Go source of one non-test file where
// the sessions table is concerned: every way it has of writing it that is not
// CreateSessionAtEpoch through auth.SessionManager.CreateSession. It is the per-file
// check of the walk below, a function of its own so that the check can be tried on
// sources made for the purpose — a walk whose check was disabled would otherwise pass.
func sessGoFileProblems(raw []byte) []string {
	var problems []string
	if sessInsertRe.Match(raw) {
		problems = append(problems, "writes INSERT INTO (or MERGE INTO) sessions itself")
	}
	if sessCopySQLRe.Match(raw) {
		problems = append(problems, "loads the sessions table with a COPY statement")
	}
	if sessCopyRe.Match(raw) {
		problems = append(problems, `copies rows into sessions (pgx.Identifier{"sessions"})`)
	}
	return problems
}

// sessInsertComment is an SQL `--` comment to the end of its line.
var sessInsertComment = regexp.MustCompile(`(?m)--[^\n]*`)

// sessInsertProblems returns what is missing from the session insert that starts
// at the first match in sql, or nil if it is the conditional one. Comments are
// stripped first: queries/sessions.sql explains FOR SHARE and auth_epoch at length
// above the statement, and a check that read the comment would pass a statement
// that had lost the very things the comment describes.
func sessInsertProblems(sql string) []string {
	code := sessInsertComment.ReplaceAllString(sql, "")
	var missing []string
	for _, need := range []struct {
		what    string
		pattern string
	}{
		{"it must insert FROM the users row (INSERT … SELECT … FROM users), not from VALUES", `(?is)\bselect\b.*\bfrom\s+users\b`},
		{"it must be conditional on the epoch the credential check read (users.auth_epoch = …)", `(?is)\bauth_epoch\s*=`},
		{"it must require an active user (users.is_active)", `(?is)\bis_active\b`},
		{"it must lock the user's row FOR SHARE, which is what orders it against a revoke-all", `(?is)\bfor\s+share\b`},
	} {
		if !regexp.MustCompile(need.pattern).MatchString(code) {
			missing = append(missing, need.what)
		}
	}
	return missing
}

// sessInsertStatements splits an sqlc query file into its statements (each begins at
// a `-- name:` line) and returns those that insert into sessions, keyed by name.
func sessInsertStatements(src string) map[string]string {
	out := map[string]string{}
	parts := regexp.MustCompile(`(?m)^-- name: `).Split(src, -1)
	for _, part := range parts[1:] {
		name, _, _ := strings.Cut(part, " ")
		if sessInsertRe.MatchString(part) {
			out[name] = part
		}
	}
	return out
}

// sessGuardGoFiles returns the non-test Go sources under root that are this
// checkout's code, as slash paths relative to root. Generated code is left to its own
// subtest, the directories that hold SQL and docs are not Go, and — the part that
// matters — anything that is a COPY of the tree is skipped: a directory named
// .claude, and any directory that is the root of a checkout of its own (it carries a
// .git entry; a worktree's is a file). A gitignored agent worktree under
// .claude/worktrees/<name>/ holds a full copy of these sources at some other commit,
// and a walk that counted it made a guard that passes in CI fail on a developer's
// machine — the class credential_render_guard_test.go and scope_params_guard_test.go
// in internal/api/handlers already skip.
func sessGuardGoFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".claude", "node_modules", "vendor", "frontend", "generated", "migrations", "queries", "docs":
				return filepath.SkipDir
			}
			if filepath.Clean(path) != filepath.Clean(root) {
				if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(files)
	return files
}

// sessGoTreeProblems is the walk of the Go half of the guard as a function of a root
// directory: it lists the non-test Go sources under root (sessGuardGoFiles), reads each
// and puts it through sessGoFileProblems, and returns how many files it scanned and
// what is wrong with them as "path problem" lines. The production subtest calls it on
// the repository root; TestSessGoTreeProblems calls it on a tree built for the purpose.
// It is a function of its own for the reason sessGoFileProblems is: a walk that
// stopped feeding the files to the check would otherwise pass over the real tree, and
// nothing would say so.
func sessGoTreeProblems(t *testing.T, root string) (scanned int, problems []string) {
	t.Helper()
	files := sessGuardGoFiles(t, root)
	for _, rel := range files {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for _, problem := range sessGoFileProblems(raw) {
			problems = append(problems, rel+" "+problem)
		}
	}
	return len(files), problems
}

// TestSessGuardWalkSkipsNestedCopies pins the walk's exclusions on a tree built for
// the purpose: a copy under .claude/worktrees (by name), a nested checkout elsewhere
// (by its .git file), generated and vendored code and test files are left out; the
// real sources are kept.
func TestSessGuardWalkSkipsNestedCopies(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("internal/auth/session.go")
	write("internal/auth/session_test.go")
	write("internal/db/generated/sessions.sql.go")
	write("vendor/x/x.go")
	write(".claude/worktrees/old/internal/auth/session.go")
	write("elsewhere/checkout/.git")
	write("elsewhere/checkout/internal/auth/session.go")

	got := sessGuardGoFiles(t, root)
	if want := []string{"internal/auth/session.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("walk = %v, want %v: a copy of the tree was counted, or the real source was skipped", got, want)
	}
}

func TestGuard_EverySessionInsertIsConditional(t *testing.T) {
	repoRoot := filepath.Join("..", "..")

	t.Run("the queries", func(t *testing.T) {
		files, err := filepath.Glob(filepath.Join(repoRoot, "queries", "*.sql"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no query files found (%v): the guard would pass vacuously", err)
		}
		found := 0
		for _, f := range files {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			for name, stmt := range sessInsertStatements(string(raw)) {
				found++
				if missing := sessInsertProblems(stmt); len(missing) > 0 {
					t.Errorf("%s: query %s inserts a session, and %s", f, name, strings.Join(missing, "; and "))
				}
			}
		}
		if found != 1 {
			t.Errorf("found %d queries that insert a session, want exactly 1 (CreateSessionAtEpoch): "+
				"a second one is a way to create a session that skips the epoch, and zero means this guard "+
				"no longer recognises the statement it is there to check", found)
		}
	})

	t.Run("the generated queries", func(t *testing.T) {
		files, err := filepath.Glob(filepath.Join(repoRoot, "internal", "db", "generated", "*.sql.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no generated query files found (%v): the guard would pass vacuously", err)
		}
		literal := regexp.MustCompile("(?s)`[^`]*`")
		found := 0
		for _, f := range files {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			for _, lit := range literal.FindAllString(string(raw), -1) {
				if !sessInsertRe.MatchString(lit) {
					continue
				}
				found++
				if missing := sessInsertProblems(lit); len(missing) > 0 {
					t.Errorf("%s: a generated statement inserts a session, and %s — regenerate (make generate) "+
						"from queries/, never edit generated code", f, strings.Join(missing, "; and "))
				}
			}
		}
		if found != 1 {
			t.Errorf("found %d generated statements that insert a session, want exactly 1", found)
		}
	})

	t.Run("no Go code writes the table itself", func(t *testing.T) {
		scanned, problems := sessGoTreeProblems(t, repoRoot)
		if scanned < 50 {
			t.Fatalf("scanned %d Go files, want the whole tree: the walk is not reaching the code it guards", scanned)
		}
		for _, problem := range problems {
			t.Errorf("%s: sessions are created by CreateSessionAtEpoch, through "+
				"auth.SessionManager.CreateSession, and nothing else", problem)
		}
	})

	// The check must be able to fail: the statement it would have to refuse is the
	// plain insert this replaced, and each condition on its own.
	t.Run("the check itself refuses the unconditional statement", func(t *testing.T) {
		plain := "-- name: CreateSession :one\n" +
			"INSERT INTO sessions (user_id, token_hash) VALUES ($1, $2) RETURNING *;\n"
		stmts := sessInsertStatements(plain)
		if len(stmts) != 1 {
			t.Fatalf("the splitter found %d statements in a one-statement file", len(stmts))
		}
		if missing := sessInsertProblems(stmts["CreateSession"]); len(missing) != 4 {
			t.Errorf("the plain insert is missing %d of the 4 conditions, want all 4: %v", len(missing), missing)
		}

		// A statement that has every condition in its COMMENT and none in its code.
		commentOnly := "-- name: CreateSessionLooksSafe :one\n" +
			"-- FROM users WHERE auth_epoch = $1 AND is_active FOR SHARE, all of it, in a comment.\n" +
			"INSERT INTO sessions (user_id, token_hash) VALUES ($1, $2) RETURNING *;\n"
		if missing := sessInsertProblems(sessInsertStatements(commentOnly)["CreateSessionLooksSafe"]); len(missing) != 4 {
			t.Errorf("conditions named only in a comment counted: %d of 4 missing, want all 4", len(missing))
		}

		full := "-- name: CreateSessionAtEpoch :one\n" +
			"INSERT INTO sessions (user_id, token_hash) SELECT u.id, $1::text FROM users u " +
			"WHERE u.id = $2 AND u.auth_epoch = $3 AND u.is_active FOR SHARE OF u RETURNING *;\n"
		if missing := sessInsertProblems(sessInsertStatements(full)["CreateSessionAtEpoch"]); len(missing) != 0 {
			t.Errorf("the conditional insert was refused: %v", missing)
		}

		for _, drop := range []string{"AND u.auth_epoch = $3", "AND u.is_active", "FOR SHARE OF u"} {
			cut := strings.Replace(full, drop, "", 1)
			if missing := sessInsertProblems(sessInsertStatements(cut)["CreateSessionAtEpoch"]); len(missing) != 1 {
				t.Errorf("without %q the check found %d problems, want exactly 1: %v", drop, len(missing), missing)
			}
		}
	})

	// A statement is found however its table is spelled. Each of these is the plain
	// insert in a different spelling, and each must be found (and then refused for
	// lacking the four conditions); the last two are other tables and must not be.
	t.Run("the table is found however it is spelled", func(t *testing.T) {
		for _, spelling := range []string{
			`INSERT INTO sessions (user_id) VALUES ($1)`,
			`insert into sessions (user_id) values ($1)`,
			`INSERT INTO "sessions" (user_id) VALUES ($1)`,
			`INSERT INTO public.sessions (user_id) VALUES ($1)`,
			`INSERT INTO public . sessions (user_id) VALUES ($1)`,
			`INSERT INTO "public"."sessions" (user_id) VALUES ($1)`,
			`INSERT INTO public."sessions" (user_id) VALUES ($1)`,
			"INSERT\n\tINTO\n\tsessions (user_id) VALUES ($1)",
			"INSERT   INTO   sessions(user_id) VALUES ($1)",
			// A schema that is not public, bare and quoted, in every combination.
			`INSERT INTO nexara.sessions (user_id) VALUES ($1)`,
			`INSERT INTO "nexara"."sessions" (user_id) VALUES ($1)`,
			`INSERT INTO nexara."sessions" (user_id) VALUES ($1)`,
			`INSERT INTO "nexara".sessions (user_id) VALUES ($1)`,
			`INSERT INTO "my schema"."sessions" (user_id) VALUES ($1)`,
			`INSERT INTO nexara . sessions (user_id) VALUES ($1)`,
			"INSERT INTO nexara\n\t.\n\tsessions (user_id) VALUES ($1)",
			`INSERT INTO nexara_db.nexara.sessions (user_id) VALUES ($1)`,
			// The same, inside an interpreted Go string, where the quotes are escaped.
			`"INSERT INTO \"nexara\".\"sessions\" (user_id) VALUES ($1)"`,
			`"INSERT INTO \"sessions\" (user_id) VALUES ($1)"`,
			// MERGE is an insert when the row is not matched.
			`MERGE INTO sessions s USING (VALUES ($1)) AS v(id) ON false WHEN NOT MATCHED THEN INSERT (user_id) VALUES (v.id)`,
			`MERGE INTO public.sessions s USING users u ON false WHEN NOT MATCHED THEN INSERT (user_id) VALUES (u.id)`,
			`merge into "nexara"."sessions" s USING users u ON false WHEN NOT MATCHED THEN INSERT (user_id) VALUES (u.id)`,
			"MERGE\n\tINTO\n\tnexara.sessions s USING users u ON false WHEN NOT MATCHED THEN INSERT (user_id) VALUES (u.id)",
		} {
			stmts := sessInsertStatements("-- name: Sneaky :one\n" + spelling + " RETURNING *;\n")
			if len(stmts) != 1 {
				t.Errorf("the spelling %q was not recognised as an insert into sessions", spelling)
				continue
			}
			if missing := sessInsertProblems(stmts["Sneaky"]); len(missing) != 4 {
				t.Errorf("the spelling %q was found but %d of 4 conditions were reported missing, want all 4", spelling, len(missing))
			}
		}
		for _, other := range []string{
			`INSERT INTO veeam_sessions (id) VALUES ($1)`,
			`INSERT INTO sessions_archive (id) VALUES ($1)`,
			`INSERT INTO "veeam_sessions" (id) VALUES ($1)`,
			`INSERT INTO nexara.veeam_sessions (id) VALUES ($1)`,
			`INSERT INTO "nexara"."sessions_archive" (id) VALUES ($1)`,
			`MERGE INTO veeam_sessions s USING users u ON false WHEN NOT MATCHED THEN INSERT (id) VALUES (u.id)`,
			`INSERT INTO users (email, sessions) VALUES ($1, $2)`,
		} {
			if got := sessInsertStatements("-- name: Other :one\n" + other + ";\n"); len(got) != 0 {
				t.Errorf("%q was taken for an insert into sessions", other)
			}
		}
	})
}

// TestSessGuardFindsTheCopyProtocol: pgx's CopyFrom writes rows without a statement of
// ours, naming the table by identifier, and is the other way a Go file could create a
// session that skips the conditional insert. The identifier is found in any schema
// position; another table whose name merely contains the word is not.
func TestSessGuardFindsTheCopyProtocol(t *testing.T) {
	for _, src := range []string{
		`conn.CopyFrom(ctx, pgx.Identifier{"sessions"}, cols, rows)`,
		`conn.CopyFrom(ctx, pgx.Identifier{"public", "sessions"}, cols, rows)`,
		`conn.CopyFrom(ctx, pgx.Identifier{ "nexara" , "sessions" }, cols, rows)`,
		"conn.CopyFrom(ctx, pgx.Identifier{\n\t\"nexara\",\n\t\"sessions\",\n}, cols, rows)",
	} {
		if !sessCopyRe.MatchString(src) {
			t.Errorf("the copy into sessions in %q was not found", src)
		}
	}
	for _, src := range []string{
		`conn.CopyFrom(ctx, pgx.Identifier{"veeam_sessions"}, cols, rows)`,
		`conn.CopyFrom(ctx, pgx.Identifier{"public", "sessions_archive"}, cols, rows)`,
		`pgx.Identifier{"users"}`,
	} {
		if sessCopyRe.MatchString(src) {
			t.Errorf("%q was taken for a copy into sessions", src)
		}
	}
}

// TestSessGoFileProblems tries the per-file check of the Go walk on sources made for
// the purpose, as the walker tests do for the walk itself: each way of writing the
// table is found, in its own right, and a source that only talks about the table, or
// reads it, or writes another one, is left alone. A walk over the real tree passes
// vacuously if its check is switched off, so this is what proves the check can fail.
func TestSessGoFileProblems(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want int // how many problems, one per way of writing the table
	}{
		{"a file that creates sessions the sanctioned way", "sm.CreateSession(ctx, id, epoch, token, role, ua, ip, ttl, dev)\n// sessions are inserted by CreateSessionAtEpoch", 0},
		{"a plain INSERT in a raw string", "const q = `INSERT INTO sessions (user_id) VALUES ($1)`", 1},
		{"an INSERT in another schema, quoted, in an interpreted string", `q := "INSERT INTO \"nexara\".\"sessions\" (user_id) VALUES ($1)"`, 1},
		{"a MERGE", "const q = `MERGE INTO sessions s USING users u ON false WHEN NOT MATCHED THEN INSERT (user_id) VALUES (u.id)`", 1},
		{"a COPY from STDIN", "const q = `COPY sessions (user_id, token_hash) FROM STDIN`", 1},
		{"a COPY from a file, in a schema", "const q = `COPY public.sessions FROM '/tmp/sessions.csv'`", 1},
		{"a COPY from a program, the table quoted", "const q = `copy \"sessions\" from program 'cat x'`", 1},
		{"a COPY in an interpreted string", `q := "COPY nexara.sessions (id) FROM STDIN"`, 1},
		{"the copy protocol by identifier", `conn.CopyFrom(ctx, pgx.Identifier{"public", "sessions"}, cols, rows)`, 1},
		{"every way at once", "a := `INSERT INTO sessions (id) VALUES ($1)`\nb := `COPY sessions FROM STDIN`\nc := pgx.Identifier{\"sessions\"}", 3},

		// What is left alone.
		{"prose that says copy sessions from", "// copy sessions from the old table when the migration runs", 0},
		{"a COPY that only reads the table out", "const q = `COPY (SELECT * FROM sessions) TO STDOUT`", 0},
		{"another table's INSERT", "const q = `INSERT INTO veeam_sessions (id) VALUES ($1)`", 0},
		{"another table's COPY", "const q = `COPY sessions_archive (id) FROM STDIN`", 0},
		{"another table's identifier", `conn.CopyFrom(ctx, pgx.Identifier{"veeam_sessions"}, cols, rows)`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sessGoFileProblems([]byte(tt.src))
			if len(got) != tt.want {
				t.Errorf("sessGoFileProblems(%q) = %v, want %d problem(s)", tt.src, got, tt.want)
			}
		})
	}
}

// TestSessGoTreeProblems tries the walk of the Go half of the guard on a tree made for
// the purpose, as TestSessGuardWalkSkipsNestedCopies does for the file list: files
// that write the table in each way are REPORTED, with their path, and a clean file, a
// test file and a copy of the tree under .claude are not. It is what shows that the
// walk really feeds what it finds to the check — the same gap TestSessGoFileProblems
// closes for the check itself — so that a walk whose call to the check was disabled
// or fed nothing fails here and not silently over the real tree.
func TestSessGoTreeProblems(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("internal/clean/clean.go", "package clean\n\n// sessions are created by CreateSessionAtEpoch.\nfunc Create() {}\n")
	write("internal/rogue/insert.go", "package rogue\n\nconst q = `INSERT INTO sessions (user_id) VALUES ($1)`\n")
	write("internal/rogue/copy.go", "package rogue\n\nconst c = `COPY sessions (user_id) FROM STDIN`\n")
	// The same violation where the walk must not look: a test file, a copy of the tree
	// under .claude, generated code.
	write("internal/rogue/insert_test.go", "package rogue\n\nconst q = `INSERT INTO sessions (user_id) VALUES ($1)`\n")
	write(".claude/worktrees/old/internal/rogue/insert.go", "package rogue\n\nconst q = `INSERT INTO sessions (user_id) VALUES ($1)`\n")
	write("internal/db/generated/sessions.sql.go", "package db\n\nconst q = `INSERT INTO sessions (user_id) VALUES ($1)`\n")

	scanned, problems := sessGoTreeProblems(t, root)

	if scanned != 3 {
		t.Errorf("scanned %d files, want the 3 non-test sources outside the skipped directories", scanned)
	}
	want := []string{
		"internal/rogue/copy.go loads the sessions table with a COPY statement",
		"internal/rogue/insert.go writes INSERT INTO (or MERGE INTO) sessions itself",
	}
	sort.Strings(problems)
	if !reflect.DeepEqual(problems, want) {
		t.Errorf("problems = %q, want exactly %q: a file that writes the table was not reported, or one the walk must skip was", problems, want)
	}
}
