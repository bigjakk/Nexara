package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// Synthetic throughout. The token id is the house fixture, the secret is a
// made-up UUID, and the key is a throwaway 32-byte hex string.
const (
	credTestKey       = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	credTestTokenID   = "nexara@pve!api"
	credTestSecret    = "11111111-2222-3333-4444-555555555555"
	credTestSourceVM  = 100
	credTestSourceCT  = 200
	credTestNode      = "pve-01"
	credTestClusterFP = "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"
)

// --- a DBTX that answers GetCluster and nothing else ---

// stubDBTX answers exactly one query — the clusters read behind
// clientForCluster — and is its own pgx.Row, since that query returns one row.
type stubDBTX struct {
	t       *testing.T
	cluster db.Cluster
	// execs, when non-nil, records every write instead of rejecting it.
	execs *[]execCall
}

// execCall is one write the code under test performed.
type execCall struct {
	sql  string
	args []any
}

func (s stubDBTX) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	// Honour the context the way pgx does, and do it BEFORE the recording
	// check. A real pool refuses a write on a cancelled context — the
	// statement never reaches Postgres — so a stub that accepted one would
	// make every "this write happened" assertion unable to fail for the one
	// mistake that matters on a shutdown path: passing the cancelled parent
	// instead of the cleanupCtxFor-derived context.
	//
	// This is what pins cleanupCtxFor. There is no direct unit test of
	// migration's copy anywhere (internal/rolling tests only its own), so
	// every caller that reaches a write through it — failJob, and
	// pollTaskStatus's ctx.Done() branch — is verified here or nowhere.
	if err := ctx.Err(); err != nil {
		return pgconn.CommandTag{}, err
	}
	// A test that has not opted into recording writes is asserting there are
	// none, so keep failing loudly for it.
	if s.execs == nil {
		s.t.Errorf("stubDBTX: unexpected Exec — this path should not write: %s", sql)
		return pgconn.CommandTag{}, errors.New("stubDBTX: Exec not expected")
	}
	*s.execs = append(*s.execs, execCall{sql: sql, args: args})
	return pgconn.CommandTag{}, nil
}

// arg returns the nth bind parameter of the first recorded write whose SQL
// contains needle, and fails the test if there was no such write — so an
// assertion can never quietly pass because the write never happened.
func findExecArg(t *testing.T, execs []execCall, needle string, n int) string {
	t.Helper()
	for _, e := range execs {
		if !strings.Contains(e.sql, needle) {
			continue
		}
		if n >= len(e.args) {
			t.Fatalf("write matching %q has %d args, wanted arg %d", needle, len(e.args), n)
		}
		got, ok := e.args[n].(string)
		if !ok {
			t.Fatalf("write matching %q arg %d is %T, not a string", needle, n, e.args[n])
		}
		return got
	}
	t.Fatalf("no write matching %q was performed — nothing was tested (saw %d writes)", needle, len(execs))
	return ""
}

func (s stubDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) {
	s.t.Errorf("stubDBTX: unexpected Query — this path should only read one cluster")
	return nil, errors.New("stubDBTX: Query not expected")
}

func (s stubDBTX) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	s.t.Helper()
	if !strings.Contains(sql, "FROM clusters") {
		s.t.Fatalf("stubDBTX: only the clusters read is faked, got: %s", sql)
	}
	return s
}

// Scan fills the caller's destinations from the one cluster this stub holds.
// The generated GetCluster scans the columns in struct-field order, so field i
// goes to dest[i]; a count mismatch means sqlc regenerated with a different
// column list and this fake is lying rather than failing.
func (s stubDBTX) Scan(dest ...any) error {
	s.t.Helper()
	v := reflect.ValueOf(s.cluster)
	if len(dest) != v.NumField() {
		s.t.Fatalf("GetCluster scanned %d columns but db.Cluster has %d fields — "+
			"this fake is out of step with sqlc", len(dest), v.NumField())
	}
	for i := range dest {
		reflect.ValueOf(dest[i]).Elem().Set(v.Field(i))
	}
	return nil
}

// newCredTestOrchestrator wires an Orchestrator whose only cluster is the one
// at serverURL, holding an encrypted credTestSecret.
func newCredTestOrchestrator(t *testing.T, serverURL string, execs *[]execCall) *Orchestrator {
	t.Helper()
	encrypted, err := crypto.Encrypt(credTestSecret, credTestKey)
	if err != nil {
		t.Fatalf("crypto.Encrypt: %v", err)
	}
	stub := stubDBTX{t: t, execs: execs, cluster: db.Cluster{
		ID:                   uuid.New(),
		Name:                 "cluster02",
		ApiUrl:               serverURL,
		TokenID:              credTestTokenID,
		TokenSecretEncrypted: encrypted,
		TlsFingerprint:       credTestClusterFP,
		IsActive:             true,
	}}
	return NewOrchestrator(
		db.New(stub),
		credTestKey,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
	)
}

// Change 1. executeCrossCluster assembles the target cluster's stored,
// long-lived API token into the target-endpoint property string, and hands
// whatever error comes back to Execute, which passes err.Error() straight to
// failJob (orchestrator.go, the TypeCrossCluster branch). failJob both logs
// that string and writes it to migration_jobs.error_message — a column the
// migration handlers return as error_message to anyone holding view:migration
// on the source or target cluster, which is a Viewer-level read.
//
// The fixture makes the upstream echo the credential back, because that is the
// one link in the chain nobody controls: Proxmox's own error formatting. It is
// not a behaviour PVE has been observed to have; the test asserts the chain
// holds even if it did.
//
// Non-vacuity is load-bearing in three places, and each is checked:
//   - the handler asserts the secret really did go out on the wire and really
//     was echoed back, so an absence result cannot come from a broken fixture;
//   - the error must still carry the client's own context and the upstream
//     message, so a scrub that returned "" would fail;
//   - REDACTED must be present, so a pass cannot come from the scrub silently
//     doing nothing.
func TestExecuteCrossCluster_ScrubsTheTargetTokenFromTheFailure(t *testing.T) {
	bodies := []struct {
		name string
		make func(endpoint string) (int, string)
	}{
		{
			// The Proxmox validation-error envelope, which parseProxmoxError
			// flattens into "<field>: <msg>".
			name: "parameter rejection envelope",
			make: func(endpoint string) (int, string) {
				return http.StatusBadRequest, fmt.Sprintf(
					`{"errors":{"target-endpoint":"invalid format - %s"},"message":"Parameter verification failed."}`,
					endpoint)
			},
		},
		{
			// Anything that is not that envelope reaches APIError.Message as
			// the raw body, untouched.
			name: "raw body",
			make: func(endpoint string) (int, string) {
				return http.StatusInternalServerError,
					"500 Internal Server Error: could not connect using " + endpoint
			},
		},
	}

	guests := []struct {
		name   string
		vmType string
		vmid   int32
		path   string
		wrap   string
	}{
		{"QEMU", VMTypeQEMU, credTestSourceVM,
			"/api2/json/nodes/pve-01/qemu/100/remote_migrate",
			"remote migrate VM 100 on pve-01"},
		{"LXC", VMTypeLXC, credTestSourceCT,
			"/api2/json/nodes/pve-01/lxc/200/remote_migrate",
			"remote migrate CT 200 on pve-01"},
	}

	for _, body := range bodies {
		for _, g := range guests {
			t.Run(body.name+"/"+g.name, func(t *testing.T) {
				var echoed bool

				mux := http.NewServeMux()
				mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
					t.Errorf("request went to an unexpected path %s — the fixture is wrong", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				})
				mux.HandleFunc(g.path, func(w http.ResponseWriter, r *http.Request) {
					if err := r.ParseForm(); err != nil {
						t.Errorf("ParseForm: %v", err)
					}
					endpoint := r.PostFormValue("target-endpoint")
					if !strings.Contains(endpoint, credTestSecret) {
						t.Errorf("target-endpoint = %q — the credential never reached the wire, "+
							"so the scrub below is being tested against nothing", endpoint)
						return
					}
					echoed = true
					status, respBody := body.make(endpoint)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, respBody)
				})
				srv := httptest.NewServer(mux)
				defer srv.Close()

				host := mustHostname(t, srv.URL)
				o := newCredTestOrchestrator(t, srv.URL, nil)
				srcClient, srcCluster, err := o.clientForCluster(context.Background(), uuid.New())
				if err != nil {
					t.Fatalf("clientForCluster: %v", err)
				}

				job := db.MigrationJob{
					SourceClusterID: uuid.New(),
					TargetClusterID: uuid.New(),
					SourceNode:      credTestNode,
					Vmid:            g.vmid,
					VmType:          g.vmType,
					MigrationType:   TypeCrossCluster,
					// Both are set so the auto-detect paths, which would need
					// more of the API faked, stay out of the way.
					NetworkMap: []byte(`{"vmbr0":"vmbr0"}`),
					StorageMap: []byte(`{}`),
					TargetVmid: 999,
				}

				mc := &migrationContext{job: job}
				_, err = o.executeCrossCluster(context.Background(), srcClient, srcCluster, job, mc)
				if err == nil {
					t.Fatal("executeCrossCluster succeeded; the fixture was supposed to reject the request")
				}
				if !echoed {
					t.Fatal("the upstream handler never echoed the endpoint back — nothing was tested")
				}

				// This is the exact string Execute hands to failJob.
				msg := err.Error()

				if strings.Contains(msg, credTestSecret) {
					t.Errorf("the token secret reached failJob: %s", msg)
				}
				if strings.Contains(msg, credTestTokenID+"="+credTestSecret) {
					t.Errorf("the full apitoken reached failJob: %s", msg)
				}
				if !strings.Contains(msg, redactedTokenSecret) {
					t.Errorf("no %s marker — the scrub did not run, the secret simply was not there: %s",
						redactedTokenSecret, msg)
				}
				// Survival. A scrub that returned "" passes every check above,
				// which is the defect this repo repeats most often.
				survives := []string{
					g.wrap,                  // the client's own context
					host,                    // the endpoint it was talking to
					"apitoken=PVEAPIToken=", // the echoed property string, minus the secret
					credTestTokenID,         // the token id half, deliberately kept
				}
				for _, want := range survives {
					if !strings.Contains(msg, want) {
						t.Errorf("the scrub ate %q; the failure is no longer diagnosable: %s", want, msg)
					}
				}
			})
		}
	}
}

func mustHostname(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u.Hostname()
}

// The empty-needle case is the classic vacuous input: strings.ReplaceAll with
// "" splices the replacement in at every position. These pin the boundaries of
// scrubEndpointSecret directly, since the end-to-end test above can only reach
// the well-formed one.
func TestScrubEndpointSecret(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		apiToken string
		want     string
	}{
		{
			name:     "removes the secret half and keeps the rest",
			in:       "remote migrate VM 100 on pve-01: proxmox API error 400: target-endpoint: invalid format - apitoken=PVEAPIToken=nexara@pve!api=" + credTestSecret + ",host=pve-01.example.com",
			apiToken: credTestTokenID + "=" + credTestSecret,
			want:     "remote migrate VM 100 on pve-01: proxmox API error 400: target-endpoint: invalid format - apitoken=PVEAPIToken=nexara@pve!api=REDACTED,host=pve-01.example.com",
		},
		{
			name:     "an empty token leaves the message byte for byte",
			in:       "remote migrate VM 100 on pve-01: connection refused",
			apiToken: "",
			want:     "remote migrate VM 100 on pve-01: connection refused",
		},
		{
			name:     "a token id with no secret leaves the message byte for byte",
			in:       "remote migrate VM 100 on pve-01: connection refused",
			apiToken: credTestTokenID + "=",
			want:     "remote migrate VM 100 on pve-01: connection refused",
		},
		{
			name:     "a token with no separator is treated as secret in full",
			in:       "remote migrate VM 100 on pve-01: rejected bare-secret-value",
			apiToken: "bare-secret-value",
			want:     "remote migrate VM 100 on pve-01: rejected REDACTED",
		},
		{
			name:     "a message that never held the secret is untouched",
			in:       "remote migrate VM 100 on pve-01: connection refused",
			apiToken: credTestTokenID + "=" + credTestSecret,
			want:     "remote migrate VM 100 on pve-01: connection refused",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scrubEndpointSecret(errors.New(tt.in), tt.apiToken)
			if got.Error() != tt.want {
				t.Errorf("scrubEndpointSecret() = %q, want %q", got.Error(), tt.want)
			}
		})
	}

	if scrubEndpointSecret(nil, credTestTokenID+"="+credTestSecret) != nil {
		t.Error("scrubEndpointSecret(nil, ...) should stay nil")
	}
}

// Unwrap is kept so a sentinel check still sees through the scrub. Without it
// a caller reaching for proxmox.ErrForbidden would silently stop matching.
func TestScrubEndpointSecretKeepsTheErrorChain(t *testing.T) {
	inner := fmt.Errorf("%w: token %s rejected", proxmox.ErrForbidden, credTestSecret)
	got := scrubEndpointSecret(inner, credTestTokenID+"="+credTestSecret)

	if strings.Contains(got.Error(), credTestSecret) {
		t.Errorf("the secret survived: %s", got.Error())
	}
	if !errors.Is(got, proxmox.ErrForbidden) {
		t.Error("errors.Is no longer reaches ErrForbidden through the scrub")
	}
}

// L1. The synchronous rejection is only half the exposure. When Proxmox
// ACCEPTS the remote_migrate and the worker dies later, pollTaskStatus reads
// PVE's die-message out of the task's exit status and writes it to two
// Viewer-readable places: migration_jobs.error_message (the same view:migration
// column the synchronous path lands in) and task_history.exit_status (served
// to any view:task holder). Same vendor, same lack of control over its error
// formatting, same column — so the same scrub has to reach it.
//
// The credential is not in scope at the pollTaskStatus call site, so the two
// halves are joined at the point the credential is built: executeCrossCluster
// arms the migrationContext, and pollTaskStatus reads it back.
//
// Non-vacuity: findExecArg fails if the write never happened, the fixture
// asserts the secret really was in the exit status, and each DB sink is
// checked for survival as well as absence.
//
// The third sink — the o.logger.Error line beside those two writes — is NOT
// covered here, and that is a stated gap rather than an oversight: replacing
// mc.scrub with the raw value there leaves this package green. It is
// defence-in-depth (no HTTP route serves the application log) and capturing
// slog would buy a regression guard on the one sink a reader cannot reach.
// Say so rather than letting "each sink" imply three.
func TestPollTaskStatus_ScrubsTheTargetTokenFromTheWorkerFailure(t *testing.T) {
	const (
		node = credTestNode
		upid = "UPID:pve-01:00001234:00000000:68000000:qmigrate:100:root@pam:"
	)
	// What PVE's worker would say if it echoed the endpoint it was handed.
	dieMessage := "migration aborted (duration 00:00:03): error - target endpoint " +
		"apitoken=PVEAPIToken=" + credTestTokenID + "=" + credTestSecret + " refused"

	var polled bool
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("request went to an unexpected path %s — the fixture is wrong", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/api2/json/nodes/"+node+"/tasks/"+upid+"/status",
		func(w http.ResponseWriter, _ *http.Request) {
			polled = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
				"status": "stopped", "exitstatus": dieMessage,
				"type": "qmigrate", "upid": upid, "node": node,
			}})
		})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var execs []execCall
	o := newCredTestOrchestrator(t, srv.URL, &execs)
	client, _, err := o.clientForCluster(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("clientForCluster: %v", err)
	}

	// Armed the way executeCrossCluster arms it, from the same token string.
	mc := &migrationContext{job: db.MigrationJob{
		SourceClusterID: uuid.New(), Vmid: credTestSourceVM, VmType: VMTypeQEMU,
		MigrationType: TypeCrossCluster,
	}}
	mc.armEndpointScrubber(credTestTokenID + "=" + credTestSecret)

	o.pollTaskStatus(context.Background(), client, node, upid, uuid.New(), mc)

	if !polled {
		t.Fatal("the task-status handler never ran — nothing was tested")
	}
	if !strings.Contains(dieMessage, credTestSecret) {
		t.Fatal("the fixture die-message does not contain the secret — nothing was tested")
	}

	// CompleteMigrationJob binds (id, status, completed_at, error_message).
	// ReconcileTaskHistory binds (upid, status, exit_status, finished_at).
	for _, sink := range []struct {
		name     string
		needle   string
		arg      int
		survives string
	}{
		{"migration_jobs.error_message", "UPDATE migration_jobs", 3, "Task exit status: migration aborted"},
		{"task_history.exit_status", "UPDATE task_history", 2, "migration aborted"},
	} {
		t.Run(sink.name, func(t *testing.T) {
			got := findExecArg(t, execs, sink.needle, sink.arg)
			if strings.Contains(got, credTestSecret) {
				t.Errorf("the token secret was written to %s: %s", sink.name, got)
			}
			if !strings.Contains(got, redactedTokenSecret) {
				t.Errorf("no %s marker in %s — the scrub did not run: %s",
					redactedTokenSecret, sink.name, got)
			}
			// Survival: a scrubber that returned "" would pass both checks
			// above while destroying the operator's only clue.
			if !strings.Contains(got, sink.survives) {
				t.Errorf("the scrub ate %q from %s: %s", sink.survives, sink.name, got)
			}
		})
	}
}

// The other half of L1: pollTaskStatus can only scrub what executeCrossCluster
// armed. This pins the wiring between them, which is invisible to both of the
// tests above — one arms the context by hand, the other never reaches the
// async path.
func TestExecuteCrossCluster_ArmsTheContextScrubber(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "nope")
	}))
	defer srv.Close()

	o := newCredTestOrchestrator(t, srv.URL, nil)
	srcClient, srcCluster, err := o.clientForCluster(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("clientForCluster: %v", err)
	}

	mc := &migrationContext{}
	if got := mc.scrub("secret " + credTestSecret); got != "secret "+credTestSecret {
		t.Fatalf("an unarmed context should pass text through, got %q", got)
	}

	_, _ = o.executeCrossCluster(context.Background(), srcClient, srcCluster, db.MigrationJob{
		SourceClusterID: uuid.New(), TargetClusterID: uuid.New(),
		SourceNode: credTestNode, Vmid: credTestSourceVM, VmType: VMTypeQEMU,
		MigrationType: TypeCrossCluster,
		NetworkMap:    []byte(`{"vmbr0":"vmbr0"}`), StorageMap: []byte(`{}`),
		TargetVmid: 999,
	}, mc)

	got := mc.scrub("worker died: " + credTestSecret + " refused")
	if strings.Contains(got, credTestSecret) {
		t.Errorf("executeCrossCluster did not arm the scrubber; the async sinks are unprotected: %s", got)
	}
	if !strings.Contains(got, "worker died:") || !strings.Contains(got, "refused") {
		t.Errorf("the armed scrubber ate the surrounding text: %s", got)
	}
}

// armEndpointScrubber leaves the context unarmed when there is no secret to
// hide, so that mc.scrubber != nil keeps meaning "this job has a credential in
// play". Without this the guard is unkillable: installing a do-nothing closure
// behaves identically at every call site, so only the nil-ness distinguishes
// them.
func TestArmEndpointScrubberLeavesASecretlessTokenUnarmed(t *testing.T) {
	for _, apiToken := range []string{"", credTestTokenID + "="} {
		t.Run("token "+strconv.Quote(apiToken), func(t *testing.T) {
			mc := &migrationContext{}
			mc.armEndpointScrubber(apiToken)
			if mc.scrubber != nil {
				t.Errorf("armed a scrubber for %q, which has no secret to hide", apiToken)
			}
		})
	}

	// And the positive control, so the assertion above is not passing because
	// arming never works.
	mc := &migrationContext{}
	mc.armEndpointScrubber(credTestTokenID + "=" + credTestSecret)
	if mc.scrubber == nil {
		t.Fatal("a real token did not arm the scrubber")
	}
}

// TestFailJob_ScrubsAnArmedContextsCredential makes failJob's own scrub
// killable.
//
// It is not reachable through executeCrossCluster, which scrubs at its exit
// and so hands failJob text that is already clean — which is exactly why the
// scrub inside failJob survived every mutation until this test existed. The
// hole it closes is the next early `return "", err` added between arming the
// context and that exit: Execute passes the raw text straight to failJob, and
// failJob writes it to the job row, the audit details and the log.
//
// So this drives failJob DIRECTLY with an armed context and unscrubbed text,
// which is the only way to exercise the line at all.
func TestFailJob_ScrubsAnArmedContextsCredential(t *testing.T) {
	var execs []execCall
	o := newCredTestOrchestrator(t, "http://127.0.0.1:1", &execs)

	// userID stays zero so failJob skips the audit insert: this test is about
	// the job row, and auditLog's own scrubbing rides on the same errMsg.
	mc := &migrationContext{job: db.MigrationJob{Vmid: credTestSourceVM, VmType: VMTypeQEMU}}
	mc.armEndpointScrubber(credTestTokenID + "=" + credTestSecret)

	raw := "remote migrate failed: apitoken=PVEAPIToken=" +
		credTestTokenID + "=" + credTestSecret + ",host=pve-01.example.com"
	if !strings.Contains(raw, credTestSecret) {
		t.Fatal("the fixture does not carry the secret, so this test would pass against anything")
	}

	o.failJob(context.Background(), uuid.New(), raw, mc)

	got := findExecArg(t, execs, "UPDATE migration_jobs", 3)
	if strings.Contains(got, credTestSecret) {
		t.Errorf("failJob wrote the token secret to migration_jobs.error_message: %q", got)
	}
	// Survival, without which a scrub returning "" would pass the line above.
	if !strings.Contains(got, "remote migrate failed") {
		t.Errorf("error_message = %q, want the failure text to survive the scrub", got)
	}
	if !strings.Contains(got, credTestTokenID) {
		t.Errorf("error_message = %q, want the token ID to survive; only the secret half is scrubbed", got)
	}
}
