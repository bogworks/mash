package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/store"
)

// writeUserCreds writes a creds file of a fresh user (signed by a fresh account key) into dir, mode 0600, and returns
// its path. It stands in for the system credentials of a test server that has none.
func writeUserCreds(t *testing.T, dir string) string {
	t.Helper()
	path, _ := writeUserCredsKey(t, dir)
	return path
}

// writeUserCredsKey is writeUserCreds that also returns the user's public key.
func writeUserCredsKey(t *testing.T, dir string) (path, pub string) {
	t.Helper()
	akp, _ := nkeys.CreateAccount()
	ukp, _ := nkeys.CreateUser()
	pub, _ = ukp.PublicKey()
	seed, _ := ukp.Seed()
	tok, err := jwt.NewUserClaims(pub).Encode(akp)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jwt.FormatUserConfig(tok, seed)
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, "sys.creds")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, pub
}

// userPub is a fresh nkey public key of the kind an outside system would hold.
func userPub(t *testing.T) string {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	return pub
}

// digests is the SHA-256 of every file under root that skip does not name, keyed by path.
func digests(t *testing.T, root string, skip func(rel string) bool) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if skip != nil && skip(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			out[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = fmt.Sprintf("%x", sha256.Sum256(b))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// adminOf is an Admin on the addEnv server.
func (h *addEnv) adminOf() *admin.Admin {
	h.t.Helper()
	a, err := admin.New(h.e.Conn(h.t, h.e.AdminCreds), h.e.Owner)
	if err != nil {
		h.t.Fatal(err)
	}
	return a
}

// adoptAs runs `policy adopt --submitter key` and requires it to succeed.
func (h *addEnv) adoptAs(key string) string {
	h.t.Helper()
	out, errb, code := h.run("policy", "adopt", "--submitter", key, "--sys-creds", writeUserCreds(h.t, h.t.TempDir()))
	if code != 0 {
		h.t.Fatalf("policy adopt: %d %q %q", code, out, errb)
	}
	return out
}

// state is everything an adopted account's refused command must leave alone: the files of the policy, the key store
// and the creds directory, the directory records and the issue record.
func (h *addEnv) state() string {
	h.t.Helper()
	d := digests(h.t, h.dir, nil)
	recs := h.records()
	issued, _, err := h.adminOf().Issued(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	var keys []string
	for k, v := range d {
		keys = append(keys, k+"="+v)
	}
	slices.Sort(keys)
	return strings.Join(keys, "\n") + "\nrecords=" + strings.Join(recs, ",") + fmt.Sprintf("\nissued=%d", len(issued))
}

func TestPolicyAdoptWritesTheMarker(t *testing.T) {
	e := newApplyEnv(t, twoZones)
	sub := userPub(t)

	if out, errb, code := e.run("policy", "adopt", "--show"); code != 0 || strings.TrimSpace(out) != "not adopted" || errb != "" {
		t.Fatalf("show before: %d %q %q", code, out, errb)
	}
	out, errb, code := e.run("policy", "adopt", "--submitter", sub)
	if code != 0 || !strings.Contains(out, "adopted this account") || !strings.Contains(out, sub) || !strings.Contains(out, "un-adoption is not supported yet") {
		t.Fatalf("adopt: %d %q %q", code, out, errb)
	}

	// The marker, through the admin API as another reader would read it.
	cfg := config.Config{URL: e.srv.ClientURL(), Owner: "alice", AdminCreds: e.env["MASH_ADMIN_CREDS"], InsecureLoopback: true}
	a, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAdmin()
	m, found, err := a.Adoption(ctx)
	if err != nil || !found {
		t.Fatalf("%v %v", found, err)
	}
	by, err := ctl.AdminUserKey(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if m.V != 1 || m.Submitter != sub || m.Account != e.acctPub || m.By != by || m.Action != "adopt" || m.NextSubmitter != "" || m.AdoptedAt == "" {
		t.Fatalf("marker %+v (account %s, admin user %s)", m, e.acctPub, by)
	}
	if err := store.VerifyAdoptionBucket(ctx, jsOf(t, a)); err != nil {
		t.Fatalf("the bucket is not hardened: %v", err)
	}

	// show, text and JSON.
	out, _, code = e.run("policy", "adopt", "--show")
	if code != 0 || !strings.Contains(out, "adopted: list sets come only from the submitter "+sub) || !strings.Contains(out, "action adopt") {
		t.Fatalf("show: %d %q", code, out)
	}
	out, _, code = e.run("policy", "adopt", "--show", "--json")
	var v struct {
		Adopted bool
		Marker  store.Marker
	}
	if code != 0 || json.Unmarshal([]byte(out), &v) != nil || !v.Adopted || !reflect.DeepEqual(v.Marker, m) {
		t.Fatalf("show --json: %d %q %+v", code, out, v)
	}

	// The same key again is a no-op that says so.
	kv, _ := jsOf(t, a).KeyValue(ctx, store.AdoptionBucket)
	before, _ := kv.Get(ctx, store.AdoptionKey)
	out, _, code = e.run("policy", "adopt", "--submitter", sub)
	if code != 0 || !strings.Contains(out, "already adopted") || !strings.Contains(out, "nothing was changed") {
		t.Fatalf("again: %d %q", code, out)
	}
	if after, _ := kv.Get(ctx, store.AdoptionKey); after.Revision() != before.Revision() {
		t.Error("an adopt with the same key wrote a revision")
	}

	// A different key is the recovery rotation.
	sub2 := userPub(t)
	out, _, code = e.run("policy", "adopt", "--submitter", sub2)
	if code != 0 || !strings.Contains(out, "recovery rotation") || !strings.Contains(out, sub) || !strings.Contains(out, sub2) {
		t.Fatalf("recover: %d %q", code, out)
	}
	out, _, _ = e.run("policy", "adopt", "--show")
	if !strings.Contains(out, "submitter "+sub2) || !strings.Contains(out, "action recover") || strings.Contains(out, sub+"\n") {
		t.Fatalf("show after recover: %q", out)
	}
}

func jsOf(t *testing.T, a *admin.Admin) jetstream.JetStream {
	t.Helper()
	js, err := jetstream.New(a.Conn())
	if err != nil {
		t.Fatal(err)
	}
	return js
}

func TestPolicyAdoptRefusals(t *testing.T) {
	e := newApplyEnv(t, twoZones)
	seed := func() string {
		kp, _ := nkeys.CreateUser()
		s, _ := kp.Seed()
		return string(s)
	}()
	_, op, err := e.ks.Operator()
	if err != nil {
		t.Fatal(err)
	}
	_, sys, err := e.ks.System()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AdminCreds: e.env["MASH_ADMIN_CREDS"]}
	adminUser, err := ctl.AdminUserKey(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		key  string
		code int
		text string
	}{
		"not a key":      {"hello", 2, "not an nkey public key"},
		"a seed":         {seed, 2, "not an nkey public key"},
		"the account":    {e.acctPub, 1, "the account's own key"},
		"the operator":   {op, 1, "the operator key"},
		"the system":     {sys, 1, "the system account key"},
		"the admin user": {adminUser, 1, "the admin user's key"},
	}
	for name, c := range cases {
		out, errb, code := e.run("policy", "adopt", "--submitter", c.key)
		if code != c.code || !strings.Contains(errb, c.text) || out != "" {
			t.Errorf("%s: %d %q %q", name, code, out, errb)
		}
		if strings.Contains(errb, c.key) {
			t.Errorf("%s: the refusal echoes the value", name)
		}
	}
	if out, _, _ := e.run("policy", "adopt", "--show"); strings.TrimSpace(out) != "not adopted" {
		t.Errorf("a refused adopt wrote a marker: %q", out)
	}
	// Any valid nkey public key type is accepted: the contract does not fix the prefix.
	kp, _ := nkeys.CreateOperator()
	other, _ := kp.PublicKey()
	if _, errb, code := e.run("policy", "adopt", "--submitter", other); code != 0 {
		t.Errorf("an operator-type key that mash does not hold: %d %q", code, errb)
	}
	for _, args := range [][]string{{"policy", "adopt"}, {"policy", "adopt", "--show", "--submitter", other}, {"policy", "adopt", "--json"}, {"policy", "adopt", "--submitter"}, {"policy", "adopt", "--bogus"}} {
		if _, _, code := e.run(args...); code != 2 {
			t.Errorf("%v: exit %d, want the usage code 2", args, code)
		}
	}
}

// The admin credentials must belong to the key store's account: admin creds issued for another account are refused
// before anything is written, and no marker exists afterwards.
func TestPolicyAdoptRefusesAdminCredsOfAnotherAccount(t *testing.T) {
	e := newApplyEnv(t, twoZones)
	// A creds file of a user issued by a fresh account key, so its account is not the key store's.
	orig := e.env["MASH_ADMIN_CREDS"]
	e.env["MASH_ADMIN_CREDS"] = writeUserCreds(t, t.TempDir())
	out, errb, code := e.run("policy", "adopt", "--submitter", userPub(t))
	want := "the admin credentials belong to another account than the key store's account key; nothing was changed"
	if code != 1 || out != "" || !strings.Contains(errb, want) {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	e.env["MASH_ADMIN_CREDS"] = orig
	if out, _, _ := e.run("policy", "adopt", "--show"); strings.TrimSpace(out) != "not adopted" {
		t.Errorf("a refused adopt wrote a marker: %q", out)
	}
}

// A command and what to run it with on a fresh account.
type guarded struct {
	name string
	args []string
	pre  func(h *addEnv) // prepares the account the command runs on
	// failsLater is true for a command that, on an account that is not adopted, still stops for a reason of its own
	// after the guard (reissue here has no system credentials to check revocation with).
	failsLater bool
	// build, when set, makes the arguments from the prepared account (provision needs a minted user key).
	build func(h *addEnv) []string
}

// provisionWith is the arguments of `provision a2 <extra>` with a user minted on the account.
func provisionWith(extra ...string) func(h *addEnv) []string {
	return func(h *addEnv) []string {
		_, u := h.e.Mint(h.t, "a2")
		return append([]string{"provision", "a2", "--user", u, "--account", h.e.Account}, extra...)
	}
}

func provisionB(h *addEnv) { h.provisionDeclared("b") }

func addEnvGuarded() []guarded {
	return []guarded{
		{"add", []string{"add", "c", "--label", "env=prod"}, nil, false, nil},
		{"reissue", []string{"reissue", "a"}, func(h *addEnv) { h.seed("a") }, true, nil},
		{"label", []string{"label", "a", "env=prod"}, nil, false, nil},
		{"label --sync", []string{"label", "a", "env=prod", "--sync"}, nil, false, nil},
		{"label --dry-run", []string{"label", "a", "env=prod", "--dry-run"}, nil, false, nil},
		{"unlabel", []string{"unlabel", "a", "team"}, nil, false, nil},
		{"retire", []string{"retire", "b"}, provisionB, false, nil},
		{"set-meta --label", []string{"set-meta", "b", "--label", "k=v"}, provisionB, false, nil},
		{"provision --label", []string{"provision"}, nil, false, provisionWith("--label", "k=v")},
	}
}

// Every guarded command is refused on an adopted account and changes nothing: no policy file edit, no key, no creds
// file, no directory record, no issue record.
func TestAdoptedAccountRefusesEveryGuardedCommand(t *testing.T) {
	for _, g := range addEnvGuarded() {
		t.Run(g.name, func(t *testing.T) {
			h := newFleetEnv(t, addPolicy)
			if g.pre != nil {
				g.pre(h)
			}
			sub := userPub(t)
			h.adoptAs(sub)
			before := h.state()
			args := g.args
			if g.build != nil {
				args = g.build(h)
			}
			out, errb, code := h.run(args...)
			verb := args[0]
			want := "mashctl: " + verb + " is refused: this account is adopted by an outside system (submitter " + sub[:8] + "...): list sets come only through the list-set path; nothing was changed"
			if code != 1 || !strings.Contains(errb, want) || strings.Contains(out, "added") || strings.Contains(out, "labeled") {
				t.Fatalf("%d\nstdout %q\nstderr %q\nwant %q", code, out, errb, want)
			}
			if strings.Contains(errb, sub) {
				t.Error("the refusal shows the whole key")
			}
			if after := h.state(); after != before {
				t.Errorf("a refused command changed the account:\nbefore\n%s\nafter\n%s", before, after)
			}
		})
	}
}

// The same commands on an account that is not adopted work as they did.
func TestUnadoptedAccountRunsEveryGuardedCommand(t *testing.T) {
	for _, g := range addEnvGuarded() {
		t.Run(g.name, func(t *testing.T) {
			h := newFleetEnv(t, addPolicy)
			if g.pre != nil {
				g.pre(h)
			}
			args := g.args
			if g.build != nil {
				args = g.build(h)
			}
			out, errb, code := h.run(args...)
			if (code != 0 && !g.failsLater) || strings.Contains(out+errb, "adopted by an outside system") || strings.Contains(out+errb, "is refused") {
				t.Fatalf("%d %q %q", code, out, errb)
			}
		})
	}
}

// policy apply, policy repair (and their dry runs) are refused with nothing pushed, no key made and the file as it
// was; policy diff is read-only and still works; both run on an account that is not adopted.
func TestAdoptedAccountRefusesApplyAndRepair(t *testing.T) {
	state := func(e *applyEnv) string {
		d := digests(t, e.keyDir, nil)
		var keys []string
		for k, v := range d {
			keys = append(keys, k+"="+v)
		}
		slices.Sort(keys)
		b, _ := os.ReadFile(e.policy)
		return strings.Join(keys, "\n") + "\npolicy=" + string(b) + "\njwt=" + e.accountJWT()
	}
	rows := []struct {
		name string
		args []string
		verb string
	}{
		{"apply", []string{"policy", "apply"}, "policy apply"},
		{"apply --dry-run", []string{"policy", "apply", "--dry-run"}, "policy apply"},
		{"repair", []string{"policy", "repair"}, "policy repair"},
		{"repair --dry-run", []string{"policy", "repair", "--dry-run"}, "policy repair"},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			e := newApplyEnv(t, twoZones)
			// Not adopted: the command goes on as before (repair and, with zones removed, apply stop at a directory this
			// server never had made: the zoned-directory gate fails closed, so only the adopted-account refusal is ruled out).
			if out, errb, code := e.run(r.args...); strings.Contains(out+errb, "adopted by an outside system") || strings.Contains(out+errb, "is refused") {
				t.Fatalf("not adopted: %d %q %q", code, out, errb)
			}
			e = newApplyEnv(t, twoZones)
			sub := userPub(t)
			if _, errb, code := e.run("policy", "adopt", "--submitter", sub); code != 0 {
				t.Fatal(errb)
			}
			before := state(e)
			out, errb, code := e.run(r.args...)
			want := "mashctl: " + r.verb + " is refused: this account is adopted by an outside system (submitter " + sub[:8] + "...): list sets come only through the list-set path; nothing was changed"
			if code != 1 || !strings.Contains(errb, want) {
				t.Fatalf("%d %q %q", code, out, errb)
			}
			if after := state(e); after != before {
				t.Errorf("a refused %s changed the account:\n%s\n---\n%s", r.name, before, after)
			}
		})
	}
	// Read-only: diff, drift, show, check, explain, test.
	e := newApplyEnv(t, twoZones)
	if _, errb, code := e.run("policy", "adopt", "--submitter", userPub(t)); code != 0 {
		t.Fatal(errb)
	}
	for _, args := range [][]string{{"policy", "drift"}, {"policy", "check"}, {"policy", "test"}} {
		if out, errb, code := e.run(args...); code == 1 && strings.Contains(out+errb, "adopted") {
			t.Errorf("%v was refused on an adopted account: %q %q", args, out, errb)
		}
	}
}

// Read-only commands, and deprovision (operator housekeeping of one endpoint's inbox), work on an adopted account.
func TestAdoptedAccountStillAllowsReadsAndDeprovision(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
	h.provisionDeclared("b")
	h.adoptAs(userPub(t))
	for _, args := range [][]string{{"list"}, {"audit"}, {"issued", "list"}, {"policy", "check"}, {"policy", "test"}, {"policy", "adopt", "--show"}, {"suspended", "b"}} {
		out, errb, code := h.run(args...)
		if strings.Contains(out+errb, "adopted by an outside system") || (code != 0 && code != 3) {
			t.Errorf("%v: %d %q %q", args, code, out, errb)
		}
	}
	out, errb, code := h.run("deprovision", "b")
	if code != 0 || strings.Contains(out+errb, "adopted by an outside system") {
		t.Errorf("deprovision: %d %q %q", code, out, errb)
	}
}

// A marker that cannot be read refuses every guarded command: fail closed.
func TestUnreadableMarkerRefusesGuardedCommands(t *testing.T) {
	garbage := func(h *addEnv) {
		a := h.adminOf()
		kv, err := jsOf(t, a).KeyValue(context.Background(), store.AdoptionBucket)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := kv.Put(context.Background(), store.AdoptionKey, []byte("not a marker")); err != nil {
			t.Fatal(err)
		}
	}
	unhardened := func(h *addEnv) { // the bucket as a plain key-value create makes it: it allows rollup and deletes
		a := h.adminOf()
		js := jsOf(t, a)
		ctx := context.Background()
		if err := js.DeleteStream(ctx, store.AdoptionStreamName); err != nil {
			t.Fatal(err)
		}
		if _, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: store.AdoptionBucket, History: 64}); err != nil {
			t.Fatal(err)
		}
	}
	for name, breakIt := range map[string]func(*addEnv){"an unreadable value": garbage, "an unhardened bucket": unhardened} {
		for _, g := range addEnvGuarded() {
			t.Run(name+"/"+g.name, func(t *testing.T) {
				h := newFleetEnv(t, addPolicy)
				if g.pre != nil {
					g.pre(h)
				}
				h.adoptAs(userPub(t))
				breakIt(h)
				before := h.state()
				args := g.args
				if g.build != nil {
					args = g.build(h)
				}
				out, errb, code := h.run(args...)
				if code != 1 || !strings.Contains(errb, "is refused: cannot tell whether this account is adopted") || !strings.Contains(errb, "nothing was changed") {
					t.Fatalf("%d %q %q", code, out, errb)
				}
				if h.state() != before {
					t.Error("a refused command changed the account")
				}
			})
		}
	}
}

// With a server configured, an admin connection that cannot be made refuses (cannot tell); with no server configured
// at all, an edit of the policy file alone is allowed and says nothing about adoption.
func TestGuardWithoutAConnection(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
	h.env["MASH_ADMIN_CREDS"] = filepath.Join(h.dir, "no-such.creds")
	before := h.state()
	_, errb, code := h.run("label", "a", "env=prod")
	if code != 1 || !strings.Contains(errb, "is refused: cannot tell whether this account is adopted") {
		t.Fatalf("a configured server that cannot be asked: %d %q", code, errb)
	}
	if h.state() != before {
		t.Error("a refused label changed the account")
	}

	delete(h.env, "MASH_URL")
	delete(h.env, "MASH_ADMIN_CREDS")
	out, errb, code := h.run("label", "a", "env=prod")
	if code != 0 || strings.Contains(out+errb, "adopted by an outside system") || !strings.Contains(h.policyText(), "env: prod") {
		t.Fatalf("an offline policy-file edit: %d %q %q\n%s", code, out, errb, h.policyText())
	}
}

// The marker records the admin user's and the system user's keys, by public key, and every --exempt key; a second run
// behaves as the maintainers ruled; the marker's JSON holds no seed.
func TestPolicyAdoptRecordsTheExemptKeys(t *testing.T) {
	e := newApplyEnv(t, twoZones)
	sub := userPub(t)
	adminUser, err := ctl.AdminUserKey(config.Config{AdminCreds: e.env["MASH_ADMIN_CREDS"]})
	if err != nil {
		t.Fatal(err)
	}
	sysUser, err := credsUserKey(e.env["MASH_SYS_CREDS"])
	if err != nil {
		t.Fatal(err)
	}
	svc1, svc2 := userPub(t), userPub(t)
	exempt := func() []string {
		out, _, code := e.run("policy", "adopt", "--show", "--json")
		var v struct{ Marker store.Marker }
		if code != 0 || json.Unmarshal([]byte(out), &v) != nil {
			t.Fatalf("show --json: %d %q", code, out)
		}
		if seedToken.MatchString(out) || strings.Contains(out, "seed") { // a user seed token, not any text with "SU" in a random key
			t.Errorf("the marker output mentions a seed: %q", out)
		}
		return v.Marker.Exempt
	}
	sorted := func(k ...string) []string { slices.Sort(k); return slices.Compact(k) }

	if out, errb, code := e.run("policy", "adopt", "--submitter", sub, "--exempt", svc2, "--exempt="+svc1, "--exempt", svc1); code != 0 {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if got, want := exempt(), sorted(adminUser, sysUser, svc1, svc2); !slices.Equal(got, want) {
		t.Fatalf("exempt %v, want %v", got, want)
	}
	out, _, _ := e.run("policy", "adopt", "--show")
	for _, k := range []string{adminUser, sysUser, svc1, svc2} {
		if !strings.Contains(out, k) {
			t.Errorf("--show lacks the exempt key %s: %q", k, out)
		}
	}

	// The same submitter and the same set: nothing written.
	out, _, code := e.run("policy", "adopt", "--submitter", sub, "--exempt", svc1, "--exempt", svc2)
	if code != 0 || !strings.Contains(out, "nothing was changed") {
		t.Fatalf("same: %d %q", code, out)
	}
	// The same submitter and another set: a new adopt revision.
	out, _, code = e.run("policy", "adopt", "--submitter", sub, "--exempt", svc1)
	if code != 0 || !strings.Contains(out, "exempt keys updated") {
		t.Fatalf("updated: %d %q", code, out)
	}
	if got, want := exempt(), sorted(adminUser, sysUser, svc1); !slices.Equal(got, want) {
		t.Fatalf("exempt %v, want %v", got, want)
	}
	if out, _, _ := e.run("policy", "adopt", "--show"); !strings.Contains(out, "action adopt") {
		t.Errorf("an exempt update is an adopt revision: %q", out)
	}
	// A different submitter is a recovery and refreshes the set.
	out, _, code = e.run("policy", "adopt", "--submitter", userPub(t), "--exempt", svc2)
	if code != 0 || !strings.Contains(out, "recovery rotation") {
		t.Fatalf("recover: %d %q", code, out)
	}
	if got, want := exempt(), sorted(adminUser, sysUser, svc2); !slices.Equal(got, want) {
		t.Fatalf("exempt %v, want %v", got, want)
	}
}

// A missing system creds file refuses with nothing written; a malformed --exempt and the keys mash holds are refused
// without being echoed.
func TestPolicyAdoptExemptRefusals(t *testing.T) {
	e := newApplyEnv(t, twoZones)
	sub := userPub(t)
	_, op, err := e.ks.Operator()
	if err != nil {
		t.Fatal(err)
	}
	seedKP, _ := nkeys.CreateUser()
	seed, _ := seedKP.Seed()
	bad := func(args ...string) (string, int) {
		out, errb, code := e.run(append([]string{"policy", "adopt", "--submitter", sub}, args...)...)
		if out != "" {
			t.Errorf("%v wrote %q", args, out)
		}
		return errb, code
	}

	for name, c := range map[string]struct {
		args []string
		code int
		text string
	}{
		"a missing sys creds": {[]string{"--sys-creds", filepath.Join(t.TempDir(), "none.creds")}, 1, "system account credentials"},
		"not a key":           {[]string{"--exempt", "hello"}, 2, "--exempt is not an nkey user public key"},
		"a seed":              {[]string{"--exempt", string(seed)}, 2, "--exempt is not an nkey user public key"},
		"an operator key":     {[]string{"--exempt", op}, 2, "--exempt is not an nkey user public key"},
		"the account key":     {[]string{"--exempt", e.acctPub}, 2, "--exempt is not an nkey user public key"},
		"no value":            {[]string{"--exempt"}, 2, "--exempt needs"},
	} {
		errb, code := bad(c.args...)
		if code != c.code || !strings.Contains(errb, c.text) {
			t.Errorf("%s: %d %q", name, code, errb)
		}
		for _, v := range c.args {
			if (v == op || v == string(seed) || v == "hello") && strings.Contains(errb, v) {
				t.Errorf("%s: the refusal echoes the value", name)
			}
		}
	}
	// No system creds anywhere: neither the flag, the environment nor a sys.creds beside the admin creds.
	e.env["MASH_SYS_CREDS"] = ""
	raw, err := os.ReadFile(e.env["MASH_ADMIN_CREDS"])
	if err != nil {
		t.Fatal(err)
	}
	lone := filepath.Join(t.TempDir(), "admin.creds") // the admin creds with no sys.creds beside them
	if err := os.WriteFile(lone, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	e.env["MASH_ADMIN_CREDS"] = lone
	if errb, code := bad(); code != 1 || !strings.Contains(errb, "no system account credentials") || !strings.Contains(errb, "nothing was changed") {
		t.Errorf("no system creds: %d %q", code, errb)
	}
	if out, _, _ := e.run("policy", "adopt", "--show"); strings.TrimSpace(out) == "" || strings.Contains(out, "adopted: ") {
		t.Errorf("a refused adopt wrote a marker: %q", out)
	}
}

// A marker written before the exempt field existed is read as empty and --show says what to do.
func TestPolicyAdoptShowsAnOlderMarker(t *testing.T) {
	e := newApplyEnv(t, twoZones)
	sub := userPub(t)
	if _, errb, code := e.run("policy", "adopt", "--submitter", sub); code != 0 {
		t.Fatal(errb)
	}
	cfg := config.Config{URL: e.srv.ClientURL(), Owner: "alice", AdminCreds: e.env["MASH_ADMIN_CREDS"], InsecureLoopback: true}
	a, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAdmin()
	m, _, _ := a.Adoption(ctx)
	old := fmt.Sprintf(`{"v":1,"account":%q,"submitter":%q,"adopted_at":%q,"by":%q,"action":"adopt"}`, m.Account, m.Submitter, m.AdoptedAt, m.By)
	kv, _ := jsOf(t, a).KeyValue(ctx, store.AdoptionBucket)
	if _, err := kv.Put(ctx, store.AdoptionKey, []byte(old)); err != nil {
		t.Fatal(err)
	}
	out, errb, code := e.run("policy", "adopt", "--show")
	if code != 0 || !strings.Contains(out, "exempt: none recorded: run adopt again before the reconcile") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	// Running adopt again with the same submitter records the set.
	if out, errb, code := e.run("policy", "adopt", "--submitter", sub); code != 0 || !strings.Contains(out, "exempt keys updated") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
}

// provision without --label and --zone stays allowed on an adopted account.
func TestAdoptedAccountAllowsAPlainProvision(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
	h.adoptAs(userPub(t))
	out, errb, code := h.run(provisionWith()(h)...)
	if code != 0 || strings.Contains(out+errb, "is refused") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
}

// seedToken matches a user seed: the prefix SU and 56 base32 characters, as a whole token.
var seedToken = regexp.MustCompile(`\bSU[A-Z2-7]{56}\b`)
