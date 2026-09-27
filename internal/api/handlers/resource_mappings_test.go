package handlers

import (
	"errors"
	"fmt"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/proxmox"
)

func TestMapUSBMappingCreateError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{
			// What pve-manager's create dies with, re-died by lock_usb_config,
			// in the JSON envelope checkStatus keeps as the message.
			name: "a taken id is a conflict",
			err: &proxmox.APIError{
				StatusCode: 500,
				Message:    `{"data":null,"message":"create hardware mapping failed: usb ID 'usbdev01' already defined\n"}`,
			},
			want: fiber.StatusConflict,
		},
		{
			name: "a taken id is a conflict without the envelope too",
			err: &proxmox.APIError{
				StatusCode: 500,
				Message:    "create hardware mapping failed: usb ID 'usbdev01' already defined",
			},
			want: fiber.StatusConflict,
		},
		{
			// The client refused it; it never reached Proxmox.
			name: "a malformed device id stays a 400",
			err:  fmt.Errorf("%w: USB device id %q must be vendor:product", proxmox.ErrInvalidInput, "12345678"),
			want: fiber.StatusBadRequest,
		},
		{
			// A PVEAdmin token: no Mapping.Modify.
			name: "a token without Mapping.Modify stays a 403",
			err:  proxmox.ErrForbidden,
			want: fiber.StatusForbidden,
		},
		{
			name: "an unreachable cluster stays a 502",
			err:  proxmox.ErrConnectionFailed,
			want: fiber.StatusBadGateway,
		},
		{
			// The lock itself failing is the same wrapper with another cause.
			name: "an unrelated create failure stays a 502",
			err: &proxmox.APIError{
				StatusCode: 500,
				Message:    "create hardware mapping failed: can't lock file '/var/lock/pve-manager/pve-mapping-usb.lck' - got timeout",
			},
			want: fiber.StatusBadGateway,
		},
		{
			// The phrase in the client's wrap is the caller's own input, so
			// it must not decide the status; only Proxmox's words are read.
			name: "the phrase in the client's wrap is not Proxmox's",
			err: fmt.Errorf("create USB mapping %s: %w", "already defined",
				&proxmox.APIError{StatusCode: 500, Message: "unable to read usb.cfg"}),
			want: fiber.StatusBadGateway,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fe *fiber.Error
			err := mapUSBMappingCreateError(tt.err)
			if !errors.As(err, &fe) {
				t.Fatalf("mapUSBMappingCreateError(%v) = %v, want a *fiber.Error", tt.err, err)
			}
			if fe.Code != tt.want {
				t.Errorf("status = %d, want %d (message %q)", fe.Code, tt.want, fe.Message)
			}
		})
	}
	if mapUSBMappingCreateError(nil) != nil {
		t.Error("mapUSBMappingCreateError(nil) should stay nil")
	}
}
