package ws

import (
	"context"
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
// a Proxmox ticket minted. A credential that is missing, or names both a session
// and a key or neither, is refused as any failure is; then stillLive. It returns
// the check to repeat, and the logger to log the credential's events with.
func admitCredential(conn *fiberWs.Conn, queries *db.Queries, logger *slog.Logger) (liveCheck, *slog.Logger, bool) {
	cred, ok := conn.Locals(consoleCredentialLocal).(consoleCredential)
	if !ok || cred.UserID == uuid.Nil || !cred.Valid() {
		writeRefusal(conn, "console credential missing")
		return nil, nil, false
	}
	logger = logger.With("user_id", cred.UserID)
	if cred.SessionID != uuid.Nil {
		logger = logger.With("session_id", cred.SessionID)
	} else {
		logger = logger.With("api_key_id", cred.APIKeyID)
	}
	live := credentialLive(queries, cred)
	return live, logger, stillLive(conn, live, logger)
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

// watchCredential asks live at every tick until ctx is done, and calls end — once —
// when the credential has ended, or could not be checked maxFailedChecks times in a
// row. It reports whether it called end. A panic in a check ends the console too.
func watchCredential(ctx context.Context, ticks <-chan time.Time, live liveCheck, end func(code int, reason string), logger *slog.Logger) (ended bool) {
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
		cctx, cancel := context.WithTimeout(ctx, credentialCheckTimeout)
		ok, err := live(cctx)
		cancel()
		switch {
		case err != nil && ctx.Err() != nil:
			return false // the relay ended while it asked
		case err != nil:
			failed++
			logger.Warn("console: could not check its session", "error", err, "failed_in_a_row", failed)
			if failed >= maxFailedChecks {
				finish(gorillaWs.CloseInternalServerErr, checkFailedReason)
				return true
			}
		case !ok:
			finish(closeSessionEnded, sessionEndedReason)
			return true
		default:
			failed = 0
		}
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

// watchRelay starts watching, asking live every `every`. stop must be called once
// both legs have returned.
func watchRelay(live liveCheck, every time.Duration, browser *fiberWs.Conn, px *gorillaWs.Conn, logger *slog.Logger) *relayWatch {
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
			logger.Info("console: closed it, as its session has ended or could not be checked", "reason", reason)
		}
		if !watchCredential(ctx, ticker.C, live, end, logger) {
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
