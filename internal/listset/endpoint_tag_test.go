package listset

import (
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/sap"
)

// A legacy identity gets the tag of its name (ADR 0138); the owner and name must still be the address's.
func TestEndpointTagOfALegacyIdentityIsItsName(t *testing.T) {
	id, err := sap.Legacy("c", "core")
	if err != nil {
		t.Fatal(err)
	}
	got, err := EndpointTag(id, "c.core")
	if err != nil || got != "ep:core" {
		t.Fatalf("got %q, %v; want ep:core", got, err)
	}
	for _, addr := range []string{"d.core", "c.lead"} {
		if _, err := EndpointTag(id, addr); err == nil || !strings.Contains(err.Error(), "not at address") {
			t.Fatalf("address %s: %v", addr, err)
		}
	}
	tags, err := WithEndpointTag([]string{"allow:c.lead"}, "cc.c.core", "c.core")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range tags {
		found = found || x == "ep:core"
	}
	if !found {
		t.Fatalf("tags %v", tags)
	}
	// A legacy and a new-form flat key never collide.
	nf, _ := sap.New("claude-code", "c", "core")
	if nt, _ := EndpointTag(nf, "c.core"); nt == got {
		t.Fatalf("legacy and new-form tag are both %q", nt)
	}
}

// A legacy credential's expanded template lines name only its own inbox and reply prefix.
func TestLegacyCredentialExpandsToItsOwnInboxOnly(t *testing.T) {
	c, err := Compile(at001())
	if err != nil {
		t.Fatal(err)
	}
	var tpl jwt.Permissions
	for _, x := range c.Templates {
		if x.Group == "c" {
			tpl = x.Permissions
		}
	}
	id, _ := sap.Legacy("c", "core")
	tag, err := EndpointTag(id, "c.core")
	if err != nil {
		t.Fatal(err)
	}
	tags := append(append([]string{}, c.Users[0].Tags...), tag)
	p, err := expandPermissions(tpl, "core", tags)
	if err != nil {
		t.Fatal(err)
	}
	all := append(append(append(append([]string{}, p.pubAllow...), p.subAllow...), p.pubDeny...), p.subDeny...)
	var inbox, reply bool
	for _, l := range all {
		inbox = inbox || strings.Contains(l, "inbox-core")
		reply = reply || strings.Contains(l, "_INBOX.core")
		if strings.Contains(l, "inbox-") && !strings.Contains(l, "inbox-core") {
			t.Errorf("line %q names an inbox that is not its own", l)
		}
		if strings.Contains(l, "_INBOX.") && l != "_INBOX.>" && !strings.Contains(l, "_INBOX.core") {
			t.Errorf("line %q names a reply prefix that is not its own", l)
		}
		for _, other := range []string{"inbox-lead", "inbox-sdk", "_INBOX.lead", "_INBOX.sdk"} {
			if strings.Contains(l, other) {
				t.Errorf("line %q names another endpoint's inbox", l)
			}
		}
	}
	if !inbox || !reply {
		t.Fatalf("no inbox-core (%v) or _INBOX.core (%v) line in %v", inbox, reply, all)
	}
}
