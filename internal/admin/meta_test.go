package admin_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

func sp(s string) *string { return &s }

func TestProvisionMeta(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, u1 := e.Mint(t, "withmeta")
	_, u2 := e.Mint(t, "plain")
	rec, err := a.Provision(ctx, "withmeta", u1, e.Account, "n", admin.Meta{Role: "reviewer", Host: "laptop", Labels: map[string]string{"team": "acme"}})
	if err != nil || rec.Role != "reviewer" || rec.Host != "laptop" || rec.Labels["team"] != "acme" {
		t.Fatalf("%+v %v", rec, err)
	}
	if _, err := a.Provision(ctx, "plain", u2, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	ent, _ := kv.Get(ctx, "plain")
	for _, k := range []string{"role", "host", "labels"} {
		if strings.Contains(string(ent.Value()), `"`+k+`"`) {
			t.Fatalf("plain record carries %s: %s", k, ent.Value())
		}
	}
	got, err := store.GetRecord(ctx, kv, "withmeta")
	if err != nil || got.Role != "reviewer" || got.Labels["team"] != "acme" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestMetaValidation(t *testing.T) {
	e, a, _ := setup(t)
	ctx := context.Background()
	_, u := e.Mint(t, "alpha")
	for _, bad := range []string{"a\tb", "a\nb", "a\rb", strings.Repeat("x", 65), "a\x00b"} {
		if _, err := a.Provision(ctx, "alpha", u, e.Account, "", admin.Meta{Role: bad}); err == nil {
			t.Errorf("provision accepted role %q", bad)
		}
		if _, err := a.Provision(ctx, "alpha", u, e.Account, "", admin.Meta{Labels: map[string]string{"k": bad}}); err == nil {
			t.Errorf("provision accepted label value %q", bad)
		}
	}
	for _, bad := range []map[string]string{{"Bad": "x"}, {"1x": "x"}, {"a b": "x"}, {"": "x"},
		{"a": "1", "b": "1", "c": "1", "d": "1", "e": "1", "f": "1", "g": "1", "h": "1", "i": "1"}} {
		if _, err := a.Provision(ctx, "alpha", u, e.Account, "", admin.Meta{Labels: bad}); err == nil {
			t.Errorf("provision accepted labels %v", bad)
		}
	}
	if _, err := a.Provision(ctx, "alpha", u, e.Account, "", admin.Meta{Host: strings.Repeat("x", 64)}); err != nil {
		t.Fatalf("64 chars rejected: %v", err)
	}
	if _, err := a.SetMeta(ctx, "alpha", admin.MetaUpdate{Host: sp("a\tb")}); err == nil {
		t.Error("set-meta accepted a tab")
	}
	if _, err := a.SetMeta(ctx, "alpha", admin.MetaUpdate{Role: sp(strings.Repeat("x", 65))}); err == nil {
		t.Error("set-meta accepted 65 chars")
	}
}

func TestSetMeta(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, u := e.Mint(t, "alpha")
	if _, err := a.Provision(ctx, "alpha", u, e.Account, "keep me", admin.Meta{Role: "r1", Host: "h1"}); err != nil {
		t.Fatal(err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	// A field only a newer mash knows: set-meta must not drop it.
	ent, _ := kv.Get(ctx, "alpha")
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(ent.Value(), &obj)
	obj["future_field"] = json.RawMessage(`{"x":[1,2]}`)
	b, _ := json.Marshal(obj)
	if _, err := kv.Put(ctx, "alpha", b); err != nil {
		t.Fatal(err)
	}
	rec, err := a.SetMeta(ctx, "alpha", admin.MetaUpdate{Labels: map[string]string{"team": "h"}, Role: sp("r2")})
	if err != nil || rec.Role != "r2" || rec.Labels["team"] != "h" || rec.Host != "h1" || rec.Note != "keep me" {
		t.Fatalf("%+v %v", rec, err)
	}
	ent, _ = kv.Get(ctx, "alpha")
	if !strings.Contains(string(ent.Value()), `"future_field":{"x":[1,2]}`) {
		t.Fatalf("unknown field lost: %s", ent.Value())
	}
	// "" (the CLI's -) clears.
	rec, err = a.SetMeta(ctx, "alpha", admin.MetaUpdate{Host: sp("")})
	if err != nil || rec.Host != "" || rec.Role != "r2" {
		t.Fatalf("%+v %v", rec, err)
	}
	ent, _ = kv.Get(ctx, "alpha")
	if strings.Contains(string(ent.Value()), `"host"`) || !strings.Contains(string(ent.Value()), `future_field`) {
		t.Fatalf("clear: %s", ent.Value())
	}
	if _, err := a.SetMeta(ctx, "ghost", admin.MetaUpdate{Role: sp("x")}); err == nil || !strings.Contains(err.Error(), "not provisioned") {
		t.Fatalf("missing endpoint: %v", err)
	}
}

func TestSetMetaRetriesAfterAConflict(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, u := e.Mint(t, "alpha")
	if _, err := a.Provision(ctx, "alpha", u, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	race := func(n int) {
		calls := 0
		prev := admin.SetAfterMetaGet(func() {
			calls++
			if calls > n {
				return
			}
			// A concurrent writer (another set-meta) lands between our Get and Update.
			ent, _ := kv.Get(ctx, "alpha")
			var obj map[string]json.RawMessage
			_ = json.Unmarshal(ent.Value(), &obj)
			obj["host"] = json.RawMessage(`"racer"`)
			b, _ := json.Marshal(obj)
			if _, err := kv.Update(ctx, "alpha", b, ent.Revision()); err != nil {
				t.Error(err)
			}
		})
		defer admin.SetAfterMetaGet(prev)
		_, err := a.SetMeta(ctx, "alpha", admin.MetaUpdate{Role: sp("mine")})
		if n < 5 && err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if n >= 5 && (err == nil || !strings.Contains(err.Error(), "kept changing")) {
			t.Fatalf("n=%d: want give-up error, got %v", n, err)
		}
	}
	race(2)
	rec, err := store.GetRecord(ctx, kv, "alpha")
	if err != nil || rec.Role != "mine" || rec.Host != "racer" {
		t.Fatalf("a concurrent write was lost: %+v %v", rec, err)
	}
	race(99)
}

func TestDeprovisionWithMeta(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, u := e.Mint(t, "alpha")
	if _, err := a.Provision(ctx, "alpha", u, e.Account, "", admin.Meta{Role: "r", Labels: map[string]string{"team": "h"}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Deprovision(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if _, err := kv.Get(ctx, "alpha"); err == nil {
		t.Fatal("record still there")
	}
}

func TestSetMetaLabels(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, u := e.Mint(t, "alpha")
	if _, err := a.Provision(ctx, "alpha", u, e.Account, "", admin.Meta{Labels: map[string]string{"a": "1", "b": "2"}}); err != nil {
		t.Fatal(err)
	}
	// Set one, change one, clear one (empty value), leave none of the others touched.
	rec, err := a.SetMeta(ctx, "alpha", admin.MetaUpdate{Labels: map[string]string{"b": "22", "c": "3", "a": ""}})
	if err != nil || len(rec.Labels) != 2 || rec.Labels["b"] != "22" || rec.Labels["c"] != "3" {
		t.Fatalf("%+v %v", rec, err)
	}
	// A bad key or value is refused, and the record is unchanged.
	for _, bad := range []map[string]string{{"Bad": "x"}, {"k": "a\tb"}} {
		if _, err := a.SetMeta(ctx, "alpha", admin.MetaUpdate{Labels: bad}); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	// The cap applies after the merge: 2 existing + 6 = 8 is fine, one more is not.
	six := map[string]string{"d": "1", "e": "1", "f": "1", "g": "1", "h": "1", "i": "1"}
	if rec, err = a.SetMeta(ctx, "alpha", admin.MetaUpdate{Labels: six}); err != nil || len(rec.Labels) != 8 {
		t.Fatalf("%+v %v", rec, err)
	}
	if _, err := a.SetMeta(ctx, "alpha", admin.MetaUpdate{Labels: map[string]string{"j": "1"}}); err == nil {
		t.Fatal("ninth label accepted")
	}
	// Changing an existing key at the cap, or clearing one and adding one in the same update, is fine.
	if _, err := a.SetMeta(ctx, "alpha", admin.MetaUpdate{Labels: map[string]string{"b": "x", "c": "", "j": "1"}}); err != nil {
		t.Fatalf("swap at the cap: %v", err)
	}
	// Clearing every label removes the key from the stored record.
	clear := map[string]string{}
	rec, _ = a.SetMeta(ctx, "alpha", admin.MetaUpdate{Labels: map[string]string{"a": ""}})
	for k := range rec.Labels {
		clear[k] = ""
	}
	if rec, err = a.SetMeta(ctx, "alpha", admin.MetaUpdate{Labels: clear}); err != nil || len(rec.Labels) != 0 {
		t.Fatalf("%+v %v", rec, err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	ent, _ := kv.Get(ctx, "alpha")
	if strings.Contains(string(ent.Value()), `"labels"`) {
		t.Fatalf("empty labels stored: %s", ent.Value())
	}
}

func TestProvisionDescriptionAndCaps(t *testing.T) {
	e, a, _ := setup(t)
	ctx := context.Background()
	_, u := e.Mint(t, "alpha")
	rec, err := a.Provision(ctx, "alpha", u, e.Account, "", admin.Meta{Description: "runs nats tooling", Caps: []string{"zeta", "nats-accounts"}})
	if err != nil || rec.Description != "runs nats tooling" || strings.Join(rec.Caps, ",") != "nats-accounts,zeta" {
		t.Fatalf("%+v %v", rec, err)
	}
	_, u2 := e.Mint(t, "beta")
	for name, m := range map[string]admin.Meta{
		"bad cap": {Caps: []string{"Bad"}}, "dup cap": {Caps: []string{"a", "a"}},
		"nine caps": {Caps: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}},
		"tab":       {Description: "a\tb"}, "long": {Description: strings.Repeat("x", 257)},
	} {
		if _, err := a.Provision(ctx, "beta", u2, e.Account, "", m); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestSetMetaDescriptionAndCaps(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, u := e.Mint(t, "alpha")
	if _, err := a.Provision(ctx, "alpha", u, e.Account, "", admin.Meta{Role: "r", Caps: []string{"b"}}); err != nil {
		t.Fatal(err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	// Plant a field this mash does not know, as a newer mash would.
	ent, _ := kv.Get(ctx, "alpha")
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(ent.Value(), &obj)
	obj["future"] = json.RawMessage(`{"x":[1,2]}`)
	val, _ := json.Marshal(obj)
	if _, err := kv.Put(ctx, "alpha", val); err != nil {
		t.Fatal(err)
	}
	rec, err := a.SetMeta(ctx, "alpha", admin.MetaUpdate{Description: sp("does things"), AddCaps: []string{"c", "a"}})
	if err != nil || rec.Description != "does things" || strings.Join(rec.Caps, ",") != "a,b,c" || rec.Role != "r" {
		t.Fatalf("%+v %v", rec, err)
	}
	ent, _ = kv.Get(ctx, "alpha")
	if !strings.Contains(string(ent.Value()), `"future":{"x":[1,2]}`) {
		t.Fatalf("unknown field lost: %s", ent.Value())
	}
	if rec, err = a.SetMeta(ctx, "alpha", admin.MetaUpdate{DropCaps: []string{"b"}, AddCaps: []string{"d"}}); err != nil || strings.Join(rec.Caps, ",") != "a,c,d" {
		t.Fatalf("%+v %v", rec, err)
	}
	if rec, err = a.SetMeta(ctx, "alpha", admin.MetaUpdate{AddCaps: []string{"a"}, DropCaps: []string{"zz"}}); err != nil || len(rec.Caps) != 3 {
		t.Fatalf("%+v %v", rec, err)
	}
	// The limit applies after the merge: 3 + 5 = 8 is fine, one more is not.
	if rec, err = a.SetMeta(ctx, "alpha", admin.MetaUpdate{AddCaps: []string{"e", "f", "g", "h", "i"}}); err != nil || len(rec.Caps) != 8 {
		t.Fatalf("%+v %v", rec, err)
	}
	if _, err := a.SetMeta(ctx, "alpha", admin.MetaUpdate{AddCaps: []string{"j"}}); err == nil {
		t.Fatal("ninth cap accepted")
	}
	if rec, err = a.SetMeta(ctx, "alpha", admin.MetaUpdate{AddCaps: []string{"j"}, DropCaps: []string{"a"}}); err != nil || len(rec.Caps) != 8 {
		t.Fatalf("swap at the limit: %+v %v", rec, err)
	}
	for name, up := range map[string]admin.MetaUpdate{"bad cap": {AddCaps: []string{"Bad"}}, "bad uncap": {DropCaps: []string{"a b"}},
		"tab": {Description: sp("a\tb")}, "long": {Description: sp(strings.Repeat("x", 257))}} {
		if _, err := a.SetMeta(ctx, "alpha", up); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if rec, err = a.SetMeta(ctx, "alpha", admin.MetaUpdate{Description: sp(""), ClearCaps: true}); err != nil || rec.Description != "" || len(rec.Caps) != 0 || rec.Role != "r" {
		t.Fatalf("%+v %v", rec, err)
	}
	ent, _ = kv.Get(ctx, "alpha")
	if s := string(ent.Value()); strings.Contains(s, `"caps"`) || strings.Contains(s, `"description"`) || !strings.Contains(s, `"future"`) {
		t.Fatalf("after clear: %s", s)
	}
	_, _ = a.SetMeta(ctx, "alpha", admin.MetaUpdate{AddCaps: []string{"p", "q"}})
	if rec, err = a.SetMeta(ctx, "alpha", admin.MetaUpdate{ClearCaps: true, AddCaps: []string{"z"}}); err != nil || strings.Join(rec.Caps, ",") != "z" {
		t.Fatalf("%+v %v", rec, err)
	}
}
