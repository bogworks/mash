package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
)

// These tests pin the guarantee in docs/operations.md: when its stdin reaches EOF, mash serve stops, releases the
// endpoint lease and exits; an EOF during the first open is seen as soon as that open returns (bounded by the open
// timeout); and a serve that has lost its lease never writes it again.

// stdinEOFExitBound is how long serve may take to exit after its stdin closes. The longest step of the shutdown is
// endpoint.Close's lease release, bounded by endpoint.releaseTimeout (3 s, internal/endpoint/endpoint.go); the rest
// (cancel, join the worker, stop the SAP service) is local. 5 s leaves 2 s of margin and is the figure documented.
const stdinEOFExitBound = 5 * time.Second

// leaseInstance returns the instance id that holds the lease named key, and whether the key exists.
func leaseInstance(t *testing.T, w *world, key string) (string, bool) {
	t.Helper()
	e, err := leaseKV(t, w).Get(context.Background(), key)
	if err != nil {
		return "", false
	}
	var v struct {
		Instance string `json:"instance_id"`
	}
	if err := json.Unmarshal(e.Value(), &v); err != nil {
		t.Fatalf("lease value %q: %v", e.Value(), err)
	}
	return v.Instance, true
}

// EOF while holding the lease: serve exits 0 within the bound and the lease is released, so an unrelated serve of the
// same name takes it at once.
func TestServeStdinEOFWhileHoldingReleasesLeaseAndExits(t *testing.T) {
	setTiming(t, time.Hour, 20*time.Second) // a waiting serve would not retry for an hour: only a freed lease passes
	w := newWorld(t, "a")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	var held string
	eventually(t, 5*time.Second, "serve holds the lease", func() bool {
		id, ok := leaseInstance(t, w, tAddr("a").Flat())
		held = id
		return ok
	})

	start := time.Now()
	s.in.Close()
	s.exited(t, stdinEOFExitBound)
	if took := time.Since(start); took > stdinEOFExitBound {
		t.Fatalf("serve took %s to exit after EOF, bound %s", took, stdinEOFExitBound)
	}
	if code := <-s.done; code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	s.done <- 0
	if id, ok := leaseInstance(t, w, tAddr("a").Flat()); ok {
		t.Fatalf("the lease is still held (by %s) after serve exited", id)
	}

	// Another serve of the same name (no session identity in tests, so no same-session takeover) binds on its first
	// open: no waiting, no retry.
	attempts := acquireAttempts.Load()
	b := startServe(t, w.env["a"])
	b.initialize(t)
	b.tools(t)
	eventually(t, 5*time.Second, "the next serve takes the freed lease", func() bool {
		id, ok := leaseInstance(t, w, tAddr("a").Flat())
		return ok && id != held
	})
	if txt := resultText(b.call(t, 5, "status", `{}`)); !strings.Contains(txt, "connection: OK") {
		t.Fatalf("the next serve's status: %s", txt)
	}
	if n := acquireAttempts.Load(); n != attempts {
		t.Fatalf("the next serve needed %d retries; the lease was not free at once", n-attempts)
	}
}

// EOF during the first open: serve acts on it once that open returns, never later than the open bound, and does not
// keep a lease.
func TestServeStdinEOFDuringFirstOpen(t *testing.T) {
	const bound = 400 * time.Millisecond
	t.Run("open runs to its bound", func(t *testing.T) {
		setTiming(t, time.Hour, bound)
		w := newWorld(t, "a")
		inOpen := make(chan struct{})
		openEndpoint = func(ctx context.Context, cfg config.Config) (*endpoint.Endpoint, error) {
			close(inOpen)
			<-ctx.Done() // an open that never completes: only its bound ends it
			return nil, ctx.Err()
		}
		s := startServe(t, w.env["a"])
		<-inOpen
		start := time.Now()
		s.in.Close()
		s.exited(t, bound+stdinEOFExitBound)
		if took := time.Since(start); took > bound+2*time.Second {
			t.Fatalf("serve took %s to exit, want within the open bound %s plus slack", took, bound)
		}
		if id, ok := leaseInstance(t, w, tAddr("a").Flat()); ok {
			t.Fatalf("lease held by %s after exit", id)
		}
	})
	t.Run("open binds the lease after EOF", func(t *testing.T) {
		setTiming(t, time.Hour, 20*time.Second)
		endpoint.LeaseRefresh = 50 * time.Millisecond
		w := newWorld(t, "a")
		inOpen, release := make(chan struct{}), make(chan struct{})
		openEndpoint = func(ctx context.Context, cfg config.Config) (*endpoint.Endpoint, error) {
			close(inOpen)
			<-release
			return endpoint.Open(ctx, cfg)
		}
		s := startServe(t, w.env["a"])
		<-inOpen
		s.in.Close() // EOF while Open is in flight
		select {
		case <-s.done:
			t.Fatal("serve exited before its first open returned")
		case <-time.After(200 * time.Millisecond):
		}
		start := time.Now()
		close(release)
		s.exited(t, stdinEOFExitBound)
		if took := time.Since(start); took > stdinEOFExitBound {
			t.Fatalf("serve took %s to exit after its open returned", took)
		}
		if id, ok := leaseInstance(t, w, tAddr("a").Flat()); ok {
			t.Fatalf("serve left the lease it bound after EOF (held by %s)", id)
		}
		// It does not come back: several refresh intervals later the key is still absent.
		time.Sleep(5 * endpoint.LeaseRefresh)
		if id, ok := leaseInstance(t, w, tAddr("a").Flat()); ok {
			t.Fatalf("the lease reappeared (held by %s)", id)
		}
	})
}

// A serve superseded by a newer serve of its session never renews the lease, and its exit leaves the new holder's.
func TestServeSupersededNeverRefreshesThenEOFLeavesHolder(t *testing.T) {
	setTiming(t, time.Hour, 20*time.Second)
	endpoint.LeaseRefresh = 50 * time.Millisecond
	asSession(t)
	w := newWorld(t, "a")
	old := startServe(t, w.env["a"])
	old.initialize(t)
	old.tools(t)
	var oldID string
	eventually(t, 5*time.Second, "the old serve holds the lease", func() bool {
		id, ok := leaseInstance(t, w, tAddr("a").Flat())
		oldID = id
		return ok
	})

	fresh := startServe(t, w.env["a"])
	fresh.initialize(t)
	fresh.tools(t)
	var newID string
	eventually(t, 10*time.Second, "the new serve takes the lease", func() bool {
		id, ok := leaseInstance(t, w, tAddr("a").Flat())
		newID = id
		return ok && id != oldID
	})
	eventually(t, 5*time.Second, "the old serve steps down", func() bool {
		return strings.Contains(resultText(old.call(t, 20, "status", `{}`)), "SUPERSEDED")
	})

	// Over many refresh intervals (the new holder rewrites the key every 50 ms, the old one would too) the key is only
	// ever the new holder's: the old serve has stopped refreshing, and has not taken the key back.
	deadline := time.Now().Add(15 * endpoint.LeaseRefresh)
	for time.Now().Before(deadline) {
		if id, ok := leaseInstance(t, w, tAddr("a").Flat()); !ok || id != newID {
			t.Fatalf("lease key is held by %q (present %v), want only the new serve %s", id, ok, newID)
		}
		time.Sleep(10 * time.Millisecond)
	}

	old.in.Close()
	old.exited(t, stdinEOFExitBound)
	if code := <-old.done; code != 0 {
		t.Fatalf("superseded serve exit code %d", code)
	}
	old.done <- 0
	deadline = time.Now().Add(15 * endpoint.LeaseRefresh)
	for time.Now().Before(deadline) {
		if id, ok := leaseInstance(t, w, tAddr("a").Flat()); !ok || id != newID {
			t.Fatalf("after the old serve exited the lease key is held by %q (present %v), want %s", id, ok, newID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
