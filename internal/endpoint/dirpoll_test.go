package endpoint

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type dpClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *dpClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *dpClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

var dpT0 = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

func baseState() DirState {
	return DirState{Created: dpT0.Add(-time.Hour), Msgs: 5, LastSeq: 9, LastTime: dpT0.Add(-time.Minute)}
}

// poller returns a poller whose random draws are all r (so the forced reload is due at 20 min * (0.75 + 0.5 r)).
func poller(c *dpClock, r float64) *DirPoller {
	return NewDirPoller(c.now, func() float64 { return r })
}

type counts struct{ state, load int }

func tick(t *testing.T, p *DirPoller, st DirState, stErr, loadErr error) (counts, bool, error) {
	t.Helper()
	var n counts
	reloaded, err := p.Tick(context.Background(),
		func(context.Context) (DirState, error) { n.state++; return st, stErr },
		func(context.Context) error { n.load++; return loadErr })
	return n, reloaded, err
}

func TestDirPollerUnchangedSkipsLoad(t *testing.T) {
	c := &dpClock{t: dpT0}
	p := poller(c, 0.5)
	n, re, err := tick(t, p, baseState(), nil, nil)
	if n != (counts{1, 1}) || !re || err != nil {
		t.Fatalf("first tick must reload: %+v %v %v", n, re, err)
	}
	for i := range 5 {
		c.add(15 * time.Second)
		n, re, err = tick(t, p, baseState(), nil, nil)
		if n != (counts{1, 0}) || re || err != nil {
			t.Fatalf("unchanged tick %d: %+v %v %v", i, n, re, err)
		}
	}
}

func TestDirPollerEachFieldTriggersLoad(t *testing.T) {
	for name, mut := range map[string]func(*DirState){
		"created":  func(s *DirState) { s.Created = s.Created.Add(time.Second) },
		"msgs":     func(s *DirState) { s.Msgs++ },
		"last seq": func(s *DirState) { s.LastSeq++ },
		"last ts":  func(s *DirState) { s.LastTime = s.LastTime.Add(time.Nanosecond) },
	} {
		t.Run(name, func(t *testing.T) {
			c := &dpClock{t: dpT0}
			p := poller(c, 0.5)
			tick(t, p, baseState(), nil, nil)
			c.add(time.Second)
			st := baseState()
			mut(&st)
			if n, re, _ := tick(t, p, st, nil, nil); n != (counts{1, 1}) || !re {
				t.Fatalf("changed %s must reload: %+v %v", name, n, re)
			}
			c.add(time.Second)
			if n, re, _ := tick(t, p, st, nil, nil); n.load != 0 || re {
				t.Fatalf("the new state is recorded: %+v %v", n, re)
			}
		})
	}
}

func TestDirPollerLoadErrorRetries(t *testing.T) {
	c := &dpClock{t: dpT0}
	p := poller(c, 0.5)
	boom := errors.New("boom")
	n, re, err := tick(t, p, baseState(), nil, boom)
	if n != (counts{1, 1}) || !re || !errors.Is(err, boom) {
		t.Fatalf("%+v %v %v", n, re, err)
	}
	// the failed load recorded nothing: the next poll reloads although the state is the same
	if n, re, err = tick(t, p, baseState(), nil, nil); n != (counts{1, 1}) || !re || err != nil {
		t.Fatalf("retry: %+v %v %v", n, re, err)
	}
	// and after a failed reload of a changed state, the old state is not taken as current
	st := baseState()
	st.LastSeq++
	tick(t, p, st, nil, boom)
	if n, re, _ = tick(t, p, st, nil, nil); n.load != 1 || !re {
		t.Fatalf("retry after failed reload: %+v %v", n, re)
	}
}

func TestDirPollerStateErrorRetries(t *testing.T) {
	c := &dpClock{t: dpT0}
	p := poller(c, 0.5)
	boom := errors.New("no state")
	n, re, err := tick(t, p, DirState{}, boom, nil)
	if n != (counts{1, 0}) || re || !errors.Is(err, boom) {
		t.Fatalf("%+v %v %v", n, re, err)
	}
	if n, re, err = tick(t, p, baseState(), nil, nil); n != (counts{1, 1}) || !re || err != nil {
		t.Fatalf("%+v %v %v", n, re, err)
	}
	// a state error after a load keeps the record: the next good, unchanged state does not reload
	tick(t, p, DirState{}, boom, nil)
	if n, re, _ = tick(t, p, baseState(), nil, nil); n.load != 0 || re {
		t.Fatalf("state error must not drop the record: %+v %v", n, re)
	}
}

// A change during the load is seen by the next poll: the state recorded is the one read before the load.
func TestDirPollerRecordsStateReadBeforeLoad(t *testing.T) {
	c := &dpClock{t: dpT0}
	p := poller(c, 0.5)
	before := baseState()
	after := before
	after.LastSeq++
	p.Tick(context.Background(), func(context.Context) (DirState, error) { return before, nil },
		func(context.Context) error { return nil })
	if n, re, _ := tick(t, p, after, nil, nil); n.load != 1 || !re {
		t.Fatalf("the state that moved during the load must reload: %+v %v", n, re)
	}
}

func TestDirPollerForcedReloadDue(t *testing.T) {
	old := DirectoryFullReload
	DirectoryFullReload = 20 * time.Minute
	t.Cleanup(func() { DirectoryFullReload = old })
	for _, r := range []float64{0, 0.5, 0.999} {
		c := &dpClock{t: dpT0}
		p := poller(c, r)
		due := time.Duration(float64(20*time.Minute) * (0.75 + 0.5*r))
		if due < 15*time.Minute || due > 25*time.Minute {
			t.Fatalf("due %v outside 20 min +-25%%", due)
		}
		tick(t, p, baseState(), nil, nil)
		c.add(due - time.Second)
		if n, re, _ := tick(t, p, baseState(), nil, nil); n.load != 0 || re {
			t.Fatalf("r=%v: reloaded before due: %+v", r, n)
		}
		c.add(2 * time.Second)
		if n, re, _ := tick(t, p, baseState(), nil, nil); n != (counts{1, 1}) || !re {
			t.Fatalf("r=%v: not reloaded when due: %+v %v", r, n, re)
		}
		// and the clock restarts from that reload
		c.add(time.Minute)
		if n, re, _ := tick(t, p, baseState(), nil, nil); n.load != 0 || re {
			t.Fatalf("r=%v: reloaded again at once: %+v", r, n)
		}
	}
}

func TestDirPollerForcedReloadSpreadAcrossEndpoints(t *testing.T) {
	seen := map[time.Duration]bool{}
	for range 50 {
		p := NewDirPoller(nil, nil)
		d := time.Duration(float64(20*time.Minute) * p.fullFactor)
		if d < 15*time.Minute || d >= 25*time.Minute {
			t.Fatalf("forced reload due after %v", d)
		}
		seen[d] = true
	}
	if len(seen) < 40 {
		t.Fatalf("due times are not per-endpoint random: %d distinct of 50", len(seen))
	}
}

func TestDirPollerJitterBounds(t *testing.T) {
	p := NewDirPoller(nil, nil)
	every := 15 * time.Second
	lo, hi := time.Duration(1<<62), time.Duration(0)
	for range 20000 {
		w := p.Wait(every)
		if w < every*3/4 || w > every*5/4 {
			t.Fatalf("Wait %v outside [0.75, 1.25] x %v", w, every)
		}
		lo, hi = min(lo, w), max(hi, w)
		f := p.FirstWait(every)
		if f < 0 || f >= every {
			t.Fatalf("FirstWait %v outside [0, %v)", f, every)
		}
	}
	if lo > every*8/10 || hi < every*12/10 {
		t.Fatalf("jitter does not spread: %v .. %v", lo, hi)
	}
	// the extremes of the random source
	if w := NewDirPoller(nil, func() float64 { return 0 }).Wait(every); w != every*3/4 {
		t.Fatalf("%v", w)
	}
	if w := NewDirPoller(nil, func() float64 { return 0 }).FirstWait(every); w != 0 {
		t.Fatalf("%v", w)
	}
}

func TestDirPollerConcurrent(t *testing.T) {
	p := NewDirPoller(nil, rand.Float64)
	var loads atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				_, _ = p.Tick(context.Background(),
					func(context.Context) (DirState, error) { return baseState(), nil },
					func(context.Context) error { loads.Add(1); return nil })
				p.Wait(time.Second)
				p.FirstWait(time.Second)
			}
		}()
	}
	wg.Wait()
	if loads.Load() < 1 {
		t.Fatal("never loaded")
	}
}

func TestParseDirState(t *testing.T) {
	body := `{"type":"io.nats.jetstream.api.v1.stream_info_response","created":"2026-11-02T10:00:00.123456789Z",` +
		`"state":{"messages":4,"bytes":99,"first_seq":1,"last_seq":7,"last_ts":"2026-11-02T11:00:00Z","consumer_count":0}}`
	st, err := ParseDirState([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	want := DirState{Created: time.Date(2026, 11, 2, 10, 0, 0, 123456789, time.UTC), Msgs: 4, LastSeq: 7,
		LastTime: time.Date(2026, 11, 2, 11, 0, 0, 0, time.UTC)}
	if !st.equal(want) {
		t.Fatalf("%+v", st)
	}
	if _, err := ParseDirState([]byte(`{"error":{"code":404,"description":"stream not found"}}`)); err == nil || err.Error() != "stream not found" {
		t.Fatalf("%v", err)
	}
	if _, err := ParseDirState([]byte(`not json`)); err == nil {
		t.Fatal("garbage must fail")
	}
}
