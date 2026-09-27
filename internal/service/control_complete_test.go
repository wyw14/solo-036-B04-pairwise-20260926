package service

import (
	"context"
	"testing"
	"time"

	"github.com/wyw14/solo-036/internal/model"
	"github.com/wyw14/solo-036/internal/tracker"
)

const (
	launcherStation = "station-a"
	receiverStation = "station-c"
	mainRoute       = "main-station-a-station-c"
)

// driveToDepressurizing runs the full launch -> track -> receive flow and
// returns a run sitting in depressurizing, holding mainRoute.
func driveToDepressurizing(t *testing.T, rt *Runtime) string {
	t.Helper()
	op, err := rt.Launch(context.Background(), LaunchRequest{
		StationID:      launcherStation,
		RouteID:        mainRoute,
		TargetPressure: 8.0,
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	runID := op.Run.ID

	now := time.Now().UTC()
	for _, position := range []float64{10, 20} {
		if _, err := rt.Track(runID, TrackingRequest{
			StationID:  receiverStation,
			SourceID:   "magnetic/1",
			Kind:       tracker.SourceMagnetic,
			Position:   position,
			ObservedAt: now.Add(time.Duration(position) * time.Second),
		}); err != nil {
			t.Fatalf("track %.0f: %v", position, err)
		}
	}

	if _, err := rt.Receive(runID, ReceiveRequest{
		StationID:      receiverStation,
		IsolationEpoch: 500,
	}); err != nil {
		t.Fatalf("receive: %v", err)
	}
	current, ok := rt.Runs.Get(runID)
	if !ok || current.State != model.StateDepressurizing {
		t.Fatalf("expected depressurizing before completion, got %+v ok=%v", current, ok)
	}
	return runID
}

// reserveForeignRoute simulates a second, valid route fenced to another run.
func reserveForeignRoute(t *testing.T, rt *Runtime) string {
	t.Helper()
	other := rt.Runs.Create(launcherStation)
	if _, err := rt.Routes.Reserve("main-station-a-station-b", other.ID, other.Epoch); err != nil {
		t.Fatalf("reserve foreign route: %v", err)
	}
	rt.mu.Lock()
	rt.control.Routes["main-station-a-station-b"] = model.RouteState{
		ID:          "main-station-a-station-b",
		FromStation: launcherStation,
		ToStation:   "station-b",
		RunID:       other.ID,
		LeaseEpoch:  other.Epoch,
		Reserved:    true,
		UpdatedAt:   time.Now().UTC(),
	}
	rt.mu.Unlock()
	return other.ID
}

func assertLease(t *testing.T, rt *Runtime, route, wantOwner string) {
	t.Helper()
	lease, ok := rt.Routes.Owner(route)
	if wantOwner == "" {
		if ok {
			t.Fatalf("route %s should be free, still leased to %s", route, lease.RunID)
		}
		return
	}
	if !ok || lease.RunID != wantOwner {
		t.Fatalf("route %s should be leased to %s, got ok=%v lease=%+v", route, wantOwner, ok, lease)
	}
}

// A completion request naming the wrong route must not mark the run complete
// and must not strand the route the run actually holds.
func TestCompleteRejectsWrongRoute(t *testing.T) {
	rt := NewRuntime()
	runID := driveToDepressurizing(t, rt)
	otherID := reserveForeignRoute(t, rt)

	// Unknown / mistyped route number.
	if _, err := rt.Complete(runID, "main-typo"); err == nil {
		t.Fatal("completion with an unknown route must fail")
	}
	// Empty route number.
	if _, err := rt.Complete(runID, ""); err == nil {
		t.Fatal("completion with an empty route must fail")
	}
	// A different, real route that is fenced to another run.
	if _, err := rt.Complete(runID, "main-station-a-station-b"); err == nil {
		t.Fatal("completion against another run's route must fail")
	}

	// Nothing moved: the run is still in progress and both leases stand, so
	// the operator can correct the route number and finish cleanly.
	if current, _ := rt.Runs.Get(runID); current.State != model.StateDepressurizing {
		t.Fatalf("run must remain depressurizing after rejected completion, got %s", current.State)
	}
	if current, _ := rt.Runs.Get(runID); current.State.Terminal() {
		t.Fatal("run must not be terminal after a rejected completion")
	}
	assertLease(t, rt, mainRoute, runID)
	assertLease(t, rt, "main-station-a-station-b", otherID)

	// Retrying with the correct route completes and releases exactly that route.
	if _, err := rt.Complete(runID, mainRoute); err != nil {
		t.Fatalf("correct completion failed: %v", err)
	}
	if current, _ := rt.Runs.Get(runID); current.State != model.StateComplete {
		t.Fatalf("run should be complete, got %s", current.State)
	}
	assertLease(t, rt, mainRoute, "")
	assertLease(t, rt, "main-station-a-station-b", otherID)

	routeState := rt.Snapshot()
	for _, rs := range routeState.Routes {
		if rs.ID == mainRoute && (rs.Reserved || rs.RunID != "") {
			t.Fatalf("completed route must be unreserved in control state: %+v", rs)
		}
	}

	if err := rt.RecoveryBarrier(); err != nil {
		t.Fatalf("recovery barrier must pass after clean completion: %v", err)
	}
}

// After a correct completion the released line must be usable by a new run.
func TestCompletedRouteIsReusable(t *testing.T) {
	rt := NewRuntime()
	runID := driveToDepressurizing(t, rt)

	if _, err := rt.Complete(runID, mainRoute); err != nil {
		t.Fatalf("complete: %v", err)
	}

	if _, err := rt.Launch(context.Background(), LaunchRequest{
		StationID:      launcherStation,
		RouteID:        mainRoute,
		TargetPressure: 7.5,
	}); err != nil {
		t.Fatalf("route should be reusable after completion: %v", err)
	}
}

func TestCompleteFromWrongState(t *testing.T) {
	rt := NewRuntime()
	op, err := rt.Launch(context.Background(), LaunchRequest{
		StationID:      launcherStation,
		RouteID:        mainRoute,
		TargetPressure: 8.0,
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	// Run is launched (not yet received); completion must be refused.
	if _, err := rt.Complete(op.Run.ID, mainRoute); err == nil {
		t.Fatal("completion must be refused before depressurizing")
	}
	assertLease(t, rt, mainRoute, op.Run.ID)
	if current, _ := rt.Runs.Get(op.Run.ID); current.State != model.StateLaunched {
		t.Fatalf("run state must be unchanged, got %s", current.State)
	}
}
