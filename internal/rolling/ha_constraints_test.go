package rolling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// haStub serves the three HA listings, letting each one be replaced by a
// failure. A nil failure means "answer normally".
type haStub struct {
	resourcesErr, groupsErr, rulesErr *int // HTTP status to answer with, or nil
	resourcesBody, groupsBody         string
	rulesBody                         string
	// groupsMsg overrides the failure sentence, so a groups listing can fail
	// for a reason other than PVE 9's migration.
	groupsMsg string
}

func httpStatus(code int) *int { return &code }

func (s haStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fail := func(code *int, msg string) bool {
			if code == nil {
				return false
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(*code)
			payload, _ := json.Marshal(map[string]any{"data": nil, "message": msg})
			_, _ = w.Write(payload)
			return true
		}
		ok := func(body string) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}
		switch r.URL.Path {
		case "/api2/json/cluster/ha/resources":
			if fail(s.resourcesErr, "failed to read resources") {
				return
			}
			ok(s.resourcesBody)
		case "/api2/json/cluster/ha/groups":
			// PVE 9 soft-disables this once groups become rules.
			msg := s.groupsMsg
			if msg == "" {
				msg = "ha groups have been migrated to rules"
			}
			if fail(s.groupsErr, msg) {
				return
			}
			ok(s.groupsBody)
		case "/api2/json/cluster/ha/rules":
			// PVE before 9.0: the path is unrouted and the dispatcher answers.
			if fail(s.rulesErr, "Method 'GET /cluster/ha/rules' not implemented") {
				return
			}
			ok(s.rulesBody)
		default:
			http.NotFound(w, r)
		}
	}
}

// shortenListingRetryBackoff keeps the retry itself under test while removing the
// real sleeps: attempts stay at 3, so a test that expects a failure still proves
// the budget is exhausted rather than skipped.
func shortenListingRetryBackoff(t *testing.T) {
	t.Helper()
	prev := listingRetryBackoff
	listingRetryBackoff = time.Millisecond
	t.Cleanup(func() { listingRetryBackoff = prev })
}

func defaultHAStub() haStub {
	return haStub{
		resourcesBody: `{"data":[{"sid":"vm:100","state":"started"}]}`,
		groupsBody:    `{"data":[{"group":"g1","nodes":"pve-01"}]}`,
		rulesBody:     `{"data":[{"rule":"r1","type":"node-affinity","resources":"vm:100","nodes":"pve-01"}]}`,
	}
}

// TestLoadHAConstraints_DistinguishesNoneFromUnreadable is the whole point of
// the loader.
//
// SelectTarget scores an empty constraint set and an unread one identically —
// as "nothing applies to this guest" — so a failed listing used to place a
// guest as if no affinity rule existed. The two PVE versions that answer an
// error meaning "there are genuinely none" must still pass, or every rolling
// update on the releases either side of the groups-to-rules migration stops.
func TestLoadHAConstraints_DistinguishesNoneFromUnreadable(t *testing.T) {
	tests := []struct {
		name      string
		stub      func(haStub) haStub
		wantErr   string // substring; empty means success expected
		wantRules int
	}{
		{
			name:      "everything readable",
			stub:      func(s haStub) haStub { return s },
			wantRules: 1,
		},
		{
			// The row the non-nil-map assertion below exists for: with every
			// listing empty, a make() moved behind a len() > 0 guard would
			// hand callers nil maps to range over.
			name: "a cluster with no HA configured at all",
			stub: func(s haStub) haStub {
				s.resourcesBody = `{"data":[]}`
				s.groupsBody = `{"data":[]}`
				s.rulesBody = `{"data":[]}`
				return s
			},
			wantRules: 0,
		},
		{
			// PVE 9: groups are gone because they became rules. Not a failure.
			name: "groups migrated to rules is not a failure",
			stub: func(s haStub) haStub {
				s.groupsErr = httpStatus(http.StatusInternalServerError)
				return s
			},
			wantRules: 1,
		},
		{
			// PVE before 9.0: the dispatcher answers an unrouted path with 501.
			name: "a PVE answering 501 for rules is not a failure",
			stub: func(s haStub) haStub {
				s.rulesErr = httpStatus(http.StatusNotImplemented)
				return s
			},
			wantRules: 0,
		},
		{
			// A 404 is NOT PVE saying "no rules here" — no PVE answers 404 on
			// this path. It is what a reverse proxy in front of Nexara answers
			// for a rewrite bug, and admitting it would drain unconstrained.
			name: "a 404 on rules stops the job, because only a proxy sends one",
			stub: func(s haStub) haStub {
				s.rulesErr = httpStatus(http.StatusNotFound)
				return s
			},
			wantErr: "list HA rules",
		},
		{
			// The case that made the predicate status-based: the sentence says
			// the words but the status says the cluster could not answer, and
			// treating it as "no rules here" would drain a node unconstrained.
			name: "a 503 whose message says not implemented still stops the job",
			stub: func(s haStub) haStub {
				s.rulesErr = httpStatus(http.StatusServiceUnavailable)
				return s
			},
			wantErr: "list HA rules",
		},
		{
			name: "an unreadable resources listing stops the job",
			stub: func(s haStub) haStub {
				s.resourcesErr = httpStatus(http.StatusServiceUnavailable)
				return s
			},
			wantErr: "list HA resources",
		},
		{
			// The benign case is the migration sentence, not the status. Only
			// the rules listing has a version that legitimately 501s, so a 501
			// here is a failure like any other — without this, widening the
			// unsupported predicate to the groups call passes unnoticed.
			name: "a 501 on groups stops the job",
			stub: func(s haStub) haStub {
				s.groupsErr = httpStatus(http.StatusNotImplemented)
				s.groupsMsg = "Method 'GET /cluster/ha/groups' not implemented"
				return s
			},
			wantErr: "list HA groups",
		},
		{
			// Same for resources, which has no legitimately-empty version at
			// all: the HA stack ships with every PVE.
			name: "a 501 on resources stops the job",
			stub: func(s haStub) haStub {
				s.resourcesErr = httpStatus(http.StatusNotImplemented)
				return s
			},
			wantErr: "list HA resources",
		},
		{
			name: "a 404 on resources stops the job",
			stub: func(s haStub) haStub {
				s.resourcesErr = httpStatus(http.StatusNotFound)
				return s
			},
			wantErr: "list HA resources",
		},
		{
			// Only the migration sentence is benign; a groups endpoint that is
			// merely broken leaves the constraints unknown like any other.
			name: "a groups listing that failed for another reason stops the job",
			stub: func(s haStub) haStub {
				s.groupsErr = httpStatus(http.StatusServiceUnavailable)
				s.groupsMsg = "cluster is not quorate"
				return s
			},
			wantErr: "list HA groups",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shortenListingRetryBackoff(t)
			stub := tt.stub(defaultHAStub())
			_, client, closeStub := stubOrchestrator(t, stub.handler())
			defer closeStub()

			ha, err := loadHAConstraints(context.Background(), client, listingRetryAttempts)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("loadHAConstraints succeeded; want an error naming %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("err = %v, want it to name %q", err, tt.wantErr)
				}
				// Nothing partially-read may escape: a caller that ignored the
				// error would otherwise score against a half-built constraint
				// set, which is the failure this whole function prevents.
				if ha.resources != nil || ha.groups != nil || ha.rules != nil {
					t.Errorf("returned %+v alongside the error, want the zero value", ha)
				}
				return
			}
			if err != nil {
				t.Fatalf("loadHAConstraints: %v", err)
			}
			if len(ha.rules) != tt.wantRules {
				t.Errorf("rules = %d, want %d", len(ha.rules), tt.wantRules)
			}
			// Maps are always usable, so callers never have to nil-check.
			if ha.resources == nil || ha.groups == nil {
				t.Errorf("resources=%v groups=%v — both must be non-nil on success", ha.resources, ha.groups)
			}
		})
	}
}

// TestRetryHAListing_SurvivesABlip is why the retry exists at all.
//
// Stopping is expensive here: failNode fails the whole job, and a failed job is
// recreated rather than resumed. Without a retry, one dropped response during a
// multi-node rolling update costs the operator the entire job — so the listing
// has to fail repeatedly before it is believed. verifyNodeDrained takes the same
// three-attempt shape for the same reason, from the other direction.
func TestRetryHAListing_SurvivesABlip(t *testing.T) {
	shortenListingRetryBackoff(t)

	// Hardcoded, not derived from listingRetryAttempts: a stub that fails
	// "attempts-1 times" succeeds on the first call when attempts is 1, so the
	// test would pass with the retry deleted.
	const failuresBeforeSuccess = 2

	var calls atomic.Int32
	stub := defaultHAStub()
	_, client, closeStub := stubOrchestrator(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/cluster/ha/rules" && calls.Add(1) <= failuresBeforeSuccess {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"data":null,"message":"connection timed out"}`))
			return
		}
		stub.handler()(w, r)
	})
	defer closeStub()

	rules, err := listHARules(context.Background(), client, listingRetryAttempts)
	if err != nil {
		t.Fatalf("listHARules gave up after %d attempts: %v", calls.Load(), err)
	}
	if len(rules) != 1 {
		t.Errorf("rules = %d, want 1", len(rules))
	}
	if got := calls.Load(); got != failuresBeforeSuccess+1 {
		t.Errorf("attempts = %d, want %d — two failures must not be fatal", got, failuresBeforeSuccess+1)
	}
}

// TestRetryHAListing_StopsEarlyOnABenignError keeps the retry from burning its
// budget on an answer that cannot change: a PVE without the endpoint will say so
// three times just as fast as once, and the caller waits for it each time.
func TestRetryHAListing_StopsEarlyOnABenignError(t *testing.T) {
	shortenListingRetryBackoff(t)

	var calls atomic.Int32
	_, client, closeStub := stubOrchestrator(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"data":null,"message":"Method 'GET /cluster/ha/rules' not implemented"}`))
	})
	defer closeStub()

	rules, err := listHARules(context.Background(), client, listingRetryAttempts)
	if err != nil {
		t.Fatalf("listHARules: %v", err)
	}
	if rules != nil {
		t.Errorf("rules = %v, want nil", rules)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1 — a PVE without the endpoint is not a blip", got)
	}
}

// TestListHARules covers the shared decision directly, because both callers
// route through it: the drain's disable step and the target-selection loader.
// A regression here is a node drained with its affinity rules still armed.
func TestListHARules(t *testing.T) {
	tests := []struct {
		name      string
		rulesErr  *int
		wantErr   bool
		wantRules int
	}{
		{name: "rules listed", wantRules: 1},
		{name: "501 means this PVE has none", rulesErr: httpStatus(http.StatusNotImplemented)},
		{name: "404 is a proxy, not a PVE without rules", rulesErr: httpStatus(http.StatusNotFound), wantErr: true},
		{name: "503 means we could not look", rulesErr: httpStatus(http.StatusServiceUnavailable), wantErr: true},
		{name: "403 means we could not look", rulesErr: httpStatus(http.StatusForbidden), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shortenListingRetryBackoff(t)
			stub := defaultHAStub()
			stub.rulesErr = tt.rulesErr
			_, client, closeStub := stubOrchestrator(t, stub.handler())
			defer closeStub()

			rules, err := listHARules(context.Background(), client, listingRetryAttempts)
			if tt.wantErr {
				if err == nil {
					t.Fatal("listHARules succeeded; an unreadable listing must not read as an empty one")
				}
				if rules != nil {
					t.Errorf("rules = %v on failure, want nil so a caller cannot use them by accident", rules)
				}
				return
			}
			if err != nil {
				t.Fatalf("listHARules: %v", err)
			}
			if len(rules) != tt.wantRules {
				t.Errorf("rules = %d, want %d", len(rules), tt.wantRules)
			}
		})
	}
}

// TestInterrupted separates "this process stopped running the job" from "the
// cluster failed it". Getting it wrong either way is costly: false, and a
// leadership handover terminally fails a job nothing is wrong with; true, and a
// genuine cluster failure is silently ignored and the node stalls.
func TestInterrupted(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name     string
		shutdown context.Context
		err      error
		want     bool
	}{
		{
			// What retryHAListing returns from its own ctx checks. This is the
			// live path: api_client.go flattens a cancelled HTTP call into text,
			// so errors.Is cannot see it through a Proxmox error.
			name:     "a cancelled context is a handover, not a failure",
			shutdown: context.Background(),
			err:      context.Canceled,
			want:     true,
		},
		{
			name:     "a shutdown in progress is not a failure either",
			shutdown: cancelled,
			err:      errors.New("list HA rules: proxmox API error 503"),
			want:     true,
		},
		{
			name:     "an ordinary cluster failure is a failure",
			shutdown: context.Background(),
			err:      errors.New("list HA rules: proxmox API error 503"),
			want:     false,
		},
		{
			// A deadline is the request giving up, not the process losing the
			// job — it must still fail the node rather than stall it.
			name:     "a deadline is not a handover",
			shutdown: context.Background(),
			err:      context.DeadlineExceeded,
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := NewOrchestrator(tt.shutdown, nil, "", slog.Default(), nil, nil)
			if got := o.interrupted(tt.err); got != tt.want {
				t.Errorf("interrupted(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestRetryHAListing_ReportsCancellationAsCancellation pins that a cancelled
// context surfaces AS a cancellation rather than as the cluster's own error —
// which is what lets o.interrupted hand the job over instead of failing it.
//
// It does not isolate the backoff select: with the select replaced by a plain
// sleep this still passes, because the next call aborts on the cancelled ctx
// before reaching the network and the post-call check answers. That is the
// equivalence recorded on retryHAListing itself; the two paths are one
// behaviour with two entry points.
func TestRetryHAListing_ReportsCancellationAsCancellation(t *testing.T) {
	shortenListingRetryBackoff(t)

	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	_, client, closeStub := stubOrchestrator(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			cancel() // the handover lands between attempt one and attempt two
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"data":null,"message":"cluster is not quorate"}`))
	})
	defer closeStub()

	_, err := listHARules(ctx, client, listingRetryAttempts)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled so the caller can tell a handover from a cluster failure", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1 — a cancelled context must stop the retry, not sleep through it", got)
	}
}

// TestRetryHAListing_RefusesAZeroBudget covers a fail-open inside the function
// whose whole purpose is not to fail open: a zero budget ran no attempts and
// returned (zero, nil) — success, with nothing read.
func TestRetryHAListing_RefusesAZeroBudget(t *testing.T) {
	shortenListingRetryBackoff(t)

	var calls atomic.Int32
	_, client, closeStub := stubOrchestrator(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"data":null,"message":"cluster is not quorate"}`))
	})
	defer closeStub()

	if _, err := listHARules(context.Background(), client, 0); err == nil {
		t.Fatal("a zero budget reported success without reading anything")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1 — the budget is clamped up, not down to nothing", got)
	}
}

// TestRetryHAListing_CancellationOnTheFinalAttempt covers the check after the
// call, not the one in the backoff.
//
// A cancellation during the last attempt never reaches the backoff select, so
// without the post-call ctx check the function returns the cluster's own error.
// o.interrupted cannot see context.Canceled through it — api_client.go flattens
// the transport error with %s — so a job that had merely lost leadership would
// be terminally failed.
func TestRetryHAListing_CancellationOnTheFinalAttempt(t *testing.T) {
	shortenListingRetryBackoff(t)

	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	_, client, closeStub := stubOrchestrator(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == int32(listingRetryAttempts) {
			cancel() // only on the last attempt: the backoff select is already behind us
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"data":null,"message":"cluster is not quorate"}`))
	})
	defer closeStub()

	_, err := listHARules(ctx, client, listingRetryAttempts)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled; the job would be failed instead of handed over", err)
	}
	if got := calls.Load(); got != int32(listingRetryAttempts) {
		t.Errorf("attempts = %d, want %d", got, listingRetryAttempts)
	}
}

// TestRetryHAListing_HonoursASingleAttempt pins the parameter the request path
// relies on: the cached Proxmox client's timeout is minutes, so a pre-flight
// that retried three times would triple how long an HTTP request can hang.
func TestRetryHAListing_HonoursASingleAttempt(t *testing.T) {
	shortenListingRetryBackoff(t)

	var calls atomic.Int32
	_, client, closeStub := stubOrchestrator(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"data":null,"message":"cluster is not quorate"}`))
	})
	defer closeStub()

	if _, err := listHARules(context.Background(), client, listingRetryOnce); err == nil {
		t.Fatal("listHARules succeeded against a failing stub")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1", got)
	}
}

// TestAnalyzeHAConstraints_PropagatesAnUnreadableListing covers the gap that
// let the first attempt at this fix ship broken.
//
// loadHAConstraints was well tested; nothing proved the pre-flight actually
// surfaced its error. The report feeds haPolicy == "strict", so a swallowed
// error means "no conflicts found" — the gate whose job is to refuse a risky
// job answering all-clear because it could not look. A review caught that the
// error was still being dropped one layer up; this is the test that would have.
func TestAnalyzeHAConstraints_PropagatesAnUnreadableListing(t *testing.T) {
	shortenListingRetryBackoff(t)

	stub := defaultHAStub()
	stub.rulesErr = httpStatus(http.StatusServiceUnavailable)
	var ruleCalls atomic.Int32
	_, client, closeStub := stubOrchestrator(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/cluster/ha/rules" {
			ruleCalls.Add(1)
		}
		stub.handler()(w, r)
	})
	defer closeStub()

	report, err := AnalyzeHAConstraints(context.Background(), client, nil, uuid.New(), []string{"pve-01"})
	// One attempt, not the orchestrator's three: this runs inside an HTTP
	// request against a client whose timeout is minutes, so retrying would
	// multiply how long the pre-flight can hang before it answers.
	if got := ruleCalls.Load(); got != 1 {
		t.Errorf("rules listed %d times, want 1 — the pre-flight is using the orchestrator's retry budget", got)
	}
	if err == nil {
		t.Fatal("AnalyzeHAConstraints reported success on an unreadable rules listing; the strict gate would pass")
	}
	if !strings.Contains(err.Error(), "list HA rules") {
		t.Errorf("err = %v, want it to name the listing that failed", err)
	}
	if report != nil {
		t.Errorf("report = %+v alongside the error; a half-built pre-flight must not read as a complete one", report)
	}
}

// TestIsHARulesUnsupportedError keeps the "there are none" predicate from
// widening into "the listing failed", which is the distinction the loader is
// built on.
func TestIsHARulesUnsupportedError(t *testing.T) {
	unsupported := []error{
		&proxmox.APIError{StatusCode: http.StatusNotImplemented, Message: "Method 'GET /cluster/ha/rules' not implemented"},
		fmt.Errorf("get HA rules: %w", &proxmox.APIError{StatusCode: http.StatusNotImplemented, Message: "not implemented"}),
	}
	for _, err := range unsupported {
		if !proxmox.IsHARulesUnsupportedError(err) {
			t.Errorf("IsHARulesUnsupportedError(%v) = false, want true", err)
		}
	}

	readable := []error{
		nil,
		fmt.Errorf("get HA rules: %w", proxmox.ErrConnectionFailed),
		&proxmox.APIError{StatusCode: http.StatusServiceUnavailable, Message: "cluster is not quorate"},
		&proxmox.APIError{StatusCode: http.StatusForbidden, Message: "permission denied"},
		// The whole reason this is status-based: the sentence says the words,
		// the status says the cluster could not answer.
		&proxmox.APIError{StatusCode: http.StatusServiceUnavailable, Message: "Method 'GET /cluster/ha/rules' not implemented"},
		// A 404 reaches us as a bare sentinel with the body discarded, so it
		// cannot be told from a reverse proxy's 404. No PVE sends one here.
		proxmox.ErrNotFound,
		fmt.Errorf("get HA rules: %w", proxmox.ErrNotFound),
		// The sibling predicate's sentence must not be swallowed by this one.
		&proxmox.APIError{StatusCode: http.StatusInternalServerError, Message: "ha groups have been migrated to rules"},
	}
	for _, err := range readable {
		if proxmox.IsHARulesUnsupportedError(err) {
			t.Errorf("IsHARulesUnsupportedError(%v) = true, want false — an unreadable listing would be treated as an empty one", err)
		}
	}
}
