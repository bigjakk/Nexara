package ws

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	fiberWs "github.com/gofiber/contrib/v3/websocket"
	"github.com/google/uuid"
	gorillaWs "github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// A console is held to the sign-in it was opened under — the session, or the API
// key, that minted its console token — for as long as it is open. Every way a
// sign-in ends is a row in the database: a sign-out or a revoke from the session
// list, every revoke-all, a deactivation (by an admin or LDAP sync), an expiry, a
// revoked key. So each console asks the database whether its credential is still
// live (IsConsoleCredentialLive) when it opens and every credentialCheckEvery
// after, and is closed when the answer is no. Asking, rather than being told,
// needs no publish in each of the paths that end a sign-in, no event for an
// expiry, and no message that a replica can miss.
//
// The /ws hub socket is held to its sign-in the same way (Server.handleWS).
// On the same tick a console also asks RBAC whether its user still holds the
// console permission it was opened with (consoleAllowed). RBAC's cache can lag a
// revoke by its TTL; that is accepted.

// consoleCredentialLocal is the Locals key under which the console gate hands
// HandleConsole and HandleVNC the credential the console token was minted under.
const consoleCredentialLocal = "consoleCredential"

const (
	// credentialCheckEvery is how often an open console asks again.
	credentialCheckEvery = 5 * time.Second
	// credentialCheckTimeout bounds one question.
	credentialCheckTimeout = 2 * time.Second
	// maxFailedChecks ends a console whose credential could not be checked this
	// many times in a row (about a minute): a broken query, or a replica cut off
	// from the database, must not leave every console open for good.
	maxFailedChecks = 12
)

const (
	// closeSessionEnded is the Close code a console gets when its credential has
	// ended: 3000, registered with IANA as "Unauthorized".
	closeSessionEnded  = 3000
	sessionEndedReason = "session ended"
	// checkFailedReason ends a console whose credential could not be checked. Its
	// code is 1011, as for any other failure, so the browser may try again.
	checkFailedReason = "could not check the session"
	// closeAccessRemoved is the Close code a console gets when its user no longer
	// holds the permission to it: 3003, registered with IANA as "Forbidden". Unlike
	// closeSessionEnded it says nothing of the sign-in, which stays.
	closeAccessRemoved  = 3003
	accessRemovedReason = "console access removed"
)

// consoleCredential is who a console is held to: the user its token names, and
// the session or API key the token was minted under.
type consoleCredential struct {
	UserID uuid.UUID
	auth.ConsoleCredential
}

// liveCheck asks whether a console's credential is still live: yes, no, or an
// error when it could not look.
type liveCheck func(ctx context.Context) (bool, error)

// credentialLive asks the database.
func credentialLive(q *db.Queries, cred consoleCredential) liveCheck {
	return func(ctx context.Context) (bool, error) {
		return q.IsConsoleCredentialLive(ctx, db.IsConsoleCredentialLiveParams{
			UserID:    cred.UserID,
			SessionID: nullableUUID(cred.SessionID),
			ApiKeyID:  nullableUUID(cred.APIKeyID),
		})
	}
}

func nullableUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != uuid.Nil}
}

// admitCredential is a console's check at open, before anything is looked up or
// a Proxmox ticket minted, and the hub socket's before anything is sent on it. A
// credential that is missing, or names both a session and a key or neither (a hub
// token minted before hub tokens named one), is refused as any failure is; then
// stillLive. It returns the user, the check to
// repeat, and the logger to log the credential's events with.
func admitCredential(conn *fiberWs.Conn, queries *db.Queries, logger *slog.Logger) (uuid.UUID, liveCheck, *slog.Logger, bool) {
	cred, ok := conn.Locals(consoleCredentialLocal).(consoleCredential)
	if !ok || cred.UserID == uuid.Nil || !cred.Valid() {
		writeRefusal(conn, "console credential missing")
		return uuid.Nil, nil, nil, false
	}
	logger = logger.With("user_id", cred.UserID)
	if cred.SessionID != uuid.Nil {
		logger = logger.With("session_id", cred.SessionID)
	} else {
		logger = logger.With("api_key_id", cred.APIKeyID)
	}
	live := credentialLive(queries, cred)
	return cred.UserID, live, logger, stillLive(conn, live, logger)
}

// stillLive asks live once, and refuses the console when its credential could not
// be checked, as any failure is, or has ended: told so (writeSessionEnded), which
// the browser takes as final. Asked at open and again just before the console is
// announced: Proxmox can take seconds to set it up, and the next check is
// credentialCheckEvery away.
func stillLive(conn *fiberWs.Conn, live liveCheck, logger *slog.Logger) bool {
	ctx, cancel := context.WithTimeout(context.Background(), credentialCheckTimeout)
	isLive, err := live(ctx)
	cancel()
	switch {
	case err != nil:
		logger.Warn("console: could not check its session; refusing it", "error", err)
		writeRefusal(conn, checkFailedReason)
		return false
	case !isLive:
		logger.Info("console: refusing it, as its session has ended")
		writeSessionEnded(conn)
		return false
	}
	return true
}

// writeRefusal is the handlers' writeError, for refusals made here.
func writeRefusal(conn *fiberWs.Conn, msg string) {
	_ = conn.WriteMessage(fiberWs.TextMessage, []byte(`{"type":"error","message":`+strconv.Quote(msg)+`}`))
	_ = conn.WriteMessage(fiberWs.CloseMessage,
		fiberWs.FormatCloseMessage(fiberWs.CloseInternalServerErr, msg))
}

// writeSessionEnded tells a console opened for a credential that has ended why it
// is refused, with a code the browser stops on.
func writeSessionEnded(conn *fiberWs.Conn) {
	_ = conn.WriteMessage(fiberWs.TextMessage,
		[]byte(`{"type":"error","code":"session_ended","message":`+strconv.Quote(sessionEndedReason)+`}`))
	_ = conn.WriteMessage(fiberWs.CloseMessage,
		fiberWs.FormatCloseMessage(closeSessionEnded, sessionEndedReason))
}

// watchCredential asks live, then allowed (when it is not nil), at every tick until
// ctx is done, and calls end — once — when the credential has ended, when allowed
// says no, or when a check could not be made maxFailedChecks times in a row: a
// failing permission lookup counts as a failing liveness check, never as a removed
// permission. It reports whether it called end. A panic in a check ends it too.
func watchCredential(ctx context.Context, ticks <-chan time.Time, live, allowed liveCheck, end func(code int, reason string), logger *slog.Logger) (ended bool) {
	finish := func(code int, reason string) {
		ended = true // first: an end that panics is not called again
		end(code, reason)
	}
	defer func() {
		if r := recover(); r != nil {
			logger.Error("console: checking its session panicked; ending it", "panic", r)
			if !ended {
				defer func() { _ = recover() }()
				finish(gorillaWs.CloseInternalServerErr, checkFailedReason)
			}
		}
	}()
	failed := 0
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticks:
		}
		ok, err := askBounded(ctx, live)
		code, reason := closeSessionEnded, sessionEndedReason
		if err == nil && ok && allowed != nil {
			ok, err = askBounded(ctx, allowed)
			code, reason = closeAccessRemoved, accessRemovedReason
		}
		switch {
		case err != nil && ctx.Err() != nil:
			return false // the socket ended while it asked
		case err != nil:
			failed++
			logger.Warn("ws: could not check a socket's session or permission", "error", err, "failed_in_a_row", failed)
			if failed >= maxFailedChecks {
				finish(gorillaWs.CloseInternalServerErr, checkFailedReason)
				return true
			}
		case !ok:
			finish(code, reason)
			return true
		default:
			failed = 0
		}
	}
}

// askBounded asks check once, bounded by credentialCheckTimeout.
func askBounded(ctx context.Context, check liveCheck) (bool, error) {
	cctx, cancel := context.WithTimeout(ctx, credentialCheckTimeout)
	defer cancel()
	return check(cctx)
}

// consoleResource is the RBAC resource whose console permission a console of
// scopeType needs: the mapping AuthHandler.ConsoleToken mints it under.
func consoleResource(scopeType string) string {
	switch scopeType {
	case "node_shell":
		return "node"
	case "vm_serial", "vm_vnc":
		return "vm"
	case "ct_attach", "ct_vnc":
		return "container"
	}
	return ""
}

// consoleAllowed asks RBAC whether userID still holds console:<resource> on the
// scope's cluster: what the console token was minted for. With no checker, or a
// scope it cannot name, it answers an error, never a yes.
func consoleAllowed(check PermissionChecker, userID uuid.UUID, scope *auth.ConsoleScope) liveCheck {
	return func(ctx context.Context) (bool, error) {
		clusterID, err := uuid.Parse(scope.ClusterID)
		resource := consoleResource(scope.Type)
		switch {
		case check == nil:
			return false, errors.New("no permission checker configured")
		case err != nil || resource == "":
			return false, fmt.Errorf("console scope names no permission: %q on %q", scope.Type, scope.ClusterID)
		}
		return check(ctx, userID, "console", resource, "cluster", clusterID)
	}
}

// relayWatch holds an open relay to its credential. The watcher never writes a
// data frame — the Proxmox-to-browser leg is the browser's one data writer — so it
// ends a console by closing the Proxmox leg, which stops the browser's input at
// once, after recording why; that leg's reader then fails, and the leg closes the
// browser with the reason recorded (browserClose).
type relayWatch struct {
	mu     sync.Mutex
	code   int
	reason string
	cancel context.CancelFunc
	done   chan struct{}
}

// watchRelay starts watching, asking live and allowed every `every`. stop must be
// called once both legs have returned.
func watchRelay(live, allowed liveCheck, every time.Duration, browser *fiberWs.Conn, px *gorillaWs.Conn, logger *slog.Logger) *relayWatch {
	if every <= 0 {
		every = credentialCheckEvery
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &relayWatch{code: gorillaWs.CloseNormalClosure, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(w.done)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		end := func(code int, reason string) {
			w.mu.Lock()
			w.code, w.reason = code, reason
			w.mu.Unlock()
			// The Close waits for the leg writing to Proxmox; if that write is stuck,
			// the socket is closed instead, so the browser's input stops at once.
			if !closeRelayLeg(px, gorillaWs.CloseNormalClosure, "") {
				_ = px.Close()
			}
			logger.Info("console: closed it, as its session or access has ended or could not be checked", "reason", reason)
		}
		if !watchCredential(ctx, ticker.C, live, allowed, end, logger) {
			return
		}
		// A leg stuck writing to a browser that has stopped reading holds the
		// relay open past the Close; after a generous wait the sockets are closed
		// under it.
		select {
		case <-ctx.Done():
		case <-time.After(2 * closeDrainTimeout):
			_ = px.Close()
			_ = browser.NetConn().Close()
		}
	}()
	return w
}

// browserClose is the Close the Proxmox-to-browser leg sends the browser: the
// reason the watcher ended the console, or a normal closure.
func (w *relayWatch) browserClose() (code int, reason string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.code, w.reason
}

// stop ends the watcher and waits for it.
func (w *relayWatch) stop() {
	w.cancel()
	<-w.done
}
