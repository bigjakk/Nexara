package notifications

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var errNoRow = errors.New("engineDB: no row for this query")

// engineDB is the database the engine's dispatch tests run against: each
// QueryRow named in rows answers with that sqlc row struct, every Exec
// succeeds, and every call is recorded by its sqlc query name. onCall, when
// set, runs after each is recorded.
type engineDB struct {
	rows   map[string]any
	onCall func(name string)
	mu     sync.Mutex
	calls  []engineCall
}

type engineCall struct {
	name string
	args []any
}

func queryName(sql string) string {
	_, rest, _ := strings.Cut(sql, "-- name: ")
	name, _, _ := strings.Cut(rest, " ")
	return name
}

func (d *engineDB) record(sql string, args []any) {
	d.mu.Lock()
	d.calls = append(d.calls, engineCall{name: queryName(sql), args: args})
	d.mu.Unlock()
	if d.onCall != nil {
		d.onCall(queryName(sql))
	}
}

// callsOf returns the arguments of every recorded call of one query.
func (d *engineDB) callsOf(name string) [][]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out [][]any
	for _, c := range d.calls {
		if c.name == name {
			out = append(out, c.args)
		}
	}
	return out
}

func (d *engineDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	d.record(sql, args)
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (d *engineDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	d.record(sql, args)
	return nil, errNoRow
}

func (d *engineDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	d.record(sql, args)
	return replayRow{row: d.rows[queryName(sql)]}
}

// replayRow scans a sqlc row struct's fields positionally, the order the
// generated Scan passes its destinations in; a nil row is no row.
type replayRow struct{ row any }

func (r replayRow) Scan(dest ...any) error {
	if r.row == nil {
		return errNoRow
	}
	v := reflect.ValueOf(r.row)
	if v.NumField() != len(dest) {
		return fmt.Errorf("scan got %d destinations, row has %d fields", len(dest), v.NumField())
	}
	for i, d := range dest {
		reflect.ValueOf(d).Elem().Set(v.Field(i))
	}
	return nil
}
