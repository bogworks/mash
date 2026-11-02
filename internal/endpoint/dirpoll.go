package endpoint

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"sync"
	"time"
)

// DirectoryFullReload is the longest a running endpoint goes without a full directory reload, whatever the stream
// state says (ADR 0065). The moment it is due is jittered per endpoint, plus or minus 25 percent, so a stale view
// cannot outlive it and the endpoints do not all reload together.
var DirectoryFullReload = 20 * time.Minute

// DirState is what one unfiltered stream-info request says about the directory stream. Two equal states mean no
// record was written, changed or deleted in between (and the stream was not recreated: Created differs then).
type DirState struct {
	Created  time.Time
	Msgs     uint64
	LastSeq  uint64
	LastTime time.Time
}

// ParseDirState reads a stream-info reply (the body of an unfiltered request is a few hundred bytes). An API error in
// the reply is returned as an error carrying its description.
func ParseDirState(body []byte) (DirState, error) {
	var resp struct {
		Error *struct {
			Description string `json:"description"`
		} `json:"error"`
		Created time.Time `json:"created"`
		State   struct {
			Messages uint64    `json:"messages"`
			LastSeq  uint64    `json:"last_seq"`
			LastTS   time.Time `json:"last_ts"`
		} `json:"state"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return DirState{}, err
	}
	if resp.Error != nil {
		return DirState{}, errors.New(resp.Error.Description)
	}
	return DirState{Created: resp.Created, Msgs: resp.State.Messages, LastSeq: resp.State.LastSeq, LastTime: resp.State.LastTS}, nil
}

func (s DirState) equal(o DirState) bool {
	return s.Created.Equal(o.Created) && s.Msgs == o.Msgs && s.LastSeq == o.LastSeq && s.LastTime.Equal(o.LastTime)
}

// DirPoller decides, once per poll, whether the directory must be reloaded: only when the stream state moved since
// the last load, or the forced-reload age (DirectoryFullReload, jittered) is reached. It is safe for concurrent use.
type DirPoller struct {
	now func() time.Time
	rnd func() float64 // uniform in [0, 1)

	fullFactor float64 // this poller's draw: the forced reload is due DirectoryFullReload * fullFactor after a load

	mu       sync.Mutex
	have     bool
	last     DirState
	lastFull time.Time
}

// NewDirPoller returns a poller; nil now and rnd mean the wall clock and math/rand/v2.
func NewDirPoller(now func() time.Time, rnd func() float64) *DirPoller {
	if now == nil {
		now = time.Now
	}
	if rnd == nil {
		rnd = rand.Float64
	}
	return &DirPoller{now: now, rnd: rnd, fullFactor: 0.75 + 0.5*rnd()}
}

// Tick performs one poll. It calls state; if the state equals the one recorded at the last load and the forced-reload
// age is not reached, it returns (false, nil) after that one request. Otherwise it calls load, records the state read
// before the load (a change during the load is seen by the next poll) and the time, and returns (true, err). A failed
// state request, or a failed load, leaves the record unchanged so the next poll retries; a poller that never loaded
// reloads on its first Tick.
func (p *DirPoller) Tick(ctx context.Context, state func(context.Context) (DirState, error), load func(context.Context) error) (bool, error) {
	st, err := state(ctx)
	if err != nil {
		return false, err
	}
	p.mu.Lock()
	skip := p.have && st.equal(p.last) && p.now().Sub(p.lastFull) < time.Duration(float64(DirectoryFullReload)*p.fullFactor)
	p.mu.Unlock()
	if skip {
		return false, nil
	}
	started := p.now()
	if err := load(ctx); err != nil {
		return true, err
	}
	p.mu.Lock()
	p.have, p.last, p.lastFull = true, st, started
	p.mu.Unlock()
	return true, nil
}

// Wait is the wait before the next poll: every, jittered by plus or minus 25 percent.
func (p *DirPoller) Wait(every time.Duration) time.Duration {
	return time.Duration(float64(every) * (0.75 + 0.5*p.rnd()))
}

// FirstWait is the wait before the first poll: a random offset in [0, every), so endpoints started together spread out.
func (p *DirPoller) FirstWait(every time.Duration) time.Duration {
	return time.Duration(float64(every) * p.rnd())
}
