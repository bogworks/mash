package listset

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// RawClientLimit is the sentence every answer of Show and Explain carries (REQ-010): the lists do not close the
// reply and plumbing subjects.
const RawClientLimit = "The reply and plumbing subjects stay open whatever the lists say: a raw client can send data to another address on them. The lists govern who may mail, prompt, probe and watch whom, not who may communicate at all."

// DescribesListSet is the Describes value of an answer about the list set. A sender's installed credential, which
// would be "installed_credential", comes later; every answer says which of the two it describes.
const DescribesListSet = "list_set"

// Reason is why a verb is allowed or refused for a sender and a receiver.
type Reason string

// The reasons, in order of precedence when more than one holds: deny (on a shared verb), allow, owner line, no entry.
const (
	// ReasonOwnerLine: the receiver is in the sender's owner group and the verb is shared there.
	ReasonOwnerLine Reason = "owner_line"
	// ReasonAllowTag: the receiver is on the sender's allow list.
	ReasonAllowTag Reason = "allow_tag"
	// ReasonDenyTag: the receiver is on the sender's deny list on a shared verb, which beats the owner line.
	// Deny lines exist only for shared verbs, so a closed verb reports ReasonNoEntry instead.
	ReasonDenyTag Reason = "deny_tag"
	// ReasonNoEntry: nothing grants it. The verb is closed in the group, or the receiver is outside the group, and
	// there is no allow entry.
	ReasonNoEntry Reason = "no_entry"
)

// View is the answer of Show: the list set in force. It is plain data; json.Marshal of equal inputs, given in any
// order, yields identical bytes, and every slice is non-nil.
//
// Whether the list set is applied comes with the apply path and is not part of View yet.
type View struct {
	// Describes is DescribesListSet.
	Describes string `json:"describes"`
	// Generation is the generation of the list set in force, as the caller read it from the generation record.
	Generation uint64 `json:"generation"`
	// Range is the account's range.
	Range string `json:"range"`
	// Switches is every group and verb switch, sorted by group, then verb in Verbs order.
	Switches []SwitchView `json:"switches"`
	// Senders is one entry per sender, sorted by sender.
	Senders []SenderView `json:"senders"`
	// RawClientLimit is RawClientLimit.
	RawClientLimit string `json:"raw_client_limit"`
}

// SwitchView is one owner group's setting of one verb.
type SwitchView struct {
	Group  string `json:"group"`
	Verb   Verb   `json:"verb"`
	Switch Switch `json:"switch"`
}

// SenderView is one sender's lists, its effective reach, and the subjects open to it whatever the lists say.
type SenderView struct {
	Sender string `json:"sender"`
	// Allow and Deny are the sender's lists, sorted.
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
	// Reach has one entry per verb in Verbs order: what the compiled credential grants over the addresses the set
	// names.
	Reach []VerbReach `json:"reach"`
	// OpenSubjects are the sender's subjects that hold whatever the lists say.
	OpenSubjects OpenSubjects `json:"open_subjects"`
}

// VerbReach is the receivers a sender reaches with one verb, sorted.
type VerbReach struct {
	Verb      Verb     `json:"verb"`
	Receivers []string `json:"receivers"`
}

// OpenSubjects are the non-agents. lines of a sender's expanded template: replies, the service API, JetStream and KV
// plumbing, and its own inbox prefix. Both lists are sorted. A raw client can use them to reach another address
// (see RawClientLimit).
type OpenSubjects struct {
	Publish   []string `json:"publish"`
	Subscribe []string `json:"subscribe"`
}

// Explanation is the answer of Explain: per verb, whether a sender may reach a receiver and why. Every slice is
// non-nil.
type Explanation struct {
	// Describes is DescribesListSet.
	Describes string `json:"describes"`
	Sender    string `json:"sender"`
	Receiver  string `json:"receiver"`
	// Verbs has one entry per verb in Verbs order.
	Verbs []VerbExplanation `json:"verbs"`
	// OpenSubjects are the sender's subjects that hold whatever the lists say.
	OpenSubjects OpenSubjects `json:"open_subjects"`
	// RawClientLimit is RawClientLimit.
	RawClientLimit string `json:"raw_client_limit"`
}

// VerbExplanation is the answer for one verb. Detail is one plain sentence.
type VerbExplanation struct {
	Verb    Verb   `json:"verb"`
	Allowed bool   `json:"allowed"`
	Reason  Reason `json:"reason"`
	Detail  string `json:"detail"`
}

// Show describes the list set ls in force (REQ-010): its switches, and for each sender its lists, its effective
// reach and its open subjects. c is the compiled output of ls. generation is the generation of the list set in
// force; the generation record lives elsewhere, so the caller passes it in. Show is pure.
//
// A ListSet that fails Validate returns that error. Reach is computed from c, the credential's expansion, not from
// the lists, so a c that does not match ls shows what c would grant.
func Show(ls ListSet, c *Compiled, generation uint64) (View, error) {
	m, err := validate(ls)
	if err != nil {
		return View{}, err
	}
	if c == nil {
		return View{}, errors.New("no compiled list set")
	}
	v := View{
		Describes:      DescribesListSet,
		Generation:     generation,
		Range:          m.rng,
		Switches:       []SwitchView{},
		Senders:        []SenderView{},
		RawClientLimit: RawClientLimit,
	}
	groups := make([]string, 0, len(m.switches))
	for g := range m.switches {
		groups = append(groups, g)
	}
	slices.Sort(groups)
	for _, g := range groups {
		for _, verb := range Verbs {
			if sw, ok := m.switches[g][verb]; ok {
				v.Switches = append(v.Switches, SwitchView{Group: g, Verb: verb, Switch: sw})
			}
		}
	}
	named := universeNamed(m)
	for _, s := range m.senders {
		who := s.addr.String()
		reach, err := reachOver(c, who, named)
		if err != nil {
			return View{}, err
		}
		open, err := openSubjects(c, who)
		if err != nil {
			return View{}, err
		}
		sv := SenderView{
			Sender:       who,
			Allow:        addrStrings(s.allow),
			Deny:         addrStrings(s.deny),
			Reach:        []VerbReach{},
			OpenSubjects: open,
		}
		for _, verb := range Verbs {
			sv.Reach = append(sv.Reach, VerbReach{Verb: verb, Receivers: nonNil(reach[verb])})
		}
		v.Senders = append(v.Senders, sv)
	}
	return v, nil
}

// Explain says, for sender and receiver, per verb, whether the list set ls allows it and why (REQ-010). The reason
// is derived from the lists (deny beats allow, allow beats the owner line, then no entry) and the answer from the
// compiled credential c, expanded the way the server does; if the two disagree, c does not match ls and Explain
// returns an error. Explain is pure.
//
// It returns an error for a ListSet that fails Validate, a sender the set does not list, and a receiver that is not
// a valid three-part address in the set's range. The receiver need not be named in the set.
func Explain(ls ListSet, c *Compiled, sender, receiver string) (Explanation, error) {
	m, err := validate(ls)
	if err != nil {
		return Explanation{}, err
	}
	if c == nil {
		return Explanation{}, errors.New("no compiled list set")
	}
	sa, reason := parseAddress(sender)
	if reason != "" {
		return Explanation{}, fmt.Errorf("sender %q: %s", sender, reason)
	}
	idx := -1
	for i := range m.senders {
		if m.senders[i].addr == sa {
			idx = i
		}
	}
	if idx < 0 {
		return Explanation{}, fmt.Errorf("sender %q is not in the list set", sender)
	}
	s := m.senders[idx]
	ra, reason := parseAddress(receiver)
	if reason != "" {
		return Explanation{}, fmt.Errorf("receiver %q: %s", receiver, reason)
	}
	if ra.rng != m.rng {
		return Explanation{}, fmt.Errorf("receiver %q: range part %q is not the list set's range %q", receiver, ra.rng, m.rng)
	}
	who, to := sa.String(), ra.String()
	reach, err := reachOver(c, who, []string{to})
	if err != nil {
		return Explanation{}, err
	}
	open, err := openSubjects(c, who)
	if err != nil {
		return Explanation{}, err
	}
	e := Explanation{
		Describes:      DescribesListSet,
		Sender:         who,
		Receiver:       to,
		Verbs:          []VerbExplanation{},
		OpenSubjects:   open,
		RawClientLimit: RawClientLimit,
	}
	group := sa.group()
	inGroup := ra.group() == group
	for _, verb := range Verbs {
		var ve VerbExplanation
		switch {
		case slices.Contains(s.deny, ra) && m.shared(group, verb):
			ve = VerbExplanation{verb, false, ReasonDenyTag, fmt.Sprintf("%q is on the deny list", to)}
		case slices.Contains(s.allow, ra):
			ve = VerbExplanation{verb, true, ReasonAllowTag, fmt.Sprintf("%q is on the allow list", to)}
		case inGroup && m.shared(group, verb):
			ve = VerbExplanation{verb, true, ReasonOwnerLine, fmt.Sprintf("owner group %q shares %s", group, verb)}
		case inGroup:
			ve = VerbExplanation{verb, false, ReasonNoEntry, fmt.Sprintf("owner group %q keeps %s closed and %q is on no list", group, verb, to)}
		default:
			ve = VerbExplanation{verb, false, ReasonNoEntry, fmt.Sprintf("%q is outside owner group %q and on no list", to, group)}
		}
		if got := slices.Contains(reach[verb], to); got != ve.Allowed {
			return Explanation{}, fmt.Errorf("compiled credential of %s does not match the lists: %s to %s is %s by the lists (%s) but %s by the credential",
				who, verb, to, allowedWord(ve.Allowed), ve.Reason, allowedWord(got))
		}
		e.Verbs = append(e.Verbs, ve)
	}
	return e, nil
}

func allowedWord(b bool) string {
	if b {
		return "allowed"
	}
	return "refused"
}

// openSubjects are the non-agents. lines of sender's expanded template, sorted and without repeats.
func openSubjects(c *Compiled, sender string) (OpenSubjects, error) {
	p, err := expandSender(c, sender)
	if err != nil {
		return OpenSubjects{}, err
	}
	pick := func(lines []string) []string {
		out := []string{}
		for _, l := range lines {
			if !strings.HasPrefix(l, "agents.") {
				out = append(out, l)
			}
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	return OpenSubjects{Publish: pick(p.pubAllow), Subscribe: pick(p.subAllow)}, nil
}

func addrStrings(as []address) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.String()
	}
	return out
}
