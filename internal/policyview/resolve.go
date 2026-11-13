package policyview

import (
	"context"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/store"
)

// DirectoryResolver returns a ResolveAddress that reads mash's directory records: a record's user is the endpoint's
// user public key, and its address is the three-part agent.owner.name of its identity (the range is the agent token).
// A legacy record has no three-part address and resolves to none. The key-to-address map is read from kv and kept for
// ttl (zero means ten seconds), so a burst of requests reads the directory once.
func DirectoryResolver(kv jetstream.KeyValue, ttl time.Duration) func(ctx context.Context, userPub string) (string, bool, error) {
	if ttl <= 0 {
		ttl = 10 * time.Second
	}
	var (
		mu    sync.Mutex
		at    time.Time
		byKey map[string]string
	)
	return func(ctx context.Context, userPub string) (string, bool, error) {
		mu.Lock()
		defer mu.Unlock()
		if byKey == nil || time.Since(at) > ttl {
			m, err := readAddresses(ctx, kv)
			if err != nil {
				return "", false, err
			}
			byKey, at = m, time.Now()
		}
		a, ok := byKey[userPub]
		return a, ok, nil
	}
}

func readAddresses(ctx context.Context, kv jetstream.KeyValue) (map[string]string, error) {
	keys, err := kv.Keys(ctx)
	if err != nil {
		if err == jetstream.ErrNoKeysFound {
			return map[string]string{}, nil
		}
		return nil, err
	}
	out := make(map[string]string, len(keys))
	ambiguous := map[string]bool{}
	for _, k := range keys {
		e, err := kv.Get(ctx, k)
		if err != nil {
			continue // a key deleted since the listing
		}
		rec, err := store.ParseRecord(k, e.Value())
		if err != nil {
			continue
		}
		id, err := rec.Identity()
		if err != nil || id.IsLegacy() {
			continue
		}
		if prev, dup := out[rec.User]; dup && prev != id.String() {
			ambiguous[rec.User] = true // one key under two addresses names no one
		}
		out[rec.User] = id.String()
	}
	for u := range ambiguous {
		delete(out, u)
	}
	return out, nil
}
