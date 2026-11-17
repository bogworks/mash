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

func TestCreateKeyPublicFailsLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "d.seed")
	// The public file's directory is a regular file, so it cannot be made.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := CreateKey(KeyOptions{Out: seed, PubOut: filepath.Join(blocker, "d.pub")})
	if err == nil {
		t.Fatal("want an error")
	}
	if _, err := os.Stat(seed); !os.IsNotExist(err) {
		t.Errorf("seed left behind: %v", err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("files after failure: %v", ents)
	}
}

func TestCreateKeyNoFinalFileOnWriteFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop root")
	}
	dir := t.TempDir()
	pubDir := filepath.Join(dir, "pub")
	if err := os.Mkdir(pubDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(pubDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(pubDir, 0o700) })
	seed := filepath.Join(dir, "d.seed")
	if _, err := CreateKey(KeyOptions{Out: seed, PubOut: filepath.Join(pubDir, "d.pub")}); err == nil {
		t.Fatal("want an error")
	}
	if _, err := os.Stat(seed); !os.IsNotExist(err) {
		t.Errorf("seed left behind: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pubDir, "d.pub")); !os.IsNotExist(err) {
		t.Errorf("public file left behind: %v", err)
	}
	for _, d := range []string{dir, pubDir} {
		ents, _ := os.ReadDir(d)
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), ".seed-") || strings.HasPrefix(e.Name(), ".pub-") {
				t.Errorf("temp file left in %s: %s", d, e.Name())
			}
		}
	}
}

func TestCreateKeyStaleTempDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, ".seed-12345")
	if err := os.WriteFile(stale, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := CreateKey(KeyOptions{Out: filepath.Join(dir, "d.seed")})
	if err != nil || res.Outcome != Created {
		t.Fatalf("result %+v, %v", res, err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Errorf("the stale file was removed: %v", err)
	}
}

func TestCreateKeyLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	res, err := CreateKey(KeyOptions{Out: filepath.Join(dir, "d.seed")})
	if err != nil || res.Outcome != Created {
		t.Fatalf("result %+v, %v", res, err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 2 {
		t.Errorf("want the seed and the public file only: %v", ents)
	}
}

func TestCreateKeyMakesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "a", "b", "d.seed")
	pub := filepath.Join(dir, "c", "d.pub")
	res, err := CreateKey(KeyOptions{Out: seed, PubOut: pub})
	if err != nil || res.Outcome != Created {
		t.Fatalf("result %+v, %v", res, err)
	}
	for _, d := range []string{filepath.Dir(seed), filepath.Dir(pub)} {
		if st, err := os.Stat(d); err != nil || st.Mode().Perm() != 0o700 {
			t.Errorf("directory %s: %v, %v", d, st, err)
		}
	}
	if res, err := CreateKey(KeyOptions{Out: filepath.Join(dir, "x", "y.seed"), DryRun: true}); err != nil || res.Outcome != Planned {
		t.Fatalf("dry run %+v, %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "x")); !os.IsNotExist(err) {
		t.Errorf("dry run made a directory: %v", err)
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

func TestCreateCredentialsRefusesInputAsOutput(t *testing.T) {
	dir, seedPath, jwtPath, seed, tok, _ := credFixture(t, "design")
	alias := filepath.Join(dir, "alias.seed")
	if err := os.Symlink(seedPath, alias); err != nil {
		t.Skip("no symlinks:", err)
	}
	hard := filepath.Join(dir, "hard.jwt")
	if err := os.Link(jwtPath, hard); err != nil {
		t.Skip("no hard links:", err)
	}
	for _, out := range []string{seedPath, jwtPath, alias, hard} {
		res, err := CreateCredentials(CredOptions{Seed: seedPath, JWT: jwtPath, Out: out, Force: true})
		if err != nil || res.Outcome != Refused || res.Refusal.Code != CodeOutIsInput {
			t.Fatalf("out %s: %+v, %v", out, res, err)
		}
	}
	if b, _ := os.ReadFile(seedPath); strings.TrimSpace(string(b)) != seed {
		t.Error("seed file changed")
	}
	if b, _ := os.ReadFile(jwtPath); strings.TrimSpace(string(b)) != tok {
		t.Error("jwt file changed")
	}
}
