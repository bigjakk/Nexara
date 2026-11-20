package scheduler

import (
	"maps"
	"slices"
	"sync"

	"github.com/google/uuid"
)

// clusterPasses records which clusters one engine has a pass running on.
//
// The engines that keep one handle each cluster in a goroutine of its own, and
// cmd/nexara starts their next pass without waiting for the last. They used to
// handle the clusters in turn, inside one pass that the next tick waited for, so
// a cluster that stopped answering held back every other cluster's work for as
// long as its own calls took to fail: a DRS migration waits up to 30 minutes,
// and a read of a cluster that hangs waits the client's whole timeout. This
// record is what keeps two passes off one cluster now.
//
// The zero value is ready to use.
type clusterPasses struct {
	mu      sync.Mutex
	running map[uuid.UUID]int // the passes running on each cluster; absent when none
}

// claim starts a pass on cluster unless one is running there already, and
// reports whether it did.
func (p *clusterPasses) claim(cluster uuid.UUID) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running[cluster] > 0 {
		return false
	}
	p.startLocked(cluster)
	return true
}

// join starts a pass on cluster whether or not one is running there, for work
// that is this pass's alone, such as rows it has claimed in the database.
func (p *clusterPasses) join(cluster uuid.UUID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.startLocked(cluster)
}

func (p *clusterPasses) startLocked(cluster uuid.UUID) {
	if p.running == nil {
		p.running = make(map[uuid.UUID]int)
	}
	p.running[cluster]++
}

// done ends a pass that claim or join started.
func (p *clusterPasses) done(cluster uuid.UUID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.running[cluster]--
	if p.running[cluster] <= 0 {
		delete(p.running, cluster)
	}
}

// busy lists the clusters with a pass running, nil when there are none.
func (p *clusterPasses) busy() []uuid.UUID {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Collect(maps.Keys(p.running))
}

// goClusterPass runs pass in a goroutine of its own, counted in wg, and ends
// the pass on cluster that the caller claimed or joined in passes when it
// returns, panic or not.
func (s *Scheduler) goClusterPass(wg *sync.WaitGroup, passes *clusterPasses, engine string, cluster uuid.UUID, pass func()) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer passes.done(cluster)
		// Its own, because the recover in the method that started it is on
		// another goroutine.
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("scheduler cluster pass panicked", "engine", engine, "cluster_id", cluster, "panic", r)
			}
		}()
		pass()
	}()
}
