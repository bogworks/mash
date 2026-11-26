package endpoint

import (
	"context"
	"fmt"
	"strings"

	"github.com/bogworks/mash/internal/namerule"
	"github.com/bogworks/mash/internal/sap"
)

// The name rule (docs/name-rule.md) is applied at every entry point, before the directory is read. The point names
// the entry in the error the caller gets, which carries the rule's id and message.
const (
	pointStart           = "start"
	pointSendRecipient   = "send: the recipient"
	pointSendSender      = "send: the sender"
	pointReply           = "reply: the identity being answered"
	pointPromptRecipient = "prompt: the recipient"
	pointPromptSender    = "prompt: the sender"
	pointPromptAsker     = "prompt: the asker"
	pointRelayOrigin     = "mail: the declared origin of a relay"
	pointLookup          = "lookup"
)

// checkRule applies rule to a for the entry point and words the refusal. A nil rule accepts everything.
func checkRule(rule *namerule.Rule, point string, a sap.Address) error {
	if err := rule.Check(a); err != nil {
		return fmt.Errorf("mash: %s: %w", point, err)
	}
	return nil
}

// parseRecipient is the address a recipient argument names: the dotted owner.group.instance, or the flat key
// owner_group_instance that who and the directory use. Anything else is refused, naming the form to give. A bare
// instance is not completed from the sender's own owner or group.
func parseRecipient(arg string) (sap.Address, error) {
	var a sap.Address
	var err error
	if strings.Contains(arg, ".") {
		a, err = sap.ParseAddress(arg)
	} else {
		a, err = sap.AddressFromFlat(arg)
	}
	if err != nil {
		return sap.Address{}, fmt.Errorf("mash: %q is not an endpoint address: give the full address owner.group.name as who shows it (the flat owner_group_name is accepted too)", arg)
	}
	return a, nil
}

// checkKey applies the rule to the address the directory key (or recipient argument) names; an argument that names
// none passes here and is refused by the code that follows, on its own terms.
func (e *Endpoint) checkKey(point, key string) error {
	if e.cfg.NameRule == nil {
		return nil
	}
	if a, err := parseRecipient(key); err == nil {
		return checkRule(e.cfg.NameRule, point, a)
	}
	return nil
}

// checkSelf applies the rule to this endpoint's own identity.
func (e *Endpoint) checkSelf(point string) error {
	if e.cfg.NameRule == nil || e.ident.Validate() != nil {
		return nil
	}
	return checkRule(e.cfg.NameRule, point, e.ident)
}

// DiscoverRecipient is the package's DiscoverRecipient for a prompt this endpoint sends: its own identity and the
// addressed one are checked against the name rule first, so a refusal comes back before anything is asked of the
// network.
func (e *Endpoint) DiscoverRecipient(ctx context.Context, arg string) (string, error) {
	arg = sap.KeyOf(arg)
	if err := e.checkSelf(pointPromptSender); err != nil {
		return "", err
	}
	if err := e.checkKey(pointPromptRecipient, arg); err != nil {
		return "", err
	}
	if h := s5Hook(); h != nil && h.promptDirectory {
		return e.discoverRecipientS5(ctx, h, arg)
	}
	return DiscoverRecipient(ctx, e.nc, arg)
}

// viaAllowed applies the rule to the origin named by a verified relay declaration ("<origin> #<seq>"). Mail from a
// refused origin is still delivered (the sender is judged on its own identity), but the declaration is not honoured:
// via comes back empty, so the recipient is never shown "relayed from" a name the rule refuses. The error says why.
func (e *Endpoint) viaAllowed(via string) (string, error) {
	if via == "" || e.cfg.NameRule == nil {
		return via, nil
	}
	origin, _, _ := strings.Cut(via, " #")
	if err := e.checkKey(pointRelayOrigin, origin); err != nil {
		return "", err
	}
	return via, nil
}
