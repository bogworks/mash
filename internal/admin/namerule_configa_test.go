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

// Configuration A of the naming plan's negative tests (steps 1.10 and 3.1), at the provision entry point of REQ-016:
// a rule file that refuses the groups cc and mash. These tests are permanent; none may be skipped or weakened.
// The full table, and the other entry points, are in internal/endpoint/namerule_configa_test.go.
//
// A provision of an address in a refused group is refused by the rule, before the directory or the user key is read.
func configARule(t *testing.T) *namerule.Rule {
	t.Helper()
	p := filepath.Join(t.TempDir(), "name-rule.json")
	body := `{"version":1,"rules":[` +
		`{"id":"no-cc","match":{"group":{"is":"cc"}},"message":"the group cc is retired"},` +
		`{"id":"no-mash","match":{"group":{"is":"mash"}},"message":"the group mash is reserved"}]}`
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
	// An endpoint provisioned before the rule came: provisioning its address again is refused by the rule, before the
	// directory is read ("already exists" would mean it was read).
	earlyAddr := epIn(t, "o", "cc", "early")
	_, early := e.Mint(t, earlyAddr)
	if _, err := a.Provision(ctx, earlyAddr, early, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	ruled := a.WithNameRule(configARule(t))
	_, user := e.Mint(t, epIn(t, "o", "cc", "fresh"))

	cases := []struct {
		name  string
		addr  sap.Address
		user  string
		rule  string
		clean bool // nothing may exist for the address afterwards
	}{
		{"group cc, no record", epIn(t, "o", "cc", "fresh"), user, "no-cc", true},
		{"group cc, record exists", earlyAddr, early, "no-cc", false},
		{"group mash", epIn(t, "o", "mash", "fresh"), user, "no-mash", true},
		{"group cc before the directory is read (the user key is not even valid)", epIn(t, "o", "cc", "gone"), "not-a-key", "no-cc", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ruled.Provision(ctx, c.addr, c.user, e.Account, "", admin.Meta{})
			var ref *namerule.Refusal
			if !errors.As(err, &ref) || ref.RuleID != c.rule ||
				!strings.Contains(err.Error(), `name rule "`+c.rule+`"`) ||
				!strings.Contains(err.Error(), "provision") {
				t.Fatalf("got %v", err)
			}
			if strings.Contains(err.Error(), "already") || strings.Contains(err.Error(), "user public key") {
				t.Fatalf("the directory or the user was read before the rule: %v", err)
			}
			if c.clean {
				kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
				if _, err := store.GetRecord(ctx, kv, c.addr.Flat()); err == nil {
					t.Errorf("a refused provision wrote the record %q", c.addr.Flat())
				}
				if _, err := js.Stream(ctx, c.addr.InboxStream()); err == nil {
					t.Errorf("a refused provision created the inbox of %q", c.addr.Flat())
				}
			}
		})
	}
	// An address in an allowed group is provisioned under the same rule.
	okAddr := epIn(t, "o", "dev", "fine")
	_, ok := e.Mint(t, okAddr)
	if _, err := ruled.Provision(ctx, okAddr, ok, e.Account, "", admin.Meta{}); err != nil {
		t.Fatalf("an allowed address was refused: %v", err)
	}
}

// CheckProvision is the check provisioning runs before it touches a key, a policy file or a record.
func TestConfigACheckProvision(t *testing.T) {
	r := configARule(t)
	for _, g := range []string{"cc", "mash"} {
		err := admin.CheckProvision(r, epIn(t, "acme", g, "fresh"))
		var ref *namerule.Refusal
		if !errors.As(err, &ref) || ref.RuleID != "no-"+g {
			t.Errorf("CheckProvision(group %q): %v", g, err)
		}
	}
	if err := admin.CheckProvision(r, epIn(t, "acme", "dev", "fresh")); err != nil {
		t.Errorf("an allowed group was refused: %v", err)
	}
}
