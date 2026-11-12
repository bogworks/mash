package listset

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
)

// Generations and digests (contract mash-messaging-policy REQ-017, AT-016; ADR 0098).
//
// Each accepted list set is one generation. A list set states the generation it was based on (ListSet.Base); Accept
// refuses it unless that is the generation in force, so two submitters working from the same generation cannot both
// win, and a stale one changes nothing. Accept is a pure function and does not by itself serialize submissions: two
// callers that read the same Record and call Accept both succeed. Ledger holds the Record in force and makes the
// check and the replacement one atomic step within a process. The package persists nothing.

// ErrStaleBase is matched, through errors.Is, by the *StaleBaseError Accept returns for a list set whose stated base
// is not the generation in force.
var ErrStaleBase = errors.New("list set is based on a generation that is not in force")

// ErrGenerationOverflow is returned by Accept when the generation in force is the largest a generation can be.
var ErrGenerationOverflow = errors.New("generation number would overflow")

// StaleBaseError is the error Accept returns when ListSet.Base differs from the generation in force. It matches
// ErrStaleBase.
type StaleBaseError struct {
	// Stated is the base generation the list set gave.
	Stated uint64
	// InForce is the generation in force, 0 when nothing is.
	InForce uint64
}

func (e *StaleBaseError) Error() string {
	return fmt.Sprintf("list set refused: it is based on generation %d, and generation %d is in force", e.Stated, e.InForce)
}

// Is reports whether target is ErrStaleBase.
func (e *StaleBaseError) Is(target error) bool { return target == ErrStaleBase }

// Digest is a SHA-256 digest.
type Digest [sha256.Size]byte

// String returns "sha256:" and the lower-case hex of d.
func (d Digest) String() string { return "sha256:" + hex.EncodeToString(d[:]) }

// IsZero reports whether d is the zero Digest, which no real input produces.
func (d Digest) IsZero() bool { return d == Digest{} }

// Record describes the list set in force: its generation and three digests. The zero Record means nothing is in force.
type Record struct {
	// Generation is the number of the list set in force; the first accepted list set is generation 1.
	Generation uint64
	// Received is the SHA-256 of exactly the bytes the list set was received as, so it identifies the submission.
	// It is the zero Digest when Accept was given nil bytes, which means no bytes were supplied.
	Received Digest
	// Content is the digest of the list set's content (DigestListSet), independent of encoding and ordering: two
	// encodings of the same content share a Content and differ in Received.
	Content Digest
	// Signed is the digest of what was signed from it (DigestCompiled).
	Signed Digest
}

// Accept decides whether ls may replace the list set in force, and if so compiles it. received is the exact bytes
// ls was decoded from; Record.Received is their SHA-256, or the zero Digest if received is nil.
//
// It refuses a list set whose Base is not inForce.Generation with a *StaleBaseError (matching ErrStaleBase) before it
// validates or compiles anything, and refuses one when the generation would overflow with ErrGenerationOverflow.
// Otherwise it runs Compile (which validates and checks) and returns the new Record, with Generation
// inForce.Generation+1, together with the compiled output. On any error it returns the zero Record and nil, so the
// caller keeps inForce and signs nothing. A zero inForce means nothing is in force, and ls.Base must then be 0.
//
// Accept is pure and does not by itself serialize submissions: two submissions built on the same inForce both pass
// it and get the same next generation. Use a Ledger to have exactly one of them win.
func Accept(inForce Record, ls ListSet, received []byte) (Record, *Compiled, error) {
	if ls.Base != inForce.Generation {
		return Record{}, nil, &StaleBaseError{Stated: ls.Base, InForce: inForce.Generation}
	}
	if inForce.Generation == math.MaxUint64 {
		return Record{}, nil, ErrGenerationOverflow
	}
	c, err := Compile(ls)
	if err != nil {
		return Record{}, nil, err
	}
	rec := Record{
		Generation: inForce.Generation + 1,
		Content:    DigestListSet(ls),
		Signed:     DigestCompiled(c),
	}
	if received != nil {
		rec.Received = sha256.Sum256(received)
	}
	return rec, c, nil
}

// Ledger holds the Record in force and is safe for concurrent use. Its Accept runs the pure Accept against the
// current record and replaces the record only on success, under one lock: an atomic compare-and-replace, so of
// several submissions built on the same generation exactly one succeeds and the rest get ErrStaleBase.
//
// A Ledger is in process memory only. Persisting the record and the cross-process apply path are not built yet, and
// that path must keep the same compare-and-replace: check the base against the stored record and replace it in one
// atomic step.
type Ledger struct {
	mu      sync.Mutex
	inForce Record
}

// NewLedger returns a Ledger whose record in force is inForce; the zero Record means nothing is in force.
func NewLedger(inForce Record) *Ledger { return &Ledger{inForce: inForce} }

// InForce returns the record in force.
func (l *Ledger) InForce() Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inForce
}

// Accept is the package Accept run against the record in force, which it replaces with the returned Record on
// success. On any error the record in force is unchanged.
func (l *Ledger) Accept(ls ListSet, received []byte) (Record, *Compiled, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, c, err := Accept(l.inForce, ls, received)
	if err != nil {
		return Record{}, nil, err
	}
	l.inForce = rec
	return rec, c, nil
}

// DigestListSet returns the digest of ls over a canonical encoding that does not depend on the order of its switches,
// senders, or the entries of any allow or deny list, so the same content gives the same digest however it was
// ordered. Content is hashed as given: it is not validated, normalised for case, or de-duplicated, and a duplicated
// entry gives a different digest.
//
// Encoding (every string is its length as a uvarint, then its bytes; every count is a uvarint):
//
//	"mash.listset.v1\x00" | Base as 8 bytes big-endian | Range
//	| count of switches | switches sorted by their encoding, each Group Verb Switch
//	| count of senders  | senders sorted by their encoding, each Sender
//	                        | count of allow | allow sorted | count of deny | deny sorted
//
// It gives Record.Content. Record.Received is a separate digest of the received bytes.
func DigestListSet(ls ListSet) Digest {
	e := newEnc("mash.listset.v1\x00")
	e.u64(ls.Base)
	e.str(ls.Range)

	sw := make([]string, len(ls.Switches))
	for i, s := range ls.Switches {
		sw[i] = enc1(s.Group, string(s.Verb), string(s.Switch))
	}
	e.sortedRaw(sw)

	sd := make([]string, len(ls.Senders))
	for i, s := range ls.Senders {
		sd[i] = enc1(s.Sender) + encSorted(s.Allow) + encSorted(s.Deny)
	}
	e.sortedRaw(sd)
	return e.sum()
}

// DigestCompiled returns the digest of what Compile produced and is to be signed, deterministic for equal content.
// A nil c digests as an empty Compiled.
//
// Encoding (strings and counts as in DigestListSet):
//
//	"mash.compiled.v1\x00" | Range
//	| count of templates | templates sorted by their encoding, each Group
//	    | pub allow sorted | pub deny sorted | sub allow sorted | sub deny sorted
//	    | response permission: 0, or 1 then MaxMsgs and Expires as 8 bytes big-endian
//	| count of users | users sorted by their encoding, each Sender Name | tags sorted
//
// The order of lines within a permission list and of tags does not change what the server enforces, so it does not
// change the digest.
func DigestCompiled(c *Compiled) Digest {
	e := newEnc("mash.compiled.v1\x00")
	if c == nil {
		c = &Compiled{}
	}
	e.str(c.Range)

	ts := make([]string, len(c.Templates))
	for i, t := range c.Templates {
		s := enc1(t.Group) +
			encSorted(t.Permissions.Pub.Allow) + encSorted(t.Permissions.Pub.Deny) +
			encSorted(t.Permissions.Sub.Allow) + encSorted(t.Permissions.Sub.Deny)
		if r := t.Permissions.Resp; r != nil {
			s += "\x01" + be64(uint64(int64(r.MaxMsgs))) + be64(uint64(r.Expires))
		} else {
			s += "\x00"
		}
		ts[i] = s
	}
	e.sortedRaw(ts)

	us := make([]string, len(c.Users))
	for i, u := range c.Users {
		us[i] = enc1(u.Sender, u.Name) + encSorted(u.Tags)
	}
	e.sortedRaw(us)
	return e.sum()
}

type enc struct{ b []byte }

func newEnc(domain string) *enc { return &enc{b: []byte(domain)} }

func (e *enc) u64(n uint64) { e.b = binary.BigEndian.AppendUint64(e.b, n) }

func (e *enc) str(s string) {
	e.b = binary.AppendUvarint(e.b, uint64(len(s)))
	e.b = append(e.b, s...)
}

// sortedRaw writes the count of items and then the items, each already encoded, in sorted order.
func (e *enc) sortedRaw(items []string) {
	sort.Strings(items)
	e.b = binary.AppendUvarint(e.b, uint64(len(items)))
	for _, it := range items {
		e.b = append(e.b, it...)
	}
}

func (e *enc) sum() Digest { return sha256.Sum256(e.b) }

func be64(n uint64) string { return string(binary.BigEndian.AppendUint64(nil, n)) }

// enc1 encodes strings as length-prefixed values, in the order given.
func enc1(ss ...string) string {
	var b []byte
	for _, s := range ss {
		b = binary.AppendUvarint(b, uint64(len(s)))
		b = append(b, s...)
	}
	return string(b)
}

// encSorted encodes a count and then the strings sorted, without changing the caller's slice.
func encSorted(in []string) string {
	ss := append([]string(nil), in...)
	sort.Strings(ss)
	return string(binary.AppendUvarint(nil, uint64(len(ss)))) + enc1(ss...)
}
