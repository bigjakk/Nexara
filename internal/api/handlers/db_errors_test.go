package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/puddle/v2"
)

// neverSent is an error pgconn calls safe to retry: the request never went out.
type neverSent struct{}

func (neverSent) Error() string     { return "the request was never sent" }
func (neverSent) SafeToRetry() bool { return true }

// pgState is an error as the server reports it, with a SQLSTATE.
func pgState(code string) error {
	return fmt.Errorf("executing: %w", &pgconn.PgError{Code: code, Message: "reported by the server"})
}

// TestIsTransientDBError pins which database failures are "the database was not there to
// ask" (a 503 from every auth handler) and which are defects (a 500), so a row moving from one
// list to the other changes what clients are told. The transient rows are the shapes pgx
// produces, wrapped the way the call sites wrap them; the rest must never become a 503:
// constraint violations, undefined objects, malformed values, pgx.ErrNoRows, a scan that does
// not fit.
func TestIsTransientDBError(t *testing.T) {
	transient := map[string]error{
		"our own bound running out":         fmt.Errorf("rotating: %w", context.DeadlineExceeded),
		"a cancelled context":               context.Canceled,
		"a connection reset under read":     errRaceTransient,
		"a refused dial":                    &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED},
		"a failed name lookup":              &net.DNSError{Err: "no such host", Name: "db.example.com", IsNotFound: true},
		"an EOF in the middle of a message": fmt.Errorf("failed to receive message: %w", io.ErrUnexpectedEOF),
		"a bare EOF":                        io.EOF,
		"a closed connection":               fmt.Errorf("write: %w", net.ErrClosed),
		"a connect error":                   &pgconn.ConnectError{Config: &pgconn.Config{}},
		"a request that never went out":     fmt.Errorf("acquiring: %w", neverSent{}),
		// A request that arrives while the server shuts down meets a closed pool.
		// pgxpool returns puddle's error as it is: it has no sentinel of its own.
		"a closed pool":          puddle.ErrClosedPool,
		"a closed pool, wrapped": fmt.Errorf("acquiring a connection: %w", puddle.ErrClosedPool),
	}
	for _, code := range []string{
		"08000", "08001", "08003", "08004", "08006", "08P01", // class 08: connection exception
		"53000", "53100", "53200", "53300", // class 53: insufficient resources
		"57P01", "57P02", "57P03", // admin shutdown, crash shutdown, cannot connect now
		"57P05",          // idle session timeout: the server closed a session that sat idle
		"57014",          // query cancelled
		"40001", "40P01", // serialization failure, deadlock
		"55P03", // lock not available
		"25006", // read-only transaction: a write that reached a replica after a failover
		"25P03", // idle-in-transaction timeout: the server ended a transaction left idle
	} {
		transient["SQLSTATE "+code] = pgState(code)
	}

	defects := map[string]error{
		"nothing at all":               nil,
		"pgx.ErrNoRows":                fmt.Errorf("looking up: %w", pgx.ErrNoRows),
		"a transaction already closed": pgx.ErrTxClosed,
		"an error nobody classified":   errors.New("unexpected database failure"),
		"a scan that does not fit":     errors.New("scan destination 2 is *string, want *int"),
		"a text that says reset":       errors.New("connection reset by peer"),
		"a wrapped defect":             fmt.Errorf("revoking: %w", errors.New("unexpected database failure")),
	}
	for _, code := range []string{
		"23502", "23503", "23505", "23514", // not null, foreign key, unique, check
		"42601", "42703", "42P01", // syntax error, undefined column, undefined table
		"22001", "22P02", // string too long, invalid text representation
		"P0001", "XX000", // raise_exception, internal_error
		"57P04",          // database dropped
		"25P02", "25001", // in failed transaction, active transaction: the transaction class is not transient as a whole
	} {
		defects["SQLSTATE "+code] = pgState(code)
	}

	for name, err := range transient {
		if !isTransientDBError(err) {
			t.Errorf("%s (%T) is not classified as transient: the handlers would answer it 500", name, err)
		}
	}
	for name, err := range defects {
		if isTransientDBError(err) {
			t.Errorf("%s (%T) is classified as transient: the handlers would answer it 503, \"retry shortly\", for ever", name, err)
		}
	}
}

// TestADatabaseFailureIsA503OrA500ByWhatItIs drives each failure the handlers handle through
// the real handlers: the database being away is a 503 (nothing was decided, retry) and a defect
// a 500, and in both the cookie is left alone, the session is untouched and nothing is issued.
// The classification itself is TestIsTransientDBError's, so each site is shown to use it with
// one error of each side and each SQLSTATE route, not with a row per kind of error.
func TestADatabaseFailureIsA503OrA500ByWhatItIs(t *testing.T) {
	kinds := []struct {
		name string
		err  error
		want int
	}{
		{"a connection reset", errRaceTransient, http.StatusServiceUnavailable},
		{"a server shutting down (57P01)", pgState("57P01"), http.StatusServiceUnavailable},
		{"a unique violation (23505)", pgState("23505"), http.StatusInternalServerError},
		{"an error nobody classified", errRaceBug, http.StatusInternalServerError},
	}

	type site struct {
		name string
		// inject arms the failure; path, cookie and hdr (send as the account) make the request.
		inject func(a *authRaceApp, err error)
		path   string
		cookie string
		hdr    bool
		// refresh marks the refresh sites, whose failures must also leave no Redis row
		// and revoke nothing.
		refresh bool
	}
	sites := []site{
		{name: "Refresh: the session lookup", inject: func(a *authRaceApp, err error) { a.store.currentErr = err }, path: "/auth/refresh", cookie: raceCurrentToken, refresh: true},
		{name: "Refresh: the permission lookup", inject: func(a *authRaceApp, err error) { a.store.permsErr = err }, path: "/auth/refresh", cookie: raceCurrentToken, refresh: true},
		{name: "Refresh: the start of the transaction", inject: func(a *authRaceApp, err error) { a.pool.beginErr = err }, path: "/auth/refresh", cookie: raceCurrentToken, refresh: true},
		{name: "Refresh: the user lookup", inject: func(a *authRaceApp, err error) { a.store.userErr = err }, path: "/auth/refresh", cookie: raceCurrentToken, refresh: true},
		{name: "Refresh: the rotation", inject: func(a *authRaceApp, err error) { a.store.rotateErr = err }, path: "/auth/refresh", cookie: raceCurrentToken, refresh: true},
		{name: "Refresh: the commit", inject: func(a *authRaceApp, err error) { a.store.commitErr = err }, path: "/auth/refresh", cookie: raceCurrentToken, refresh: true},
		{name: "Logout: the session lookup", inject: func(a *authRaceApp, err error) { a.store.currentErr = err }, path: "/auth/logout", cookie: raceCurrentToken},
		{name: "Logout: the revoke", inject: func(a *authRaceApp, err error) { a.store.revokeErr = err }, path: "/auth/logout", cookie: raceCurrentToken},
		{name: "LogoutAll: the revoke", inject: func(a *authRaceApp, err error) { a.store.revokeAllErr = err }, path: "/auth/logout-all", hdr: true},
	}

	for _, st := range sites {
		for _, k := range kinds {
			t.Run(st.name+" fails with "+k.name, func(t *testing.T) {
				a := newAuthRaceApp(t, nil)
				st.inject(a, k.err)
				var headers map[string]string
				if st.hdr {
					headers = map[string]string{"X-Test-Acting-User": a.store.user.ID.String()}
				}

				resp, _ := a.postTimed(t, st.path, "{}", st.cookie, headers, 5*time.Second)
				body := authRequireStatus(t, resp, k.want)
				if _, issued := body["access_token"]; issued {
					t.Errorf("a failed request issued an access token: %v", body)
				}
				cookies := refreshCookies(resp)
				switch st.path {
				case "/auth/logout":
					// Logout clears its cookie on every answer but the 403 — a lookup or a revoke that
					// failed included (see Logout for why): this site is never a 403.
					if len(cookies) != 1 || !cookieDeleted(cookies[0]) {
						t.Errorf("Set-Cookie = %+v, want the cookie deleted as on every sign-out answer", cookies)
					}
				default:
					if len(cookies) != 0 {
						t.Errorf("Set-Cookie = %+v, want the cookie left alone", cookies)
					}
				}
				if st.refresh {
					if keys := a.redis.Keys(); len(keys) != 0 {
						t.Errorf("a failed refresh wrote Redis rows %v", keys)
					}
					if n := len(a.store.named("RevokeSession")); n != 0 {
						t.Errorf("a failed refresh revoked the session (%d times): a failed call says nothing about the session", n)
					}
				}
				if a.store.snapshot().IsRevoked {
					t.Error("the session is revoked although the call that would have revoked it failed")
				}
			})
		}
	}
}

// TestCommitOutcomeUnknown pins when a failed COMMIT may nevertheless have landed, which
// decides what a user whose password change did not return cleanly is told ("it was not done"
// is safe to retry; "it may have been done" is not). The server answering (a serialization
// failure, a deadlock, a unique violation, a commit that came back as a rollback) means it did
// not commit, and so does a COMMIT that never went out (SafeToRetry for a context that had
// ended). A deadline cut off while the reply was awaited is NOT such a case: that one may have
// landed. Everything else leaves it open.
func TestCommitOutcomeUnknown(t *testing.T) {
	unknown := map[string]error{
		"a connection reset":                errRaceTransient,
		"an EOF after the COMMIT went out":  fmt.Errorf("failed to receive message: %w", io.ErrUnexpectedEOF),
		"a deadline while the reply waited": fmt.Errorf("timeout: %w", context.DeadlineExceeded),
		"a cancelled context":               context.Canceled,
		"a connect error":                   &pgconn.ConnectError{Config: &pgconn.Config{}},
		"an error nobody classified":        errRaceBug,
		"a closed pool":                     fmt.Errorf("acquiring: %w", puddle.ErrClosedPool),
		"a cancel while the reply waited":   fmt.Errorf("timeout: %w", context.Canceled),
	}
	known := map[string]error{
		"nothing at all":                      nil,
		"a serialization failure":             pgState("40001"),
		"a deadlock":                          pgState("40P01"),
		"a unique violation":                  pgState("23505"),
		"a commit that came back as ROLLBACK": fmt.Errorf("commit: %w", pgx.ErrTxCommitRollback),
		"a transaction already closed":        pgx.ErrTxClosed,
	}
	for name, err := range unknown {
		if !commitOutcomeUnknown(err) {
			t.Errorf("%s: the outcome is reported as known, so the user would be told the change was not made", name)
		}
	}
	for name, err := range known {
		if commitOutcomeUnknown(err) {
			t.Errorf("%s: the outcome is reported as unknown, although the server answered", name)
		}
	}

	// The COMMIT never went out: its context had ended before it was sent, or its
	// connection was already closed. Nothing reached the server, which rolls the
	// transaction back, so there is nothing to wonder about — and the user is not to
	// be told the new password "may be in effect".
	neverSent := map[string]error{
		"a COMMIT whose context had already ended":  &neverSentError{err: context.DeadlineExceeded},
		"the same, cancelled rather than timed out": &neverSentError{err: context.Canceled},
		"the same, wrapped by the caller":           fmt.Errorf("commit: %w", &neverSentError{err: context.DeadlineExceeded}),
	}
	for name, err := range neverSent {
		if commitOutcomeUnknown(err) {
			t.Errorf("%s: the outcome is reported as unknown, although the COMMIT was never sent and the server rolled the transaction back", name)
		}
	}

	// SafeToRetry without a context error proves nothing about a COMMIT. pgx reports a
	// connection that drops while the reply is awaited as "conn closed", which says
	// SafeToRetry, and the server may have executed the COMMIT: against the real driver
	// the row is committed (TestALostCommitReplyLooksSafeToRetryAndHasLanded, in
	// internal/db). Classified as never sent, the change would be answered "NOT
	// changed" and never settled while the new password is valid.
	safeToRetryButSent := map[string]error{
		"a connection that closed while the reply was awaited (conn closed)":    connClosedError{},
		"the same, wrapped by the caller":                                       fmt.Errorf("commit: %w", connClosedError{}),
		"a SafeToRetry error whose cause is a closed connection, not a context": &neverSentError{err: pgconn.ErrConnClosed},
	}
	for name, err := range safeToRetryButSent {
		if !commitOutcomeUnknown(err) {
			t.Errorf("%s: the outcome is reported as known, although SafeToRetry here says nothing about whether the COMMIT was executed", name)
		}
	}
}

// TestAFailedCallIsLoggedAtTheLevelOfWhatItIs pins the other half of the 503/500 distinction:
// the database being away is a Warn and a defect an Error, for the lookup of Refresh and the
// lookup and revoke of Logout and LogoutAll. An operator tells "the database blipped" from "the
// code is broken" by the level, not by reading the status; a defect logged as a Warn is the one
// nobody pages on. Refresh's line also says which answer it is giving and names the session.
func TestAFailedCallIsLoggedAtTheLevelOfWhatItIs(t *testing.T) {
	type site struct {
		name   string
		inject func(*raceStore, error)
		path   string
		cookie string
		asUser bool
		// away and defect are the log lines for the two kinds of failure.
		away, defect string
		namesSession bool
	}
	sites := []site{
		{
			name: "Refresh's user lookup", inject: func(s *raceStore, err error) { s.userErr = err },
			path: "/auth/refresh", cookie: raceCurrentToken,
			away: "refresh: user lookup failed; answering 503", defect: "refresh: user lookup failed; answering 500", namesSession: true,
		},
		{
			name: "Logout's session lookup", inject: func(s *raceStore, err error) { s.currentErr = err },
			path: "/auth/logout", cookie: raceCurrentToken,
			away: "logout: session lookup failed, nothing revoked", defect: "logout: session lookup failed, nothing revoked",
		},
		{
			name: "Logout's revoke", inject: func(s *raceStore, err error) { s.revokeErr = err },
			path: "/auth/logout", cookie: raceCurrentToken,
			away: "logout: revoking the session did not complete", defect: "logout: revoking the session failed",
		},
		{
			name: "LogoutAll's revoke", inject: func(s *raceStore, err error) { s.revokeAllErr = err },
			path: "/auth/logout-all", asUser: true,
			away: "logout-all: revoking the sessions did not complete", defect: "logout-all: revoking the sessions failed",
		},
	}

	for _, st := range sites {
		for _, kind := range []struct {
			name  string
			err   error
			level string
			line  func(site) string
		}{
			{"the database is away", errRaceTransient, `"level":"WARN"`, func(s site) string { return s.away }},
			{"a defect", errRaceBug, `"level":"ERROR"`, func(s site) string { return s.defect }},
		} {
			t.Run(st.name+": "+kind.name, func(t *testing.T) {
				logs := captureProductionLog(t)
				a := newAuthRaceApp(t, func(s *raceStore) { st.inject(s, kind.err) })

				if st.asUser {
					a.postAs(t, st.path, "{}", 5*time.Second)
				} else {
					a.postBody(t, st.path, "{}", st.cookie, nil)
				}

				want := kind.line(st)
				var found string
				for _, line := range strings.Split(logs.String(), "\n") {
					if strings.Contains(line, want) {
						found = line
					}
				}
				if found == "" {
					t.Fatalf("no %q line in the log: %q", want, logs.String())
				}
				if !strings.Contains(found, kind.level) {
					t.Errorf("the line is not logged at %s: %s", kind.level, found)
				}
				if st.namesSession && !strings.Contains(found, a.store.session.ID.String()) {
					t.Errorf("the line does not name the session: %s", found)
				}
			})
		}
	}
}
