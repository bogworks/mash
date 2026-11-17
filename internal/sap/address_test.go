package sap_test

import (
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/sap"
)

func rep(c byte, n int) string { return strings.Repeat(string(c), n) }

func TestNewAddressValid(t *testing.T) {
	cases := []struct{ o, g, i string }{
		{"a", "b", "c"},
		{"acme", "ops", "worker-1"},
		{"cc", "cc", "cc"},
		{"a1-b2", "c3-d4", "e5-f6"},
		{rep('a', 32), rep('b', 15), rep('c', 15)},
		{rep('a', 20), rep('b', 20), rep('c', 22)}, // total 64
	}
	for _, c := range cases {
		a, err := sap.NewAddress(c.o, c.g, c.i)
		if err != nil {
			t.Errorf("NewAddress(%q,%q,%q): %v", c.o, c.g, c.i, err)
			continue
		}
		if a.Owner != c.o || a.Group != c.g || a.Instance != c.i {
			t.Errorf("NewAddress(%q,%q,%q) = %+v", c.o, c.g, c.i, a)
		}
		if err := a.Validate(); err != nil {
			t.Errorf("Validate(%v): %v", a, err)
		}
	}
}

func TestNewAddressRefusals(t *testing.T) {
	bad := []struct{ tok, rule string }{
		{"", "empty"},
		{"Abc", "lower-case"},
		{"a--b", "consecutive dashes"},
		{"ab-", "end with a dash"},
		{"9ab", "start with a letter"},
		{rep('a', 33), "too long"},
		{"a_b", "character"},
		{"a.b", "character"},
		{"a/b", "character"},
	}
	names := []string{"owner", "group", "instance"}
	for _, v := range bad {
		for pos := 0; pos < 3; pos++ {
			tk := []string{"x", "y", "z"}
			tk[pos] = v.tok
			_, err := sap.NewAddress(tk[0], tk[1], tk[2])
			if err == nil {
				t.Errorf("NewAddress accepted %q at %s", v.tok, names[pos])
				continue
			}
			if !strings.Contains(err.Error(), names[pos]) || !strings.Contains(err.Error(), v.rule) {
				t.Errorf("NewAddress(%q at %s) error %q should name %q and %q", v.tok, names[pos], err, names[pos], v.rule)
			}
		}
	}
}

func TestNewAddressTotalLength(t *testing.T) {
	if _, err := sap.NewAddress(rep('a', 21), rep('b', 21), rep('c', 21)); err == nil {
		t.Error("total 65 accepted")
	} else if !strings.Contains(err.Error(), "65") {
		t.Errorf("total error %q should give the length", err)
	}
	if _, err := sap.NewAddress(rep('a', 21), rep('b', 21), rep('c', 20)); err != nil {
		t.Errorf("total 64 refused: %v", err)
	}
	if _, err := sap.NewAddress(rep('a', 32), rep('b', 32), "c"); err == nil {
		t.Error("total 67 accepted")
	}
}

func TestAddressMissingGroupNamesGroup(t *testing.T) {
	_, err := sap.NewAddress("acme", "", "w1")
	if err == nil || !strings.Contains(err.Error(), "group") || !strings.Contains(err.Error(), "empty") {
		t.Errorf("missing group error = %v", err)
	}
	_, err = sap.NewAddress("acme", "A", "w1")
	if err == nil || !strings.Contains(err.Error(), `group token "A" must be lower-case`) {
		t.Errorf("uppercase group error = %v", err)
	}
}

func TestAddressZeroInvalid(t *testing.T) {
	if err := (sap.Address{}).Validate(); err == nil {
		t.Error("zero Address validates")
	}
	if err := (sap.Address{Owner: "a", Group: "", Instance: "c"}).Validate(); err == nil {
		t.Error("Address without a group validates")
	}
}

func TestAddressForms(t *testing.T) {
	a, err := sap.NewAddress("acme", "ops", "w-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := a.Flat(); got != "acme_ops_w-1" {
		t.Errorf("Flat = %q", got)
	}
	if got := a.String(); got != "acme.ops.w-1" {
		t.Errorf("String = %q", got)
	}
	s := a.Subjects()
	want := sap.Subjects{
		Prompt: "agents.prompt.acme.ops.w-1",
		Status: "agents.status.acme.ops.w-1",
		HB:     "agents.hb.acme.ops.w-1",
		Mail:   "agents.mail.acme.ops.w-1",
	}
	if s != want {
		t.Errorf("Subjects = %+v, want %+v", s, want)
	}
	if f := a.MailFilter(); f != s.Mail || strings.ContainsAny(f, "*>") {
		t.Errorf("MailFilter = %q, want exact %q", f, s.Mail)
	}
}

func TestAddressFlatRoundTripAndInjective(t *testing.T) {
	toks := []string{"a", "a-b", "b", "a-b-c", "b-c", "c"}
	seen := map[string]sap.Address{}
	for _, o := range toks {
		for _, g := range toks {
			for _, i := range toks {
				a, err := sap.NewAddress(o, g, i)
				if err != nil {
					t.Fatal(err)
				}
				back, err := sap.AddressFromFlat(a.Flat())
				if err != nil || back != a {
					t.Errorf("AddressFromFlat(%q) = %v, %v; want %v", a.Flat(), back, err, a)
				}
				if prev, dup := seen[a.Flat()]; dup {
					t.Errorf("flat %q is shared by %v and %v", a.Flat(), prev, a)
				}
				seen[a.Flat()] = a
				p, err := sap.ParseAddress(a.String())
				if err != nil || p != a {
					t.Errorf("ParseAddress(%q) = %v, %v", a.String(), p, err)
				}
			}
		}
	}
}

func TestAddressFromFlatRefusals(t *testing.T) {
	for _, f := range []string{"", "abc", "a_b", "a_b_c_d", "a__c", "A_b_c", "a_b_", "a.b.c", rep('a', 21) + "_" + rep('b', 21) + "_" + rep('c', 21)} {
		if a, err := sap.AddressFromFlat(f); err == nil {
			t.Errorf("AddressFromFlat(%q) = %v, want error", f, a)
		}
	}
}

func TestParseAddressRefusals(t *testing.T) {
	for _, s := range []string{"", "a", "a.b", "a.b.c.d", "a..c", "a_b_c", "A.b.c"} {
		if a, err := sap.ParseAddress(s); err == nil {
			t.Errorf("ParseAddress(%q) = %v, want error", s, a)
		}
	}
}

func TestAddressSubjectRoundTrip(t *testing.T) {
	a, err := sap.NewAddress("acme", "ops", "w-1")
	if err != nil {
		t.Fatal(err)
	}
	s := a.Subjects()
	for verb, subj := range map[string]string{"prompt": s.Prompt, "status": s.Status, "hb": s.HB, "mail": s.Mail} {
		got, v, ok := sap.AddressFromSubject(subj)
		if !ok || got != a || v != verb {
			t.Errorf("AddressFromSubject(%q) = %v, %q, %v; want %v, %q, true", subj, got, v, ok, a, verb)
		}
	}
}

func TestAddressFromSubjectRefusals(t *testing.T) {
	for _, subj := range []string{
		"",
		"agents.mail.a.b",
		"agents.mail.a.b.c.d",
		"agents.mail.*.b.c",
		"agents.mail.a.*.c",
		"agents.mail.a.b.>",
		"agents.mail.a.b.",
		"agents.bogus.a.b.c",
		"agents.*.a.b.c",
		"other.mail.a.b.c",
		"agents.mail.A.b.c",
		"agents.mail.a.b--x.c",
		"agents.mail." + rep('a', 21) + "." + rep('b', 21) + "." + rep('c', 21),
	} {
		if a, v, ok := sap.AddressFromSubject(subj); ok {
			t.Errorf("AddressFromSubject(%q) = %v, %q, true; want false", subj, a, v)
		}
	}
}
