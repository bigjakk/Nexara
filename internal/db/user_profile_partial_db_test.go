package db

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// UpdateUserProfile (queries/rbac.sql) is a PARTIAL update: a field the caller leaves
// NULL keeps the value the row holds at the moment of the write. The handler reads the
// account before it writes, to answer 404 and to refuse a caller's own role or active
// flag, and used to write all three columns back from that read — a lost update.
// These tests run the statement against Postgres, with the concurrent edit that the
// old shape undid committed between the read and the write.

func profileText(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }
func profileBool(b bool) pgtype.Bool   { return pgtype.Bool{Bool: b, Valid: true} }

// TestUpdateUserProfile_WritesOnlyWhatItIsGiven pins the statement field by field: each
// row supplies some fields, a concurrent edit has changed the OTHERS after the caller
// read the account, and the result must hold the supplied fields as written and the
// others as the concurrent edit left them.
func TestUpdateUserProfile_WritesOnlyWhatItIsGiven(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	h, purge := newEpochHarness(t, env)
	defer purge()

	tests := []struct {
		name   string
		params gen.UpdateUserProfileParams
		// startRole is the account's role when the caller reads it ("user" when empty). A
		// row whose concurrent edit sets the role must start at a DIFFERENT one, or the
		// edit changes nothing and the row cannot tell a field that was left alone from
		// one that was written back.
		startRole string
		// concurrent is what another request committed after the caller read the account.
		concurrent string
		wantName   string
		wantActive bool
		wantRole   string
	}{
		{
			// The reactivation the old shape caused: the name edit had read is_active = true.
			name:       "a name edit does not re-activate an account deactivated in the meantime",
			params:     gen.UpdateUserProfileParams{DisplayName: profileText("Renamed")},
			concurrent: `UPDATE users SET is_active = false WHERE id = $1`,
			wantName:   "Renamed", wantActive: false, wantRole: "user",
		},
		{
			// The demotion the old shape undid: the deactivation had read role = admin.
			name:       "a deactivation does not write back the role a concurrent edit demoted",
			params:     gen.UpdateUserProfileParams{IsActive: profileBool(false)},
			startRole:  "admin",
			concurrent: `UPDATE users SET role = 'user' WHERE id = $1`,
			wantName:   "Profile Original", wantActive: false, wantRole: "user",
		},
		{
			// The other direction, so that neither a constant 'user' nor a constant 'admin'
			// written for an omitted role can pass both rows.
			name:       "a name edit does not write back the role a concurrent edit promoted",
			params:     gen.UpdateUserProfileParams{DisplayName: profileText("Renamed")},
			concurrent: `UPDATE users SET role = 'admin' WHERE id = $1`,
			wantName:   "Renamed", wantActive: true, wantRole: "admin",
		},
		{
			// No concurrent edit at all: an omitted is_active is the account as it is.
			name:       "a name edit leaves an active account active",
			params:     gen.UpdateUserProfileParams{DisplayName: profileText("Renamed")},
			concurrent: `UPDATE users SET id = id WHERE id = $1`,
			wantName:   "Renamed", wantActive: true, wantRole: "user",
		},
		{
			name:       "a role change leaves an active account active",
			params:     gen.UpdateUserProfileParams{Role: profileText("admin")},
			concurrent: `UPDATE users SET id = id WHERE id = $1`,
			wantName:   "Profile Original", wantActive: true, wantRole: "admin",
		},
		{
			name:       "a role change does not write back a name or an active flag",
			params:     gen.UpdateUserProfileParams{Role: profileText("admin")},
			concurrent: `UPDATE users SET is_active = false, display_name = 'Edited Elsewhere' WHERE id = $1`,
			wantName:   "Edited Elsewhere", wantActive: false, wantRole: "admin",
		},
		{
			name:       "all three fields are written when all three are given",
			params:     gen.UpdateUserProfileParams{DisplayName: profileText("All Three"), IsActive: profileBool(false), Role: profileText("admin")},
			concurrent: `UPDATE users SET display_name = 'Edited Elsewhere' WHERE id = $1`,
			wantName:   "All Three", wantActive: false, wantRole: "admin",
		},
		{
			name:       "nothing given changes nothing",
			params:     gen.UpdateUserProfileParams{},
			concurrent: `UPDATE users SET is_active = false, role = 'admin', display_name = 'Edited Elsewhere' WHERE id = $1`,
			wantName:   "Edited Elsewhere", wantActive: false, wantRole: "admin",
		},
		{
			// NULL is "not given"; an empty string is a value and is written.
			name:       "an empty display name is a value, not an unset field",
			params:     gen.UpdateUserProfileParams{DisplayName: profileText("")},
			concurrent: `UPDATE users SET role = 'admin' WHERE id = $1`,
			wantName:   "", wantActive: true, wantRole: "admin",
		},
		{
			name:       "true is a value too: an account can be re-activated explicitly",
			params:     gen.UpdateUserProfileParams{IsActive: profileBool(true)},
			concurrent: `UPDATE users SET is_active = false WHERE id = $1`,
			wantName:   "Profile Original", wantActive: true, wantRole: "user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h.reset(t)
			startRole := tt.startRole
			if startRole == "" {
				startRole = "user"
			}
			h.exec(t, "the account as the caller read it", `UPDATE users SET display_name = 'Profile Original', role = $2, is_active = true WHERE id = $1`, epochUserA, startRole)
			// The caller's read, whose values a full overwrite would write back.
			if _, err := h.q.GetUserByID(env.Ctx, epochUserA); err != nil {
				t.Fatalf("the caller's read: %v", err)
			}
			h.exec(t, "the concurrent edit", tt.concurrent, epochUserA)

			tt.params.ID = epochUserA
			got, err := h.q.UpdateUserProfile(env.Ctx, tt.params)
			if err != nil {
				t.Fatalf("UpdateUserProfile: %v", err)
			}

			if got.DisplayName != tt.wantName || got.IsActive != tt.wantActive || got.Role != tt.wantRole {
				t.Errorf("the statement returned name=%q active=%t role=%q, want name=%q active=%t role=%q",
					got.DisplayName, got.IsActive, got.Role, tt.wantName, tt.wantActive, tt.wantRole)
			}
			// And the row holds what was returned.
			stored, err := h.q.GetUserByID(env.Ctx, epochUserA)
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			if stored.DisplayName != tt.wantName || stored.IsActive != tt.wantActive || stored.Role != tt.wantRole {
				t.Errorf("the row holds name=%q active=%t role=%q, want name=%q active=%t role=%q",
					stored.DisplayName, stored.IsActive, stored.Role, tt.wantName, tt.wantActive, tt.wantRole)
			}
		})
	}

	t.Run("an account that does not exist is no rows", func(t *testing.T) {
		_, err := h.q.UpdateUserProfile(env.Ctx, gen.UpdateUserProfileParams{ID: uuid.New(), DisplayName: profileText("Nobody")})
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("err = %v, want pgx.ErrNoRows: the handler answers a removed account from it", err)
		}
	})
}
