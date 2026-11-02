package sap

import "testing"

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

func TestNameFromSubject(t *testing.T) {
	cases := map[string]struct {
		name string
		ok   bool
	}{
		"agents.hb.cc.acme.x":        {"x", true},
		"agents.mail.cc.acme.mash-a": {"mash-a", true},
		"agents.hb.cc.acme":          {"", false},
		"agents.hb.cc.acme.x.y":      {"", false},
		"other.hb.cc.acme.x":         {"", false},
		"":                           {"", false},
	}
	for subj, w := range cases {
		n, ok := NameFromSubject(subj)
		if n != w.name || ok != w.ok {
			t.Errorf("%q: got (%q,%v) want (%q,%v)", subj, n, ok, w.name, w.ok)
		}
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
	if MailFilterZones("acme", "p-ops2") != "agents.mail.*.acme.*.p-ops2" {
		t.Fatal(MailFilterZones("acme", "p-ops2"))
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
