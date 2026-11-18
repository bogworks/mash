package admin

import (
	"slices"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/sap"
)

func addrOf(t *testing.T, owner, group, instance string) sap.Address {
	t.Helper()
	a, err := sap.NewAddress(owner, group, instance)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// fixedLines is the per-endpoint publish lines of the endpoint with flat key flat and heartbeat hb.
func fixedLines(owner, flat, hb string) []string {
	return []string{
		hb,
		"_INBOX." + owner + ".>",
		"$SRV.>",
		"$JS.API.INFO",
		"$JS.API.STREAM.INFO.inbox-" + flat,
		"$JS.API.CONSUMER.INFO.inbox-" + flat + ".deliver",
		"$JS.API.CONSUMER.MSG.NEXT.inbox-" + flat + ".deliver",
		"$JS.ACK.inbox-" + flat + ".deliver.>",
		"$JS.API.DIRECT.GET.inbox-" + flat,
		"$JS.API.DIRECT.GET.inbox-" + flat + ".>",
		"$JS.API.STREAM.INFO.KV_mash-directory",
		"$JS.API.DIRECT.GET.KV_mash-directory.>",
		"$JS.API.STREAM.INFO.KV_mash-leases",
		"$KV.mash-leases." + flat,
		"$JS.API.DIRECT.GET.KV_mash-leases.$KV.mash-leases." + flat,
	}
}

func sameLines(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n got  %q\n want %q", what, got, want)
	}
}

func TestPermissionsExact(t *testing.T) {
	a := addrOf(t, "alice", "dev", "design")
	p := Permissions(a)
	sameLines(t, "pub allow", p.Pub.Allow, append([]string{
		"agents.mail.alice.*.*",
		"agents.prompt.alice.*.*",
		"agents.status.alice.*.*",
	}, fixedLines("alice", "alice_dev_design", "agents.hb.alice.dev.design")...))
	sameLines(t, "sub allow", p.Sub.Allow, []string{
		"_INBOX.alice.alice_dev_design.>",
		"agents.prompt.alice.dev.design",
		"agents.status.alice.dev.design",
		"$SRV.>",
		"agents.hb.alice.*.*",
	})
	if len(p.Pub.Deny) != 0 || len(p.Sub.Deny) != 0 {
		t.Errorf("unexpected deny lists")
	}
	if p.Resp != nil {
		t.Errorf("resp %+v: a response grant lets an endpoint launder any reply subject it receives", p.Resp)
	}
}

func TestPermissionsLongestAddress(t *testing.T) {
	const t32 = "abcdefghijklmnopqrstuvwxyzabcdef"
	a, err := sap.NewAddress(t32[:20], t32[:20], t32[:22])
	if err != nil {
		t.Fatal(err)
	}
	p := Permissions(a)
	sameLines(t, "sub allow", p.Sub.Allow, []string{
		"_INBOX." + a.Owner + "." + a.Flat() + ".>",
		a.Subjects().Prompt,
		a.Subjects().Status,
		"$SRV.>",
		"agents.hb." + a.Owner + ".*.*",
	})
}

// No peer line reaches another owner: every publish and subscribe line is under the endpoint's own flat key, a fixed
// line, or has the owner as a literal in the owner slot (token 3 of an agents. subject).
func TestPermissionsNeverReachAnotherOwner(t *testing.T) {
	for _, a := range []sap.Address{
		addrOf(t, "alice", "dev", "design"),
		addrOf(t, "bob", "ops", "pager"),
		addrOf(t, "o", "g", "i"),
	} {
		p := Permissions(a)
		flat := a.Flat()
		for _, s := range append(slices.Clone([]string(p.Pub.Allow)), p.Sub.Allow...) {
			if strings.HasPrefix(s, "agents.") {
				toks := strings.Split(s, ".")
				if len(toks) != 5 {
					t.Errorf("%s: %q is not a five-token subject", a, s)
					continue
				}
				if toks[2] != a.Owner {
					t.Errorf("%s: %q does not carry the owner as a literal in slot 1", a, s)
				}
				if toks[1] == "hb" && slices.Contains(p.Pub.Allow, s) && s != a.Subjects().HB {
					t.Errorf("%s: heartbeat publication beyond its own subject: %q", a, s)
				}
				continue
			}
			if strings.Contains(s, "inbox-") && !strings.Contains(s, "inbox-"+flat) {
				t.Errorf("%s: grant on another inbox: %q", a, s)
			}
			if strings.Contains(s, "mash-leases.") && !strings.HasSuffix(s, "mash-leases."+flat) {
				t.Errorf("%s: grant on another lease key: %q", a, s)
			}
			if s == ">" || s == "$JS.API.>" || strings.Contains(s, "STREAM.DELETE") || strings.Contains(s, "STREAM.PURGE") ||
				strings.Contains(s, "CONSUMER.CREATE") || strings.Contains(s, "CONSUMER.DELETE") {
				t.Errorf("%s: over-broad grant %q", a, s)
			}
			if strings.HasPrefix(s, "_INBOX") && s != "_INBOX."+a.Owner+"."+flat+".>" && s != "_INBOX."+a.Owner+".>" {
				t.Errorf("%s: unexpected inbox grant: %q", a, s)
			}
			if strings.Contains(s, "$KV.mash-directory") {
				t.Errorf("%s: directory must be read-only: %q", a, s)
			}
		}
	}
}

func TestPermissionsInvalidAddressIsNil(t *testing.T) {
	for _, a := range []sap.Address{{}, {Owner: "*", Group: "g", Instance: "i"}, {Owner: "a", Group: "*", Instance: "i"}, {Owner: "a.b", Group: "g", Instance: "i"}} {
		if p := Permissions(a); p != nil {
			t.Errorf("%+v: got %v, want nil (an empty set would allow everything)", a, p)
		}
		if f := NSCFlags(a); f != nil {
			t.Errorf("%+v: flags %v", a, f)
		}
	}
}

func TestNSCFlags(t *testing.T) {
	a := addrOf(t, "alice", "dev", "design")
	f := NSCFlags(a)
	if f[0] != "--allow-pub" || f[1] != "agents.mail.alice.*.*" || len(f) != 2*(3+15+5) {
		t.Errorf("flag shape: %q", f)
	}
	s := strings.Join(f, " ")
	for _, want := range []string{"--allow-pub agents.mail.alice.*.*", "--allow-sub agents.prompt.alice.dev.design"} {
		if !strings.Contains(s, want) {
			t.Errorf("flags missing %q:\n%s", want, s)
		}
	}
}

// A wildcard in the owner slot (or before it) is refused, however the grant is spelled; a literal owner with
// wildcards after it is accepted.
func TestValidateGrantRefusesWildcardOwner(t *testing.T) {
	for _, bad := range []string{
		"agents.mail.*.dev.design", "agents.mail.>", "agents.*.alice.dev.design", "agents.>", "*.mail.alice.dev.design", ">",
		"agents.mail.al*.dev.design", "agents.mail..dev.design", "agents.mail.alice", "agents.mail.>.dev",
		"_INBOX.>", "_INBOX.*.x", "_INBOX.*.>", "_INBOX.>.x",
	} {
		if err := ValidateGrant(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, ok := range []string{"agents.mail.alice.*.*", "agents.prompt.alice.dev.*", "agents.status.alice.>", "other.subject.*", "_INBOX.alice.>", "_INBOX.alice.alice_dev_design.>"} {
		if err := ValidateGrant(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
}

// Every peer line Permissions gives passes ValidateGrant.
func TestPermissionsPeerLinesPassValidateGrant(t *testing.T) {
	for _, s := range Permissions(addrOf(t, "alice", "dev", "design")).Pub.Allow {
		if strings.HasPrefix(s, "agents.") && !strings.HasPrefix(s, "agents.hb.") {
			if err := ValidateGrant(s); err != nil {
				t.Errorf("%q: %v", s, err)
			}
		}
	}
}

func TestGoldenLockKey(t *testing.T) {
	if got := lockKey("alice_dev_design"); got != "limits-lock.alice_dev_design" {
		t.Errorf("lockKey = %q", got)
	}
}
