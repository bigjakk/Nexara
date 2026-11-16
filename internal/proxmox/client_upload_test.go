package proxmox

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// endlessFile is an upload's file that never ends.
type endlessFile struct{}

func (endlessFile) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

// heldFile gives one read of data, then holds the next until free is closed,
// as a browser that has stopped sending holds a read of its stream. It
// records whether that read was still going when the upload returned.
type heldFile struct {
	reads              atomic.Int32
	holding, free      chan struct{}
	returned, overlaps atomic.Bool
}

func (f *heldFile) Read(p []byte) (int, error) {
	if f.reads.Add(1) == 1 {
		return copy(p, "x"), nil
	}
	if f.reads.Load() == 2 {
		close(f.holding)
	}
	<-f.free
	if f.returned.Load() {
		f.overlaps.Store(true)
	}
	return 0, errors.New("the browser went away")
}

// TestUploadToStorage_ReturnsOnlyOnceNoReadIsGoing: Proxmox answers while
// net/http is in a read of the file. The storage handler's file is a request
// stream fasthttp recycles once the handler returns, so UploadToStorage may
// not return until that read has ended. The fake Proxmox answers only once the
// held read has started, so the scenario is certain rather than a race its
// answer can win. Ending the held read needs a wait: one that passes is the fix
// (still blocked), one that is too short only lets a broken build pass, never
// fails a good one. Every other wait is bounded by an outcome, not a clock.
func TestUploadToStorage_ReturnsOnlyOnceNoReadIsGoing(t *testing.T) {
	t.Parallel()
	file := &heldFile{holding: make(chan struct{}), free: make(chan struct{})}
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-file.holding:
		case <-r.Context().Done():
			return
		case <-stop: // a failed run: let srv.Close end instead of waiting on this handler
			return
		}
		const answer = `{"message":"storage 'store01' is not online\n"}`
		// A Content-Length, as pveproxy sends, so the answer ends before the body does.
		w.Header().Set("Content-Length", strconv.Itoa(len(answer)))
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	var freeOnce sync.Once
	free := func() { freeOnce.Do(func() { close(file.free) }) }
	// Runs before srv.Close (cleanups run last-in first-out), so a run that
	// failed early ends as a failure: the handler and the held read are released.
	t.Cleanup(func() { close(stop); free() })

	done := make(chan error, 1)
	go func() {
		_, err := newTestClient(t, srv.URL).UploadToStorage(context.Background(), "pve-01", "store01", "iso",
			"linux01.iso", file, 1<<20)
		file.returned.Store(true)
		done <- err
	}()
	select {
	case <-file.holding:
	case err := <-done:
		t.Fatalf("UploadToStorage returned (%v) before the second read of the file began", err)
	case <-time.After(30 * time.Second): // only a transport that stopped reading gets here
		t.Fatal("no second read of the file began")
	}
	select {
	case <-done:
		t.Fatal("UploadToStorage returned while a read of the file was still going")
	case <-time.After(time.Second):
	}
	free()
	var apiErr *APIError
	select {
	case err := <-done:
		if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusInternalServerError {
			t.Errorf("err = %v, want Proxmox's 500", err)
		}
	case <-time.After(30 * time.Second): // only an upload that never returns gets here
		t.Fatal("UploadToStorage never returned after the held read ended")
	}
	if file.overlaps.Load() {
		t.Error("the held read ended after UploadToStorage returned")
	}
}

// TestUploadToStorage_AbandonsAStalledUpload: a Proxmox that stops taking the
// body leaves the transport blocked in a write with no read of the file to
// time out. The watchdog has to end the upload, with ErrUploadStalled.
func TestUploadToStorage_AbandonsAStalledUpload(t *testing.T) {
	t.Parallel()
	// The handler reads none of the body. net/http notices a closed connection
	// only once a body is read, so the handler is released by hand, before Close.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	c := newTestClient(t, srv.URL)
	c.uploadStall = 100 * time.Millisecond

	done := make(chan error, 1)
	go func() {
		_, err := c.UploadToStorage(context.Background(), "pve-01", "store01", "iso", "linux01.iso", endlessFile{}, 1<<40)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrUploadStalled) {
			t.Errorf("err = %v, want ErrUploadStalled", err)
		}
	case <-time.After(30 * time.Second): // 300x the stall timeout: only a missing watchdog gets here
		t.Fatal("the stalled upload was never abandoned")
	}
}

// fakeClock drives uploadBody's watchdog without waiting: its timers fire
// when advance moves the clock past them. Single goroutine only.
type fakeClock struct {
	now    time.Duration
	timers []*fakeTimer
}

type fakeTimer struct {
	c      *fakeClock
	at     time.Duration
	f      func()
	active bool
}

func (c *fakeClock) after(d time.Duration, f func()) uploadTimer {
	t := &fakeTimer{c: c, at: c.now + d, f: f, active: true}
	c.timers = append(c.timers, t)
	return t
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	was := t.active
	t.at, t.active = t.c.now+d, true
	return was
}

func (t *fakeTimer) Stop() bool {
	was := t.active
	t.active = false
	return was
}

func (c *fakeClock) advance(d time.Duration) {
	c.now += d
	for _, t := range c.timers {
		if t.active && t.at <= c.now {
			t.active = false
			t.f()
		}
	}
}

// trickle gives one byte a read, n times, then ends.
type trickle struct{ n int }

func (r *trickle) Read(p []byte) (int, error) {
	if r.n == 0 {
		return 0, io.EOF
	}
	r.n--
	return copy(p, "x"), nil
}

type timedOut struct{}

func (timedOut) Error() string { return "i/o timeout" }
func (timedOut) Timeout() bool { return true }

type timingOutReader struct{}

func (timingOutReader) Read([]byte) (int, error) { return 0, timedOut{} }

// TestUploadBody covers the three things uploadBody decides on its own.
func TestUploadBody(t *testing.T) {
	t.Parallel()
	const stall = time.Minute

	// A slow upload that keeps moving is never cut: each byte restarts the
	// watchdog, across several stall periods in all.
	t.Run("a trickle is not a stall", func(t *testing.T) {
		t.Parallel()
		clock, aborted := &fakeClock{}, 0
		b := newUploadBody(&trickle{n: 6}, stall, func() { aborted++ }, clock.after)
		for {
			clock.advance(stall * 6 / 10)
			if _, err := b.Read(make([]byte, 8)); err == io.EOF {
				break
			}
		}
		if aborted != 0 || b.failure() != nil {
			t.Errorf("aborted %d time(s), failure %v, after %v of a trickle; want neither", aborted, b.failure(), clock.now)
		}
	})

	// Once released, the file is never read again: the handler's stream is
	// recycled by then.
	t.Run("no read after release", func(t *testing.T) {
		t.Parallel()
		inner := &trickle{n: 1}
		b := newUploadBody(inner, stall, func() {}, (&fakeClock{}).after)
		b.release()
		if _, err := b.Read(make([]byte, 8)); !errors.Is(err, errUploadBodyReleased) || inner.n != 1 {
			t.Errorf("read after release = %v, file read %v; want errUploadBodyReleased and no read", err, inner.n != 1)
		}
	})

	// A browser that stops sending trips the watchdog a moment before its
	// read times out; the timed-out read is the cause to report.
	t.Run("a timed-out read is the browser's stall", func(t *testing.T) {
		t.Parallel()
		clock := &fakeClock{}
		b := newUploadBody(timingOutReader{}, stall, func() {}, clock.after)
		clock.advance(stall)
		_, _ = b.Read(make([]byte, 8))
		if err := b.failure(); !errors.Is(err, ErrUploadBodyRead) || errors.Is(err, ErrUploadStalled) {
			t.Errorf("failure = %v, want ErrUploadBodyRead", err)
		}
	})
}
