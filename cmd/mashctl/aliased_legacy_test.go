package main

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

// aliasedLegacy provisions the legacy endpoint "ghost" and a new-form endpoint that names it as its alias, and returns
// the legacy user key and the holder's flat name.
func aliasedLegacy(t *testing.T, e *testenv.Env) (ghostUser, holder string, a *admin.Admin) {
	t.Helper()
	ctlRun(t, e, "init")
	a, err := admin.New(e.Conn(t, e.AdminCreds), e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, ghostUser = e.Mint(t, "ghost")
	if _, err := a.Provision(ctx, "ghost", ghostUser, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	id, err := sap.New("acme", "team1", "ops1")
	if err != nil {
		t.Fatal(err)
	}
	_, u := e.Mint(t, "ops1")
	if _, err := a.ProvisionIdentity(ctx, id, &store.Alias{Owner: e.Owner, Name: "ghost"}, u, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	return ghostUser, id.Flat(), a
}

func wantAliasedRefusal(t *testing.T, out, errb string, code int, holder string) {
	t.Helper()
	if code != 1 || out != "" {
		t.Fatalf("want exit 1 and no stdout: %d %q %q", code, out, errb)
	}
	for _, w := range []string{"ghost", holder, "alias", "nothing was changed", "retire or re-point"} {
		if !strings.Contains(errb, w) {
			t.Errorf("message lacks %q: %q", w, errb)
		}
	}
	if strings.Contains(errb, "PARTIAL") || strings.Count(errb, "nothing was") != 1 {
		t.Errorf("muddled message: %q", errb)
	}
	if strings.Count(errb, "\n") != 1 {
		t.Errorf("want one line: %q", errb)
	}
}

func stillThere(t *testing.T, e *testenv.Env) {
	t.Helper()
	if out, _, _ := ctlRun(t, e, "list"); !strings.HasPrefix(out, "ghost\t") && !strings.Contains(out, "\nghost\t") {
		t.Fatalf("ghost was removed: %q", out)
	}
}

func TestDeprovisionAliasedLegacyIsRefusedClearly(t *testing.T) {
	e := testenv.Start(t)
	_, holder, _ := aliasedLegacy(t, e)
	out, errb, code := ctlRun(t, e, "deprovision", "ghost")
	wantAliasedRefusal(t, out, errb, code, holder)
	stillThere(t, e)
}

func TestDeprovisionUserAliasedLegacyIsRefusedClearly(t *testing.T) {
	e := testenv.Start(t)
	user, holder, _ := aliasedLegacy(t, e)
	out, errb, code := ctlRun(t, e, "deprovision", "ghost", "--user", user)
	wantAliasedRefusal(t, out, errb, code, holder)
	stillThere(t, e)
}

func TestDeprovisionDrainedAliasedLegacyIsRefusedClearly(t *testing.T) {
	e := testenv.Start(t)
	_, holder, a := aliasedLegacy(t, e)
	st, err := a.Suspend(context.Background(), "ghost")
	if err != nil {
		t.Fatal(err)
	}
	out, errb, code := ctlRun(t, e, "deprovision", "ghost", "--suspension", st.ID, "--drained-to", strconv.FormatUint(st.LastSeq, 10))
	wantAliasedRefusal(t, out, errb, code, holder)
	stillThere(t, e)
}

func TestRetireAliasedLegacyIsRefusedClearly(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("b")
	a, err := admin.New(h.e.Conn(t, h.e.AdminCreds), h.e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := sap.New("acme", "team1", "ops1")
	_, u := h.e.Mint(t, "ops1")
	if _, err := a.ProvisionIdentity(context.Background(), id, &store.Alias{Owner: h.e.Owner, Name: "b"}, u, h.e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	out, errb, code := h.run("retire", "b")
	if code != 1 || !strings.HasPrefix(out, "") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	for _, w := range []string{"b", id.Flat(), "alias", "nothing was changed", "retire or re-point"} {
		if !strings.Contains(errb, w) {
			t.Errorf("message lacks %q: %q", w, errb)
		}
	}
	if strings.Count(errb, "nothing was") != 1 || strings.Contains(errb, "PARTIAL") {
		t.Errorf("muddled: %q", errb)
	}
	if r := h.records(); len(r) != 2 {
		t.Errorf("records %v", r)
	}
}
