package handlers

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/proxmox"
)

func TestMapProxmoxError_InvalidInputIsClientError(t *testing.T) {
	// A rejected identifier never reaches Proxmox, so it must not be reported as
	// a Proxmox failure — the operator needs to know their input was the problem.
	err := fmt.Errorf("%w: volume id %q has a bad path segment %q",
		proxmox.ErrInvalidInput, "local:iso/../x", "..")

	var fe *fiber.Error
	if !errors.As(mapProxmoxError(err), &fe) {
		t.Fatalf("mapProxmoxError returned %T, want *fiber.Error", mapProxmoxError(err))
	}
	if fe.Code != fiber.StatusBadRequest {
		t.Errorf("status = %d, want %d", fe.Code, fiber.StatusBadRequest)
	}
	if !strings.Contains(fe.Message, "bad path segment") {
		t.Errorf("message = %q, want it to explain the rejection", fe.Message)
	}
}

func TestMapProxmoxError_SentinelsKeepTheirStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"not found", proxmox.ErrNotFound, fiber.StatusNotFound},
		{"forbidden", proxmox.ErrForbidden, fiber.StatusForbidden},
		{"connection failed", proxmox.ErrConnectionFailed, fiber.StatusBadGateway},
		{"unknown", errors.New("boom"), fiber.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fe *fiber.Error
			if !errors.As(mapProxmoxError(tt.err), &fe) {
				t.Fatal("want *fiber.Error")
			}
			if fe.Code != tt.want {
				t.Errorf("status = %d, want %d", fe.Code, tt.want)
			}
		})
	}
}
