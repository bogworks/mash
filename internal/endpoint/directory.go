package endpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/randybias/mash/internal/sap"
	"github.com/randybias/mash/internal/store"
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

// pollDirectory re-reads the directory every DirectoryPoll until ctx ends.
func (e *Endpoint) pollDirectory(ctx context.Context) {
	t := time.NewTicker(DirectoryPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := e.loadDirectory(ctx); err != nil && ctx.Err() == nil {
				e.log.Print(err)
			}
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
		var info struct {
			Metadata  map[string]string `json:"metadata"`
			Endpoints []struct {
				Name    string `json:"name"`
				Subject string `json:"subject"`
			} `json:"endpoints"`
		}
		if json.Unmarshal(m.Data, &info) != nil || info.Metadata["owner"] != e.cfg.Owner {
			continue
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

// Refresh loads the directory and asks who is online, for one-shot callers
// (the CLI's who) that don't Run. It waits up to two seconds for answers.
func (e *Endpoint) Refresh(ctx context.Context) error {
	if err := e.loadDirectory(ctx); err != nil {
		return err
	}
	e.bootstrapPresence()
	return nil
}

// loadDirectory reads every directory record and replaces the in-memory copy:
// the keys come from stream info (subjects filter), each value by direct get.
func (e *Endpoint) loadDirectory(ctx context.Context) error {
	s, err := e.js.Stream(ctx, "KV_"+store.DirectoryBucket)
	if err != nil {
		return fmt.Errorf("mash: reading the directory: %w", err)
	}
	prefix := "$KV." + store.DirectoryBucket + "."
	info, err := s.Info(ctx, jetstream.WithSubjectFilter(prefix+">"))
	if err != nil {
		return fmt.Errorf("mash: reading the directory: %w", err)
	}
	dir := map[string]store.Record{}
	for subj := range info.State.Subjects {
		key := strings.TrimPrefix(subj, prefix)
		r, err := store.GetRecord(ctx, e.dirKV, key)
		if err != nil {
			continue // deleted or purged
		}
		dir[key] = r
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.dir = dir
	e.byUser = make(map[string]string, len(dir))
	for n, r := range dir {
		e.byUser[r.User] = n
	}
	return nil
}
