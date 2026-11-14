package policyserve

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
)

// ErrNotDecided is wrapped by the fail-closed steps: the behaviour they stand for is not decided, so nothing is done.
var ErrNotDecided = errors.New("not available yet")

// Reasons the Applier gives when it refuses a change before anything is pushed.
const (
	// NewAddressDetail is the reason a change that adds an address is refused. It is the status text, so it is plain
	// and under the intake's 500 byte limit for a refusal.
	NewAddressDetail = "this change adds an address. An address's inbox can be made only for an endpoint that was provisioned under its own key, " +
		"and mash cannot provision one from a list set yet. The change was refused and nothing was pushed. " +
		"There is nothing to do about it until that step exists; the list set can be submitted again then."
	// NarrowingDetail is the reason a change that narrows an existing sender by tags is refused. Same limits as above.
	NarrowingDetail = "this change narrows what an existing sender may reach by tags, which needs a new credential for that sender. " +
		"Replacing a sender's credential is not part of this release, so there is nothing to do about it yet: " +
		"the change was refused and nothing was pushed. Submit a list set that does not narrow an existing sender by tags."
)

// Provisioner is the policyapply.Provisioner of the service. It creates no inbox and issues no credential.
//
// CreateInbox is not available for any new address. Admin.EnsureInbox takes a legacy endpoint name (config.ValidName:
// letters, digits and single hyphens), and a list-set address range.owner.name is a new-form identity whose key is the
// flat form range_owner_name, which that rule refuses. The inbox of a new-form identity is made only by
// Admin.ProvisionIdentity, which also writes the endpoint's directory record and needs the endpoint's user public key,
// which a list set does not carry and mash does not have until the issue service has issued the credential. So the
// Applier refuses a change that adds an address before anything is pushed, and this step fails if it is reached.
//
// IssueCredential issues nothing: credentials come from the issue service on a wrangler's request (ADR 0104). It only
// checks that the address is issuable, so a step reached for an address that no list set in the run names, or whose
// owner group has no scoped key, fails.
type Provisioner struct {
	Keys Keys
	// Sets are the list sets in force and being applied; an address of any of them is issuable.
	Sets []*listset.Compiled
}

var _ policyapply.Provisioner = Provisioner{}

// CreateInbox implements policyapply.Provisioner.
func (Provisioner) CreateInbox(context.Context, string) error {
	return errors.New("creating the inbox of a new address is " + ErrNotDecided.Error())
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

// cutLast splits "range.owner.name" into "range.owner" and "name".
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
