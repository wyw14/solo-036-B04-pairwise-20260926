package service

import (
	"strings"
	"testing"
	"time"

	"github.com/wyw14/solo-036/internal/model"
	"github.com/wyw14/solo-036/internal/run"
)

const testRouteID = "main-station-a-station-c"

// occupyRoute mirrors what Launch does once a route lease is taken.
func occupyRoute(rt *Runtime, routeID, runID string, epoch int64) {
	lease, err := rt.Routes.Reserve(routeID, runID, epoch)
	if err != nil {
		panic(err)
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	routeState := rt.control.Routes[routeID]
	routeState.ID = routeID
	routeState.RunID = runID
	routeState.LeaseEpoch = lease.Epoch
	routeState.Reserved = true
	routeState.UpdatedAt = time.Now().UTC()
	rt.control.Routes[routeID] = routeState
}

// depressurizingRun builds a run that has been received and awaits closure.
func depressurizingRun(t *testing.T, rt *Runtime, routeID string) model.Run {
	t.Helper()
	created, err := rt.CreateOperation("station-a")
	if err != nil {
		t.Fatalf("create operation: %v", err)
	}
	occupyRoute(rt, routeID, created.ID, created.Epoch)
	if err := run.Start(rt.Runs, created.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := run.Track(rt.Runs, created.ID); err != nil {
		t.Fatalf("track run: %v", err)
	}
	if err := run.Receive(rt.Runs, created.ID); err != nil {
		t.Fatalf("receive run: %v", err)
	}
	return created
}

func requireLeaseState(t *testing.T, rt *Runtime, runID, routeID string, reserved bool) {
	t.Helper()
	_, leased := rt.Routes.LeaseOf(runID)
	if leased != reserved {
		t.Fatalf("run %s lease held = %v, want %v", runID, leased, reserved)
	}
	rt.mu.Lock()
	routeState := rt.control.Routes[routeID]
	rt.mu.Unlock()
	if routeState.Reserved != reserved {
		t.Fatalf("route %s reserved = %v, want %v", routeID, routeState.Reserved, reserved)
	}
	if reserved && routeState.RunID != runID {
		t.Fatalf("route %s mirror owner = %q, want %q", routeID, routeState.RunID, runID)
	}
	if !reserved && routeState.RunID != "" {
		t.Fatalf("route %s mirror owner = %q, want released", routeID, routeState.RunID)
	}
}

func TestCompleteRejectsForeignRoute(t *testing.T) {
	rt := NewRuntime()
	created := depressurizingRun(t, rt, testRouteID)

	// A second, known route that this run does not occupy.
	const otherRouteID = "main-station-b-station-c"
	rt.mu.Lock()
	rt.control.Routes[otherRouteID] = model.RouteState{ID: otherRouteID, FromStation: "station-b", ToStation: "station-c", UpdatedAt: time.Now().UTC()}
	rt.mu.Unlock()

	if _, err := rt.Complete(created.ID, otherRouteID); err == nil {
		t.Fatal("complete with a foreign route id must fail")
	} else if !strings.Contains(err.Error(), testRouteID) {
		t.Fatalf("error should name the route the run actually occupies, got %q", err.Error())
	}

	current, ok := rt.Runs.Get(created.ID)
	if !ok {
		t.Fatal("run must still exist")
	}
	if current.State != model.StateDepressurizing {
		t.Fatalf("run state = %s, want %s (completion must not be recorded)", current.State, model.StateDepressurizing)
	}
	requireLeaseState(t, rt, created.ID, testRouteID, true)

	// The run can still be closed with the correct route id afterwards.
	if _, err := rt.Complete(created.ID, testRouteID); err != nil {
		t.Fatalf("complete with the occupied route id: %v", err)
	}
	current, _ = rt.Runs.Get(created.ID)
	if current.State != model.StateComplete {
		t.Fatalf("run state = %s, want %s", current.State, model.StateComplete)
	}
	requireLeaseState(t, rt, created.ID, testRouteID, false)
}

func TestCompleteRejectsUnknownAndEmptyRoute(t *testing.T) {
	rt := NewRuntime()
	created := depressurizingRun(t, rt, testRouteID)

	for _, routeID := range []string{"", "main-station-a-station-x"} {
		if _, err := rt.Complete(created.ID, routeID); err == nil {
			t.Fatalf("complete with route id %q must fail", routeID)
		}
	}
	current, _ := rt.Runs.Get(created.ID)
	if current.State != model.StateDepressurizing {
		t.Fatalf("run state = %s, want %s", current.State, model.StateDepressurizing)
	}
	requireLeaseState(t, rt, created.ID, testRouteID, true)
}

func TestCompleteDoesNotTouchOtherRunsLease(t *testing.T) {
	rt := NewRuntime()
	first := depressurizingRun(t, rt, testRouteID)

	const otherRouteID = "main-station-b-station-c"
	rt.mu.Lock()
	rt.control.Routes[otherRouteID] = model.RouteState{ID: otherRouteID, FromStation: "station-b", ToStation: "station-c", UpdatedAt: time.Now().UTC()}
	rt.mu.Unlock()
	second := depressurizingRun(t, rt, otherRouteID)

	// Closing the first run with the second run's route must fail and
	// leave both leases intact.
	if _, err := rt.Complete(first.ID, otherRouteID); err == nil {
		t.Fatal("complete with another run's route id must fail")
	}
	requireLeaseState(t, rt, first.ID, testRouteID, true)
	requireLeaseState(t, rt, second.ID, otherRouteID, true)

	if _, err := rt.Complete(first.ID, testRouteID); err != nil {
		t.Fatalf("complete first run: %v", err)
	}
	requireLeaseState(t, rt, first.ID, testRouteID, false)
	requireLeaseState(t, rt, second.ID, otherRouteID, true)
}

func TestCompleteTerminalRunStaysRejected(t *testing.T) {
	rt := NewRuntime()
	created := depressurizingRun(t, rt, testRouteID)

	operation, err := rt.Complete(created.ID, testRouteID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if operation.Run.State != model.StateComplete {
		t.Fatalf("operation state = %s, want %s", operation.Run.State, model.StateComplete)
	}
	requireLeaseState(t, rt, created.ID, testRouteID, false)

	events := rt.Journal.Events()
	last := events[len(events)-1]
	if last.Kind != "complete" || last.Payload != testRouteID {
		t.Fatalf("last journal event = %s/%s, want complete/%s", last.Kind, last.Payload, testRouteID)
	}

	// A terminal run cannot be completed again, and the freed route stays free.
	if _, err := rt.Complete(created.ID, testRouteID); err == nil {
		t.Fatal("completing a terminal run must fail")
	}
	requireLeaseState(t, rt, created.ID, testRouteID, false)
}
