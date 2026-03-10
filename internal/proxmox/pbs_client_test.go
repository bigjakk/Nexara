package proxmox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// pbsTestServer creates an httptest server that responds with the given data for the given path.
func pbsTestServer(t *testing.T, routes map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strip the /api2/json prefix
		path := r.URL.Path
		if len(path) > 10 && path[:10] == "/api2/json" {
			path = path[10:]
		}

		data, ok := routes[path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(response{Data: json.RawMessage(`null`)})
			return
		}

		raw, _ := json.Marshal(data)
		_ = json.NewEncoder(w).Encode(response{Data: raw})
	}))
}


func TestPBSClient_GetDatastores(t *testing.T) {
	tests := []struct {
		name      string
		data      []PBSDatastore
		wantCount int
	}{
		{
			name:      "two datastores",
			data:      []PBSDatastore{{Name: "store1"}, {Name: "store2"}},
			wantCount: 2,
		},
		{
			name:      "empty",
			data:      []PBSDatastore{},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := pbsTestServer(t, map[string]interface{}{
				"/admin/datastore": tt.data,
			})
			defer srv.Close()

			// Use insecure client for test TLS server
			client := &PBSClient{apiClient: &apiClient{
				httpClient: srv.Client(),
				baseURL:    srv.URL,
				authHeader: "PBSAPIToken=test@pam!token:secret",
			}}

			stores, err := client.GetDatastores(context.Background())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(stores) != tt.wantCount {
				t.Errorf("got %d stores, want %d", len(stores), tt.wantCount)
			}
		})
	}
}

func TestPBSClient_GetDatastoreStatus(t *testing.T) {
	data := []PBSDatastoreStatus{
		{Store: "store1", Total: 1000, Used: 500, Avail: 500},
	}

	srv := pbsTestServer(t, map[string]interface{}{
		"/status/datastore-usage": data,
	})
	defer srv.Close()

	client := &PBSClient{apiClient: &apiClient{
		httpClient: srv.Client(),
		baseURL:    srv.URL,
		authHeader: "PBSAPIToken=test@pam!token:secret",
	}}

	status, err := client.GetDatastoreStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(status) != 1 {
		t.Fatalf("got %d status entries, want 1", len(status))
	}
	if status[0].Store != "store1" {
		t.Errorf("got store %q, want %q", status[0].Store, "store1")
	}
}

func TestPBSClient_GetSnapshots(t *testing.T) {
	data := []PBSSnapshot{
		{BackupType: "vm", BackupID: "100", BackupTime: 1700000000, Size: 1024},
		{BackupType: "ct", BackupID: "200", BackupTime: 1700000001, Size: 2048},
	}

	srv := pbsTestServer(t, map[string]interface{}{
		"/admin/datastore/store1/snapshots": data,
	})
	defer srv.Close()

	client := &PBSClient{apiClient: &apiClient{
		httpClient: srv.Client(),
		baseURL:    srv.URL,
		authHeader: "PBSAPIToken=test@pam!token:secret",
	}}

	snaps, err := client.GetSnapshots(context.Background(), "store1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("got %d snapshots, want 2", len(snaps))
	}
	if snaps[0].BackupType != "vm" {
		t.Errorf("got type %q, want %q", snaps[0].BackupType, "vm")
	}
}

func TestPBSClient_GetSyncJobs(t *testing.T) {
	data := []PBSSyncJob{
		{ID: "sync-1", Store: "store1", Remote: "remote1"},
	}

	srv := pbsTestServer(t, map[string]interface{}{
		"/admin/sync": data,
	})
	defer srv.Close()

	client := &PBSClient{apiClient: &apiClient{
		httpClient: srv.Client(),
		baseURL:    srv.URL,
		authHeader: "PBSAPIToken=test@pam!token:secret",
	}}

	jobs, err := client.GetSyncJobs(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1", len(jobs))
	}
	if jobs[0].ID != "sync-1" {
		t.Errorf("got ID %q, want %q", jobs[0].ID, "sync-1")
	}
}

func TestPBSClient_GetVerifyJobs(t *testing.T) {
	data := []PBSVerifyJob{
		{ID: "verify-1", Store: "store1"},
	}

	srv := pbsTestServer(t, map[string]interface{}{
		"/admin/verify": data,
	})
	defer srv.Close()

	client := &PBSClient{apiClient: &apiClient{
		httpClient: srv.Client(),
		baseURL:    srv.URL,
		authHeader: "PBSAPIToken=test@pam!token:secret",
	}}

	jobs, err := client.GetVerifyJobs(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1", len(jobs))
	}
}

func TestPBSClient_GetTasks(t *testing.T) {
	data := []PBSTask{
		{UPID: "UPID:node:001", Type: "garbage_collection"},
	}

	srv := pbsTestServer(t, map[string]interface{}{
		"/nodes/localhost/tasks": data,
	})
	defer srv.Close()

	client := &PBSClient{apiClient: &apiClient{
		httpClient: srv.Client(),
		baseURL:    srv.URL,
		authHeader: "PBSAPIToken=test@pam!token:secret",
	}}

	tasks, err := client.GetTasks(context.Background(), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want 1", len(tasks))
	}
}

func TestPBSClient_EmptyStoreName(t *testing.T) {
	client := &PBSClient{apiClient: &apiClient{
		baseURL:    "https://example.com",
		authHeader: "PBSAPIToken=test@pam!token:secret",
	}}

	_, err := client.GetSnapshots(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty store name")
	}

	_, err = client.GetBackupGroups(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty store name")
	}

	_, err = client.TriggerGC(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty store name")
	}
}

func TestNewPBSClient_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ClientConfig
		wantErr bool
	}{
		{
			name:    "empty base URL",
			cfg:     ClientConfig{TokenID: "t", TokenSecret: "s"},
			wantErr: true,
		},
		{
			name:    "empty token ID",
			cfg:     ClientConfig{BaseURL: "https://example.com", TokenSecret: "s"},
			wantErr: true,
		},
		{
			name:    "empty token secret",
			cfg:     ClientConfig{BaseURL: "https://example.com", TokenID: "t"},
			wantErr: true,
		},
		{
			name:    "valid config",
			cfg:     ClientConfig{BaseURL: "https://example.com", TokenID: "t", TokenSecret: "s"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewPBSClient(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewPBSClient() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
