package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

// mash admin deprovision of a legacy endpoint that a new-form endpoint names as its alias is refused, in every form,
// with one line that names both and says nothing was changed.
func TestAdminCLIDeprovisionAliasedLegacyIsRefused(t *testing.T) {
	e := testenv.Start(t)
	env := map[string]string{"HOME": t.TempDir(), "MASH_URL": e.URL, "MASH_OWNER": e.Owner, "MASH_ADMIN_CREDS": e.AdminCreds, "MASH_INSECURE_LOOPBACK": "1"}
	runAdmin := func(args ...string) (string, int) {
		var out bytes.Buffer
		code := run(append([]string{"admin"}, args...), envFrom(env), strings.NewReader(""), &out, &out)
		return out.String(), code
	}
	if out, code := runAdmin("init"); code != 0 {
		t.Fatal(out)
	}
	adm, err := admin.New(e.Conn(t, e.AdminCreds), e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, gu := e.Mint(t, "ghost")
	if _, err := adm.Provision(ctx, "ghost", gu, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	id, err := sap.New("acme", "team1", "ops1")
	if err != nil {
		t.Fatal(err)
	}
	_, u := e.Mint(t, "ops1")
	if _, err := adm.ProvisionIdentity(ctx, id, &store.Alias{Owner: e.Owner, Name: "ghost"}, u, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	st, err := adm.Suspend(ctx, "ghost")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"deprovision", "ghost"},
		{"deprovision", "ghost", "--user", gu},
		{"deprovision", "ghost", "--suspension", st.ID, "--drained-to", fmt.Sprint(st.LastSeq)},
	} {
		out, code := runAdmin(args...)
		// mash admin prints a deprecation banner first; the refusal is the last line.
		out = strings.TrimPrefix(out, "mash admin is deprecated and will be removed; use mashctl (same verbs, e.g. mashctl list)\n")
		if code != 1 || strings.Count(out, "\n") != 1 || strings.Count(out, "nothing was") != 1 {
			t.Fatalf("%v: %d %q", args, code, out)
		}
		for _, w := range []string{"ghost", id.Flat(), "names it as its alias", "nothing was changed", "retire or re-point " + id.Flat()} {
			if !strings.Contains(out, w) {
				t.Errorf("%v: lacks %q: %q", args, w, out)
			}
		}
	}
	if out, _ := runAdmin("list"); !strings.Contains(out, "ghost\t") {
		t.Fatalf("ghost was removed: %q", out)
	}
}
