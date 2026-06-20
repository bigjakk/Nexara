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

func TestIsLockedError(t *testing.T) {
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
			name: "migrate against locked VM (raw body message)",
			err:  &APIError{StatusCode: 500, Message: `{"data":null,"message":"VM is locked (migrate)\n"}`},
			want: true,
		},
		{
			name: "plain locked message",
			err:  &APIError{StatusCode: 500, Message: "VM is locked (migrate)"},
			want: true,
		},
		{
			name: "CT locked (backup)",
			err:  &APIError{StatusCode: 500, Message: "CT is locked (backup)"},
			want: true,
		},
		{
			name: "wrapped as the orchestrator wraps it",
			err:  fmt.Errorf("migrate VM 106 on HV02: %w", &APIError{StatusCode: 500, Message: `{"data":null,"message":"VM is locked (migrate)\n"}`}),
			want: true,
		},
		{
			name: "unrelated API error",
			err:  &APIError{StatusCode: 500, Message: "no such logical volume"},
			want: false,
		},
		{
			name: "non-API error mentioning locked",
			err:  errors.New("VM is locked (migrate)"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsLockedError(tt.err); got != tt.want {
				t.Errorf("IsLockedError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
