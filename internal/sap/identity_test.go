package sap_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
)

func rep(c byte, n int) string { return strings.Repeat(string(c), n) }

func TestNewTokenVectors(t *testing.T) {
	valid := []string{"a", "acme", "ops1", "sap-go-sdk", rep('a', 32)}
	valid = append(valid, "a-1", "ops-2x", "a1-b2-c3")
	for _, tok := range valid {
		for pos := 0; pos < 3; pos++ {
			tk := []string{"x", "y", "z"}
			tk[pos] = tok
			if len(tk[0])+len(tk[1])+len(tk[2])+2 > 64 {
				continue
			}
			if _, err := sap.New(tk[0], tk[1], tk[2]); err != nil {
				t.Errorf("New with valid token %q at %d: %v", tok, pos, err)
			}
		}
	}
	invalid := []struct{ tok, rule string }{
		{"", "empty"},
		{"Lead", "lower-case"},
		{"9x", "start with a letter"},
		{"-lead", "start with a letter"},
		{"lead-", "end with a dash"},
		{"le--ad", "consecutive dashes"},
		{"le_ad", "character"},
		{"le.ad", "character"},
		{"le:ad", "character"},
		{"le/ad", "character"},
		{"le ad", "character"},
		{"léad", "character"},
		{"le*ad", "character"},
		{"le>ad", "character"},
		{"le\x01ad", "character"},
		{rep('a', 33), "too long"},
	}
	names := []string{"agent", "owner", "name"}
	for _, v := range invalid {
		for pos := 0; pos < 3; pos++ {
			tk := []string{"x", "y", "z"}
			tk[pos] = v.tok
			_, err := sap.New(tk[0], tk[1], tk[2])
			if err == nil {
				t.Errorf("New accepted %q at %s", v.tok, names[pos])
				continue
			}
			if !strings.Contains(err.Error(), names[pos]) || !strings.Contains(err.Error(), v.rule) {
				t.Errorf("New(%q at %s) error %q should name %q and %q", v.tok, names[pos], err, names[pos], v.rule)
			}
		}
	}
}

func TestNewRefusesLegacyAgentToken(t *testing.T) {
	_, err := sap.New("cc", "o", "n")
	if err == nil {
		t.Fatal("New(cc,o,n) accepted")
	}
	if !strings.Contains(err.Error(), "legacy") {
		t.Errorf("error %q should say cc is the legacy form's agent token", err)
	}
	for _, agent := range []string{"cc1", "acc", "a-cc", "cc-a"} {
		id, err := sap.New(agent, "o", "n")
		if err != nil {
			t.Errorf("New(%q): %v", agent, err)
			continue
		}
		if id.IsLegacy() || id.Flat() != agent+"_o_n" {
			t.Errorf("New(%q): legacy=%v flat=%q", agent, id.IsLegacy(), id.Flat())
		}
	}
}

func TestLegacyAndCCNewFormCannotShareSubject(t *testing.T) {
	leg, err := sap.Legacy("o", "n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sap.New("cc", "o", "n"); err == nil {
		t.Errorf("New(cc,o,n) accepted beside legacy %v with subjects %v", leg, leg.Subjects())
	}
}

func TestValidateRefusesCCNewForm(t *testing.T) {
	if err := (sap.EndpointIdentity{Agent: "cc", Owner: "o", Name: "n"}).Validate(); err == nil {
		t.Error("literal new-form identity with Agent cc is valid")
	}
}

func TestOverlaps(t *testing.T) {
	mk := func(a, o, n string) sap.EndpointIdentity {
		id, err := sap.New(a, o, n)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	leg, err := sap.Legacy("o", "n")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		a, b sap.EndpointIdentity
		want bool
	}{
		{"legacy vs new, same owner and name", leg, mk("x", "o", "n"), true},
		{"legacy vs new, other owner", leg, mk("x", "p", "n"), false},
		{"legacy vs new, other name", leg, mk("x", "o", "m"), false},
		{"identical new", mk("x", "o", "n"), mk("x", "o", "n"), true},
		{"identical legacy", leg, leg, true},
		{"new differing in agent", mk("x", "o", "n"), mk("y", "o", "n"), false},
		{"new differing in owner", mk("x", "o", "n"), mk("x", "p", "n"), false},
		{"new differing in name", mk("x", "o", "n"), mk("x", "o", "m"), false},
	}
	for _, c := range cases {
		if got := sap.Overlaps(c.a, c.b); got != c.want {
			t.Errorf("%s: Overlaps(a,b) = %v, want %v", c.name, got, c.want)
		}
		if got := sap.Overlaps(c.b, c.a); got != c.want {
			t.Errorf("%s: Overlaps(b,a) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTotalBound(t *testing.T) {
	if _, err := sap.New(rep('a', 20), rep('b', 21), rep('c', 21)); err != nil {
		t.Errorf("62 token characters: %v", err)
	}
	for _, c := range [][3]int{{21, 21, 21}, {32, 32, 32}, {32, 31, 1}} {
		_, err := sap.New(rep('a', c[0]), rep('b', c[1]), rep('c', c[2]))
		if err == nil {
			t.Errorf("%v accepted", c)
		} else if !strings.Contains(err.Error(), "64") {
			t.Errorf("%v error %q should name the 64 bound", c, err)
		}
	}
}

func TestFlatInjectiveExhaustive(t *testing.T) {
	set := []string{"a", "a-b", "b", "ab", "a1"}
	seen := map[string][3]string{}
	for _, a := range set {
		for _, o := range set {
			for _, n := range set {
				id, err := sap.New(a, o, n)
				if err != nil {
					t.Fatal(err)
				}
				f := id.Flat()
				if prev, dup := seen[f]; dup {
					t.Fatalf("%v and %v share Flat %q", prev, [3]string{a, o, n}, f)
				}
				seen[f] = [3]string{a, o, n}
				back, err := sap.FromFlat(f)
				if err != nil || back != id {
					t.Fatalf("FromFlat(%q) = %v, %v; want %v", f, back, err, id)
				}
				if err := back.Validate(); err != nil {
					t.Fatalf("FromFlat result %v fails Validate: %v", back, err)
				}
			}
		}
	}
}

func randToken(r *rand.Rand, max int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	const rest = "abcdefghijklmnopqrstuvwxyz0123456789"
	n := 1 + r.Intn(max)
	b := []byte{letters[r.Intn(len(letters))]}
	for len(b) < n {
		if b[len(b)-1] != '-' && len(b) < n-1 && r.Intn(5) == 0 {
			b = append(b, '-')
			continue
		}
		b = append(b, rest[r.Intn(len(rest))])
	}
	return string(b)
}

func TestFlatPropertySeeded(t *testing.T) {
	r := rand.New(rand.NewSource(20261110))
	seen := map[string]sap.EndpointIdentity{}
	made, over := 0, 0
	for made < 2000 {
		mx := 4 + r.Intn(29)
		a, o, n := randToken(r, mx), randToken(r, mx), randToken(r, mx)
		id, err := sap.New(a, o, n)
		if total := len(a) + len(o) + len(n) + 2; total > sap.MaxIdentityLen {
			if err == nil {
				t.Fatalf("New(%q,%q,%q) accepted %d characters", a, o, n, total)
			}
			over++
			continue
		}
		if err != nil {
			t.Fatalf("New(%q,%q,%q) refused a valid triple: %v", a, o, n, err)
		}
		if err := id.Validate(); err != nil {
			t.Fatalf("New result %v fails Validate: %v", id, err)
		}
		made++
		f := id.Flat()
		back, err := sap.FromFlat(f)
		if err != nil || back != id {
			t.Fatalf("FromFlat(Flat(%v)) = %v, %v", id, back, err)
		}
		if prev, dup := seen[f]; dup && prev != id {
			t.Fatalf("%v and %v share Flat %q", prev, id, f)
		}
		seen[f] = id
	}
}

func TestFromFlatRefusals(t *testing.T) {
	cases := []struct{ in, want string }{
		{"lead", "underscore"},
		{"a_b", "three"},
		{"cc_a", "three"},
		{"cc_o_n", "legacy"},
		{"a_b_c_d", "three"},
		{"a__c", "empty"},
		{"a_B_c", "lower-case"},
		{"a_" + rep('b', 33) + "_c", "too long"},
		{rep('a', 22) + "_" + rep('b', 21) + "_" + rep('c', 21), "64"},
	}
	for _, c := range cases {
		_, err := sap.FromFlat(c.in)
		if err == nil {
			t.Errorf("FromFlat(%q) accepted", c.in)
		} else if !strings.Contains(err.Error(), c.want) {
			t.Errorf("FromFlat(%q) error %q should mention %q", c.in, err, c.want)
		}
	}
}

func TestLegacy(t *testing.T) {
	o48, n48 := "o"+rep('a', 47), "n"+rep('b', 47)
	for _, c := range [][2]string{{"acme", "mash-core"}, {"o", "n"}, {o48, n48}, {"a1", "b-2-c"}} {
		id, err := sap.Legacy(c[0], c[1])
		if err != nil {
			t.Errorf("Legacy(%q,%q): %v", c[0], c[1], err)
			continue
		}
		if err := id.Validate(); err != nil {
			t.Errorf("Legacy(%q,%q) fails Validate: %v", c[0], c[1], err)
		}
		if !id.IsLegacy() || id.Agent != sap.AgentToken || id.Owner != c[0] || id.Name != c[1] {
			t.Errorf("Legacy fields: %+v", id)
		}
		if id.Subjects() != sap.For(c[0], c[1]) {
			t.Errorf("Subjects %v != For %v", id.Subjects(), sap.For(c[0], c[1]))
		}
		if id.MailFilter() != sap.MailFilter(c[0], c[1]) {
			t.Errorf("MailFilter %q", id.MailFilter())
		}
		if id.Flat() != c[1] || id.String() != c[1] {
			t.Errorf("Flat/String %q %q", id.Flat(), id.String())
		}
	}
	if _, err := sap.Legacy("o"+rep('a', 48), "n"); err == nil {
		t.Error("49-character owner accepted")
	}
	if _, err := sap.Legacy("o", "n"+rep('a', 48)); err == nil {
		t.Error("49-character name accepted")
	}
}

func TestLegacyMatchesConfigValidName(t *testing.T) {
	vectors := []string{"", "a", "A", "9a", "-a", "a-", "a--b", "a-b", "a_b", "a.b", "a b", "é", "a*", "a>", "a\x00",
		"a1", "a-1", "a-b-c", rep('a', 47), rep('a', 48), rep('a', 49), "x" + rep('9', 47), "x" + rep('9', 48),
		"sap-go-sdk", "UPPER", "a:b", "a/b"}
	for _, v := range vectors {
		want := config.ValidName(v)
		_, errO := sap.Legacy(v, "n")
		_, errN := sap.Legacy("o", v)
		if (errO == nil) != want || (errN == nil) != want {
			t.Errorf("%q: config.ValidName=%v, Legacy owner ok=%v name ok=%v", v, want, errO == nil, errN == nil)
		}
	}
}

func TestNewFormSubjects(t *testing.T) {
	id, err := sap.New("acme", "team1", "ops1")
	if err != nil {
		t.Fatal(err)
	}
	s := id.Subjects()
	for verb, got := range map[string]string{"prompt": s.Prompt, "status": s.Status, "hb": s.HB, "mail": s.Mail} {
		want := fmt.Sprintf("agents.%s.acme.team1.ops1", verb)
		if got != want {
			t.Errorf("%s = %q, want %q", verb, got, want)
		}
		if n := len(strings.Split(got, ".")); n != 5 {
			t.Errorf("%s has %d tokens", verb, n)
		}
	}
	if mf := id.MailFilter(); mf != "agents.mail.acme.team1.ops1" || strings.ContainsAny(mf, "*>") {
		t.Errorf("MailFilter = %q", mf)
	}
	if id.IsLegacy() || id.String() != "acme.team1.ops1" || id.Flat() != "acme_team1_ops1" {
		t.Errorf("String/Flat/IsLegacy: %q %q %v", id.String(), id.Flat(), id.IsLegacy())
	}
}

func TestValidateRejectsMisuse(t *testing.T) {
	if err := (sap.EndpointIdentity{}).Validate(); err == nil {
		t.Error("zero value is valid")
	}
	if err := (sap.EndpointIdentity{Agent: "cc", Owner: "c", Name: "d"}).Validate(); err == nil {
		t.Error("literal new-form identity with agent cc is valid")
	}
	if err := (sap.EndpointIdentity{Agent: "a_b", Owner: "c", Name: "d"}).Validate(); err == nil {
		t.Error("literal with underscore token is valid")
	}
	for _, id := range []sap.EndpointIdentity{
		{Agent: "A", Owner: "o", Name: "n"},
		{Agent: "a", Owner: "o", Name: rep('n', 33)},
		{Agent: rep('a', 21), Owner: rep('b', 21), Name: rep('c', 21)},
	} {
		if err := id.Validate(); err == nil {
			t.Errorf("literal %+v is valid", id)
		}
	}
	leg, err := sap.Legacy("acme", "mash-core")
	if err != nil {
		t.Fatal(err)
	}
	mut := leg
	mut.Agent = "other"
	if err := mut.Validate(); err == nil {
		t.Error("legacy identity with mutated Agent is valid")
	}
	mut = leg
	mut.Name = "Bad_Name"
	if err := mut.Validate(); err == nil {
		t.Error("legacy identity with mutated Name is valid")
	}
	if err := leg.Validate(); err != nil {
		t.Errorf("untouched legacy: %v", err)
	}
	for _, id := range []sap.EndpointIdentity{
		{Agent: "acme", Owner: "team1", Name: "ops1"},
		{Agent: "a-1", Owner: "ops-2x", Name: "a1-b2-c3"},
	} {
		if err := id.Validate(); err != nil {
			t.Errorf("valid literal %+v: %v", id, err)
		}
	}
}
