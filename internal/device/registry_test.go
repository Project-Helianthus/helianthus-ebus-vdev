package device

import (
	"testing"

	"github.com/d3vi1/helianthus-ebusgo/emulation"
)

func TestRegistry_AddAndLookup(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	target := &emulation.Target{Name: "test-device", Address: 0x75}

	if err := r.Add(0x75, "VR90-Zone3", target); err != nil {
		t.Fatalf("Add(0x75) unexpected error: %v", err)
	}

	got := r.Lookup(0x75)
	if got != target {
		t.Fatalf("Lookup(0x75) = %v; want %v", got, target)
	}
}

func TestRegistry_LookupMiss(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	if got := r.Lookup(0x75); got != nil {
		t.Fatalf("Lookup(0x75) = %v; want nil", got)
	}
}

func TestRegistry_DuplicateAddress(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	target1 := &emulation.Target{Name: "device-1", Address: 0x75}
	target2 := &emulation.Target{Name: "device-2", Address: 0x75}

	if err := r.Add(0x75, "first", target1); err != nil {
		t.Fatalf("Add first: unexpected error: %v", err)
	}
	if err := r.Add(0x75, "second", target2); err == nil {
		t.Fatal("Add duplicate: expected error, got nil")
	}

	// Original target should remain
	if got := r.Lookup(0x75); got != target1 {
		t.Fatalf("Lookup(0x75) after duplicate = %v; want original %v", got, target1)
	}
}

func TestRegistry_Targets(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	t1 := &emulation.Target{Name: "d1", Address: 0x75}
	t2 := &emulation.Target{Name: "d2", Address: 0x35}

	_ = r.Add(0x75, "d1", t1)
	_ = r.Add(0x35, "d2", t2)

	snapshot := r.Targets()
	if len(snapshot) != 2 {
		t.Fatalf("Targets() len = %d; want 2", len(snapshot))
	}
	if snapshot[0x75] != t1 || snapshot[0x35] != t2 {
		t.Fatalf("Targets() contents mismatch")
	}
}

func TestRegistry_Len(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	if r.Len() != 0 {
		t.Fatalf("Len() = %d; want 0", r.Len())
	}
	_ = r.Add(0x75, "d1", &emulation.Target{})
	if r.Len() != 1 {
		t.Fatalf("Len() = %d; want 1", r.Len())
	}
}
