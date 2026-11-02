package policy

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/admin"
)

// subjectMatches evaluates a NATS subject pattern against a literal subject: "*" matches one token, ">" (last
// token only) one or more.
func subjectMatches(pattern, subject string) bool {
	p, s := strings.Split(pattern, "."), strings.Split(subject, ".")
	for i, tok := range p {
		if tok == ">" && i == len(p)-1 {
			return len(s) > i
		}
		if i >= len(s) {
			return false
		}
		if tok != "*" && tok != s[i] {
			return false
		}
	}
	return len(p) == len(s)
}

// canPublish is NATS's rule: some allow matches and no deny does.
func canPublish(p jwt.Permissions, subject string) bool {
	for _, d := range p.Pub.Deny {
		if subjectMatches(d, subject) {
			return false
		}
	}
	for _, a := range p.Pub.Allow {
		if subjectMatches(a, subject) {
			return true
		}
	}
	return false
}

func TestSubjectMatches(t *testing.T) {
	for _, c := range []struct {
		pat, subj string
		want      bool
	}{
		{"a.b", "a.b", true}, {"a.*", "a.b", true}, {"a.*", "a.b.c", false}, {"a.*", "a", false},
		{"a.>", "a.b.c", true}, {"a.>", "a", false}, {"*.b", "a.b", true}, {"a.*.c", "a.b.c", true},
		{"a.*.c", "a.b.d", false}, {">", "x.y", true}, {"a.b", "a.c", false},
	} {
		if got := subjectMatches(c.pat, c.subj); got != c.want {
			t.Errorf("%q vs %q: %v", c.pat, c.subj, got)
		}
	}
}

// TestEffectiveMatchesRecipients: for random files, the set of (mail) recipient set the expanded permissions of a
// sender let it publish to, over every declared endpoint, equals its Recipients: never wider, never narrower. The
// files mix zone-wide rules with rules on other labels, so every encoding (wildcard, allow tags, deny tags) occurs.
func TestEffectiveMatchesRecipients(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	zones := []string{"red", "blue", "green"}
	vals := []string{"a", "b"}
	randSel := func() string {
		var parts []string
		if rng.Intn(2) == 0 {
			parts = append(parts, "zone: "+zones[rng.Intn(len(zones))])
		}
		for _, k := range []string{"k1", "k2"} {
			if rng.Intn(4) == 0 {
				parts = append(parts, k+": "+vals[rng.Intn(len(vals))])
			}
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	var sawWildcard, sawAllow, sawDeny, sawStar bool
	for iter := 0; iter < 400; iter++ {
		var b strings.Builder
		if rng.Intn(2) == 0 {
			b.WriteString("default: deny\n")
		}
		n := 2 + rng.Intn(9)
		b.WriteString("endpoints:\n")
		for i := 0; i < n; i++ {
			var parts []string
			if rng.Intn(5) != 0 { // some endpoints are in zone default
				parts = append(parts, "zone: "+zones[rng.Intn(len(zones))])
			}
			for _, k := range []string{"k1", "k2"} {
				if rng.Intn(2) == 0 {
					parts = append(parts, k+": "+vals[rng.Intn(len(vals))])
				}
			}
			fmt.Fprintf(&b, "  e%d: {labels: {%s}}\n", i, strings.Join(parts, ", "))
		}
		b.WriteString("rules:\n")
		for i, nr := 0, rng.Intn(8); i < nr; i++ {
			act := "allow"
			if rng.Intn(3) == 0 {
				act = "deny"
			}
			fmt.Fprintf(&b, "  - {name: r%d, action: %s, from: %s, to: %s}\n", i, act, randSel(), randSel())
		}
		f, err := Parse([]byte(b.String()))
		if err != nil {
			t.Fatalf("%v\n%s", err, b.String())
		}
		c, err := Compile(f, "o")
		if err != nil {
			t.Fatal(err)
		}
		for _, z := range c.Zones {
			sawWildcard = sawWildcard || len(z.Wildcards) > 0
		}
		for _, from := range c.Endpoints {
			for _, tag := range from.Tags {
				sawAllow = sawAllow || strings.HasPrefix(tag, "allow:")
				sawDeny = sawDeny || strings.HasPrefix(tag, "deny:") && tag != admin.NoDenyTag
				sawStar = sawStar || strings.HasSuffix(tag, ".*")
			}
			perms, err := c.Effective(from.Name)
			if err != nil {
				t.Fatalf("%v\n%s", err, b.String())
			}
			if len(perms.Pub.Allow) == 0 || len(perms.Sub.Allow) == 0 {
				t.Fatalf("%s: empty permission block", from.Name)
			}
			for _, to := range c.Endpoints {
				in := slices.Contains(from.Recipients, to.Name)
				x, err := Explain(f, from.Name, to.Name)
				if err != nil || x.Allowed != in {
					t.Fatalf("%s -> %s: recipients %v, explain %v %v", from.Name, to.Name, in, x.Allowed, err)
				}
				// Mail is the one policed verb: the six-token subject is publishable exactly for the recipients.
				subj := strings.Replace(MailSubjectPattern("o", to.Zone, to.Name), "*", "cc", 1)
				if got := canPublish(perms, subj); got != in {
					t.Fatalf("%s -> %s mail: effective permissions say %v, recipients say %v\ntags %v\nzone key %+v\n%s",
						from.Name, to.Name, got, in, from.Tags, zoneOf(t, c, from.Zone).Perms.Pub, b.String())
				}
				// Prompt and status are open: five tokens, always publishable, whatever the rules say.
				for _, verb := range []string{"prompt", "status"} {
					if open := "agents." + verb + ".cc.o." + to.Name; !canPublish(perms, open) {
						t.Fatalf("%s may not publish %s (prompt and status are open)\n%s", from.Name, open, b.String())
					}
				}
			}
			// Nothing leaks to another owner or to the heartbeat of a peer.
			for _, subj := range []string{"agents.mail.cc.other." + from.Zone + ".x", "agents.hb.cc.o.not-" + from.Name} {
				if canPublish(perms, subj) {
					t.Fatalf("%s may publish %s", from.Name, subj)
				}
			}
			// Its own prompt and status subscriptions are the five-token SAP subjects, no zone token.
			for _, verb := range []string{"prompt", "status"} {
				want := "agents." + verb + ".cc.o." + from.Name
				if !slices.ContainsFunc(perms.Sub.Allow, func(s string) bool { return s == want }) {
					t.Fatalf("%s: sub allow lacks %s: %v", from.Name, want, perms.Sub.Allow)
				}
				if slices.ContainsFunc(perms.Sub.Allow, func(s string) bool { return strings.HasPrefix(s, "agents."+verb+".") && strings.Count(s, ".") != 4 }) {
					t.Fatalf("%s: a non-five-token %s subscription: %v", from.Name, verb, perms.Sub.Allow)
				}
			}
			if !canPublish(perms, "agents.hb.cc.o."+from.Name) {
				t.Fatalf("%s may not publish its own heartbeat", from.Name)
			}
		}
	}
	if !sawWildcard || !sawAllow || !sawDeny || !sawStar {
		t.Fatalf("the random files did not cover every encoding: wildcard %v allow %v deny %v star %v", sawWildcard, sawAllow, sawDeny, sawStar)
	}
}

func zoneOf(t *testing.T, c *Compiled, name string) CompiledZone {
	t.Helper()
	z, ok := c.zone(name)
	if !ok {
		t.Fatalf("no zone %s", name)
	}
	return z
}

func TestEffectiveTemplates(t *testing.T) {
	f := mustParse(t, `
default: deny
endpoints:
  a1: {labels: {zone: red, team: x}}
  a2: {labels: {zone: red}}
  b1: {labels: {zone: blue}}
  b2: {labels: {zone: blue, tier: secret}}
  b3: {labels: {zone: blue}}
rules:
  - {name: r, action: allow, from: {zone: red}, to: {zone: blue}}
  - {name: d, action: deny, from: {team: x}, to: {tier: secret}}
`)
	c, err := Compile(f, "o")
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Effective("a1")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"agents.hb.cc.o.a1", "$KV.mash-leases.a1", "agents.mail.*.o.blue.*", "agents.prompt.*.o.*", "agents.status.*.o.*"} {
		if !slices.Contains(p.Pub.Allow, w) {
			t.Errorf("a1 pub allow lacks %s: %v", w, p.Pub.Allow)
		}
	}
	for _, w := range []string{"agents.mail.*.o.blue.b2"} {
		if !slices.Contains(p.Pub.Deny, w) {
			t.Errorf("a1 pub deny lacks %s: %v", w, p.Pub.Deny)
		}
	}
	for _, w := range []string{"_INBOX.a1.>", "agents.prompt.cc.o.a1", "agents.status.cc.o.a1"} {
		if !slices.Contains(p.Sub.Allow, w) {
			t.Errorf("a1 sub allow lacks %s: %v", w, p.Sub.Allow)
		}
	}
	for _, l := range append(append([]string{}, p.Pub.Allow...), append(p.Pub.Deny, p.Sub.Allow...)...) {
		if strings.Contains(l, "{{") {
			t.Errorf("unexpanded template %s", l)
		}
	}
	// The allow template with no allow tag drops its lines.
	for _, l := range p.Pub.Allow {
		if strings.Contains(l, "{{") || l == "agents.mail.*.o." {
			t.Errorf("bad line %q", l)
		}
	}
	if _, err := c.Effective("nobody"); err == nil {
		t.Error("unknown endpoint accepted")
	}
	// nats-server refuses a user of a zone with deny templates that has no deny tag; so does Effective.
	for i := range c.Endpoints {
		if c.Endpoints[i].Name == "a2" {
			c.Endpoints[i].Tags = nil
		}
	}
	if _, err := c.Effective("a2"); err == nil || !strings.Contains(err.Error(), "refuses the user") {
		t.Errorf("tagless user of a deny zone: %v", err)
	}
}
