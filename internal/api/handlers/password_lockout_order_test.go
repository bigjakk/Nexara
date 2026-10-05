package handlers

import (
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/auth"
)

// Two properties of the change-password lockout that only requests IN FLIGHT at the
// same time can show, driven without a clock: the check of a password is held where
// it starts, through the seam that stands in for auth.CheckPassword, until the test
// lets it go, so what is in flight when is a fact of the test and not of how long a
// bcrypt takes on the machine it runs on.

// changeAsync posts a change-password request as the harness's user from a
// goroutine of its own, and reports its status — -1 if it never answered — on the
// returned channel. It never touches t from that goroutine.
func (a *authRaceApp) changeAsync(body string) <-chan int {
	out := make(chan int, 1)
	userID := a.store.user.ID.String()
	go func() {
		resp, err := a.app.Test(raceRequest("/auth/change-password", body, "", map[string]string{"X-Test-Acting-User": userID}),
			fiber.TestConfig{Timeout: 60 * time.Second, FailOnTimeout: true})
		if err != nil {
			out <- -1
			return
		}
		defer func() { _ = resp.Body.Close() }()
		out <- resp.StatusCode
	}()
	return out
}

// gatedChecks holds every check of a password the handler makes whose password
// match(password) says to, at its start, until open is called. entered receives one
// value per check that has begun. open is also registered for the end of the test, so
// that a test that fails early does not leave requests blocked.
func gatedChecks(t *testing.T, a *authRaceApp, match func(password string) bool) (entered <-chan struct{}, open func()) {
	t.Helper()
	in := make(chan struct{}, 4*passwordLockoutThreshold)
	gate := make(chan struct{})
	var once sync.Once
	open = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(open)
	a.handler.passwordChecker = func(hash, password string) error {
		if match(password) {
			in <- struct{}{}
			<-gate
		}
		return auth.CheckPassword(hash, password)
	}
	return in, open
}

// awaitStatus waits for one answer and reports it, failing the test when none comes.
func awaitStatus(t *testing.T, what string, answer <-chan int, within time.Duration) int {
	t.Helper()
	select {
	case status := <-answer:
		return status
	case <-time.After(within):
		t.Fatalf("%s was not answered within %v", what, within)
		return 0
	}
}

// TestChangePassword_TheLockExistsWhileEveryGuessIsStillBeingChecked is the property
// "count before checking" is for, shown directly and not through its side effects.
// The budget's worth of wrong guesses arrive together and every check is held at its
// start. By the time the last of them has begun its check it has been admitted, and
// the admission of the last one is what arms the lock — so the lock is in place
// while all five checks are still in progress, and a password that arrives NOW is
// refused unchecked, the right one included.
//
// An implementation that checks first and counts a failure afterwards cannot pass
// it, however it is arranged — it may read the lock before the check and count after
// it — because until a check has failed nothing has been counted: its lock appears
// only when a guess has been answered, and the right password that arrives while the
// guesses are being checked is checked as well, and waits in the held check here
// instead of being refused. The answers to a burst of wrong guesses cannot tell the
// two apart (both are four 403s and the rest 429s), which is why the test looks at
// what is in flight and not at what was answered.
func TestChangePassword_TheLockExistsWhileEveryGuessIsStillBeingChecked(t *testing.T) {
	for _, kind := range []string{"redis", "memory"} {
		t.Run(kind, func(t *testing.T) {
			a, _ := lockoutApp(t, kind, raceOptions{concurrent: true})
			entered, open := gatedChecks(t, a, func(string) bool { return true })

			guesses := make([]<-chan int, passwordLockoutThreshold)
			for i := range guesses {
				guesses[i] = a.changeAsync(wrongChangeBody)
			}
			for begun := 0; begun < passwordLockoutThreshold; begun++ {
				select {
				case <-entered:
				case <-time.After(30 * time.Second):
					t.Fatalf("%d of the %d guesses had begun their check after 30 seconds", begun, passwordLockoutThreshold)
				}
			}
			// Every guess is admitted, none has been checked, so none has been answered:
			// whatever the lock is now, no failure armed it.
			for i, answer := range guesses {
				select {
				case status := <-answer:
					t.Fatalf("guess %d was answered %d while its check was held", i+1, status)
				default:
				}
			}
			if kind == "redis" && !a.redis.Exists(passwordLockKey(a.store.user.ID)) {
				t.Error("there is no lock in Redis while the budget's worth of guesses is being checked: the lock was not armed by the last admission")
			}

			// The right password, with a new one that is refused as too weak, arrives now.
			probe := a.changeAsync(weakChangeBody)
			select {
			case status := <-probe:
				if status != http.StatusTooManyRequests {
					t.Errorf("the right password while the budget was spent = %d, want 429", status)
				}
			case <-time.After(10 * time.Second):
				t.Error("the right password was not answered while the budget's worth of guesses was being checked: it is waiting in the check itself, " +
					"so a password that arrives while the guesses are in flight is checked too — the lock was not in place before the checks began")
			}
			if len(entered) != 0 {
				t.Errorf("%d more checks began after the budget was spent: a request the lock refuses must not be checked", len(entered))
			}

			open()
			counts := map[int]int{}
			for _, answer := range guesses {
				counts[awaitStatus(t, "a guess", answer, 30*time.Second)]++
			}
			if counts[http.StatusForbidden] != passwordLockoutThreshold-1 || counts[http.StatusTooManyRequests] != 1 {
				t.Errorf("the guesses were answered %v, want %d x 403 and the one 429 that armed the lock", counts, passwordLockoutThreshold-1)
			}
			if got := a.store.auditActions(); !slices.Equal(got, []string{"password_change_locked"}) {
				t.Errorf("audit actions = %v, want exactly one lockout row", got)
			}
		})
	}
}

// TestChangePassword_AVerifiedRequestLeavesALockSomeoneElseArmed: the owner's request
// has the right current password and a new one that is refused, so it hands its
// attempt back — and while its check is in progress four guesses arrive, taking the
// next four attempts, the fifth of which arms the lock. The owner's request must not
// undo that lock. What it took was one attempt of the count, which the lock has
// since replaced; it hands back that attempt and nothing else, so the lock the audit
// row announced stays in force, and the next guess is refused.
//
// A hand-back that deleted the account's keys would lift the lock the guesses earned
// the moment the owner's request ended.
func TestChangePassword_AVerifiedRequestLeavesALockSomeoneElseArmed(t *testing.T) {
	for _, kind := range []string{"redis", "memory"} {
		t.Run(kind, func(t *testing.T) {
			a, _ := lockoutApp(t, kind, raceOptions{concurrent: true})
			// Only the owner's check — the one with the right password — is held.
			entered, open := gatedChecks(t, a, func(password string) bool { return password == racePassword })

			owner := a.changeAsync(weakChangeBody)
			select {
			case <-entered:
			case <-time.After(30 * time.Second):
				t.Fatal("the owner's request never reached the check of its password")
			}

			// Four guesses, one after the other, while the owner's check is still held:
			// they are attempts 2 to 5, and the fifth arms the lock.
			guesses := make([]int, 0, passwordLockoutThreshold-1)
			for range passwordLockoutThreshold - 1 {
				status, _, _ := a.change(t, wrongChangeBody)
				guesses = append(guesses, status)
			}
			if want := []int{http.StatusForbidden, http.StatusForbidden, http.StatusForbidden, http.StatusTooManyRequests}; !slices.Equal(guesses, want) {
				t.Fatalf("the four guesses were answered %v, want %v: the fifth attempt must arm the lock", guesses, want)
			}

			// The owner's check is let go: the password verified, the new one is refused.
			open()
			if status := awaitStatus(t, "the owner's request", owner, 30*time.Second); status != http.StatusBadRequest {
				t.Fatalf("the owner's request = %d, want 400: the password was right and the new one too weak", status)
			}

			status, body, _ := a.change(t, wrongChangeBody)
			if status != http.StatusTooManyRequests {
				t.Errorf("the next guess = %d (%v), want 429: the lock the fifth guess armed is gone — the owner's request, which verified its password, "+
					"deleted a lock that was not its own", status, body)
			}
			if kind == "redis" && !a.redis.Exists(passwordLockKey(a.store.user.ID)) {
				t.Error("the lock's key is gone from Redis after the owner's request ended")
			}
			if got := a.store.auditActions(); !slices.Equal(got, []string{"password_change_locked"}) {
				t.Errorf("audit actions = %v, want exactly the lockout row of the fifth guess", got)
			}
		})
	}
}
