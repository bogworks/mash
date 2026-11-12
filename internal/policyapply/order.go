package policyapply

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"

	"github.com/bogworks/mash/internal/listset"
)

// Ordering (contract REQ-015, AT-014): a new receiver is reachable only after the senders that must refuse it.
//
// A list set can add an address that some sender must not reach, for example because its owner line is shared and the
// sender denies the new address. Until REQ-011's gate has passed for that sender, the sender's old credential may
// still reach the address. So mash does nothing that lets the new address receive until the gate has passed for every
// sender that must refuse it: creating its inbox stream or consumer (Provisioner.CreateInbox) or issuing its credential
// (Provisioner.IssueCredential). The second rule is for an existing address newly denied to a sender: the change is
// reported applied only after that sender's gate passes (Applied).
//
// Cost. Each push that finishes a sender's gate closes every connection under that owner group's scoped key, so every
// sender under the key is disconnected, not only the one whose reach changed (ADR 0097). RunOrdered pushes once for
// the whole change, so the price is paid once per owner group, not once per sender.
//
// Provisioning happens inside the run, after the gate reports applied and before the generation is committed. If a
// step fails nothing is committed and the generation is pending: resubmitting the same list set pushes and gates again
// and resumes the provisioning, so a Provisioner's steps must be idempotent.

// NewAddress is an address the target generation adds and the senders that must refuse it.
type NewAddress struct {
	Address string
	// MustRefuse are the senders that must not reach Address, sorted: those with a removed reach to it.
	MustRefuse []string
}

// Denial is an existing address a sender reached in the previous generation and does not in the target.
type Denial struct {
	Sender   string
	Receiver string
}

// Order is what REQ-015 needs to know about one change.
type Order struct {
	// NewAddresses are the addresses of the target that the previous generation lacks, sorted. When there is no
	// previous generation every address is new.
	NewAddresses []NewAddress
	// NewlyDenied are the (sender, receiver) pairs whose receiver existed before and is taken away, sorted.
	NewlyDenied []Denial
}

// NewOrder derives the ordering of a change from the previous generation's Compiled (nil when none), the target's and
// the change's Expect. A sender must refuse a new address when Expect has a removed reach (Removed or CredentialOnly)
// to it; NewExpect computes reach over the addresses of both generations, so a previous owner line that would have
// reached the new address counts.
func NewOrder(prev, target *listset.Compiled, e Expect) Order {
	var o Order
	if target == nil {
		return o
	}
	had := map[string]bool{}
	if prev != nil {
		for _, u := range prev.Users {
			had[u.Sender] = true
		}
	}
	refuse := map[string]map[string]bool{}
	denied := map[Denial]bool{}
	for _, r := range slices.Concat(e.Removed, e.CredentialOnly) {
		if refuse[r.Receiver] == nil {
			refuse[r.Receiver] = map[string]bool{}
		}
		refuse[r.Receiver][r.Sender] = true
		if had[r.Receiver] {
			denied[Denial{Sender: r.Sender, Receiver: r.Receiver}] = true
		}
	}
	for _, u := range target.Users {
		if had[u.Sender] {
			continue
		}
		na := NewAddress{Address: u.Sender, MustRefuse: []string{}}
		for s := range refuse[u.Sender] {
			na.MustRefuse = append(na.MustRefuse, s)
		}
		sort.Strings(na.MustRefuse)
		o.NewAddresses = append(o.NewAddresses, na)
	}
	sort.Slice(o.NewAddresses, func(i, j int) bool { return o.NewAddresses[i].Address < o.NewAddresses[j].Address })
	for d := range denied {
		o.NewlyDenied = append(o.NewlyDenied, d)
	}
	sort.Slice(o.NewlyDenied, func(i, j int) bool {
		a, b := o.NewlyDenied[i], o.NewlyDenied[j]
		if a.Sender != b.Sender {
			return a.Sender < b.Sender
		}
		return a.Receiver < b.Receiver
	})
	return o
}

// SenderPassed reports whether REQ-011's gate has passed for sender in st: st names at least one server, every server
// holds the target generation, and st has no failure for that sender. A failure with an empty Sender is server-wide
// (a server missing the push, an unreadable revocation list, an incomplete server set) and counts against every
// sender. A failure of another sender does not.
func SenderPassed(st Status, sender string) bool {
	if len(st.Servers) == 0 {
		return false
	}
	for _, s := range st.Servers {
		if s.Generation == nil || *s.Generation != st.Generation {
			return false
		}
	}
	for _, f := range st.Failing {
		if f.Sender == "" || f.Sender == sender {
			return false
		}
	}
	return true
}

// Applied is REQ-015's second rule: a change that newly denies d.Receiver to d.Sender is reported applied only after
// that sender's gate passes.
func Applied(st Status, d Denial) bool { return SenderPassed(st, d.Sender) }

// Provisioner takes the steps that let an address receive. RunOrdered calls it only inside a run, after the gate
// reports applied, and only for an address of the target list set that Run accepted and whose senders that must refuse
// it have passed. Both steps MUST be idempotent (create-if-absent): a run whose provisioning failed is resubmitted and
// calls them again for every new address, including those that already succeeded.
type Provisioner interface {
	// CreateInbox creates the inbox stream and consumer of address, or leaves them if they exist.
	CreateInbox(ctx context.Context, address string) error
	// IssueCredential issues the credential of address, or leaves it if it is issued.
	IssueCredential(ctx context.Context, address string) error
}

// The provisioning steps a ProvisionError names.
const (
	StepCreateInbox     = "create_inbox"
	StepIssueCredential = "issue_credential"
)

// The reasons a HeldAddress names.
const (
	// HeldListSetRefused: the list set was refused, nothing was pushed and the gate never ran.
	HeldListSetRefused = "list set refused"
	// HeldSendersNotPassed: the gate did not pass for the senders in Blocking.
	HeldSendersNotPassed = "senders not passed"
	// HeldNotApplied: the generation was not applied although no sender that must refuse the address is named.
	HeldNotApplied = "generation not applied"
)

// HeldAddress is a new address nothing was provisioned for. Blocking lists the senders whose gate did not pass, and is
// empty unless Reason is HeldSendersNotPassed.
type HeldAddress struct {
	Address  string   `json:"address"`
	Reason   string   `json:"reason"`
	Blocking []string `json:"blocking"`
}

// ProvisionError is a failed provisioning step; the steps after it were not taken for that address.
type ProvisionError struct {
	Address string `json:"address"`
	Step    string `json:"step"`
	Detail  string `json:"detail"`
}

// OrderedStatus is the report of RunOrdered. Its JSON is stable: snake_case, slices never null and sorted.
type OrderedStatus struct {
	Status      Status           `json:"status"`
	Provisioned []string         `json:"provisioned"`
	Held        []HeldAddress    `json:"held"`
	Errors      []ProvisionError `json:"errors"`
}

// MarshalJSON writes the stable form.
func (o OrderedStatus) MarshalJSON() ([]byte, error) {
	type plain OrderedStatus
	p := plain(o)
	p.Provisioned = append([]string{}, o.Provisioned...)
	sort.Strings(p.Provisioned)
	p.Held = make([]HeldAddress, 0, len(o.Held))
	for _, h := range o.Held {
		h.Blocking = append([]string{}, h.Blocking...)
		sort.Strings(h.Blocking)
		p.Held = append(p.Held, h)
	}
	sort.Slice(p.Held, func(i, j int) bool { return p.Held[i].Address < p.Held[j].Address })
	p.Errors = append([]ProvisionError{}, o.Errors...)
	sort.SliceStable(p.Errors, func(i, j int) bool {
		if p.Errors[i].Address != p.Errors[j].Address {
			return p.Errors[i].Address < p.Errors[j].Address
		}
		return p.Errors[i].Step < p.Errors[j].Step
	})
	return json.Marshal(p)
}

// errProvisioning is what the BeforeCommit hook of RunOrdered returns when a provisioning step failed.
var errProvisioning = errors.New("provisioning failed")

// RunOrdered runs the list set with Run, which includes REQ-011's gate, and provisions the new addresses inside the run.
//
// It takes no Order: the order is derived here with NewOrder from in.Previous and the target Compiled and Expect that
// Run accepted and froze, so no caller input can add an address or remove a sender that must refuse one. After the gate
// reports applied, and before the generation is committed, every new address of the order whose MustRefuse senders
// all passed the gate (SenderPassed) is provisioned (CreateInbox, then IssueCredential, stopping at the first error for
// that address). The generation is committed only if every step succeeded. If a step fails, nothing is committed, the
// generation is pending (see Run), and the returned Status is partial, not in force, with a CheckProvision failure
// naming the address and step; Errors names them too and the error is nil. Resubmitting the same list set pushes and
// gates again and resumes provisioning, which is why the Provisioner's steps must be idempotent.
//
// When the generation is not applied, or the list set was refused, nothing is provisioned and the new addresses are
// held, each with its Reason. The Provisioner is never called before the gate has applied. An error from Run (a stale
// base, a pending generation, a signing error, a commit failure) is returned as is. See the package comment for the
// cost of the push. A hook the caller set in in.BeforeCommit runs first.
func RunOrdered(ctx context.Context, in RunInput, prov Provisioner) (OrderedStatus, error) {
	var out OrderedStatus
	var acc Accepted
	userHook := in.BeforeCommit
	in.BeforeCommit = func(ctx context.Context, a Accepted, st Status) error {
		if userHook != nil {
			if err := userHook(ctx, a, st); err != nil {
				return err
			}
		}
		failed := false
		for _, na := range NewOrder(in.Previous, a.Compiled, a.Expect).NewAddresses {
			if len(blockingOf(st, na)) > 0 { // defence: an applied gate has no failures
				continue
			}
			if err := prov.CreateInbox(ctx, na.Address); err != nil {
				out.Errors = append(out.Errors, ProvisionError{Address: na.Address, Step: StepCreateInbox, Detail: err.Error()})
				failed = true
				continue
			}
			if err := prov.IssueCredential(ctx, na.Address); err != nil {
				out.Errors = append(out.Errors, ProvisionError{Address: na.Address, Step: StepIssueCredential, Detail: err.Error()})
				failed = true
				continue
			}
			out.Provisioned = append(out.Provisioned, na.Address)
		}
		if failed {
			return errProvisioning
		}
		return nil
	}
	st, err := run(ctx, in, &acc)
	out.Status = st
	switch {
	case errors.Is(err, errProvisioning):
		out.Status.State, out.Status.InForce = StatePartial, false
		for _, pe := range out.Errors {
			out.Status.Failing = append(out.Status.Failing, Failure{Check: CheckProvision, Detail: pe.Address + ": " + pe.Step + ": " + pe.Detail})
		}
		sortFailures(out.Status.Failing)
		return out, nil
	case err != nil:
		return out, err
	case st.State == StateApplied:
		return out, nil
	}
	// Not applied: every new address is held.
	if acc.Compiled == nil { // refused: nothing was accepted, and nothing is provisioned from the submission
		for _, a := range newAddressesOf(in) {
			out.Held = append(out.Held, HeldAddress{Address: a, Reason: HeldListSetRefused})
		}
		return out, nil
	}
	for _, na := range NewOrder(in.Previous, acc.Compiled, acc.Expect).NewAddresses {
		h := HeldAddress{Address: na.Address, Reason: HeldNotApplied}
		if b := blockingOf(st, na); len(b) > 0 {
			h.Reason, h.Blocking = HeldSendersNotPassed, b
		}
		out.Held = append(out.Held, h)
	}
	return out, nil
}

// blockingOf returns the senders that must refuse na and have not passed the gate in st.
func blockingOf(st Status, na NewAddress) []string {
	var blocking []string
	for _, s := range na.MustRefuse {
		if !SenderPassed(st, s) {
			blocking = append(blocking, s)
		}
	}
	return blocking
}

// newAddressesOf lists the senders of the submitted list set that the previous generation lacks. It is used only to
// report held addresses when the list set was refused and nothing was accepted; nothing is provisioned from it.
func newAddressesOf(in RunInput) []string {
	had := map[string]bool{}
	if in.Previous != nil {
		for _, u := range in.Previous.Users {
			had[u.Sender] = true
		}
	}
	var out []string
	for _, s := range in.ListSet.Senders {
		if !had[s.Sender] && !slices.Contains(out, s.Sender) {
			out = append(out, s.Sender)
		}
	}
	sort.Strings(out)
	return out
}
