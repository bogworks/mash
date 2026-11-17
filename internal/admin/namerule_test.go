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
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

func ruleRefusingName(t *testing.T, name string) *namerule.Rule {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rule.json")
	body := `{"version":1,"rules":[{"id":"no-` + name + `","match":{"instance":{"is":"` + name + `"}},"message":"` + name + ` is retired"}]}`
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
	_, user := e.Mint(t, ep(t, "retired"))
	_, err := a.Provision(ctx, ep(t, "retired"), user, e.Account, "", admin.Meta{})
	var ref *namerule.Refusal
	if !errors.As(err, &ref) || ref.RuleID != "no-retired" || !strings.Contains(err.Error(), "retired is retired") || !strings.Contains(err.Error(), "provision") {
		t.Fatalf("got %v", err)
	}
	if _, err := js.Stream(ctx, ep(t, "retired").InboxStream()); err == nil {
		t.Error("a refused provision created an inbox")
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if _, err := store.GetRecord(ctx, kv, "o_dev_retired"); err == nil {
		t.Error("a refused provision wrote a record")
	}
	_, user2 := e.Mint(t, ep(t, "fine"))
	if _, err := a.Provision(ctx, ep(t, "fine"), user2, e.Account, "", admin.Meta{}); err != nil {
		t.Fatalf("an allowed name was refused: %v", err)
	}
}

func TestProvisionWithoutARuleIsUnchanged(t *testing.T) {
	e, a, _ := setup(t)
	_, user := e.Mint(t, ep(t, "retired"))
	if _, err := a.WithNameRule(nil).Provision(context.Background(), ep(t, "retired"), user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckProvision(t *testing.T) {
	r := ruleRefusingName(t, "retired")
	if err := admin.CheckProvision(r, ep(t, "retired")); err == nil {
		t.Fatal("not refused")
	}
	if err := admin.CheckProvision(r, ep(t, "fine")); err != nil {
		t.Fatal(err)
	}
	if err := admin.CheckProvision(nil, ep(t, "retired")); err != nil {
		t.Fatal(err)
	}
	if err := admin.CheckProvision(r, sap.Address{Owner: "o", Group: "dev", Instance: "Bad Name"}); err != nil { // not an address: refused elsewhere
		t.Fatal(err)
	}
}

// ruleRefusing builds a rule refusing addresses whose token (owner, group or instance) is exactly value.
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

// A provision applies the name rule to all three tokens of the address, before anything is read or written.
func TestProvisionAppliesTheNameRuleToEveryToken(t *testing.T) {
	for _, c := range []struct{ token, value string }{{"owner", "banned"}, {"group", "banned"}, {"instance", "banned"}} {
		t.Run(c.token, func(t *testing.T) {
			e, a, js := setup(t)
			ctx := context.Background()
			owner, group, instance := "o", "team1", "ops1"
			switch c.token {
			case "owner":
				owner = c.value
			case "group":
				group = c.value
			default:
				instance = c.value
			}
			a = a.WithNameRule(ruleRefusing(t, c.token, c.value))
			addr := epIn(t, owner, group, instance)
			_, user := e.Mint(t, epIn(t, "o", "team1", "x"))
			_, err := a.Provision(ctx, addr, user, e.Account, "", admin.Meta{})
			var ref *namerule.Refusal
			if !errors.As(err, &ref) || ref.RuleID != "no-"+c.value || !strings.Contains(err.Error(), c.value+" is retired") {
				t.Fatalf("got %v", err)
			}
			if _, err := js.Stream(ctx, addr.InboxStream()); err == nil {
				t.Error("a refused provision created an inbox")
			}
			kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
			if _, err := store.GetRecord(ctx, kv, addr.Flat()); err == nil {
				t.Error("a refused provision wrote a record")
			}
			if markerPresent(t, js, addr.Flat()) {
				t.Error("a refused provision wrote a retired marker")
			}
			// An allowed address under the same rule is provisioned.
			ok := epIn(t, "o", "team1", "fine")
			_, u2 := e.Mint(t, ok)
			if _, err := a.Provision(ctx, ok, u2, e.Account, "", admin.Meta{}); err != nil {
				t.Fatalf("an allowed address was refused: %v", err)
			}
		})
	}
}
