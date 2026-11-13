package policyapply

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/listset"
)

// StatusSchema is the schema of the status value. A value with another schema is refused, never guessed at.
const StatusSchema = "mash.policy.status/1"

// DefaultLeaseTTL is how long a run lease lasts without renewal.
const DefaultLeaseTTL = 2 * time.Minute

// ErrLeaseLost is returned by CompareAndCommit when this run no longer holds the lease: another run took it over or
// the renewal failed. Nothing is committed; the pushes already made are pending and the run is resubmitted.
var ErrLeaseLost = errors.New("the run lease was lost: another run may have taken over")

// ErrUnknownSchema is returned when the status value carries a schema this code does not know.
var ErrUnknownSchema = errors.New("the status value has an unknown schema")

// ErrNoPolicyBucket is returned by NewKVLedger when the range's policy bucket does not exist. Operations creates it;
// mash never does.
var ErrNoPolicyBucket = errors.New("the policy bucket does not exist")

// StatusWriter is implemented by a Ledger that also keeps the latest Status. Run writes the Status of every run to it.
type StatusWriter interface {
	WriteStatus(ctx context.Context, st Status) error
}

// KVLedgerOptions configures a KVLedger. Only Bucket is required.
type KVLedgerOptions struct {
	// Bucket is the resolved name of the policy bucket (see PolicyBucket). Required.
	Bucket string
	// LeaseTTL is how long the run lease lasts without renewal; zero means DefaultLeaseTTL. The lease is renewed at
	// half of it while held.
	LeaseTTL time.Duration
	// Holder names this ledger in the lease value; empty means a random id.
	Holder string
	// Now gives the time the lease expiry is judged by; nil means time.Now.
	Now func() time.Time
	// Poll is how long Lock waits before looking at a held lease again; zero means 200 ms.
	Poll time.Duration
	// OpTimeout bounds each KV operation of the Ledger methods, which take no context; zero means 10 s.
	OpTimeout time.Duration
}

// KVLedger is a Ledger kept in the range's policy KV bucket (see seam.go), one value per account.
//
// The value at StatusKey(account) holds the record in force, the pending record and the latest Status, as canonical
// JSON with a schema field. Every write is a compare-and-set on the key's revision (Create when absent, Update at
// the revision read): that revision check is the real single-winner guard. CompareAndCommit re-reads under it and
// commits only if the generation in force is still its base; a status-only write is a read-modify-write that retries
// when it loses.
//
// The lease at LeaseKey(account) is not what makes a commit safe; it stops two runs from pushing over each other.
// Lock creates it, or takes it over when its expires_at has passed, or waits; while held a goroutine renews it at
// half the TTL. If a renewal loses the compare-and-set the lease is marked lost and CompareAndCommit returns
// ErrLeaseLost. Unlock releases it by writing an expired value (never Delete or Purge: the bucket may refuse them).
// Expiry is judged by the clocks of the processes, so keep them close compared to the TTL.
//
// The ledger does not create the bucket.
type KVLedger struct {
	kv       jetstream.KeyValue
	key      string
	leaseKey string
	holder   string
	ttl      time.Duration
	poll     time.Duration
	opTO     time.Duration
	now      func() time.Time

	sem chan struct{} // one hold of the lease per ledger value at a time

	mu       sync.Mutex // guards the fields below
	held     bool
	lost     bool
	leaseRev uint64
	expires  time.Time
}

var accountRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// NewKVLedger returns the ledger of account in the policy bucket opts.Bucket. It fails, wrapping
// ErrNoPolicyBucket, when the bucket does not exist.
func NewKVLedger(ctx context.Context, js jetstream.JetStream, account string, opts KVLedgerOptions) (*KVLedger, error) {
	if opts.Bucket == "" {
		return nil, fmt.Errorf("policy ledger: no policy bucket name; set %s", PolicyBucketEnv)
	}
	if !accountRE.MatchString(account) {
		return nil, fmt.Errorf("policy ledger: %q is not an account key", account)
	}
	bucket := opts.Bucket
	kv, err := js.KeyValue(ctx, bucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		return nil, fmt.Errorf("%w: bucket %s is created by operations; ask for it before applying policy", ErrNoPolicyBucket, bucket)
	}
	if err != nil {
		return nil, fmt.Errorf("policy ledger: bucket %s: %w", bucket, err)
	}
	l := &KVLedger{kv: kv, key: StatusKey(account), leaseKey: LeaseKey(account), holder: opts.Holder, ttl: opts.LeaseTTL,
		poll: opts.Poll, opTO: opts.OpTimeout, now: opts.Now, sem: make(chan struct{}, 1)}
	if l.ttl <= 0 {
		l.ttl = DefaultLeaseTTL
	}
	if l.poll <= 0 {
		l.poll = 200 * time.Millisecond
	}
	if l.opTO <= 0 {
		l.opTO = 10 * time.Second
	}
	if l.now == nil {
		l.now = time.Now
	}
	if l.holder == "" {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		l.holder = hex.EncodeToString(b[:])
	}
	return l, nil
}

var (
	_ Ledger       = (*KVLedger)(nil)
	_ StatusWriter = (*KVLedger)(nil)
)

// recordValue is a listset.Record in the status value: digests are their strings.
type recordValue struct {
	Generation uint64 `json:"generation"`
	Received   string `json:"received"`
	Content    string `json:"content"`
	Signed     string `json:"signed"`
}

// statusValue is the value at the status key.
type statusValue struct {
	Schema  string       `json:"schema"`
	InForce *recordValue `json:"in_force,omitempty"`
	Pending *recordValue `json:"pending,omitempty"`
	Status  *Status      `json:"status,omitempty"`
}

// leaseValue is the value at the lease key.
type leaseValue struct {
	Holder    string `json:"holder"`
	ExpiresAt string `json:"expires_at"` // RFC 3339, UTC
}

func recordToValue(r listset.Record) *recordValue {
	return &recordValue{Generation: r.Generation, Received: r.Received.String(), Content: r.Content.String(), Signed: r.Signed.String()}
}

func parseDigest(s string) (listset.Digest, error) {
	var d listset.Digest
	h, ok := strings.CutPrefix(s, "sha256:")
	if !ok {
		return d, fmt.Errorf("digest %q is not sha256:<hex>", s)
	}
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != len(d) {
		return d, fmt.Errorf("digest %q is not sha256:<64 hex digits>", s)
	}
	copy(d[:], b)
	return d, nil
}

func (v *recordValue) record() (listset.Record, error) {
	if v == nil {
		return listset.Record{}, nil
	}
	r := listset.Record{Generation: v.Generation}
	var err error
	if r.Received, err = parseDigest(v.Received); err != nil {
		return listset.Record{}, err
	}
	if r.Content, err = parseDigest(v.Content); err != nil {
		return listset.Record{}, err
	}
	if r.Signed, err = parseDigest(v.Signed); err != nil {
		return listset.Record{}, err
	}
	return r, nil
}

func decodeStatusValue(b []byte) (statusValue, error) {
	var v statusValue
	if err := json.Unmarshal(b, &v); err != nil {
		return statusValue{}, fmt.Errorf("the status value is unreadable: %w", err)
	}
	if v.Schema != StatusSchema {
		return statusValue{}, fmt.Errorf("%w: %q", ErrUnknownSchema, v.Schema)
	}
	return v, nil
}

// casLost reports whether err is the refusal of a write made at a stale revision (or of a Create of a key that exists).
func casLost(err error) bool {
	if errors.Is(err, jetstream.ErrKeyExists) {
		return true
	}
	var api *jetstream.APIError
	return errors.As(err, &api) && api.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequence
}

const casTries = 8

func (l *KVLedger) opCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), l.opTO)
}

// read returns the status value and its revision; a key not written yet is the empty value at revision 0.
func (l *KVLedger) read(ctx context.Context) (statusValue, uint64, error) {
	e, err := l.kv.Get(ctx, l.key)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return statusValue{Schema: StatusSchema}, 0, nil
	}
	if err != nil {
		return statusValue{}, 0, fmt.Errorf("policy ledger %s: %w", l.key, err)
	}
	v, err := decodeStatusValue(e.Value())
	if err != nil {
		return statusValue{}, 0, fmt.Errorf("policy ledger %s: %w", l.key, err)
	}
	return v, e.Revision(), nil
}

// write stores v at the revision rev (Create when rev is 0). A lost compare-and-set returns an error for which casLost
// is true.
func (l *KVLedger) write(ctx context.Context, v statusValue, rev uint64) error {
	v.Schema = StatusSchema
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if rev == 0 {
		_, err = l.kv.Create(ctx, l.key, b)
	} else {
		_, err = l.kv.Update(ctx, l.key, b, rev)
	}
	return err
}

// mutate is a read-modify-write of the status value: change edits the value read and the result is written at the
// revision read, retried from the read when another writer got there first.
func (l *KVLedger) mutate(change func(v *statusValue) error) error {
	ctx, cancel := l.opCtx()
	defer cancel()
	for range casTries {
		v, rev, err := l.read(ctx)
		if err != nil {
			return err
		}
		if err := change(&v); err != nil {
			return err
		}
		err = l.write(ctx, v, rev)
		if err == nil {
			return nil
		}
		if !casLost(err) {
			return fmt.Errorf("policy ledger %s: %w", l.key, err)
		}
	}
	return fmt.Errorf("policy ledger %s: changed by other writers too often; try again", l.key)
}

// InForce implements Ledger.
func (l *KVLedger) InForce() (listset.Record, error) {
	ctx, cancel := l.opCtx()
	defer cancel()
	v, _, err := l.read(ctx)
	if err != nil {
		return listset.Record{}, err
	}
	return v.InForce.record()
}

// Pending implements Ledger.
func (l *KVLedger) Pending() (listset.Record, bool, error) {
	ctx, cancel := l.opCtx()
	defer cancel()
	v, _, err := l.read(ctx)
	if err != nil {
		return listset.Record{}, false, err
	}
	if v.Pending == nil {
		return listset.Record{}, false, nil
	}
	r, err := v.Pending.record()
	return r, err == nil, err
}

// SetPending implements Ledger.
func (l *KVLedger) SetPending(rec listset.Record) error {
	return l.mutate(func(v *statusValue) error { v.Pending = recordToValue(rec); return nil })
}

// ClearPending implements Ledger.
func (l *KVLedger) ClearPending() error {
	return l.mutate(func(v *statusValue) error { v.Pending = nil; return nil })
}

// WriteStatus stores st as the latest status, keeping the record in force and the pending record.
func (l *KVLedger) WriteStatus(ctx context.Context, st Status) error {
	for range casTries {
		v, rev, err := l.read(ctx)
		if err != nil {
			return err
		}
		v.Status = &st
		err = l.write(ctx, v, rev)
		if err == nil {
			return nil
		}
		if !casLost(err) {
			return fmt.Errorf("policy ledger %s: %w", l.key, err)
		}
	}
	return fmt.Errorf("policy ledger %s: changed by other writers too often; try again", l.key)
}

// CompareAndCommit implements Ledger. It first checks that the lease is still this ledger's (ErrLeaseLost), then
// commits at the revision it read, and only if the generation in force is still base; a lost compare-and-set is read
// again, so only a different generation in force is a stale base.
func (l *KVLedger) CompareAndCommit(base uint64, rec listset.Record) error {
	ctx, cancel := l.opCtx()
	defer cancel()
	if err := l.checkLease(ctx); err != nil {
		return err
	}
	for range casTries {
		v, rev, err := l.read(ctx)
		if err != nil {
			return err
		}
		cur, err := v.InForce.record()
		if err != nil {
			return fmt.Errorf("policy ledger %s: %w", l.key, err)
		}
		if cur.Generation != base {
			return &listset.StaleBaseError{Stated: base, InForce: cur.Generation}
		}
		v.InForce = recordToValue(rec)
		err = l.write(ctx, v, rev)
		if err == nil {
			return nil
		}
		if !casLost(err) {
			return fmt.Errorf("policy ledger %s: %w", l.key, err)
		}
	}
	return fmt.Errorf("policy ledger %s: %w: changed by other writers too often", l.key, listset.ErrStaleBase)
}

// checkLease returns ErrLeaseLost unless the lease value is still the one this ledger wrote last and has not
// expired by its own clock. A ledger that holds no lease is not checked: the revision check guards the commit.
func (l *KVLedger) checkLease(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.held {
		return nil
	}
	if l.lost || !l.now().Before(l.expires) {
		l.lost = true
		return ErrLeaseLost
	}
	e, err := l.kv.Get(ctx, l.leaseKey)
	if err != nil {
		return fmt.Errorf("policy ledger %s: %w", l.leaseKey, err)
	}
	var lv leaseValue
	if json.Unmarshal(e.Value(), &lv) != nil || lv.Holder != l.holder || e.Revision() != l.leaseRev {
		l.lost = true
		return ErrLeaseLost
	}
	return nil
}

func (l *KVLedger) leaseBytes(holder string, expires time.Time) []byte {
	b, _ := json.Marshal(leaseValue{Holder: holder, ExpiresAt: expires.UTC().Format(time.RFC3339Nano)})
	return b
}

// tryAcquire takes the lease if it is absent or expired. ok is false when another holder has it.
func (l *KVLedger) tryAcquire(ctx context.Context) (rev uint64, expires time.Time, ok bool, err error) {
	now := l.now()
	expires = now.Add(l.ttl)
	e, err := l.kv.Get(ctx, l.leaseKey)
	switch {
	case errors.Is(err, jetstream.ErrKeyNotFound):
		rev, err = l.kv.Create(ctx, l.leaseKey, l.leaseBytes(l.holder, expires))
		if casLost(err) {
			return 0, time.Time{}, false, nil
		}
		return rev, expires, err == nil, err
	case err != nil:
		return 0, time.Time{}, false, fmt.Errorf("policy ledger %s: %w", l.leaseKey, err)
	}
	var lv leaseValue
	if err := json.Unmarshal(e.Value(), &lv); err != nil {
		return 0, time.Time{}, false, fmt.Errorf("policy ledger %s: unreadable lease: %w", l.leaseKey, err)
	}
	until, err := time.Parse(time.RFC3339Nano, lv.ExpiresAt)
	if err != nil {
		return 0, time.Time{}, false, fmt.Errorf("policy ledger %s: unreadable lease expiry: %w", l.leaseKey, err)
	}
	if until.After(now) {
		return 0, time.Time{}, false, nil
	}
	rev, err = l.kv.Update(ctx, l.leaseKey, l.leaseBytes(l.holder, expires), e.Revision())
	if casLost(err) {
		return 0, time.Time{}, false, nil
	}
	return rev, expires, err == nil, err
}

// Lock implements Ledger: it takes the run lease, waiting while another holder has it, until ctx is done.
func (l *KVLedger) Lock(ctx context.Context) (func(), error) {
	select {
	case l.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	for {
		opCtx, cancel := context.WithTimeout(ctx, l.opTO)
		rev, expires, ok, err := l.tryAcquire(opCtx)
		cancel()
		if err != nil {
			<-l.sem
			return nil, err
		}
		if ok {
			l.mu.Lock()
			l.held, l.lost, l.leaseRev, l.expires = true, false, rev, expires
			l.mu.Unlock()
			stop, done := make(chan struct{}), make(chan struct{})
			go l.renew(stop, done)
			var once sync.Once
			return func() {
				once.Do(func() {
					close(stop)
					<-done
					l.release()
					<-l.sem
				})
			}, nil
		}
		select {
		case <-time.After(l.poll):
		case <-ctx.Done():
			<-l.sem
			return nil, ctx.Err()
		}
	}
}

// renew extends the lease every half TTL until stop. A renewal that loses the compare-and-set, or that keeps failing
// until the lease has expired by this ledger's clock, marks the lease lost.
func (l *KVLedger) renew(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	t := time.NewTicker(l.ttl / 2)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		l.mu.Lock()
		if l.lost {
			l.mu.Unlock()
			return
		}
		ctx, cancel := l.opCtx()
		exp := l.now().Add(l.ttl)
		rev, err := l.kv.Update(ctx, l.leaseKey, l.leaseBytes(l.holder, exp), l.leaseRev)
		cancel()
		switch {
		case err == nil:
			l.leaseRev, l.expires = rev, exp
		case casLost(err) || !l.now().Before(l.expires):
			l.lost = true
		}
		l.mu.Unlock()
	}
}

// release writes an expired lease value at the revision held. Failing to is harmless: the lease expires by itself.
func (l *KVLedger) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	wasLost, rev := l.lost, l.leaseRev
	l.held = false
	if wasLost {
		return
	}
	ctx, cancel := l.opCtx()
	defer cancel()
	_, _ = l.kv.Update(ctx, l.leaseKey, l.leaseBytes("", time.Unix(0, 0)), rev)
}
