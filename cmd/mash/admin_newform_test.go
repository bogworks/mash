package main

import (
	"context"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/testenv"
)

// mash admin provisions and deprovisions by the three tokens when agent_token is set (naming plan 1.6); the endpoint it
// provisioned is then shown with its tokens byte-identical by who, and a retired identity is refused on provision.
func TestAdminProvisionsAndDeprovisionsByThreeTokens(t *testing.T) {
	e := testenv.Start(t)
	adm, err := admin.New(e.Conn(t, e.AdminCreds), e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := adm.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	adminEnv := map[string]string{"HOME": t.TempDir(), "MASH_URL": e.URL, "MASH_OWNER": e.Owner, "MASH_ADMIN_CREDS": e.AdminCreds,
		"MASH_INSECURE_LOOPBACK": "1", "MASH_AGENT_TOKEN": "ops"}
	adminAs := func(args ...string) (string, string, int) {
		var out, errb strings.Builder
		code := run(append([]string{"admin"}, args...), envFrom(adminEnv), strings.NewReader(""), &out, &errb)
		return out.String(), errb.String(), code
	}
	id, err := sap.New("acme", "team1", "recv")
	if err != nil {
		t.Fatal(err)
	}
	perms, err := admin.NewFormPermissions(id, nil)
	if err != nil {
		t.Fatal(err)
	}
	creds, user := e.MintRaw(t, id.Flat(), perms)
	out, errb, code := adminAs("provision", "recv", "--agent", "acme", "--owner", "team1", "--user", user, "--account", e.Account)
	if code != 0 || !strings.Contains(out, "provisioned acme.team1.recv:") || !strings.Contains(errb, adminDeprecation) {
		t.Fatalf("provision: %d %q %q", code, out, errb)
	}
	recvEnv := map[string]string{"HOME": t.TempDir(), "MASH_NAME": "recv", "MASH_OWNER": "team1", "MASH_AGENT_TOKEN": "acme",
		"MASH_URL": e.URL, "MASH_CREDS": creds, "MASH_SETTLE_MS": "20"}
	assertShows(t, "who", cliAs(t, recvEnv, "who"), id)

	// Legacy form with the setting on is still refused; the setting off refuses the three-token form.
	if _, errb, code := adminAs("provision", "x"); code == 0 || !strings.Contains(errb, adminAgentTokenRefusal) {
		t.Fatalf("legacy with setting: %d %q", code, errb)
	}
	delete(adminEnv, "MASH_AGENT_TOKEN")
	if _, errb, code := adminAs("deprovision", "acme.team1.recv"); code != 1 || !strings.Contains(errb, "agent_token") {
		t.Fatalf("setting off: %d %q", code, errb)
	}
	adminEnv["MASH_AGENT_TOKEN"] = "ops"
	if out, errb, code := adminAs("deprovision", "acme.team1.recv"); code != 0 || !strings.Contains(out, "deprovisioned acme.team1.recv") {
		t.Fatalf("deprovision: %d %q %q", code, out, errb)
	}
	if _, errb, code := adminAs("provision", "recv", "--agent", "acme", "--owner", "team1", "--user", user, "--account", e.Account); code != 1 || !strings.Contains(errb, "is retired") {
		t.Fatalf("retired: %d %q", code, errb)
	}
}
