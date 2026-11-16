package db

import (
	"net/url"
	"strings"
	"testing"
)

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
			// pgxpool reads these and golang-migrate would send them to the server.
			name: "pool_only_keys_dropped_others_kept_in_order",
			in:   "postgres://u@h/db?pool_max_conns=20&sslmode=disable&pool_min_conns=2&application_name=x&pool_max_conn_lifetime=1h",
			want: "pgx5://u@h/db?sslmode=disable&application_name=x",
		},
		{
			name: "only_pool_keys_leave_no_query",
			in:   "postgresql://u@h/db?pool_health_check_period=1m&pool_min_idle_conns=1&pool_max_conn_idle_time=5m&pool_max_conn_lifetime_jitter=1s",
			want: "pgx5://u@h/db",
		},
		{
			// The app pool's timeouts, set by an operator, must not cut a migration.
			name: "app_session_timeouts_dropped",
			in:   "postgres://u@h/db?lock_timeout=5s&sslmode=disable&idle_in_transaction_session_timeout=30s",
			want: "pgx5://u@h/db?sslmode=disable",
		},
		{
			name: "keyword_value_dsn_untouched",
			in:   "host=h dbname=db pool_max_conns=20",
			want: "host=h dbname=db pool_max_conns=20",
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

// TestNewMigrator_ErrorsNeverCarryThePassword: the error is printed by the startup
// fatal and by `nexara migrate`, so the password must not be in it, whatever form
// DATABASE_URL takes. A keyword/value string with a ":" in it once came back from
// golang-migrate as "unknown driver <the whole string>".
func TestNewMigrator_ErrorsNeverCarryThePassword(t *testing.T) {
	t.Parallel()
	const pw = "s3cr3t:Pa55-synthetic"
	for _, tc := range []struct{ name, dsn, says string }{
		{"keyword/value", "host=/var/run/postgresql user=nexara password=" + pw + " dbname=nexara", "postgres://user:password@host"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, cleanup, err := NewMigrator(tc.dsn)
			if err == nil {
				cleanup()
				t.Fatalf("NewMigrator(%s) succeeded, want an error", tc.name)
			}
			if strings.Contains(err.Error(), "s3cr3t") || !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error = %q: want no password and %q in it", err, tc.says)
			}
		})
	}
}

// TestWithoutSecrets_ScrubsEverySpellingOfThePassword: golang-migrate's text is not
// ours, so the scrub must catch the password however a library may have written it.
func TestWithoutSecrets_ScrubsEverySpellingOfThePassword(t *testing.T) {
	t.Parallel()
	dsn := "postgres://nexara:" + url.PathEscape("a:b;c x") + "@db.example.com:5432/nexara"
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	for _, spelling := range []string{
		"a:b;c x",                      // decoded
		"a%3Ab%3Bc+x",                  // url.QueryEscape
		"a:b%3Bc%20x",                  // url.PathEscape
		"a%3Ab;c%20x",                  // url.URL's userinfo
		dsn,                            // the URL as given
		"pgx5" + dsn[len("postgres"):], // as handed to golang-migrate
	} {
		if got := withoutSecrets("dial failed: "+spelling+" refused", dsn, parsed); strings.Contains(got, spelling) || !strings.Contains(got, "REDACTED") {
			t.Errorf("withoutSecrets kept %q: %q", spelling, got)
		}
	}
}
