package proxmox

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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
// not return until that read has ended. Ending the held read needs a wait:
// one that passes is the fix (still blocked), one that is too short only lets
// a broken build pass, never fails a good one.
func TestUploadToStorage_ReturnsOnlyOnceNoReadIsGoing(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		const answer = `{"message":"storage 'store01' is not online\n"}`
		// A Content-Length, as pveproxy sends, so the answer ends before the body does.
		w.Header().Set("Content-Length", strconv.Itoa(len(answer)))
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)

	file := &heldFile{holding: make(chan struct{}), free: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := newTestClient(t, srv.URL).UploadToStorage(context.Background(), "pve-01", "store01", "iso",
			"linux01.iso", file, 1<<20)
		file.returned.Store(true)
		done <- err
	}()
	<-file.holding
	select {
	case <-done:
		t.Fatal("UploadToStorage returned while a read of the file was still going")
	case <-time.After(time.Second):
	}
	close(file.free)
	var apiErr *APIError
	if err := <-done; !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("err = %v, want Proxmox's 500", err)
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
