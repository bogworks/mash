package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/natsauth"
)

// seedFile writes kp's seed to a 0600 file named name in dir and returns its path. The seed is never printed.
func seedFile(t *testing.T, dir, name string, kp nkeys.KeyPair) string {
	t.Helper()
	seed, err := kp.Seed()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, append(append([]byte(nil), seed...), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// connectsWith reports whether the server takes a connection with the creds file.
func connectsWith(t *testing.T, url, creds string) bool {
	t.Helper()
	nc, err := nats.Connect(url, natsauth.UserCredentials(creds), nats.MaxReconnects(0), nats.Timeout(15*time.Second))
	if err != nil {
		return false
	}
	nc.Close()
	return true
}

// credsClaims reads the user claims of a creds file.
func credsClaims(t *testing.T, path string) *jwt.UserClaims {
	t.Helper()
	uc, err := userClaims(mustRead(t, path))
	if err != nil {
		t.Fatal(err)
	}
	return uc
}

// initStrict sets up a fresh key store on the strict server e. withSigning gives the setup file signing_key.
func initStrict(t *testing.T, e *applyEnv, sk, osk nkeys.KeyPair, withSigning bool) *initer {
	t.Helper()
	a := newIniter(t, e)
	if err := os.WriteFile(a.opSeed, mustRead(t, seedFile(t, t.TempDir(), "op", osk)), 0o600); err != nil {
		t.Fatal(err)
	}
	sc := filepath.Join(a.root, "in", "signer.creds")
	signingKeyCreds(t, e.acctPub, sk, sc)
	a.vals["admin_creds"] = sc
	if withSigning {
		a.vals["signing_key"] = seedFile(t, filepath.Join(a.root, "in"), "signing.seed", sk)
	}
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("init: %d\n%s\n%s", code, out, errb)
	}
	return a
}

// With signing_key, endpoint create, the sign-in of the new creds, reissue and delete work on an operator with
// strict_signing_key_usage, and the users are signed by the signing key for the account.
func TestStrictSigningKeyEndpointLifecycle(t *testing.T) {
	prev := revokeUsers
	revokeUsers = revokeUserKeys // the real revocation, through the system account
	t.Cleanup(func() { revokeUsers = prev })
	e, sk, osk := strictEnv(t)
	skPub, _ := sk.PublicKey()
	a := initStrict(t, e, sk, osk, true)
	if got := mustPub(t, filepath.Join(a.env["MASH_KEYS_DIR"], "signing.nk")); got != skPub {
		t.Fatalf("the key store holds %s as the signing key, want %s", got, skPub)
	}
	if fi, err := os.Stat(filepath.Join(a.env["MASH_KEYS_DIR"], "signing.nk")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("signing.nk: %v %v", fi, err)
	}
	cfgDir := a.env["MASH_CONFIG_DIR"]
	creds := filepath.Join(cfgDir, "alice.g.x.creds")

	if out, errb, code := a.run("endpoint", "create", "alice.g.x"); code != 0 {
		t.Fatalf("create: %d\n%s\n%s", code, out, errb)
	}
	uc := credsClaims(t, creds)
	if uc.Issuer != skPub || uc.IssuerAccount != e.acctPub {
		t.Fatalf("issuer %s issuer_account %s, want %s for %s", uc.Issuer, uc.IssuerAccount, skPub, e.acctPub)
	}
	// the sign-in `mash doctor` makes is a connection with these creds
	if !connectsWith(t, e.srv.ClientURL(), creds) {
		t.Fatal("the strict server refused the created endpoint's creds")
	}

	// reissue: the old key is revoked through the resolver, which keeps the signing key in the account JWT
	oldPub := uc.Subject
	kp, _ := nkeys.CreateUser()
	newPub, _ := kp.PublicKey()
	pubFile := filepath.Join(a.root, "new.pub")
	if err := os.WriteFile(pubFile, []byte(newPub+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	join := filepath.Join(a.root, "new.join")
	if out, errb, code := a.run("endpoint", "reissue", "alice.g.x", "--out", join, "--public-key", pubFile); code != 0 {
		t.Fatalf("reissue: %d\n%s\n%s", code, out, errb)
	}
	j, _ := readJoin(t, join)
	nuc, err := jwt.DecodeUserClaims(j.UserJWT)
	if err != nil || nuc.Subject != newPub || nuc.Issuer != skPub || nuc.IssuerAccount != e.acctPub {
		t.Fatalf("reissued JWT: %+v %v", nuc, err)
	}
	c := e.claims()
	if _, ok := c.Revocations[oldPub]; !ok {
		t.Error("the old key was not revoked")
	}
	if !c.SigningKeys.Contains(skPub) {
		t.Error("the revocation dropped the account signing key")
	}
	if connectsWith(t, e.srv.ClientURL(), creds) {
		t.Error("the revoked creds still connect")
	}

	// delete
	if out, errb, code := a.run("endpoint", "delete", "alice.g.x", "--yes"); code != 0 {
		t.Fatalf("delete: %d\n%s\n%s", code, out, errb)
	}
	if _, ok := e.claims().Revocations[newPub]; !ok {
		t.Error("delete did not revoke the reissued key")
	}
	if !e.claims().SigningKeys.Contains(skPub) {
		t.Error("delete dropped the account signing key")
	}
}

// Without signing_key a strict operator refuses the users mashctl creates: the identity key signs and names no
// issuer_account.
func TestStrictWithoutSigningKeyIsRefused(t *testing.T) {
	e, sk, osk := strictEnv(t)
	a := initStrict(t, e, sk, osk, false)
	if _, err := os.Stat(filepath.Join(a.env["MASH_KEYS_DIR"], "signing.nk")); err == nil {
		t.Fatal("a signing key was stored that no setup file gave")
	}
	if out, errb, code := a.run("endpoint", "create", "alice.g.x"); code != 0 {
		t.Fatalf("create: %d\n%s\n%s", code, out, errb)
	}
	creds := filepath.Join(a.env["MASH_CONFIG_DIR"], "alice.g.x.creds")
	if uc := credsClaims(t, creds); uc.Issuer != e.acctPub || uc.IssuerAccount != "" {
		t.Fatalf("issuer %s issuer_account %q", uc.Issuer, uc.IssuerAccount)
	}
	if connectsWith(t, e.srv.ClientURL(), creds) {
		t.Fatal("the strict server took a user signed by the identity key")
	}
}

func TestInitSigningKeyChecks(t *testing.T) {
	e := newApplyEnv(t)
	a := newIniter(t, e)
	in := filepath.Join(a.root, "in")

	t.Run("not in the account JWT", func(t *testing.T) {
		a.t = t
		other, _ := nkeys.CreateAccount()
		a.refuses(1, []string{"signing_key", "not one of the signing keys", "nothing was changed"}, map[string]any{"signing_key": seedFile(t, in, "other.seed", other)})
	})
	t.Run("the identity key", func(t *testing.T) {
		a.t = t
		a.refuses(1, []string{"signing_key is the account's identity key"}, map[string]any{"signing_key": a.acSeed})
	})
	t.Run("not an account seed", func(t *testing.T) {
		a.t = t
		a.refuses(1, []string{"signing_key", "account signing", "holds an operator seed (SO)", "signing_key needs an account seed (SA)"}, map[string]any{"signing_key": a.opSeed})
	})
	t.Run("a file with open mode", func(t *testing.T) {
		a.t = t
		other, _ := nkeys.CreateAccount()
		p := seedFile(t, in, "open.seed", other)
		if err := os.Chmod(p, 0o644); err != nil {
			t.Fatal(err)
		}
		a.refuses(1, []string{"mode 0644"}, map[string]any{"signing_key": p})
		a.vals["signing_key"] = p
		_, errb, _ := a.init()
		a.vals["signing_key"] = nil
		if strings.Contains(errb, "must be a 0600 file") || strings.Contains(errb, "mashctl:") {
			t.Errorf("a mode refusal carries the content hint or a prefix:\n%s", errb)
		}
	})
}

// A listed signing key is imported once; a rerun changes nothing, and another key is refused.
func TestInitSigningKeyImport(t *testing.T) {
	e := newApplyEnv(t)
	sk, _ := nkeys.CreateAccount()
	skPub, _ := sk.PublicKey()
	opKP, _, err := e.ks.Operator()
	if err != nil {
		t.Fatal(err)
	}
	ac := e.claims()
	ac.SigningKeys.Add(skPub)
	tok, err := ac.Encode(opKP)
	if err != nil {
		t.Fatal(err)
	}
	// the resolver of this env takes the account JWT by an update through the system account
	pushAccountJWT(t, e, tok)

	a := newIniter(t, e)
	a.vals["signing_key"] = seedFile(t, filepath.Join(a.root, "in"), "signing.seed", sk)
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("init: %d\n%s\n%s", code, out, errb)
	} else if !strings.Contains(out, "\nSigning key: "+skPub+"\n") {
		t.Fatalf("init output does not name the signing key on its own line: %q", out)
	} else if strings.Contains(out, "signed with signing key") {
		t.Fatalf("init still puts the key on the first line: %q", out)
	} else {
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "Signing key: ") && len(l) > 80 {
				t.Fatalf("the signing key line is %d columns: %q", len(l), l)
			}
		}
	}
	stored := filepath.Join(a.env["MASH_KEYS_DIR"], "signing.nk")
	if mustPub(t, stored) != skPub {
		t.Fatal("the stored signing key is not the one given")
	}
	ks, err := ctlkeys.Open(a.env["MASH_KEYS_DIR"], "")
	if err != nil {
		t.Fatal(err)
	}
	if kp, pub, err := ks.Signing(); err != nil || pub != skPub {
		t.Fatalf("Signing: %v %v", pub, err)
	} else {
		kp.Wipe()
	}
	snap := tree(t, a.root)
	if out, _, code := a.init(); code != 0 || !strings.Contains(out, "Already initialized") {
		t.Fatalf("rerun: %d %q", code, out)
	}
	sameTree(t, snap, tree(t, a.root))
	// another key than the stored one: refused, nothing changed (it is listed, so the refusal is the key store's)
	sk2, _ := nkeys.CreateAccount()
	sk2Pub, _ := sk2.PublicKey()
	ac2 := e.claims()
	ac2.SigningKeys.Add(sk2Pub)
	tok2, _ := ac2.Encode(opKP)
	pushAccountJWT(t, e, tok2)
	a.refuses(1, []string{"different signing key", skPub, "MASH_KEYS_DIR"}, map[string]any{"signing_key": seedFile(t, filepath.Join(a.root, "in"), "signing2.seed", sk2)})
}

// pushAccountJWT updates the account JWT of the env's resolver through the system account.
func pushAccountJWT(t *testing.T, e *applyEnv, tok string) {
	t.Helper()
	nc, err := nats.Connect(e.srv.ClientURL(), nats.Timeout(15*time.Second), natsauth.UserCredentials(e.sysCreds))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	m, err := nc.Request("$SYS.REQ.CLAIMS.UPDATE", []byte(tok), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(m.Data), "\"code\":200") && !strings.Contains(string(m.Data), "\"data\"") {
		t.Fatalf("the update was refused: %s", m.Data)
	}
}

// An endpoint whose user JWT was signed outside mashctl, by a plain signing key of the account that is not the
// stored one (nsc, a rehearsal), is deleted like any other under signing_key: its user key is revoked from the
// directory record, and the foreign signing key stays in the account JWT.
func TestStrictDeleteForeignSignedUser(t *testing.T) {
	prev := revokeUsers
	revokeUsers = revokeUserKeys
	t.Cleanup(func() { revokeUsers = prev })
	e, sk, osk := strictEnv(t)
	skPub, _ := sk.PublicKey()
	a := initStrict(t, e, sk, osk, true)

	// another plain signing key joins the account JWT, as nsc would add it
	fk, _ := nkeys.CreateAccount()
	fkPub, _ := fk.PublicKey()
	ac := e.claims()
	ac.SigningKeys.Add(fkPub)
	tok, err := ac.Encode(osk) // a strict operator takes account JWTs signed by its signing key
	if err != nil {
		t.Fatal(err)
	}
	pushAccountJWT(t, e, tok)

	// the endpoint is recorded (directory and issue record) for a user key the operator holds
	ukp, _ := nkeys.CreateUser()
	userPub, _ := ukp.PublicKey()
	pubFile := filepath.Join(a.root, "y.pub")
	if err := os.WriteFile(pubFile, []byte(userPub+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, errb, code := a.run("endpoint", "create", "y.rehearse.one", "--out", filepath.Join(a.root, "y.join"), "--public-key", pubFile); code != 0 {
		t.Fatalf("create: %d\n%s\n%s", code, out, errb)
	}
	// its creds were signed by the foreign key, with issuer_account
	uc := jwt.NewUserClaims(userPub)
	uc.Name = "y.rehearse.one"
	uc.IssuerAccount = e.acctPub
	utok, err := uc.Encode(fk)
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := ukp.Seed()
	raw, err := jwt.FormatUserConfig(utok, seed)
	if err != nil {
		t.Fatal(err)
	}
	creds := filepath.Join(a.env["MASH_CONFIG_DIR"], "y.rehearse.one.creds")
	if err := os.WriteFile(creds, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if !connectsWith(t, e.srv.ClientURL(), creds) {
		t.Fatal("the strict server refused the foreign-signed creds")
	}

	// delete renames the creds file aside, so the connect check afterwards uses a copy
	copyCreds := filepath.Join(a.root, "y.copy.creds")
	if err := os.WriteFile(copyCreds, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, errb, code := a.run("endpoint", "delete", "y.rehearse.one", "--yes"); code != 0 {
		t.Fatalf("delete: %d\n%s\n%s", code, out, errb)
	}
	c := e.claims()
	if _, ok := c.Revocations[userPub]; !ok {
		t.Error("delete did not revoke the foreign-signed user key")
	}
	if !c.SigningKeys.Contains(fkPub) || !c.SigningKeys.Contains(skPub) {
		t.Error("delete dropped a signing key from the account JWT")
	}
	if connectsWith(t, e.srv.ClientURL(), copyCreds) {
		t.Error("the revoked foreign-signed creds still connect")
	}
	if out, _, code := a.run("endpoint", "list"); code != 0 || strings.Contains(out, "y.rehearse.one") {
		t.Errorf("the record is still listed: %d\n%s", code, out)
	}
}

// A rerun whose setup file gives no signing_key still checks the stored signing.nk: when the account JWT no longer
// lists it as a plain signing key, init refuses and changes nothing.
func TestInitStoredSigningKeyNoLongerListed(t *testing.T) {
	e, sk, osk := strictEnv(t)
	skPub, _ := sk.PublicKey()
	a := initStrict(t, e, sk, osk, true)
	// still listed: a rerun without signing_key is fine
	a.vals["signing_key"] = nil
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("rerun while listed: %d\n%s\n%s", code, out, errb)
	}
	// the account lists the key as a scoped signing key now
	ac := e.claims()
	delete(ac.SigningKeys, skPub)
	sc := jwt.NewUserScope()
	sc.Key, sc.Role = skPub, "legacy"
	ac.SigningKeys.AddScopedSigner(sc)
	tok, err := ac.Encode(osk)
	if err != nil {
		t.Fatal(err)
	}
	pushAccountJWT(t, e, tok)
	a.refuses(1, []string{"signing.nk", "no longer a plain listed signing key", "new config folder and key folder", "nothing was changed"}, nil)
}

// A signing.nk that is lost after init (the record signing.pub stays) is refused by init without signing_key,
// nothing changed; init with the same signing_key restores it.
func TestInitSigningKeyLost(t *testing.T) {
	e := newApplyEnv(t)
	sk, _ := nkeys.CreateAccount()
	skPub, _ := sk.PublicKey()
	opKP, _, err := e.ks.Operator()
	if err != nil {
		t.Fatal(err)
	}
	ac := e.claims()
	ac.SigningKeys.Add(skPub)
	tok, err := ac.Encode(opKP)
	if err != nil {
		t.Fatal(err)
	}
	pushAccountJWT(t, e, tok)
	a := newIniter(t, e)
	seed := seedFile(t, filepath.Join(a.root, "in"), "signing.seed", sk)
	a.vals["signing_key"] = seed
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("init: %d\n%s\n%s", code, out, errb)
	}
	keysDir := a.env["MASH_KEYS_DIR"]
	if _, err := os.Stat(filepath.Join(keysDir, "signing.pub")); err != nil {
		t.Fatalf("no signing.pub record: %v", err)
	}
	if err := os.Remove(filepath.Join(keysDir, "signing.nk")); err != nil {
		t.Fatal(err)
	}
	a.refuses(1, []string{"signing.nk", "mashctl init", "SAME key", "signing.pub records " + skPub}, map[string]any{"signing_key": nil})
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("init to restore: %d\n%s\n%s", code, out, errb)
	}
	if mustPub(t, filepath.Join(keysDir, "signing.nk")) != skPub {
		t.Fatal("signing.nk was not restored")
	}
}

// A lost signing.nk is refused by endpoint create before anything is recorded: no endpoint record, no creds, and the
// hint is init (the same create would fail again); init with the same signing_key restores it and create then works.
func TestEndpointCreateSigningKeyLostRecordsNothing(t *testing.T) {
	e, sk, osk := strictEnv(t)
	a := initStrict(t, e, sk, osk, true)
	keysDir := a.env["MASH_KEYS_DIR"]
	if err := os.Remove(filepath.Join(keysDir, "signing.nk")); err != nil {
		t.Fatal(err)
	}
	out, errb, code := a.run("endpoint", "create", "alice.g.x")
	if code != 1 {
		t.Fatalf("create: exit %d\n%s\n%s", code, out, errb)
	}
	for _, m := range []string{"signing.nk", "SAME key", "nothing was changed", "mashctl init --config <file>"} {
		if !strings.Contains(errb, m) {
			t.Errorf("stderr lacks %q:\n%s", m, errb)
		}
	}
	if strings.Contains(errb, "no account signing key configured") {
		t.Errorf("stderr contradicts itself:\n%s", errb)
	}
	creds := filepath.Join(a.env["MASH_CONFIG_DIR"], "alice.g.x.creds")
	if _, err := os.Stat(creds); err == nil {
		t.Fatal("creds were written")
	}
	if _, err := os.Lstat(creds + ".pending"); err == nil {
		t.Fatal("a .pending seed was left behind")
	}
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("init to restore: %d\n%s\n%s", code, out, errb)
	}
	// Had the refused create recorded the endpoint, this one would say it already exists.
	if out, errb, code := a.run("endpoint", "create", "alice.g.x"); code != 0 {
		t.Fatalf("create after restore: %d\n%s\n%s", code, out, errb)
	}
}

// A signing.pub that is not a key record fails Signing with an error that is neither ErrNoSigningKey nor
// ErrSigningKeyLost: create still refuses before staging a .pending seed or recording the endpoint.
func TestEndpointCreateSigningRecordUnreadableRecordsNothing(t *testing.T) {
	e, sk, osk := strictEnv(t)
	a := initStrict(t, e, sk, osk, true)
	keysDir := a.env["MASH_KEYS_DIR"]
	if err := os.WriteFile(filepath.Join(keysDir, "signing.pub"), []byte("not a key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errb, code := a.run("endpoint", "create", "alice.g.x")
	if code != 1 {
		t.Fatalf("create: exit %d\n%s\n%s", code, out, errb)
	}
	if !strings.Contains(errb, "nothing was changed") {
		t.Errorf("stderr lacks %q:\n%s", "nothing was changed", errb)
	}
	creds := filepath.Join(a.env["MASH_CONFIG_DIR"], "alice.g.x.creds")
	for _, f := range []string{creds, creds + ".pending"} {
		if _, err := os.Lstat(f); err == nil {
			t.Errorf("%s was left behind", f)
		}
	}
	// (with the field in config.json, a store whose signing.pub is missing is refused too, so the record is restored)
	skPub, _ := sk.PublicKey()
	if err := os.WriteFile(filepath.Join(keysDir, "signing.pub"), []byte(skPub+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Had the refused create recorded the endpoint, this one would say it already exists.
	if out, errb, code := a.run("endpoint", "create", "alice.g.x"); code != 0 {
		t.Fatalf("create after repair: %d\n%s\n%s", code, out, errb)
	}
}

// init that stopped after writing signing.nk and before signing.pub leaves a seed with no record: a rerun with the
// same signing_key completes the record (0600, atomic); one with another key is refused and writes nothing.
func TestInitSigningRecordCompletedOnRetry(t *testing.T) {
	e := newApplyEnv(t)
	sk, _ := nkeys.CreateAccount()
	skPub, _ := sk.PublicKey()
	opKP, _, err := e.ks.Operator()
	if err != nil {
		t.Fatal(err)
	}
	ac := e.claims()
	ac.SigningKeys.Add(skPub)
	tok, err := ac.Encode(opKP)
	if err != nil {
		t.Fatal(err)
	}
	pushAccountJWT(t, e, tok)
	a := newIniter(t, e)
	a.vals["signing_key"] = seedFile(t, filepath.Join(a.root, "in"), "signing.seed", sk)
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("init: %d\n%s\n%s", code, out, errb)
	}
	rec := filepath.Join(a.env["MASH_KEYS_DIR"], "signing.pub")
	if err := os.Remove(rec); err != nil {
		t.Fatal(err)
	}
	other, _ := nkeys.CreateAccount()
	a.refuses(1, []string{"different signing key"}, map[string]any{"signing_key": seedFile(t, filepath.Join(a.root, "in"), "other.seed", other)})
	if _, err := os.Stat(rec); err == nil {
		t.Fatal("a record was written for a different key")
	}
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("init to complete the record: %d\n%s\n%s", code, out, errb)
	}
	b, err := os.ReadFile(rec)
	if err != nil || strings.TrimSpace(string(b)) != skPub {
		t.Fatalf("record: %q %v", b, err)
	}
	if fi, _ := os.Stat(rec); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
}
