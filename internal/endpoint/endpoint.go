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

	"github.com/nats-io/jwt/v2"
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

	instance    string // this process's id (lease holder, SAP instance)
	dirKV       jetstream.KeyValue
	leaseKV     jetstream.KeyValue
	leaseRev    atomic.Uint64
	leaseCancel context.CancelFunc
	leaseDone   chan struct{}
	leaseLost   chan error

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

// checkCredsFor refuses credentials minted for another endpoint before any
// network call: an endpoint user may only receive under its own inbox prefix.
// Unrestricted credentials (no subscribe list) are left to the directory check.
func checkCredsFor(path, name string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("mash: credentials: %w", err)
	}
	tok, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		return fmt.Errorf("mash: credentials: %w", err)
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		return fmt.Errorf("mash: credentials: %w", err)
	}
	allow := uc.Permissions.Sub.Allow
	if len(allow) == 0 || allow.Contains(sap.InboxPrefix(name)+".>") {
		return nil
	}
	return fmt.Errorf("mash: the credentials in %s do not match endpoint %s (they are for another endpoint)", path, name)
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
	if err := checkCredsFor(cfg.Creds, cfg.Name); err != nil {
		return nil, err
	}
	nc, err := nats.Connect(cfg.URL,
		nats.UserCredentials(cfg.Creds),
		nats.Name("mash "+cfg.Name),
		nats.CustomInboxPrefix(sap.InboxPrefix(cfg.Name)),
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
	return e.loadDirectory(ctx)
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
		if ctx.Err() != nil {
			return ctx.Err() // cancelled while looking up the holder: not "held"
		}
		return held
	}
	if err != nil {
		return fmt.Errorf("mash: lease: %w", err)
	}
	e.leaseRev.Store(rev)
	e.keepLease()
	return nil
}

// keepLease refreshes the lease from the moment it is taken until Close, so a
// slow start (the MCP handshake) cannot let it expire. A loss is reported on
// leaseLost, which Run watches.
func (e *Endpoint) keepLease() {
	ctx, cancel := context.WithCancel(context.Background())
	e.leaseCancel, e.leaseDone, e.leaseLost = cancel, make(chan struct{}), make(chan error, 1)
	go func() {
		defer close(e.leaseDone)
		t := time.NewTicker(LeaseRefresh)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := e.refreshLease(ctx); err != nil {
					e.log.Print(err)
					e.leaseLost <- err
					return
				}
			}
		}
	}()
}

// refreshLease renews the lease. A conflict means another session took the
// name (ours expired); that is fatal. Transport errors are retried next tick.
func (e *Endpoint) refreshLease(ctx context.Context) error {
	host, _ := os.Hostname()
	val, _ := json.Marshal(leaseValue{Instance: e.instance, Host: host, PID: fmt.Sprint(os.Getpid()),
		Since: time.Now().UTC().Format(time.RFC3339)})
	rev, err := e.leaseKV.Update(ctx, e.cfg.Name, val, e.leaseRev.Load())
	if err == nil {
		e.leaseRev.Store(rev)
		return nil
	}
	var apiErr *jetstream.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode != jetstream.JSErrCodeStreamWrongLastSequence {
		e.log.Printf("lease refresh: %v (will retry)", err)
		return nil
	}
	// Our revision is stale. Either an earlier refresh landed but its reply was
	// lost (the entry is still ours: adopt it), the entry expired during an
	// outage (re-take it if free), or another session holds the name.
	if cur, gerr := e.leaseKV.Get(ctx, e.cfg.Name); gerr == nil {
		var lv leaseValue
		if json.Unmarshal(cur.Value(), &lv) == nil && lv.Instance == e.instance {
			e.leaseRev.Store(cur.Revision())
			if rev, uerr := e.leaseKV.Update(ctx, e.cfg.Name, val, cur.Revision()); uerr == nil {
				e.leaseRev.Store(rev)
			} else {
				e.log.Printf("lease refresh: %v (will retry)", uerr)
			}
			return nil
		}
	} else if !errors.Is(gerr, jetstream.ErrKeyNotFound) && !errors.Is(gerr, jetstream.ErrKeyDeleted) {
		e.log.Printf("lease check: %v (will retry)", gerr)
		return nil
	}
	rev, cerr := e.leaseKV.Create(ctx, e.cfg.Name, val)
	if cerr == nil {
		e.leaseRev.Store(rev)
		return nil
	}
	if !errors.Is(cerr, jetstream.ErrKeyExists) {
		e.log.Printf("lease re-take: %v (will retry)", cerr) // e.g. a lost reply: next tick adopts it
		return nil
	}
	if cur, gerr := e.leaseKV.Get(ctx, e.cfg.Name); gerr == nil {
		var lv leaseValue
		if json.Unmarshal(cur.Value(), &lv) == nil && lv.Instance == e.instance {
			e.leaseRev.Store(cur.Revision())
			return nil
		}
	}
	return fmt.Errorf("mash: lost the lease on %s to another session", e.cfg.Name)
}

// Run consumes mail and keeps the directory, presence and lease current
// until ctx ends or the lease is lost.
func (e *Endpoint) Run(ctx context.Context, deliver Deliverer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go e.pollDirectory(ctx)
	if err := e.watchPresence(ctx); err != nil {
		return err
	}
	stop, err := e.consume(ctx, deliver)
	if err != nil {
		return err
	}
	defer stop()
	select {
	case <-ctx.Done():
		return nil
	case err := <-e.leaseLost: // nil channel (no lease) never fires
		return err
	}
}

// Close releases the lease and closes the connection.
func (e *Endpoint) Close() {
	if svc := e.svc(); svc != nil {
		svc.stop()
	}
	if e.leaseCancel != nil {
		e.leaseCancel()
		<-e.leaseDone
	}
	if e.leaseKV != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = e.leaseKV.Delete(ctx, e.cfg.Name, jetstream.LastRevision(e.leaseRev.Load()))
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
