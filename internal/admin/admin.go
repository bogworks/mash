package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/randybias/mash/internal/config"
	"github.com/randybias/mash/internal/sap"
	"github.com/randybias/mash/internal/store"
)

// Admin provisions endpoints with the account's admin credentials.
type Admin struct {
	js    jetstream.JetStream
	owner string
}

// New returns an Admin for owner.
func New(nc *nats.Conn, owner string) (*Admin, error) {
	if !config.ValidName(owner) {
		return nil, fmt.Errorf("mash: owner %q is not a valid name", owner)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, err
	}
	return &Admin{js: js, owner: owner}, nil
}

// Init creates the directory and lease buckets. It is idempotent.
func (a *Admin) Init(ctx context.Context) error {
	for _, cfg := range []jetstream.KeyValueConfig{store.DirectoryConfig(), store.LeaseConfig()} {
		if _, err := a.js.CreateOrUpdateKeyValue(ctx, cfg); err != nil {
			return fmt.Errorf("mash: bucket %s: %w", cfg.Bucket, err)
		}
	}
	return nil
}

func (a *Admin) dir(ctx context.Context) (jetstream.KeyValue, error) {
	kv, err := a.js.KeyValue(ctx, store.DirectoryBucket)
	if err != nil {
		return nil, fmt.Errorf("mash: directory bucket (run `mash admin init`): %w", err)
	}
	return kv, nil
}

// Provision creates name's inbox stream, its consumer and, last, its
// directory record, so a half-done provision can simply be re-run.
func (a *Admin) Provision(ctx context.Context, name, user, account, note string) (store.Record, error) {
	if !config.ValidName(name) {
		return store.Record{}, fmt.Errorf("mash: endpoint name %q: 1-48 lower-case letters, digits and single hyphens", name)
	}
	if !nkeys.IsValidPublicUserKey(user) {
		return store.Record{}, fmt.Errorf("mash: %q is not a user public key (U...)", user)
	}
	if !nkeys.IsValidPublicAccountKey(account) {
		return store.Record{}, fmt.Errorf("mash: %q is not an account public key (A...)", account)
	}
	kv, err := a.dir(ctx)
	if err != nil {
		return store.Record{}, err
	}
	if _, err := kv.Get(ctx, name); err == nil {
		return store.Record{}, fmt.Errorf("mash: endpoint %s is already provisioned", name)
	} else if !errors.Is(err, jetstream.ErrKeyNotFound) {
		return store.Record{}, err
	}
	s, err := a.js.CreateOrUpdateStream(ctx, store.InboxConfig(a.owner, name))
	if err != nil {
		return store.Record{}, fmt.Errorf("mash: inbox stream: %w", err)
	}
	if _, err := s.CreateOrUpdateConsumer(ctx, store.ConsumerConfig()); err != nil {
		return store.Record{}, fmt.Errorf("mash: inbox consumer: %w", err)
	}
	rec := store.Record{
		Name: name, Owner: a.owner, Agent: sap.AgentName, AgentToken: sap.AgentToken,
		Account: account, User: user, MailSubject: sap.For(a.owner, name).Mail,
		Created: time.Now().UTC().Format(time.RFC3339), Note: note,
	}
	val, _ := json.Marshal(rec)
	if _, err := kv.Create(ctx, name, val); err != nil {
		if errors.Is(err, jetstream.ErrKeyExists) {
			return store.Record{}, fmt.Errorf("mash: endpoint %s is already provisioned", name)
		}
		return store.Record{}, fmt.Errorf("mash: directory record: %w", err)
	}
	return rec, nil
}

// Deprovision removes name's directory record, lease and inbox stream.
func (a *Admin) Deprovision(ctx context.Context, name string) error {
	kv, err := a.dir(ctx)
	if err != nil {
		return err
	}
	if err := kv.Purge(ctx, name); err != nil && !errors.Is(err, jetstream.ErrKeyNotFound) {
		return fmt.Errorf("mash: directory record: %w", err)
	}
	if lease, err := a.js.KeyValue(ctx, store.LeaseBucket); err == nil {
		_ = lease.Purge(ctx, name)
	}
	if err := a.js.DeleteStream(ctx, sap.InboxStream(name)); err != nil && !errors.Is(err, jetstream.ErrStreamNotFound) {
		return fmt.Errorf("mash: inbox stream: %w", err)
	}
	return nil
}

// List returns every directory record, sorted by name.
func (a *Admin) List(ctx context.Context) ([]store.Record, error) {
	kv, err := a.dir(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := kv.Keys(ctx)
	if errors.Is(err, jetstream.ErrNoKeysFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	slices.Sort(keys)
	var out []store.Record
	for _, k := range keys {
		r, err := store.GetRecord(ctx, kv, k)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
