package admin

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/sap"
)

// ValidateGrant checks one subject grant against REQ-027: a grant that applies to the agents. subject space must not
// carry '*' or '>' in the range (agent) token position, which is token index 2 of agents.<verb>.<agent>.<owner>.<name>,
// or at or before it. A grant is accepted only if every token is a literal or a lone '*', '>' appears only as the
// last token, no token is empty or has whitespace, and no token mixes a wildcard character with other text.
// A grant that starts with a wildcard applies to the agents. space too, so it is held to the same rule. An agents.
// grant needs at least four tokens (verb, agent and owner are named). Subjects outside the agents. space pass the
// structural checks only.
func ValidateGrant(subject string) error {
	const rule = "REQ-027: a grant must not carry '*' or '>' in the agent token position (token 3) or before it"
	if subject == "" || strings.ContainsAny(subject, " \t\r\n") {
		return fmt.Errorf("grant %q refused (%s): empty or contains whitespace", subject, rule)
	}
	toks := strings.Split(subject, ".")
	for i, t := range toks {
		switch {
		case t == "":
			return fmt.Errorf("grant %q refused (%s): empty token", subject, rule)
		case t == ">" && i != len(toks)-1:
			return fmt.Errorf("grant %q refused (%s): '>' only as the last token", subject, rule)
		case t != "*" && t != ">" && strings.ContainsAny(t, "*>"):
			return fmt.Errorf("grant %q refused (%s): partial wildcard token %q", subject, rule, t)
		}
	}
	if toks[0] != "agents" && toks[0] != "*" && toks[0] != ">" {
		return nil
	}
	for i, t := range toks {
		if i > 2 {
			break
		}
		if t == "*" || t == ">" {
			return fmt.Errorf("grant %q refused (%s)", subject, rule)
		}
	}
	if len(toks) < 4 {
		return fmt.Errorf("grant %q refused (%s): an agents. grant needs at least verb, agent and owner tokens before any wildcard", subject, rule)
	}
	return nil
}

// RefuseCompiledForNewForm is the policy-compiled-user refusal of amendment 2: ADR 0053's compiler uses the zone as
// its wildcard unit, so a policy-compiled user cannot carry a three-token identity until Randy rules on the zone
// question. It returns an error when compiled is true.
func RefuseCompiledForNewForm(compiled bool) error {
	if compiled {
		return errors.New("a three-token identity cannot take a policy-compiled user until the zone ruling")
	}
	return nil
}

// NewFormPermissions is the DEFAULT, un-policied permission set of the new-form endpoint id, plus the validated extra
// grants. It takes no policy input on purpose: new-form provisioning (step 1.6) MUST use it for every three-token
// identity and MUST refuse any policy-compiled permission set (RefuseCompiledForNewForm), because the compiler's zone
// wildcard unit is unruled (amendment 2). Anything not listed is denied; there is no response permission.
//
// Publish: peer lines to any endpoint of the same owner with the same literal agent token (mail, prompt, status; no
// wildcard in the agent position), its own exact heartbeat, replies, the service API, and fixedPub's JetStream and KV
// lines keyed on the flat identity. Subscribe: its private inbox, its own prompt and status subjects, $SRV.>, and the
// heartbeats of its own owner and agent token.
//
// extra are configured grants (REQ-027 validated). Each must start with "agents." and carry the verb mail, prompt or
// status (never hb or any other verb: heartbeat publication is granted only on the endpoint's own exact subjects);
// it is added to publish allow only.
func NewFormPermissions(id sap.EndpointIdentity, extra []string) (jwt.Permissions, error) {
	var p jwt.Permissions
	if err := id.Validate(); err != nil {
		return p, fmt.Errorf("mash: new-form permissions: %w", err)
	}
	if id.IsLegacy() {
		return p, fmt.Errorf("mash: new-form permissions: %s is a legacy identity: use Permissions", id)
	}
	for _, g := range extra {
		if err := ValidateGrant(g); err != nil {
			return jwt.Permissions{}, err
		}
		if !strings.HasPrefix(g, "agents.") {
			return jwt.Permissions{}, fmt.Errorf("mash: new-form permissions: grant %q is outside the agents. subject space", g)
		}
		// ValidateGrant guarantees at least four tokens, so token 1 is the verb.
		if verb := strings.Split(g, ".")[1]; verb != "mail" && verb != "prompt" && verb != "status" {
			return jwt.Permissions{}, fmt.Errorf("mash: new-form permissions: grant %q refused: only the mail, prompt and status verbs may be extra grants; heartbeat publication is granted only on the endpoint's own exact subjects", g)
		}
	}
	s := id.Subjects()
	peer := id.Agent + "." + id.Owner
	var pub []string
	for _, verb := range []string{"mail", "prompt", "status"} {
		pub = append(pub, "agents."+verb+"."+peer+".*")
	}
	pub = append(pub, fixedPubKeyed(s.HB, id.Flat())...)
	pub = append(pub, extra...)
	sub := []string{
		sap.InboxPrefix(id.Flat()) + ".>",
		s.Prompt,
		s.Status,
		"$SRV.>",
		"agents.hb." + peer + ".*",
	}
	p.Pub.Allow.Add(dedupe(pub)...)
	p.Sub.Allow.Add(dedupe(sub)...)
	return p, nil
}

// WindowBridge is what the window adds for new-form id that has legacy alias (amendment 1, removed in 3.2): publish to
// legacy endpoints of the alias's owner for mail, prompt and status only (wildcard in the name position only, literal
// agent token cc), the heartbeat of the alias's own exact subject and no other, and exact subscribe grants for the
// alias's prompt and status subjects. It refuses an id that shares the alias's owner (admission's rule: the legacy
// wildcard filter would reach it) or overlaps it.
//
// WindowBridge trusts alias. The grants name another endpoint's exact prompt and status subjects and its heartbeat,
// so the caller (provisioning, step 1.6) MUST verify the alias mapping first, and no check here can: the alias's
// legacy owner and name pass the legacy rule, a legacy record with exactly that owner and name exists, and no other
// new-form record claims it (one alias per record and per legacy identity).
func WindowBridge(id, alias sap.EndpointIdentity) (jwt.Permissions, error) {
	var p jwt.Permissions
	if err := id.Validate(); err != nil {
		return p, fmt.Errorf("mash: window bridge: %w", err)
	}
	if id.IsLegacy() {
		return p, fmt.Errorf("mash: window bridge: %s must be a new-form identity", id)
	}
	if err := alias.Validate(); err != nil {
		return p, fmt.Errorf("mash: window bridge: alias: %w", err)
	}
	if !alias.IsLegacy() {
		return p, fmt.Errorf("mash: window bridge: alias %s must be a legacy identity", alias)
	}
	if id.Owner == alias.Owner {
		return p, fmt.Errorf("mash: window bridge refused: owner %q equals the alias owner (admission rule: the legacy wildcard filter would reach %s)", id.Owner, id)
	}
	if sap.Overlaps(id, alias) {
		return p, fmt.Errorf("mash: window bridge refused: %s overlaps alias %s (admission overlap rule)", id, alias)
	}
	as := alias.Subjects()
	for _, verb := range []string{"mail", "prompt", "status"} {
		p.Pub.Allow.Add("agents." + verb + "." + sap.AgentToken + "." + alias.Owner + ".*")
	}
	p.Pub.Allow.Add(as.HB)
	p.Sub.Allow.Add(as.Prompt, as.Status)
	return p, nil
}

func dedupe(in []string) []string {
	var out []string
	for _, s := range in {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
