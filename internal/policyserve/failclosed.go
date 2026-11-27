package policyserve

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policydoc"
	"github.com/bogworks/mash/internal/sap"
)

// ErrNotDecided is wrapped by the fail-closed steps: the behaviour they stand for is not decided, so nothing is done.
var ErrNotDecided = errors.New("not available yet")

// maxNamedAddresses is how many addresses a refusal names; the rest are counted, so the status stays short.
const maxNamedAddresses = 3

// Reasons the Applier gives when it refuses a change before anything is pushed.
const (
	// NewAddressDetail is what a refusal for an address with no inbox says after naming the addresses. It is the
	// status text, so it is plain and, with the names, under the 500 byte limit for a refusal.
	NewAddressDetail = "A new address needs its endpoint made first, with `mashctl endpoint create`, and then a credential with its tags, which the apply does not issue. " +
		"The change was refused and nothing was pushed; submit it again once the endpoint exists."
	// NarrowingDetail is the reason a change that narrows an existing sender by tags is refused. Same limits as above.
	NarrowingDetail = "this change narrows what an existing sender may reach by tags, which needs a new credential for that sender. " +
		"Replacing a sender's credential is not part of this release, so there is nothing to do about it yet: " +
		"the change was refused and nothing was pushed. Submit a list set that does not narrow an existing sender by tags."
)

// NoInboxDetail is the status text of a change that adds addresses that have no inbox yet.
func NoInboxDetail(addresses []string) string {
	shown := addresses
	if len(shown) > maxNamedAddresses {
		shown = shown[:maxNamedAddresses]
	}
	list := strings.Join(shown, ", ")
	if n := len(addresses) - len(shown); n > 0 {
		list += fmt.Sprintf(" and %d more", n)
	}
	return list + ": no inbox yet. " + NewAddressDetail
}

// SuspendedInboxDetail is the status text of a change that adds addresses whose inbox is suspended: a suspended inbox
// receives no mail, so the address would be admitted but unreachable.
func SuspendedInboxDetail(addresses []string) string {
	shown := addresses
	if len(shown) > maxNamedAddresses {
		shown = shown[:maxNamedAddresses]
	}
	list := strings.Join(shown, ", ")
	if n := len(addresses) - len(shown); n > 0 {
		list += fmt.Sprintf(" and %d more", n)
	}
	return list + ": its inbox is suspended; resume it first (mashctl endpoint resume ADDRESS). The change was refused and nothing was pushed; " +
		"submit it again once the inbox is resumed."
}

// InboxChecker reads whether an address has the inbox mail depends on and creates nothing. *admin.Admin is one.
type InboxChecker interface {
	CheckInbox(ctx context.Context, addr sap.Address) (admin.InboxCheck, error)
}

var _ InboxChecker = (*admin.Admin)(nil)

// Provisioner is the policyapply.Provisioner of the service. It creates no inbox and issues no credential: a list set
// does not carry the endpoint's public key, and mash never holds an endpoint's private key (ADR 0104), so an
// address's endpoint is made first, with `mashctl endpoint create`, and applying a list set only checks it.
//
// CreateInbox verifies, through Inboxes, that the address has its inbox stream and durable consumer, with the
// configuration mail depends on. The Applier runs the same check for every added address before anything is pushed,
// so a step reached here finds the inbox it found then.
//
// IssueCredential issues nothing: credentials come from `mashctl endpoint create` and `mashctl endpoint reissue` (ADR 0104). It only
// checks that the address is issuable, so a step reached for an address that no list set in the run names, or whose
// owner group has no scoped key, fails. It is idempotent.
type Provisioner struct {
	Keys Keys
	// Sets are the list sets in force and being applied; an address of any of them is issuable.
	Sets []*listset.Compiled
	// Inboxes is the read-only inbox check. Nil means no address has an inbox.
	Inboxes InboxChecker
}

var _ policyapply.Provisioner = Provisioner{}

// SuspendedReason follows the address in the answer of MissingInbox for a suspended inbox.
const SuspendedReason = ": its inbox is suspended; resume it first"

// MissingInbox says what the inbox of address lacks, in plain words, or "" when it has a stream and a consumer. A
// suspended inbox counts as lacking: it receives no mail, so the answer is that it is suspended, with the resume step.
// Callers tell that answer apart with SuspendedReason. A
// stream or consumer that exists but does not have the configuration mail depends on is an error, as is a failed read.
func (p Provisioner) MissingInbox(ctx context.Context, address string) (string, error) {
	id, err := sap.ParseAddress(address)
	if err != nil {
		return "", fmt.Errorf("address %.64q is not owner.group.name: %w", address, err)
	}
	if p.Inboxes == nil {
		return address + " has no inbox stream", nil
	}
	got, err := p.Inboxes.CheckInbox(ctx, id)
	switch {
	case err != nil:
		return "", err
	case got.Stream == "":
		return address + " has no inbox stream", nil
	case got.Suspended:
		return address + SuspendedReason + "; run mashctl endpoint resume " + id.String(), nil
	case !got.Consumer:
		return address + " has an inbox stream (" + got.Stream + ") but not the consumer that reads it", nil
	}
	return "", nil
}

// VerifiesOnly implements policyapply.VerifyOnly: CreateInbox creates nothing, so no provisioning marker is needed.
func (Provisioner) VerifiesOnly() bool { return true }

var _ policyapply.VerifyOnly = Provisioner{}

// CreateInbox implements policyapply.Provisioner. It creates nothing: it fails, naming what is missing, unless the
// address's inbox exists.
func (p Provisioner) CreateInbox(ctx context.Context, address string) error {
	missing, err := p.MissingInbox(ctx, address)
	if err != nil {
		return err
	}
	if missing != "" {
		return errors.New(missing + "; the endpoint must be provisioned first")
	}
	return nil
}

// IssueCredential implements policyapply.Provisioner. It is idempotent and changes nothing.
func (p Provisioner) IssueCredential(_ context.Context, address string) error {
	group := ""
	for _, c := range p.Sets {
		if c == nil {
			continue
		}
		for _, u := range c.Users {
			if u.Sender == address {
				group, _, _ = cutLast(address)
			}
		}
	}
	if group == "" {
		return errors.New("the address is in no list set of this run, so no credential can be issued for it")
	}
	pubs, err := p.Keys.GroupPublics()
	if err != nil {
		return fmt.Errorf("read the owner group keys: %w", err)
	}
	if _, ok := pubs[group]; !ok {
		return fmt.Errorf("owner group %s has no scoped key, so no credential can be issued for its addresses", group)
	}
	return nil
}

// cutLast splits "owner.group.instance" into "owner.group" and "instance".
func cutLast(address string) (group, name string, ok bool) {
	i := strings.LastIndex(address, ".")
	if i < 0 {
		return "", address, false
	}
	return address[:i], address[i+1:], true
}

// FailClosedDeliverer is the Deliverer of the service: it places nothing. mash never holds a sender's seed, and how a
// rotated sender gets its new credential is not decided. The Applier refuses a narrowing change before the push.
type FailClosedDeliverer struct{}

var _ policyapply.Deliverer = FailClosedDeliverer{}

// Deliver implements policyapply.Deliverer.
func (FailClosedDeliverer) Deliver(context.Context, string, string) error {
	return errors.New("delivering a rotated credential is " + ErrNotDecided.Error())
}

// FailClosed is the checks the Applier makes before it signs or pushes anything, in its order: every address the change
// adds must already have its inbox (a suspended one first, then a missing one; made only when checkInbox is true, as
// the Applier makes it only when it provisions), then the change must not narrow an existing sender by tags. prev is
// the list set in force, nil when none. It returns the refusals, each with the stage and the exact reason text the
// Applier gives (the first is the one Apply reports), or none when the change passes. The error is a read of an inbox
// that failed, worded as Apply words it. `mashctl policy diff` calls this too, so the two cannot say different things.
func FailClosed(ctx context.Context, prev, target *listset.Compiled, inboxes InboxChecker, checkInbox bool) ([]listset.Refusal, error) {
	ex, err := policyapply.NewExpect(prev, target, nil)
	if err != nil {
		return nil, err
	}
	var out []listset.Refusal
	if checkInbox {
		svc := Provisioner{Sets: []*listset.Compiled{prev, target}, Inboxes: inboxes}
		var missing, suspended []string
		for _, na := range policyapply.NewOrder(prev, target, ex).NewAddresses {
			m, err := svc.MissingInbox(ctx, na.Address)
			if err != nil {
				return nil, fmt.Errorf("the inbox of %s cannot be used: %w; nothing was pushed", na.Address, err)
			}
			switch {
			case strings.HasPrefix(m, na.Address+SuspendedReason):
				suspended = append(suspended, na.Address)
			case m != "":
				missing = append(missing, na.Address)
			}
		}
		if len(suspended) > 0 {
			out = append(out, listset.Refusal{Stage: policydoc.StageInbox, Entry: "inbox", Reason: SuspendedInboxDetail(suspended)})
		}
		if len(missing) > 0 {
			out = append(out, listset.Refusal{Stage: policydoc.StageInbox, Entry: "inbox", Reason: NoInboxDetail(missing)})
		}
	}
	if len(ex.CredentialOnly) > 0 {
		out = append(out, listset.Refusal{Stage: policydoc.StageNarrowing, Entry: "narrowing", Reason: NarrowingDetail})
	}
	return out, nil
}
