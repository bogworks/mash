package keycred

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

func TestPublicPathFor(t *testing.T) {
	for in, want := range map[string]string{
		"design.seed": "design.pub", "a/b/x.seed": "a/b/x.pub", "design": "design.pub", "k.key": "k.key.pub", ".seed": ".seed.pub",
	} {
		if got := PublicPathFor(in); got != want {
			t.Errorf("PublicPathFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCreateKey(t *testing.T) {
	type tc struct {
		name    string
		seed    string // relative to the temp dir
		pubOut  string
		wantPub string
		dry     bool
	}
	for _, c := range []tc{
		{name: "seed suffix", seed: "d.seed", wantPub: "d.pub"},
		{name: "other name", seed: "d.key", wantPub: "d.key.pub"},
		{name: "pub-out", seed: "d.seed", pubOut: "elsewhere.txt", wantPub: "elsewhere.txt"},
		{name: "dry run", seed: "d.seed", wantPub: "d.pub", dry: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			o := KeyOptions{Out: filepath.Join(dir, c.seed), DryRun: c.dry}
			if c.pubOut != "" {
				o.PubOut = filepath.Join(dir, c.pubOut)
			}
			res, err := CreateKey(o)
			if err != nil {
				t.Fatal(err)
			}
			if res.PublicPath != filepath.Join(dir, c.wantPub) {
				t.Errorf("public path %q", res.PublicPath)
			}
			if c.dry {
				if res.Outcome != Planned {
					t.Errorf("outcome %q", res.Outcome)
				}
				if ents, _ := os.ReadDir(dir); len(ents) != 0 {
					t.Errorf("dry run wrote %v", ents)
				}
				return
			}
			if res.Outcome != Created {
				t.Fatalf("outcome %q", res.Outcome)
			}
			st, err := os.Stat(o.Out)
			if err != nil || st.Mode().Perm() != 0o600 {
				t.Errorf("seed mode %v, %v", st, err)
			}
			seed, _ := os.ReadFile(o.Out)
			kp, err := nkeys.FromSeed([]byte(strings.TrimSpace(string(seed))))
			if err != nil {
				t.Fatal(err)
			}
			pub, _ := kp.PublicKey()
			got, _ := os.ReadFile(res.PublicPath)
			if strings.TrimSpace(string(got)) != pub || pub != res.PublicKey || !strings.HasPrefix(pub, "U") {
				t.Errorf("public file %q, key %q, result %q", got, pub, res.PublicKey)
			}
		})
	}
}

func TestCreateKeyRefusesOverwrite(t *testing.T) {
	for _, which := range []string{"seed", "pub"} {
		t.Run(which, func(t *testing.T) {
			dir := t.TempDir()
			seed, pub := filepath.Join(dir, "d.seed"), filepath.Join(dir, "d.pub")
			keep := seed
			if which == "pub" {
				keep = pub
			}
			if err := os.WriteFile(keep, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			res, err := CreateKey(KeyOptions{Out: seed})
			if err != nil {
				t.Fatal(err)
			}
			if res.Outcome != Refused || res.Refusal.Code != CodeFileExists {
				t.Fatalf("result %+v", res)
			}
			if b, _ := os.ReadFile(keep); string(b) != "old" {
				t.Errorf("existing file changed to %q", b)
			}
			ents, _ := os.ReadDir(dir)
			if len(ents) != 1 {
				t.Errorf("files after refusal: %v", ents)
			}
		})
	}
}

func TestCreateKeyRemovesSeedWhenPublicFails(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "d.seed")
	_, err := CreateKey(KeyOptions{Out: seed, PubOut: filepath.Join(dir, "missing-dir", "d.pub")})
	if err == nil {
		t.Fatal("want an error")
	}
	if _, err := os.Stat(seed); !os.IsNotExist(err) {
		t.Errorf("seed left behind: %v", err)
	}
}

func userJWT(t *testing.T, name string, subject string) string {
	t.Helper()
	acct, _ := nkeys.CreateAccount()
	uc := jwt.NewUserClaims(subject)
	uc.Name = name
	tok, err := uc.Encode(acct)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func credFixture(t *testing.T, name string) (dir, seedPath, jwtPath, seed, tok, pub string) {
	t.Helper()
	dir = t.TempDir()
	kp, _ := nkeys.CreateUser()
	pub, _ = kp.PublicKey()
	sb, _ := kp.Seed()
	seed = string(sb)
	tok = userJWT(t, name, pub)
	seedPath, jwtPath = filepath.Join(dir, "u.seed"), filepath.Join(dir, "u.jwt")
	os.WriteFile(seedPath, []byte(seed+"\n"), 0o600)
	os.WriteFile(jwtPath, []byte("  "+tok+"\n"), 0o600)
	return
}

func TestCreateCredentialsRoundTrip(t *testing.T) {
	dir, seedPath, jwtPath, seed, tok, pub := credFixture(t, "design")
	out := filepath.Join(dir, "design.creds")
	res, err := CreateCredentials(CredOptions{Seed: seedPath, JWT: jwtPath, Out: out})
	if err != nil || res.Outcome != Created || res.PublicKey != pub || res.Name != "design" {
		t.Fatalf("%+v, %v", res, err)
	}
	st, _ := os.Stat(out)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	raw, _ := os.ReadFile(out)
	gotJWT, err := jwt.ParseDecoratedJWT(raw)
	if err != nil || gotJWT != tok {
		t.Errorf("jwt %q, %v", gotJWT, err)
	}
	kp, err := jwt.ParseDecoratedNKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	gotSeed, _ := kp.Seed()
	if string(gotSeed) != seed {
		t.Error("seed differs")
	}
	claims, _ := jwt.DecodeUserClaims(gotJWT)
	if p, _ := kp.PublicKey(); claims.Subject != p {
		t.Errorf("subject %q, seed key %q", claims.Subject, p)
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".creds-") {
			t.Errorf("temp file left: %s", e.Name())
		}
	}
}

func TestCreateCredentialsMismatch(t *testing.T) {
	dir, seedPath, jwtPath, _, _, _ := credFixture(t, "design")
	other, _ := nkeys.CreateUser()
	opub, _ := other.PublicKey()
	os.WriteFile(jwtPath, []byte(userJWT(t, "design", opub)), 0o600)
	out := filepath.Join(dir, "x.creds")
	res, err := CreateCredentials(CredOptions{Seed: seedPath, JWT: jwtPath, Out: out})
	if err != nil || res.Outcome != Refused || res.Refusal.Code != CodeKeyMismatch {
		t.Fatalf("%+v, %v", res, err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("creds written on mismatch")
	}
}

func TestCreateCredentialsExistingAndForce(t *testing.T) {
	dir, seedPath, jwtPath, _, _, _ := credFixture(t, "design")
	out := filepath.Join(dir, "x.creds")
	os.WriteFile(out, []byte("old"), 0o644)
	res, _ := CreateCredentials(CredOptions{Seed: seedPath, JWT: jwtPath, Out: out})
	if res.Outcome != Refused || res.Refusal.Code != CodeFileExists {
		t.Fatalf("%+v", res)
	}
	if b, _ := os.ReadFile(out); string(b) != "old" {
		t.Error("existing file changed")
	}
	res, err := CreateCredentials(CredOptions{Seed: seedPath, JWT: jwtPath, Out: out, Force: true})
	if err != nil || res.Outcome != Created {
		t.Fatalf("%+v, %v", res, err)
	}
	st, _ := os.Stat(out)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode after force %v", st.Mode())
	}
	if b, _ := os.ReadFile(out); !strings.Contains(string(b), "BEGIN NATS USER JWT") {
		t.Error("not replaced")
	}
}

func TestCreateCredentialsDefaultOut(t *testing.T) {
	_, seedPath, jwtPath, _, _, _ := credFixture(t, "design")
	cfg := filepath.Join(t.TempDir(), "newcfg")
	res, err := CreateCredentials(CredOptions{Seed: seedPath, JWT: jwtPath, ConfigDir: cfg})
	if err != nil || res.CredsPath != filepath.Join(cfg, "design.creds") {
		t.Fatalf("%+v, %v", res, err)
	}
	if st, err := os.Stat(cfg); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("config dir %v, %v", st, err)
	}
	_, seedPath, jwtPath, _, _, _ = credFixture(t, "")
	if _, err := CreateCredentials(CredOptions{Seed: seedPath, JWT: jwtPath, ConfigDir: cfg}); err != ErrNeedOut {
		t.Errorf("err = %v, want ErrNeedOut", err)
	}
}

func TestCreateCredentialsDryRun(t *testing.T) {
	dir, seedPath, jwtPath, _, _, _ := credFixture(t, "design")
	out := filepath.Join(dir, "x.creds")
	res, err := CreateCredentials(CredOptions{Seed: seedPath, JWT: jwtPath, Out: out, DryRun: true})
	if err != nil || res.Outcome != Planned {
		t.Fatalf("%+v, %v", res, err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("dry run wrote")
	}
}
