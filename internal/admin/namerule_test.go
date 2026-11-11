package admin_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/namerule"
	"github.com/bogworks/mash/internal/store"
)

func ruleRefusingName(t *testing.T, name string) *namerule.Rule {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rule.json")
	body := `{"version":1,"rules":[{"id":"no-` + name + `","match":{"name":{"is":"` + name + `"}},"message":"` + name + ` is retired"}]}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := namerule.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// (f) provision: the identity being provisioned is checked before the directory is read or anything is created.
func TestProvisionAppliesTheNameRule(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	a = a.WithNameRule(ruleRefusingName(t, "retired"))
	_, user := e.Mint(t, "retired")
	_, err := a.Provision(ctx, "retired", user, e.Account, "", admin.Meta{})
	var ref *namerule.Refusal
	if !errors.As(err, &ref) || ref.RuleID != "no-retired" || !strings.Contains(err.Error(), "retired is retired") || !strings.Contains(err.Error(), "provision") {
		t.Fatalf("got %v", err)
	}
	if _, err := js.Stream(ctx, "inbox-retired"); err == nil {
		t.Error("a refused provision created an inbox")
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if _, err := store.GetRecord(ctx, kv, "retired"); err == nil {
		t.Error("a refused provision wrote a record")
	}
	_, user2 := e.Mint(t, "fine")
	if _, err := a.Provision(ctx, "fine", user2, e.Account, "", admin.Meta{}); err != nil {
		t.Fatalf("an allowed name was refused: %v", err)
	}
}

func TestProvisionWithoutARuleIsUnchanged(t *testing.T) {
	e, a, _ := setup(t)
	_, user := e.Mint(t, "retired")
	if _, err := a.WithNameRule(nil).Provision(context.Background(), "retired", user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckProvision(t *testing.T) {
	r := ruleRefusingName(t, "retired")
	if err := admin.CheckProvision(r, "o", "retired"); err == nil {
		t.Fatal("not refused")
	}
	if err := admin.CheckProvision(r, "o", "fine"); err != nil {
		t.Fatal(err)
	}
	if err := admin.CheckProvision(nil, "o", "retired"); err != nil {
		t.Fatal(err)
	}
	if err := admin.CheckProvision(r, "o", "Bad Name"); err != nil { // not a legacy name: refused elsewhere
		t.Fatal(err)
	}
}

// ruleRefusing builds a rule refusing identities whose token (agent, owner or name) is exactly value.
func ruleRefusing(t *testing.T, token, value string) *namerule.Rule {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rule.json")
	body := `{"version":1,"rules":[{"id":"no-` + value + `","match":{"` + token + `":{"is":"` + value + `"}},"message":"` + value + ` is retired"}]}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := namerule.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// A new-form provision applies the name rule to all three tokens of the identity, before anything is read or written.
func TestProvisionIdentityAppliesTheNameRule(t *testing.T) {
	for _, c := range []struct{ token, value string }{{"owner", "banned"}, {"agent", "banned"}, {"name", "banned"}} {
		t.Run(c.token, func(t *testing.T) {
			e, a, js := setup(t)
			ctx := context.Background()
			agent, owner, name := "acme", "team1", "ops1"
			switch c.token {
			case "owner":
				owner = c.value
			case "agent":
				agent = c.value
			default:
				name = c.value
			}
			a = a.WithNameRule(ruleRefusing(t, c.token, c.value))
			id := newID(t, agent, owner, name)
			_, user := e.Mint(t, "x")
			keysBefore, streamsBefore := dirKeys(t, js), streamNames(t, js)
			_, err := a.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
			var ref *namerule.Refusal
			if !errors.As(err, &ref) || ref.RuleID != "no-"+c.value || !strings.Contains(err.Error(), c.value+" is retired") {
				t.Fatalf("got %v", err)
			}
			if _, err := js.Stream(ctx, "inbox-"+id.Flat()); err == nil {
				t.Error("a refused provision created an inbox")
			}
			kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
			if _, err := store.GetRecord(ctx, kv, id.Flat()); err == nil {
				t.Error("a refused provision wrote a record")
			}
			if markerPresent(t, js, id.Flat()) {
				t.Error("a refused provision wrote a retired marker")
			}
			if got := dirKeys(t, js); !slices.Equal(got, keysBefore) {
				t.Errorf("directory changed: %v -> %v", keysBefore, got)
			}
			if got := streamNames(t, js); !slices.Equal(got, streamsBefore) {
				t.Errorf("streams changed: %v -> %v", streamsBefore, got)
			}
			// An allowed identity under the same rule is provisioned.
			ok := newID(t, "acme", "team1", "fine")
			_, u2 := e.Mint(t, "fine")
			if _, err := a.ProvisionIdentity(ctx, ok, nil, u2, e.Account, "", admin.Meta{}); err != nil {
				t.Fatalf("an allowed identity was refused: %v", err)
			}
		})
	}
}

func TestProvisionIdentityWithoutARuleIsUnchanged(t *testing.T) {
	e, a, _ := setup(t)
	_, user := e.Mint(t, "x")
	if _, err := a.WithNameRule(nil).ProvisionIdentity(context.Background(), newID(t, "acme", "banned", "ops1"), nil, user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
}

// The rule is applied to the new identity only, not to the legacy identity it aliases.
func TestProvisionIdentityRuleIsNotAppliedToTheAlias(t *testing.T) {
	e, a, _ := setup(t)
	ctx := context.Background()
	provLegacy(t, e, a, "ghost", admin.Meta{})
	a = a.WithNameRule(ruleRefusingName(t, "ghost"))
	_, user := e.Mint(t, "x")
	if _, err := a.ProvisionIdentity(ctx, newID(t, "acme", "team1", "ops1"), &store.Alias{Owner: e.Owner, Name: "ghost"}, user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatalf("the alias was subjected to the rule: %v", err)
	}
}
