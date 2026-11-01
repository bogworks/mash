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
