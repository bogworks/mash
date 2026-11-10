package sap

import (
	"strings"
	"testing"
)

func TestFor(t *testing.T) {
	s := For("acme", "p-ops2")
	want := Subjects{
		Prompt: "agents.prompt.cc.acme.p-ops2",
		Status: "agents.status.cc.acme.p-ops2",
		HB:     "agents.hb.cc.acme.p-ops2",
		Mail:   "agents.mail.cc.acme.p-ops2",
	}
	if s != want {
		t.Fatalf("got %+v", s)
	}
	if MailFilter("acme", "p-ops2") != "agents.mail.*.acme.p-ops2" {
		t.Fatal(MailFilter("acme", "p-ops2"))
	}
	if InboxPrefix("p-ops2") != "_INBOX.p-ops2" {
		t.Fatal(InboxPrefix("p-ops2"))
	}
	if InboxStream("p-ops2") != "inbox-p-ops2" {
		t.Fatal(InboxStream("p-ops2"))
	}
}

func TestForZone(t *testing.T) {
	s := ForZone("acme", "default", "p-ops2")
	want := Subjects{
		Prompt: "agents.prompt.cc.acme.p-ops2",
		Status: "agents.status.cc.acme.p-ops2",
		HB:     "agents.hb.cc.acme.p-ops2",
		Mail:   "agents.mail.cc.acme.default.p-ops2",
	}
	if s != want {
		t.Fatalf("got %+v", s)
	}
	if got := For("acme", "p-ops2").Mail; got != "agents.mail.cc.acme.p-ops2" {
		t.Fatalf("legacy mail subject changed: %s", got)
	}
	if MailFilterZone("acme", "z1", "p-ops2") != "agents.mail.*.acme.z1.p-ops2" {
		t.Fatal(MailFilterZone("acme", "z1", "p-ops2"))
	}
	if z, ok := ZoneOfMailFilter("agents.mail.*.acme.z1.p-ops2", "acme", "p-ops2"); !ok || z != "z1" {
		t.Fatal(z, ok)
	}
	for _, bad := range []string{"agents.mail.*.acme.*.p-ops2", "agents.mail.*.acme.p-ops2", "agents.mail.cc.acme.z1.p-ops2", "agents.mail.*.other.z1.p-ops2", "agents.mail.*.acme.z1.x", "agents.mail.*.acme.Z1.p-ops2"} {
		if _, ok := ZoneOfMailFilter(bad, "acme", "p-ops2"); ok {
			t.Fatal(bad)
		}
	}
	if MailFilter("acme", "p-ops2") != "agents.mail.*.acme.p-ops2" {
		t.Fatal("legacy filter changed")
	}
	if DefaultZone != "default" {
		t.Fatal(DefaultZone)
	}
}

func TestValidZone(t *testing.T) {
	long := "abcdefghijklmnopqrstuvwxyz012345" // 32
	for z, want := range map[string]bool{
		"default": true, "a": true, "z-1": true, "-": true, "0": true, long: true,
		"": false, long + "x": false, "Zone": false, "a.b": false, "a*": false, "*": false, ">": false,
		"a b": false, "a\tb": false, "a\n": false, "é": false, "a_b": false,
	} {
		if got := ValidZone(z); got != want {
			t.Errorf("ValidZone(%q) = %v, want %v", z, got, want)
		}
	}
}

func TestSplitMail(t *testing.T) {
	type r struct {
		owner, zone, name string
		ok                bool
	}
	for subj, w := range map[string]r{
		"agents.mail.cc.acme.x":     {"acme", "", "x", true},
		"agents.mail.cc.acme.z.x":   {"acme", "z", "x", true},
		"agents.mail.cc.acme":       {"", "", "", false},
		"agents.mail.cc.acme.z.x.y": {"", "", "", false},
		"agents.prompt.cc.acme.x":   {"", "", "", false},
		"agents.mail.cc.acme..x":    {"", "", "", false},
		"":                          {"", "", "", false},
	} {
		o, z, n, ok := SplitMail(subj)
		if (r{o, z, n, ok}) != w {
			t.Errorf("SplitMail(%q) = %q %q %q %v", subj, o, z, n, ok)
		}
	}
	if o, z, n, ok := SplitMail(ForZone("o", "zz", "n").Mail); !ok || o != "o" || z != "zz" || n != "n" {
		t.Fatal("round trip")
	}
}

func TestKeyFromSubject(t *testing.T) {
	cases := map[string]struct {
		key string
		ok  bool
	}{
		"agents.prompt.cc.acme.ops1":       {"ops1", true},
		"agents.hb.cc.acme.mash-a":         {"mash-a", true},
		"agents.prompt.codex.team1.ops1":   {"codex_team1_ops1", true},
		"agents.hb.acme.team1.ops1":        {"acme_team1_ops1", true},
		"agents.prompt.Codex.team1.ops1":   {"", false},
		"agents.prompt.codex.team_1.ops1":  {"", false},
		"agents.prompt.codex.team1.1ops":   {"", false},
		"agents.prompt.codex.team1.":       {"", false},
		"agents.prompt.codex.team1.o--ps":  {"", false},
		"agents.mail.cc.acme.default.ops1": {"", false},
		"agents.mail.codex.acme.default.o": {"", false},
		"agents.hb.codex.acme":             {"", false},
		"other.hb.codex.acme.x":            {"", false},
		"":                                 {"", false},
	}
	for subj, w := range cases {
		k, ok := KeyFromSubject(subj)
		if k != w.key || ok != w.ok {
			t.Errorf("%q: got (%q,%v) want (%q,%v)", subj, k, ok, w.key, w.ok)
		}
	}
	legacy, _ := KeyFromSubject("agents.prompt.cc.acme.ops1")
	newer, _ := KeyFromSubject("agents.prompt.codex.acme.ops1")
	if legacy == newer {
		t.Fatalf("legacy and new-form subjects share key %q", legacy)
	}
	long := "agents.prompt.codex.acme." + strings.Repeat("n", 33)
	if _, ok := KeyFromSubject(long); ok {
		t.Fatal("33-character name accepted")
	}
}
