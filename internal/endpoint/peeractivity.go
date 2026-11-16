package endpoint

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
)

// What a peer reports about itself over the SAP status endpoint (SAP 0.3.1, section 8.3: an agent adds fields of its
// own): the status reply is the heartbeat-shaped JSON plus, when the peer reports, the object program_status with
// state and kind. The report is the peer's own and unsigned: it is information, never a grant.
const (
	// PeerActivityTimeout is how long one status request waits for an answer (and how long it waits for a free slot).
	PeerActivityTimeout = time.Second
	// PeerActivityConcurrency is the most status requests one endpoint has in flight at once.
	PeerActivityConcurrency = 8
	// PeerActivityCacheTTL is how long an answer for one name is reused.
	PeerActivityCacheTTL = 5 * time.Second
)

// PeerOutcome is how a status request ended.
type PeerOutcome string

const (
	// PeerReported: the peer answered with a recognised state.
	PeerReported PeerOutcome = "reported"
	// PeerNotReported: the peer answered with no recognised state. Never read this as idle.
	PeerNotReported PeerOutcome = "not_reported"
	// PeerNoAnswer: no answer in time, or no responder.
	PeerNoAnswer PeerOutcome = "no_answer"
	// PeerRefused: the server refused this endpoint's request by permissions.
	PeerRefused PeerOutcome = "refused"
)

// PeerState is what PeerActivity learned. State and Kind are set only when Outcome is PeerReported, and Kind only with
// State "blocked". InstanceID is the answering instance's id (JSON use; empty when nobody answered or it gave none).
// Nothing else of the reply is kept: msg, title, progress and the rest are never read.
type PeerState struct {
	Outcome    PeerOutcome
	State      string
	Kind       string
	InstanceID string
}

// Display is the short form a person or a model reads: "working", "blocked(question)", "-" (answered, not reported:
// not idle) or "?" (no answer in time, or refused).
func (p PeerState) Display() string {
	switch p.Outcome {
	case PeerReported:
		if p.Kind != "" {
			return p.State + "(" + p.Kind + ")"
		}
		return p.State
	case PeerNotReported:
		return "-"
	}
	return "?"
}

var (
	peerStates = map[string]bool{"idle": true, "working": true, "done": true, "blocked": true, "error": true}
	peerKinds  = map[string]bool{"permission": true, "question": true, "auth": true}
)

// parsePeerStatus reads a status reply body: program_status.state and program_status.kind. A non-object
// program_status, or a state outside the five values, means not reported; a kind outside the three values, or without
// blocked, is dropped; unknown members and keys (top-level state and kind included) are ignored.
func parsePeerStatus(body []byte) PeerState {
	out := PeerState{Outcome: PeerNotReported}
	var raw map[string]json.RawMessage
	if json.Unmarshal(body, &raw) != nil {
		return out
	}
	str := func(m map[string]json.RawMessage, k string) string {
		var s string
		if v, ok := m[k]; ok && json.Unmarshal(v, &s) == nil {
			return s
		}
		return ""
	}
	if id := str(raw, "instance_id"); len(id) <= 128 {
		out.InstanceID = config.Clean(id)
	}
	var ps map[string]json.RawMessage
	if v, ok := raw["program_status"]; !ok || json.Unmarshal(v, &ps) != nil || ps == nil {
		return out
	}
	state := str(ps, "state")
	if !peerStates[state] {
		return out
	}
	out.Outcome, out.State = PeerReported, state
	if k := str(ps, "kind"); state == "blocked" && peerKinds[k] {
		out.Kind = k
	}
	return out
}

type peerCacheEntry struct {
	at time.Time
	st PeerState
}

// peerActivity is an endpoint's status-request state: a limit on requests in flight and a short per-name cache.
type peerActivity struct {
	once  sync.Once
	sem   chan struct{}
	mu    sync.Mutex
	cache map[string]peerCacheEntry
}

func (p *peerActivity) slots() chan struct{} {
	p.once.Do(func() { p.sem = make(chan struct{}, PeerActivityConcurrency) })
	return p.sem
}

// peerStatusSubject is the status subject of the endpoint a recipient argument names, resolved as a send resolves its
// recipient (DiscoverRecipient): a legacy endpoint name under this owner, or a three-token endpoint written
// canonically or flat, which carries its own owner.
func (e *Endpoint) peerStatusSubject(arg string) (string, bool) {
	arg = sap.KeyOf(arg)
	if config.ValidName(arg) {
		return sap.For(e.cfg.Owner, arg).Status, true
	}
	id, err := sap.FromFlat(arg)
	if err != nil {
		return "", false
	}
	return id.Subjects().Status, true
}

// PeerActivity asks the endpoint name (a directory key, as who shows it) what it is doing: a SAP status request with an
// empty body, PeerActivityTimeout, at most PeerActivityConcurrency in flight per endpoint, an answer reused for
// PeerActivityCacheTTL. It never panics and never waits longer than about two timeouts (one for a free slot, one for
// the answer), less when ctx ends first; a failure is an outcome, not an error. The report is the peer's own and
// unsigned.
func (e *Endpoint) PeerActivity(ctx context.Context, name string) (st PeerState) {
	defer func() {
		if recover() != nil {
			st = PeerState{Outcome: PeerNoAnswer}
		}
	}()
	subj, ok := e.peerStatusSubject(name)
	if !ok || e.nc == nil {
		return PeerState{Outcome: PeerNoAnswer}
	}
	pa := &e.peers
	pa.mu.Lock()
	if c, ok := pa.cache[subj]; ok && time.Since(c.at) < PeerActivityCacheTTL {
		pa.mu.Unlock()
		return c.st
	}
	pa.mu.Unlock()

	slots := pa.slots()
	wait := time.NewTimer(PeerActivityTimeout)
	defer wait.Stop()
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-wait.C:
		return PeerState{Outcome: PeerNoAnswer}
	case <-ctx.Done():
		return PeerState{Outcome: PeerNoAnswer}
	}

	rctx, cancel := context.WithTimeout(ctx, PeerActivityTimeout)
	defer cancel()
	started := time.Now()
	req := nats.NewMsg(subj)
	// SAP 0.3 §12: a status request carries Agent-Sender, signed when this endpoint holds the seed. With no key it goes
	// unsigned: a status request stays answerable (§13.9) and PeerActivity never errors.
	if h, err := e.id.Sign(subj, nil, e.key, time.Now(), ""); err == nil {
		req.Header.Set(sap.HeaderName, h.Encode())
	}
	m, err := e.nc.RequestMsgWithContext(rctx, req)
	switch {
	case err == nil:
		st = parsePeerStatus(m.Data)
	case ctx.Err() != nil:
		return PeerState{Outcome: PeerNoAnswer} // the caller gave up: not an answer about the peer, not cached
	case errors.Is(err, nats.ErrPermissionViolation) || e.refusals.violatedSince(subj, started):
		st = PeerState{Outcome: PeerRefused}
	default:
		st = PeerState{Outcome: PeerNoAnswer}
	}
	pa.mu.Lock()
	if pa.cache == nil || len(pa.cache) > 1024 {
		pa.cache = map[string]peerCacheEntry{}
	}
	pa.cache[subj] = peerCacheEntry{at: time.Now(), st: st}
	pa.mu.Unlock()
	return st
}
