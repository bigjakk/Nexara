package handlers

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// A folder's subtree takes each folder once: a tree whole, a cycle through the
// folder — which two concurrent moves could leave — to its end rather than for
// ever, a chain deeper than the cap down to the cap, and a folder the cluster
// does not have as nothing.
func TestFolderSubtree(t *testing.T) {
	t.Parallel()
	folder := func(id uuid.UUID, parent uuid.UUID) db.VmFolder {
		return db.VmFolder{ID: id, ParentID: pgtype.UUID{Bytes: parent, Valid: parent != uuid.Nil}}
	}
	a, b, c, d := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	chain := make([]db.VmFolder, folderDepthCap+2)
	for i := range chain {
		parent := uuid.Nil
		if i > 0 {
			parent = chain[i-1].ID
		}
		chain[i] = folder(uuid.New(), parent)
	}
	ids := func(fs []db.VmFolder) []uuid.UUID {
		out := make([]uuid.UUID, 0, len(fs))
		for _, f := range fs {
			out = append(out, f.ID)
		}
		return out
	}
	for _, tt := range []struct {
		name string
		all  []db.VmFolder
		id   uuid.UUID
		want []uuid.UUID
	}{
		{"a tree", []db.VmFolder{folder(a, uuid.Nil), folder(b, a), folder(c, a), folder(d, b)}, a, []uuid.UUID{a, b, c, d}},
		{"a cycle through the folder", []db.VmFolder{folder(a, c), folder(b, a), folder(c, b)}, a, []uuid.UUID{a, b, c}},
		{"a chain past the cap", chain, chain[0].ID, ids(chain[:folderDepthCap+1])},
		{"a folder the cluster does not have", []db.VmFolder{folder(a, uuid.Nil)}, b, nil},
	} {
		if got := folderSubtree(tt.all, tt.id); !slices.Equal(got, tt.want) {
			t.Errorf("%s: %d folders, want %d", tt.name, len(got), len(tt.want))
		}
	}
}
