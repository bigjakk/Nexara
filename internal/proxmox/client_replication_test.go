package proxmox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetReplicationJobsReadsRateAsNumberOrString: Proxmox's section config
// returns the number-typed rate as a JSON number, and one job with a rate limit
// used to fail the decode of the whole list.
func TestGetReplicationJobsReadsRateAsNumberOrString(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"100-0","type":"local","guest":100,"target":"pve-02","rate":10.5},
			{"id":"101-0","type":"local","guest":101,"target":"pve-02","rate":"20"},
			{"id":"102-0","type":"local","guest":102,"target":"pve-02"}]}`))
	}))
	t.Cleanup(srv.Close)

	jobs, err := newTestClient(t, srv.URL).GetReplicationJobs(context.Background())
	if err != nil {
		t.Fatalf("GetReplicationJobs: %v", err)
	}
	got := make([]string, 0, len(jobs))
	for _, j := range jobs {
		got = append(got, j.ID+"="+j.Rate.String())
	}
	if want := []string{"100-0=10.5", "101-0=20", "102-0="}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("jobs = %v, want %v", got, want)
	}
}
