package listset

import (
	"slices"
	"strings"
	"testing"
)

// endpointSet is three senders: c.core and c.other (the targets), and d.solo, whose list mails c.core only. Every verb
// is closed, so d.solo reaches exactly what its allow tag gives.
func endpointSet() ListSet {
	return ListSet{
		Switches: concat(
			switches("c", Closed, Closed, Closed, Closed),
			switches("d", Closed, Closed, Closed, Closed)),
		Senders: []SenderLists{{Sender: "c.core"}, {Sender: "c.other"}, {Sender: "d.solo", Allow: []string{"c.core"}}},
	}
}

// No template line holds a literal agent token: the agent token position of every address subject is a wildcard, and
// the fixed lines name the endpoint through {{tag(ep)}}. The only literal word of an agents. line is "agents" itself.
func TestNoTemplateLineNamesAnAgentToken(t *testing.T) {
	for name, ls := range map[string]ListSet{"at001": at001(), "at003 deny": at003Deny(), "endpoint set": endpointSet()} {
		c, err := Compile(ls)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, tpl := range c.Templates {
			p := tpl.Permissions
			for _, l := range slices.Concat(p.Pub.Allow, p.Pub.Deny, p.Sub.Allow, p.Sub.Deny) {
				toks := strings.Split(l, ".")
				if toks[0] != "agents" {
					continue
				}
				if len(toks) < 4 || toks[2] != "*" {
					t.Errorf("%s: template %s line %q: the agent token position is not a wildcard", name, tpl.Group, l)
				}
				for _, tk := range testAgents {
					if strings.Contains(l, tk) {
						t.Errorf("%s: template %s line %q names the agent token %q", name, tpl.Group, l, tk)
					}
				}
			}
		}
		for _, u := range c.Users {
			for _, tag := range u.Tags {
				if IsEndpointTag(tag) {
					t.Errorf("%s: compiled user %s carries the endpoint tag %q", name, u.Sender, tag)
				}
			}
		}
	}
	// Check refuses a template that names a token, with the token in an owner line or in an allow-tag line.
	ls := endpointSet()
	c, err := Compile(ls)
	if err != nil {
		t.Fatal(err)
	}
	for i := range c.Templates {
		if c.Templates[i].Group == "d" {
			c.Templates[i].Permissions.Pub.Allow.Add("agents.mail.cc.c.core")
		}
	}
	if err := Check(ls, c); err == nil || !strings.Contains(err.Error(), "names an agent token") {
		t.Fatalf("Check with a literal agent token in a line = %v", err)
	}
}

// A sender allowed to mail c.core can mail it under any agent token of the receiver (cc and claude-code here), and
// cannot mail c.other under any.
func TestLiveAllowedAddressIsReachedUnderAnyAgentToken(t *testing.T) {
	if testing.Short() {
		t.Skip("live nats-server test")
	}
	c, err := Compile(endpointSet())
	if err != nil {
		t.Fatal(err)
	}
	env := newLiveEnv(t, c)
	_, obs := env.observer()
	solo, _, err := env.user(c, "d.solo")
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range []string{"cc", "claude-code"} {
		if !canPublish(t, solo, obs, "agents.mail."+tk+".c.core") {
			t.Errorf("d.solo cannot mail agents.mail.%s.c.core, which its list gives", tk)
		}
		if canPublish(t, solo, obs, "agents.mail."+tk+".c.other") {
			t.Errorf("d.solo mailed agents.mail.%s.c.other, which its list does not give", tk)
		}
		// The verbs its list gives, and no other peer address.
		for _, v := range []Verb{Prompt, Status} {
			if !canPublish(t, solo, obs, subjT(tk, "c.core", v)) {
				t.Errorf("d.solo cannot %s c.core under agent token %s", v, tk)
			}
			if canPublish(t, solo, obs, subjT(tk, "c.other", v)) {
				t.Errorf("d.solo reached c.other with %s under agent token %s", v, tk)
			}
		}
	}
}

// A credential without the endpoint tag (an old one, or one with list tags only) connects, keeps its agents. subjects,
// and gets no inbox, lease or reply access from the template: the lines that need the tag are dropped by the server.
// A credential with the tag gets exactly the lines of its own flat key; the template trusts the tag, which is why the
// issuer builds it from mash's own table and refuses an endpoint that is not at the address.
func TestLiveCredentialWithoutEndpointTagHasNoInboxAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("live nats-server test")
	}
	c, err := Compile(endpointSet())
	if err != nil {
		t.Fatal(err)
	}
	env := newLiveEnv(t, c)
	admin, obs := env.observer()
	var tagless *conn
	for _, u := range c.Users {
		if u.Sender == "d.solo" {
			if tagless, err = env.connect(env.scoped["d"], u.Name, u.Tags, true); err != nil {
				t.Fatalf("a credential without the endpoint tag cannot connect: %v", err)
			}
			t.Cleanup(tagless.nc.Close)
		}
	}
	own := flat("d.solo")
	for _, s := range []string{
		"$JS.API.STREAM.INFO.inbox-" + own,
		"$JS.API.CONSUMER.INFO.inbox-" + own + ".deliver",
		"$JS.API.CONSUMER.MSG.NEXT.inbox-" + own + ".deliver",
		"$JS.ACK.inbox-" + own + ".deliver.1.1.1.1.1",
		"$JS.API.DIRECT.GET.inbox-" + own,
		"$KV.mash-leases." + own,
		"$JS.API.DIRECT.GET.KV_mash-leases.$KV.mash-leases." + own,
	} {
		if canPublish(t, tagless, obs, s) {
			t.Errorf("a credential without the endpoint tag published %s", s)
		}
	}
	if subscribeReaches(t, tagless, admin.nc, "_INBOX."+own+".>", "_INBOX."+own+".reply") {
		t.Error("a credential without the endpoint tag subscribed to its reply prefix")
	}
	// What needs no tag still works: its own subjects and the shared plumbing, and its list's reach.
	if !canPublish(t, tagless, obs, subjT("cc", "d.solo", Heartbeat)) || !canPublish(t, tagless, obs, "$JS.API.INFO") || !canPublish(t, tagless, obs, "agents.mail.cc.c.core") {
		t.Error("a credential without the endpoint tag lost a line that needs no endpoint tag")
	}

	// With the tag: exactly its own flat key.
	solo, _, err := env.user(c, "d.solo")
	if err != nil {
		t.Fatal(err)
	}
	if !canPublish(t, solo, obs, "$KV.mash-leases."+own) || !canPublish(t, solo, obs, "$JS.API.STREAM.INFO.inbox-"+own) {
		t.Error("a credential with the endpoint tag cannot reach its own inbox and lease")
	}
	if !subscribeReaches(t, solo, admin.nc, "_INBOX."+own+".>", "_INBOX."+own+".reply") {
		t.Error("a credential with the endpoint tag does not receive on its reply prefix")
	}
	for _, other := range []string{flat("c.core"), flat("c.other"), "claude-code_d_solo", "foo_d_solo2"} {
		if canPublish(t, solo, obs, "$JS.API.STREAM.INFO.inbox-"+other) || canPublish(t, solo, obs, "$KV.mash-leases."+other) {
			t.Errorf("d.solo reached the inbox or lease of %s", other)
		}
		if subscribeReaches(t, solo, admin.nc, "_INBOX."+other+".>", "_INBOX."+other+".reply") {
			t.Errorf("d.solo subscribed to the reply prefix of %s", other)
		}
	}
	// The template trusts the tag: a credential carrying another endpoint's tag gets that endpoint's lines. That is
	// why the issuer, not the requester, builds the tag, and refuses an endpoint that is not at the address.
	var forged *conn
	for _, u := range c.Users {
		if u.Sender == "d.solo" {
			if forged, err = env.connect(env.scoped["d"], u.Name, slices.Concat(u.Tags, []string{EndpointTagName + ":" + flat("c.core")}), true); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(forged.nc.Close)
		}
	}
	if !canPublish(t, forged, obs, "$JS.API.STREAM.INFO.inbox-"+flat("c.core")) {
		t.Error("expected the template to trust a forged endpoint tag (the issuer's refusal is what protects it)")
	}
}
