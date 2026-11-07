package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/codercollo/nanga/internal/platform/money"
)

func TestHoldLifecycle(t *testing.T) {
	h, err := NewHold("h1", "a", "pay-1", money.FromKES(500), now)
	if err != nil || h.Status != HoldActive || !h.ResolvedAt.IsZero() {
		t.Fatalf("%+v %v", h, err)
	}
	later := now.Add(time.Minute)
	r, err := h.Release(later)
	if err != nil || r.Status != HoldReleased || !r.ResolvedAt.Equal(later) {
		t.Fatalf("%+v %v", r, err)
	}
	c, err := h.Capture(later, "j1")
	if err != nil || c.Status != HoldCaptured || c.JournalID != "j1" {
		t.Fatalf("%+v %v", c, err)
	}
	if h.Status != HoldActive {
		t.Fatal("receiver mutated")
	}
}

func TestHoldTerminalStates(t *testing.T) {
	h, _ := NewHold("h1", "a", "pay-1", money.FromKES(500), now)
	r, _ := h.Release(now)
	if _, err := r.Release(now); !errors.Is(err, ErrHoldNotActive) {
		t.Fatalf("double release: %v", err)
	}
	if _, err := r.Capture(now, "j"); !errors.Is(err, ErrHoldNotActive) {
		t.Fatalf("capture after release: %v", err)
	}
	c, _ := h.Capture(now, "j")
	if _, err := c.Release(now); !errors.Is(err, ErrHoldNotActive) {
		t.Fatalf("release after capture: %v", err)
	}
	if _, err := h.Capture(now, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("capture without journal: %v", err)
	}
}

func TestNewHoldValidation(t *testing.T) {
	bad := []func() (Hold, error){
		func() (Hold, error) { return NewHold("", "a", "r", money.FromKES(1), now) },
		func() (Hold, error) { return NewHold("h", "", "r", money.FromKES(1), now) },
		func() (Hold, error) { return NewHold("h", "a", "", money.FromKES(1), now) },
		func() (Hold, error) { return NewHold("h", "a", "r", money.FromKES(0), now) },
		func() (Hold, error) { return NewHold("h", "a", "r", money.Money{Minor: 1, Currency: "USD"}, now) },
	}
	for i, f := range bad {
		if _, err := f(); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d: want ErrInvalid, got %v", i, err)
		}
	}
}
