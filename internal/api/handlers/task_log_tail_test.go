package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// A status poll reads a running task's log from where the last poll found it
// ended, one request, instead of from the top; a log shorter than remembered
// is read again from its start.
func TestTaskLogTailRemembersWhereTheLogEnded(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	length, starts := 0, []int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start, _ := strconv.Atoi(r.URL.Query().Get("start"))
		mu.Lock()
		starts = append(starts, start)
		entries := []proxmox.TaskLogEntry{}
		for n := start + 1; n <= length && len(entries) < proxmox.TaskLogPageLines; n++ {
			entries = append(entries, proxmox.TaskLogEntry{N: n, T: "line " + strconv.Itoa(n)})
		}
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"data": entries})
	}))
	t.Cleanup(srv.Close)
	client, err := proxmox.NewClient(proxmox.ClientConfig{BaseURL: srv.URL, TokenID: "user@pam!test", TokenSecret: "secret"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	const upid = "UPID:pve-01:00001234:00005678:65000000:vzdump:101:alice@pve:"
	key := t.Name()
	poll := func(lines int, wantStarts ...int) {
		t.Helper()
		mu.Lock()
		length, starts = lines, nil
		mu.Unlock()
		got, err := taskLogTail(context.Background(), client, key, "pve-01", upid)
		if err != nil || len(got) == 0 || got[len(got)-1] != "line "+strconv.Itoa(lines) {
			t.Fatalf("log of %d lines: tail ends %v (%v), want line %d", lines, got[max(0, len(got)-1):], err, lines)
		}
		mu.Lock()
		defer mu.Unlock()
		if !slices.Equal(starts, wantStarts) {
			t.Errorf("log of %d lines read from %v, want %v", lines, starts, wantStarts)
		}
	}
	poll(6000, 0, 5000)
	poll(6100, 6000-taskLogTailKeep)
	poll(40, 6100-taskLogTailKeep, 0) // shorter than remembered
}

// The remembered ends are bounded: an entry expires, and a full cache drops
// its oldest entry for a new one.
func TestTaskLogEndCacheIsBounded(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	c := newTaskLogEndCache(2, time.Minute, func() time.Time { return now })
	c.put("a", 1)
	now = now.Add(time.Second)
	c.put("b", 2)
	c.put("c", 3)
	if _, ok := c.get("a"); ok {
		t.Error("the oldest entry outlived a full cache")
	}
	if end, ok := c.get("c"); !ok || end != 3 {
		t.Errorf("get(c) = %d, %v, want 3", end, ok)
	}
	now = now.Add(2 * time.Minute)
	if _, ok := c.get("b"); ok {
		t.Error("an entry outlived its age")
	}
}
