package ws

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	gorillaws "github.com/gorilla/websocket"
)

// TestWatchCredential drives the watcher one tick at a time over scripted answers:
// it ends a console once its credential has ended, or once the check has failed
// maxFailedChecks times IN A ROW (a broken query must not leave every console open
// for good), and never after it has been told to stop.
func TestWatchCredential(t *testing.T) {
	t.Parallel()
	type answer struct {
		live   bool
		err    error
		panics bool
	}
	live, ended := answer{live: true}, answer{}
	failed := answer{err: errors.New("could not look")}
	failures := func(n int) []answer { return slices.Repeat([]answer{failed}, n) }

	tests := []struct {
		name      string
		answers   []answer
		want      []int // the codes end was called with
		endPanics bool
	}{
		{"ends it, once, when the credential has ended", []answer{live, ended}, []int{closeSessionEnded}, false},
		{"ends it when the check fails maxFailedChecks times in a row",
			failures(maxFailedChecks), []int{gorillaws.CloseInternalServerErr}, false},
		// One success in between starts the count again.
		{"keeps it through failures that a success interrupts",
			slices.Concat(failures(maxFailedChecks-1), []answer{live}, failures(maxFailedChecks-1)), nil, false},
		{"ends it when a check panics", []answer{live, {panics: true}}, []int{gorillaws.CloseInternalServerErr}, false},
		// A second call from the recover would panic outside it, and take the process.
		{"does not end it twice when ending it panics", []answer{ended}, []int{closeSessionEnded}, true},
		{"returns without ending it when stopped", []answer{live}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			ticks := make(chan time.Time)
			asked := 0
			check := func(context.Context) (bool, error) {
				a := tt.answers[asked]
				asked++
				if a.panics {
					panic("check")
				}
				return a.live, a.err
			}
			var codes []int
			done := make(chan bool)
			go func() {
				done <- watchCredential(ctx, ticks, check, func(code int, _ string) {
					codes = append(codes, code)
					if tt.endPanics {
						panic("end")
					}
				}, testLogger())
			}()

			for range tt.answers {
				select {
				case ticks <- time.Time{}:
				case <-done:
					t.Fatalf("the watcher returned after %d of %d answers", asked, len(tt.answers))
				}
			}
			if tt.want == nil {
				stop() // nothing ended it: it keeps watching until told to stop
			}
			ended := <-done

			if !slices.Equal(codes, tt.want) || ended != (tt.want != nil) {
				t.Errorf("end was called with %v (reported %v), want %v", codes, ended, tt.want)
			}
		})
	}
}
