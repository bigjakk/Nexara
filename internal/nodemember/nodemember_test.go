package nodemember

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// lookupFunc adapts a function to Lookup.
type lookupFunc func(db.GetNodeByClusterAndNameParams) (db.Node, error)

func (f lookupFunc) GetNodeByClusterAndName(_ context.Context, arg db.GetNodeByClusterAndNameParams) (db.Node, error) {
	return f(arg)
}

// TestCheckAndRequire pins the one definition of "member": a row is a member,
// only "no such row" (wrapped or not) is a stranger, and any other failure is
// neither — Check reports it, and Require turns it into a LookupError whose
// text never carries the database's own words, since it lands in
// operator-readable fields.
func TestCheckAndRequire(t *testing.T) {
	cluster := uuid.New()
	for _, tt := range []struct {
		name       string
		answer     error
		wantMember bool
		wantErr    bool
		wantText   string
	}{
		{name: "a row", wantMember: true},
		{name: "no row", answer: pgx.ErrNoRows,
			wantText: `node "192.0.2.10" is not one of this cluster's nodes; it was not contacted`},
		{name: "no row, wrapped", answer: fmt.Errorf("query: %w", pgx.ErrNoRows),
			wantText: `node "192.0.2.10" is not one of this cluster's nodes; it was not contacted`},
		{name: "a failed lookup", answer: errors.New("connection refused"), wantErr: true,
			wantText: `could not confirm node "192.0.2.10" is one of this cluster's nodes; it was not contacted`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var asked []db.GetNodeByClusterAndNameParams
			q := lookupFunc(func(arg db.GetNodeByClusterAndNameParams) (db.Node, error) {
				asked = append(asked, arg)
				return db.Node{}, tt.answer
			})

			member, err := Check(context.Background(), q, cluster, "192.0.2.10")
			if member != tt.wantMember || (err != nil) != tt.wantErr {
				t.Errorf("Check = %v, %v; want member %v, error %v", member, err, tt.wantMember, tt.wantErr)
			}

			err = Require(context.Background(), q, cluster, "192.0.2.10")
			switch {
			case tt.wantMember:
				if err != nil {
					t.Errorf("Require = %v, want nil", err)
				}
			case tt.wantErr:
				var lookupErr *LookupError
				if !errors.As(err, &lookupErr) || !errors.Is(err, tt.answer) {
					t.Errorf("Require = %#v, want a *LookupError wrapping %v", err, tt.answer)
				}
			default:
				if !errors.As(err, new(*NotMemberError)) {
					t.Errorf("Require = %#v, want a *NotMemberError", err)
				}
			}
			if tt.wantText != "" && (err == nil || err.Error() != tt.wantText) {
				t.Errorf("Require's text = %v, want %q", err, tt.wantText)
			}
			if err != nil && strings.Contains(err.Error(), "connection refused") {
				t.Errorf("Require's text %q carries the database's error", err)
			}

			want := db.GetNodeByClusterAndNameParams{ClusterID: cluster, Name: "192.0.2.10"}
			if len(asked) != 2 || asked[0] != want || asked[1] != want {
				t.Errorf("asked %+v, want %+v twice", asked, want)
			}
		})
	}
}
