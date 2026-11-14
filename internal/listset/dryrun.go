package listset

import (
	"errors"
	"slices"
	"sort"
	"strings"
)

// Stages name where a Refusal came from.
const (
	// StageInForce is a problem with the list set in force, which stops the comparison.
	StageInForce = "in_force"
	// StageValidate is a refusal from Validate on the new list set.
	StageValidate = "validate"
	// StageCheck is a refusal from Check on the new list set's compiled output.
	StageCheck = "check"
	// StageSize is a refusal from SizeCheck.
	StageSize = "size"
)

// Template changes.
const (
	TemplateAdded   = "added"
	TemplateRemoved = "removed"
	TemplateChanged = "changed"
)

// Plan is what applying a new list set would do, computed without signing, pushing, revoking or writing anything
// (REQ-009, AT-008). It is plain data: json.Marshal of equal inputs, given in any order, yields identical bytes. Every
// slice is non-nil (an empty one marshals as []) and sorted by the key named on its field.
//
// When Refused is true the whole list set is refused (REQ-003, REQ-006, REQ-007) and nothing would change: Refusals
// holds every refusal, and all other slices are empty.
type Plan struct {
	// Refused is true when any refusal exists; nothing would change.
	Refused bool `json:"refused"`
	// Refusals holds every refusal from Validate, Check and SizeCheck on the new set, not only the first, sorted by
	// stage (validate, check, size; in_force first), then entry, then reason. If Validate fails, compile cannot run,
	// so only validate refusals are reported.
	Refusals []Refusal `json:"refusals"`
	// Receivers is, per sender and verb, the receivers added and removed, sorted by sender then verb (mail, prompt,
	// status, heartbeat). A sender that is new has everything added; one that leaves has everything removed. The
	// comparison is of effective reach, the addresses each credential would reach once its tags are expanded through
	// its template (Check's simulation), over the addresses either set names. A pair with no difference is absent.
	Receivers []ReceiverChange `json:"receivers"`
	// Templates is the owner-group templates added, removed or changed, sorted by group.
	Templates []TemplateChange `json:"templates"`
	// Tags is the senders of the new set whose tags are new or differ, sorted by sender. A sender that leaves has no
	// entry.
	Tags []TagChange `json:"tags"`
	// RotateAndRevoke is the senders whose tags narrowed (see Narrowed), sorted. Their old credential would keep the
	// removed reach, so REQ-014 requires a new credential and the old one revoked.
	RotateAndRevoke []string `json:"rotate_and_revoke"`
	// NewCredential is the senders whose tags changed but did not narrow, sorted: they need a new credential and
	// nothing is revoked.
	NewCredential []string `json:"new_credential"`
	// Revoke is the senders in force that are not in the new set, sorted: every key issued for them is revoked.
	Revoke []string `json:"revoke"`
	// FirstCredential is the senders new to the set, sorted: they need a first credential.
	FirstCredential []string `json:"first_credential"`
}

// Refusal is one refused entry, with the stage that refused it.
type Refusal struct {
	Stage  string `json:"stage"`
	Entry  string `json:"entry"`
	Reason string `json:"reason"`
}

// ReceiverChange is the receivers one sender gains and loses for one verb. Both lists are sorted.
type ReceiverChange struct {
	Sender  string   `json:"sender"`
	Verb    Verb     `json:"verb"`
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// TemplateChange is one owner group's template that is added, removed or changed.
//
// Applying a changed template on a scoped key disconnects every open connection under that key (AT-011, ADR 0097);
// the clients reconnect and are bound by the new template, with no re-issue needed. Disconnects is true for a changed
// template, and DisconnectedSenders lists the senders of the group that are in force and stay in the new set, sorted.
// A removed template's senders are all revoked and an added template has no connections, so neither disconnects
// anything beyond the revokes.
type TemplateChange struct {
	Group               string   `json:"group"`
	Change              string   `json:"change"`
	Disconnects         bool     `json:"disconnects"`
	DisconnectedSenders []string `json:"disconnected_senders"`
}

// TagChange is one sender's tags before and after, each sorted. Old is empty for a new sender.
type TagChange struct {
	Sender string   `json:"sender"`
	Old    []string `json:"old"`
	New    []string `json:"new"`
}

// Narrowed reports whether a sender's tags narrowed from old to next: an allow tag is removed, or a deny tag is added.
// The deny sentinel (NoDenyTag) is neither: it is not an exception, it only keeps a user of a deny key able to
// connect, so adding or removing it never narrows.
//
// Anything else that changes the tags is widened only (an allow tag added, or a deny tag removed, or the sentinel
// changed): the sender needs a new credential, and the old one keeps no reach the new set removes. A change that
// both narrows and widens counts as narrowed. A template-only change, such as a verb flip, changes no tags and so is
// neither: it is reported as the disconnect of its group.
func Narrowed(old, next []string) bool {
	for _, t := range old {
		if strings.HasPrefix(t, AllowTag+":") && !slices.Contains(next, t) {
			return true
		}
	}
	for _, t := range next {
		if strings.HasPrefix(t, DenyTag+":") && t != NoDenyTag && !slices.Contains(old, t) {
			return true
		}
	}
	return false
}

var stageRank = map[string]int{StageInForce: 0, StageValidate: 1, StageCheck: 2, StageSize: 3}

// DryRun compares next with the list set in force and reports what applying it would do (REQ-009). A nil inForce
// means nothing is in force: every sender is new. It is pure: it signs nothing with a real key, does no I/O and does
// not modify its arguments. servers are the limits SizeCheck uses; none means one default server.
//
// It collects every refusal of Validate, Check and SizeCheck on next. If any exists the Plan is Refused and carries
// no changes. A list set in force that does not itself compile is reported as an in_force refusal, because there
// is nothing sound to compare with.
func DryRun(inForce *ListSet, next ListSet, servers []ServerLimits) Plan {
	p := Plan{
		Refusals: []Refusal{}, Receivers: []ReceiverChange{}, Templates: []TemplateChange{}, Tags: []TagChange{},
		RotateAndRevoke: []string{}, NewCredential: []string{}, Revoke: []string{}, FirstCredential: []string{},
	}
	refuse := func(stage string, ps []Problem) {
		for _, q := range ps {
			p.Refusals = append(p.Refusals, Refusal{stage, q.Entry, q.Reason})
		}
	}

	var oldM *model
	var oldC *Compiled
	if inForce != nil {
		m, err := validate(*inForce)
		if err != nil {
			refuse(StageInForce, problemsOf(err))
		} else {
			oldM, oldC = m, build(m)
		}
	}

	newM, err := validate(next)
	var newC *Compiled
	if err != nil {
		refuse(StageValidate, problemsOf(err))
	} else {
		newC = build(newM)
		if err := Check(next, newC); err != nil {
			refuse(StageCheck, problemsOf(err))
		}
		refuse(StageSize, SizeCheck(newC, servers))
	}

	if len(p.Refusals) == 0 {
		if err := p.diff(oldM, oldC, newM, newC); err != nil {
			p.Refusals = append(p.Refusals, Refusal{StageCheck, "dry run", err.Error()})
		}
	}
	if len(p.Refusals) > 0 {
		p.Refused = true
		p.Receivers, p.Templates, p.Tags = []ReceiverChange{}, []TemplateChange{}, []TagChange{}
		p.RotateAndRevoke, p.NewCredential, p.Revoke, p.FirstCredential = []string{}, []string{}, []string{}, []string{}
		sort.SliceStable(p.Refusals, func(i, j int) bool {
			a, b := p.Refusals[i], p.Refusals[j]
			if a.Stage != b.Stage {
				return stageRank[a.Stage] < stageRank[b.Stage]
			}
			if a.Entry != b.Entry {
				return a.Entry < b.Entry
			}
			return a.Reason < b.Reason
		})
	}
	return p
}

func problemsOf(err error) []Problem {
	var ve *ValidationError
	var ce *CheckError
	switch {
	case errors.As(err, &ve):
		return ve.Problems
	case errors.As(err, &ce):
		return ce.Problems
	}
	return []Problem{{"list set", err.Error()}}
}

// diff fills the change lists. oldM and oldC are nil when nothing is in force.
func (p *Plan) diff(oldM *model, oldC *Compiled, newM *model, newC *Compiled) error {
	oldUsers := map[string]User{}
	oldTpl := map[string]string{}
	var universe []string
	if oldC != nil {
		for _, u := range oldC.Users {
			oldUsers[u.Sender] = u
		}
		for _, t := range oldC.Templates {
			oldTpl[t.Group] = permKey(t)
		}
		universe = universeNamed(oldM)
	}
	newUsers := map[string]User{}
	newTpl := map[string]string{}
	for _, u := range newC.Users {
		newUsers[u.Sender] = u
	}
	for _, t := range newC.Templates {
		newTpl[t.Group] = permKey(t)
	}
	universe = append(universe, universeNamed(newM)...)
	slices.Sort(universe)
	universe = slices.Compact(universe)

	// Receivers, from the effective reach of each credential.
	senders := map[string]bool{}
	for s := range oldUsers {
		senders[s] = true
	}
	for s := range newUsers {
		senders[s] = true
	}
	for _, s := range sortedKeys(senders) {
		var before, after map[Verb][]string
		var err error
		if _, ok := oldUsers[s]; ok {
			if before, err = reachOver(oldC, s, universe); err != nil {
				return err
			}
		}
		if _, ok := newUsers[s]; ok {
			if after, err = reachOver(newC, s, universe); err != nil {
				return err
			}
		}
		for _, v := range Verbs {
			added := without(after[v], before[v])
			removed := without(before[v], after[v])
			if len(added) > 0 || len(removed) > 0 {
				p.Receivers = append(p.Receivers, ReceiverChange{s, v, added, removed})
			}
		}
	}

	// Templates.
	groups := map[string]bool{}
	for g := range oldTpl {
		groups[g] = true
	}
	for g := range newTpl {
		groups[g] = true
	}
	for _, g := range sortedKeys(groups) {
		o, inOld := oldTpl[g]
		n, inNew := newTpl[g]
		tc := TemplateChange{Group: g, DisconnectedSenders: []string{}}
		switch {
		case !inOld:
			tc.Change = TemplateAdded
		case !inNew:
			tc.Change = TemplateRemoved
		case o != n:
			tc.Change = TemplateChanged
			tc.Disconnects = true
			for _, s := range sortedKeys(senders) {
				_, wasIn := oldUsers[s]
				_, stays := newUsers[s]
				if a, _ := parseAddress(s); wasIn && stays && a.group() == g {
					tc.DisconnectedSenders = append(tc.DisconnectedSenders, s)
				}
			}
		default:
			continue
		}
		p.Templates = append(p.Templates, tc)
	}

	// Tags and credentials.
	for _, s := range sortedKeys(senders) {
		o, inOld := oldUsers[s]
		n, inNew := newUsers[s]
		switch {
		case !inNew:
			p.Revoke = append(p.Revoke, s)
		case !inOld:
			p.FirstCredential = append(p.FirstCredential, s)
			p.Tags = append(p.Tags, TagChange{s, []string{}, nonNil(n.Tags)})
		case !slices.Equal(o.Tags, n.Tags):
			p.Tags = append(p.Tags, TagChange{s, nonNil(o.Tags), nonNil(n.Tags)})
			if Narrowed(o.Tags, n.Tags) {
				p.RotateAndRevoke = append(p.RotateAndRevoke, s)
			} else {
				p.NewCredential = append(p.NewCredential, s)
			}
		}
	}
	return nil
}

// permKey is a canonical form of a template's permissions, independent of line order.
func permKey(t Template) string {
	var b strings.Builder
	for _, l := range [][]string{t.Permissions.Pub.Allow, t.Permissions.Pub.Deny, t.Permissions.Sub.Allow, t.Permissions.Sub.Deny} {
		c := slices.Clone(l)
		slices.Sort(c)
		b.WriteString(strings.Join(c, "\x00"))
		b.WriteString("\x01")
	}
	return b.String()
}

// without returns the elements of a that are not in b, in order, never nil.
func without(a, b []string) []string {
	out := []string{}
	for _, x := range a {
		if !slices.Contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return slices.Clone(s)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
