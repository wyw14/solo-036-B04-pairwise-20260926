package route

import (
	"fmt"
	"github.com/wyw14/solo-036/internal/model"
	"time"
)

func (c *Coordinator) Reserve(route, runID string, epoch int64) (model.RouteLease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if owner, ok := c.owners[route]; ok && owner.RunID != runID {
		return model.RouteLease{}, fmt.Errorf("route owned by %s", owner.RunID)
	}
	lease := model.RouteLease{Route: route, RunID: runID, Epoch: epoch, AcquiredAt: time.Now().UTC()}
	c.owners[route] = lease
	return lease, nil
}
func (c *Coordinator) Owner(route string) (model.RouteLease, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	lease, ok := c.owners[route]
	return lease, ok
}

// ReleaseIfOwner releases the route only when runID is the current lease
// holder. It returns an error instead of silently doing nothing when the
// caller does not hold the route, so that a completion against the wrong
// route can never leave a lease stranded on a finished run.
func (c *Coordinator) ReleaseIfOwner(route, runID string) (model.RouteLease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	lease, ok := c.owners[route]
	if !ok {
		return model.RouteLease{}, fmt.Errorf("route %s is not reserved", route)
	}
	if lease.RunID != runID {
		return model.RouteLease{}, fmt.Errorf("route %s is fenced to run %s", route, lease.RunID)
	}
	delete(c.owners, route)
	delete(c.progress, runID)
	return lease, nil
}

func (c *Coordinator) Release(route, runID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if owner, ok := c.owners[route]; ok && owner.RunID == runID {
		delete(c.owners, route)
		delete(c.progress, runID)
	}
}
