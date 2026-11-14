// Package policyintake is the intake of list sets: it watches the key `listset.<account>` of the policy bucket,
// checks each delivered revision against the account's adoption marker, hands a verified list set to an applier and
// reports every refusal. It decides nothing about what a list set says: decoding, applying and writing the status
// value are behind the small interfaces below, so this package imports none of the apply stack.
//
// Concurrency: the intake takes the account lock (the one a recovery adopt and every guarded policy change take)
// around the whole processing of one revision: it reads the marker and its revision, verifies, applies and records a
// rotation, then releases. A recovery adopt therefore waits for an apply in flight and cannot interleave. As a second
// guard, just before applying the intake checks that it still holds the lock and that the marker is still at the
// revision it verified under; if not it applies nothing and processes the revision again from the top. The rotation
// writes are bound to the same revision and fail with ErrMarkerMoved when it changed.
//
// The checks run in a fixed order and the first that fails refuses the revision (never applying it): the account
// must be adopted, the document must not already be the one in force, it must decode and verify against the key the
// marker expects, name this account and this range, and a planned rotation's next key must be acceptable. The marker
// is read for every revision, never cached.
package policyintake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

// KeyPrefix is the start of the key a list set is written to; the rest is the account's public key. The outside
// system writes only keys of this family; mash writes only `status.*`.
const KeyPrefix = "listset."

// The stages of a refusal, as the status value reports them.
const (
	// StageAdoption: the account is not adopted, so list sets are ignored.
	StageAdoption = "adoption"
	// StageSignature: the signature is missing or bad, or the signer is not the key the marker expects.
	StageSignature = "signature"
	// StageDocument: the document is malformed, of an unknown schema, or asks for an unacceptable next submitter.
	StageDocument = "document"
	// StageAccount: the document names another account than the key does.
	StageAccount = "account"
	// StageRange: the document names another range than this intake serves.
	StageRange = "range"
	// StageApply: the applier failed before it wrote a status of its own.
	StageApply = "apply"
)

// The waits of the intake. A retry doubles its wait from RetryInitialDefault up to RetryMaxDefault.
const (
	// RetryInitialDefault is the first wait before a retry (a marker read, an in-force read, a marker write, a new watch).
	RetryInitialDefault = 2 * time.Second
	// RetryMaxDefault is the longest wait between two retries.
	RetryMaxDefault = time.Minute
	// DetailMaxBytes bounds the detail of a refusal.
	DetailMaxBytes = 500
	// shortKeyLen is how much of a public key a log line or a refusal may show.
	shortKeyLen = 8
)

// Sentinel errors a Decode function returns, wrapped, to classify a failure. Any other error is taken as
// ErrDocument.
var (
	// ErrSignature marks a bad or missing signature, an unsigned document, or a signer other than the expected one.
	ErrSignature = errors.New("policyintake: signature")
	// ErrDocument marks a malformed document or one of an unknown schema.
	ErrDocument = errors.New("policyintake: document")
	// ErrMarkerMoved is returned (wrapped) by a MarkerStore write when the marker is no longer at the revision the
	// document was verified under (a recovery adopt replaced it): retrying cannot help, so the intake abandons the
	// rotation and the recovery adopt decides.
	ErrMarkerMoved = errors.New("policyintake: the adoption marker changed")
)

// Doc is what the intake needs from a decoded document.
type Doc struct {
	Account, Range string
	Generation     uint64
	BaseGeneration uint64
	// Raw is the document's bytes as delivered; the intake sets it.
	Raw []byte
	// Verified is true when the signature verified under the expected submitter.
	Verified bool
	// NextSubmitter is the planned rotation's next key, or "".
	NextSubmitter string
}

// Result is what an Applier reports besides its error.
type Result struct {
	// StatusWritten is true when the applier wrote the status value of this document itself.
	StatusWritten bool
}

// MarkerStore is the adoption marker as the intake uses it.
type MarkerStore interface {
	// Current reads the marker's latest revision and that revision's number. found is false when the account is not
	// adopted.
	Current(ctx context.Context) (m store.Marker, rev uint64, found bool, err error)
	// SetNext records next as the pending next submitter (marker action rotate), only if the marker is still at
	// expectedRev: any other revision is ErrMarkerMoved and nothing is written. ownRev is the revision of this
	// caller's own earlier write of the same change, 0 for none; a marker already in the end state at expectedRev
	// or ownRev writes nothing. It returns the marker's revision after the call.
	SetNext(ctx context.Context, next string, expectedRev, ownRev uint64) (rev uint64, err error)
	// Promote makes next, the pending one, the submitter and clears it (marker action rotate), bound as SetNext is.
	Promote(ctx context.Context, next string, expectedRev, ownRev uint64) (rev uint64, err error)
}

// Release gives up a held account lock. It is safe to call more than once.
type Release func()

// AccountLock is the account-wide lock a recovery adopt and every guarded policy change take. Lock waits for it
// and returns the release and a held check: held returns nil only while the lock is still this holder's. A busy
// lock is an error wrapping admin.ErrAccountBusy.
type AccountLock interface {
	Lock(ctx context.Context) (release Release, held func(ctx context.Context) error, err error)
}

// Applier takes a verified document. It writes the status value of everything it handles (stale base, size, gate).
type Applier interface {
	Apply(ctx context.Context, d Doc) (Result, error)
}

// Refusal is what the intake reports for a revision it did not apply.
type Refusal struct {
	Account string
	// Generation is the document's generation, 0 when unknown (it did not decode).
	Generation uint64
	// Digest is the hex SHA-256 of the raw bytes.
	Digest string
	// Stage is one of the Stage constants.
	Stage string
	// Detail is a short reason, never the document and never more than the first 8 characters of a key.
	Detail string
	// At is when the intake refused it.
	At time.Time
}

// Reporter records refusals (the status value's refusals). The raw document is never given to it.
type Reporter interface {
	Refused(ctx context.Context, r Refusal) error
}

// Intake watches one account's list-set key. Fill every field but Log, Now, RetryInitial and RetryMax.
type Intake struct {
	// Account is the account's public key; the key watched is KeyPrefix + Account.
	Account string
	// Range is the range name every document must carry.
	Range string
	// KV is the policy bucket.
	KV     jetstream.KeyValue
	Marker MarkerStore
	// Lock is the account lock, held while one revision is processed.
	Lock AccountLock
	// Decode decodes raw and verifies its signature under submitter (the public key the marker expects). It returns
	// an error wrapping ErrSignature or ErrDocument.
	Decode func(raw []byte, submitter string) (Doc, error)
	// InForce returns the hex SHA-256 of the raw bytes of the document in force, "" when none.
	InForce func(ctx context.Context) (receivedSHA256 string, err error)
	Apply   Applier
	Report  Reporter
	Log     *log.Logger
	Now     func() time.Time
	// RetryInitial and RetryMax override the retry waits; zero keeps the defaults.
	RetryInitial, RetryMax time.Duration

	lastRev uint64
}

var keyRE = regexp.MustCompile(`\b[A-Z][A-Z2-7]{55}\b`)

// redact shortens every nkey-shaped token in s to its first 8 characters and bounds the result.
func redact(s string) string {
	s = keyRE.ReplaceAllStringFunc(s, func(k string) string { return k[:shortKeyLen] + "..." })
	if len(s) > DetailMaxBytes {
		s = s[:DetailMaxBytes]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}

func short(k string) string {
	if len(k) > shortKeyLen {
		return k[:shortKeyLen] + "..."
	}
	return k
}

func digest(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func (in *Intake) logf(format string, args ...any) {
	if in.Log != nil {
		in.Log.Printf(format, args...)
	}
}

func (in *Intake) now() time.Time {
	if in.Now != nil {
		return in.Now()
	}
	return time.Now()
}

func (in *Intake) key() string { return KeyPrefix + in.Account }

func (in *Intake) waits() (initial, max time.Duration) {
	initial, max = in.RetryInitial, in.RetryMax
	if initial <= 0 {
		initial = RetryInitialDefault
	}
	if max <= 0 {
		max = RetryMaxDefault
	}
	return
}

// sleep waits d or until ctx ends; it reports whether the wait ran its course.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

var errSuperseded = errors.New("superseded by a newer revision")

// retry runs f until it succeeds, backing off between attempts and logging each failure. It returns errSuperseded
// when superseded (if not nil) reports a newer revision has arrived, and ctx's error when ctx ends. A failure that
// wraps ErrMarkerMoved is returned at once.
func (in *Intake) retry(ctx context.Context, what string, superseded func() bool, f func() error) error {
	wait, max := in.waits()
	for attempt := 1; ; attempt++ {
		err := f()
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrMarkerMoved) {
			return err
		}
		in.logf("policyintake: %s failed (attempt %d), retrying in %s: %v", what, attempt, wait, redact(err.Error()))
		if superseded != nil && superseded() {
			return errSuperseded
		}
		if !sleep(ctx, wait) {
			return ctx.Err()
		}
		if superseded != nil && superseded() {
			return errSuperseded
		}
		wait = min(wait*2, max)
	}
}

// Run watches the key until ctx is done (it then returns nil) or the watch cannot be created at the start (an
// error). A watch that closes later is created again with backoff. Revisions are handled one at a time, in order.
func (in *Intake) Run(ctx context.Context) error {
	switch {
	case in.Account == "" || in.Range == "":
		return errors.New("policyintake: Account and Range are required")
	case in.KV == nil || in.Marker == nil || in.Lock == nil || in.Decode == nil || in.InForce == nil || in.Apply == nil || in.Report == nil:
		return errors.New("policyintake: KV, Marker, Lock, Decode, InForce, Apply and Report are required")
	}
	w, err := in.KV.Watch(ctx, in.key())
	if err != nil {
		return fmt.Errorf("policyintake: watch %s: %w", in.key(), err)
	}
	q := newQueue()
	done := make(chan struct{})
	go func() {
		defer close(done)
		in.pump(ctx, w, q)
	}()
	defer func() { <-done }()
	for {
		e, ok := q.next(ctx)
		if !ok {
			return nil
		}
		in.handle(ctx, e, q)
	}
}

// pump forwards the watch's entries to q, creating the watch again when it closes, until ctx is done.
func (in *Intake) pump(ctx context.Context, w jetstream.KeyWatcher, q *queue) {
	defer q.close()
	for {
		for open := true; open; {
			select {
			case <-ctx.Done():
				open = false
			case e, ok := <-w.Updates():
				if !ok {
					open = false
				} else if e != nil {
					q.push(e)
				}
			}
		}
		_ = w.Stop()
		wait, max := in.waits()
		for {
			if ctx.Err() != nil {
				return
			}
			in.logf("policyintake: the watch on %s closed; creating it again", in.key())
			nw, err := in.KV.Watch(ctx, in.key())
			if err == nil {
				w = nw
				break
			}
			in.logf("policyintake: watch %s: %v; retrying in %s", in.key(), err, wait)
			if !sleep(ctx, wait) {
				return
			}
			wait = min(wait*2, max)
		}
	}
}

// refuse logs and reports a refusal; it never applies anything.
func (in *Intake) refuse(ctx context.Context, gen uint64, raw []byte, stage, detail string) {
	detail = redact(detail)
	in.logf("policyintake: refused %s revision (generation %d, sha256 %s): %s", stage, gen, digest(raw)[:16], detail)
	r := Refusal{Account: in.Account, Generation: gen, Digest: digest(raw), Stage: stage, Detail: detail, At: in.now()}
	if err := in.Report.Refused(ctx, r); err != nil {
		in.logf("policyintake: could not report the refusal: %v", redact(err.Error()))
	}
}

// maxReprocess bounds how many times one revision is processed again because the lock was lost or the marker moved
// before the apply.
const maxReprocess = 10

// handle takes one delivered revision through the checks, holding the account lock around each pass.
func (in *Intake) handle(ctx context.Context, e jetstream.KeyValueEntry, q *queue) {
	rev := e.Revision()
	if rev <= in.lastRev {
		in.logf("policyintake: debug: revision %d already handled by this process; skipped", rev)
		return
	}
	defer func() {
		if ctx.Err() == nil {
			in.lastRev = rev
		}
	}()
	if op := e.Operation(); op == jetstream.KeyValueDelete || op == jetstream.KeyValuePurge {
		in.logf("policyintake: revision %d of %s is a delete or purge; ignored", rev, in.key())
		return
	}
	for pass := 1; pass <= maxReprocess; pass++ {
		if !in.locked(ctx, e, q) {
			return
		}
		if ctx.Err() != nil {
			return
		}
		in.logf("policyintake: revision %d: the lock or the adoption marker changed before the apply; nothing applied, processing it again", rev)
	}
	in.logf("policyintake: ERROR revision %d not processed: the lock or the adoption marker kept changing; nothing applied", rev)
}

// locked takes the account lock, runs one pass over the revision and releases the lock on every path. It returns
// true when the pass must be run again.
func (in *Intake) locked(ctx context.Context, e jetstream.KeyValueEntry, q *queue) (again bool) {
	rev := e.Revision()
	superseded := func() bool { return q.newerThan(rev) }
	var release Release
	var held func(context.Context) error
	err := in.retry(ctx, "taking the account lock (an adoption or policy change is in progress)", superseded, func() (err error) {
		release, held, err = in.Lock.Lock(ctx)
		return err
	})
	if err != nil {
		if errors.Is(err, errSuperseded) {
			in.logf("policyintake: revision %d abandoned: a newer revision arrived while waiting for the account lock", rev)
		}
		return false
	}
	defer release()
	return in.pass(ctx, e, q, held)
}

// pass is one run of the checks and the apply under the account lock. It returns true when the revision must be
// processed again from the top.
func (in *Intake) pass(ctx context.Context, e jetstream.KeyValueEntry, q *queue, held func(context.Context) error) (again bool) {
	rev := e.Revision()
	raw := e.Value()
	superseded := func() bool { return q.newerThan(rev) }
	abandoned := func(err error) bool {
		if errors.Is(err, errSuperseded) {
			in.logf("policyintake: revision %d abandoned: a newer revision arrived while waiting", rev)
		}
		return err != nil
	}

	// 2. The marker, read every time.
	var marker store.Marker
	var markerRev uint64
	var found bool
	if abandoned(in.retry(ctx, "reading the adoption marker", superseded, func() (err error) {
		marker, markerRev, found, err = in.Marker.Current(ctx)
		return err
	})) {
		return false
	}
	if !found {
		in.refuse(ctx, 0, raw, StageAdoption, "this account is not adopted: list sets are ignored")
		return false
	}
	if marker.Account != "" && marker.Account != in.Account {
		in.refuse(ctx, 0, raw, StageAdoption, "the adoption marker names another account: list sets are ignored")
		return false
	}

	// 5 (before decoding). The document in force: a replay after a restart is skipped silently. It is checked before
	// the signature so that a replay of the applied document, signed by a key a rotation has since retired, does not
	// become a refusal that overwrites the applied status.
	var inForce string
	if abandoned(in.retry(ctx, "reading the document in force", superseded, func() (err error) {
		inForce, err = in.InForce(ctx)
		return err
	})) {
		return false
	}
	d := digest(raw)
	if inForce != "" && strings.EqualFold(inForce, d) {
		in.logf("policyintake: debug: revision %d is the document in force (sha256 %s); skipped", rev, d[:16])
		return false
	}

	// 3. The expected signer: the pending next key if there is one (the old key is refused from then on).
	expected, pending := marker.Submitter, marker.NextSubmitter != ""
	if pending {
		expected = marker.NextSubmitter
	}
	doc, err := in.Decode(raw, expected)
	if err != nil {
		stage := StageDocument
		detail := err.Error()
		if errors.Is(err, ErrSignature) {
			stage = StageSignature
			detail = "the signature does not verify under the key the adoption marker expects (" + short(expected) + ")"
			if pending {
				detail += "; a rotation is pending, so only the next submitter's key is accepted"
			}
		}
		in.refuse(ctx, 0, raw, stage, detail)
		return false
	}
	doc.Raw = raw

	// 4. Checks after a successful decode.
	switch {
	case !doc.Verified:
		in.refuse(ctx, doc.Generation, raw, StageSignature, "the document is not signed by the key the adoption marker expects ("+short(expected)+")")
		return false
	case doc.Account != strings.TrimPrefix(e.Key(), KeyPrefix):
		in.refuse(ctx, doc.Generation, raw, StageAccount, "the document names another account than the key it was written to")
		return false
	case doc.Range != in.Range:
		in.refuse(ctx, doc.Generation, raw, StageRange, "the document names another range than this one serves")
		return false
	}

	// 6. A planned rotation's next key is validated before anything is applied.
	startRotation := !pending && doc.NextSubmitter != ""
	if startRotation {
		if err := admin.ValidateNextSubmitter(in.Account, marker, doc.NextSubmitter); err != nil {
			in.refuse(ctx, doc.Generation, raw, StageDocument, "next_submitter is not acceptable: "+err.Error())
			return false
		}
	}

	// 7. Apply, but only if the lock is still ours and the marker is still the one this document was verified under.
	if err := held(ctx); err != nil {
		in.logf("policyintake: revision %d: the account lock is no longer held (%v)", rev, redact(err.Error()))
		return true
	}
	var nowRev uint64
	var nowFound bool
	if abandoned(in.retry(ctx, "re-reading the adoption marker before the apply", superseded, func() (err error) {
		_, nowRev, nowFound, err = in.Marker.Current(ctx)
		return err
	})) {
		return false
	}
	if !nowFound || nowRev != markerRev {
		return true
	}
	res, err := in.Apply.Apply(ctx, doc)
	if err != nil {
		if res.StatusWritten {
			in.logf("policyintake: generation %d: apply failed, status written by the applier: %v", doc.Generation, redact(err.Error()))
		} else {
			in.refuse(ctx, doc.Generation, raw, StageApply, err.Error())
		}
		return false
	}
	in.logf("policyintake: generation %d applied (sha256 %s)", doc.Generation, d[:16])

	// 6, after a successful apply: the rotation completes, bound to the marker revision this document was verified
	// under. own is the revision of this intake's own earlier write of the same change, if a write reported one.
	var own uint64
	write := func(f func(next string) (uint64, error), next string) func() error {
		return func() error {
			r, err := f(next)
			if r != 0 {
				own = r
			}
			return err
		}
	}
	switch {
	case pending:
		in.finishRotation(ctx, "promoting the next submitter ("+short(marker.NextSubmitter)+")", write(func(n string) (uint64, error) {
			return in.Marker.Promote(ctx, n, markerRev, own)
		}, marker.NextSubmitter))
	case startRotation:
		in.finishRotation(ctx, "recording the next submitter ("+short(doc.NextSubmitter)+")", write(func(n string) (uint64, error) {
			return in.Marker.SetNext(ctx, n, markerRev, own)
		}, doc.NextSubmitter))
	}
	return false
}

// finishRotation writes the marker after an applied document, retrying until it lands: the rotation must not be lost.
// The applied status stands; no refusal is reported.
func (in *Intake) finishRotation(ctx context.Context, what string, f func() error) {
	err := in.retry(ctx, "ERROR the applied list set's rotation is not recorded yet: "+what, nil, f)
	switch {
	case err == nil:
		in.logf("policyintake: rotation recorded: %s", what)
	case errors.Is(err, ErrMarkerMoved):
		in.logf("policyintake: ERROR rotation not recorded and abandoned, the adoption marker changed after the document was verified (%s); a recovery adopt decides, and the key it set stays the expected signer: %v", what, redact(err.Error()))
	default:
		in.logf("policyintake: ERROR rotation NOT recorded (%s): %v; the document was applied, run adopt again to rotate", what, err)
	}
}
