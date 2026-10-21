package endpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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

func (e *Endpoint) setRecord(r store.Record) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if old, ok := e.dir[r.Name]; ok && e.byUser[old.User] == r.Name {
		delete(e.byUser, old.User)
	}
	e.dir[r.Name] = r
	e.byUser[r.User] = r.Name
}

func (e *Endpoint) dropRecord(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if old, ok := e.dir[name]; ok {
		if e.byUser[old.User] == name {
			delete(e.byUser, old.User)
		}
		delete(e.dir, name)
	}
}

// watchDirectory keeps the in-memory directory current. It returns once the
// initial values are loaded.
func (e *Endpoint) watchDirectory(ctx context.Context) error {
	w, err := e.dirKV.WatchAll(ctx)
	if err != nil {
		return fmt.Errorf("mash: watching the directory: %w", err)
	}
	ready := make(chan struct{})
	go func() {
		defer w.Stop()
		initial := true
		for {
			select {
			case <-ctx.Done():
				return
			case u, ok := <-w.Updates():
				if !ok {
					return
				}
				if u == nil {
					if initial {
						initial = false
						close(ready)
					}
					continue
				}
				switch u.Operation() {
				case jetstream.KeyValuePut:
					if r, err := store.ParseRecord(u.Key(), u.Value()); err == nil {
						e.setRecord(r)
					} else {
						e.log.Print(err)
					}
				default:
					e.dropRecord(u.Key())
				}
			}
		}
	}()
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Second):
		return fmt.Errorf("mash: the directory did not load")
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
	inbox := nats.NewInbox()
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
