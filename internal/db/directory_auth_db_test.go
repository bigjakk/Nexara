package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// directoryTx runs fn in a transaction that is always rolled back, after migrating up:
// these tests read the queries' answers over rows of their own and leave nothing behind.
func directoryTx(t *testing.T, fn func(ctx context.Context, tx pgx.Tx, q *gen.Queries)) {
	t.Helper()
	env := setupMigration(t)
	defer env.Cleanup()
	migrateUp(t, env.Migrate)
	tx, err := env.Pool.Begin(env.Ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	fn(env.Ctx, tx, gen.New(tx))
}

// TestEnabledDirectoryConfig_TheOldestIsUsed: with two enabled LDAP (or OIDC) configs,
// sign-in uses the oldest, whatever order the rows sit in. The newer row is inserted
// first, so a LIMIT 1 without an ORDER BY returns it from a sequential scan.
func TestEnabledDirectoryConfig_TheOldestIsUsed(t *testing.T) {
	directoryTx(t, func(ctx context.Context, tx pgx.Tx, q *gen.Queries) {
		older := time.Now().Add(-time.Hour)
		for _, stmt := range []string{
			`UPDATE ldap_configs SET enabled = false`,
			`UPDATE oidc_configs SET enabled = false`,
			`INSERT INTO ldap_configs (name, enabled, server_url, search_base_dn, created_at) VALUES ('newer', true, 'ldap://192.0.2.10', 'dc=example,dc=com', $1::timestamptz + interval '1 minute')`,
			`INSERT INTO ldap_configs (name, enabled, server_url, search_base_dn, created_at) VALUES ('older', true, 'ldap://192.0.2.11', 'dc=example,dc=com', $1)`,
			`INSERT INTO oidc_configs (name, enabled, issuer_url, client_id, created_at) VALUES ('newer', true, 'https://sso.example.com', 'a', $1::timestamptz + interval '1 minute')`,
			`INSERT INTO oidc_configs (name, enabled, issuer_url, client_id, created_at) VALUES ('older', true, 'https://sso.example.com', 'b', $1)`,
		} {
			args := []any{older}
			if stmt[0] == 'U' {
				args = nil
			}
			if _, err := tx.Exec(ctx, stmt, args...); err != nil {
				t.Fatalf("%s: %v", stmt, err)
			}
		}
		ldap, err := q.GetEnabledLDAPConfig(ctx)
		if err != nil || ldap.Name != "older" {
			t.Errorf("GetEnabledLDAPConfig = %q, %v; want the oldest, older", ldap.Name, err)
		}
		oidc, err := q.GetEnabledOIDCConfig(ctx)
		if err != nil || oidc.Name != "older" {
			t.Errorf("GetEnabledOIDCConfig = %q, %v; want the oldest, older", oidc.Name, err)
		}
	})
}

// TestLDAPSyncState_SQL: the sync's statements against Postgres. A sync deactivates only
// an active LDAP account and re-enables only one a sync deactivated; an administrator's
// change of is_active makes the deactivation theirs; the lease admits one sync at a time
// and lets the next one in once released or run out.
func TestLDAPSyncState_SQL(t *testing.T) {
	directoryTx(t, func(ctx context.Context, tx pgx.Tx, q *gen.Queries) {
		var userID, cfgID uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO users (email, password_hash, display_name, auth_source) VALUES ('carol@example.com', '', 'Carol', 'ldap') RETURNING id`).Scan(&userID); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(ctx, `INSERT INTO ldap_configs (server_url, search_base_dn) VALUES ('ldap://192.0.2.10', 'dc=example,dc=com') RETURNING id`).Scan(&cfgID); err != nil {
			t.Fatal(err)
		}
		state := func() (active, byDirectory bool) {
			if err := tx.QueryRow(ctx, `SELECT is_active, deactivated_by_directory FROM users WHERE id = $1`, userID).Scan(&active, &byDirectory); err != nil {
				t.Fatal(err)
			}
			return active, byDirectory
		}
		must := func(n int64, err error, want int64, what string) {
			t.Helper()
			if err != nil || n != want {
				t.Errorf("%s: %d rows, %v; want %d", what, n, err, want)
			}
		}
		n, err := q.ReactivateLDAPUserFromDirectory(ctx, userID)
		must(n, err, 0, "reactivating an active account")
		n, err = q.DeactivateLDAPUserForDirectory(ctx, userID)
		must(n, err, 1, "a sync's deactivation")
		n, err = q.DeactivateLDAPUserForDirectory(ctx, userID)
		must(n, err, 0, "deactivating an inactive account")
		if a, d := state(); a || !d {
			t.Errorf("after a sync's deactivation: active %t, by directory %t", a, d)
		}
		if _, err := q.UpdateUserProfile(ctx, gen.UpdateUserProfileParams{ID: userID, IsActive: pgtype.Bool{Bool: false, Valid: true}}); err != nil {
			t.Fatal(err)
		}
		n, err = q.ReactivateLDAPUserFromDirectory(ctx, userID)
		must(n, err, 0, "reactivating an account an administrator deactivated")
		if _, err := q.UpdateUserProfile(ctx, gen.UpdateUserProfileParams{ID: userID, IsActive: pgtype.Bool{Bool: true, Valid: true}}); err != nil {
			t.Fatal(err)
		}
		n, err = q.DeactivateLDAPUserForDirectory(ctx, userID)
		must(n, err, 1, "a sync's deactivation again")
		n, err = q.ReactivateLDAPUserFromDirectory(ctx, userID)
		must(n, err, 1, "a sync's re-enable")
		if a, d := state(); !a || d {
			t.Errorf("after a sync's re-enable: active %t, by directory %t", a, d)
		}

		first, second := uuid.New(), uuid.New()
		claim := func(token uuid.UUID, secs float64) error {
			_, err := q.ClaimLDAPSyncLease(ctx, gen.ClaimLDAPSyncLeaseParams{ID: cfgID, Token: token, LeaseSeconds: secs})
			return err
		}
		if err := claim(first, 3600); err != nil {
			t.Fatalf("the first claim: %v", err)
		}
		for token, want := range map[uuid.UUID]int64{first: 1, second: 0} {
			n, err := q.RenewLDAPSyncLease(ctx, gen.RenewLDAPSyncLeaseParams{ID: cfgID, Token: token, LeaseSeconds: 3600})
			must(n, err, want, "renewing the lease")
		}
		if err := claim(second, 3600); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("a second claim while the first holds the lease: %v, want no row", err)
		}
		if err := q.ReleaseLDAPSyncLease(ctx, gen.ReleaseLDAPSyncLeaseParams{ID: cfgID, Token: second}); err != nil {
			t.Fatal(err)
		}
		if err := claim(second, 3600); !errors.Is(err, pgx.ErrNoRows) {
			t.Error("a release by a sync that does not hold the lease freed it")
		}
		if err := q.ReleaseLDAPSyncLease(ctx, gen.ReleaseLDAPSyncLeaseParams{ID: cfgID, Token: first}); err != nil {
			t.Fatal(err)
		}
		if err := claim(second, 0); err != nil {
			t.Errorf("a claim after the release: %v", err)
		}
		if err := claim(first, 3600); err != nil {
			t.Errorf("a claim after a lease ran out: %v", err)
		}
	})
}

// TestUserByEmail_IgnoresCaseWhenUnique: an address names the account spelled exactly so,
// else the one account differing only by case; a pair that differs only by case is kept,
// each answering to its own spelling and neither to a third. The lookup has its index.
func TestUserByEmail_IgnoresCaseWhenUnique(t *testing.T) {
	directoryTx(t, func(ctx context.Context, tx pgx.Tx, q *gen.Queries) {
		for _, e := range []string{"Carol@example.com", "dave@example.com", "DAVE@example.com", "irin@example.com"} {
			if _, err := tx.Exec(ctx, `INSERT INTO users (email, password_hash, display_name) VALUES ($1, '', 'x')`, e); err != nil {
				t.Fatal(err)
			}
		}
		for typed, want := range map[string]string{
			"carol@EXAMPLE.com": "Carol@example.com",
			"DAVE@example.com":  "DAVE@example.com",
			"dave@example.com":  "dave@example.com",
			"Dave@example.com":  "",
			// lower() makes U+0130 "i", the lockout's fold does not: a non-ASCII spelling
			// matches exactly or not at all, or it would have a sign-in budget of its own.
			"\u0130rin@example.com": "",
		} {
			u, err := q.GetUserByEmail(ctx, typed)
			if want == "" && !errors.Is(err, pgx.ErrNoRows) || want != "" && (err != nil || u.Email != want) {
				t.Errorf("GetUserByEmail(%q) = %q, %v; want %q", typed, u.Email, err, want)
			}
		}
		if u, err := q.GetUserByEmailAndSource(ctx, gen.GetUserByEmailAndSourceParams{Email: "CAROL@example.com", AuthSource: "local"}); err != nil || u.Email != "Carol@example.com" {
			t.Errorf("GetUserByEmailAndSource(CAROL, local) = %q, %v", u.Email, err)
		}
		if _, err := q.GetUserByEmailAndSource(ctx, gen.GetUserByEmailAndSourceParams{Email: "CAROL@example.com", AuthSource: "ldap"}); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("GetUserByEmailAndSource(CAROL, ldap) = %v, want no row", err)
		}
		if taken, err := q.EmailTakenIgnoringCase(ctx, "CAROL@EXAMPLE.COM"); err != nil || !taken {
			t.Errorf("EmailTakenIgnoringCase = %t, %v; want true", taken, err)
		}
		if taken, err := q.EmailTakenIgnoringCase(ctx, "\u0130rin@example.com"); err != nil || taken {
			t.Errorf("EmailTakenIgnoringCase(U+0130rin) = %t, %v; want false", taken, err)
		}
		if _, err := q.GetUserByEmailAndSource(ctx, gen.GetUserByEmailAndSourceParams{Email: "\u0130rin@example.com", AuthSource: "local"}); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("GetUserByEmailAndSource(U+0130rin, local) = %v, want no row", err)
		}
		var indexed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE tablename = 'users' AND indexdef LIKE '%lower(email)%')`).Scan(&indexed); err != nil || !indexed {
			t.Errorf("no index on users (lower(email)): %v", err)
		}
	})
}

// TestOIDCIdentity_SQL: the link of an SSO account to its provider identity. Only an
// unlinked SSO account is linked, a stored link is never overwritten, one identity
// names one account, and no other source can hold one.
func TestOIDCIdentity_SQL(t *testing.T) {
	directoryTx(t, func(ctx context.Context, tx pgx.Tx, q *gen.Queries) {
		const iss = "https://sso.example.com"
		ids := map[string]uuid.UUID{}
		for email, source := range map[string]string{"carol@example.com": "oidc", "dave@example.com": "local"} {
			var id uuid.UUID
			if err := tx.QueryRow(ctx, `INSERT INTO users (email, password_hash, display_name, auth_source) VALUES ($1, '', 'x', $2) RETURNING id`, email, source).Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids[source] = id
		}
		link := func(id uuid.UUID, sub string) error {
			_, err := q.LinkOIDCIdentity(ctx, gen.LinkOIDCIdentityParams{ID: id, OidcIssuer: iss, OidcSubject: sub})
			return err
		}
		if err := link(ids["local"], "sub-1"); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("linking a local account = %v, want no row", err)
		}
		if err := link(ids["oidc"], "sub-1"); err != nil {
			t.Fatalf("linking an unlinked SSO account: %v", err)
		}
		if err := link(ids["oidc"], "sub-2"); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("linking a linked account again = %v, want no row", err)
		}
		if u, err := q.GetUserByOIDCIdentity(ctx, gen.GetUserByOIDCIdentityParams{OidcIssuer: iss, OidcSubject: "sub-1"}); err != nil || u.ID != ids["oidc"] {
			t.Errorf("GetUserByOIDCIdentity = %v, %v; want carol", u.ID, err)
		}
		if n, err := q.CountUsersLinkedToIssuer(ctx, iss); err != nil || n != 1 {
			t.Errorf("CountUsersLinkedToIssuer = %d, %v; want 1", n, err)
		}
		// The users list says whether an account is linked, not to what.
		listed, err := q.ListUsersWithRoles(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range listed {
			if want := u.ID == ids["oidc"]; (u.ID == ids["oidc"] || u.ID == ids["local"]) && u.SsoLinked != want {
				t.Errorf("ListUsersWithRoles: %s sso_linked = %t, want %t", u.Email, u.SsoLinked, want)
			}
		}
		// A second account for the identity, and an identity on a local account: each
		// inside a savepoint, so the refusal does not end the transaction.
		for _, st := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO users (email, password_hash, display_name, auth_source, oidc_issuer, oidc_subject) VALUES ('erin@example.com', '', 'x', 'oidc', $1, 'sub-1')`, []any{iss}},
			{`UPDATE users SET oidc_issuer = $1, oidc_subject = 'sub-3' WHERE id = $2`, []any{iss, ids["local"]}},
		} {
			sp, err := tx.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := sp.Exec(ctx, st.sql, st.args...); err == nil {
				t.Errorf("%s: accepted, want refused", st.sql)
			}
			_ = sp.Rollback(ctx)
		}
		if n, err := q.ClearOIDCIdentity(ctx, ids["oidc"]); err != nil || n != 1 {
			t.Errorf("ClearOIDCIdentity = %d, %v; want 1", n, err)
		}
		if err := link(ids["oidc"], "sub-2"); err != nil {
			t.Errorf("linking after a reset: %v", err)
		}
		if cleared, err := q.ClearAllOIDCIdentities(ctx); err != nil || len(cleared) != 1 || cleared[0] != ids["oidc"] {
			t.Errorf("ClearAllOIDCIdentities = %v, %v; want carol", cleared, err)
		}
	})
}

// TestTOTPStepUpProof_SQL: the SSO step-up's proof is recorded only for a live session of
// an active user whose auth_time is recent by the database's clock, and is used up once,
// only by that session, that user and that purpose, and only before it expires.
func TestTOTPStepUpProof_SQL(t *testing.T) {
	directoryTx(t, func(ctx context.Context, tx pgx.Tx, q *gen.Queries) {
		var userID, other, live, revoked uuid.UUID
		for _, row := range []struct {
			dst  *uuid.UUID
			stmt string
		}{
			{&userID, `INSERT INTO users (email, password_hash, display_name, auth_source) VALUES ('erin@example.com', '', 'Erin', 'oidc') RETURNING id`},
			{&other, `INSERT INTO users (email, password_hash, display_name, auth_source) VALUES ('frank@example.com', '', 'Frank', 'oidc') RETURNING id`},
		} {
			if err := tx.QueryRow(ctx, row.stmt).Scan(row.dst); err != nil {
				t.Fatal(err)
			}
		}
		for dst, revokedFlag := range map[*uuid.UUID]bool{&live: false, &revoked: true} {
			if err := tx.QueryRow(ctx, `INSERT INTO sessions (user_id, token_hash, expires_at, is_revoked) VALUES ($1, 'h', now() + interval '1 hour', $2) RETURNING id`,
				userID, revokedFlag).Scan(dst); err != nil {
				t.Fatal(err)
			}
		}
		now := float64(time.Now().Unix())
		record := func(session, user uuid.UUID, authTime float64) int64 {
			n, err := q.RecordTOTPStepUpProof(ctx, gen.RecordTOTPStepUpProofParams{Purpose: "totp_setup", TtlSeconds: 300,
				SessionID: session, UserID: user, AuthTime: authTime, WindowSeconds: 300, SkewSeconds: 60})
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
		consume := func(session, user uuid.UUID, purpose string) int64 {
			n, err := q.ConsumeTOTPStepUpProof(ctx, gen.ConsumeTOTPStepUpProofParams{SessionID: session, UserID: user, Purpose: purpose})
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
		for _, tc := range []struct {
			name          string
			session, user uuid.UUID
			authTime      float64
			want          int64
		}{
			{"an auth_time ten minutes old", live, userID, now - 600, 0},
			{"an auth_time five minutes ahead", live, userID, now + 300, 0},
			{"a revoked session", revoked, userID, now, 0},
			{"a session of another user", live, other, now, 0},
		} {
			if got := record(tc.session, tc.user, tc.authTime); got != tc.want {
				t.Errorf("%s: recorded %d rows, want %d", tc.name, got, tc.want)
			}
		}
		if fresh, err := q.TOTPStepUpAuthTimeIsFresh(ctx, gen.TOTPStepUpAuthTimeIsFreshParams{AuthTime: now - 600, WindowSeconds: 300, SkewSeconds: 60}); err != nil || fresh {
			t.Errorf("an auth_time ten minutes old is fresh = %v, %v; want false", fresh, err)
		}
		if got := record(live, userID, now-30); got != 1 {
			t.Fatalf("a recent sign-in on a live session recorded %d rows, want 1", got)
		}
		if consume(live, userID, "other_purpose") != 0 || consume(live, other, "totp_setup") != 0 || consume(revoked, userID, "totp_setup") != 0 {
			t.Error("the proof was used by another purpose, user or session")
		}
		if consume(live, userID, "totp_setup") != 1 || consume(live, userID, "totp_setup") != 0 {
			t.Error("the proof was not used up exactly once")
		}
		record(live, userID, now)
		if _, err := tx.Exec(ctx, `UPDATE totp_step_up_proofs SET expires_at = now() - interval '1 second'`); err != nil {
			t.Fatal(err)
		}
		if consume(live, userID, "totp_setup") != 0 {
			t.Error("an expired proof was used")
		}
	})
}
