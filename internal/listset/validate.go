package listset

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/bogworks/mash/internal/sap"
)

// address is a parsed three-part address, "prefix.owner.name": the endpoint address owner.group.instance, with the
// account's prefix as the owner token.
type address struct{ prefix, owner, name string }

func (a address) String() string { return a.prefix + "." + a.owner + "." + a.name }

// group is "prefix.owner".
func (a address) group() string { return a.prefix + "." + a.owner }

// tail is "owner.name", the tag value form of an address inside the account's prefix.
func (a address) tail() string { return a.owner + "." + a.name }

// parseAddress parses a three-part address with mash's naming grammar (sap.ParseAddress). The grammar
// refuses every character a wildcard needs, but the reason names the wildcard so the caller sees REQ-003 at work.
func parseAddress(s string) (address, string) {
	if strings.ContainsAny(s, "*>") {
		return address{}, "wildcards are refused: an entry names one exact address"
	}
	id, err := sap.ParseAddress(s)
	if err != nil {
		return address{}, strings.TrimPrefix(err.Error(), "sap: ")
	}
	return address{id.Owner, id.Group, id.Instance}, ""
}

// parseGroup parses "prefix.owner". A three-part value is a switch for a single sender.
func parseGroup(s string) (prefix, owner, reason string) {
	if strings.ContainsAny(s, "*>") {
		return "", "", "wildcards are refused: a switch names one exact owner group"
	}
	switch parts := strings.Split(s, "."); len(parts) {
	case 2:
		// sap.NewAddress is the grammar; "a" is a name that always passes it.
		if _, err := sap.NewAddress(parts[0], parts[1], "a"); err != nil {
			return "", "", strings.TrimPrefix(err.Error(), "sap: ")
		}
		return parts[0], parts[1], ""
	case 3:
		return "", "", "a switch is set for an owner group, never for a single sender"
	default:
		return "", "", "a group is two parts joined by a dot"
	}
}

// model is a validated list set in a form the compiler and the checker share.
type model struct {
	prefix   string
	switches map[string]map[Verb]Switch // group -> verb -> switch
	senders  []sender                   // sorted by address
}

type sender struct {
	addr        address
	allow, deny []address // sorted, no duplicates
}

func (m *model) shared(group string, v Verb) bool { return m.switches[group][v] == Shared }

// anyShared reports whether group has a shared verb.
func (m *model) anyShared(group string) bool {
	for _, v := range Verbs {
		if m.shared(group, v) {
			return true
		}
	}
	return false
}

// Validate refuses a list set holding any problem and names every one (REQ-001 to REQ-003, REQ-006's deny rule). It
// is pure. The error is a *ValidationError.
func Validate(ls ListSet) error {
	_, err := validate(ls)
	return err
}

func validate(ls ListSet) (*model, error) {
	var ps []Problem
	bad := func(entry, format string, a ...any) { ps = append(ps, Problem{entry, fmt.Sprintf(format, a...)}) }

	if _, err := sap.NewAddress(ls.Prefix, "a", "a"); err != nil {
		bad("address prefix "+quote(ls.Prefix), "%s", strings.TrimPrefix(err.Error(), "sap: "))
	}
	m := &model{prefix: ls.Prefix, switches: map[string]map[Verb]Switch{}}

	for _, e := range ls.Switches {
		entry := fmt.Sprintf("switch %s %s", quote(e.Group), quote(string(e.Verb)))
		prefix, owner, reason := parseGroup(e.Group)
		ok := reason == ""
		if !ok {
			bad(entry, "%s", reason)
		} else if prefix != ls.Prefix {
			bad(entry, "address prefix %q is not the account's address prefix %q", prefix, ls.Prefix)
			ok = false
		}
		if !e.Verb.valid() {
			bad(entry, "unknown verb: use mail, prompt, status or heartbeat")
			ok = false
		}
		if e.Switch != Shared && e.Switch != Closed {
			bad(entry, "unknown switch %q: use shared or closed", string(e.Switch))
			ok = false
		}
		if !ok {
			continue
		}
		g := prefix + "." + owner
		if _, dup := m.switches[g][e.Verb]; dup {
			bad(entry, "stated twice; a group and verb take one value, even the same one")
			continue
		}
		if m.switches[g] == nil {
			m.switches[g] = map[Verb]Switch{}
		}
		m.switches[g][e.Verb] = e.Switch
	}

	seen := map[string]bool{}
	groupsWithSender := map[string]bool{}
	for _, sl := range ls.Senders {
		addr, reason := parseAddress(sl.Sender)
		if reason != "" {
			bad("sender "+quote(sl.Sender), "%s", reason)
			// The entries of a sender that cannot be named are still checked for shape.
			for _, kind := range []struct {
				name string
				list []string
			}{{"allow", sl.Allow}, {"deny", sl.Deny}} {
				for _, s := range kind.list {
					if _, r := parseAddress(s); r != "" {
						bad(kind.name+" of "+quote(sl.Sender)+": "+quote(s), "%s", r)
					}
				}
			}
			continue
		}
		if addr.prefix != ls.Prefix {
			bad("sender "+quote(sl.Sender), "address prefix %q is not the account's address prefix %q", addr.prefix, ls.Prefix)
			continue
		}
		if seen[addr.String()] {
			bad("sender "+quote(sl.Sender), "listed twice")
			continue
		}
		seen[addr.String()] = true
		groupsWithSender[addr.group()] = true
		s := sender{addr: addr}
		s.allow = parseList(sl.Allow, "allow", addr, ls.Prefix, bad)
		s.deny = parseList(sl.Deny, "deny", addr, ls.Prefix, bad)
		for _, d := range s.deny {
			entry := "deny of " + quote(sl.Sender) + ": " + quote(d.String())
			switch {
			case slices.Contains(s.allow, d):
				bad(entry, "also on the allow list; an address is allowed or denied, not both")
			case d.group() != addr.group():
				bad(entry, "outside the sender's owner group %s; a deny takes an address away from a shared verb, and only the owner line is shared", addr.group())
			case len(m.switches[addr.group()]) != len(Verbs):
				// The missing verbs are reported below; no second problem for the same fault.
			case !m.anyShared(addr.group()):
				bad(entry, "owner group %s has no shared verb, so the deny can only be a mistake (a closed verb reaches only the allow list)", addr.group())
			}
		}
		m.senders = append(m.senders, s)
	}

	groups := make([]string, 0, len(groupsWithSender))
	for g := range groupsWithSender {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	for _, g := range groups {
		for _, v := range Verbs {
			if _, ok := m.switches[g][v]; !ok {
				bad("group "+quote(g)+" "+quote(string(v)), "no switch; every verb is stated for every owner group with a sender, and mash infers none from another")
			}
		}
	}

	if len(ps) > 0 {
		return nil, &ValidationError{Problems: ps}
	}
	sort.Slice(m.senders, func(i, j int) bool { return m.senders[i].addr.String() < m.senders[j].addr.String() })
	return m, nil
}

// parseList parses one allow or deny list, refusing a bad address, another prefix and a repeat.
func parseList(in []string, kind string, owner address, prefix string, bad func(string, string, ...any)) []address {
	var out []address
	for _, s := range in {
		entry := kind + " of " + quote(owner.String()) + ": " + quote(s)
		a, reason := parseAddress(s)
		switch {
		case reason != "":
			bad(entry, "%s", reason)
		case a.prefix != prefix:
			bad(entry, "address prefix %q is not the account's address prefix %q", a.prefix, prefix)
		case slices.Contains(out, a):
			bad(entry, "listed twice")
		default:
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func quote(s string) string { return fmt.Sprintf("%q", s) }
