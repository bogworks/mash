package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	madmin "github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/store"
)

const policyAppliedMsg = "a zone policy is applied: zones are removed (ADR 0091); stay on the last release that has zones until the label policy model ships"

// zonedFixture initialises the directory and provisions "alpha" in zone z1, with admin creds set; with applyPolicyFirst
// the account JWT also carries a scoped signing key, as a zone policy of an older release left it.
func zonedFixture(t *testing.T, e *applyEnv, applyPolicyFirst bool) {
	t.Helper()
	e.env["MASH_ADMIN_CREDS"] = e.pl.Creds
	if out, errb, code := e.run("init"); code != 0 {
		t.Fatalf("init: %d %s %s", code, out, errb)
	}
	cfg, err := config.Load(func(k string) (string, bool) { v, ok := e.env[k]; return v, ok }, e.root)
	if err != nil {
		t.Fatal(err)
	}
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAdmin()
	kp, _ := nkeys.CreateUser()
	pub, _ := kp.PublicKey()
	if _, err := adm.Provision(ctx, "alpha", pub, e.acctPub, "", madmin.Meta{}); err != nil {
		t.Fatal(err)
	}
	js, _ := jetstream.New(adm.Conn())
	makeZoned(t, js, "alice", "alpha", "z1")
	if _, err := js.Publish(ctx, "agents.mail.cc.alice.z1.alpha", []byte("queued")); err != nil {
		t.Fatal(err)
	}
	if applyPolicyFirst { // a zone policy of an older release: the account JWT carries a scoped signing key
		e.applyZonePolicy()
	}
}

// makeZoned rewrites the provisioned endpoint name as a zoned release left it: the record carries the zone and the
// six-token mail subject, and the inbox stream takes the five-token filter and the zone's. Nothing in mash builds such
// a record any more (ADR 0091).
func makeZoned(t *testing.T, js jetstream.JetStream, owner, name, zone string) {
	t.Helper()
	ctx := context.Background()
	kv, err := js.KeyValue(ctx, store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	ent, err := kv.Get(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(ent.Value(), &obj); err != nil {
		t.Fatal(err)
	}
	obj["zone"] = json.RawMessage(`"` + zone + `"`)
	obj["mail_subject"] = json.RawMessage(`"agents.mail.cc.` + owner + `.` + zone + `.` + name + `"`)
	raw, _ := json.Marshal(obj)
	if _, err := kv.Update(ctx, name, raw, ent.Revision()); err != nil {
		t.Fatal(err)
	}
	st, err := js.Stream(ctx, "inbox-"+name)
	if err != nil {
		t.Fatal(err)
	}
	cfg := st.CachedInfo().Config
	cfg.Subjects = []string{"agents.mail.*." + owner + "." + name, "agents.mail.*." + owner + "." + zone + "." + name}
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestUnzoneRefusesWhileAZonePolicyIsApplied(t *testing.T) {
	e := newApplyEnv(t)
	zonedFixture(t, e, true)
	out, errb, code := e.run("unzone", "alpha")
	if code != 1 || !strings.Contains(errb, policyAppliedMsg) || strings.Contains(out, "unzoned") {
		t.Fatalf("unzone: %d\n%s\n%s", code, out, errb)
	}
	// Nothing changed: the record is still zoned.
	adm := openTestAdmin(t, e)
	recs, _ := adm.List(context.Background())
	if err := store.FirstZonedRecord(recs); err == nil || !strings.Contains(err.Error(), "mashctl unzone") {
		t.Fatalf("record was changed or the guard text lacks the verb: %v", err)
	}
}

func openTestAdmin(t *testing.T, e *applyEnv) *madmin.Admin {
	t.Helper()
	cfg, err := config.Load(func(k string) (string, bool) { v, ok := e.env[k]; return v, ok }, e.root)
	if err != nil {
		t.Fatal(err)
	}
	adm, _, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeAdmin)
	return adm
}

func TestUnzoneWithNoPolicyMovesTheEndpointAndRerunsAsANoOp(t *testing.T) {
	e := newApplyEnv(t)
	zonedFixture(t, e, false)
	// Detection is false on an account with no zone policy.
	if applied, err := apply.AppliedZonePolicy(context.Background(), applyResolver(t, e), e.ks, 0); err != nil || applied {
		t.Fatalf("AppliedZonePolicy before apply: %v %v", applied, err)
	}
	out, errb, code := e.run("unzone", "alpha")
	if code != 0 || !strings.Contains(out, "unzoned alpha") || !strings.Contains(out, "permissions alpha --nsc") {
		t.Fatalf("unzone: %d\n%s\n%s", code, out, errb)
	}
	out, errb, code = e.run("unzone", "alpha")
	if code != 0 || !strings.Contains(out, "already five-token") {
		t.Fatalf("rerun: %d\n%s\n%s", code, out, errb)
	}
	if _, errb, code := e.run("unzone", "ghost"); code != 1 || !strings.Contains(errb, "not provisioned") {
		t.Fatalf("unknown: %d %s", code, errb)
	}
	// After unzone the policy-free account still has none, and the directory has no zoned record.
	recs, _ := openTestAdmin(t, e).List(context.Background())
	if err := store.FirstZonedRecord(recs); err != nil {
		t.Fatal(err)
	}
	// The account is still free of a zone policy afterwards.
	if applied, err := apply.AppliedZonePolicy(context.Background(), applyResolver(t, e), e.ks, 0); err != nil || applied {
		t.Fatalf("AppliedZonePolicy after unzone: %v %v", applied, err)
	}
}

func applyResolver(t *testing.T, e *applyEnv) apply.Resolver {
	t.Helper()
	cfg, err := config.Load(func(k string) (string, bool) { v, ok := e.env[k]; return v, ok }, e.root)
	if err != nil {
		t.Fatal(err)
	}
	nc, err := connectSys(cfg, "", func(k string) string { return e.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return apply.NATS{Conn: nc}
}

// With no system credentials the applied-policy check cannot be made: unzone refuses and changes nothing. There is no
// flag that skips the check: --no-policy-check is an unknown argument and changes nothing either.
func TestUnzoneWithoutSysCredsRefusesAndHasNoBypass(t *testing.T) {
	e := newApplyEnv(t)
	zonedFixture(t, e, false)
	// The default sys.creds is found next to the admin creds: put a copy of the admin creds where there is none.
	raw, err := os.ReadFile(e.pl.Creds)
	if err != nil {
		t.Fatal(err)
	}
	alone := filepath.Join(t.TempDir(), "admin.creds")
	if err := os.WriteFile(alone, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	e.env["MASH_SYS_CREDS"], e.env["MASH_ADMIN_CREDS"] = "", alone
	out, errb, code := e.run("unzone", "alpha")
	if code != 1 || !strings.Contains(errb, "cannot check whether a zone policy is applied") || strings.Contains(errb, "--no-policy-check") || strings.Contains(out, "unzoned") {
		t.Fatalf("unzone: %d\n%s\n%s", code, out, errb)
	}
	recs, _ := openTestAdmin(t, e).List(context.Background())
	if err := store.FirstZonedRecord(recs); err == nil {
		t.Fatal("a refused unzone changed the record")
	}
	out, errb, code = e.run("unzone", "alpha", "--no-policy-check")
	if code != 2 || !strings.Contains(errb, "unknown") || strings.Contains(out, "unzoned") || strings.Contains(errb, "skipping") {
		t.Fatalf("--no-policy-check: %d\n%s\n%s", code, out, errb)
	}
	recs, _ = openTestAdmin(t, e).List(context.Background())
	if err := store.FirstZonedRecord(recs); err == nil {
		t.Fatal("--no-policy-check changed the record")
	}
}
