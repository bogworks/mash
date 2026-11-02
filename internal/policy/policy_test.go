package policy

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
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
	for _, e := range c.Endpoints {
		if len(e.Recipients) != 0 || e.Recipients == nil {
			t.Fatalf("%s recipients %#v", e.Name, e.Recipients)
		}
		if len(e.Perms.Pub.Allow) == 0 || len(e.Perms.Sub.Allow) == 0 {
			t.Fatalf("%s has an empty permission block", e.Name)
		}
		for _, s := range e.Perms.Pub.Allow {
			if strings.HasPrefix(s, "agents.mail") || strings.HasPrefix(s, "agents.prompt") || strings.HasPrefix(s, "agents.status") {
				t.Fatalf("%s may still address peers: %s", e.Name, s)
			}
		}
	}
	// A recipient gets the three lines.
	f = mustParse(t, "default: deny\n"+trio+"rules:\n  - {name: r, action: allow, from: {team: y}, to: {team: x}}\n")
	c, _ = Compile(f, "o")
	var pub []string
	for _, e := range c.Endpoints {
		if e.Name == "c" {
			pub = e.Perms.Pub.Allow
		}
	}
	for _, w := range []string{"agents.mail.*.o.a", "agents.prompt.*.o.a", "agents.status.*.o.b"} {
		if !slices.Contains(pub, w) {
			t.Errorf("missing %s in %v", w, pub)
		}
	}
	if slices.Contains(pub, "agents.mail.*.o.*") {
		t.Error("wildcard line leaked")
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
	want := "from \\ to  a  b  c\na          -  -  +\nb          -  -  +\nc          -  -  -\n\n+ may mail, prompt and probe; - may not\n"
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
	if err != nil || !rep.OK || rep.Endpoints != 60 || rep.Limit != 1048576 {
		t.Fatalf("%+v %v", rep, err)
	}
	t.Logf("60 endpoints all-to-all: account JWT %d bytes of %d", rep.AccountJWTBytes, rep.Limit)
}

func TestBudgetOverLimit(t *testing.T) {
	f := mustParse(t, "endpoints:\n  small: {}\n  big: {}\n")
	c, _ := Compile(f, "o")
	c.Endpoints[0].Recipients = c.Endpoints[0].Recipients[:0]
	// Give "big" (index 0, sorted) a much larger permission set than "small".
	rec := make([]string, 200)
	for i := range rec {
		rec[i] = fmt.Sprintf("peer-%03d", i)
	}
	c.Endpoints[0].Perms = permsFor(rec)
	rep, err := budget(c, 2000)
	if err == nil || rep.OK || !strings.Contains(err.Error(), "biggest signing key is big") {
		t.Fatalf("%+v %v", rep, err)
	}
	if rep2, err := budget(c, 1<<30); err != nil || !rep2.OK {
		t.Fatalf("%+v %v", rep2, err)
	}
}

// TestCompileNeverWidens: for random files, a sender's compiled recipients are exactly what Explain allows, and the
// permission lines name no one else.
func TestCompileNeverWidens(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	vals := []string{"a", "b", "c"}
	keys := []string{"k1", "k2"}
	randSel := func() string {
		var parts []string
		for _, k := range keys {
			if rng.Intn(3) == 0 {
				parts = append(parts, k+": "+vals[rng.Intn(3)])
			}
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	for iter := 0; iter < 200; iter++ {
		var b strings.Builder
		if rng.Intn(2) == 0 {
			b.WriteString("default: deny\n")
		}
		n := 2 + rng.Intn(5)
		b.WriteString("endpoints:\n")
		for i := 0; i < n; i++ {
			var parts []string
			for _, k := range keys {
				if rng.Intn(2) == 0 {
					parts = append(parts, k+": "+vals[rng.Intn(3)])
				}
			}
			fmt.Fprintf(&b, "  e%d: {labels: {%s}}\n", i, strings.Join(parts, ", "))
		}
		b.WriteString("rules:\n")
		for i, nr := 0, rng.Intn(6); i < nr; i++ {
			act := "allow"
			if rng.Intn(2) == 0 {
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
		for _, e := range c.Endpoints {
			for _, to := range f.names() {
				x, err := Explain(f, e.Name, to)
				if err != nil {
					t.Fatal(err)
				}
				in := slices.Contains(e.Recipients, to)
				if in != x.Allowed {
					t.Fatalf("%s -> %s: compiled %v, explain %v\n%s", e.Name, to, in, x.Allowed, b.String())
				}
				line := "agents.mail.*.o." + to
				if slices.Contains(e.Perms.Pub.Allow, line) != in {
					t.Fatalf("%s -> %s: permission line disagrees with recipients", e.Name, to)
				}
			}
			if slices.Contains(e.Perms.Pub.Allow, "agents.mail.*.o.*") {
				t.Fatal("wildcard mail line in compiled perms")
			}
		}
	}
}
