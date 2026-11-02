package policy

import (
	"fmt"
	"maps"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
)

func mustParse(t *testing.T, src string) *File {
	t.Helper()
	f, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, src)
	}
	return f
}

func TestParseErrors(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"unknown top-level field", "defualt: deny\n", "line 1"},
		{"unknown rule field", "endpoints:\n  a: {}\nrules:\n  - name: r\n    action: allow\n    from: {}\n    to: {}\n    frm: {}\n", "line 8"},
		{"duplicate top key", "default: allow\ndefault: deny\n", "line 2: duplicate key \"default\""},
		{"duplicate endpoint", "endpoints:\n  a: {}\n  a: {}\n", "duplicate key \"a\""},
		{"duplicate label", "endpoints:\n  a:\n    labels: {k: v, k: w}\n", "duplicate key \"k\""},
		{"bad action", "endpoints:\n  a: {}\nrules:\n  - {name: r, action: permit, from: {}, to: {}}\n", `action "permit"`},
		{"bad default", "default: maybe\n", `"maybe" is not allow or deny`},
		{"bad endpoint name", "endpoints:\n  Web_Intake: {}\n", "endpoint Web_Intake"},
		{"bad label key", "endpoints:\n  a:\n    labels: {Team: x}\n", "label key"},
		{"empty label value", "endpoints:\n  a:\n    labels: {team: \"\"}\n", "empty value"},
		{"too many labels", "endpoints:\n  a:\n    labels: {a: 1, b: 1, c: 1, d: 1, e: 1, f: 1, g: 1, h: 1, i: 1}\n", "9 labels"},
		{"empty rule name", "rules:\n  - {name: \"\", action: allow, from: {}, to: {}}\n", "name must be non-empty"},
		{"duplicate rule name", "rules:\n  - {name: r, action: allow, from: {}, to: {}}\n  - {name: r, action: deny, from: {}, to: {}}\n", "already used by rule 1"},
		{"missing from", "rules:\n  - {name: r, action: allow, to: {}}\n", "from is missing"},
		{"null to", "rules:\n  - {name: r, action: allow, from: {}, to: }\n", "to is missing"},
		{"test undeclared endpoint", "endpoints:\n  a: {}\ntests:\n  - {from: a, to: ghost, expect: allow}\n", `"ghost" is not a declared endpoint`},
		{"test bad expect", "endpoints:\n  a: {}\ntests:\n  - {from: a, to: a, expect: yes}\n", "expect"},
		{"root is a list", "- a\n- b\n", "must be a mapping"},
		{"root is a scalar", "just text\n", "must be a mapping"},
		{"empty file", "", "empty file"},
		{"two documents", "default: allow\n---\ndefault: deny\n", "more than one"},
		{"bad yaml", "default: [\n", "policy:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.src))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error containing %q", err, c.want)
			}
		})
	}
}

func TestParseDefaults(t *testing.T) {
	f := mustParse(t, "endpoints:\n  a: {}\n")
	if f.Default != "allow" {
		t.Fatalf("default %q", f.Default)
	}
	// Label values like yes and 1 are strings.
	f = mustParse(t, "endpoints:\n  a:\n    labels: {internet: yes, tier: 1}\nrules:\n  - {name: r, action: deny, from: {internet: yes}, to: {}}\n")
	if f.Endpoints["a"].Labels["internet"] != "yes" || f.Endpoints["a"].Labels["tier"] != "1" {
		t.Fatalf("labels %v", f.Endpoints["a"].Labels)
	}
	if f.Rules[0].To == nil || len(f.Rules[0].To) != 0 {
		t.Fatalf("{} must parse to an empty non-nil selector")
	}
}

const trio = `
endpoints:
  a: {labels: {team: x, tier: "1"}}
  b: {labels: {team: x}}
  c: {labels: {team: y}}
`

func recipients(t *testing.T, f *File) map[string][]string {
	t.Helper()
	c, err := Compile(f, "o")
	if err != nil {
		t.Fatal(err)
	}
	m := map[string][]string{}
	for _, e := range c.Endpoints {
		m[e.Name] = e.Recipients
	}
	return m
}

func TestSelectors(t *testing.T) {
	// AND: only a has both labels. {} matches everyone. A missing label never matches.
	f := mustParse(t, "default: deny\n"+trio+`
rules:
  - {name: both, action: allow, from: {team: x, tier: "1"}, to: {team: y}}
  - {name: everyone, action: allow, from: {team: y}, to: {}}
  - {name: nolabel, action: allow, from: {nosuch: v}, to: {}}
`)
	got := recipients(t, f)
	want := map[string][]string{"a": {"c"}, "b": {}, "c": {"a", "b", "c"}}
	for k, w := range want {
		if !slices.Equal(got[k], w) {
			t.Errorf("%s: got %v want %v", k, got[k], w)
		}
	}
	r := f.Check()
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0].Message, "{nosuch=v} matches no endpoint") {
		t.Errorf("warnings %+v", r.Warnings)
	}
	// An endpoint with a label of the right key but another value does not match.
	if (Selector{"team": "x"}).matches(map[string]string{"team": "y"}) {
		t.Error("value mismatch matched")
	}
}

func TestDenyBeatsAllowAndDefault(t *testing.T) {
	f := mustParse(t, trio+`
rules:
  - {name: allow-all, action: allow, from: {}, to: {}}
  - {name: no-y, action: deny, from: {}, to: {team: y}}
`)
	got := recipients(t, f)
	if !slices.Equal(got["a"], []string{"a", "b"}) || !slices.Equal(got["c"], []string{"a", "b"}) {
		t.Fatalf("%v", got)
	}
	// Default allow with no rules: everyone, self included. Default deny: nobody.
	if g := recipients(t, mustParse(t, trio)); !slices.Equal(g["a"], []string{"a", "b", "c"}) {
		t.Fatalf("default allow: %v", g)
	}
	if g := recipients(t, mustParse(t, "default: deny\n"+trio)); len(g["a"]) != 0 || g["a"] == nil {
		t.Fatalf("default deny: %#v", g["a"])
	}
	// The default applies only where no rule matched.
	f = mustParse(t, "default: allow\n"+trio+"rules:\n  - {name: r, action: allow, from: {team: x}, to: {team: x}}\n")
	if g := recipients(t, f); !slices.Equal(g["a"], []string{"a", "b", "c"}) {
		t.Fatalf("an allow rule must not turn the default off: %v", g)
	}
}

func TestSelfMail(t *testing.T) {
	f := mustParse(t, "default: deny\n"+trio+"rules:\n  - {name: all, action: allow, from: {}, to: {}}\n")
	if g := recipients(t, f); !slices.Contains(g["a"], "a") {
		t.Fatalf("from {} to {} allow must include self: %v", g)
	}
	f = mustParse(t, "default: deny\n"+trio+"rules:\n  - {name: x-to-y, action: allow, from: {team: x}, to: {team: y}}\n")
	if g := recipients(t, f); slices.Contains(g["a"], "a") {
		t.Fatalf("no rule covers self: %v", g)
	}
	f = mustParse(t, trio+"rules:\n  - {name: no-self-for-a, action: deny, from: {tier: \"1\"}, to: {tier: \"1\"}}\n")
	if g := recipients(t, f); slices.Contains(g["a"], "a") || !slices.Contains(g["a"], "b") {
		t.Fatalf("a deny covering only the pair a->a: %v", g)
	}
}

func TestRuleOrderIrrelevant(t *testing.T) {
	f := mustParse(t, "default: deny\n"+trio+`
rules:
  - {name: r1, action: allow, from: {team: x}, to: {}}
  - {name: r2, action: deny, from: {}, to: {team: y}}
  - {name: r3, action: allow, from: {team: y}, to: {team: x}}
  - {name: r4, action: deny, from: {tier: "1"}, to: {team: x}}
`)
	base, _ := Compile(f, "o")
	baseJSON, _ := base.JSON()
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20; i++ {
		g := *f
		g.Rules = slices.Clone(f.Rules)
		rng.Shuffle(len(g.Rules), func(i, j int) { g.Rules[i], g.Rules[j] = g.Rules[j], g.Rules[i] })
		c, err := Compile(&g, "o")
		if err != nil {
			t.Fatal(err)
		}
		if j, _ := c.JSON(); string(j) != string(baseJSON) {
			t.Fatalf("order changed the result:\n%s\n%s", j, baseJSON)
		}
		for _, a := range []string{"a", "b", "c"} {
			for _, b := range []string{"a", "b", "c"} {
				x1, _ := Explain(f, a, b)
				x2, _ := Explain(&g, a, b)
				if x1.Text != x2.Text {
					t.Fatalf("explain differs: %q vs %q", x1.Text, x2.Text)
				}
			}
		}
	}
}

func TestCompileOwnerAndNoRecipients(t *testing.T) {
	f := mustParse(t, "default: deny\n"+trio)
	for _, bad := range []string{"", "Acme", "a_b", "-a", strings.Repeat("a", 49)} {
		if _, err := Compile(f, bad); err == nil {
			t.Errorf("owner %q accepted", bad)
		}
	}
	c, err := Compile(f, "o")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Zones) != 1 || c.Zones[0].Name != DefaultZone || len(c.Zones[0].Wildcards) != 0 {
		t.Fatalf("zones %+v", c.Zones)
	}
	z := c.Zones[0]
	if len(z.Perms.Pub.Allow) == 0 || len(z.Perms.Sub.Allow) == 0 {
		t.Fatal("empty permission block")
	}
	for _, s := range z.Perms.Pub.Allow {
		if strings.HasPrefix(s, "agents.mail") && !strings.Contains(s, "{{tag(allow)}}") {
			t.Fatalf("zone key may still address peers: %s", s)
		}
	}
	for _, e := range c.Endpoints {
		if len(e.Recipients) != 0 || e.Recipients == nil || !slices.Equal(e.Tags, []string{admin.NoDenyTag}) {
			t.Fatalf("%s recipients %#v tags %#v", e.Name, e.Recipients, e.Tags)
		}
		p, err := c.Effective(e.Name)
		if err != nil || len(p.Pub.Allow) == 0 || len(p.Sub.Allow) == 0 {
			t.Fatalf("%s: %+v %v", e.Name, p, err)
		}
		for _, s := range p.Pub.Allow {
			// Mail is policed; the two open lines (prompt, status; ADR 0054) are fixed for every endpoint.
			if strings.HasPrefix(s, "agents.mail") {
				t.Fatalf("%s may still mail peers: %s", e.Name, s)
			}
		}
	}
}

func TestZonesAndTags(t *testing.T) {
	const head = `
default: deny
endpoints:
  a1: {labels: {zone: red, team: x}}
  a2: {labels: {zone: red}}
  b1: {labels: {zone: blue}}
  b2: {labels: {zone: blue, tier: secret}}
  b3: {labels: {zone: blue}}
  n1: {}
`
	tags := func(c *Compiled) map[string]string {
		m := map[string]string{}
		for _, e := range c.Endpoints {
			m[e.Name] = e.Zone + " " + strings.Join(e.Tags, ",")
		}
		return m
	}
	compile := func(rules string) *Compiled {
		t.Helper()
		c, err := Compile(mustParse(t, head+"rules:\n"+rules), "o")
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	zoneKey := func(c *Compiled, name string) CompiledZone {
		for _, z := range c.Zones {
			if z.Name == name {
				return z
			}
		}
		t.Fatalf("no zone %s", name)
		return CompiledZone{}
	}

	// One rule between zones: one wildcard, no tags; the endpoint without the label is zone default.
	c := compile("  - {name: r, action: allow, from: {zone: red}, to: {zone: blue}}\n")
	if got := len(c.Zones); got != 3 || zoneKey(c, "default").Name != "default" {
		t.Fatalf("zones %+v", c.Zones)
	}
	if w := zoneKey(c, "red").Wildcards; !slices.Equal(w, []string{"blue"}) {
		t.Fatalf("wildcards %v", w)
	}
	for n, got := range tags(c) {
		if strings.Contains(got, ":") && !strings.HasSuffix(got, " "+admin.NoDenyTag) {
			t.Errorf("%s has tags beyond the placeholder: %s", n, got)
		}
	}
	if !slices.Contains(zoneKey(c, "red").Perms.Pub.Allow, "agents.mail.*.o.blue.*") || len(zoneKey(c, "red").Perms.Pub.Deny) != 1 {
		t.Errorf("red key: %+v", zoneKey(c, "red").Perms)
	}

	// A deny for one pair in a zone of three: the wildcard plus one deny tag (cheaper than allow tags), and a2, who
	// has no real deny tag, carries only the placeholder; every user carries it.
	c = compile("  - {name: r, action: allow, from: {zone: red}, to: {zone: blue}}\n  - {name: d, action: deny, from: {team: x}, to: {tier: secret}}\n")
	want := map[string]string{
		"a1": "red deny:_none,deny:blue.b2", "a2": "red deny:_none", "b1": "blue deny:_none", "b2": "blue deny:_none", "b3": "blue deny:_none", "n1": "default deny:_none",
	}
	if got := tags(c); !maps.Equal(got, want) {
		t.Errorf("tags %v want %v", got, want)
	}
	if len(zoneKey(c, "red").Perms.Pub.Deny) != 1 || len(zoneKey(c, "blue").Perms.Pub.Deny) != 1 {
		t.Errorf("deny templates: red %v blue %v", zoneKey(c, "red").Perms.Pub.Deny, zoneKey(c, "blue").Perms.Pub.Deny)
	}

	// Mostly denied: allow tags, and allow:blue.* for a sender who may mail the whole zone.
	c = compile("  - {name: r, action: allow, from: {team: x}, to: {zone: blue}}\n  - {name: r2, action: allow, from: {zone: red}, to: {tier: secret}}\n")
	want = map[string]string{
		"a1": "red allow:blue.*,deny:_none", "a2": "red allow:blue.b2,deny:_none", "b1": "blue deny:_none", "b2": "blue deny:_none", "b3": "blue deny:_none", "n1": "default deny:_none",
	}
	if got := tags(c); !maps.Equal(got, want) {
		t.Errorf("tags %v want %v", got, want)
	}
	if len(zoneKey(c, "red").Wildcards) != 0 {
		t.Errorf("red key: %+v", zoneKey(c, "red"))
	}

	// A tie goes to the allow encoding: a1 is allowed b1 only, a2 b2 only, in a zone of two (2 tags either way).
	f := mustParse(t, `
default: deny
endpoints:
  a1: {labels: {zone: red, team: x}}
  a2: {labels: {zone: red, team: y}}
  b1: {labels: {zone: blue, tier: low}}
  b2: {labels: {zone: blue, tier: secret}}
rules:
  - {name: r, action: allow, from: {zone: red}, to: {zone: blue}}
  - {name: d, action: deny, from: {team: x}, to: {tier: secret}}
  - {name: d2, action: deny, from: {team: y}, to: {tier: low}}
`)
	c, err := Compile(f, "o")
	if err != nil {
		t.Fatal(err)
	}
	if got := tags(c)["a1"] + "|" + tags(c)["a2"]; got != "red allow:blue.b1,deny:_none|red allow:blue.b2,deny:_none" {
		t.Errorf("tie: %s", got)
	}
}

func TestSubjectLabel(t *testing.T) {
	f := mustParse(t, `
subject_label: site
endpoints:
  a: {labels: {site: paris, zone: ignored}}
  b: {}
`)
	c, err := Compile(f, "o")
	if err != nil {
		t.Fatal(err)
	}
	if c.SubjectLabel != "site" || c.Endpoints[0].Zone != "paris" || c.Endpoints[1].Zone != "default" {
		t.Fatalf("%+v", c)
	}
	if mustParse(t, "endpoints:\n  a: {}\n").SubjectLabel != "zone" {
		t.Error("default subject_label is not zone")
	}
	for name, src := range map[string]string{
		"zone value with a dot":    "endpoints:\n  a: {labels: {zone: \"a.b\"}}\n",
		"zone value with capitals": "endpoints:\n  a: {labels: {zone: Paris}}\n",
		"zone value with wildcard": "endpoints:\n  a: {labels: {zone: \"*\"}}\n",
		"bad subject_label":        "subject_label: Zone\nendpoints:\n  a: {}\n",
		"custom label bad value":   "subject_label: site\nendpoints:\n  a: {labels: {site: \"x y\"}}\n",
	} {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// A value that is a bad zone under another subject_label is fine.
	if _, err := Parse([]byte("subject_label: site\nendpoints:\n  a: {labels: {zone: \"A B\"}}\n")); err != nil {
		t.Errorf("zone label is not the subject label here: %v", err)
	}
}

func TestCheckZoneWarnings(t *testing.T) {
	f := mustParse(t, "endpoints:\n  a: {labels: {zone: red}}\nrules:\n  - {name: r, action: allow, from: {zone: red}, to: {zone: ghost}}\n")
	var got []string
	for _, w := range f.Check().Warnings {
		got = append(got, w.String())
	}
	if len(got) != 1 || !strings.Contains(got[0], "to selector {zone=ghost} matches no endpoint (zone ghost has no endpoints)") {
		t.Errorf("%q", got)
	}

	// An endpoint told apart from half of 400 peers needs 200 tags, more than the connect line holds.
	var b strings.Builder
	b.WriteString("default: deny\nendpoints:\n  hub: {labels: {team: hub}}\n")
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&b, "  p%03d: {labels: {zone: other, team: peer, parity: \"%d\"}}\n", i, i%2)
	}
	b.WriteString("rules:\n  - {name: r, action: allow, from: {team: hub}, to: {parity: \"0\"}}\n")
	f = mustParse(t, b.String())
	var warn []string
	for _, w := range f.Check().Warnings {
		warn = append(warn, w.String())
	}
	if len(warn) == 0 || !strings.Contains(warn[0], "endpoint hub") || !strings.Contains(warn[0], "201 tags") {
		t.Errorf("%q", warn[:min(len(warn), 3)])
	}
}

func TestExplainTexts(t *testing.T) {
	f := mustParse(t, `
default: deny
endpoints:
  web-intake: {labels: {internet: "yes", zone: dmz}}
  billing-agent: {labels: {team: finance}}
  ledger-agent: {labels: {team: finance}}
rules:
  - {name: nothing-internet-facing-reaches-finance, action: deny, from: {internet: "yes"}, to: {team: finance}}
  - {name: also-no-dmz-to-finance, action: deny, from: {zone: dmz}, to: {team: finance}}
  - {name: dmz-anything, action: allow, from: {zone: dmz}, to: {}}
  - {name: finance-talks, action: allow, from: {team: finance}, to: {team: finance}}
`)
	check := func(from, to string, allowed bool, rule, via, text string) {
		t.Helper()
		x, err := Explain(f, from, to)
		if err != nil {
			t.Fatal(err)
		}
		if x.Allowed != allowed || x.Rule != rule || x.Via != via || x.Text != text {
			t.Errorf("%s -> %s:\n got %+v\nwant allowed=%v rule=%q via=%q\n     %s", from, to, x, allowed, rule, via, text)
		}
	}
	check("web-intake", "billing-agent", false, "also-no-dmz-to-finance", "deny rule",
		"web-intake cannot mail billing-agent: rule also-no-dmz-to-finance (deny), selected by from {zone=dmz} and to {team=finance}; also deny by nothing-internet-facing-reaches-finance; allow rules also matched: dmz-anything (a deny beats any allow)")
	check("billing-agent", "ledger-agent", true, "finance-talks", "allow rule",
		"billing-agent can mail ledger-agent: rule finance-talks (allow), selected by from {team=finance} and to {team=finance}")
	check("billing-agent", "web-intake", false, "", "default", "billing-agent cannot mail web-intake: no rule matched; default deny")
	if _, err := Explain(f, "web-intake", "ghost"); err == nil {
		t.Error("unknown endpoint accepted")
	}
	g := mustParse(t, "endpoints:\n  a: {}\n  b: {}\n")
	if x, _ := Explain(g, "a", "b"); x.Text != "a can mail b: no rule matched; default allow" || !x.Allowed {
		t.Errorf("%+v", x)
	}
}

func TestRunTests(t *testing.T) {
	if r := RunTests(mustParse(t, trio)); r != nil {
		t.Fatalf("no tests, got %v", r)
	}
	f := mustParse(t, trio+`
rules:
  - {name: r, action: deny, from: {team: x}, to: {team: y}}
tests:
  - {from: a, to: c, expect: deny}
  - {from: a, to: c, expect: allow}
  - {from: c, to: a, expect: allow}
`)
	r := RunTests(f)
	if len(r) != 3 || !r[0].Pass || r[1].Pass || !r[2].Pass {
		t.Fatalf("%+v", r)
	}
	if r[1].Got != "deny" || r[1].Rule != "r" || !strings.Contains(r[1].Text, "a cannot mail c") {
		t.Fatalf("%+v", r[1])
	}
}

func TestCheckWarnings(t *testing.T) {
	f := mustParse(t, "default: deny\n"+trio+`
rules:
  - {name: x-to-x, action: allow, from: {team: x}, to: {team: x}}
  - {name: typo, action: deny, from: {team: xx}, to: {}}
`)
	r := f.Check()
	if len(r.Errors) != 0 {
		t.Fatal(r.Errors)
	}
	var got []string
	for _, w := range r.Warnings {
		got = append(got, w.String())
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "rule typo") || !strings.Contains(joined, "endpoint c (line 6): default is deny") || strings.Contains(joined, "endpoint a") {
		t.Errorf("warnings:\n%s", joined)
	}
	// Under default allow nothing is stranded, and unused labels are fine.
	r = mustParse(t, trio).Check()
	if len(r.Warnings) != 0 || len(r.Errors) != 0 {
		t.Errorf("%+v", r)
	}
}

func TestMatrixAndJSON(t *testing.T) {
	f := mustParse(t, "default: deny\n"+trio+"rules:\n  - {name: r, action: allow, from: {team: x}, to: {team: y}}\n")
	c, _ := Compile(f, "o")
	want := "from \\ to  a  b  c\na          -  -  +\nb          -  -  +\nc          -  -  -\n\n+ may mail; - may not\n"
	if got := c.Matrix(); got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
	js, err := c.JSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{`"owner": "o"`, `"default": "deny"`, `"may_mail_nobody": true`, `"recipients": []`} {
		if !strings.Contains(string(js), w) {
			t.Errorf("json lacks %s:\n%s", w, js)
		}
	}
	if strings.Contains(string(js), "agents.") {
		t.Error("json leaks permission subjects")
	}
}

func TestBudget60AllToAll(t *testing.T) {
	var b strings.Builder
	b.WriteString("endpoints:\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, "  agent-%02d: {labels: {fleet: all}}\n", i)
	}
	f := mustParse(t, b.String())
	c, err := Compile(f, "acme")
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Budget(c)
	if err != nil || !rep.OK || rep.Zones != 1 || rep.AccountLimit != 1048576 || rep.UserLimit != UserJWTLimit {
		t.Fatalf("%+v %v", rep, err)
	}
	t.Logf("60 endpoints all-to-all: account JWT %d bytes of %d, worst user JWT %d bytes (%s)", rep.AccountJWTBytes, rep.AccountLimit, rep.WorstUserJWTBytes, rep.WorstUser)
}

func TestBudgetOverLimit(t *testing.T) {
	f := mustParse(t, `
endpoints:
  small: {labels: {zone: z1}}
  big: {labels: {zone: z2}}
`)
	c, _ := Compile(f, "o")
	// Give zone z2 a much bigger template than z1.
	for i := range c.Zones {
		if c.Zones[i].Name == "z2" {
			for j := 0; j < 200; j++ {
				c.Zones[i].Perms.Pub.Allow.Add(fmt.Sprintf("agents.mail.*.o.zone-%03d.*", j))
			}
		}
	}
	rep, err := budget(c, 2000, UserJWTLimit)
	if err == nil || rep.OK || !strings.Contains(err.Error(), "biggest signing key is zone z2") {
		t.Fatalf("%+v %v", rep, err)
	}
	if rep2, err := budget(c, 1<<30, UserJWTLimit); err != nil || !rep2.OK {
		t.Fatalf("%+v %v", rep2, err)
	}
}

func TestBudgetUserOverLimit(t *testing.T) {
	f := mustParse(t, "endpoints:\n  small: {}\n  tagged: {}\n")
	c, _ := Compile(f, "o")
	for j := 0; j < 200; j++ {
		c.Endpoints[1].Tags = append(c.Endpoints[1].Tags, fmt.Sprintf("allow:default.peer-%03d", j))
	}
	rep, err := Budget(c)
	if err == nil || rep.OK || rep.WorstUser != "tagged" || !strings.Contains(err.Error(), "user JWT of tagged") || !strings.Contains(err.Error(), "201 tags") {
		t.Fatalf("%+v %v", rep, err)
	}
	if rep.AccountJWTBytes > rep.AccountLimit {
		t.Fatal("the account is fine")
	}
}

// TestBudgetScale1000: 1,000 endpoints in 20 zones with sparse rules (each zone may mail the next three) fit
// easily: the account JWT holds 20 keys whatever the number of endpoints.
func TestBudgetScale1000(t *testing.T) {
	var b strings.Builder
	b.WriteString("default: deny\nendpoints:\n")
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&b, "  ep%04d: {labels: {zone: zone%02d}}\n", i, i%20)
	}
	b.WriteString("rules:\n")
	for z := 0; z < 20; z++ {
		for k := 1; k <= 3; k++ {
			fmt.Fprintf(&b, "  - {name: z%02d-to-z%02d, action: allow, from: {zone: zone%02d}, to: {zone: zone%02d}}\n", z, (z+k)%20, z, (z+k)%20)
		}
	}
	f := mustParse(t, b.String())
	c, err := Compile(f, "acme")
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Budget(c)
	if err != nil || !rep.OK || rep.Zones != 20 || rep.AccountJWTBytes >= 100_000 {
		t.Fatalf("%+v %v", rep, err)
	}
	for _, e := range c.Endpoints {
		if !slices.Equal(e.Tags, []string{admin.NoDenyTag}) {
			t.Fatalf("%s has tags %v", e.Name, e.Tags)
		}
	}
	t.Logf("1,000 endpoints, 20 zones, sparse: account JWT %d bytes, worst user JWT %d bytes", rep.AccountJWTBytes, rep.WorstUserJWTBytes)
}

// TestBudget60ZonesAllToAll is the worst shape for the zone layout: every endpoint in a zone of its own and every
// zone granted to every other, so each key carries 60 wildcards (quadratic in zones, not endpoints).
func TestBudget60ZonesAllToAll(t *testing.T) {
	var b strings.Builder
	b.WriteString("endpoints:\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, "  agent-%02d: {labels: {zone: zone-%02d}}\n", i, i)
	}
	c, err := Compile(mustParse(t, b.String()), "acme")
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Budget(c)
	if err != nil || !rep.OK || rep.Zones != 60 {
		t.Fatalf("%+v %v", rep, err)
	}
	t.Logf("60 endpoints in 60 zones all-to-all: account JWT %d bytes of %d, worst user JWT %d bytes", rep.AccountJWTBytes, rep.AccountLimit, rep.WorstUserJWTBytes)
}

func TestCheckRefusesLogTrue(t *testing.T) {
	f, err := Parse([]byte("endpoints:\n  a: {}\nrules:\n  - name: noisy\n    action: deny\n    from: {}\n    to: {}\n    log: true\n"))
	if err == nil || !strings.Contains(err.Error(), "rule noisy (line 4): log is not implemented yet; remove it") {
		t.Fatalf("%v %v", f, err)
	}
	// log: false is the same as leaving it out
	mustParse(t, "endpoints:\n  a: {}\nrules:\n  - name: quiet\n    action: deny\n    from: {}\n    to: {}\n    log: false\n")
}

func TestCheckWarnsOnCaseVariantLabelValues(t *testing.T) {
	f := mustParse(t, "default: allow\nendpoints:\n  a: {labels: {team: Finance}}\n  b: {labels: {team: finance}}\n  c: {labels: {team: finance, tier: Gold}}\n  d: {labels: {tier: gold}}\n  e: {labels: {tier: gold}}\n")
	rep := f.Check()
	if len(rep.Errors) != 0 {
		t.Fatal(rep.Errors)
	}
	var got []string
	for _, w := range rep.Warnings {
		got = append(got, w.String())
	}
	if len(got) != 2 || !strings.HasPrefix(got[0], "label team: ") || !strings.Contains(got[0], `"Finance" on endpoint a (line 3)`) ||
		!strings.Contains(got[0], `"finance" on endpoint b (line 4)`) || !strings.HasPrefix(got[1], "label tier: ") {
		t.Fatalf("%q", got)
	}
	// the same spelling twice is not a variant
	if w := mustParse(t, "endpoints:\n  a: {labels: {team: x}}\n  b: {labels: {team: x}}\n").Check().Warnings; len(w) != 0 {
		t.Fatalf("%v", w)
	}
}
