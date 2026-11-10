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

	"github.com/bogworks/mash/internal/natsauth"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nuid"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
	"github.com/bogworks/mash/internal/store"
)

// LeaseRefresh is how often a held lease is renewed (it expires after store.LeaseTTL).
var LeaseRefresh = 30 * time.Second

// Endpoint is an open mash endpoint.
type Endpoint struct {
	// refusals routes the connection's publish permission violations to the sends waiting on them (refusal.go).
	refusals *refusals

	// The last answered ping of serve's MCP client (unix seconds, 0 for none), written into the lease.
	mcpSeen atomic.Int64

	// skew is the server's clock minus ours in ns, from the last consumer info (noteServerTime); skewSet is false until
	// there is one.
	skew    atomic.Int64
	skewSet atomic.Bool

	// relay traces relay-by-asking from signed headers and timing only (relay.go).
	relay *relayTracker

	// waking is the unread waking-mail count written into the lease (waking.go).
	waking *wakingTracker

	// duplicateWindow overrides store.InboxDuplicateWindow for the seen set (tests that shorten the stream's window).
	duplicateWindow time.Duration

	headMu sync.Mutex // guards the stream-headroom cache (streamHasHeadroom)
	headAt time.Time
	headOK bool

	cfg config.Config
	// ident is the endpoint's identity (cfg.Identity()) and key its directory/stream/lease key, ident.Flat(): the bare
	// name for a legacy endpoint (today's value), agent_owner_name for a three-token one. Every key and subject of
	// the endpoint derives from these; cfg.Name is display text only.
	ident sap.EndpointIdentity
	key   string
	nc    *nats.Conn
	js    jetstream.JetStream
	id    sap.Identity
	subj  sap.Subjects
	self  store.Record
	log   *log.Logger

	instance    string // this process's id (lease holder, SAP instance)
	dirKV       jetstream.KeyValue
	leaseKV     jetstream.KeyValue
	hold        holdProbe // what Open read of the lease before taking it
	leaseRev    atomic.Uint64
	warnedToken bool // the shared-launch-token warning was printed
	leaseCancel context.CancelFunc
	leaseDone   chan struct{}
	leaseLost   chan error

	mu       sync.RWMutex
	dir      map[string]store.Record // by name
	byUser   map[string]string       // user key -> name
	lastSeen map[string]time.Time    // by name: the last verified answer to a presence probe
	probeAt  time.Time               // start of the latest completed presence probe (zero: none yet)
	presence PresenceCache           // when WhoFresh probes

	// fullDir: dir holds every directory record (loadDirectory ran); else only the ones a one-shot command read.
	fullDir bool
	// pre is what the first wave of a one-shot client read for the command that follows.
	pre prefetched

	dropped atomic.Int64
	service *service

	// The hook hand-over (hook.go): nil until SetSpool; since is when this session began, so only a hook run in this
	// session switches delivery to the spool.
	spool *spool.Spool
	since time.Time
	// fromStart: the session counts as hooked from the start (SpoolFromStart).
	fromStart bool
	// noChannel: the host takes no channel delivery, so mail is held, never channel-delivered (NoChannel).
	noChannel bool

	// hl is the hookless detector's state (hookless.go), guarded by its own mutex: tool calls and the read tool reach
	// it from other goroutines than Run's.
	hl hookless

	// The test-only before-ack failpoint (hook.go): nil outside the crash tests.
	failBeforeAck func(seq uint64)
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
	// Start is the serving process's start time (procStart), so a pid reused by another process is told from the
	// holder; absent before this field, or where the OS gives none.
	Start string `json:"start,omitempty"`
	// Machine is machineID: the boot and pid namespace the holder runs in. Only a holder with the same Machine as the
	// taker can be judged gone by its pid; absent before this field, or where the OS gives none.
	Machine string `json:"machine,omitempty"`
	// LaunchToken names the launch of the agent this session serves (MASH_LAUNCH_TOKEN); absent when none was given.
	LaunchToken string `json:"launch_token,omitempty"`
	// Deprovisioned marks store.LeaseTombstone: the endpoint is gone and the name is fenced.
	Deprovisioned bool `json:"deprovisioned,omitempty"`
	// Version and Commit name the serve's own build, so a supervisor can name a stale serve; absent before mash v0.1.2.
	Version string `json:"version,omitempty"`
	Commit  string `json:"commit,omitempty"`
	// MCPSeen is when the session's MCP client last answered serve's ping: the client is alive and reading serve's
	// output. It is not a delivery signal: it does not show that the client takes channel events, and nothing a client
	// sends does (Claude Code 2.1.312 sends the same messages with and without --channels; codex H1 on #14). Absent
	// before mash v0.1.2, or before the first answered ping.
	MCPSeen string `json:"mcp_seen,omitempty"`
	// WakingPending is the number of unread mail that should wake the endpoint (kind blocker, question or task, or sent
	// urgent; see waking.go), as of WakingPendingAt. Omitted when zero: read it together with WakingPendingAt, which is
	// always set once serve knows the count, so zero with a fresh stamp is "none" and no stamp is "unknown". Fresh means
	// the stamp is within store.LeaseTTL (90 s). Absent before this field, and while serve is still receiving its backlog.
	WakingPending   int    `json:"waking_pending,omitempty"`
	WakingPendingAt string `json:"waking_pending_at,omitempty"` // RFC3339 UTC
}

// ErrDeprovisioned is the permanent failure of a session whose endpoint was deprovisioned.
var ErrDeprovisioned = errors.New("mash: this endpoint has been deprovisioned")

// afterLeaseHook runs in Open between taking the lease and re-checking the directory; a test barrier, nil in
// production.
var afterLeaseHook func()

// fenced reports whether a lease entry is Deprovision's tombstone.
func fenced(val []byte) bool {
	var lv leaseValue
	return json.Unmarshal(val, &lv) == nil && lv.Deprovisioned
}

// ourMachine is this process's machineID, computed once; empty when the OS gives none.
var ourMachine = sync.OnceValue(func() string { m, _ := machineID(); return m })

// lease is the value this session writes to its lease, on taking it and on every refresh.
func (e *Endpoint) lease() []byte {
	host, _ := os.Hostname()
	start, _ := procStart(os.Getpid())
	lv := leaseValue{Instance: e.instance, Host: host, PID: fmt.Sprint(os.Getpid()), Start: start, Machine: ourMachine(),
		Since: time.Now().UTC().Format(time.RFC3339), LaunchToken: e.cfg.LaunchToken,
		Version: CurrentVersion(), Commit: CurrentCommit(), MCPSeen: unixRFC3339(e.mcpSeen.Load())}
	if n, ok := e.waking.snapshot(); ok {
		lv.WakingPending, lv.WakingPendingAt = n, wakingClock().UTC().Format(time.RFC3339)
	}
	val, _ := json.Marshal(lv)
	return val
}

// SetMCPSeen records that the MCP client answered a ping at t; the next lease refresh carries it.
func (e *Endpoint) SetMCPSeen(t time.Time) { e.mcpSeen.Store(t.Unix()) }

func unixRFC3339(sec int64) string {
	if sec == 0 {
		return ""
	}
	return time.Unix(sec, 0).UTC().Format(time.RFC3339)
}

// checkCredsFor refuses credentials minted for another endpoint before any
// network call: an endpoint user may only receive under its own inbox prefix.
// Unrestricted credentials (no subscribe list) are left to the directory check.
func checkCredsFor(path, name string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("mash: credentials: %w", err)
	}
	if err := natsauth.CheckCreds(path, raw); err != nil {
		return err
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
	return sap.IdentityFromCredsAt(path, raw)
}

// connect is nats.Connect bounded by ctx: the dial's timeout is what is left of ctx's deadline, and a connect that
// outlasts ctx (a server that accepts the TCP connection and never speaks NATS) returns when ctx ends, the late
// connection closed (codex M1 on #121: open_timeout bounds the connect, not just the requests after it).
func connect(ctx context.Context, url string, opts ...nats.Option) (*nats.Conn, error) {
	if dl, ok := ctx.Deadline(); ok {
		left := time.Until(dl)
		if left <= 0 {
			return nil, context.DeadlineExceeded
		}
		opts = append(opts, nats.Timeout(left))
	}
	type result struct {
		nc  *nats.Conn
		err error
	}
	ch := make(chan result, 1)
	go func() {
		nc, err := nats.Connect(url, opts...)
		ch <- result{nc, err}
	}()
	select {
	case r := <-ch:
		return r.nc, r.err
	case <-ctx.Done():
		go func() {
			if r := <-ch; r.nc != nil {
				r.nc.Close()
			}
		}()
		return nil, ctx.Err()
	}
}

// identityOf is the identity, key and subjects an endpoint with cfg works under. A legacy configuration (no
// agent_token) gets exactly today's values and never an error here: its key is cfg.Name and its subjects
// sap.For(owner, name). A three-token one must have a valid identity.
func identityOf(cfg config.Config) (sap.EndpointIdentity, string, sap.Subjects, error) {
	if cfg.AgentToken == "" {
		id, _ := cfg.Identity() // zero when the legacy rule fails: the legacy paths never needed it
		return id, cfg.Name, sap.For(cfg.Owner, cfg.Name), nil
	}
	id, err := cfg.Identity()
	if err != nil {
		return sap.EndpointIdentity{}, "", sap.Subjects{}, err
	}
	return id, id.Flat(), id.Subjects(), nil
}

// StateLocation is where an endpoint's state (spool, relay counters) lives under the state directory: the owner and
// the directory name. A legacy endpoint is (owner, name), as always; a three-token one is (its owner token, its flat
// key), so two identities that share a name token never share a directory. An invalid identity is an error.
func StateLocation(cfg config.Config) (owner, name string, err error) {
	if cfg.AgentToken == "" {
		return cfg.Owner, cfg.Name, nil
	}
	id, err := cfg.Identity()
	if err != nil {
		return "", "", err
	}
	return id.Owner, id.Flat(), nil
}

// SpoolDir is the spool directory of the endpoint cfg configures (see StateLocation).
func SpoolDir(cfg config.Config) (string, error) {
	o, n, err := StateLocation(cfg)
	if err != nil {
		return "", err
	}
	return spool.Dir(cfg.StateDir, o, n), nil
}

// OpenClient connects as cfg.Name and checks the identity, without a lease. It loads the whole directory: serve and
// anything else that classifies mail from every sender wants that.
func OpenClient(ctx context.Context, cfg config.Config) (*Endpoint, error) {
	return OpenClientWith(ctx, cfg, ClientOptions{FullDirectory: true})
}

// OpenClientWith is OpenClient for a one-shot command that knows what it needs: the requests for it go out together
// with the identity check, so the command pays for one wave of round trips, not one per thing it reads.
func OpenClientWith(ctx context.Context, cfg config.Config, opts ClientOptions) (*Endpoint, error) {
	if cfg.Inert() {
		return nil, errors.New("mash: no endpoint name (set MASH_NAME)")
	}
	ident, key, subj, err := identityOf(cfg)
	if err != nil {
		return nil, err
	}
	// The name rule is applied to the endpoint's own identity before anything is connected or read.
	if cfg.NameRule != nil && ident.Validate() == nil {
		if err := checkRule(cfg.NameRule, pointStart, ident); err != nil {
			return nil, err
		}
	}
	if err := config.CheckCredsMode(cfg.Creds); err != nil {
		return nil, err
	}
	id, err := readIdentity(cfg.Creds)
	if err != nil {
		return nil, err
	}
	if err := checkCredsFor(cfg.Creds, key); err != nil {
		return nil, err
	}
	refs := newRefusals()
	nc, err := connect(ctx, cfg.URL, append(natsauth.TLSOptions(cfg.TLSCA, cfg.TLSCert, cfg.TLSKey),
		natsauth.UserCredentials(cfg.Creds),
		nats.Name("mash "+cfg.Name),
		nats.CustomInboxPrefix(sap.InboxPrefix(key)),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.ReconnectJitter(5*time.Second, 5*time.Second),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			log.New(os.Stderr, "mash: ", 0).Printf("nats: %v", err)
			refs.note(err)
		}),
	)...)
	if err != nil {
		return nil, fmt.Errorf("mash: connect %s: %w", config.RedactURL(cfg.URL), config.RedactErr(natsauth.TLSHint(err), cfg.URL))
	}
	refs.flush = nc.FlushWithContext
	e := &Endpoint{cfg: cfg, ident: ident, key: key, nc: nc, id: id, subj: subj,
		log: log.New(os.Stderr, "mash["+cfg.Name+"]: ", 0), instance: nuid.Next(), waking: newWakingTracker(), relay: newRelayTracker(cfg.RelayWindow), refusals: refs,
		failBeforeAck: failpointBeforeAck(os.Getenv("MASH_SERVE_FAILPOINT")),
		dir:           map[string]store.Record{}, byUser: map[string]string{}, lastSeen: map[string]time.Time{}}
	if opts.Presence {
		// Asked before anything else, so the answers arrive while the rest of the open is under way.
		e.pre.probe = e.startPresence()
	}
	if err := e.init(ctx, opts); err != nil {
		nc.Close()
		return nil, err
	}
	return e, nil
}

// AdoptLaunchInstance makes a client (`mash send`) run inside a launched agent session sign as that session's
// serve: when MASH_LAUNCH_TOKEN is set and the endpoint's lease carries the same launch token, the client takes the
// lease's instance id instead of its own fresh one. A reader that binds a reply to the session's instance (the
// lease's) then accepts mail the agent sent from its shell exactly as mail sent through the channel. Without a token
// it does nothing. With a token it fails when the lease can't be read, is absent, or belongs to another launch: mail
// sent under a fresh instance would look delivered and still be rejected by such a reader. It holds the same
// endpoint key either way: this changes which instance the mail names, not who signs it.
// The error is advisory: `mash send` reports it and sends under its own instance (it never blocks a send).
func (e *Endpoint) AdoptLaunchInstance(ctx context.Context) error {
	if e.cfg.LaunchToken == "" {
		return nil
	}
	val, err := e.ownLease(ctx)
	if errors.Is(err, nats.ErrNoResponders) {
		return fmt.Errorf("mash: MASH_LAUNCH_TOKEN is set but the lease bucket can't be read: %w", err)
	}
	if err != nil {
		return fmt.Errorf("mash: MASH_LAUNCH_TOKEN is set but %s's lease can't be read (is its serve running?): %w", e.key, err)
	}
	var lv leaseValue
	if err := json.Unmarshal(val, &lv); err != nil || lv.Instance == "" {
		return fmt.Errorf("mash: MASH_LAUNCH_TOKEN is set but %s's lease names no instance", e.key)
	}
	if lv.LaunchToken != e.cfg.LaunchToken {
		return fmt.Errorf("mash: MASH_LAUNCH_TOKEN does not match the launch holding %s's lease: this shell is not that session's", e.key)
	}
	e.instance = lv.Instance
	return nil
}

// Open connects, checks the identity and takes the endpoint's lease.
func Open(ctx context.Context, cfg config.Config) (*Endpoint, error) {
	e, err := OpenClientWith(ctx, cfg, ClientOptions{FullDirectory: true, holding: true})
	if err != nil {
		return nil, err
	}
	if err := e.takeLease(ctx); err != nil {
		e.nc.Close()
		return nil, err
	}
	if afterLeaseHook != nil {
		afterLeaseHook()
	}
	// The directory record was read before the lease was taken: a deprovision in between would leave this session
	// online for an endpoint that no longer exists. Read it again now that the name is held.
	if _, err := store.GetRecord(ctx, e.dirKV, e.key); err != nil {
		e.Close() // gives the lease back
		if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
			return nil, fmt.Errorf("%w: %s's directory record is gone", ErrDeprovisioned, e.key)
		}
		return nil, fmt.Errorf("mash: directory: %w", err)
	}
	return e, nil
}

// holdProbe is what Open read of the lease bucket in the first wave, before the lease is taken: the bucket, and the
// lease key as it stood. Neither is trusted: taking the lease is a compare-and-set that fails if the key has changed
// since, and then the full path below runs as if nothing had been read.
type holdProbe struct {
	kv  jetstream.KeyValue
	err error  // reading the key: ErrKeyNotFound or ErrKeyDeleted for one that is free
	rev uint64 // the revision of the delete or purge marker when err is ErrKeyDeleted
}

func (e *Endpoint) takeLease(ctx context.Context) error {
	kv := e.hold.kv
	if kv == nil {
		var err error
		if kv, err = e.js.KeyValue(ctx, store.LeaseBucket); err != nil {
			return fmt.Errorf("mash: lease bucket: %w", err)
		}
	}
	e.leaseKV = kv
	val := e.lease()
	// A released lease leaves a delete marker, which a plain Create refuses, so the Create, the read that finds the
	// marker and the update over it were three round trips. The update over the marker the open saw is one: it takes
	// the key only if nothing has been written to it since.
	if e.hold.err != nil && errors.Is(e.hold.err, jetstream.ErrKeyDeleted) && e.hold.rev > 0 {
		if rev, err := kv.Update(ctx, e.key, val, e.hold.rev); err == nil {
			e.leaseRev.Store(rev)
			e.keepLease()
			return nil
		}
	}
	rev, err := kv.Create(ctx, e.key, val)
	if errors.Is(err, jetstream.ErrKeyExists) {
		held := &LeaseHeldError{Name: e.key, Host: "?", PID: "?", Since: "?"}
		if cur, gerr := kv.Get(ctx, e.key); gerr == nil {
			if fenced(cur.Value()) {
				return fmt.Errorf("%w: %s", ErrDeprovisioned, e.key)
			}
			var lv leaseValue
			if json.Unmarshal(cur.Value(), &lv) == nil {
				if e.cfg.LaunchToken != "" && lv.LaunchToken == e.cfg.LaunchToken && !e.warnedToken {
					e.warnedToken = true
					fmt.Fprintln(os.Stderr, "mash: MASH_LAUNCH_TOKEN is shared with the session holding the lease; hooks cannot tell the two sessions apart (use a fresh value per launch)")
				}
				held.Host, held.PID, held.Since = lv.Host, lv.PID, lv.Since
				// G270: a restarted session need not wait out the TTL for its dead predecessor. Only a holder on this
				// machine whose process is gone (holderGone: no such pid, or a pid whose start time is not the one
				// the lease recorded) is taken over. "This machine" is the lease's Machine equal to ours (boot and
				// pid namespace), not the hostname: hosts and containers can share a hostname, and a live remote
				// holder's pid is absent here (codex H1 on #44). A lease with no Machine, or a taker with none, is
				// never judged and expires normally. The compare-and-set loses to any write made since the lease was
				// read.
				if me := ourMachine(); me != "" && lv.Machine == me && lv.PID != fmt.Sprint(os.Getpid()) && holderGone(lv.PID, lv.Start) {
					if rev, uerr := kv.Update(ctx, e.key, val, cur.Revision()); uerr == nil {
						e.log.Printf("lease: took over from dead holder %s (pid %s)", lv.Instance, lv.PID)
						e.leaseRev.Store(rev)
						e.keepLease()
						return nil
					}
				}
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
		lastWrite := wakingClock() // the lease was just written
		var later *time.Timer      // a change in the waking count waiting to be written
		var laterC <-chan time.Time
		stopLater := func() {
			if later != nil {
				later.Stop()
				later, laterC = nil, nil
			}
		}
		defer stopLater()
		write := func() bool {
			stopLater()
			lastWrite = wakingClock()
			if err := e.refreshLease(ctx); err != nil {
				e.log.Print(err)
				e.leaseLost <- err
				return false
			}
			return true
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if !write() {
					return
				}
			case <-e.waking.notify:
				// A change: write once the burst has settled, and no sooner than WakingWriteMin after the last write.
				if later == nil {
					later = time.NewTimer(max(WakingWriteMin-wakingClock().Sub(lastWrite), wakingCoalesce))
					laterC = later.C
				}
			case <-laterC:
				if !write() {
					return
				}
			}
		}
	}()
}

// refreshLease renews the lease. A conflict means another session took the
// name (ours expired); that is fatal. Transport errors are retried next tick.
func (e *Endpoint) refreshLease(ctx context.Context) error {
	e.waking.reconcile(ctx)
	val := e.lease()
	rev, err := e.leaseKV.Update(ctx, e.key, val, e.leaseRev.Load())
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
	if cur, gerr := e.leaseKV.Get(ctx, e.key); gerr == nil {
		if fenced(cur.Value()) {
			return fmt.Errorf("%w: %s", ErrDeprovisioned, e.key)
		}
		var lv leaseValue
		if json.Unmarshal(cur.Value(), &lv) == nil && lv.Instance == e.instance {
			e.leaseRev.Store(cur.Revision())
			if rev, uerr := e.leaseKV.Update(ctx, e.key, val, cur.Revision()); uerr == nil {
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
	rev, cerr := e.leaseKV.Create(ctx, e.key, val)
	if cerr == nil {
		e.leaseRev.Store(rev)
		return nil
	}
	if !errors.Is(cerr, jetstream.ErrKeyExists) {
		e.log.Printf("lease re-take: %v (will retry)", cerr) // e.g. a lost reply: next tick adopts it
		return nil
	}
	if cur, gerr := e.leaseKV.Get(ctx, e.key); gerr == nil {
		var lv leaseValue
		if json.Unmarshal(cur.Value(), &lv) == nil && lv.Instance == e.instance {
			e.leaseRev.Store(cur.Revision())
			return nil
		}
	}
	return fmt.Errorf("mash: lost the lease on %s to another session", e.key)
}

// Run consumes mail and keeps the directory and lease current (presence is asked for on demand, ADR 0066)
// until ctx ends or the lease is lost.
func (e *Endpoint) Run(ctx context.Context, deliver Deliverer) error {
	// This serve's relay counters go to the state directory for `mash status` (another process).
	e.relay.persistTo(e.relayCountersPath(), e.Instance, e.log.Printf)
	defer e.relay.stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// The poller is joined before Run returns, and its interval read here: a poller outliving Run raced a test that
	// changes DirectoryPoll after it (TestDirectoryLiveUpdate under -race).
	polled := make(chan struct{})
	every := DirectoryPoll
	go func() { e.pollDirectory(ctx, every); close(polled) }()
	defer func() { cancel(); <-polled }()
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
		ctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
		if err := e.releaseLease(ctx); err != nil {
			e.log.Printf("lease release: %v; the next session waits up to %s for it to expire", err, store.LeaseTTL)
		}
		cancel()
	}
	e.nc.Close()
}

// releaseTimeout bounds Close's lease release, retries included.
var releaseTimeout = 3 * time.Second

// releaseLease deletes this session's lease, so the next session can take the name at once instead of waiting out
// the TTL (G270). The last known revision may be stale (a refresh landed but its reply was lost): on a revision
// conflict the entry is re-read and deleted at its current revision if it is still this instance's; another
// instance's lease is never touched. Transport errors are retried until ctx ends.
func (e *Endpoint) releaseLease(ctx context.Context) error {
	rev := e.leaseRev.Load()
	for {
		err := e.leaseKV.Delete(ctx, e.key, jetstream.LastRevision(rev))
		if err == nil {
			return nil
		}
		var apiErr *jetstream.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequence {
			cur, gerr := e.leaseKV.Get(ctx, e.key)
			switch {
			case errors.Is(gerr, jetstream.ErrKeyNotFound) || errors.Is(gerr, jetstream.ErrKeyDeleted):
				return nil // already gone
			case gerr == nil:
				var lv leaseValue
				if json.Unmarshal(cur.Value(), &lv) != nil || lv.Instance != e.instance {
					return nil // another session holds it now: not ours to release
				}
				rev = cur.Revision()
				continue
			default:
				err = gerr
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w (last: %v)", ctx.Err(), err)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Name is this endpoint's name.
func (e *Endpoint) Name() string { return e.cfg.Name }

// Key is this endpoint's directory key (the flat identity): the bare name for a legacy endpoint, agent_owner_name for
// a three-token one. It is what the endpoint signs as, what its stream, lease and directory record are keyed by, and
// what who and the mail labels show.
func (e *Endpoint) Key() string { return e.key }

// EndpointIdentity is this endpoint's identity (agent, owner and name tokens).
func (e *Endpoint) EndpointIdentity() sap.EndpointIdentity { return e.ident }

// Dropped counts forged or malformed mail that was discarded.
func (e *Endpoint) Dropped() int64 { return e.dropped.Load() }

// Conn exposes the connection (for the CLI's live prompts).
func (e *Endpoint) Conn() *nats.Conn { return e.nc }

// Identity is this endpoint's signing identity.
func (e *Endpoint) Identity() sap.Identity { return e.id }

// skewOf is the server's clock minus the local one, the server's time being taken as read at the midpoint of the
// request (t0 to t1). A zero server time (an old server that sends none) is no reading.
func skewOf(server, t0, t1 time.Time) (time.Duration, bool) {
	if server.IsZero() {
		return 0, false
	}
	return server.Sub(t0.Add(t1.Sub(t0) / 2)), true
}

// noteServerTime records the skew from a server timestamp that came back on a call made anyway (the consumer info at
// each lease refresh), so measuring the clock adds no traffic.
func (e *Endpoint) noteServerTime(server, t0, t1 time.Time) {
	if d, ok := skewOf(server, t0, t1); ok {
		e.skew.Store(int64(d))
		e.skewSet.Store(true)
	}
}

// ClockSkew is the server's clock minus this machine's as of the last lease refresh; ok is false before the first
// reading. A skew beyond sap.LiveSkew makes peers look offline (live signatures are refused) and mail [stale].
func (e *Endpoint) ClockSkew() (time.Duration, bool) {
	return time.Duration(e.skew.Load()), e.skewSet.Load()
}
