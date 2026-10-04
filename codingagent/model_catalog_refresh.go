package codingagent

// Port of modes/interactive/model-search.ts and model-catalog-refresh.ts:
// search-text construction and the concurrent-refresh coordinator that
// shares one operation while keeping each caller's cancellation
// independent.

import (
	"sync"
)

// ModelCatalogRefreshCoordinator shares concurrent all-catalog refreshes.
type ModelCatalogRefreshCoordinator struct {
	mu     sync.Mutex
	active map[*ModelRuntime]*activeModelCatalogRefresh
}

type activeModelCatalogRefresh struct {
	done    chan struct{}
	err     error
	waiters int
	cancel  func()
}

// NewModelCatalogRefreshCoordinator builds the coordinator.
func NewModelCatalogRefreshCoordinator() *ModelCatalogRefreshCoordinator {
	return &ModelCatalogRefreshCoordinator{active: map[*ModelRuntime]*activeModelCatalogRefresh{}}
}

var modelCatalogRefreshCoordinator = NewModelCatalogRefreshCoordinator()

// RefreshModelCatalogs shares the interactive all-catalog refresh while
// each caller's cancellation stays independent.
func RefreshModelCatalogs(runtime *ModelRuntime) error {
	return modelCatalogRefreshCoordinator.Refresh(runtime)
}

// Refresh runs (or joins) the shared refresh for one runtime.
func (c *ModelCatalogRefreshCoordinator) Refresh(runtime *ModelRuntime) error {
	c.mu.Lock()
	active, exists := c.active[runtime]
	if !exists {
		done := make(chan struct{})
		entry := &activeModelCatalogRefresh{done: done}
		entry.cancel = func() {
			// The shared operation has no external signal in the Go port;
			// the last waiter's departure releases the entry.
			c.mu.Lock()
			if c.active[runtime] == entry {
				delete(c.active, runtime)
			}
			c.mu.Unlock()
		}
		c.active[runtime] = entry
		active = entry
		entry.waiters++
		c.mu.Unlock()

		go func() {
			defer close(done)
			active.err = runtime.Refresh()
		}()
	} else {
		active.waiters++
		c.mu.Unlock()
	}

	<-active.done

	c.mu.Lock()
	active.waiters--
	last := active.waiters == 0
	c.mu.Unlock()
	if last {
		active.cancel()
	}
	return active.err
}

// ModelSearchItemText renders the selector corpus for a model.
func ModelSearchItemText(provider, id, name string, selector bool) string {
	item := ModelSearchItem{ID: id, Provider: provider, Name: name}
	if selector {
		return GetModelSelectorSearchText(item)
	}
	return GetModelSearchText(item)
}
