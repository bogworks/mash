package sap

import (
	"fmt"
	"strings"

	agents "github.com/bogworks/synadia-agents.go"
)

// Address is an endpoint's address: SAP's owner, group and instance tokens.
// Subjects are agents.<verb>.<owner>.<group>.<instance> and the flat key is
// <owner>_<group>_<instance>.
//
// The fields are exported for reading. Construct a value with NewAddress,
// AddressFromFlat, AddressFromSubject or ParseAddress; a struct literal, a
// mutated field or the zero value bypasses their checks, so Validate is the
// check for any value that did not come from them. The zero Address is not
// valid. There is no default group.
type Address struct {
	Owner, Group, Instance string
}

// addressVerbs are the verbs of an address's subjects.
var addressVerbs = map[string]bool{"prompt": true, "status": true, "hb": true, "mail": true}

func checkAddressTotal(owner, group, instance string) error {
	if n := len(owner) + len(group) + len(instance) + 2; n > MaxIdentityLen {
		return fmt.Errorf("sap: address %s.%s.%s is %d characters with separators: at most %d", owner, group, instance, n, MaxIdentityLen)
	}
	return nil
}

// NewAddress returns the address of owner, group and instance. Each token must
// satisfy the token rule (lower-case letters, digits and single dashes,
// starting with a letter, not ending with a dash, 1 to 32 characters), and the
// three tokens with their two separators must total at most 64 characters.
func NewAddress(owner, group, instance string) (Address, error) {
	for _, p := range []struct{ pos, tok string }{{"owner", owner}, {"group", group}, {"instance", instance}} {
		if err := checkToken(p.pos, p.tok); err != nil {
			return Address{}, err
		}
	}
	if err := checkAddressTotal(owner, group, instance); err != nil {
		return Address{}, err
	}
	return Address{Owner: owner, Group: group, Instance: instance}, nil
}

// Validate re-applies the construction rules to a. The zero value is invalid.
func (a Address) Validate() error {
	_, err := NewAddress(a.Owner, a.Group, a.Instance)
	return err
}

// NoSubjectError is the failure of Subjects: the SDK's subject builder refused a's tokens. Its text is mash's own
// words; the SDK's error is kept for Unwrap and is never in the text.
type NoSubjectError struct {
	Address string // owner.group.instance, as given
	err     error
}

// Error is "no subject can be built for owner.group.instance".
func (e *NoSubjectError) Error() string { return "no subject can be built for " + e.Address }

// Unwrap returns the SDK's error, for debugging.
func (e *NoSubjectError) Unwrap() error { return e.err }

// Subjects returns the four SAP subjects of a. The prompt, status and heartbeat subjects come from the SDK's subject
// builder, which refuses a token it cannot put in a subject: then Subjects returns the zero Subjects and a
// *NoSubjectError, and no subject is guessed. Use MailSubject for the mail subject alone: it cannot fail.
func (a Address) Subjects() (Subjects, error) {
	// The SDK reads an empty subject token as "use the agent name", which would build another endpoint's subject, so the
	// owner token is checked by the SDK's own token check first.
	err := agents.ValidateToken(agents.RoleSubjectToken, a.Owner)
	var s agents.AgentSubject
	if err == nil {
		s, err = agents.NewAgentSubject(AgentName, a.Group, a.Instance, agents.WithSubjectToken(a.Owner))
	}
	if err != nil {
		return Subjects{}, &NoSubjectError{Address: a.String(), err: err}
	}
	return Subjects{Prompt: s.Prompt(), Status: s.Status(), HB: s.Heartbeat(), Mail: a.MailSubject()}, nil
}

// MailSubject is a's mail subject, agents.mail.<owner>.<group>.<instance>. The SDK has no mail verb, so mash builds it;
// it cannot fail.
func (a Address) MailSubject() string {
	return "agents.mail." + a.Owner + "." + a.Group + "." + a.Instance
}

// MailFilter is the subject filter of a's inbox stream: exactly a's mail
// subject, with no wildcard.
func (a Address) MailFilter() string { return a.MailSubject() }

// Flat is the flat key of a: owner_group_instance. It is injective because
// tokens contain no underscore.
func (a Address) Flat() string { return a.Owner + "_" + a.Group + "_" + a.Instance }

// String is the dotted form of a: owner.group.instance.
func (a Address) String() string { return a.Owner + "." + a.Group + "." + a.Instance }

// AddressFromFlat is the inverse of Flat: exactly three underscore-separated
// tokens, each valid, within the total bound.
func AddressFromFlat(flat string) (Address, error) {
	p := strings.Split(flat, "_")
	if len(p) != 3 {
		return Address{}, fmt.Errorf("sap: flat %q has %d underscore-separated parts: want exactly three (owner_group_name)", flat, len(p))
	}
	return NewAddress(p[0], p[1], p[2])
}

// ParseAddress parses the dotted form owner.group.instance: exactly three
// tokens, each valid, within the total bound.
func ParseAddress(s string) (Address, error) {
	p := strings.Split(s, ".")
	if len(p) != 3 {
		return Address{}, fmt.Errorf("sap: address %q has %d dot-separated parts: want exactly three (owner.group.name)", s, len(p))
	}
	return NewAddress(p[0], p[1], p[2])
}

// AddressFromSubject returns the address and verb of a five-token
// agents.<verb>.<owner>.<group>.<instance> subject. It reports false if the
// shape is wrong, the verb is not prompt, status, hb or mail, or a token fails
// the token rule (so a wildcard token is never accepted).
func AddressFromSubject(subj string) (Address, string, bool) {
	t := strings.Split(subj, ".")
	if len(t) != 5 || t[0] != "agents" || !addressVerbs[t[1]] {
		return Address{}, "", false
	}
	a, err := NewAddress(t[2], t[3], t[4])
	if err != nil {
		return Address{}, "", false
	}
	return a, t[1], true
}

// Limits of the token rule.
const (
	// MaxTokenLen is the longest owner, group or instance token.
	MaxTokenLen = 32
	// MaxIdentityLen is the longest dotted form: three tokens and two separators.
	MaxIdentityLen = 64
)

// checkToken applies the new token rule to one token at position pos
// ("owner", "group" or "instance"): lower-case ASCII letters, digits and single
// dashes, starting with a letter, not ending with a dash, 1 to 32 characters.
func checkToken(pos, t string) error {
	if t == "" {
		return fmt.Errorf("sap: %s token is empty: use 1-%d characters", pos, MaxTokenLen)
	}
	if len(t) > MaxTokenLen {
		return fmt.Errorf("sap: %s token %q is too long: at most %d characters", pos, t, MaxTokenLen)
	}
	for i := 0; i < len(t); i++ {
		c := t[i]
		switch {
		case c >= 'A' && c <= 'Z':
			return fmt.Errorf("sap: %s token %q must be lower-case", pos, t)
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
		default:
			return fmt.Errorf("sap: %s token %q has a character that is not a lower-case letter, digit or dash", pos, t)
		}
	}
	if t[0] < 'a' || t[0] > 'z' {
		return fmt.Errorf("sap: %s token %q must start with a letter", pos, t)
	}
	if t[len(t)-1] == '-' {
		return fmt.Errorf("sap: %s token %q must not end with a dash", pos, t)
	}
	if strings.Contains(t, "--") {
		return fmt.Errorf("sap: %s token %q must not have consecutive dashes", pos, t)
	}
	return nil
}
