package policyapply

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

// GroupSigner signs a user JWT with the scoped signing key of an owner group (the group is "range.owner", the role
// of the key). The user's permissions then come only from the key's template.
type GroupSigner interface {
	SignUser(group string, uc *jwt.UserClaims) (string, error)
}

// DefaultProbeTTL is how long a probe credential is valid when ProbeMinter.TTL is zero.
const DefaultProbeTTL = 5 * time.Minute

// ProbeMinter mints probe credentials: a fresh user key, the sender's name and exactly the tags of the ProbeCred,
// no permissions of its own, signed by the sender's owner group's scoped key. It can give a probe nothing the
// sender's credential does not have under the template.
type ProbeMinter struct {
	Signer GroupSigner
	// Account is the account public key (the issuer account of every probe).
	Account string
	Record  IssueRecord
	// TTL is the probe's lifetime; zero means DefaultProbeTTL.
	TTL time.Duration
	// Now gives the time; nil means time.Now.
	Now func() time.Time
}

// Mint makes a probe for p and records it (purpose probe) before returning it. creds is a creds file; pub is the
// probe's user public key. Errors never carry seed text.
func (m *ProbeMinter) Mint(ctx context.Context, p ProbeCred) (creds []byte, pub string, err error) {
	if m == nil || m.Signer == nil || m.Record == nil {
		return nil, "", errors.New("probe minter: no signer or issue record")
	}
	if !nkeys.IsValidPublicAccountKey(m.Account) {
		return nil, "", errors.New("probe minter: not an account public key")
	}
	i := strings.LastIndex(p.Sender, ".")
	if i <= 0 || strings.Count(p.Sender, ".") < 2 {
		return nil, "", fmt.Errorf("probe for %q: not a three-part address", p.Sender)
	}
	group := p.Sender[:i]
	if p.Name == "" {
		return nil, "", fmt.Errorf("probe for %s: no name", p.Sender)
	}
	ttl := m.TTL
	if ttl <= 0 {
		ttl = DefaultProbeTTL
	}
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	at := now()

	kp, err := nkeys.CreateUser()
	if err != nil {
		return nil, "", errors.New("probe minter: cannot make a key pair")
	}
	pub, err = kp.PublicKey()
	if err != nil {
		return nil, "", errors.New("probe minter: cannot read the public key")
	}
	uc := jwt.NewUserClaims(pub)
	uc.SetScoped(true) // no permissions or limits of its own: the template supplies them
	uc.Name = p.Name
	uc.Tags.Add(p.Tags...)
	if !slices.Equal([]string(uc.Tags), p.Tags) {
		return nil, "", fmt.Errorf("probe for %s: tags are not in the canonical lower-case form", p.Sender)
	}
	uc.IssuerAccount = m.Account
	uc.Expires = at.Add(ttl).Unix()

	// Record first: a probe that was never written down must not exist.
	if err := m.Record.Add(ctx, IssueEntry{
		Address: p.Sender, PublicKey: pub, Purpose: PurposeProbe, IssuedAt: at, Expires: at.Add(ttl),
	}); err != nil {
		return nil, "", fmt.Errorf("record probe for %s: %w", p.Sender, err)
	}
	tok, err := m.Signer.SignUser(group, uc)
	if err != nil {
		return nil, "", fmt.Errorf("sign probe for %s: %w", p.Sender, errNoSeed(err))
	}
	seed, err := kp.Seed()
	if err != nil {
		return nil, "", errors.New("probe minter: cannot read the seed")
	}
	creds, err = jwt.FormatUserConfig(tok, seed)
	if err != nil {
		return nil, "", errors.New("probe minter: cannot format the credentials")
	}
	return creds, pub, nil
}

// errNoSeed returns err unless its text holds a user seed, which is then replaced.
func errNoSeed(err error) error {
	if seedRE.MatchString(err.Error()) {
		return errors.New(seedRE.ReplaceAllString(err.Error(), "[seed removed]"))
	}
	return err
}

var seedRE = regexp.MustCompile(`S[A-Z][A-Z2-7]{50,}`)

// Violation text of nats-server: Permissions Violation for Publish to "x" / for Subscription to "x".
var violationRE = regexp.MustCompile(`(?i)permissions violation for (publish|subscription) to "([^"]*)"`)

// barrierSubject is a subject no template allows. A probe publishes to it after the probed operation: the server
// answers in order, so once its refusal is seen every earlier refusal has been delivered to the error handler.
const barrierSubject = "policyapply.refusal.barrier"

// Defaults of the live transport.
const (
	defaultProbeTimeout = 5 * time.Second
	defaultProbeGrace   = time.Second
	defaultWitnessGrace = 2 * time.Second
)

// connectTo opens one connection to one server and no other: no reconnect, no server discovery, no randomizing. It
// refuses a connection that landed on a server of another name.
func connectTo(ctx context.Context, server, url string, creds []byte, timeout time.Duration, extra ...nats.Option) (*nats.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if url == "" {
		return nil, fmt.Errorf("no client URL for server %s", server)
	}
	opts := append([]nats.Option{
		nats.UserCredentialBytes(creds),
		nats.NoReconnect(), nats.DontRandomize(), nats.IgnoreDiscoveredServers(),
		nats.Timeout(timeout), nats.Name("mash-policyapply"),
	}, extra...)
	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", server, err)
	}
	if got := nc.ConnectedServerName(); got != server {
		nc.Close()
		return nil, fmt.Errorf("connected to server %q, want %q", got, server)
	}
	return nc, nil
}

// minted tracks the keys one component minted.
type minted struct {
	mu   sync.Mutex
	pubs []string
}

func (m *minted) add(p string) {
	m.mu.Lock()
	m.pubs = append(m.pubs, p)
	m.mu.Unlock()
}

func (m *minted) list() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := slices.Clone(m.pubs)
	sort.Strings(out)
	return out
}

// LiveProber is the live Prober: each probe is a new credential connecting to one named server.
type LiveProber struct {
	// URLs maps a server name to its client URL.
	URLs map[string]string
	Mint *ProbeMinter
	// Timeout bounds the connect and the flush of a probe; zero means 5 s.
	Timeout time.Duration
	// Grace is how long to wait for the server's answer to the probe's barrier; zero means 1 s. It is used in full
	// only when the barrier itself is not refused.
	Grace time.Duration

	m minted
}

var _ Prober = (*LiveProber)(nil)

// Minted returns the public keys of every probe minted so far, sorted. Revoke them when the gate ends.
func (p *LiveProber) Minted() []string { return p.m.list() }

// Refused mints a probe, connects only to server, sends the operation and reports whether the server refused it with
// a permissions violation. An error means it could not be shown either way.
func (p *LiveProber) Refused(ctx context.Context, server string, probe ProbeCred, op Op, subject string) (bool, error) {
	if op != OpPub && op != OpSub {
		return false, fmt.Errorf("unknown probe operation %q", op)
	}
	timeout, grace := p.Timeout, p.Grace
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}
	if grace <= 0 {
		grace = defaultProbeGrace
	}
	creds, pub, err := p.Mint.Mint(ctx, probe)
	if err != nil {
		return false, err
	}
	p.m.add(pub)

	var mu sync.Mutex
	seen := map[string]bool{}
	barrier := make(chan struct{})
	var once sync.Once
	nc, err := connectTo(ctx, server, p.URLs[server], creds, timeout,
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			m := violationRE.FindStringSubmatch(err.Error())
			if m == nil {
				return
			}
			kind, subj := strings.ToLower(m[1]), m[2]
			mu.Lock()
			seen[kind+" "+subj] = true
			mu.Unlock()
			if kind == "publish" && subj == barrierSubject {
				once.Do(func() { close(barrier) })
			}
		}))
	if err != nil {
		return false, err
	}
	defer nc.Close()

	kind := "publish"
	if op == OpSub {
		kind = "subscription"
		if _, err := nc.SubscribeSync(subject); err != nil {
			return false, err
		}
	} else if err := nc.Publish(subject, nil); err != nil {
		return false, err
	}
	if err := nc.Publish(barrierSubject, nil); err != nil {
		return false, err
	}
	if err := nc.FlushTimeout(timeout); err != nil {
		return false, fmt.Errorf("probe on %s: %w", server, err)
	}
	select {
	case <-barrier:
	case <-time.After(grace):
	case <-ctx.Done():
		return false, ctx.Err()
	}
	mu.Lock()
	defer mu.Unlock()
	return seen[kind+" "+subject], nil
}

// LiveWitnesses is the live Witnesses: connections opened before the push, one per server per credential, that must
// all be closed by the servers once the change is applied.
type LiveWitnesses struct {
	// Timeout bounds each connect; zero means 5 s.
	Timeout time.Duration
	// Grace is how long Closed waits, at most, for the servers to close the connections; zero means 2 s.
	Grace time.Duration

	mu    sync.Mutex
	conns map[WitnessKey][]*nats.Conn
	m     minted
}

var _ Witnesses = (*LiveWitnesses)(nil)

// Open mints a credential for each of creds and opens one connection to each server in urls (server name to client
// URL), none of them reconnecting. It may be called again to add servers. On an error it closes what that call
// opened.
func (w *LiveWitnesses) Open(ctx context.Context, urls map[string]string, minter *ProbeMinter, creds []ProbeCred) error {
	timeout := w.Timeout
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}
	servers := make([]string, 0, len(urls))
	for s := range urls {
		servers = append(servers, s)
	}
	sort.Strings(servers)
	var opened []*nats.Conn
	fail := func(err error) error {
		for _, nc := range opened {
			nc.Close()
		}
		return err
	}
	type entry struct {
		key WitnessKey
		nc  *nats.Conn
	}
	var added []entry
	for _, s := range servers {
		for _, c := range creds {
			cr, pub, err := minter.Mint(ctx, c)
			if err != nil {
				return fail(err)
			}
			w.m.add(pub)
			nc, err := connectTo(ctx, s, urls[s], cr, timeout)
			if err != nil {
				return fail(err)
			}
			opened = append(opened, nc)
			added = append(added, entry{WitnessKey{s, c.Sender}, nc})
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.conns == nil {
		w.conns = map[WitnessKey][]*nats.Conn{}
	}
	for _, e := range added {
		w.conns[e.key] = append(w.conns[e.key], e.nc)
	}
	return nil
}

// Minted returns the public keys of every credential minted for a witness, sorted.
func (w *LiveWitnesses) Minted() []string { return w.m.list() }

// Closed reports, per server and sender, whether every witness connection is closed. It waits until all are closed,
// the grace has passed or ctx is done, so a server that closes the connection a moment after the push is not
// missed; it returns at once when all are closed.
func (w *LiveWitnesses) Closed(ctx context.Context) (map[WitnessKey]bool, error) {
	grace := w.Grace
	if grace <= 0 {
		grace = defaultWitnessGrace
	}
	deadline := time.Now().Add(grace)
	for {
		out, all := w.snapshot()
		if all || !time.Now().Before(deadline) {
			return out, ctx.Err()
		}
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (w *LiveWitnesses) snapshot() (map[WitnessKey]bool, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[WitnessKey]bool, len(w.conns))
	all := true
	for k, ncs := range w.conns {
		closed := true
		for _, nc := range ncs {
			if !nc.IsClosed() {
				closed = false
			}
		}
		out[k] = closed
		all = all && closed
	}
	return out, all
}

// Close closes every witness connection.
func (w *LiveWitnesses) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, ncs := range w.conns {
		for _, nc := range ncs {
			nc.Close()
		}
	}
	w.conns = nil
}
