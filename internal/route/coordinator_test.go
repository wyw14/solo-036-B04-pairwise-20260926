package route

import "testing"

func TestReleaseIfOwnerIdentity(t *testing.T) {
	c := NewCoordinator()

	if _, err := c.ReleaseIfOwner("main-a-c", "run-1"); err == nil {
		t.Fatal("expected error releasing an unreserved route")
	}

	if _, err := c.Reserve("main-a-c", "run-1", 1); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	// A different run cannot release the lease.
	if _, err := c.ReleaseIfOwner("main-a-c", "run-2"); err == nil {
		t.Fatal("expected error when a non-owner releases the route")
	}
	if lease, ok := c.Owner("main-a-c"); !ok || lease.RunID != "run-1" {
		t.Fatalf("lease must remain with run-1 after foreign release, got %+v ok=%v", lease, ok)
	}

	// The owner can release exactly once.
	if lease, err := c.ReleaseIfOwner("main-a-c", "run-1"); err != nil || lease.RunID != "run-1" {
		t.Fatalf("owner release failed: lease=%+v err=%v", lease, err)
	}
	if _, ok := c.Owner("main-a-c"); ok {
		t.Fatal("route must be free after owner release")
	}
	if _, err := c.ReleaseIfOwner("main-a-c", "run-1"); err == nil {
		t.Fatal("expected error releasing an already-released route")
	}
}

func TestReserveRejectsForeignOwner(t *testing.T) {
	c := NewCoordinator()
	if _, err := c.Reserve("main-a-c", "run-1", 1); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := c.Reserve("main-a-c", "run-2", 1); err == nil {
		t.Fatal("expected a route held by run-1 to reject run-2")
	}
	// A stranded lease is observable: after a hypothetical stranded
	// terminal owner, the route cannot be reserved until released.
	if lease, err := c.ReleaseIfOwner("main-a-c", "run-1"); err != nil {
		t.Fatalf("owner release: %v", err)
	} else if lease.Route != "main-a-c" {
		t.Fatalf("unexpected lease route %q", lease.Route)
	}
}
