// Characterisation tests (plan 2b, Phase 0): these pin today's identifiers and validity for legacy identities; a change here is a change to a legacy identifier and needs its own decision.

package admin

import (
	"slices"
	"testing"
)

// goldenFixed returns the per-endpoint publish lines of endpoint name (name may be the template {{name()}}).
func goldenFixed(name, hb string) []string {
	return []string{
		hb,
		"_INBOX.>",
		"$SRV.>",
		"$JS.API.INFO",
		"$JS.API.STREAM.INFO.inbox-" + name,
		"$JS.API.CONSUMER.INFO.inbox-" + name + ".deliver",
		"$JS.API.CONSUMER.MSG.NEXT.inbox-" + name + ".deliver",
		"$JS.ACK.inbox-" + name + ".deliver.>",
		"$JS.API.DIRECT.GET.inbox-" + name,
		"$JS.API.DIRECT.GET.inbox-" + name + ".>",
		"$JS.API.STREAM.INFO.KV_mash-directory",
		"$JS.API.DIRECT.GET.KV_mash-directory.>",
		"$JS.API.STREAM.INFO.KV_mash-leases",
		"$KV.mash-leases." + name,
		"$JS.API.DIRECT.GET.KV_mash-leases.$KV.mash-leases." + name,
	}
}

func goldenSame(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n got  %q\n want %q", what, got, want)
	}
}

func TestGoldenPermissionsUnpolicied(t *testing.T) {
	p := Permissions("o", "a")
	goldenSame(t, "pub allow", p.Pub.Allow, append([]string{
		"agents.mail.*.o.*",
		"agents.prompt.*.o.*",
		"agents.status.*.o.*",
	}, goldenFixed("a", "agents.hb.cc.o.a")...))
	goldenSame(t, "sub allow", p.Sub.Allow, []string{
		"_INBOX.a.>",
		"agents.prompt.cc.o.a",
		"agents.status.cc.o.a",
		"$SRV.>",
		"agents.hb.*.o.*",
	})
	goldenSame(t, "pub deny", p.Pub.Deny, nil)
	goldenSame(t, "sub deny", p.Sub.Deny, nil)
	if p.Resp != nil {
		t.Errorf("response permission set: %+v", p.Resp)
	}
}

func TestGoldenPermissionsTo(t *testing.T) {
	p := PermissionsTo("o", "a", []string{"c", "b"})
	goldenSame(t, "pub allow", p.Pub.Allow, append([]string{
		"agents.mail.*.o.b", "agents.prompt.*.o.b", "agents.status.*.o.b",
		"agents.mail.*.o.c", "agents.prompt.*.o.c", "agents.status.*.o.c",
	}, goldenFixed("a", "agents.hb.cc.o.a")...))
	goldenSame(t, "empty recipients", PermissionsTo("o", "a", []string{}).Pub.Allow, goldenFixed("a", "agents.hb.cc.o.a"))
}

func TestGoldenPermissionsLongNames(t *testing.T) {
	const n48 = "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijkl"
	p := Permissions(n48, n48)
	want := append([]string{
		"agents.mail.*." + n48 + ".*",
		"agents.prompt.*." + n48 + ".*",
		"agents.status.*." + n48 + ".*",
	}, goldenFixed(n48, "agents.hb.cc."+n48+"."+n48)...)
	goldenSame(t, "pub allow", p.Pub.Allow, want)
	goldenSame(t, "sub allow", p.Sub.Allow, []string{
		"_INBOX." + n48 + ".>",
		"agents.prompt.cc." + n48 + "." + n48,
		"agents.status.cc." + n48 + "." + n48,
		"$SRV.>",
		"agents.hb.*." + n48 + ".*",
	})
}

func TestGoldenNSCFlags(t *testing.T) {
	var want []string
	p := Permissions("o", "a")
	for _, s := range p.Pub.Allow {
		want = append(want, "--allow-pub", s)
	}
	for _, s := range p.Sub.Allow {
		want = append(want, "--allow-sub", s)
	}
	goldenSame(t, "flags", NSCFlags("o", "a"), want)
	if f := NSCFlags("o", "a"); f[0] != "--allow-pub" || f[1] != "agents.mail.*.o.*" || len(f) != 2*(3+15+5) {
		t.Errorf("flag shape: %q", f)
	}
}

// The admin's own per-endpoint lock key in the lease bucket is derived from the name.
func TestGoldenLockKey(t *testing.T) {
	if got := lockKey("a"); got != "limits-lock.a" {
		t.Errorf("lockKey = %q", got)
	}
}
