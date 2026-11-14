package main

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/admin"
)

func adminPermsFor(owner, name string) jwt.Permissions  { return admin.Permissions(owner, name) }
func adminPerms(h *addEnv, name string) jwt.Permissions { return admin.Permissions(h.e.Owner, name) }

// narrowPerms is a hand-made user's permissions: one publish subject fewer than the owner-wide set.
func narrowPerms(uc *jwt.UserClaims) {
	var pub jwt.StringList
	for _, s := range uc.Permissions.Pub.Allow {
		if !strings.HasPrefix(s, "agents.prompt.") {
			pub = append(pub, s)
		}
	}
	uc.Permissions.Pub.Allow = pub
}

func (h *addEnv) credsBytes(name string) string {
	h.t.Helper()
	b, err := os.ReadFile(h.creds(name))
	if err != nil {
		h.t.Fatal(err)
	}
	return string(b)
}

// A bulk run lists what it would do and, without --yes, refuses (exit 2) and changes nothing.
func TestReissueBulkNeedsYes(t *testing.T) {
	fakeKeyRevoked(t, false, nil)
	h := newAddEnv(t)
	h.seed("a")
	h.seedWith("b", extraTag)
	calls := fakeRevoke(t, nil)
	beforeA, beforeB, recs := h.credsBytes("a"), h.credsBytes("b"), h.records()

	for _, flag := range []string{"--all", "--changed"} {
		// the verb never reads the terminal, so a terminal and a pipe behave the same: no prompt, a refusal
		out, errb, code := h.run("reissue", flag)
		if code != 2 || !strings.Contains(out, "will reissue b: ") || !strings.Contains(errb, "rerun with --yes") {
			t.Fatalf("%s: %d %q %q", flag, code, out, errb)
		}
		if flag == "--all" && !strings.Contains(out, "will reissue a: ") {
			t.Errorf("--all list lacks a: %q", out)
		}
		if flag == "--changed" && strings.Contains(out, "will reissue a") {
			t.Errorf("--changed lists the unchanged a: %q", out)
		}
		noSecrets(t, out, errb)
	}
	if h.credsBytes("a") != beforeA || h.credsBytes("b") != beforeB || len(*calls) != 0 || !slices.Equal(h.records(), recs) {
		t.Fatal("a refused bulk run changed something")
	}
	h.noStaged("b")

	out, errb, code := h.run("reissue", "--all", "--yes")
	if code != 0 || !strings.Contains(out, "reissued a\n") || !strings.Contains(out, "reissued b (new user key") {
		t.Fatalf("--all --yes: %d %q %q", code, out, errb)
	}
	if len(*calls) != 1 {
		t.Errorf("revoke calls %+v", *calls)
	}

	// --yes and --widen are for bulk and named runs respectively
	for _, args := range [][]string{{"reissue", "a", "--yes"}, {"reissue", "--all", "--widen"}, {"reissue", "--changed", "--widen"}} {
		if _, _, code := h.run(args...); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

// A bulk run with nothing to do needs no --yes and no system credentials.
func TestReissueBulkNothingNeedsNoYes(t *testing.T) {
	fakeKeyRevoked(t, false, nil)
	h := newAddEnv(t)
	if out, _, code := h.run("reissue", "--all"); code != 0 || out != "nothing to reissue\n" {
		t.Fatalf("--all, empty directory: %d %q", code, out)
	}
	h.seed("a")
	if out, _, code := h.run("reissue", "--changed"); code != 0 || out != "nothing to reissue\n" {
		t.Fatalf("--changed: %d %q", code, out)
	}
}

// A user whose permissions are narrower than the owner-wide set is skipped in a bulk run and untouched; named, it is
// refused without --widen and reissued with it.
func TestReissueNeverWidensSilently(t *testing.T) {
	fakeKeyRevoked(t, false, nil)
	h := newAddEnv(t)
	h.seedWith("n", narrowPerms)
	h.seed("w")
	calls := fakeRevoke(t, nil)
	before := h.credsBytes("n")
	_, oldN := h.credsClaims("n")

	for _, flag := range []string{"--all", "--changed"} {
		out, errb, code := h.run("reissue", flag, "--yes")
		if code != 0 || !strings.Contains(errb, "skipped n: its permissions are narrower than the owner-wide set; name it with --widen to widen it") || strings.Contains(out, "will reissue n") || strings.Contains(out, "reissued n") {
			t.Fatalf("%s: %d %q %q", flag, code, out, errb)
		}
		if flag == "--all" && !strings.Contains(out, "reissued w\n") {
			t.Errorf("--all did not reissue w: %q", out)
		}
	}
	if h.credsBytes("n") != before || len(*calls) != 0 {
		t.Fatal("the narrower user was changed")
	}

	out, errb, code := h.run("reissue", "n")
	if code != 1 || out != "" || !strings.Contains(errb, "reissue n:") || !strings.Contains(errb, "narrower than the owner-wide set") || h.credsBytes("n") != before {
		t.Fatalf("named without --widen: %d %q %q", code, out, errb)
	}

	out, errb, code = h.run("reissue", "n", "--widen")
	if code != 0 || !strings.Contains(out, "reissued n (new user key, old key revoked)") {
		t.Fatalf("named with --widen: %d %q %q", code, out, errb)
	}
	uc, newN := h.credsClaims("n")
	if newN == oldN || !samePermissions(uc.Permissions, adminPerms(h, "n")) {
		t.Fatal("--widen did not mint the owner-wide set under a new key")
	}
}

// Equal or wider current permissions are not widening, and a bulk run lists the endpoint.
func TestReissueEqualOrWiderIsNotWidening(t *testing.T) {
	fakeKeyRevoked(t, false, nil)
	h := newAddEnv(t)
	h.seed("e") // equal
	h.seedWith("g", func(uc *jwt.UserClaims) { uc.Permissions.Pub.Allow.Add("extra.>") })
	fakeRevoke(t, nil)
	if out, errb, code := h.run("reissue", "e", "g"); code != 0 || !strings.Contains(out, "reissued e") || !strings.Contains(out, "reissued g") {
		t.Fatalf("named: %d %q %q", code, out, errb)
	}
	out, errb, code := h.run("reissue", "--all", "--yes")
	if code != 0 || strings.Contains(errb, "skipped") || !strings.Contains(out, "reissued e\n") || !strings.Contains(out, "reissued g") {
		t.Fatalf("--all: %d %q %q", code, out, errb)
	}
}

func TestWidensPermissions(t *testing.T) {
	full := adminPermsFor("o", "x")
	if widensPermissions(full, full) || widensPermissions(jwt.Permissions{}, full) {
		t.Error("equal or empty widens")
	}
	var deny jwt.Permissions = full
	deny.Pub.Deny = jwt.StringList{"secret.>"}
	if !widensPermissions(deny, full) {
		t.Error("a deny the new set lacks is not widening")
	}
	var broad jwt.Permissions
	broad.Pub.Allow.Add(">")
	broad.Sub.Allow.Add(">")
	if widensPermissions(broad, full) {
		t.Error("a wildcard that covers the new set widens")
	}
	if !subjectCovers("a.*", "a.b") || subjectCovers("a.*", "a.b.c") || !subjectCovers("a.>", "a.b.c") || subjectCovers("a.>", "a") || !subjectCovers("a.*", "a.*") {
		t.Error("subjectCovers")
	}
}
