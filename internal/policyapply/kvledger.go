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

// StatusReader is implemented by a Ledger that can return the latest stored Status. Apply uses it, when the ledger is
// one, to answer a replay of the document in force.
type StatusReader interface {
	// LatestStatus returns the latest stored Status; ok is false when none was written.
	LatestStatus(ctx context.Context) (st Status, ok bool, err error)
}

// RefusalWriter is what the apply intake needs to record a refusal it makes before Apply. KVLedger implements it.
type RefusalWriter interface {
	WriteRefusal(ctx context.Context, st Status) error
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
	// MaxValue is the largest value written to the bucket; zero means the connection's MaxPayload less headroom when
	// it is known, else DefaultMaxValue.
	MaxValue int
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
	pendKey  string
	maxValue int
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
	l := &KVLedger{kv: kv, key: StatusKey(account), pendKey: PendingJWTKey(account), maxValue: opts.MaxValue, leaseKey: LeaseKey(account), holder: opts.Holder, ttl: opts.LeaseTTL,
		poll: opts.Poll, opTO: opts.OpTimeout, now: opts.Now, sem: make(chan struct{}, 1)}
	if l.maxValue <= 0 {
		l.maxValue = DefaultMaxValue
		if c := js.Conn(); c != nil && c.MaxPayload() > 0 {
			l.maxValue = max(int(c.MaxPayload())-16<<10, 1)
		}
	}
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
	_ Ledger           = (*KVLedger)(nil)
	_ StatusWriter     = (*KVLedger)(nil)
	_ StatusReader     = (*KVLedger)(nil)
	_ RefusalWriter    = (*KVLedger)(nil)
	_ DetailLedger     = (*KVLedger)(nil)
	_ DetailLedger     = (*MemLedger)(nil)
	_ PendingJWTLedger = (*KVLedger)(nil)
	_ PendingJWTLedger = (*MemLedger)(nil)
	_ RollbackLedger   = (*KVLedger)(nil)
	_ RollbackLedger   = (*MemLedger)(nil)
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
	// InForceJWT is the signed account JWT pushed for the record in force; InForceListSet is its list set as JSON.
	// Both are written with the record by CompareAndCommitDetail and cleared by CompareAndCommit.
	InForceJWT     string          `json:"in_force_jwt,omitempty"`
	InForceListSet json.RawMessage `json:"in_force_listset,omitempty"`
	Pending        *recordValue    `json:"pending,omitempty"`
	// PendingJWT is read only, for a value written before the pending JWT had its own key (PendingJWTKey); never written.
	PendingJWT string  `json:"pending_jwt,omitempty"`
	Status     *Status `json:"status,omitempty"`
}

// pendingJWTValue is the value at the pending JWT key: the last account JWT pushed for Record. It counts only while
// the status value's pending record is Record; an empty JWT is the cleared value.
type pendingJWTValue struct {
	Schema string       `json:"schema"`
	Record *recordValue `json:"record,omitempty"`
	JWT    string       `json:"jwt,omitempty"`
}

// DefaultMaxValue is the largest value the ledger writes when the connection's MaxPayload is not known: the server's
// default max payload (1 MiB) less headroom for the subject and headers of the write.
const DefaultMaxValue = 1<<20 - 16<<10

// ErrValueTooLarge is returned when a value the ledger would write is over the size limit. Nothing is written.
var ErrValueTooLarge = errors.New("the policy ledger value is over the size limit")

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
	if len(b) > l.maxValue {
		return fmt.Errorf("policy ledger %s: %w: the status value is %d bytes, limit %d", l.key, ErrValueTooLarge, len(b), l.maxValue)
	}
	if rev == 0 {
		_, err = l.kv.Create(ctx, l.key, b)
	} else {
		_, err = l.kv.Update(ctx, l.key, b, rev)
	}
	return err
}

// readPendingJWT returns the pending JWT value and its revision; an absent key is the empty value at revision 0.
func (l *KVLedger) readPendingJWT(ctx context.Context) (pendingJWTValue, uint64, error) {
	e, err := l.kv.Get(ctx, l.pendKey)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return pendingJWTValue{}, 0, nil
	}
	if err != nil {
		return pendingJWTValue{}, 0, fmt.Errorf("policy ledger %s: %w", l.pendKey, err)
	}
	var pv pendingJWTValue
	if err := json.Unmarshal(e.Value(), &pv); err != nil {
		return pendingJWTValue{}, 0, fmt.Errorf("policy ledger %s: unreadable: %w", l.pendKey, err)
	}
	return pv, e.Revision(), nil
}

// writePendingJWT stores jwt for rec at the pending JWT key by compare-and-set, fenced by the lease. rec nil and an
// empty jwt is the cleared value, which is not written when the key is absent or already clear. The key is never
// deleted: the bucket may refuse it.
func (l *KVLedger) writePendingJWT(ctx context.Context, rec *recordValue, jwt string) error {
	for range casTries {
		cur, rev, err := l.readPendingJWT(ctx)
		if err != nil {
			return err
		}
		if jwt == "" && cur.JWT == "" {
			return nil
		}
		if err := l.checkLease(ctx); err != nil {
			return err
		}
		b, err := json.Marshal(pendingJWTValue{Schema: StatusSchema, Record: rec, JWT: jwt})
		if err != nil {
			return err
		}
		if len(b) > l.maxValue {
			return fmt.Errorf("policy ledger %s: %w: the pending JWT value is %d bytes, limit %d", l.pendKey, ErrValueTooLarge, len(b), l.maxValue)
		}
		if rev == 0 {
			_, err = l.kv.Create(ctx, l.pendKey, b)
		} else {
			_, err = l.kv.Update(ctx, l.pendKey, b, rev)
		}
		if err == nil {
			return nil
		}
		if !casLost(err) {
			return fmt.Errorf("policy ledger %s: %w", l.pendKey, err)
		}
	}
	return fmt.Errorf("policy ledger %s: changed by other writers too often; try again", l.pendKey)
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

// InForceJWT implements DetailLedger.
func (l *KVLedger) InForceJWT() (string, error) {
	ctx, cancel := l.opCtx()
	defer cancel()
	v, _, err := l.read(ctx)
	if err != nil {
		return "", err
	}
	return v.InForceJWT, nil
}

// InForceListSet implements DetailLedger.
func (l *KVLedger) InForceListSet() (listset.ListSet, bool, error) {
	ctx, cancel := l.opCtx()
	defer cancel()
	v, _, err := l.read(ctx)
	if err != nil {
		return listset.ListSet{}, false, err
	}
	if len(v.InForceListSet) == 0 {
		return listset.ListSet{}, false, nil
	}
	var ls listset.ListSet
	if err := json.Unmarshal(v.InForceListSet, &ls); err != nil {
		return listset.ListSet{}, false, fmt.Errorf("policy ledger %s: in_force_listset: %w", l.key, err)
	}
	return ls, true, nil
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

// SetPending implements Ledger. The JWT kept for the pending record stays with it when the record is the same, and
// is ignored (PendingJWT) when the record is another.
func (l *KVLedger) SetPending(rec listset.Record) error {
	return l.mutate(func(v *statusValue) error { v.Pending, v.PendingJWT = recordToValue(rec), ""; return nil })
}

// SetPendingJWT implements PendingJWTLedger. The JWT is written to its own key first (the status value never holds
// it), then the pending record to the status value, so a pending record is never without the JWT stored for it.
func (l *KVLedger) SetPendingJWT(rec listset.Record, jwt string) error {
	ctx, cancel := l.opCtx()
	defer cancel()
	rv := recordToValue(rec)
	if err := l.writePendingJWT(ctx, rv, jwt); err != nil {
		return err
	}
	return l.mutate(func(v *statusValue) error { v.Pending, v.PendingJWT = rv, ""; return nil })
}

// PendingJWT implements PendingJWTLedger.
func (l *KVLedger) PendingJWT() (string, error) {
	ctx, cancel := l.opCtx()
	defer cancel()
	v, _, err := l.read(ctx)
	if err != nil {
		return "", err
	}
	if v.Pending == nil {
		return "", nil
	}
	pv, _, err := l.readPendingJWT(ctx)
	if err != nil {
		return "", err
	}
	if pv.Record != nil && *pv.Record == *v.Pending {
		return pv.JWT, nil
	}
	return v.PendingJWT, nil // a value written before the pending JWT had its own key
}

// ClearPending implements Ledger.
func (l *KVLedger) ClearPending() error {
	if err := l.mutate(func(v *statusValue) error { v.Pending, v.PendingJWT = nil, ""; return nil }); err != nil {
		return err
	}
	ctx, cancel := l.opCtx()
	defer cancel()
	return l.writePendingJWT(ctx, nil, "")
}

// CommitRollback implements RollbackLedger: jwt becomes the JWT in force and the pending record is cleared, in one
// write of the status value (the record in force and its list set are unchanged); the pending JWT key is cleared
// after it. The lease is checked first, as for a commit.
func (l *KVLedger) CommitRollback(jwt string) error {
	ctx, cancel := l.opCtx()
	defer cancel()
	if err := l.checkLease(ctx); err != nil {
		return err
	}
	if err := l.mutate(func(v *statusValue) error {
		v.InForceJWT, v.Pending, v.PendingJWT = jwt, nil, ""
		return nil
	}); err != nil {
		return err
	}
	return l.writePendingJWT(ctx, nil, "")
}

// WriteStatus stores st as the latest status, keeping the record in force and the pending record. Only a run under
// the lease writes status: a ledger that holds no lease gets ErrLeaseLost and writes nothing. The write is fenced:
// the status key is read, then the lease key is read and must still have this ledger's revision and holder (as in
// CompareAndCommit), and only then is the status key updated at the revision read. The window between the lease read
// and the update is a few milliseconds and remains: the lease can expire and be taken over inside it. A run that
// took over reads and writes the status key too, so the status key's own compare-and-set serializes the two: the
// update fails and is retried, with the lease read again, rather than overwriting. No clock of any host is
// compared: recency is the lease fence plus that compare-and-set.
func (l *KVLedger) WriteStatus(ctx context.Context, st Status) error {
	for range casTries {
		v, rev, err := l.read(ctx)
		if err != nil {
			return err
		}
		if err := l.fenceLease(ctx); err != nil {
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

// ErrNotRefusal is returned by WriteRefusal when the status is not a refusal: its State is not StateRefused or it
// names no refusal.
var ErrNotRefusal = errors.New("the status is not a refusal")

// LatestStatus implements StatusReader.
func (l *KVLedger) LatestStatus(ctx context.Context) (Status, bool, error) {
	v, _, err := l.read(ctx)
	if err != nil {
		return Status{}, false, err
	}
	if v.Status == nil {
		return Status{}, false, nil
	}
	return *v.Status, true, nil
}

// WriteRefusal stores st as the latest status for a refusal the intake makes before Apply (a bad signature, the wrong
// submitter, an account or range mismatch, an unknown schema, an account not adopted). It takes the ledger's lease, as
// a run does (waiting for another holder until ctx is done), writes st with the fenced WriteStatus and releases the
// lease. st.State must be StateRefused and st must carry at least one refusal, else ErrNotRefusal and nothing is
// taken or written. st.Generation 0 means the generation is unknown. It never changes the record in force, the
// pending record or the stored list set and JWT.
func (l *KVLedger) WriteRefusal(ctx context.Context, st Status) error {
	if st.State != StateRefused || len(st.Refusals) == 0 {
		return fmt.Errorf("%w: state %q, %d refusals", ErrNotRefusal, st.State, len(st.Refusals))
	}
	unlock, err := l.Lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return l.WriteStatus(ctx, st)
}

// fenceLease is checkLease for a caller that must hold the lease: a ledger that holds none gets ErrLeaseLost.
func (l *KVLedger) fenceLease(ctx context.Context) error {
	l.mu.Lock()
	held := l.held
	l.mu.Unlock()
	if !held {
		return ErrLeaseLost
	}
	return l.checkLease(ctx)
}

// CompareAndCommit implements Ledger. It stores no JWT or list set and clears those of the generation it replaces
// (see CompareAndCommitDetail). It first checks that the lease is still this ledger's (ErrLeaseLost), then
// commits at the revision it read, and only if the generation in force is still base; a lost compare-and-set is read
// again, so only a different generation in force is a stale base.
func (l *KVLedger) CompareAndCommit(base uint64, rec listset.Record) error {
	return l.commit(base, rec, InForceDetail{})
}

// CompareAndCommitDetail implements DetailLedger: CompareAndCommit that also stores d.JWT as in_force_jwt and
// d.ListSet as in_force_listset (json.Marshal of listset.ListSet), in the same write as the record.
func (l *KVLedger) CompareAndCommitDetail(base uint64, rec listset.Record, d InForceDetail) error {
	return l.commit(base, rec, d)
}

// CheckCommitSize implements CommitSizeChecker: it encodes the status value as the commit of rec with jwt and ls
// would write it (the pending record still set, as it is until the commit is done) and returns an error wrapping
// ErrValueTooLarge when it is over the limit. Nothing is written.
func (l *KVLedger) CheckCommitSize(rec listset.Record, jwt string, ls listset.ListSet) error {
	lsb, err := json.Marshal(ls)
	if err != nil {
		return fmt.Errorf("policy ledger %s: %w", l.key, err)
	}
	ctx, cancel := l.opCtx()
	defer cancel()
	v, _, err := l.read(ctx)
	if err != nil {
		return err
	}
	rv := recordToValue(rec)
	v.Schema, v.InForce, v.InForceJWT, v.InForceListSet, v.Pending = StatusSchema, rv, jwt, lsb, rv
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > l.maxValue {
		return fmt.Errorf("policy ledger %s: %w: the status value of generation %d would be %d bytes, limit %d", l.key, ErrValueTooLarge, rec.Generation, len(b), l.maxValue)
	}
	return nil
}

func (l *KVLedger) commit(base uint64, rec listset.Record, d InForceDetail) error {
	var lsb json.RawMessage
	if d.ListSet != nil {
		b, err := json.Marshal(*d.ListSet)
		if err != nil {
			return fmt.Errorf("policy ledger %s: %w", l.key, err)
		}
		lsb = b
	}
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
		v.InForce, v.InForceJWT, v.InForceListSet = recordToValue(rec), d.JWT, lsb
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
