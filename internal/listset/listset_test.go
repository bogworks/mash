package listset

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
)

// AT-002: each bad entry is refused, every one is named in one error, and nothing is produced.
func TestAT002_BadEntriesRefuseTheWholeSet(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ListSet)
		entry  string // part of the Problem.Entry that must name it
		reason string
	}{
		{"wildcard owner-part star", func(l *ListSet) { l.Senders[0].Allow = append(l.Senders[0].Allow, "foo.c.*") }, `"foo.c.*"`, "wildcard"},
		{"wildcard tail", func(l *ListSet) { l.Senders[0].Allow = append(l.Senders[0].Allow, "foo.>") }, `"foo.>"`, "wildcard"},
		{"wildcard in a deny", func(l *ListSet) { l.Senders[0].Deny = append(l.Senders[0].Deny, "foo.*.sdk") }, `"foo.*.sdk"`, "wildcard"},
		{"wildcard sender", func(l *ListSet) { l.Senders = append(l.Senders, SenderLists{Sender: "foo.c.*"}) }, `sender "foo.c.*"`, "wildcard"},
		{"wildcard group", func(l *ListSet) { l.Switches = append(l.Switches, SwitchEntry{"foo.*", Mail, Shared}) }, `switch "foo.*"`, "wildcard"},
		{"other range", func(l *ListSet) { l.Senders[1].Allow = append(l.Senders[1].Allow, "bar.c.lead") }, `"bar.c.lead"`, "range part"},
		{"other range sender", func(l *ListSet) { l.Senders = append(l.Senders, SenderLists{Sender: "bar.c.lead"}) }, `sender "bar.c.lead"`, "range part"},
		{"other range group", func(l *ListSet) { l.Switches = append(l.Switches, SwitchEntry{"bar.c", Mail, Shared}) }, `switch "bar.c"`, "range part"},
		{"grammar failing part", func(l *ListSet) { l.Senders[2].Allow = append(l.Senders[2].Allow, "foo.C.lead") }, `"foo.C.lead"`, "lower-case"},
		{"grammar failing name", func(l *ListSet) { l.Senders[2].Allow = append(l.Senders[2].Allow, "foo.c.-lead") }, `"foo.c.-lead"`, "letter"},
		{"two parts", func(l *ListSet) { l.Senders[2].Allow = append(l.Senders[2].Allow, "foo.c") }, `"foo.c"`, "three"},
		{"switch for one sender", func(l *ListSet) { l.Switches = append(l.Switches, SwitchEntry{"foo.c.core", Mail, Shared}) }, `switch "foo.c.core"`, "single sender"},
		{"duplicate group and verb, same value", func(l *ListSet) { l.Switches = append(l.Switches, SwitchEntry{"foo.c", Mail, Closed}) }, `switch "foo.c" "mail"`, "twice"},
		{"duplicate group and verb, other value", func(l *ListSet) { l.Switches = append(l.Switches, SwitchEntry{"foo.c", Status, Closed}) }, `switch "foo.c" "status"`, "twice"},
		{"missing verb", func(l *ListSet) { l.Switches = l.Switches[:3] }, `group "foo.c" "heartbeat"`, "no switch"},
		{"unknown verb", func(l *ListSet) { l.Switches[0].Verb = "post" }, `"post"`, "unknown verb"},
		{"unknown switch", func(l *ListSet) { l.Switches[0].Switch = "open" }, `"mail"`, "unknown switch"},
		{"sender twice", func(l *ListSet) { l.Senders = append(l.Senders, SenderLists{Sender: "foo.c.lead"}) }, `sender "foo.c.lead"`, "twice"},
		{"allow twice", func(l *ListSet) { l.Senders[0].Allow = []string{"foo.c.lead", "foo.c.lead"} }, `allow of "foo.c.core"`, "twice"},
		{"allow and deny", func(l *ListSet) { l.Senders[0].Deny = []string{"foo.c.lead"} }, `deny of "foo.c.core"`, "allow list"},
		{"bad range", func(l *ListSet) { l.Range = "" }, `range ""`, "empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ls := at001()
			tc.mutate(&ls)
			c, err := Compile(ls)
			if c != nil {
				t.Fatal("Compile returned output for a refused list set")
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("want *ValidationError, got %T %v", err, err)
			}
			for _, p := range ve.Problems {
				if strings.Contains(p.Entry, tc.entry) && strings.Contains(p.Reason, tc.reason) {
					return
				}
			}
			t.Fatalf("no problem with entry %q and reason %q in %v", tc.entry, tc.reason, ve)
		})
	}
}

func TestAT002_AllNamedInOneError(t *testing.T) {
	ls := at001()
	ls.Senders[0].Allow = append(ls.Senders[0].Allow, "foo.c.*", "foo.>", "bar.c.lead", "foo.C.x")
	ls.Switches = append(ls.Switches,
		SwitchEntry{"foo.c.core", Mail, Shared}, // one sender only
		SwitchEntry{"foo.c", Mail, Closed},      // duplicate
	)
	ls.Switches = slices.DeleteFunc(ls.Switches, func(e SwitchEntry) bool { return e.Verb == Heartbeat }) // missing verb
	c, err := Compile(ls)
	if c != nil || err == nil {
		t.Fatal("want refusal with no output")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want *ValidationError, got %T", err)
	}
	if len(ve.Problems) != 7 {
		t.Fatalf("want 7 problems, got %d: %v", len(ve.Problems), ve)
	}
	msg := err.Error()
	for _, want := range []string{`"foo.c.*"`, `"foo.>"`, `"bar.c.lead"`, `"foo.C.x"`, `switch "foo.c.core"`, `switch "foo.c" "mail"`, `group "foo.c" "heartbeat"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not name %s: %s", want, msg)
		}
	}
}

func TestDenyRule(t *testing.T) {
	// A deny in a group with no shared verb, a deny outside the sender's group, and one on a sender not in the group.
	for name, mk := range map[string]func() ListSet{
		"no shared verb": func() ListSet {
			ls := at001()
			ls.Switches = switches("foo.c", Closed, Closed, Closed, Closed)
			ls.Senders[1].Deny = []string{"foo.c.sdk"}
			return ls
		},
		"outside the owner group": func() ListSet {
			ls := at003Deny()
			ls.Senders[1].Deny = []string{"foo.d.solo"}
			return ls
		},
	} {
		t.Run(name, func(t *testing.T) {
			if c, err := Compile(mk()); c != nil || err == nil {
				t.Fatalf("want refusal, got %v %v", c, err)
			}
		})
	}
}

// Validate and Compile are pure: the input is untouched.
func TestCompileDoesNotModifyInput(t *testing.T) {
	ls := at003Deny()
	before := strings.Join(flatten(ls), "|")
	if _, err := Compile(ls); err != nil {
		t.Fatal(err)
	}
	if after := strings.Join(flatten(ls), "|"); after != before {
		t.Fatalf("input changed:\n%s\n%s", before, after)
	}
}

func flatten(ls ListSet) []string {
	out := []string{ls.Range}
	for _, s := range ls.Switches {
		out = append(out, s.Group, string(s.Verb), string(s.Switch))
	}
	for _, s := range ls.Senders {
		out = append(out, s.Sender)
		out = append(out, s.Allow...)
		out = append(out, "/")
		out = append(out, s.Deny...)
	}
	return out
}

// AT-004 over the corpus.
func TestAT004_Corpus(t *testing.T) {
	for _, tc := range corpus() {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Compile(tc.ls)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			if err := Check(tc.ls, c); err != nil {
				t.Fatalf("Check: %v", err)
			}
			denyKey := map[string]bool{}
			for _, g := range tc.denyKeys {
				denyKey[g] = true
			}
			for _, tpl := range c.Templates {
				if !slices.ContainsFunc(tpl.Permissions.Pub.Allow, tagFree) || !slices.ContainsFunc(tpl.Permissions.Sub.Allow, tagFree) {
					t.Errorf("template %s has no tag-free allow line", tpl.Group)
				}
				hasDeny := len(tpl.Permissions.Pub.Deny)+len(tpl.Permissions.Sub.Deny) > 0
				if hasDeny != denyKey[tpl.Group] {
					t.Errorf("template %s: deny lines %v, want %v", tpl.Group, hasDeny, denyKey[tpl.Group])
				}
			}
			for _, u := range c.Users {
				if !claimsEmpty(u.Permissions) {
					t.Errorf("user %s carries claims", u.Sender)
				}
				for _, tag := range u.Tags {
					if !strings.HasPrefix(tag, "allow:") && !strings.HasPrefix(tag, "deny:") {
						t.Errorf("user %s: tag %q", u.Sender, tag)
					}
				}
				a, _ := parseAddress(u.Sender)
				hasDenyTag := slices.ContainsFunc(u.Tags, func(s string) bool { return strings.HasPrefix(s, "deny:") })
				if denyKey[a.group()] != hasDenyTag {
					t.Errorf("user %s: deny tag %v, want %v", u.Sender, hasDenyTag, denyKey[a.group()])
				}
			}
			for sender, want := range tc.want {
				got, err := effective(tc.ls, c, sender)
				if err != nil {
					t.Fatalf("%s: %v", sender, err)
				}
				for _, v := range Verbs {
					w := slices.Clone(want[v])
					slices.Sort(w)
					if !slices.Equal(got[v], w) {
						t.Errorf("%s %s: reaches %v, want %v", sender, v, got[v], w)
					}
				}
			}
			if len(tc.want) != len(c.Users) {
				t.Errorf("corpus lists %d senders, compiled %d users", len(tc.want), len(c.Users))
			}
		})
	}
}

// A sender whose tags were all removed (JRN-006) reaches the owner line on shared verbs and nothing else; under a
// deny key it cannot connect at all.
func TestAT004_ZeroTags(t *testing.T) {
	ls := at001()
	c, err := Compile(ls)
	if err != nil {
		t.Fatal(err)
	}
	for i := range c.Users {
		c.Users[i].Tags = nil
	}
	got, err := effective(ls, c, "foo.c.core")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []Verb{Mail, Prompt, Heartbeat} {
		if len(got[v]) != 0 {
			t.Errorf("zero tags: %s reaches %v", v, got[v])
		}
	}
	if want := []string{"foo.c.core", "foo.c.lead", "foo.c.sdk"}; !slices.Equal(got[Status], want) {
		t.Errorf("zero tags: status reaches %v, want %v", got[Status], want)
	}

	ls = at003Deny()
	c, err = Compile(ls)
	if err != nil {
		t.Fatal(err)
	}
	for i := range c.Users {
		c.Users[i].Tags = nil
	}
	if _, err := effective(ls, c, "foo.c.lead"); err == nil {
		t.Error("a user of a deny key with no tags must not connect")
	}
}

// An empty allow list on a closed verb is no tag and no sentinel.
func TestEmptyAllowIsNoTag(t *testing.T) {
	c, err := Compile(at001())
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range c.Users {
		switch u.Sender {
		case "foo.c.core":
			if !slices.Equal(u.Tags, []string{"allow:c.lead"}) {
				t.Errorf("core tags %v", u.Tags)
			}
		default:
			if len(u.Tags) != 0 {
				t.Errorf("%s tags %v, want none", u.Sender, u.Tags)
			}
		}
	}
}

// Mutations: Check must refuse each of these.
func TestMutationsAreRefused(t *testing.T) {
	type mut struct {
		name   string
		ls     func() ListSet
		mutate func(*Compiled)
		want   string // substring of the error
	}
	tpl := func(c *Compiled, g string) *Template {
		for i := range c.Templates {
			if c.Templates[i].Group == g {
				return &c.Templates[i]
			}
		}
		t.Fatalf("no template %s", g)
		return nil
	}
	user := func(c *Compiled, s string) *User {
		for i := range c.Users {
			if c.Users[i].Sender == s {
				return &c.Users[i]
			}
		}
		t.Fatalf("no user %s", s)
		return nil
	}
	muts := []mut{
		{"only tag-driven allow lines", at001, func(c *Compiled) {
			p := &tpl(c, "foo.c").Permissions
			p.Pub.Allow = slices.DeleteFunc(p.Pub.Allow, tagFree)
			p.Sub.Allow = slices.DeleteFunc(p.Sub.Allow, tagFree)
		}, "no publish allow line that needs no tag"},
		{"deny-key user without a deny tag", at003Deny, func(c *Compiled) {
			u := user(c, "foo.c.lead")
			u.Tags = slices.DeleteFunc(u.Tags, func(s string) bool { return s == NoDenyTag })
		}, "cannot connect"},
		{"owner line kept on a closed verb", at001, func(c *Compiled) {
			p := &tpl(c, "foo.c").Permissions
			p.Pub.Allow.Add("agents.mail.foo.c.*")
		}, "foo.c.core, verb mail"},
		{"owner line kept on a closed heartbeat", at001, func(c *Compiled) {
			tpl(c, "foo.c").Permissions.Sub.Allow.Add("agents.hb.foo.c.*")
		}, "verb heartbeat"},
		{"a wildcard line", at001, func(c *Compiled) {
			tpl(c, "foo.c").Permissions.Pub.Allow.Add("agents.>")
		}, "agents.>"},
		{"a wildcard in another owner group", at001, func(c *Compiled) {
			tpl(c, "foo.c").Permissions.Pub.Allow.Add("agents.mail.foo.*.*")
		}, "agents.mail.foo.*.*"},
		{"another range", at001, func(c *Compiled) {
			tpl(c, "foo.c").Permissions.Pub.Allow.Add("agents.mail.bar.c.lead")
		}, "agents.mail.bar.c.lead"},
		{"a foreign plumbing line", at001, func(c *Compiled) {
			tpl(c, "foo.c").Permissions.Pub.Allow.Add("$JS.API.STREAM.INFO.inbox-foo_c_lead")
		}, "fixed lines"},
		{"another inbox consumer", at001, func(c *Compiled) {
			tpl(c, "foo.c").Permissions.Pub.Allow.Add("$JS.API.CONSUMER.MSG.NEXT.>")
		}, "fixed lines"},
		{"publish another heartbeat", at001, func(c *Compiled) {
			tpl(c, "foo.c").Permissions.Pub.Allow.Add("agents.hb.foo.c.lead")
		}, "heartbeat"},
		{"subscribe to another prompt subject", at001, func(c *Compiled) {
			tpl(c, "foo.c").Permissions.Sub.Allow.Add("agents.prompt.foo.c.lead")
		}, "prompt"},
		{"subscribe to mail", at001, func(c *Compiled) {
			tpl(c, "foo.c").Permissions.Sub.Allow.Add("agents.mail.foo.c.core")
		}, "mail"},
		{"a user with permission claims", at001, func(c *Compiled) {
			user(c, "foo.c.core").Permissions.Pub.Allow = jwt.StringList{"agents.>"}
		}, "permission claims"},
		{"a user with a non-tag claim", at001, func(c *Compiled) {
			user(c, "foo.c.core").Permissions.BearerToken = true
		}, "permission claims"},
		{"a wildcard tag", at001, func(c *Compiled) {
			user(c, "foo.c.sdk").Tags = []string{"allow:c.*"}
		}, "foo.c.sdk, verb mail"},
		{"a wildcard tag at the tail", at001, func(c *Compiled) {
			user(c, "foo.c.sdk").Tags = []string{"allow:>"}
		}, "foo.c.sdk, verb mail"},
		{"a tag that names another range through the template's range", at001, func(c *Compiled) {
			user(c, "foo.c.sdk").Tags = []string{"allow:c.zz-unlisted"}
		}, "zz-unlisted"},
		{"an extra allow tag", at001, func(c *Compiled) {
			user(c, "foo.c.sdk").Tags = []string{"allow:c.core"}
		}, "foo.c.sdk, verb mail"},
		{"a missing allow tag", at001, func(c *Compiled) {
			user(c, "foo.c.core").Tags = nil
		}, "cannot publish mail to foo.c.lead"},
		{"a deny tag dropped from the user that has a real one", at003Deny, func(c *Compiled) {
			user(c, "foo.c.core").Tags = []string{NoDenyTag}
		}, "foo.c.core, verb mail"},
		{"an unknown tag", at001, func(c *Compiled) {
			u := user(c, "foo.c.sdk")
			u.Tags = append(u.Tags, "label:x")
		}, "neither allow: nor deny:"},
		{"a missing user", at001, func(c *Compiled) {
			c.Users = c.Users[:2]
		}, "has no user"},
		{"a user that is not a sender", at001, func(c *Compiled) {
			c.Users = append(c.Users, User{Sender: "foo.c.ghost", Name: "ghost"})
		}, "not a sender"},
		{"a wrong user name", at001, func(c *Compiled) {
			user(c, "foo.c.sdk").Name = "lead"
		}, "user name"},
		{"a missing template", at001, func(c *Compiled) {
			c.Templates = nil
		}, "missing for an owner group"},
		{"a template with no allow line at all (the server would open everything)", at001, func(c *Compiled) {
			tpl(c, "foo.c").Permissions = jwt.Permissions{}
		}, "no publish allow line"},
	}
	for _, tc := range muts {
		t.Run(tc.name, func(t *testing.T) {
			ls := tc.ls()
			c, err := Compile(ls)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			tc.mutate(c)
			err = Check(ls, c)
			var ce *CheckError
			if !errors.As(err, &ce) {
				t.Fatalf("want *CheckError, got %T %v", err, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error does not mention %q: %v", tc.want, err)
			}
		})
	}
}

// Check on a list set that does not validate returns the validation error.
func TestCheckRefusesInvalidListSet(t *testing.T) {
	good, err := Compile(at001())
	if err != nil {
		t.Fatal(err)
	}
	ls := at001()
	ls.Senders[0].Allow = []string{"foo.c.*"}
	var ve *ValidationError
	if err := Check(ls, good); !errors.As(err, &ve) {
		t.Fatalf("want *ValidationError, got %v", err)
	}
	if err := Check(at001(), nil); err == nil {
		t.Fatal("nil Compiled must be refused")
	}
}

func TestSubjectMatches(t *testing.T) {
	for _, tc := range []struct {
		filter, subject string
		want            bool
	}{
		{"agents.mail.foo.c.*", "agents.mail.foo.c.core", true},
		{"agents.mail.foo.c.*", "agents.mail.foo.d.core", false},
		{"agents.mail.foo.c.*", "agents.mail.foo.c.core.x", false},
		{"agents.mail.foo.c.*", "agents.mail.foo.c", false},
		{"agents.mail.foo.*.*", "agents.mail.foo.c.core", true},
		{"agents.>", "agents.mail.foo.c.core", true},
		{"agents.>", "agents", false},
		{"agents.mail.>", "agents.prompt.foo.c.core", false},
		{">", "a", true},
		{"agents.mail.foo.c.core", "agents.mail.foo.c.core", true},
		{"agents.mail.foo.c.core", "agents.mail.foo.c.core2", false},
		{"agents.mail.foo.c.core.>", "agents.mail.foo.c.core", false},
	} {
		if got := subjectMatches(tc.filter, tc.subject); got != tc.want {
			t.Errorf("subjectMatches(%q, %q) = %v, want %v", tc.filter, tc.subject, got, tc.want)
		}
	}
}

// The sentinel can never be an address: it expands to a four-token subject, and no grammar-valid part is "_none".
func TestSentinelAddressesNothing(t *testing.T) {
	for _, v := range Verbs {
		s := "agents." + v.subject() + ".foo." + NoDenyValue
		if n := len(strings.Split(s, ".")); n != 4 {
			t.Errorf("%s has %d tokens", s, n)
		}
	}
	if _, reason := parseAddress("foo.c." + NoDenyValue); reason == "" {
		t.Error("the sentinel value is a valid name")
	}
	if _, reason := parseAddress("foo." + NoDenyValue + ".core"); reason == "" {
		t.Error("the sentinel value is a valid owner")
	}
}

// The template for the AT-001 shape holds no line naming foo.c.sdk (AT-001) and drops the owner line on closed verbs.
func TestAT001TemplateNamesNoPeer(t *testing.T) {
	c, err := Compile(at001())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Templates) != 1 {
		t.Fatalf("want one template, got %d", len(c.Templates))
	}
	p := c.Templates[0].Permissions
	for _, l := range append(slices.Clone(p.Pub.Allow), p.Sub.Allow...) {
		if strings.Contains(l, "sdk") || strings.Contains(l, "lead") || strings.Contains(l, "core") {
			t.Errorf("template line names a sender: %s", l)
		}
	}
	if !slices.Contains(p.Pub.Allow, "agents.status.foo.c.*") {
		t.Error("shared status has no owner line")
	}
	for _, l := range []string{"agents.mail.foo.c.*", "agents.prompt.foo.c.*"} {
		if slices.Contains(p.Pub.Allow, l) {
			t.Errorf("closed verb keeps %s", l)
		}
	}
	if slices.Contains(p.Sub.Allow, "agents.hb.foo.c.*") {
		t.Error("closed heartbeat keeps its owner line")
	}
	if len(p.Pub.Deny)+len(p.Sub.Deny) != 0 {
		t.Error("a key with no deny entry has deny lines")
	}
}

// Every required fixed grant, removed from the template or denied, is refused by Check.
func TestCheckRequiresFixedGrants(t *testing.T) {
	pubLines := fixedPub("agents.hb.foo.c.{{name()}}", PolicySubject("foo"), "foo_c_{{name()}}")
	subLines := []string{
		"_INBOX.foo_c_{{name()}}.>",
		"agents.prompt.foo.c.{{name()}}",
		"agents.status.foo.c.{{name()}}",
		"$SRV.>",
	}
	type cas struct {
		dir, line string
	}
	var cases []cas
	for _, l := range pubLines {
		cases = append(cases, cas{"pub", l})
	}
	for _, l := range subLines {
		cases = append(cases, cas{"sub", l})
	}
	for _, mode := range []string{"removed", "denied"} {
		for _, tc := range cases {
			t.Run(mode+" "+tc.dir+" "+tc.line, func(t *testing.T) {
				ls := at001()
				c, err := Compile(ls)
				if err != nil {
					t.Fatalf("Compile: %v", err)
				}
				var p *jwt.Permissions
				for i := range c.Templates {
					if c.Templates[i].Group == "foo.c" {
						p = &c.Templates[i].Permissions
					}
				}
				if p == nil {
					t.Fatal("no template foo.c")
				}
				allow, deny := &p.Pub.Allow, &p.Pub.Deny
				if tc.dir == "sub" {
					allow, deny = &p.Sub.Allow, &p.Sub.Deny
				}
				if !slices.Contains(*allow, tc.line) {
					t.Fatalf("template has no line %q", tc.line)
				}
				if mode == "removed" {
					*allow = slices.DeleteFunc(*allow, func(s string) bool { return s == tc.line })
				} else {
					deny.Add(tc.line)
				}
				err = Check(ls, c)
				var ce *CheckError
				if !errors.As(err, &ce) {
					t.Fatalf("want *CheckError, got %T %v", err, err)
				}
				if !strings.Contains(err.Error(), "required ") || !strings.Contains(err.Error(), "is missing or denied") {
					t.Fatalf("error does not report a missing required grant: %v", err)
				}
			})
		}
	}
}

// allowTags returns n distinct allow tags and the no-deny tag a deny key requires of every user.
func allowTags(n int) []string {
	tags := make([]string, n, n+1)
	for i := range tags {
		tags[i] = fmt.Sprintf("%s:foo.c.p%d", AllowTag, i)
	}
	return append(tags, NoDenyTag)
}

// The server counts expansions per list and cumulatively over its lines (auth.go, maxPermTemplateSubjectExpansions):
// the publish allow list holds the owner lines of the shared verbs and one allow-tag line per verb, so the third tag
// line is refused when 3N + (shared publish verbs) exceeds 4096. The subscribe list holds N + one owner line.
func TestExpansionLimitBoundary(t *testing.T) {
	cases := []struct {
		name   string
		ls     ListSet
		shared int // shared publish verbs of group foo.c
		max    int // the largest allow-tag count the server accepts
	}{
		{"status shared", at001(), 1, 1365},              // 3*1365+1 = 4096
		{"mail and prompt shared", at003Deny(), 2, 1364}, // 3*1364+2 = 4094; 1365 gives 4097
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := 3*tc.max + tc.shared; got > maxTemplateExpansions || got+3 <= maxTemplateExpansions {
				t.Fatalf("boundary %d is not the largest that fits (%d)", tc.max, got)
			}
			c, err := Compile(tc.ls)
			if err != nil {
				t.Fatal(err)
			}
			var tpl jwt.Permissions
			for _, x := range c.Templates {
				if x.Group == "foo.c" {
					tpl = x.Permissions
				}
			}
			if _, err := expandPermissions(tpl, "core", allowTags(tc.max)); err != nil {
				t.Fatalf("%d tags: %v", tc.max, err)
			}
			_, err = expandPermissions(tpl, "core", allowTags(tc.max+1))
			if !errors.Is(err, errTemplateExpansionLimit) {
				t.Fatalf("%d tags: want the expansion limit, got %v", tc.max+1, err)
			}
			for i := range c.Users {
				if c.Users[i].Sender == "foo.c.core" {
					c.Users[i].Tags = allowTags(tc.max + 1)
				}
			}
			err = Check(tc.ls, c)
			if err == nil || !strings.Contains(err.Error(), "cannot connect") || !strings.Contains(err.Error(), "template expansion exceeds limit") {
				t.Fatalf("Check with %d tags: want cannot connect, got %v", tc.max+1, err)
			}
		})
	}
}

// The limit is per list and counts lines cumulatively; a line without a tag token is emitted unchecked.
func TestExpandListLimitCounting(t *testing.T) {
	tags := allowTags(2048)
	two := []string{"a.{{tag(allow)}}", "b.{{tag(allow)}}"}
	if out, err := expandList(two, "n", tags, false); err != nil || len(out) != 4096 {
		t.Fatalf("2x2048: %d lines, %v", len(out), err)
	}
	if _, err := expandList(append(slices.Clone(two), "c.{{tag(allow)}}"), "n", allowTags(1366), false); err == nil {
		t.Fatal("3x1366 is accepted")
	}
	// A fixed line first counts toward the budget; one after the tag lines does not.
	if _, err := expandList(append([]string{"fixed"}, two...), "n", tags, false); !errors.Is(err, errTemplateExpansionLimit) {
		t.Fatalf("fixed line first: %v", err)
	}
	if _, err := expandList(append(slices.Clone(two), "fixed.{{name()}}"), "n", tags, false); err != nil {
		t.Fatalf("fixed line last: %v", err)
	}
	// Two tokens on one line multiply.
	if _, err := expandList([]string{"{{tag(allow)}}.{{tag(allow)}}"}, "n", allowTags(65), false); !errors.Is(err, errTemplateExpansionLimit) {
		t.Fatalf("65x65: %v", err)
	}
	if _, err := expandList([]string{"{{tag(allow)}}.{{tag(allow)}}"}, "n", allowTags(64), false); err != nil {
		t.Fatalf("64x64: %v", err)
	}
}

// Every template carries the policy responder's subject as a fixed publish line, and Check refuses a template
// without it.
func TestTemplateHasPolicyGrant(t *testing.T) {
	c, err := Compile(at001())
	if err != nil {
		t.Fatal(err)
	}
	want := PolicySubject(c.Range)
	if want != "agents.policy."+c.Range {
		t.Fatalf("PolicySubject = %q", want)
	}
	for _, tpl := range c.Templates {
		if !slices.Contains(tpl.Permissions.Pub.Allow, want) {
			t.Errorf("template %s lacks publish line %q", tpl.Group, want)
		}
		if slices.Contains(tpl.Permissions.Sub.Allow, want) {
			t.Errorf("template %s subscribes to %q", tpl.Group, want)
		}
	}
	for i := range c.Templates {
		var kept []string
		for _, l := range c.Templates[i].Permissions.Pub.Allow {
			if l != want {
				kept = append(kept, l)
			}
		}
		c.Templates[i].Permissions.Pub.Allow = kept
	}
	err = Check(at001(), c)
	if err == nil || !strings.Contains(err.Error(), "policy responder") {
		t.Fatalf("Check without the policy grant = %v", err)
	}
}
