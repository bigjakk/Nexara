package db

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// What pgx reports for a COMMIT that the server EXECUTED and whose reply never
// reached the client. internal/api/handlers/db_errors.go (commitOutcomeUnknown)
// tells such a COMMIT, whose outcome is unknown, from one that was never sent and
// so was rolled back, and it has to read a fact about the driver that nothing else
// here would notice changing: the driver's error for a lost reply says
// pgconn.SafeToRetry — it is a "conn closed" error, a *pgconn.connLockError — and
// carries no context error, although the transaction committed. SafeToRetry alone
// is therefore not proof that nothing was sent; only SafeToRetry together with the
// context's own error is (TestAnUnsentCommitIsSafeToRetryAndTheServerRollsBack pins
// that half). A classifier that trusted SafeToRetry alone would answer "your
// password was NOT changed" for a change that was made.
//
// The lost reply is made with a TCP proxy in front of the database that forwards
// the client's COMMIT, lets the server execute it, and closes both sockets instead
// of forwarding the reply. It needs a database reachable over TCP without TLS; one
// that is not skips these tests.

// lostReplyProxy is the proxy. Armed, it drops the reply to the next "commit"
// query it forwards, once; everything else passes through untouched.
type lostReplyProxy struct {
	ln      net.Listener
	target  string
	armed   atomic.Bool
	dropped atomic.Int32
}

func startLostReplyProxy(t *testing.T, target string) *lostReplyProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for the proxy: %v", err)
	}
	p := &lostReplyProxy{ln: ln, target: target}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go p.handle(c)
		}
	}()
	return p
}

// isCommitQuery reports whether b is a simple-protocol Query message whose text is
// "commit": 'Q', a four byte length, the text, a NUL. pgx sends a transaction's
// COMMIT as exactly that.
func isCommitQuery(b []byte) bool {
	if len(b) < 6 || b[0] != 'Q' {
		return false
	}
	return bytes.EqualFold(bytes.TrimRight(b[5:], "\x00"), []byte("commit"))
}

func (p *lostReplyProxy) handle(client net.Conn) {
	server, err := net.Dial("tcp", p.target)
	if err != nil {
		_ = client.Close()
		return
	}
	var commitSent atomic.Bool
	go func() { // the server's side: forward everything until the COMMIT's reply
		buf := make([]byte, 32*1024)
		for {
			n, err := server.Read(buf)
			if n > 0 {
				if commitSent.Load() {
					// The server has executed the COMMIT and is answering: drop the answer.
					p.armed.Store(false)
					p.dropped.Add(1)
					_ = client.Close()
					_ = server.Close()
					return
				}
				if _, werr := client.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				_ = client.Close()
				return
			}
		}
	}()
	buf := make([]byte, 32*1024)
	for { // the client's side
		n, err := client.Read(buf)
		if n > 0 {
			if p.armed.Load() && isCommitQuery(buf[:n]) {
				commitSent.Store(true)
			}
			if _, werr := server.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			_ = server.Close()
			return
		}
	}
}

// lostReplyCommit runs the change-password transaction's shape — the conditional
// update, then COMMIT — through the proxy, on a pool or on a single connection, with
// the COMMIT's reply dropped. It returns how many replies the proxy dropped and the
// error Commit gave.
func lostReplyCommit(t *testing.T, env *migrationTestEnv, viaPool bool) (dropped int32, commitErr error) {
	t.Helper()

	cfg := env.Pool.Config()
	host := cfg.ConnConfig.Host
	if strings.HasPrefix(host, "/") {
		t.Skip("the test database is reached over a unix socket; the proxy needs TCP")
	}
	proxy := startLostReplyProxy(t, net.JoinHostPort(host, strconv.Itoa(int(cfg.ConnConfig.Port))))
	_, portStr, err := net.SplitHostPort(proxy.ln.Addr().String())
	if err != nil {
		t.Fatalf("the proxy's address: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("the proxy's port: %v", err)
	}
	// The same credentials and database as the pool, through the proxy, in the clear:
	// the proxy has to read the protocol.
	cfg.ConnConfig.Host, cfg.ConnConfig.Port = "127.0.0.1", uint16(port)
	cfg.ConnConfig.TLSConfig, cfg.ConnConfig.Fallbacks = nil, nil

	var tx pgx.Tx
	if viaPool {
		pool, err := pgxpool.NewWithConfig(env.Ctx, cfg)
		if err != nil {
			t.Fatalf("open a pool through the proxy: %v", err)
		}
		defer pool.Close()
		tx, err = pool.Begin(env.Ctx)
		if err != nil {
			skipIfTLSRequired(t, err)
			t.Fatalf("begin through the proxy: %v", err)
		}
	} else {
		conn, err := pgx.ConnectConfig(env.Ctx, cfg.ConnConfig)
		if err != nil {
			skipIfTLSRequired(t, err)
			t.Fatalf("connect through the proxy: %v", err)
		}
		defer func() { _ = conn.Close(context.Background()) }()
		tx, err = conn.Begin(env.Ctx)
		if err != nil {
			t.Fatalf("begin through the proxy: %v", err)
		}
	}

	// Whatever happens from here, the transaction must not be left open: a pool does
	// not close while a connection is out, so a failed assertion below would hang the
	// test binary instead of failing the test. Once Commit has run this is a no-op.
	defer func() {
		rbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = tx.Rollback(rbCtx)
	}()

	rows, err := gen.New(tx).UpdatePassword(env.Ctx, gen.UpdatePasswordParams{
		ID: passwordCASUser, PasswordHash: passwordCASNew, ExpectedHash: passwordCASOld,
	})
	if err != nil || rows != 1 {
		t.Fatalf("the update inside the transaction: rows=%d err=%v, want 1 row", rows, err)
	}

	proxy.armed.Store(true)
	commitErr = tx.Commit(env.Ctx)
	return proxy.dropped.Load(), commitErr
}

// skipIfTLSRequired skips the test when the server refused the proxy's plaintext
// connection because it insists on TLS, which the proxy cannot read through.
func skipIfTLSRequired(t *testing.T, err error) {
	t.Helper()
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "ssl") || strings.Contains(msg, "tls") || strings.Contains(msg, "encrypt") {
		t.Skipf("the test database refuses a connection without TLS, which the proxy needs: %v", err)
	}
}

// TestALostCommitReplyLooksSafeToRetryAndHasLanded: the server executes the COMMIT
// and the client never hears. The error pgx gives says pgconn.SafeToRetry, carries
// neither context.Canceled nor context.DeadlineExceeded, and the change IS in the
// table. Both the pool the handlers use and a bare connection are checked.
func TestALostCommitReplyLooksSafeToRetryAndHasLanded(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	_, purge := newPasswordCAS(t, env)
	defer purge()

	for _, tt := range []struct {
		name    string
		viaPool bool
	}{
		{"through a pool, as the handlers use it", true},
		{"on a single connection", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resetPasswordHash(t, env)

			dropped, err := lostReplyCommit(t, env, tt.viaPool)

			if dropped != 1 {
				t.Fatalf("the proxy dropped %d replies, want 1: the lost reply was not made, so this test proved nothing", dropped)
			}
			if err == nil {
				t.Fatal("a COMMIT whose reply was dropped reported success")
			}
			t.Logf("Commit error: %T %q", err, err)
			if !pgconn.SafeToRetry(err) {
				t.Errorf("pgconn.SafeToRetry(%v) = false: the premise commitOutcomeUnknown relies on has changed — a lost reply is no longer reported that way, so the rule that SafeToRetry needs a context error beside it may need another look", err)
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("the error %v carries a context error: a lost reply now looks like a cut-off deadline, and the classifier's two halves no longer separate it from a COMMIT that was never sent", err)
			}
			if got := storedPasswordHash(t, env); got != passwordCASNew {
				t.Errorf("the account holds %q, want %q: the server did not execute the COMMIT, so this test did not make a COMMIT that landed", got, passwordCASNew)
			}
		})
	}
}
