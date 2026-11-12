package listset

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/nats-io/jwt/v2"
)

// Check refuses compiled output that would not enforce ls exactly (REQ-006, AT-004). For each sender it expands the
// sender's group template with the sender's tags the way nats-server does at connect (auth.go,
// processUserPermissionsTemplate: allow lines whose tag is missing drop out; if every allow line of a direction drops
// out the server denies everything; a deny line with a missing tag stops the user connecting), then compares what the
// expanded permissions reach, per verb, with what the lists give: the owner line on shared verbs, plus the allow list,
// minus the deny list. It refuses on any address reachable that the lists do not give, and on any the lists give that
// is not reachable.
//
// The addresses compared are every address the list set names plus probes: another name in each owner group, another
// owner group in the range, another range. Check also refuses a template with no tag-free allow line in either
// direction, a user that carries permission claims or a tag other than allow: and deny:, a deny-key user without a
// deny tag, and any line outside the fixed set and the agents. subjects the lists give.
//
// A list set that fails Validate is returned as its *ValidationError. Otherwise the error is a *CheckError.
func Check(ls ListSet, c *Compiled) error {
	m, err := validate(ls)
	if err != nil {
		return err
	}
	if c == nil {
		return &CheckError{Problems: []Problem{{"compiled policy", "is nil"}}}
	}
	var ps []Problem
	bad := func(entry, format string, a ...any) { ps = append(ps, Problem{entry, fmt.Sprintf(format, a...)}) }

	if c.Range != m.rng {
		bad("compiled range "+quote(c.Range), "is not the list set's range %q", m.rng)
	}

	senderGroups := map[string]bool{}
	for _, s := range m.senders {
		senderGroups[s.addr.group()] = true
	}
	templates := map[string]jwt.Permissions{}
	for _, t := range c.Templates {
		entry := "template " + quote(t.Group)
		if !senderGroups[t.Group] {
			bad(entry, "belongs to no owner group that has a sender")
			continue
		}
		if _, dup := templates[t.Group]; dup {
			bad(entry, "appears twice")
			continue
		}
		templates[t.Group] = t.Permissions
		if !slices.ContainsFunc(t.Permissions.Pub.Allow, tagFree) {
			bad(entry, "no publish allow line that needs no tag: a user whose tags are all missing would be left unrestricted or unable to reach its own inbox (REQ-006)")
		}
		if !slices.ContainsFunc(t.Permissions.Sub.Allow, tagFree) {
			bad(entry, "no subscribe allow line that needs no tag (REQ-006)")
		}
	}
	for g := range senderGroups {
		if _, ok := templates[g]; !ok {
			bad("template "+quote(g), "missing for an owner group that has a sender")
		}
	}

	users := map[string]User{}
	for _, u := range c.Users {
		if _, dup := users[u.Sender]; dup {
			bad("user "+quote(u.Sender), "appears twice")
			continue
		}
		users[u.Sender] = u
	}
	known := map[string]bool{}
	for _, s := range m.senders {
		known[s.addr.String()] = true
	}
	for _, u := range c.Users {
		if !known[u.Sender] {
			bad("user "+quote(u.Sender), "is not a sender of the list set")
		}
	}

	universe := universeOf(m)
	for _, s := range m.senders {
		who := "sender " + s.addr.String()
		u, ok := users[s.addr.String()]
		if !ok {
			bad(who, "has no user")
			continue
		}
		tpl, ok := templates[s.addr.group()]
		if !ok {
			continue
		}
		if !claimsEmpty(u.Permissions) {
			bad(who, "its user JWT carries permission claims; a user signed by a scoped key carries tags only")
		}
		if u.Name != s.addr.name {
			bad(who, "user name %q is not the sender's name part %q, which the template's {{name()}} expands to", u.Name, s.addr.name)
		}
		for _, t := range u.Tags {
			if !strings.HasPrefix(t, AllowTag+":") && !strings.HasPrefix(t, DenyTag+":") {
				bad(who, "tag %q is neither allow: nor deny:", t)
			}
		}
		p, err := expandPermissions(tpl, u.Name, u.Tags)
		if err != nil {
			bad(who, "cannot connect: %v", err)
			continue
		}
		for _, line := range p.pubAllow {
			if r := m.lineRefusal(s, "pub", line); r != "" {
				bad(who, "publish line %q: %s", line, r)
			}
		}
		for _, line := range p.subAllow {
			if r := m.lineRefusal(s, "sub", line); r != "" {
				bad(who, "subscribe line %q: %s", line, r)
			}
		}
		// The addresses its own expanded lines name join the universe, so a tag that names an address no list gives is
		// compared too.
		uni := slices.Clone(universe)
		for _, line := range slices.Concat(p.pubAllow, p.subAllow) {
			if toks := strings.Split(line, "."); len(toks) == 5 && toks[0] == "agents" {
				uni = append(uni, strings.Join(toks[2:], "."))
			}
		}
		slices.Sort(uni)
		uni = slices.Compact(uni)
		for _, v := range Verbs {
			want := wantReach(m, s, v, uni)
			got := p.reach(v, uni)
			for _, a := range uni {
				switch {
				case got[a] && !want[a]:
					bad(who+", verb "+string(v), "%s %s, which its lists do not give", reachWord(v), a)
				case !got[a] && want[a]:
					bad(who+", verb "+string(v), "cannot %s %s, which its lists give", reachVerb(v), a)
				}
			}
		}
		// What no list gives: publishing another sender's heartbeat, and subscribing to prompts, status or mail of any
		// address but its own.
		self := s.addr.String()
		for _, a := range uni {
			if a != self && p.canPub(subj(a, Heartbeat)) {
				bad(who+", verb heartbeat", "may publish the heartbeat of %s; a sender publishes only its own", a)
			}
			for _, v := range []Verb{Prompt, Status} {
				if a != self && p.canSub(subj(a, v)) {
					bad(who+", verb "+string(v), "may subscribe to the %s subject of %s; a sender serves only its own", v, a)
				}
			}
			if p.canSub(subj(a, Mail)) {
				bad(who+", verb mail", "may subscribe to the mail subject of %s; mail is read from the sender's own inbox stream only", a)
			}
		}
	}

	if len(ps) > 0 {
		return &CheckError{Problems: ps}
	}
	return nil
}

func reachWord(v Verb) string {
	if v == Heartbeat {
		return "may watch the heartbeat of"
	}
	return "may publish " + string(v) + " to"
}

func reachVerb(v Verb) string {
	if v == Heartbeat {
		return "watch the heartbeat of"
	}
	return "publish " + string(v) + " to"
}

func subj(address string, v Verb) string { return "agents." + v.subject() + "." + address }

// wantReach is the set the lists give a sender for verb v: the owner line on a shared verb, plus the allow list, minus
// the deny list.
//
// The owner line covers the whole group, including names no list mentions, so the universe's probe in the group is
// part of it.
func wantReach(m *model, s sender, v Verb, universe []string) map[string]bool {
	want := map[string]bool{}
	if m.shared(s.addr.group(), v) {
		for _, a := range universe {
			if strings.HasPrefix(a, s.addr.group()+".") {
				want[a] = true
			}
		}
	}
	for _, a := range s.allow {
		want[a.String()] = true
	}
	for _, d := range s.deny {
		delete(want, d.String())
	}
	return want
}

// universeAll is every address the list set names.
func (m *model) universeAll() []address {
	seen := map[address]bool{}
	var out []address
	add := func(a address) {
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	for _, s := range m.senders {
		add(s.addr)
		for _, a := range s.allow {
			add(a)
		}
		for _, d := range s.deny {
			add(d)
		}
	}
	return out
}

// universeOf is every address the list set names plus probes: a name no list names in each owner group the set
// mentions, another owner group of the range, and another range.
func universeOf(m *model) []string {
	named := m.universeAll()
	taken := map[string]bool{}
	owners := map[string]bool{}
	for _, a := range named {
		taken[a.String()] = true
		owners[a.owner] = true
	}
	free := func(rng, owner, name string) address {
		a := address{rng, owner, name}
		for i := 2; taken[a.String()]; i++ {
			a.name = fmt.Sprintf("%s%d", name, i)
		}
		taken[a.String()] = true
		return a
	}
	freeOwner := "probe-owner"
	for owners[freeOwner] {
		freeOwner += "x"
	}
	out := slices.Clone(named)
	groups := map[string]address{}
	for _, a := range named {
		groups[a.group()] = a
	}
	keys := make([]string, 0, len(groups))
	for g := range groups {
		keys = append(keys, g)
	}
	sort.Strings(keys)
	for _, g := range keys {
		a := groups[g]
		out = append(out, free(a.rng, a.owner, "probe-name"))
		out = append(out, free("probe-range", a.owner, "probe-name"))
	}
	out = append(out, free(m.rng, freeOwner, "probe-name"))
	res := make([]string, len(out))
	for i, a := range out {
		res[i] = a.String()
	}
	slices.Sort(res)
	return slices.Compact(res)
}

// fixedLines are the non-agents lines a sender's expanded template may hold: publish, then subscribe.
func fixedLines(s sender) (pub, sub []string) {
	flat := s.addr.rng + "_" + s.addr.owner + "_" + s.addr.name
	for _, l := range fixedPub("", flat) {
		if !strings.HasPrefix(l, "agents.") && l != "" {
			pub = append(pub, l)
		}
	}
	return pub, []string{"_INBOX." + flat + ".>", "$SRV.>"}
}

// lineRefusal says why an expanded allow line is not one the lists could have asked for, or "".
func (m *model) lineRefusal(s sender, dir, line string) string {
	toks := strings.Split(line, ".")
	if toks[0] != "agents" {
		pub, sub := fixedLines(s)
		fixed := pub
		if dir == "sub" {
			fixed = sub
		}
		if !slices.Contains(fixed, line) {
			return "is neither an agents. subject nor one of the sender's fixed lines"
		}
		return ""
	}
	if len(toks) != 5 {
		return "is not a five-token address subject"
	}
	var v Verb
	for _, x := range Verbs {
		if toks[1] == x.subject() {
			v = x
		}
	}
	if v == "" {
		return fmt.Sprintf("has verb %q, which is none of mail, prompt, status, hb", toks[1])
	}
	if toks[2] != m.rng {
		return fmt.Sprintf("range part %q is not the account's range %q", toks[2], m.rng)
	}
	own := s.addr.String()
	lit := toks[2] + "." + toks[3] + "." + toks[4]
	switch {
	case dir == "pub" && v == Heartbeat:
		if lit != own {
			return "lets the sender publish a heartbeat that is not its own"
		}
		return ""
	case dir == "sub" && v == Mail:
		return "lets the sender subscribe to mail subjects; mail is read from its own inbox stream only"
	case dir == "sub" && (v == Prompt || v == Status):
		if lit != own {
			return "lets the sender serve a " + string(v) + " subject that is not its own"
		}
		return ""
	}
	if toks[4] == "*" {
		if toks[3] != s.addr.owner || strings.ContainsAny(toks[3], "*>") {
			return "is a wildcard outside the sender's owner group"
		}
		if !m.shared(s.addr.group(), v) {
			return "is an owner line on a verb that is not shared"
		}
		return ""
	}
	if strings.ContainsAny(line[len("agents."):], "*>") {
		return "holds a wildcard that is not the owner line"
	}
	if _, reason := parseAddress(toks[2] + "." + toks[3] + "." + toks[4]); reason != "" {
		return "does not name a valid address: " + reason
	}
	return ""
}

// claimsEmpty reports whether l is the empty claim set a scoped-key user must carry.
func claimsEmpty(l jwt.UserPermissionLimits) bool {
	return l.Pub.Empty() && l.Sub.Empty() && l.Resp == nil &&
		len(l.Src) == 0 && len(l.Times) == 0 && l.Locale == "" &&
		l.Subs == 0 && l.Data == 0 && l.Payload == 0 &&
		!l.BearerToken && !l.ProxyRequired && len(l.AllowedConnectionTypes) == 0
}

func tagFree(line string) bool {
	for _, tok := range mustacheRE.FindAllString(line, -1) {
		op := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(tok, "{{"), "}}")))
		if strings.HasPrefix(op, "tag(") || strings.HasPrefix(op, "account-tag(") {
			return false
		}
	}
	return true
}

// expanded is a user's permissions after the server filled in the template.
type expanded struct {
	pubAllow, pubDeny, subAllow, subDeny []string
	pubOpen, subOpen                     bool // no allow list at all: the server restricts nothing
}

// mustacheRE is the server's template pattern (auth.go).
var mustacheRE = regexp.MustCompile(`{{2}([^}]+)}{2}`)

// expandPermissions fills tpl in for a user with the given name and tags, as nats-server's
// processUserPermissionsTemplate does: the lists are processed in the order sub allow, sub deny, pub allow, pub deny;
// a deny list fails on a missing tag or an invalid subject, an allow list drops the line; an allow list that was not
// empty and is empty afterwards gains a deny of everything.
func expandPermissions(tpl jwt.Permissions, name string, tags []string) (*expanded, error) {
	var e expanded
	var err error
	if e.subAllow, err = expandList(tpl.Sub.Allow, name, tags, false); err != nil {
		return nil, err
	}
	if e.subDeny, err = expandList(tpl.Sub.Deny, name, tags, true); err != nil {
		return nil, err
	}
	if e.pubAllow, err = expandList(tpl.Pub.Allow, name, tags, false); err != nil {
		return nil, err
	}
	if e.pubDeny, err = expandList(tpl.Pub.Deny, name, tags, true); err != nil {
		return nil, err
	}
	e.subOpen, e.pubOpen = len(tpl.Sub.Allow) == 0, len(tpl.Pub.Allow) == 0
	if len(tpl.Sub.Allow) > 0 && len(e.subAllow) == 0 {
		e.subDeny = append(e.subDeny, ">")
	}
	if len(tpl.Pub.Allow) > 0 && len(e.pubAllow) == 0 {
		e.pubDeny = append(e.pubDeny, ">")
	}
	return &e, nil
}

func expandList(list []string, name string, tags []string, failOnBadSubject bool) ([]string, error) {
	var out []string
	for _, line := range list {
		toks := mustacheRE.FindAllString(line, -1)
		if len(toks) == 0 {
			out = append(out, line)
			continue
		}
		values := make([][]string, len(toks))
		for i, tk := range toks {
			op := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(tk, "{{"), "}}"))
			switch {
			case strings.EqualFold(op, "name()"):
				values[i] = []string{name}
			case len(op) >= 4 && strings.EqualFold(op[:4], "tag(") && strings.HasSuffix(op, ")"):
				prefix := strings.ToLower(strings.TrimSuffix(op[4:], ")")) + ":"
				for _, t := range tags {
					if strings.HasPrefix(t, prefix) {
						values[i] = append(values[i], strings.TrimPrefix(t, prefix))
					}
				}
				if len(values[i]) == 0 {
					if failOnBadSubject {
						return nil, fmt.Errorf("generated invalid subject %q: %q is not defined: every user of a key with a tag-driven deny line needs a deny tag", line, op[4:len(op)-1])
					}
					values[i] = []string{" "} // the server's invalid-subject marker; the line is dropped below
				}
			default:
				return nil, fmt.Errorf("template operation in %q: %q is not defined", line, op)
			}
		}
		for _, combo := range product(values) {
			s := line
			for j, tk := range toks {
				s = strings.ReplaceAll(s, tk, combo[j])
			}
			switch {
			case validSubject(s):
				out = append(out, s)
			case failOnBadSubject:
				return nil, errors.New("generated invalid subject")
			}
		}
	}
	return out, nil
}

func product(vals [][]string) [][]string {
	out := [][]string{nil}
	for _, vs := range vals {
		var next [][]string
		for _, prefix := range out {
			for _, v := range vs {
				next = append(next, append(slices.Clone(prefix), v))
			}
		}
		out = next
	}
	return out
}

// validSubject is nats-server's IsValidSubject: no empty or blank token, and '>' only as the last token.
func validSubject(s string) bool {
	if s == "" {
		return false
	}
	toks := strings.Split(s, ".")
	for i, t := range toks {
		if t == "" || strings.ContainsAny(t, "\t\n\f\r ") {
			return false
		}
		if t == ">" && i != len(toks)-1 {
			return false
		}
	}
	return true
}

// subjectMatches reports whether literal subject is matched by filter: '*' matches one token, a final '>' one or
// more.
func subjectMatches(filter, subject string) bool {
	f, s := strings.Split(filter, "."), strings.Split(subject, ".")
	for i, ft := range f {
		if ft == ">" && i == len(f)-1 {
			return len(s) > i
		}
		if i >= len(s) || (ft != "*" && ft != s[i]) {
			return false
		}
	}
	return len(f) == len(s)
}

func matchAny(filters []string, subject string) bool {
	return slices.ContainsFunc(filters, func(f string) bool { return subjectMatches(f, subject) })
}

func (e *expanded) canPub(subject string) bool {
	return (e.pubOpen || matchAny(e.pubAllow, subject)) && !matchAny(e.pubDeny, subject)
}

func (e *expanded) canSub(subject string) bool {
	return (e.subOpen || matchAny(e.subAllow, subject)) && !matchAny(e.subDeny, subject)
}

// reach is the set of addresses of universe the user reaches with verb v: a publish for mail, prompt and status, a
// subscription for heartbeat.
func (e *expanded) reach(v Verb, universe []string) map[string]bool {
	out := map[string]bool{}
	for _, a := range universe {
		if v == Heartbeat && e.canSub(subj(a, v)) || v != Heartbeat && e.canPub(subj(a, v)) {
			out[a] = true
		}
	}
	return out
}

// effective returns, for sender, the addresses it reaches per verb once its tags are expanded through its template.
func effective(ls ListSet, c *Compiled, sender string) (map[Verb][]string, error) {
	m, err := validate(ls)
	if err != nil {
		return nil, err
	}
	return reachOver(c, sender, universeNamed(m))
}

// reachOver is the simulation behind effective and DryRun: the addresses of universe that sender reaches per verb
// once its tags are expanded through its group's template, the way nats-server does it (expandPermissions and
// expanded.reach, the same code Check uses). Each slice is sorted.
func reachOver(c *Compiled, sender string, universe []string) (map[Verb][]string, error) {
	p, err := expandSender(c, sender)
	if err != nil {
		return nil, err
	}
	out := map[Verb][]string{}
	for _, v := range Verbs {
		for _, addr := range universe {
			if p.reach(v, []string{addr})[addr] {
				out[v] = append(out[v], addr)
			}
		}
		slices.Sort(out[v])
	}
	return out, nil
}

// expandSender finds sender's user and group template in c and expands the template with the user's name and tags.
func expandSender(c *Compiled, sender string) (*expanded, error) {
	var tpl *Template
	var user *User
	for i := range c.Users {
		if c.Users[i].Sender == sender {
			user = &c.Users[i]
		}
	}
	if user == nil {
		return nil, fmt.Errorf("no user %s", sender)
	}
	a, reason := parseAddress(sender)
	if reason != "" {
		return nil, errors.New(reason)
	}
	for i := range c.Templates {
		if c.Templates[i].Group == a.group() {
			tpl = &c.Templates[i]
		}
	}
	if tpl == nil {
		return nil, fmt.Errorf("no template %s", a.group())
	}
	return expandPermissions(tpl.Permissions, user.Name, user.Tags)
}

// universeNamed is the addresses the list set names, sorted.
func universeNamed(m *model) []string {
	var out []string
	for _, a := range m.universeAll() {
		out = append(out, a.String())
	}
	slices.Sort(out)
	return out
}

// ReachOf returns, for sender in c, the addresses of universe it reaches per verb once its tags are expanded through
// its group's template, the way nats-server does it: the simulation behind DryRun. Each slice is sorted. It is an error
// for c to have no user or no template for sender.
func ReachOf(c *Compiled, sender string, universe []string) (map[Verb][]string, error) {
	return reachOver(c, sender, universe)
}

// Subject returns the subject a sender uses for verb v towards address: a publish subject for mail, prompt and status,
// and the subject a sender subscribes to for heartbeat.
func Subject(v Verb, address string) string { return subj(address, v) }
