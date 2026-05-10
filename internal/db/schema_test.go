package db

import "testing"

func TestParseMigrationVersion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   string
		want    uint64
		wantOk  bool
		comment string
	}{
		{name: "canonical_up", input: "000037_audit_log_perf_indexes.up.sql", want: 37, wantOk: true},
		{name: "canonical_down", input: "000037_audit_log_perf_indexes.down.sql", want: 37, wantOk: true},
		{name: "leading_zeroes", input: "000001_initial_schema.up.sql", want: 1, wantOk: true},
		{name: "no_zeroes", input: "42_some_change.up.sql", want: 42, wantOk: true},
		{name: "no_underscore", input: "000037audit.up.sql", wantOk: false, comment: "missing separator"},
		{name: "empty", input: "", wantOk: false},
		{name: "non_numeric_prefix", input: "abc_thing.up.sql", wantOk: false},
		{name: "underscore_first", input: "_000037_thing.up.sql", wantOk: false},
		{name: "negative_number", input: "-1_thing.up.sql", wantOk: false, comment: "uint parser rejects sign"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseMigrationVersion(tc.input)
			if ok != tc.wantOk {
				t.Fatalf("ok = %v, want %v (%s)", ok, tc.wantOk, tc.comment)
			}
			if ok && got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestToPgx5URL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "postgres_scheme",
			in:   "postgres://user:pw@host:5432/db?sslmode=disable",
			want: "pgx5://user:pw@host:5432/db?sslmode=disable",
		},
		{
			name: "postgresql_scheme",
			in:   "postgresql://user@host/db",
			want: "pgx5://user@host/db",
		},
		{
			name: "already_pgx5_unchanged",
			in:   "pgx5://user@host/db",
			want: "pgx5://user@host/db",
		},
		{
			name: "empty_string",
			in:   "",
			want: "",
		},
		{
			name: "scheme_only_substring_inside_path_left_alone",
			in:   "weird://postgres.example.com/db",
			want: "weird://postgres.example.com/db",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := toPgx5URL(tc.in); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
