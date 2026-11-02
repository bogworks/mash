package endpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// OnlineWindow is how recently a heartbeat must have been seen (3 x 30 s, SAP §8.2).
var OnlineWindow = 90 * time.Second

// Presence is a directory record and whether its endpoint is online.
type Presence struct {
	Record   store.Record
	Online   bool
	LastSeen time.Time
}

// DirectoryPoll is how often a running endpoint re-reads the directory.
// Polling needs only stream info and direct gets: endpoints may not create
// consumers on the directory (one could exhaust or delete another's).
var DirectoryPoll = 15 * time.Second

// pollDirectory polls the directory until ctx ends: one small unfiltered stream-info request per poll, and a full
// reload only when the stream moved or DirectoryFullReload is due (ADR 0065). Polls are jittered.
func (e *Endpoint) pollDirectory(ctx context.Context, every time.Duration) {
	p := NewDirPoller(nil, nil)
	t := time.NewTimer(p.FirstWait(every))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := p.Tick(ctx, e.directoryState, e.loadDirectory); err != nil && ctx.Err() == nil {
				e.log.Print(err)
			}
			t.Reset(p.Wait(every))
		}
	}
}

// watchPresence records verified heartbeats from the owner's agents.
func (e *Endpoint) watchPresence(ctx context.Context) error {
	seen := sap.NewNonceSet(2 * sap.LiveSkew)
	sub, err := e.nc.Subscribe("agents.hb.*."+e.cfg.Owner+".*", func(m *nats.Msg) {
		name, ok := sap.NameFromSubject(m.Subject)
		if !ok {
			return
		}
		h, tr := sap.VerifyLive(m.Header.Get(sap.HeaderName), m.Subject, m.Data, time.Now(), seen)
		if tr != sap.TrustVerified {
			return
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.byUser[h.User] == name {
			e.lastSeen[name] = time.Now()
		}
	})
	if err != nil {
		return fmt.Errorf("mash: heartbeat subscription: %w", err)
	}
	go func() { <-ctx.Done(); _ = sub.Unsubscribe() }()
	go e.bootstrapPresence()
	return nil
}

// bootstrapPresence asks every SAP agent for its registration right after
// subscribing to heartbeats (subscribe-before-discover, SAP §8.5), so presence
// doesn't wait a full heartbeat interval. A registration counts only when its
// id_sig verifies and its key is the one the directory has for that name.
func (e *Endpoint) bootstrapPresence() {
	inbox := e.nc.NewInbox()
	sub, err := e.nc.SubscribeSync(inbox)
	if err != nil {
		return
	}
	defer sub.Unsubscribe()
	if err := e.nc.PublishRequest("$SRV.INFO."+sap.ServiceName, inbox, nil); err != nil {
		return
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m, err := sub.NextMsg(time.Until(deadline))
		if err != nil {
			return
		}
		e.applyPresence(m)
	}
}

// applyPresence records the agents a $SRV.INFO answer vouches for as seen now, when the answer is from this owner.
func (e *Endpoint) applyPresence(m *nats.Msg) {
	var info struct {
		Metadata  map[string]string `json:"metadata"`
		Endpoints []struct {
			Name    string `json:"name"`
			Subject string `json:"subject"`
		} `json:"endpoints"`
	}
	if json.Unmarshal(m.Data, &info) != nil || info.Metadata["owner"] != e.cfg.Owner {
		return
	}
	md := info.Metadata
	for _, ep := range info.Endpoints {
		if ep.Name != "prompt" {
			continue
		}
		name, ok := sap.NameFromSubject(ep.Subject)
		if !ok || !sap.VerifyIDSig(md["user_nkey"], md["account"], md["agent"], md["owner"], ep.Subject, md["id_sig"]) {
			continue
		}
		e.mu.Lock()
		if e.byUser[md["user_nkey"]] == name {
			e.lastSeen[name] = time.Now()
		}
		e.mu.Unlock()
	}
}

// Who returns the directory with presence, sorted by name.
func (e *Endpoint) Who() []Presence {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Presence, 0, len(e.dir))
	for name, r := range e.dir {
		seen := e.lastSeen[name]
		out = append(out, Presence{Record: r, LastSeen: seen, Online: !seen.IsZero() && time.Since(seen) < OnlineWindow})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Record.Name < out[j].Record.Name })
	return out
}

// LeaseInfo is the live host and mash version a session wrote to its lease.
type LeaseInfo struct{ Host, Version string }

// OwnLease reads this endpoint's own lease. It is the only lease an endpoint's permissions let it read, so other
// endpoints' live host and version are not available here. ok is false when there is no readable live lease.
func (e *Endpoint) OwnLease(ctx context.Context) (LeaseInfo, bool) {
	val, err := e.ownLease(ctx)
	if err != nil || fenced(val) {
		return LeaseInfo{}, false
	}
	var lv leaseValue
	if json.Unmarshal(val, &lv) != nil || lv.Host == "" && lv.Version == "" {
		return LeaseInfo{}, false
	}
	return LeaseInfo{Host: lv.Host, Version: lv.Version}, true
}

// Refresh waits for the answers to who is online, for one-shot callers (the CLI's who) that don't Run. The directory
// was read when the client opened (ClientOptions.FullDirectory), so it is not read again. The wait ends once answers
// have come and gone quiet (PresenceQuiet), or after PresenceCap when nothing answers.
func (e *Endpoint) Refresh(ctx context.Context) error {
	p := e.takeProbe()
	if p == nil {
		p = e.startPresence()
	}
	e.awaitPresence(ctx, p)
	return nil
}

// loadDirectory reads every directory record and replaces the in-memory copy: the keys come from stream info (subjects
// filter), the values by direct gets, many at a time.
func (e *Endpoint) loadDirectory(ctx context.Context) error {
	keys, err := e.directoryKeys(ctx)
	if err != nil {
		return fmt.Errorf("mash: reading the directory: %w", err)
	}
	e.installDirectory(e.fetchRecords(ctx, keys))
	return nil
}

// installDirectory replaces the in-memory directory with the records read; a key that is gone or unreadable is left out.
func (e *Endpoint) installDirectory(got map[string]recordResult) {
	dir := make(map[string]store.Record, len(got))
	for key, r := range got {
		if r.err == nil {
			dir[key] = r.rec
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.dir = dir
	e.byUser = make(map[string]string, len(dir))
	for n, r := range dir {
		e.byUser[r.User] = n
	}
	e.fullDir = true
}
