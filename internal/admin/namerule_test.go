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
