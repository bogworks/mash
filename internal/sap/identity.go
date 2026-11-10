package sap

import (
	"fmt"
	"regexp"
	"strings"
)

// Limits of the new token rule.
const (
	// MaxTokenLen is the longest agent, owner or name token of a new-form identity.
	MaxTokenLen = 32
	// MaxIdentityLen is the longest canonical form: three tokens and two separators.
	MaxIdentityLen = 64
)

// legacyNameRE and maxLegacyNameLen duplicate config.ValidName, which imports
// this package; a test in package sap_test pins the two together.
var legacyNameRE = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

const maxLegacyNameLen = 48

// EndpointIdentity is an endpoint's identity (not the sender identity of Identity) as SAP's agent, owner and name tokens.
// Agent is the SAP agent token, distinct from the directory record's agent
// field that names the harness.
//
// The fields are exported for reading and display only. A value must come from
// New, Legacy or FromFlat; a struct literal, the zero value or a mutated field
// bypasses their checks, so Validate is the check for any value that did not.
// The zero EndpointIdentity is not valid.
type EndpointIdentity struct {
	Agent, Owner, Name string
	legacy             bool
}

// checkToken applies the new token rule to one token at position pos
// ("agent", "owner" or "name"): lower-case ASCII letters, digits and single
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

func checkTotal(agent, owner, name string) error {
	if n := len(agent) + len(owner) + len(name) + 2; n > MaxIdentityLen {
		return fmt.Errorf("sap: identity %s.%s.%s is %d characters with separators: at most %d (62 token characters)", agent, owner, name, n, MaxIdentityLen)
	}
	return nil
}

// New returns a new-form identity of SAP's agent, owner and name tokens. Each
// token must satisfy the new token rule, and the three tokens with their two
// separators must total at most 64 characters. New does not know any list of
// harness names and accepts agent "cc"; that is a range-level rule.
func New(agent, owner, name string) (EndpointIdentity, error) {
	for _, p := range []struct{ pos, tok string }{{"agent", agent}, {"owner", owner}, {"name", name}} {
		if err := checkToken(p.pos, p.tok); err != nil {
			return EndpointIdentity{}, err
		}
	}
	if err := checkTotal(agent, owner, name); err != nil {
		return EndpointIdentity{}, err
	}
	return EndpointIdentity{Agent: agent, Owner: owner, Name: name}, nil
}

func validLegacyName(s string) bool {
	return len(s) >= 1 && len(s) <= maxLegacyNameLen && legacyNameRE.MatchString(s)
}

// Legacy returns the legacy identity of name under owner: Agent is AgentToken
// and validity is exactly today's (the legacy name rule, 1-48 characters, for
// owner and name alike). The new token rule is never applied to it.
func Legacy(owner, name string) (EndpointIdentity, error) {
	if !validLegacyName(owner) {
		return EndpointIdentity{}, fmt.Errorf("sap: legacy owner %q: use 1-%d lower-case letters, digits and single hyphens, starting with a letter", owner, maxLegacyNameLen)
	}
	if !validLegacyName(name) {
		return EndpointIdentity{}, fmt.Errorf("sap: legacy name %q: use 1-%d lower-case letters, digits and single hyphens, starting with a letter", name, maxLegacyNameLen)
	}
	return EndpointIdentity{Agent: AgentToken, Owner: owner, Name: name, legacy: true}, nil
}

// Validate re-applies the construction rules to id, for a value that may not
// have come from New, Legacy or FromFlat. A legacy identity must have Agent
// equal to AgentToken and an owner and name that satisfy the legacy rule; a
// new-form identity must have three tokens that satisfy the new token rule
// within the total bound. The zero value is invalid.
func (id EndpointIdentity) Validate() error {
	if id.legacy {
		if id.Agent != AgentToken {
			return fmt.Errorf("sap: legacy identity has agent %q: want %q", id.Agent, AgentToken)
		}
		if !validLegacyName(id.Owner) {
			return fmt.Errorf("sap: legacy owner %q: use 1-%d lower-case letters, digits and single hyphens, starting with a letter", id.Owner, maxLegacyNameLen)
		}
		if !validLegacyName(id.Name) {
			return fmt.Errorf("sap: legacy name %q: use 1-%d lower-case letters, digits and single hyphens, starting with a letter", id.Name, maxLegacyNameLen)
		}
		return nil
	}
	for _, p := range []struct{ pos, tok string }{{"agent", id.Agent}, {"owner", id.Owner}, {"name", id.Name}} {
		if err := checkToken(p.pos, p.tok); err != nil {
			return err
		}
	}
	return checkTotal(id.Agent, id.Owner, id.Name)
}

// IsLegacy reports whether id was built by Legacy.
func (id EndpointIdentity) IsLegacy() bool { return id.legacy }

// Subjects returns the four SAP subjects of id. A legacy identity gets exactly
// For(owner, name); a new-form identity gets agents.<verb>.<agent>.<owner>.<name>.
func (id EndpointIdentity) Subjects() Subjects {
	if id.legacy {
		return For(id.Owner, id.Name)
	}
	s := func(verb string) string {
		return "agents." + verb + "." + id.Agent + "." + id.Owner + "." + id.Name
	}
	return Subjects{Prompt: s("prompt"), Status: s("status"), HB: s("hb"), Mail: s("mail")}
}

// MailFilter is the subject filter of id's inbox stream. A legacy identity gets
// exactly MailFilter(owner, name), with a wildcard agent token; a new-form
// identity gets its exact agent token, because the agent position is never
// wildcarded.
func (id EndpointIdentity) MailFilter() string {
	if id.legacy {
		return MailFilter(id.Owner, id.Name)
	}
	return "agents.mail." + id.Agent + "." + id.Owner + "." + id.Name
}

// Flat is the flat key of id. A new-form identity gives agent_owner_name, which
// is injective because tokens contain no underscore; a legacy identity gives
// the bare name, today's key, so legacy streams and keys do not move.
func (id EndpointIdentity) Flat() string {
	if id.legacy {
		return id.Name
	}
	return id.Agent + "_" + id.Owner + "_" + id.Name
}

// String is for display: the bare name for a legacy identity (its Flat form),
// and the canonical agent.owner.name for a new-form identity.
func (id EndpointIdentity) String() string {
	if id.legacy {
		return id.Name
	}
	return id.Agent + "." + id.Owner + "." + id.Name
}

// FromFlat is the inverse of Flat for a new-form identity: exactly three
// underscore-separated tokens, each valid under the new token rule, within the
// total bound. A bare legacy name is not a new-form flat and is an error.
func FromFlat(flat string) (EndpointIdentity, error) {
	if !strings.Contains(flat, "_") {
		return EndpointIdentity{}, fmt.Errorf("sap: flat %q has no underscore: a new-form flat is agent_owner_name", flat)
	}
	p := strings.Split(flat, "_")
	if len(p) != 3 {
		return EndpointIdentity{}, fmt.Errorf("sap: flat %q has %d underscore-separated parts: want exactly three", flat, len(p))
	}
	return New(p[0], p[1], p[2])
}
