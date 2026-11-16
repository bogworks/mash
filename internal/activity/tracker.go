package activity

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Options configures a Tracker.
type Options struct {
	Env       func(string) string // process environment reader; nil = os.Getenv
	Dir       string              // directory holding RecordFile ("" = no hooks source)
	Off       bool                // MASH_ACTIVITY=off: nothing runs, Current is always absent
	Now       func() time.Time    // nil = time.Now
	HooksPoll time.Duration       // how often RecordFile is re-read; 0 = 1s
	Runtime   RuntimeSource       // nil = no runtime source; the caller builds it (the herdr adapter is one)
}

// Tracker combines the runtime and hooks sources into one Activity.
type Tracker struct {
	o   Options
	env func(string) string
	now func() time.Time

	wg      sync.WaitGroup // every goroutine the tracker starts; tests wait on it
	spawned atomic.Int32   // how many goroutines it started
	started atomic.Bool

	// Used only by the hooks poll (one goroutine, or a test calling pollHooks directly).
	// The mod time can be coarse and a rewrite can keep the size, so a new file (rename gives a new inode) counts too.
	hkSeen bool
	hkInfo os.FileInfo

	mu    sync.Mutex
	rt    runtimeView
	hkRec Record
	hkOK  bool // hkRec is from this process's harness
	// last is the combined answer the last evaluation produced; Since in it is the time it first appeared.
	last   Activity
	lastOK bool
}

// New returns a Tracker. It does nothing until Start.
func New(o Options) *Tracker {
	t := &Tracker{o: o, env: o.Env, now: o.Now}
	if t.env == nil {
		t.env = os.Getenv
	}
	if t.now == nil {
		t.now = time.Now
	}
	return t
}

func (t *Tracker) spawn(f func()) {
	t.wg.Add(1)
	t.spawned.Add(1)
	go func() {
		defer t.wg.Done()
		f()
	}()
}

// Start begins reading the configured sources and returns at once. It starts no goroutine when Off, when neither
// source is configured, or when called a second time. Every goroutine stops when ctx is done.
func (t *Tracker) Start(ctx context.Context) {
	if t.o.Off || !t.started.CompareAndSwap(false, true) {
		return
	}
	if t.o.Runtime != nil {
		t.spawn(func() { t.o.Runtime.Run(ctx, t.setRuntime) })
	}
	if t.o.Dir != "" {
		poll := t.o.HooksPoll
		if poll <= 0 {
			poll = time.Second
		}
		t.spawn(func() {
			tick := time.NewTicker(poll)
			defer tick.Stop()
			for {
				t.pollHooks()
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
				}
			}
		})
	}
}

// Wait returns when every goroutine Start began has stopped. Cancel the context given to Start first. It returns at
// once when nothing was started.
func (t *Tracker) Wait() { t.wg.Wait() }

// Current returns the combined activity; ok is false when it is absent. Absent is not idle.
func (t *Tracker) Current() (Activity, bool) {
	if t.o.Off {
		return Activity{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.evalLocked()
	return t.last, t.lastOK
}

// evalLocked recomputes the combined answer from the latest views. Since moves only when (State, Kind) changes;
// a change of Source alone keeps it. It runs on every view change and on Current, and the stored answer makes
// repeated runs agree.
func (t *Tracker) evalLocked() {
	now := t.now().UTC()
	hk := t.hooksViewAt(now)
	a, ok := combine(t.rt, hk)
	if !ok {
		t.last, t.lastOK = Activity{}, false
		return
	}
	if t.lastOK && t.last.State == a.State && t.last.Kind == a.Kind {
		t.last.Source = a.Source
		return
	}
	// A hooks-only answer began when the hook said so (a record may be hours old at start); a runtime answer began
	// when this process saw it.
	a.Since = now
	if a.Source == "hooks" && !hk.At.After(now) {
		a.Since = hk.At.UTC()
	}
	t.last, t.lastOK = a, true
}

// setRuntime records what the runtime source says: Available false for unavailable, State empty for "not an agent".
func (t *Tracker) setRuntime(r RuntimeReport) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !r.Available {
		r.State, r.Version = "", ""
	}
	t.rt.Available, t.rt.State, t.rt.Version = r.Available, r.State, r.Version
	if r.Available && r.State == StateBlocked {
		t.rt.BlockedSeen = t.now().UTC()
	}
	t.evalLocked()
}

// Describe is one line for the local status tool.
func (t *Tracker) Describe() string {
	if t.o.Off {
		return "activity: off"
	}
	a, ok := t.Current()
	t.mu.Lock()
	rt, hk := t.rt, t.hkOK
	hkView := t.hooksViewAt(t.now().UTC())
	t.mu.Unlock()
	src := t.o.Runtime
	hasHooks := t.o.Dir != ""

	if !ok {
		var why []string
		switch {
		case src == nil:
			why = append(why, "no runtime source")
		case !rt.Available:
			why = append(why, src.Name()+" unavailable")
		default:
			why = append(why, src.Name()+": not an agent pane")
		}
		switch {
		case !hasHooks, !hk:
			why = append(why, "no hook record")
		default:
			why = append(why, "hook record stale")
		}
		return "activity: absent (" + strings.Join(why, ", ") + ")"
	}
	var parts []string
	if src != nil {
		if rt.Available {
			parts = append(parts, src.Describe(RuntimeReport{Available: true, State: rt.State, Version: rt.Version}))
		} else {
			parts = append(parts, src.Name()+" unavailable")
		}
	}
	if hasHooks {
		if hkView.Present {
			parts = append(parts, "hooks ok")
		} else {
			parts = append(parts, "hooks no record")
		}
	}
	s := string(a.State)
	if a.Kind != "" {
		s += " (" + string(a.Kind) + ")"
	}
	return fmt.Sprintf("activity: %s: %s (source %s)", strings.Join(parts, ", "), s, a.Source)
}
