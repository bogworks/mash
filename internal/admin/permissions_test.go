package admin

import (
	"slices"
	"strings"
	"testing"
)

func TestPermissionsExact(t *testing.T) {
	p := Permissions("o", "a")
	wantPub := []string{
		"$JS.ACK.inbox-a.deliver.>",
		"$JS.API.CONSUMER.INFO.inbox-a.deliver",
		"$JS.API.CONSUMER.MSG.NEXT.inbox-a.deliver",
		"$JS.API.DIRECT.GET.KV_mash-directory.>",
		"$JS.API.DIRECT.GET.KV_mash-leases.$KV.mash-leases.a",
		"$JS.API.DIRECT.GET.inbox-a",
		"$JS.API.DIRECT.GET.inbox-a.>",
		"$JS.API.INFO",
		"$JS.API.STREAM.INFO.KV_mash-directory",
		"$JS.API.STREAM.INFO.KV_mash-leases",
		"$JS.API.STREAM.INFO.inbox-a",
		"$KV.mash-leases.a",
		"$SRV.>",
		"_INBOX.>",
		"agents.hb.cc.o.a",
		"agents.mail.*.o.*",
		"agents.prompt.*.o.*",
		"agents.status.*.o.*",
	}
	wantSub := []string{
		"$SRV.>",
		"_INBOX.a.>",
		"agents.hb.*.o.*",
		"agents.prompt.cc.o.a",
		"agents.status.cc.o.a",
	}
	gotPub := slices.Sorted(slices.Values([]string(p.Pub.Allow)))
	gotSub := slices.Sorted(slices.Values([]string(p.Sub.Allow)))
	if !slices.Equal(gotPub, wantPub) {
		t.Errorf("pub\n got %q\nwant %q", gotPub, wantPub)
	}
	if !slices.Equal(gotSub, wantSub) {
		t.Errorf("sub\n got %q\nwant %q", gotSub, wantSub)
	}
	if len(p.Pub.Deny) != 0 || len(p.Sub.Deny) != 0 {
		t.Errorf("unexpected deny lists")
	}
	if p.Resp != nil {
		t.Errorf("resp %+v: a response grant lets an endpoint launder any reply subject it receives", p.Resp)
	}
}

func TestPermissionsNeverGrantOthers(t *testing.T) {
	p := Permissions("o", "a")
	for _, s := range append([]string(p.Pub.Allow), p.Sub.Allow...) {
		if s == ">" || s == "$JS.API.>" || strings.HasPrefix(s, "$JS.API.STREAM.DELETE") ||
			strings.HasPrefix(s, "$JS.API.STREAM.PURGE") || strings.HasPrefix(s, "$JS.API.STREAM.MSG.DELETE") {
			t.Errorf("over-broad grant %q", s)
		}
		if strings.Contains(s, "inbox-") && !strings.Contains(s, "inbox-a") {
			t.Errorf("grant on another inbox: %q", s)
		}
		if strings.HasPrefix(s, "_INBOX") && s != "_INBOX.a.>" && s != "_INBOX.>" {
			t.Errorf("unexpected inbox grant: %q", s)
		}
		if strings.Contains(s, "CONSUMER.CREATE") || strings.Contains(s, "CONSUMER.DELETE") {
			t.Errorf("consumer management grant: %q", s)
		}
		if strings.Contains(s, "$KV.mash-directory") {
			t.Errorf("directory must be read-only: %q", s)
		}
	}
}

func TestNSCFlags(t *testing.T) {
	f := strings.Join(NSCFlags("o", "a"), " ")
	for _, want := range []string{
		"--allow-pub agents.mail.*.o.*",
		"--allow-sub agents.prompt.cc.o.a",
	} {
		if !strings.Contains(f, want) {
			t.Errorf("flags missing %q:\n%s", want, f)
		}
	}
}

// The mail subject has five tokens (ADR 0091): the wildcard set carries that one form and no six-token line.
func TestPermissionsMailLine(t *testing.T) {
	p := Permissions("o", "a")
	if !slices.Contains(p.Pub.Allow, "agents.mail.*.o.*") {
		t.Error("missing agents.mail.*.o.*")
	}
	for _, s := range p.Pub.Allow {
		if strings.HasPrefix(s, "agents.mail") && strings.Count(s, ".") != 4 {
			t.Errorf("mail line %s is not five tokens", s)
		}
	}
	for _, s := range p.Pub.Allow {
		if strings.HasPrefix(s, "agents.prompt") || strings.HasPrefix(s, "agents.status") {
			if strings.Count(s, ".") != 4 {
				t.Errorf("prompt/status line %s changed shape", s)
			}
		}
	}
	// A recipient form keeps the single legacy line per recipient.
	for _, s := range PermissionsTo("o", "a", []string{"b"}).Pub.Allow {
		if strings.HasPrefix(s, "agents.mail") && s != "agents.mail.*.o.b" {
			t.Errorf("recipient form grants %s", s)
		}
	}
}

func TestPermissionsToNilIsPermissions(t *testing.T) {
	a, b := Permissions("o", "a"), PermissionsTo("o", "a", nil)
	if !slices.Equal(a.Pub.Allow, b.Pub.Allow) || !slices.Equal(a.Sub.Allow, b.Sub.Allow) {
		t.Fatalf("nil recipients differ from Permissions:\n%v\n%v", a, b)
	}
}

func TestPermissionsToRecipients(t *testing.T) {
	p := PermissionsTo("o", "a", []string{"c", "b"})
	for _, w := range []string{"agents.mail.*.o.b", "agents.prompt.*.o.b", "agents.status.*.o.b", "agents.mail.*.o.c", "agents.prompt.*.o.c", "agents.status.*.o.c"} {
		if !slices.Contains(p.Pub.Allow, w) {
			t.Errorf("missing %s", w)
		}
	}
	for _, s := range p.Pub.Allow {
		if strings.HasSuffix(s, ".o.*") {
			t.Errorf("wildcard line %s", s)
		}
	}
	// Same size apart from the peer lines: fixed lines minus 3 wildcards (mail, prompt, status), plus 6.
	if len(p.Pub.Allow) != len(Permissions("o", "a").Pub.Allow)-3+6 {
		t.Errorf("%d lines", len(p.Pub.Allow))
	}
	none := PermissionsTo("o", "a", []string{})
	if len(none.Pub.Allow) != len(Permissions("o", "a").Pub.Allow)-3 {
		t.Errorf("empty recipients: %d lines", len(none.Pub.Allow))
	}
	for _, s := range none.Pub.Allow {
		if strings.HasPrefix(s, "agents.mail") || strings.HasPrefix(s, "agents.prompt") || strings.HasPrefix(s, "agents.status") {
			t.Errorf("empty recipients still grants %s", s)
		}
	}
	if len(none.Pub.Allow) == 0 {
		t.Error("empty block")
	}
}
