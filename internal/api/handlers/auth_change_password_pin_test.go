package handlers

import (
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestChangePassword_TheTransactionIsPinnedToReadCommitted is
// TestRegister_TheTransactionIsPinnedToReadCommitted for the password change. The
// transaction ends every session of the user (auth.RevokeAllUserSessionsIn: the epoch
// bump, the listing, the revoke), and that is only complete under READ COMMITTED:
// each statement takes its own snapshot, so the listing and the revoke see a sign-in
// that committed while the transaction waited for the row lock. Under REPEATABLE READ
// the snapshot is taken by the first statement and the late session stays live
// (internal/db's TestRevokeAll_UnderRepeatableReadALateInsertSurvives shows it against
// Postgres). The server's default is READ COMMITTED, but a role, a database or a
// connection setting can change that default, so the handler asks for it by name. The
// harness pool records the options of every transaction it is asked to begin.
func TestChangePassword_TheTransactionIsPinnedToReadCommitted(t *testing.T) {
	a := newAuthRaceApp(t, withPasswordHash(t, nil))

	resp, _ := a.postAs(t, "/auth/change-password", changeBody, 120*time.Second)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	opts := a.pool.beginOptions()
	if len(opts) != 1 {
		t.Fatalf("%d transactions were begun with options, want exactly one", len(opts))
	}
	if opts[0].IsoLevel != pgx.ReadCommitted {
		t.Errorf("the password change transaction asked for isolation %q, want %q by name, not the server's default", opts[0].IsoLevel, pgx.ReadCommitted)
	}
}
