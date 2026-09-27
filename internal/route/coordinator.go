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
func (c *Coordinator) LeaseOf(runID string) (model.RouteLease, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, lease := range c.owners {
		if lease.RunID == runID {
			return lease, true
		}
	}
	return model.RouteLease{}, false
}
func (c *Coordinator) Release(route, runID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if owner, ok := c.owners[route]; ok && owner.RunID == runID {
		delete(c.owners, route)
		delete(c.progress, runID)
	}
}
