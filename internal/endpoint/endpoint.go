// Package endpoint is one mash endpoint: its NATS connection, identity,
// lease, inbox and view of the directory. `mash serve` runs one per Claude
// session; the CLI opens one without a lease.
package endpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nuid"

	"github.com/randybias/mash/internal/config"
	"github.com/randybias/mash/internal/sap"
	"github.com/randybias/mash/internal/store"
)

// LeaseRefresh is how often a held lease is renewed (it expires after store.LeaseTTL).
var LeaseRefresh = 30 * time.Second

// Endpoint is an open mash endpoint.
type Endpoint struct {
	cfg  config.Config
	nc   *nats.Conn
	js   jetstream.JetStream
	id   sap.Identity
	subj sap.Subjects
	self store.Record
	log  *log.Logger

	instance string // this process's id (lease holder, SAP instance)
	dirKV    jetstream.KeyValue
	leaseKV  jetstream.KeyValue
	leaseRev uint64

	mu       sync.RWMutex
	dir      map[string]store.Record // by name
	byUser   map[string]string       // user key -> name
	lastSeen map[string]time.Time    // by name, from verified heartbeats

	dropped atomic.Int64
	service *service
}

// LeaseHeldError reports that another live session holds this endpoint name.
type LeaseHeldError struct {
	Name, Host, PID, Since string
}

func (e *LeaseHeldError) Error() string {
	return fmt.Sprintf("mash: endpoint %s is already in use by another session (host %s, pid %s, since %s)",
		e.Name, e.Host, e.PID, e.Since)
}

type leaseValue struct {
	Instance string `json:"instance_id"`
	Host     string `json:"host"`
	PID      string `json:"pid"`
	Since    string `json:"since"`
}

func readIdentity(path string) (sap.Identity, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return sap.Identity{}, fmt.Errorf("mash: credentials: %w", err)
	}
	return sap.IdentityFromCreds(raw)
}

// OpenClient connects as cfg.Name and checks the identity, without a lease.
func OpenClient(ctx context.Context, cfg config.Config) (*Endpoint, error) {
	if cfg.Inert() {
		return nil, errors.New("mash: no endpoint name (set MASH_NAME)")
	}
	id, err := readIdentity(cfg.Creds)
	if err != nil {
		return nil, err
	}
	nc, err := nats.Connect(cfg.URL,
		nats.UserCredentials(cfg.Creds),
		nats.Name("mash "+cfg.Name),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.ReconnectJitter(5*time.Second, 5*time.Second),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			log.New(os.Stderr, "mash: ", 0).Printf("nats: %v", err)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("mash: connect %s: %w", cfg.URL, err)
	}
	e := &Endpoint{cfg: cfg, nc: nc, id: id, subj: sap.For(cfg.Owner, cfg.Name),
		log: log.New(os.Stderr, "mash["+cfg.Name+"]: ", 0), instance: nuid.Next(),
		dir: map[string]store.Record{}, byUser: map[string]string{}, lastSeen: map[string]time.Time{}}
	if err := e.init(ctx); err != nil {
		nc.Close()
		return nil, err
	}
	return e, nil
}

func (e *Endpoint) init(ctx context.Context) error {
	js, err := jetstream.New(e.nc)
	if err != nil {
		return err
	}
	e.js = js
	if e.dirKV, err = js.KeyValue(ctx, store.DirectoryBucket); err != nil {
		return fmt.Errorf("mash: directory: %w", err)
	}
	self, err := store.GetRecord(ctx, e.dirKV, e.cfg.Name)
	if err != nil {
		return fmt.Errorf("mash: endpoint %s is not provisioned: %w", e.cfg.Name, err)
	}
	if self.User != e.id.User {
		return fmt.Errorf("mash: the credentials in %s (user %s) do not match endpoint %s (user %s)",
			e.cfg.Creds, e.id.User, e.cfg.Name, self.User)
	}
	e.self = self
	e.setRecord(self)
	return nil
}

// Open connects, checks the identity and takes the endpoint's lease.
func Open(ctx context.Context, cfg config.Config) (*Endpoint, error) {
	e, err := OpenClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := e.takeLease(ctx); err != nil {
		e.nc.Close()
		return nil, err
	}
	return e, nil
}

func (e *Endpoint) takeLease(ctx context.Context) error {
	kv, err := e.js.KeyValue(ctx, store.LeaseBucket)
	if err != nil {
		return fmt.Errorf("mash: lease bucket: %w", err)
	}
	e.leaseKV = kv
	host, _ := os.Hostname()
	val, _ := json.Marshal(leaseValue{Instance: e.instance, Host: host, PID: fmt.Sprint(os.Getpid()),
		Since: time.Now().UTC().Format(time.RFC3339)})
	rev, err := kv.Create(ctx, e.cfg.Name, val)
	if errors.Is(err, jetstream.ErrKeyExists) {
		held := &LeaseHeldError{Name: e.cfg.Name, Host: "?", PID: "?", Since: "?"}
		if cur, gerr := kv.Get(ctx, e.cfg.Name); gerr == nil {
			var lv leaseValue
			if json.Unmarshal(cur.Value(), &lv) == nil {
				held.Host, held.PID, held.Since = lv.Host, lv.PID, lv.Since
			}
		}
		return held
	}
	if err != nil {
		return fmt.Errorf("mash: lease: %w", err)
	}
	e.leaseRev = rev
	return nil
}

// refreshLease renews the lease. A conflict means another session took the
// name (ours expired); that is fatal. Transport errors are retried next tick.
func (e *Endpoint) refreshLease(ctx context.Context) error {
	host, _ := os.Hostname()
	val, _ := json.Marshal(leaseValue{Instance: e.instance, Host: host, PID: fmt.Sprint(os.Getpid()),
		Since: time.Now().UTC().Format(time.RFC3339)})
	rev, err := e.leaseKV.Update(ctx, e.cfg.Name, val, e.leaseRev)
	if err == nil {
		e.leaseRev = rev
		return nil
	}
	var apiErr *jetstream.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode != jetstream.JSErrCodeStreamWrongLastSequence {
		e.log.Printf("lease refresh: %v (will retry)", err)
		return nil
	}
	// Our entry is gone (expired during an outage) or replaced. Re-take it if free.
	if rev, cerr := e.leaseKV.Create(ctx, e.cfg.Name, val); cerr == nil {
		e.leaseRev = rev
		return nil
	}
	return fmt.Errorf("mash: lost the lease on %s to another session", e.cfg.Name)
}

// Run consumes mail and keeps the directory, presence and lease current
// until ctx ends or the lease is lost.
func (e *Endpoint) Run(ctx context.Context, deliver Deliverer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := e.watchDirectory(ctx); err != nil {
		return err
	}
	if err := e.watchPresence(ctx); err != nil {
		return err
	}
	stop, err := e.consume(ctx, deliver)
	if err != nil {
		return err
	}
	defer stop()
	if e.leaseKV == nil {
		<-ctx.Done()
		return nil
	}
	t := time.NewTicker(LeaseRefresh)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := e.refreshLease(ctx); err != nil {
				e.log.Print(err)
				return err
			}
		}
	}
}

// Close releases the lease and closes the connection.
func (e *Endpoint) Close() {
	if e.service != nil {
		e.service.stop()
	}
	if e.leaseKV != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = e.leaseKV.Delete(ctx, e.cfg.Name, jetstream.LastRevision(e.leaseRev))
		cancel()
	}
	e.nc.Close()
}

// Name is this endpoint's name.
func (e *Endpoint) Name() string { return e.cfg.Name }

// Dropped counts forged or malformed mail that was discarded.
func (e *Endpoint) Dropped() int64 { return e.dropped.Load() }

// Conn exposes the connection (for the CLI's live prompts).
func (e *Endpoint) Conn() *nats.Conn { return e.nc }

// Identity is this endpoint's signing identity.
func (e *Endpoint) Identity() sap.Identity { return e.id }
