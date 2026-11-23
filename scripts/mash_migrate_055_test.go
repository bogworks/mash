package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The migrate script enters the config folder once and then works by relative names, so a later
// swap of a parent path cannot redirect its writes (issue 1063).

func TestMigrate055WritesLandInHeldFolder(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real", "mash")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(root, "real"), link); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir() // a different working directory, with a decoy config.json
	decoy := filepath.Join(other, "config.json")
	if err := os.WriteFile(decoy, []byte(`{"url":"decoy"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "config.json"), []byte(`{"url":"nats://a:4222","tls_ca":"/x"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := filepath.Join(link, "mash") // reached through a symbolic link
	script, err := filepath.Abs("mash-migrate-0.5.5.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script)
	cmd.Dir = other
	cmd.Env = append(os.Environ(), "MASH_CONFIG_DIR="+cfg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	want := "Migrated " + cfg + "/config.json: url -> servers, tls_ca -> ca (old file kept as " + cfg + "/config.json.0.5.5)\n"
	if string(out) != want {
		t.Fatalf("output %q, want %q", out, want)
	}
	got, _ := os.ReadFile(filepath.Join(real, "config.json"))
	if string(got) != `{"servers":["nats://a:4222"],"ca":"/x"}`+"\n" {
		t.Fatalf("migrated file: %q", got)
	}
	if _, err := os.Stat(filepath.Join(real, "config.json.0.5.5")); err != nil {
		t.Fatalf("backup missing in the held folder: %v", err)
	}
	if b, _ := os.ReadFile(decoy); string(b) != `{"url":"decoy"}` {
		t.Fatalf("the other working directory was touched: %q", b)
	}
	if left, _ := filepath.Glob(filepath.Join(real, ".mash-migrate.*")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
	if left, _ := filepath.Glob(filepath.Join(other, ".mash-migrate.*")); len(left) != 0 {
		t.Fatalf("temp files in the wrong folder: %v", left)
	}
}

func TestMigrate055NoDirPathAfterCd(t *testing.T) {
	b, err := os.ReadFile("mash-migrate-0.5.5.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(b), `cd -P -- "$dir"`)
	if !ok {
		t.Fatal("script does not cd -P into the folder")
	}
	for i, l := range strings.Split(after, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		// messages may show $dir; operations may not build a path from it
		if strings.Contains(l, `"$dir/`) && !strings.Contains(l, "refuse ") && !strings.Contains(l, "printf") {
			t.Errorf("line %d after the cd still builds a path from $dir: %s", i, l)
		}
	}
	if strings.Contains(after, `mktemp "$dir`) {
		t.Error("mktemp still uses $dir")
	}
}
