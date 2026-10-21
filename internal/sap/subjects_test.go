package sap

import "testing"

func TestFor(t *testing.T) {
	s := For("randyb", "p-ops2")
	want := Subjects{
		Prompt: "agents.prompt.cc.randyb.p-ops2",
		Status: "agents.status.cc.randyb.p-ops2",
		HB:     "agents.hb.cc.randyb.p-ops2",
		Mail:   "agents.mail.cc.randyb.p-ops2",
	}
	if s != want {
		t.Fatalf("got %+v", s)
	}
	if MailFilter("randyb", "p-ops2") != "agents.mail.*.randyb.p-ops2" {
		t.Fatal(MailFilter("randyb", "p-ops2"))
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
		"agents.hb.cc.randyb.x":        {"x", true},
		"agents.mail.cc.randyb.mash-a": {"mash-a", true},
		"agents.hb.cc.randyb":          {"", false},
		"agents.hb.cc.randyb.x.y":      {"", false},
		"other.hb.cc.randyb.x":         {"", false},
		"":                             {"", false},
	}
	for subj, w := range cases {
		n, ok := NameFromSubject(subj)
		if n != w.name || ok != w.ok {
			t.Errorf("%q: got (%q,%v) want (%q,%v)", subj, n, ok, w.name, w.ok)
		}
	}
}
