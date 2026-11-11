package admin_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/namerule"
	"github.com/bogworks/mash/internal/store"
)

// Configuration A of the naming plan's negative tests (steps 1.10 and 3.1), at the provision entry point of REQ-016:
// a rule file that refuses the agent tokens cc and mash. These tests are permanent; none may be skipped or weakened.
// The full table, and the other entry points, are in internal/endpoint/namerule_configa_test.go.
//
// Provision takes a legacy endpoint name, so what it provisions is always the identity (cc, owner, name): a bare name
// is refused by no-cc, whatever the name. A three-token identity cannot be provisioned here at all (the name argument
// is a legacy name; mashctl add refuses a configured agent_token, see cmd/mashctl/namerule_configa_test.go), so its
// forms (agent cc, agent mash) are refused by the name syntax of Provision, never accepted.
func configARule(t *testing.T) *namerule.Rule {
	t.Helper()
	p := filepath.Join(t.TempDir(), "name-rule.json")
	body := `{"version":1,"rules":[` +
		`{"id":"no-cc","match":{"agent":{"is":"cc"}},"message":"the agent token cc is retired"},` +
		`{"id":"no-mash","match":{"agent":{"is":"mash"}},"message":"the agent token mash is reserved"}]}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := namerule.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestConfigAProvision(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	// An endpoint provisioned before the rule came: provisioning its name again is refused by the rule, before the
	// directory is read ("already exists" would mean it was read).
	_, early := e.Mint(t, "early")
	if _, err := a.Provision(ctx, "early", early, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	ruled := a.WithNameRule(configARule(t))
	_, user := e.Mint(t, "fresh")

	cases := []struct {
		name, ep, user string
		want           string // "no-cc", "syntax" (the name is not a legacy name) or "" (accepted)
	}{
		{"bare legacy name, no record", "fresh", user, "no-cc"},
		{"bare legacy name, record exists", "early", early, "no-cc"},
		{"bare legacy name that is a reserved agent token", "mash", user, "no-cc"},
		{"bare legacy name before the directory is read (the user key is not even valid)", "gone", "not-a-key", "no-cc"},
		{"three-token flat key with agent cc", "cc_acme_gone", user, "syntax"},
		{"three-token flat key with agent mash", "mash_acme_gone", user, "syntax"},
		{"dotted display form with agent cc", "cc.acme.gone", user, "syntax"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ruled.Provision(ctx, c.ep, c.user, e.Account, "", admin.Meta{})
			var ref *namerule.Refusal
			switch c.want {
			case "no-cc":
				if !errors.As(err, &ref) || ref.RuleID != "no-cc" ||
					!strings.Contains(err.Error(), `name rule "no-cc"`) ||
					!strings.Contains(err.Error(), "the agent token cc is retired") ||
					!strings.Contains(err.Error(), "provision") {
					t.Fatalf("got %v", err)
				}
				if strings.Contains(err.Error(), "already") || strings.Contains(err.Error(), "user public key") {
					t.Fatalf("the directory or the user was read before the rule: %v", err)
				}
			case "syntax":
				if err == nil || errors.As(err, &ref) || !strings.Contains(err.Error(), "endpoint name") {
					t.Fatalf("want the name-syntax refusal, got %v", err)
				}
			}
		})
	}
	// Nothing was created by any refused provision.
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	for _, n := range []string{"fresh", "mash", "gone", "cc_acme_gone", "mash_acme_gone"} {
		if _, err := store.GetRecord(ctx, kv, n); err == nil {
			t.Errorf("a refused provision wrote the record %q", n)
		}
		if _, err := js.Stream(ctx, "inbox-"+n); err == nil {
			t.Errorf("a refused provision created the inbox of %q", n)
		}
	}
}

// CheckProvision is the check mashctl add runs before it touches a key, a policy file or a record.
func TestConfigACheckProvision(t *testing.T) {
	r := configARule(t)
	for _, name := range []string{"fresh", "mash", "cc", "a"} {
		err := admin.CheckProvision(r, "acme", name)
		var ref *namerule.Refusal
		if !errors.As(err, &ref) || ref.RuleID != "no-cc" {
			t.Errorf("CheckProvision(%q): %v", name, err)
		}
	}
}
