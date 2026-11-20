package keycred

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nkeys"
)

func TestPublicPathFor(t *testing.T) {
	for in, want := range map[string]string{
		"design.seed": "design.seed.pub", "a/b/x.seed": "a/b/x.seed.pub", "design": "design.pub", "mash.seed": "mash.seed.pub",
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
		{name: "seed suffix", seed: "d.seed", wantPub: "d.seed.pub"},
		{name: "other name", seed: "d.key", wantPub: "d.key.pub"},
		{name: "pub-out", seed: "d.seed", pubOut: "elsewhere.txt", wantPub: "elsewhere.txt"},
		{name: "dry run", seed: "d.seed", wantPub: "d.seed.pub", dry: true},
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
			seed, pub := filepath.Join(dir, "d.seed"), filepath.Join(dir, "d.seed.pub")
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
