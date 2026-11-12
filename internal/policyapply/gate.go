package policyapply

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/listset"
)

// GateInput is everything the gate needs to judge one target generation.
type GateInput struct {
	Cluster   Cluster
	Prober    Prober
	Witnesses Witnesses
	// Account is the account public key whose JWT the servers hold.
	Account string
	// Inventory is the operator's server count (0 when none was given), as for apply.ServerSet.Complete.
	Inventory int
	// Record is the target generation's record; Compiled is its frozen compiled output.
	Record   listset.Record
	Compiled *listset.Compiled
	// Previous is the generation before the target, nil for the first.
	Previous *Candidate
	// Expect is NewExpect for this change; its RevokedKeys are the target's.
	Expect Expect
	// Signers are the expected scoped signing keys of the target (see Candidate.Signers). Required: Gate fails
	// closed without them.
	Signers map[string]string
	// OldKeys maps each sender to the public keys of its credentials issued before this change (the caller fills it
	// from the issue record). Every sender with a credential-only reach, and every sender the change removes, needs
	// an entry, and each of its keys must be in Expect.RevokedKeys: a tag-only narrowing leaves the old credential's
	// reach in the template, and only revoking its key refuses it.
	OldKeys map[string][]string
	// Now gives the time stamped on the Status; nil means time.Now.
	Now func() time.Time
}

func (in GateInput) target() Candidate {
	return Candidate{Generation: in.Record.Generation, Compiled: in.Compiled, RevokedKeys: in.Expect.RevokedKeys, Signers: in.Signers}
}

// Gate judges the target generation and reports it. It collects every failure rather than stopping at the first.
// Steps, in order: the server set is complete; each server's generation; each revoked key is on every server; every
// witness is closed; every removed reach is refused on every server with the old credential, and every credential-only reach with the target credential. The state is applied only with no failure and
// every server on the target, else partial. An error is returned only when ctx is done; a check that cannot be made is
// a failure, because it is not proven.
func Gate(ctx context.Context, in GateInput) (Status, error) {
	st := newStatus(in.Record, StatePartial, in.Now)
	add := func(server, sender, check, detail string) {
		st.Failing = append(st.Failing, Failure{Server: server, Sender: sender, Check: check, Detail: detail})
	}
	finish := func() (Status, error) {
		sortFailures(st.Failing)
		if err := ctx.Err(); err != nil {
			return st, err
		}
		allTarget := len(st.Servers) > 0
		for _, s := range st.Servers {
			if s.Generation == nil || *s.Generation != in.Record.Generation {
				allTarget = false
			}
		}
		if len(st.Failing) == 0 && allTarget {
			st.State, st.InForce = StateApplied, true
		}
		return st, nil
	}

	set, err := in.Cluster.Servers(ctx)
	switch {
	case err != nil:
		add("", "", CheckServerSet, fmt.Sprintf("server set: %v", err))
		return finish()
	case !set.Complete(in.Inventory):
		add("", "", CheckServerSet, set.Why(in.Inventory))
		return finish()
	}
	names := slices.Clone(set.Names)
	sort.Strings(names)

	if in.Signers == nil {
		add("", "", CheckGeneration, NoSignersDetail)
		return finish()
	}
	in.checkOldKeys(add)

	jwts, err := in.Cluster.AccountJWTs(ctx, in.Account)
	jwtsRead := err == nil
	if err != nil {
		add("", "", CheckGeneration, fmt.Sprintf("read account JWTs: %v", err))
	}
	cands := []Candidate{in.target()}
	if in.Previous != nil {
		cands = append(cands, *in.Previous)
	}
	claims := map[string]*jwt.AccountClaims{}
	for _, n := range names {
		sg := ServerGeneration{Server: n}
		if tok := jwts[n]; tok != "" {
			if c, derr := jwt.DecodeAccountClaims(tok); derr == nil {
				claims[n] = c
				if g, ok := generationOfClaims(c, cands); ok {
					sg.Generation = &g
				}
			}
		}
		st.Servers = append(st.Servers, sg)
		switch {
		case sg.Generation == nil && jwtsRead:
			add(n, "", CheckGeneration, "holds an account JWT that matches no known generation, or none")
		case sg.Generation != nil && *sg.Generation != in.Record.Generation:
			add(n, "", CheckGeneration, fmt.Sprintf("holds generation %d, want %d", *sg.Generation, in.Record.Generation))
		}
	}

	for _, n := range names {
		for _, k := range in.Expect.RevokedKeys {
			if c := claims[n]; c == nil {
				add(n, "", CheckRevoked, "revocations unreadable: "+k)
			} else if _, has := c.Revocations[k]; !has {
				add(n, "", CheckRevoked, "key not refused: "+k)
			}
		}
	}

	in.checkWitnesses(ctx, names, add)

	for _, n := range names {
		for _, r := range in.Expect.Removed {
			refused, perr := in.Prober.Refused(ctx, n, r.Probe, r.Op, r.Subject)
			switch {
			case perr != nil:
				add(n, r.Sender, CheckRefused, fmt.Sprintf("probe %s %s failed: %v", r.Op, r.Subject, perr))
			case !refused:
				add(n, r.Sender, CheckRefused, fmt.Sprintf("%s %s was not refused", r.Op, r.Subject))
			}
		}
		// A Removed entry needs no Current probe: Removed holds only reaches the target generation does not grant, so
		// the old-tag probe is the stronger check.
		for _, r := range in.Expect.CredentialOnly {
			refused, perr := in.Prober.Refused(ctx, n, r.Current, r.Op, r.Subject)
			switch {
			case perr != nil:
				add(n, r.Sender, CheckRefusedCurrent, fmt.Sprintf("probe %s %s as the target credential failed: %v", r.Op, r.Subject, perr))
			case !refused:
				add(n, r.Sender, CheckRefusedCurrent, fmt.Sprintf("%s %s (%s from %s to %s) was not refused for the target credential", r.Op, r.Subject, r.Verb, r.Sender, r.Receiver))
			}
		}
	}
	return finish()
}

// checkOldKeys requires every old key of a sender with a credential-only reach, or removed by the change, to be
// revoked. A sender with no entry in OldKeys fails: the inventory is required.
func (in GateInput) checkOldKeys(add func(server, sender, check, detail string)) {
	need := map[string]bool{}
	for _, r := range in.Expect.CredentialOnly {
		need[r.Sender] = true
	}
	if in.Previous != nil && in.Previous.Compiled != nil && in.Compiled != nil {
		now := map[string]bool{}
		for _, u := range in.Compiled.Users {
			now[u.Sender] = true
		}
		for _, u := range in.Previous.Compiled.Users {
			if !now[u.Sender] {
				need[u.Sender] = true
			}
		}
	}
	senders := make([]string, 0, len(need))
	for s := range need {
		senders = append(senders, s)
	}
	sort.Strings(senders)
	for _, s := range senders {
		keys, has := in.OldKeys[s]
		if !has {
			add("", s, CheckOldKeyNotRevoked, "no inventory of the sender's old keys")
			continue
		}
		for _, k := range keys {
			if !slices.Contains(in.Expect.RevokedKeys, k) {
				add("", s, CheckOldKeyNotRevoked, "old key not revoked: "+k)
			}
		}
	}
}

// checkWitnesses requires a closed witness for every server and every sender with a removed reach or removed by the
// change, and for every witness the transport reports.
func (in GateInput) checkWitnesses(ctx context.Context, names []string, add func(server, sender, check, detail string)) {
	if len(in.Expect.Removed) == 0 && len(in.Expect.CredentialOnly) == 0 && len(in.Expect.RemovedSenders) == 0 && in.Witnesses == nil {
		return
	}
	closed := map[WitnessKey]bool{}
	if in.Witnesses != nil {
		var err error
		if closed, err = in.Witnesses.Closed(ctx); err != nil {
			add("", "", CheckWitness, fmt.Sprintf("read witnesses: %v", err))
			return
		}
	}
	need := map[WitnessKey]bool{}
	for k := range closed {
		need[k] = true
	}
	for _, r := range slices.Concat(in.Expect.Removed, in.Expect.CredentialOnly) {
		for _, n := range names {
			need[WitnessKey{n, r.Sender}] = true
		}
	}
	for _, s := range in.Expect.RemovedSenders {
		for _, n := range names {
			need[WitnessKey{n, s}] = true
		}
	}
	for k := range need {
		if !closed[k] {
			add(k.Server, k.Sender, CheckWitness, "a connection open before the change is still open")
		}
	}
}
