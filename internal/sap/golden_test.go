package sap

import "testing"

// Pins the endpoint identifiers for a few addresses; a change here is a change to a wire identifier and needs its own decision.
func TestGoldenAddressIdentifiers(t *testing.T) {
	for _, c := range []struct{ owner, group, instance, prompt, mail, flat, stream, prefix string }{
		{"o", "g", "a", "agents.prompt.o.g.a", "agents.mail.o.g.a", "o_g_a", "inbox-o_g_a", "_INBOX.o.o_g_a"},
		{"acme-corp", "build", "build-1", "agents.prompt.acme-corp.build.build-1", "agents.mail.acme-corp.build.build-1",
			"acme-corp_build_build-1", "inbox-acme-corp_build_build-1", "_INBOX.acme-corp.acme-corp_build_build-1"},
	} {
		a, err := NewAddress(c.owner, c.group, c.instance)
		if err != nil {
			t.Fatal(err)
		}
		s := a.Subjects()
		if s.Prompt != c.prompt || s.Mail != c.mail || a.Flat() != c.flat || a.InboxStream() != c.stream || a.InboxPrefix() != c.prefix {
			t.Errorf("%v: %+v %q %q %q", a, s, a.Flat(), a.InboxStream(), a.InboxPrefix())
		}
	}
}
