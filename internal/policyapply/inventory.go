package policyapply

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/bogworks/mash/internal/listset"
)

// IssueInventory is an issue record that can list every entry it holds, for the old-key inventory. AdminIssueRecord
// and MemIssueRecord are.
type IssueInventory interface {
	// AllMaxIssued returns every entry; the IssuedAt of each issue or rotation key that is not revoked is the greatest
	// issue time over all of the key's retained revisions, and an incomplete history is ErrIncompleteInventory.
	AllMaxIssued(ctx context.Context) ([]IssueEntry, error)
}

// ErrIncompleteInventory is returned by Apply when the issue record cannot give a complete old-key inventory: a
// credential it holds has no issue time, or the change needs the inventory of a sender (ADR 0113, ADR 0114) and none
// is available. Nothing is pushed.
var ErrIncompleteInventory = errors.New("the old-key inventory is incomplete")

// needsInventory returns the senders whose old keys the gate requires revoked: those with a credential-only reach and
// those the change removes. It is empty when the target does not compile (Run refuses it).
func needsInventory(prev *listset.Compiled, ls listset.ListSet) (need []string, senders map[string]bool) {
	target, err := listset.Compile(ls)
	if err != nil || prev == nil {
		return nil, nil
	}
	e, err := NewExpect(prev, target, nil)
	if err != nil {
		return nil, nil
	}
	set := map[string]bool{}
	for _, r := range e.CredentialOnly {
		set[r.Sender] = true
	}
	for _, s := range e.RemovedSenders {
		set[s] = true
	}
	senders = map[string]bool{}
	for _, u := range prev.Users {
		senders[u.Sender] = true
	}
	for _, u := range target.Users {
		senders[u.Sender] = true
	}
	for s := range set {
		need = append(need, s)
	}
	sort.Strings(need)
	return need, senders
}

// oldKeysFromRecord builds RunInput.OldKeys for Apply from the issue record (ADR 0113, ADR 0114).
//
// Each sender of either generation whose record holds an issue or rotation credential gets an entry: its keys not yet
// revoked, each with its issue time (the gate needs it, because a server refuses a user JWT only when the revocation
// is at or after its issue time). A sender whose credentials are all revoked has an empty entry. Probes are not
// inventory. It refuses (ErrIncompleteInventory) a credential without an issue time, and a sender the gate needs
// the inventory of that the record holds no credential for, so an empty inventory never stands in for a missing one.
// With a nil record the inventory is not built: that is allowed only when no sender needs one.
func oldKeysFromRecord(ctx context.Context, rec IssueInventory, prev *listset.Compiled, ls listset.ListSet) (map[string][]OldKey, error) {
	need, senders := needsInventory(prev, ls)
	if rec == nil {
		if len(need) > 0 {
			return nil, fmt.Errorf("%w: no issue record for the old keys of %v", ErrIncompleteInventory, need)
		}
		return nil, nil
	}
	all, err := rec.AllMaxIssued(ctx)
	if err != nil {
		return nil, fmt.Errorf("read issue record: %w", err)
	}
	out := map[string][]OldKey{}
	for _, e := range all {
		if !revocable(e) || !senders[e.Address] {
			continue
		}
		if _, ok := out[e.Address]; !ok {
			out[e.Address] = []OldKey{}
		}
		// A key already revoked in the record is not inventory here. RunInput.OldKeys cannot carry revoked-in-record
		// keys to the gate without a change in run.go, but none is needed: Rotate hands every revocable old key of a
		// narrowed sender to the Revoker again on a resumed attempt, even one the record already marks revoked, and
		// Run merges what AfterPush returns into both the gate's OldKeys and the revocations of the final JWT.
		// A sender removed in a retry, with no AfterPush entry for it, is covered by RemoveSender, which skips
		// revoked keys: see the report of this change.
		if e.RevokedAt != nil {
			continue
		}
		if e.IssuedAt.IsZero() {
			return nil, fmt.Errorf("%w: the credential %s of %s has no issue time", ErrIncompleteInventory, e.PublicKey, e.Address)
		}
		if !slices.ContainsFunc(out[e.Address], func(o OldKey) bool { return o.PublicKey == e.PublicKey }) {
			out[e.Address] = append(out[e.Address], OldKey{PublicKey: e.PublicKey, IssuedAt: e.IssuedAt})
		}
	}
	for _, s := range need {
		if _, ok := out[s]; !ok {
			return nil, fmt.Errorf("%w: the issue record holds no credential of %s", ErrIncompleteInventory, s)
		}
	}
	return out, nil
}
