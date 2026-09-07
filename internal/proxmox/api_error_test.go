package proxmox

import (
	"errors"
	"net/http"
	"testing"
)

// checkStatus is where a Proxmox response body becomes a typed error, and it is
// the layer the handler's status mapping depends on. Testing mapProxmoxError in
// isolation is what let a parameter-verification failure reach the operator as
// 502 for so long: the handler looked for a rejection map that checkStatus had
// already flattened away.
func TestCheckStatus_ParameterVerificationKeepsTheFieldMap(t *testing.T) {
	body := []byte(`{"errors":{"vmid":"property is not defined in schema"},"message":"Parameter verification failed.\n","data":null}`)

	var apiErr *APIError
	if !errors.As(checkStatus(http.StatusBadRequest, body), &apiErr) {
		t.Fatal("want *APIError")
	}
	if got := len(apiErr.Fields); got != 1 {
		t.Fatalf("Fields has %d entries, want 1 — the handler keys its 400 on this", got)
	}
	if got := apiErr.Fields["vmid"]; got != "property is not defined in schema" {
		t.Errorf("Fields[vmid] = %q", got)
	}
	// Message stays the flattened form the Contains-based helpers in errors.go
	// and the collector already read.
	if want := "vmid: property is not defined in schema"; apiErr.Message != want {
		t.Errorf("Message = %q, want %q", apiErr.Message, want)
	}
	if !apiErr.IsParameterRejection() {
		t.Error("IsParameterRejection() = false; the handler's 400 keys on this")
	}
}

// checkStatus parses the envelope on everything from 400 up, so a rejection map
// can arrive attached to a status that is not Proxmox rejecting parameters.
func TestAPIError_IsParameterRejectionNeedsA400(t *testing.T) {
	body := []byte(`{"errors":{"detail":"no healthy upstream"},"message":"Service Unavailable"}`)

	var apiErr *APIError
	if !errors.As(checkStatus(http.StatusServiceUnavailable, body), &apiErr) {
		t.Fatal("want *APIError")
	}
	if len(apiErr.Fields) == 0 {
		t.Fatal("Fields should still be captured; only the classification differs")
	}
	if apiErr.IsParameterRejection() {
		t.Error("a 503 carrying a rejection map is the gateway's failure, not the caller's")
	}
}

func TestCheckStatus_MultiFieldRejectionIsOrdered(t *testing.T) {
	body := []byte(`{"errors":{"vmid":"bad","name":"bad","cores":"bad","memory":"bad"},"message":"Parameter verification failed.\n"}`)
	want := "cores: bad; memory: bad; name: bad; vmid: bad"

	// Map iteration is randomised per run and per range, so repeat: an unsorted
	// implementation shows the operator a different order on every request.
	for i := range 20 {
		var apiErr *APIError
		if !errors.As(checkStatus(http.StatusBadRequest, body), &apiErr) {
			t.Fatal("want *APIError")
		}
		if apiErr.Message != want {
			t.Fatalf("iteration %d: Message = %q, want %q", i, apiErr.Message, want)
		}
	}
}

func TestCheckStatus_NonValidationBodiesCarryNoFields(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantMessage string
	}{
		{
			// The shape a cluster without Ceph returns. No rejection map, so
			// the handler must not call this the operator's fault.
			name:        "envelope without an errors map",
			status:      http.StatusInternalServerError,
			body:        `{"message":"binary not installed: /usr/bin/ceph-mon\n","data":null}`,
			wantMessage: `{"message":"binary not installed: /usr/bin/ceph-mon\n","data":null}`,
		},
		{
			name:        "plain text body",
			status:      http.StatusInternalServerError,
			body:        "500 Internal Server Error",
			wantMessage: "500 Internal Server Error",
		},
		{
			name:        "empty errors map is not a rejection",
			status:      http.StatusBadRequest,
			body:        `{"errors":{},"message":"nope\n"}`,
			wantMessage: `{"errors":{},"message":"nope\n"}`,
		},
		{
			name:        "empty body",
			status:      http.StatusInternalServerError,
			body:        "",
			wantMessage: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var apiErr *APIError
			if !errors.As(checkStatus(tt.status, []byte(tt.body)), &apiErr) {
				t.Fatal("want *APIError")
			}
			if len(apiErr.Fields) != 0 {
				t.Errorf("Fields = %v, want none", apiErr.Fields)
			}
			if apiErr.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", apiErr.Message, tt.wantMessage)
			}
			if apiErr.StatusCode != tt.status {
				t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tt.status)
			}
		})
	}
}

// The sentinel branches run before the APIError one, so they never carry Fields.
func TestCheckStatus_SentinelsAreUnchanged(t *testing.T) {
	if err := checkStatus(http.StatusNotFound, []byte(`{"errors":{"a":"b"}}`)); !errors.Is(err, ErrNotFound) {
		t.Errorf("404 = %v, want ErrNotFound", err)
	}
	if err := checkStatus(http.StatusForbidden, []byte("denied")); !errors.Is(err, ErrForbidden) {
		t.Errorf("403 = %v, want ErrForbidden", err)
	}
	if err := checkStatus(http.StatusOK, []byte("{}")); err != nil {
		t.Errorf("200 = %v, want nil", err)
	}
}
