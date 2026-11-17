package sap

import "testing"

func TestAddressSubjectsAndInbox(t *testing.T) {
	a, err := NewAddress("acme", "ops", "p1")
	if err != nil {
		t.Fatal(err)
	}
	want := Subjects{
		Prompt: "agents.prompt.acme.ops.p1",
		Status: "agents.status.acme.ops.p1",
		HB:     "agents.hb.acme.ops.p1",
		Mail:   "agents.mail.acme.ops.p1",
	}
	if a.Subjects() != want || a.MailFilter() != want.Mail {
		t.Fatalf("got %+v filter %q", a.Subjects(), a.MailFilter())
	}
	if a.InboxPrefix() != "_INBOX.acme_ops_p1" || InboxPrefix(a.Flat()) != a.InboxPrefix() {
		t.Fatal(a.InboxPrefix())
	}
	if a.InboxStream() != "inbox-acme_ops_p1" || InboxStream(a.Flat()) != a.InboxStream() {
		t.Fatal(a.InboxStream())
	}
}

func TestProtocolConstants(t *testing.T) {
	if AgentName != "claude-code" || ServiceName != "agents" || QueueGroup != "agents" || ProtocolVersion != "0.3" {
		t.Error("a protocol constant changed")
	}
}
