package policyapply

import (
	"fmt"
	"slices"
	"sort"

	"github.com/bogworks/mash/internal/listset"
)

// RemovedReach is one (sender, verb, receiver) the previous generation's credential reached and the target's does
// not.
type RemovedReach struct {
	Sender   string
	Verb     listset.Verb
	Receiver string
	Op       Op
	Subject  string
	// Probe is the sender's old credential.
	Probe ProbeCred
	// Current is the sender's target credential: its name and tags in the target Compiled. Filled for every entry.
	// Only a CredentialOnly entry is probed with it. A tag-only narrowing cannot be refused for the old credential by
	// the template, because the template still grants the old tags the reach: only revoking the old key refuses it
	// (REQ-014's narrowing rotation, a later change, makes sure that key is revoked). The gate proves the target
	// credential is refused and that each witness was closed.
	Current ProbeCred
}

// Expect is what the gate checks for a target generation.
type Expect struct {
	// RevokedKeys are the user public keys the change revokes (an input), sorted.
	RevokedKeys []string
	// Removed are the reaches the change takes away for senders present in both generations, and that the old
	// credential (a probe) loses under the target's template: the gate probes each on every server. Sorted by sender,
	// verb and receiver.
	Removed []RemovedReach
	// CredentialOnly are the reaches the change takes away that the old credential's probe cannot test (a tag-only
	// narrowing). The target's template, expanded with the OLD tags, still grants them, so the old credential is
	// refused only by revoking its key (REQ-014's narrowing rotation, a later change, makes sure it is revoked). The
	// gate probes each with the target credential (Current) and requires it refused, and requires each witness
	// closed. It also holds the reaches of a sender whose old credential cannot be expanded under the target's template
	// (the template needs a tag the old tags lack): the old credential cannot connect, which is not a refusal of one
	// send. Sorted as Removed.
	CredentialOnly []RemovedReach
}

// NewExpect derives what the gate checks, purely from the previous generation's Compiled (nil when none) and the
// target's, plus the revoked keys. The universe is every sender of either generation. A sender only in one of them
// has no removed reach: a removed sender is covered by its revoked keys.
func NewExpect(prev, target *listset.Compiled, revoked []string) (Expect, error) {
	e := Expect{RevokedKeys: slices.Clone(revoked)}
	slices.Sort(e.RevokedKeys)
	if prev == nil || target == nil {
		return e, nil
	}
	universe := map[string]bool{}
	oldUsers := map[string]listset.User{}
	newUsers := map[string]bool{}
	newUserByS := map[string]listset.User{}
	for _, u := range prev.Users {
		universe[u.Sender] = true
		oldUsers[u.Sender] = u
	}
	for _, u := range target.Users {
		universe[u.Sender] = true
		newUsers[u.Sender] = true
		newUserByS[u.Sender] = u
	}
	all := make([]string, 0, len(universe))
	for a := range universe {
		all = append(all, a)
	}
	sort.Strings(all)
	for _, s := range all {
		u, wasOld := oldUsers[s]
		if !wasOld || !newUsers[s] {
			continue
		}
		before, err := listset.ReachOf(prev, s, all)
		if err != nil {
			return Expect{}, fmt.Errorf("previous generation: %w", err)
		}
		after, err := listset.ReachOf(target, s, all)
		if err != nil {
			return Expect{}, fmt.Errorf("target generation: %w", err)
		}
		// The target's template with the OLD tags. When it cannot be expanded (a template line that needs a tag the old
		// credential lacks, such as a new tag-driven deny line) the server cannot take that credential at all, and a
		// send with it cannot be shown refused by one probe: every removed reach is credential-only, tested with the
		// target credential.
		oldCred, err := listset.ReachOf(withUser(target, u), s, all)
		if err != nil {
			oldCred = map[listset.Verb][]string{}
			for _, v := range listset.Verbs {
				oldCred[v] = all
			}
		}
		nu := newUserByS[s]
		current := ProbeCred{Sender: s, Name: nu.Name, Tags: slices.Clone(nu.Tags)}
		for _, v := range listset.Verbs {
			for _, r := range before[v] {
				if r == s || slices.Contains(after[v], r) { // a send to oneself is no policy
					continue
				}
				rr := RemovedReach{
					Sender: s, Verb: v, Receiver: r, Op: OpOf(v), Subject: listset.Subject(v, r),
					Probe: ProbeCred{Sender: s, Name: u.Name, Tags: slices.Clone(u.Tags)}, Current: current,
				}
				if slices.Contains(oldCred[v], r) {
					e.CredentialOnly = append(e.CredentialOnly, rr)
				} else {
					e.Removed = append(e.Removed, rr)
				}
			}
		}
	}
	return e, nil
}

// withUser returns c with the user of u.Sender replaced by u: the target's templates with that sender's old name and
// tags. c is not modified.
func withUser(c *listset.Compiled, u listset.User) *listset.Compiled {
	out := &listset.Compiled{Range: c.Range, Templates: c.Templates}
	for _, x := range c.Users {
		if x.Sender == u.Sender {
			x = u
		}
		out.Users = append(out.Users, x)
	}
	return out
}
