package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Many endpoints never run more checks at once than the limit, and every one runs.
func TestEachBoundedNeverExceedsTheLimit(t *testing.T) {
	const n = 40
	var running, peak, done atomic.Int32
	var mu sync.Mutex
	seen := map[int]bool{}
	eachBounded(n, doctorParallel, func(i int) {
		cur := running.Add(1)
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		running.Add(-1)
		done.Add(1)
		mu.Lock()
		seen[i] = true
		mu.Unlock()
	})
	if got := peak.Load(); got > doctorParallel {
		t.Errorf("peak concurrency %d, limit %d", got, doctorParallel)
	}
	if done.Load() != n || len(seen) != n {
		t.Errorf("ran %d of %d (%d distinct)", done.Load(), n, len(seen))
	}
}
