package proxmox

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The rule a scheduled snapshot's name follows: a prefix, then the run's date and
// time in UTC. internal/scheduler composes the name with TimestampedSnapshotName
// and internal/api/handlers checks a stored prefix with ValidateSnapshotNamePrefix,
// so these tests are what both rest on. Every zone below other than UTC is a
// synthetic fixed zone: no zone in use today is 30 or 90 minutes off UTC.

// TestMain runs the WHOLE package with the local clock 90 minutes east of UTC. The
// scheduler hands TimestampedSnapshotName the server's local time, and on a host
// whose zone is UTC (CI's) a name formatted on that clock reads like a UTC one, so
// no test could tell at.Local().Format from at.UTC().Format. Set before m.Run, so
// before any test or any goroutine a test starts reads it.
func TestMain(m *testing.M) {
	time.Local = time.FixedZone("synthetic", 90*60)
	m.Run()
}

// prefixTestInstants are the instants the verdict must not depend on: the zero
// time ValidateSnapshotNamePrefix composes with, an ordinary run, and the last
// second a four-digit year can print — in UTC, and as a run's start arrives from a
// server east of UTC, where that second's wall clock already reads year 10000 and
// the name would gain a digit and outgrow the budget if it were not printed in UTC.
var prefixTestInstants = []time.Time{
	{},
	time.Date(2026, time.September, 26, 2, 0, 0, 0, time.UTC),
	time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC),
	time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC).In(time.FixedZone("synthetic", 90*60)),
}

func TestTimestampedSnapshotName(t *testing.T) {
	if _, offset := time.Now().Zone(); offset == 0 {
		t.Fatalf("the package's local clock (%v) sits at UTC's offset, so nothing read on it can be told "+
			"from UTC; TestMain must put it off UTC", time.Local)
	}
	// An afternoon instant (a 12-hour layout would name a 02:00 and a 14:00 run alike)
	// in a zone of its own, so the name is shown to be converted to UTC rather than
	// follow at's location: 14:30:05 at +01:30 is 13:00:05 UTC.
	at := time.Date(2026, time.September, 26, 14, 30, 5, 0, time.FixedZone("synthetic", 90*60))
	if got, want := TimestampedSnapshotName("nightly", at), "nightly-20260926-130005"; got != want {
		t.Errorf("TimestampedSnapshotName(nightly, %s) = %q, want %q", at, got, want)
	}
	// It composes and nothing else: the cut for an over-long prefix is the scheduler's,
	// and a shortening here would let ValidateSnapshotNamePrefix wave one through.
	long := strings.Repeat("a", SnapshotNamePrefixMaxLen+1)
	if got := TimestampedSnapshotName(long, at); !strings.HasPrefix(got, long+"-") {
		t.Errorf("TimestampedSnapshotName shortened a %d-character prefix to %q", len(long), got)
	}
}

// TestTimestampedSnapshotNameSurvivesAFallBack is why a name is in UTC. Where the
// server's zone has daylight saving, the autumn fall-back shows one hour of wall
// clock twice, and the scheduler's cron fires the repeated time again; named from
// the wall clock, the second run asked for the name the first had taken an hour
// before and its task failed "already used". Two fixed zones an hour apart stand in
// for one zone's summer and winter offsets.
func TestTimestampedSnapshotNameSurvivesAFallBack(t *testing.T) {
	summer := time.FixedZone("synthetic", 90*60)
	winter := time.FixedZone("synthetic", 30*60)
	first := time.Date(2026, time.September, 26, 2, 30, 0, 0, summer)
	repeat := time.Date(2026, time.September, 26, 2, 30, 0, 0, winter)

	// The fixture has to BE a repeat, or the assertion below cannot fail.
	if first.Format(snapshotNameTimestampLayout) != repeat.Format(snapshotNameTimestampLayout) || repeat.Sub(first) != time.Hour {
		t.Fatalf("%s and %s are not the two passes through one repeated hour", first, repeat)
	}
	firstName, repeatName := TimestampedSnapshotName("nightly", first), TimestampedSnapshotName("nightly", repeat)
	if firstName == repeatName {
		t.Fatalf("both passes through the repeated hour are named %q, so the second run's task fails with "+
			"\"snapshot name '%s' already used\"", firstName, firstName)
	}
	// Each is its own instant in UTC: 02:30 at +01:30 and 02:30 at +00:30.
	if want := "nightly-20260926-010000"; firstName != want {
		t.Errorf("the first pass is named %q, want %q", firstName, want)
	}
	if want := "nightly-20260926-020000"; repeatName != want {
		t.Errorf("the repeat is named %q, want %q", repeatName, want)
	}
}

// TestSnapshotNamePrefixMaxLenFillsTheCap pins the budget from both sides: a prefix
// of exactly SnapshotNamePrefixMaxLen composes a name of exactly SnapshotMaxNameLen,
// at every instant. Asserting 24 alone would hold if the layout changed width.
func TestSnapshotNamePrefixMaxLenFillsTheCap(t *testing.T) {
	if SnapshotNamePrefixMaxLen != 24 {
		t.Errorf("SnapshotNamePrefixMaxLen = %d, want 24: 40, less \"-\" and a 15-character YYYYMMDD-HHMMSS", SnapshotNamePrefixMaxLen)
	}
	prefix := strings.Repeat("a", SnapshotNamePrefixMaxLen)
	for _, at := range prefixTestInstants {
		if got := len(TimestampedSnapshotName(prefix, at)); got != SnapshotMaxNameLen {
			t.Errorf("a %d-character prefix at %s composes a %d-character name, want exactly %d", len(prefix), at, got, SnapshotMaxNameLen)
		}
	}
}

func TestValidateSnapshotNamePrefix(t *testing.T) {
	kinds := []SnapshotGuestKind{QemuSnapshot, LXCSnapshot}

	// Proxmox reserves the reserved words as a WHOLE name, and "current-20260926-020000"
	// is not "current": refusing them would be a rule Proxmox does not have.
	accepted := []struct{ prefix, why string }{
		{"nightly", "an ordinary prefix"},
		{"a", "one letter: Proxmox's two-character minimum is on the whole name"},
		{"auto", "the scheduler's own default prefix"},
		{strings.Repeat("a", SnapshotNamePrefixMaxLen), "exactly the budget"},
		{"current", "reserved only as a whole name"},
		{"pending", "reserved for a VM only as a whole name"},
		{"PENDING", "reserved for a VM only as a whole name, in any case"},
		{"vzdump", "reserved for a container only as a whole name"},
	}
	refused := []struct{ prefix, why string }{
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
		for _, tt := range accepted {
			if err := ValidateSnapshotNamePrefix(kind, tt.prefix); err != nil {
				t.Errorf("ValidateSnapshotNamePrefix(%s, %q) = %v, want nil (%s)", kind, tt.prefix, err, tt.why)
			}
		}
		for _, tt := range refused {
			err := ValidateSnapshotNamePrefix(kind, tt.prefix)
			if err == nil {
				t.Errorf("ValidateSnapshotNamePrefix(%s, %q) = nil, want a refusal (%s)", kind, tt.prefix, tt.why)
				continue
			}
			// ErrInvalidInput marks a caller mistake (a 400) and keeps this refusal apart
			// from the unknown-kind one below, which handlers.snapshotRuleError answers 500.
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("ValidateSnapshotNamePrefix(%s, %q) = %v, want it to wrap ErrInvalidInput", kind, tt.prefix, err)
			}
			// The message must describe a PREFIX and its budget: the whole-name rule's
			// "2-40 characters" would send a caller refused at 30 characters after the wrong limit.
			for _, phrase := range []string{"prefix", "at most 24 characters"} {
				if !strings.Contains(err.Error(), phrase) {
					t.Errorf("ValidateSnapshotNamePrefix(%s, %q) = %q, want it to say %q", kind, tt.prefix, err, phrase)
				}
			}
		}
	}
}

// TestValidateSnapshotNamePrefixAgreesWithTheFinalName pins what the prefix check
// is FOR: its verdict is ValidateSnapshotName's verdict on the name a run will send,
// whatever instant that run happens at. If they disagree, the form refuses a
// schedule whose every snapshot Proxmox would take, or accepts one whose every
// snapshot the client refuses on each fire.
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
					t.Errorf("%s prefix %q: the prefix check says ok=%v but the name a run at %s sends, %q, is ok=%v",
						kind, prefix, prefixOK, at, name, nameOK)
				}
			}
		}
	}
}

// TestValidateSnapshotNamePrefixUnknownKindRefuses is the third outcome, and why the
// final name is checked through ValidateSnapshotName and not by a shape rule of the
// prefix's own: that is where an unregistered kind is caught. It must stay
// distinguishable from a bad prefix, which the API layer answers differently.
func TestValidateSnapshotNamePrefixUnknownKindRefuses(t *testing.T) {
	err := ValidateSnapshotNamePrefix("not-a-guest-kind", "nightly")
	if !errors.Is(err, ErrUnknownSnapshotGuestKind) {
		t.Errorf("err = %v, want it to wrap ErrUnknownSnapshotGuestKind", err)
	}
	if errors.Is(err, ErrInvalidInput) {
		t.Errorf("err = %v, want it NOT to wrap ErrInvalidInput — that would bill our own misconfiguration to the caller as a 400", err)
	}
}
