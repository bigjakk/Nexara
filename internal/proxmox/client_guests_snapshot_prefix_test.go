package proxmox

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// --- Timestamped snapshot names ---
//
// The rule a scheduled snapshot's name follows: a prefix, then the run's date
// and time in UTC. internal/scheduler composes the name with
// TimestampedSnapshotName and internal/api/handlers checks a stored prefix
// with ValidateSnapshotNamePrefix, so these tests are what both of them rest
// on.
//
// Every zone below other than UTC is a synthetic fixed zone, named "synthetic":
// the tests need a clock that is not UTC but is not a place either, and no zone
// in use today is 30 or 90 minutes off UTC.

// TestMain runs the WHOLE package with the local clock in one of those
// synthetic zones, 90 minutes east of UTC. The scheduler hands
// TimestampedSnapshotName the server's local time, and on a host whose local
// zone is UTC — CI's, and this image's by default — a name formatted on that
// clock reads exactly like a UTC one, so no test here could tell
// at.Local().Format from at.UTC().Format. It is set before m.Run, so before any
// test, or any goroutine a test starts, reads it.
func TestMain(m *testing.M) {
	time.Local = time.FixedZone("synthetic", 90*60)
	m.Run()
}

// prefixTestInstants are the instants the verdict must not depend on: the zero
// time ValidateSnapshotNamePrefix composes with, an ordinary run, and the last
// second a four-digit year can print — once in UTC, and once as a run's start
// arrives from a server whose clock is east of UTC, where that second's own
// wall clock already reads the year 10000. Printed in that zone, the name
// would gain a digit and outgrow the budget; printed in UTC, it cannot.
var prefixTestInstants = []time.Time{
	{},
	time.Date(2026, time.September, 26, 2, 0, 0, 0, time.UTC),
	time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC),
	time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC).In(time.FixedZone("synthetic", 90*60)),
}

func TestTimestampedSnapshotName(t *testing.T) {
	// A name formatted on the local clock reads exactly like a UTC one while
	// that clock is UTC, so the tests in this file need TestMain's zone.
	if _, offset := time.Now().Zone(); offset == 0 {
		t.Fatalf("the package's local clock (%v) sits at UTC's offset, so nothing read on it can be told "+
			"from UTC; TestMain must put it off UTC", time.Local)
	}

	// An afternoon instant, in UTC as well, so a 12-hour layout ("03" for
	// "15") cannot pass: it would name a 02:00 and a 14:00 run of a
	// twice-daily schedule alike, and the second would fail "already used".
	// Handed over in a zone of its own, as the scheduler hands over the
	// server's local time, so the name is shown to be converted to UTC rather
	// than follow at's location: 14:30:05 at +01:30 is 13:00:05 UTC.
	at := time.Date(2026, time.September, 26, 14, 30, 5, 0, time.FixedZone("synthetic", 90*60))
	if got, want := TimestampedSnapshotName("nightly", at), "nightly-20260926-130005"; got != want {
		t.Errorf("TimestampedSnapshotName(nightly, %s) = %q, want %q", at, got, want)
	}
	// It composes and nothing else. The cut for an over-long stored prefix is
	// the scheduler's, and a shortening here would let
	// ValidateSnapshotNamePrefix wave through a prefix longer than the budget.
	long := strings.Repeat("a", SnapshotNamePrefixMaxLen+1)
	if got := TimestampedSnapshotName(long, at); !strings.HasPrefix(got, long+"-") {
		t.Errorf("TimestampedSnapshotName shortened a %d-character prefix to %q", len(long), got)
	}
}

// TestTimestampedSnapshotNameSurvivesAFallBack is why a name is in UTC. Where
// the server's zone has daylight saving, the autumn fall-back moves its offset
// back an hour, so one hour of wall clock is shown twice, and the scheduler's
// cron, which runs on that clock, fires the repeated time again. Named from the
// wall clock, the second run could ask for the name the first had taken an
// hour before, and its task fail "already used". It did whenever the two runs
// started in the same second, which the scheduler's steady one-minute tick
// makes likely, and that is the case here: the two instants share their second.
//
// Two synthetic fixed zones an hour apart stand in for one zone's summer and
// winter offsets, so no real zone is named.
func TestTimestampedSnapshotNameSurvivesAFallBack(t *testing.T) {
	summer := time.FixedZone("synthetic", 90*60)
	winter := time.FixedZone("synthetic", 30*60)
	first := time.Date(2026, time.September, 26, 2, 30, 0, 0, summer)
	repeat := time.Date(2026, time.September, 26, 2, 30, 0, 0, winter)

	// The fixture has to BE a repeat, or the assertion below cannot fail: the
	// same wall clock, an hour later.
	if first.Format(snapshotNameTimestampLayout) != repeat.Format(snapshotNameTimestampLayout) ||
		repeat.Sub(first) != time.Hour {
		t.Fatalf("%s and %s are not the two passes through one repeated hour", first, repeat)
	}

	firstName, repeatName := TimestampedSnapshotName("nightly", first), TimestampedSnapshotName("nightly", repeat)
	if firstName == repeatName {
		t.Fatalf("both passes through the repeated hour are named %q, so the second run's task fails "+
			"with \"snapshot name '%s' already used\"", firstName, firstName)
	}
	// Each is its own instant in UTC: 02:30 at +01:30 and 02:30 at +00:30.
	if want := "nightly-20260926-010000"; firstName != want {
		t.Errorf("the first pass is named %q, want %q", firstName, want)
	}
	if want := "nightly-20260926-020000"; repeatName != want {
		t.Errorf("the repeat is named %q, want %q", repeatName, want)
	}
}

// TestSnapshotNamePrefixMaxLenFillsTheCap pins the budget from both sides: a
// prefix of exactly SnapshotNamePrefixMaxLen composes a name of exactly
// SnapshotMaxNameLen, at every instant. Asserting 24 alone would hold even if
// the layout changed width underneath it.
func TestSnapshotNamePrefixMaxLenFillsTheCap(t *testing.T) {
	if SnapshotNamePrefixMaxLen != 24 {
		t.Errorf("SnapshotNamePrefixMaxLen = %d, want 24: 40, less \"-\" and a 15-character YYYYMMDD-HHMMSS",
			SnapshotNamePrefixMaxLen)
	}
	prefix := strings.Repeat("a", SnapshotNamePrefixMaxLen)
	for _, at := range prefixTestInstants {
		if got := len(TimestampedSnapshotName(prefix, at)); got != SnapshotMaxNameLen {
			t.Errorf("a %d-character prefix at %s composes a %d-character name, want exactly %d",
				len(prefix), at, got, SnapshotMaxNameLen)
		}
	}
}

func TestValidateSnapshotNamePrefix(t *testing.T) {
	kinds := []SnapshotGuestKind{QemuSnapshot, LXCSnapshot}

	accepted := []struct {
		prefix string
		why    string
	}{
		{"nightly", "an ordinary prefix"},
		{"a", "one letter: Proxmox's two-character minimum is on the whole name"},
		{"auto", "the scheduler's own default prefix"},
		{strings.Repeat("a", SnapshotNamePrefixMaxLen), "exactly the budget"},
		// The reserved words, for both kinds. Proxmox reserves the WHOLE name,
		// and "current-20260926-020000" is not "current". Refusing these would
		// be a rule Proxmox does not have — the reserved-name check belongs on
		// the final name, and the final name carries a date.
		{"current", "reserved only as a whole name"},
		{"pending", "reserved for a VM only as a whole name"},
		{"PENDING", "reserved for a VM only as a whole name, in any case"},
		{"vzdump", "reserved for a container only as a whole name"},
	}
	for _, kind := range kinds {
		for _, tt := range accepted {
			if err := ValidateSnapshotNamePrefix(kind, tt.prefix); err != nil {
				t.Errorf("ValidateSnapshotNamePrefix(%s, %q) = %v, want nil (%s)", kind, tt.prefix, err, tt.why)
			}
		}
	}

	refused := []struct {
		prefix string
		why    string
	}{
		{strings.Repeat("a", SnapshotNamePrefixMaxLen+1), "one over the budget composes a 41-character name"},
		{strings.Repeat("a", SnapshotMaxNameLen), "the old whole-name maximum no longer fits"},
		{"", "the scheduler reads empty as \"use auto\"; as a prefix it starts the name with a dash"},
		{"my snap", "a space"},
		{"nightly.1", "a dot"},
		{"1nightly", "a leading digit"},
		{"-nightly", "a leading dash"},
		{"_nightly", "a leading underscore"},
	}
	for _, kind := range kinds {
		for _, tt := range refused {
			err := ValidateSnapshotNamePrefix(kind, tt.prefix)
			if err == nil {
				t.Errorf("ValidateSnapshotNamePrefix(%s, %q) = nil, want a refusal (%s)", kind, tt.prefix, tt.why)
				continue
			}
			// ErrInvalidInput is the client's mark for a caller mistake
			// (mapProxmoxError answers it with a 400), and it keeps this
			// refusal apart from the unknown-kind one below, which
			// handlers.snapshotRuleError — applied to this function's
			// result in validateSnapshotScheduleParams — answers with a 500
			// rather than a 400.
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("ValidateSnapshotNamePrefix(%s, %q) = %v, want it to wrap ErrInvalidInput",
					kind, tt.prefix, err)
			}
			// The message must describe a PREFIX and its budget. The whole-name
			// rule's "2-40 characters" would send a caller whose 30-character
			// prefix was refused after the wrong limit.
			for _, phrase := range []string{"prefix", "at most 24 characters"} {
				if !strings.Contains(err.Error(), phrase) {
					t.Errorf("ValidateSnapshotNamePrefix(%s, %q) = %q, want it to say %q",
						kind, tt.prefix, err, phrase)
				}
			}
		}
	}
}

// TestValidateSnapshotNamePrefixAgreesWithTheFinalName pins what the prefix
// check is FOR: its verdict is ValidateSnapshotName's verdict on the name a run
// will actually send, whatever instant that run happens at. If the two ever
// disagree, the form refuses a schedule whose every snapshot Proxmox would take,
// or accepts one whose every snapshot the client refuses on each fire.
func TestValidateSnapshotNamePrefixAgreesWithTheFinalName(t *testing.T) {
	prefixes := []string{
		"a", "ab", "nightly", "auto", "current", "Current", "pending", "Pending", "vzdump",
		"x_y-z", "Z9", strings.Repeat("a", SnapshotNamePrefixMaxLen), strings.Repeat("a", SnapshotNamePrefixMaxLen+1),
		"", " ", "my snap", "a.b", "a/b", "1a", "-a", "_a", "é",
	}
	for _, kind := range []SnapshotGuestKind{QemuSnapshot, LXCSnapshot} {
		for _, prefix := range prefixes {
			prefixOK := ValidateSnapshotNamePrefix(kind, prefix) == nil
			for _, at := range prefixTestInstants {
				name := TimestampedSnapshotName(prefix, at)
				if nameOK := ValidateSnapshotName(kind, name) == nil; nameOK != prefixOK {
					t.Errorf("%s prefix %q: the prefix check says ok=%v but the name a run at %s sends, %q, "+
						"is ok=%v", kind, prefix, prefixOK, at, name, nameOK)
				}
			}
		}
	}
}

// TestValidateSnapshotNamePrefixUnknownKindRefuses is the third outcome, and
// why the final name is checked through ValidateSnapshotName rather than by a
// shape rule of the prefix's own: that is where an unregistered kind is caught.
// It must stay distinguishable from a bad prefix, since the API layer answers
// the two differently (handlers.snapshotRuleError).
func TestValidateSnapshotNamePrefixUnknownKindRefuses(t *testing.T) {
	err := ValidateSnapshotNamePrefix("not-a-guest-kind", "nightly")
	if !errors.Is(err, ErrUnknownSnapshotGuestKind) {
		t.Errorf("err = %v, want it to wrap ErrUnknownSnapshotGuestKind", err)
	}
	if errors.Is(err, ErrInvalidInput) {
		t.Errorf("err = %v, want it NOT to wrap ErrInvalidInput — that would bill our own "+
			"misconfiguration to the caller as a 400", err)
	}
}
