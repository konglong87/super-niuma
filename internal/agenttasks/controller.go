package agenttasks

import (
	"context"
	"sync"
)

type Controller struct {
	mu      sync.Mutex
	cancels map[uint64]context.CancelFunc
}

func NewController() *Controller {
	return &Controller{cancels: map[uint64]context.CancelFunc{}}
}

func (c *Controller) Register(taskID uint64, cancel context.CancelFunc) {
	if c == nil || taskID == 0 || cancel == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancels == nil {
		c.cancels = map[uint64]context.CancelFunc{}
	}
	c.cancels[taskID] = cancel
}

func (c *Controller) Unregister(taskID uint64) {
	if c == nil || taskID == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cancels, taskID)
}

func (c *Controller) Cancel(taskID uint64) bool {
	if c == nil || taskID == 0 {
		return false
	}
	c.mu.Lock()
	cancel, ok := c.cancels[taskID]
	if ok {
		delete(c.cancels, taskID)
	}
	c.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

func (c *Controller) Active(taskID uint64) bool {
	if c == nil || taskID == 0 {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.cancels[taskID]
	return ok
}
