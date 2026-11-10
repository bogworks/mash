package endpoint

import (
	"context"
	"fmt"

	"github.com/bogworks/mash/internal/config"
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
	pointLookup          = "lookup"
	pointAlias           = "the compatibility alias"
)

// checkRule applies rule to id for the entry point and words the refusal. A nil rule accepts everything.
func checkRule(rule *namerule.Rule, point string, id sap.EndpointIdentity) error {
	if err := rule.Check(id); err != nil {
		return fmt.Errorf("mash: %s: %w", point, err)
	}
	return nil
}

// keyIdentity is the identity a directory key (or a recipient argument) names, formed as mash resolves it: a bare
// legacy name is the legacy identity (cc, owner, name) under the asking endpoint's owner; the flat key of a
// three-token endpoint (agent_owner_name) is that identity. The dotted display form is not a recipient form and is
// not parsed. ok is false for anything else, which the code that follows refuses on its own terms.
func keyIdentity(owner, key string) (sap.EndpointIdentity, bool) {
	if config.ValidName(key) {
		id, err := sap.Legacy(owner, key)
		return id, err == nil
	}
	id, err := sap.FromFlat(key)
	return id, err == nil
}

// checkKey applies the rule to the identity the directory key names; a key that names none passes here.
func (e *Endpoint) checkKey(point, key string) error {
	if e.cfg.NameRule == nil {
		return nil
	}
	if id, ok := keyIdentity(e.cfg.Owner, key); ok {
		return checkRule(e.cfg.NameRule, point, id)
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
	if err := e.checkSelf(pointPromptSender); err != nil {
		return "", err
	}
	if err := e.checkKey(pointPromptRecipient, arg); err != nil {
		return "", err
	}
	return DiscoverRecipient(ctx, e.nc, e.cfg.Owner, arg)
}
