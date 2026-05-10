package handlers

import (
	"context"
	"testing"
)

// TestNewMigrationHandler_SlotCapacity asserts the slot semaphore has the
// expected capacity. Constructor wiring this wrong (e.g. unbuffered chan)
// would silently deadlock or pass everything through.
func TestNewMigrationHandler_SlotCapacity(t *testing.T) {
	h := NewMigrationHandler(context.Background(), nil, "", nil)
	if cap(h.slots) != MigrationConcurrencyLimit {
		t.Fatalf("slots capacity = %d, want %d", cap(h.slots), MigrationConcurrencyLimit)
	}
	if MigrationConcurrencyLimit < 1 {
		t.Fatalf("MigrationConcurrencyLimit = %d, want >= 1", MigrationConcurrencyLimit)
	}
}

// TestNewMigrationHandler_NilShutdownCtxFallback ensures partial construction
// (e.g. tests passing nil) doesn't nil-panic when h.shutdownCtx is read later.
func TestNewMigrationHandler_NilShutdownCtxFallback(t *testing.T) {
	h := NewMigrationHandler(nil, nil, "", nil) //nolint:staticcheck // nil ctx is the path under test
	if h.shutdownCtx == nil {
		t.Fatal("shutdownCtx is nil; constructor must fall back to context.Background()")
	}
	// background never expires.
	select {
	case <-h.shutdownCtx.Done():
		t.Fatal("background-derived ctx should not be Done()")
	default:
	}
}

// TestMigrationSlots_NonBlockingSendRejectsWhenFull mirrors the
// `case h.slots <- struct{}{}: default: 429` pattern in Execute. If the
// slots channel is saturated, the non-blocking send must fail (so the
// handler returns 429) — not block the request goroutine indefinitely.
func TestMigrationSlots_NonBlockingSendRejectsWhenFull(t *testing.T) {
	h := NewMigrationHandler(context.Background(), nil, "", nil)

	// Fill every slot.
	for i := 0; i < MigrationConcurrencyLimit; i++ {
		select {
		case h.slots <- struct{}{}:
		default:
			t.Fatalf("slot %d/%d unexpectedly rejected; channel cap is wrong", i+1, MigrationConcurrencyLimit)
		}
	}

	// Next non-blocking send must fail (this is the 429 path).
	select {
	case h.slots <- struct{}{}:
		t.Fatal("expected non-blocking send to fail when slots are saturated")
	default:
		// expected — Execute would return 429 here.
	}

	// Releasing a slot must let the next send succeed.
	<-h.slots
	select {
	case h.slots <- struct{}{}:
		// expected — slot was released.
	default:
		t.Fatal("expected non-blocking send to succeed after a slot is released")
	}
}
