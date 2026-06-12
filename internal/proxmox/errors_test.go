package proxmox

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsGuestNotRunningError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "qemu vncproxy on stopped VM",
			err:  &APIError{StatusCode: 500, Message: "VM 105 not running"},
			want: true,
		},
		{
			name: "lxc vncproxy on stopped CT",
			err:  &APIError{StatusCode: 500, Message: "CT 105 not running"},
			want: true,
		},
		{
			name: "wrapped API error",
			err:  fmt.Errorf("VM 105 vncproxy on pve1: %w", &APIError{StatusCode: 500, Message: "VM 105 not running"}),
			want: true,
		},
		{
			name: "unrelated API error",
			err:  &APIError{StatusCode: 500, Message: "internal error"},
			want: false,
		},
		{
			name: "non-API error mentioning not running",
			err:  errors.New("VM not running"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsGuestNotRunningError(tt.err); got != tt.want {
				t.Errorf("IsGuestNotRunningError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
