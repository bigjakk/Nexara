package auth

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// newEpochSessionManager is a manager over fake and a miniredis the test can look
// into: the Redis rows a created session leaves are half of what is checked.
func newEpochSessionManager(t *testing.T, fake *fakeSessionDB) (*SessionManager, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewSessionManager(db.New(fake), rdb), mr
}

// TestCreateSession_ARefusalAndAFailureAreDifferentAnswers pins the three-outcome
// rule at the manager: the insert answering "no row" is the refusal of the sign-in
// (ErrSessionRefused) and nothing else is — a database that could not be asked, or
// that failed, is an error of its own and must never be readable as the credential
// having been refused (it would blame the user for the database) nor as a session
// that exists. Neither writes a Redis row; only a created session does.
//
// The created case also pins WHAT the statement is asked: the user and the epoch the
// caller passed, in the generated argument order — the arguments the whole
// guarantee rests on, which a refactor that swapped or dropped one would turn into a
// check against the wrong value without any other test noticing.
func TestCreateSession_ARefusalAndAFailureAreDifferentAnswers(t *testing.T) {
	userID := uuid.New()
	const epoch = int64(41)
	transient := fmt.Errorf("failed to receive message: %w", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET})
	defect := errors.New("insert or update on table \"sessions\" violates a check constraint")

	create := func(sm *SessionManager) (db.Session, error) {
		return sm.CreateSession(context.Background(), userID, epoch, "refresh-token-value", "admin",
			"Mozilla/5.0", "192.0.2.10", time.Hour, DeviceInfo{Name: "Laptop", Type: "web", ID: "device-0001"})
	}

	t.Run("a created session is returned and has its Redis row", func(t *testing.T) {
		want := db.Session{ID: uuid.New(), UserID: userID, TokenHash: HashToken("refresh-token-value"), UserRole: "admin"}
		fake := &fakeSessionDB{created: want}
		sm, mr := newEpochSessionManager(t, fake)
		// The row is asserted the moment CreateSession returns, which holds for a healthy
		// Redis because the write is waited for — for as long as the wait lasts. A generous
		// wait keeps this test from depending on the write finishing inside the production
		// second on a loaded machine: it returns the moment the write is done.
		sm.mirrorWait = 30 * time.Second

		got, err := create(sm)

		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if got.ID != want.ID {
			t.Errorf("session = %v, want %v", got.ID, want.ID)
		}
		if !mr.Exists(redisKey(want.ID.String())) {
			t.Error("the created session has no Redis row")
		}

		calls := fake.named("CreateSessionAtEpoch")
		if len(calls) != 1 {
			t.Fatalf("%d CreateSessionAtEpoch statements, want 1", len(calls))
		}
		args := calls[0].args
		// token_hash, user_agent, ip_address, expires_at, device_name, device_type,
		// device_id, user_role, user_id, epoch — the order queries/sessions.sql generates.
		if len(args) != 10 {
			t.Fatalf("the statement got %d arguments, want 10: %v", len(args), args)
		}
		if args[0] != HashToken("refresh-token-value") {
			t.Errorf("token hash argument = %v, want the hash of the refresh token (never the token itself)", args[0])
		}
		if args[8] != userID {
			t.Errorf("user argument = %v, want %v", args[8], userID)
		}
		if args[9] != epoch {
			t.Errorf("epoch argument = %v (%T), want %d: the insert would be conditional on the wrong generation", args[9], args[9], epoch)
		}
	})

	t.Run("a Redis that is down does not fail a session that was created", func(t *testing.T) {
		// The Redis row mirrors the session for nobody to read (see WriteSessionRedis), and
		// it is written after the insert has committed: failing the sign-in over it would
		// answer "nothing was issued" for a session that is live in PostgreSQL, with no
		// token anybody holds for it.
		want := db.Session{ID: uuid.New(), UserID: userID, TokenHash: HashToken("refresh-token-value"), UserRole: "admin"}
		fake := &fakeSessionDB{created: want}
		mr := miniredis.RunT(t)
		// No retries and a short dial: the client's defaults would spend a second and a
		// half failing, which is the very thing under test going slowly.
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
		t.Cleanup(func() { _ = rdb.Close() })
		sm := NewSessionManager(db.New(fake), rdb)
		mr.Close()

		got, err := create(sm)

		if err != nil {
			t.Fatalf("error = %v: a failed mirror write must not fail the sign-in", err)
		}
		if got.ID != want.ID {
			t.Errorf("session = %v, want %v", got.ID, want.ID)
		}
		if n := len(fake.named("CreateSessionAtEpoch")); n != 1 {
			t.Errorf("%d inserts, want 1", n)
		}
	})

	t.Run("no row from the insert is the refusal, and nothing is written", func(t *testing.T) {
		fake := &fakeSessionDB{createErr: pgx.ErrNoRows}
		sm, mr := newEpochSessionManager(t, fake)

		got, err := create(sm)

		if !errors.Is(err, ErrSessionRefused) {
			t.Fatalf("error = %v, want ErrSessionRefused", err)
		}
		if got.ID != uuid.Nil {
			t.Errorf("a refused sign-in returned session %v", got.ID)
		}
		if keys := mr.Keys(); len(keys) != 0 {
			t.Errorf("a refused sign-in left Redis rows %v", keys)
		}
	})

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"a database that could not be reached", transient},
		{"a defect in the statement", defect},
	} {
		t.Run(tc.name+" is an error, not a refusal", func(t *testing.T) {
			fake := &fakeSessionDB{createErr: tc.err}
			sm, mr := newEpochSessionManager(t, fake)

			got, err := create(sm)

			if err == nil {
				t.Fatal("no error: a session that was not created was reported as created")
			}
			if errors.Is(err, ErrSessionRefused) {
				t.Errorf("error = %v reads as a refusal: the credential would be blamed for the database", err)
			}
			if !errors.Is(err, tc.err) {
				t.Errorf("error = %v, want it to wrap the cause so the caller can classify it", err)
			}
			if got.ID != uuid.Nil {
				t.Errorf("a failed insert returned session %v", got.ID)
			}
			if keys := mr.Keys(); len(keys) != 0 {
				t.Errorf("a failed insert left Redis rows %v", keys)
			}
		})
	}
}

// lockedLog is a log buffer the mirror goroutine can write to while the test reads it.
type lockedLog struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (l *lockedLog) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(b)
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// captureLockedLog routes the default slog logger to a buffer that is safe to read
// while a goroutine is still writing to it. Sequential tests only: it swaps a
// process-wide logger.
func captureLockedLog(t *testing.T) *lockedLog {
	t.Helper()
	out := &lockedLog{}
	saved := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(saved) })
	return out
}

// stuckRedis is a go-redis hook that holds every command stuck answers true for until
// release is closed — what a Redis that accepts a connection and never answers looks
// like to its caller: the command neither succeeds nor fails, and no context deadline
// reaches it. panics makes such a command panic instead.
type stuckRedis struct {
	stuck   func(cmd redis.Cmder) bool
	release chan struct{}
	panics  bool
}

func (h *stuckRedis) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *stuckRedis) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if h.stuck(cmd) {
			if h.panics {
				panic("the Redis client panicked")
			}
			<-h.release
		}
		return next(ctx, cmd)
	}
}

func (h *stuckRedis) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// isSessionMirrorWrite is the SET of a session's Redis row.
func isSessionMirrorWrite(cmd redis.Cmder) bool {
	args := cmd.Args()
	if len(args) < 2 || args[0] != "set" {
		return false
	}
	key, _ := args[1].(string)
	return strings.HasPrefix(key, "nexara:session:")
}

// TestCreateSession_ARedisThatNeverAnswersDoesNotHoldTheSignIn: the Redis row of a
// session is a mirror nothing reads, written after the insert has committed, and a
// Redis that accepts the write and never answers must not hold every sign-in for the
// client's read timeout (five seconds, and a failed read is tried again). The write
// is waited for for a moment and then left to finish on its own.
//
// The command is held by a hook, not by a short context deadline, because that is
// the point: a deadline does not reach the client's socket, so only not waiting
// bounds it. What is checked is that the sign-in returns the session it created
// while the write is still stuck; that the warning names the session and the user and
// never the token or its hash; and that the write is not abandoned — released, it
// lands.
func TestCreateSession_ARedisThatNeverAnswersDoesNotHoldTheSignIn(t *testing.T) {
	logs := captureLockedLog(t)
	userID := uuid.New()
	want := db.Session{ID: uuid.New(), UserID: userID, TokenHash: HashToken("refresh-token-value"), UserRole: "admin"}
	fake := &fakeSessionDB{created: want}
	sm, mr := newEpochSessionManager(t, fake)
	sm.mirrorWait = 50 * time.Millisecond

	release := make(chan struct{})
	var once sync.Once
	releaseAll := func() { once.Do(func() { close(release) }) }
	t.Cleanup(releaseAll)
	sm.redis.AddHook(&stuckRedis{stuck: isSessionMirrorWrite, release: release})

	// CreateSession runs on a goroutine of its own so that one that waits for the stuck
	// write fails this test in a few seconds instead of hanging it.
	type created struct {
		session db.Session
		err     error
	}
	result := make(chan created, 1)
	start := time.Now()
	go func() {
		s, err := sm.CreateSession(context.Background(), userID, 3, "refresh-token-value", "admin",
			"Mozilla/5.0", "192.0.2.10", time.Hour, DeviceInfo{})
		result <- created{s, err}
	}()
	var got db.Session
	var err error
	select {
	case r := <-result:
		got, err = r.session, r.err
	case <-time.After(5 * time.Second):
		releaseAll()
		t.Fatal("CreateSession did not return within 5 s with a 50 ms mirror wait: it waits for the stuck write")
	}
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("error = %v: a mirror that is stuck must not fail the sign-in", err)
	}
	if got.ID != want.ID {
		t.Errorf("session = %v, want %v", got.ID, want.ID)
	}
	if elapsed > 2*time.Second {
		t.Errorf("CreateSession took %v with a 50 ms mirror wait: it waited for the stuck write", elapsed)
	}
	if mr.Exists(redisKey(want.ID.String())) {
		t.Error("the row exists although the write is held: the premise of the test is gone")
	}
	line := logs.String()
	if !strings.Contains(line, "taking too long") || !strings.Contains(line, want.ID.String()) || !strings.Contains(line, userID.String()) {
		t.Errorf("no warning naming the session and the user for the write that was given up on; log:\n%s", line)
	}
	if strings.Contains(line, "refresh-token-value") || strings.Contains(line, HashToken("refresh-token-value")) {
		t.Error("the refresh token, or its hash, reached the log")
	}

	// The write was not abandoned: released, it lands.
	releaseAll()
	deadline := time.Now().Add(5 * time.Second)
	for !mr.Exists(redisKey(want.ID.String())) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !mr.Exists(redisKey(want.ID.String())) {
		t.Error("the released write never landed: it was abandoned, not left to finish")
	}
}

// TestCreateSession_AHealthyRedisDoesNotMakeTheSignInWaitOutTheWait is the other half of
// the stuck-Redis test: when the write finishes, CreateSession returns THEN, and not
// when the wait ends. The wait is thirty seconds here, so a CreateSession that waited
// for its timer on a healthy Redis — because the write's end was never signalled —
// shows as a watchdog failure and not as a second that might be noise, and the
// warning that the write is "taking too long" must not have been written for a write
// that finished at once.
func TestCreateSession_AHealthyRedisDoesNotMakeTheSignInWaitOutTheWait(t *testing.T) {
	logs := captureLockedLog(t)
	userID := uuid.New()
	want := db.Session{ID: uuid.New(), UserID: userID, TokenHash: HashToken("refresh-token-value"), UserRole: "admin"}
	sm, mr := newEpochSessionManager(t, &fakeSessionDB{created: want})
	sm.mirrorWait = 30 * time.Second

	type created struct {
		session db.Session
		err     error
	}
	result := make(chan created, 1)
	start := time.Now()
	go func() {
		s, err := sm.CreateSession(context.Background(), userID, 3, "refresh-token-value", "admin",
			"Mozilla/5.0", "192.0.2.10", time.Hour, DeviceInfo{})
		result <- created{s, err}
	}()
	select {
	case r := <-result:
		if r.err != nil || r.session.ID != want.ID {
			t.Fatalf("CreateSession = (%v, %v), want the session it created", r.session.ID, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CreateSession had not returned after 5 s on a healthy Redis with a 30 s mirror wait: it waits for its timer, not for the write")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("CreateSession took %v on a healthy Redis", elapsed)
	}
	if !mr.Exists(redisKey(want.ID.String())) {
		t.Error("the row was not written before CreateSession returned")
	}
	if line := logs.String(); strings.Contains(line, "taking too long") || strings.Contains(line, "was not written") {
		t.Errorf("a warning was written for a write that finished at once; log:\n%s", line)
	}
}

// TestCreateSession_APanickingMirrorWriteDoesNotTakeTheProcessDown: the write runs on
// a goroutine nothing is waiting on, and a panic there would end the process for the
// sake of a cache row. It is recovered, logged with the session and the user, and the
// sign-in that created the session carries on.
func TestCreateSession_APanickingMirrorWriteDoesNotTakeTheProcessDown(t *testing.T) {
	logs := captureLockedLog(t)
	userID := uuid.New()
	want := db.Session{ID: uuid.New(), UserID: userID, TokenHash: HashToken("refresh-token-value"), UserRole: "admin"}
	sm, _ := newEpochSessionManager(t, &fakeSessionDB{created: want})
	sm.redis.AddHook(&stuckRedis{stuck: isSessionMirrorWrite, panics: true})

	got, err := sm.CreateSession(context.Background(), userID, 3, "refresh-token-value", "admin",
		"Mozilla/5.0", "192.0.2.10", time.Hour, DeviceInfo{})

	if err != nil || got.ID != want.ID {
		t.Fatalf("CreateSession = (%v, %v), want the session it created", got.ID, err)
	}
	line := logs.String()
	if !strings.Contains(line, "panicked") || !strings.Contains(line, want.ID.String()) {
		t.Errorf("the panic was not logged with the session; log:\n%s", line)
	}
}

// authGuardGoFiles returns the non-test Go sources under root that are this
// checkout's code, as slash paths relative to root. It skips generated code (it
// defines the methods rather than calling them), vendored trees, node_modules and —
// the part that matters — anything that is a COPY of the tree rather than part of it:
// a directory named .claude, and any directory that is the root of a checkout of its
// own (it carries a .git entry; a worktree's is a file). A gitignored agent worktree
// under .claude/worktrees/<name>/ holds a full copy of these sources at some other
// commit, and a walk that counted its session.go beside this one made a guard that
// passes in CI fail on a developer's machine — the class credential_render_guard_test.go
// and scope_params_guard_test.go in internal/api/handlers already skip.
func authGuardGoFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".claude", "node_modules", "vendor", "generated", "frontend":
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

// TestAuthGuardWalkSkipsNestedCopies pins the walk's exclusions on a tree built for
// the purpose: a copy under .claude/worktrees (by name), a nested checkout elsewhere
// (by its .git file), generated and vendored code and test files are left out; the
// real sources are kept.
func TestAuthGuardWalkSkipsNestedCopies(t *testing.T) {
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

	got := authGuardGoFiles(t, root)
	if want := []string{"internal/auth/session.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("walk = %v, want %v: a copy of the tree was counted, or the real source was skipped", got, want)
	}
}

// TestGuard_EveryRevokeAllGoesThroughTheBump holds the structure the epoch rests on,
// read from the source of the whole tree: ending every session of a user, moving the
// user's epoch, and creating a session are each done in ONE place.
//
//   - Nothing but RevokeAllUserSessionsIn may mention RevokeAllUserSessions or
//     BumpUserAuthEpoch, and nothing but SessionManager.CreateSession may mention
//     CreateSessionAtEpoch. "Mention" is any selector of that name — a call, a method
//     value (`f := q.RevokeAllUserSessions`), a call through an alias
//     (`store := h.queries; store.RevokeAllUserSessions(…)`), through a db.Querier
//     field or a locally declared interface — and in a package-level initializer as
//     well as in a function. It does not depend on what the receiver is called or
//     what type it has, which is what an earlier version of this guard did, and which
//     is why SessionManager.RevokeAllUserSessions, whose name collided with the
//     query's, had to go. A handler that revoked straight through the Queries — the
//     tempting two-line version — would end the sessions without moving the epoch,
//     and an in-flight sign-in would mint a live one afterwards; a handler that
//     bumped on its own would refuse sign-ins for no reason that anything records;
//     and CreateSessionAtEpoch is what turns "no row" into ErrSessionRefused and
//     everything else into an error, which a caller of the raw query would read as it
//     liked.
//   - Inside RevokeAllUserSessionsIn the three statements are written in the order
//     bump, list, revoke. TestRevokeAllUserSessionsIn pins the order at run time; this
//     pins it in the text, so that a reordering fails here too and for the reason
//     above, not only where a fake happens to count.
//
// ListUserSessions is not guarded by caller: the session list endpoint reads it too,
// and a read is not the hazard.
//
// What it does NOT see, so that nobody reads it as more than it is. It matches Go
// selector NAMES, so:
//
//   - a NEW sqlc query that revokes a user's sessions under another name (an
//     `UPDATE sessions SET is_revoked = true …` that is not RevokeAllUserSessions) is
//     not seen, nor is a revoke written some other way that never names the three
//     methods above;
//   - a call made through reflect (`MethodByName("RevokeAllUserSessions")`) is not seen;
//   - code in a directory the walk skips by name — generated, vendor, frontend and
//     .claude — is not seen.
//
// What covers them is not this guard. For the revoke-alls that exist, the DB tests —
// TestRevokeAllUserSessionsIn_EveryPathMovesTheEpochAndEndsTheSessions and
// TestRevokeAllTransactions_AgainstASignInInProgress in internal/db — run each one
// against Postgres and fail if a session survives or the epoch does not move. For a
// new query that revokes a user's sessions without the bump, nothing automated does:
// review does.
func TestGuard_EveryRevokeAllGoesThroughTheBump(t *testing.T) {
	root := filepath.Join("..", "..")
	files := authGuardGoFiles(t, root)
	if len(files) < 100 {
		t.Fatalf("scanned %d Go files, want the whole tree: the walk is not reaching the code it guards", len(files))
	}

	type site struct{ file, fn string }
	watched := map[string]bool{"RevokeAllUserSessions": true, "BumpUserAuthEpoch": true, "CreateSessionAtEpoch": true}
	mentions := map[string][]site{}

	for _, rel := range files {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		inspect := func(fn string, node ast.Node) {
			ast.Inspect(node, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok && watched[sel.Sel.Name] {
					mentions[sel.Sel.Name] = append(mentions[sel.Sel.Name], site{rel, fn})
				}
				return true
			})
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Body != nil {
					inspect(d.Name.Name, d.Body)
				}
			case *ast.GenDecl:
				inspect("", d) // a package-level initializer
			}
		}
	}

	allowed := map[string]site{
		"RevokeAllUserSessions": {"internal/auth/session.go", "RevokeAllUserSessionsIn"},
		"BumpUserAuthEpoch":     {"internal/auth/session.go", "RevokeAllUserSessionsIn"},
		"CreateSessionAtEpoch":  {"internal/auth/session.go", "CreateSession"},
	}
	names := make([]string, 0, len(allowed))
	for name := range allowed {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		got := mentions[name]
		if len(got) != 1 || got[0] != allowed[name] {
			t.Errorf("%s is mentioned from %v, want exactly %v: "+
				"every revoke-all must go through auth.RevokeAllUserSessionsIn and every session be created "+
				"through auth.SessionManager.CreateSession (see this test's comment for why)", name, got, allowed[name])
		}
	}

	// The order of the three statements, read from the function body.
	want := []string{"BumpUserAuthEpoch", "ListUserSessions", "RevokeAllUserSessions"}
	got := epochStatementOrder(t, filepath.Join(root, "internal", "auth", "session.go"), "RevokeAllUserSessionsIn", want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("RevokeAllUserSessionsIn issues %v in that order, want %v: the bump must be a statement of its own, first", got, want)
	}
}

// epochStatementOrder returns the names in want, in the order the function fn in file
// calls them as methods.
func epochStatementOrder(t *testing.T, file, fn string, want []string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	type hit struct {
		name string
		pos  token.Pos
	}
	var hits []hit
	for _, decl := range f.Decls {
		d, ok := decl.(*ast.FuncDecl)
		if !ok || d.Name.Name != fn || d.Body == nil {
			continue
		}
		ast.Inspect(d.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			for _, w := range want {
				if sel.Sel.Name == w {
					hits = append(hits, hit{w, call.Pos()})
				}
			}
			return true
		})
	}
	if len(hits) == 0 {
		t.Fatalf("%s does not call any of %v: the guard no longer finds what it checks", fn, want)
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].pos < hits[j].pos })
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.name
	}
	return out
}
