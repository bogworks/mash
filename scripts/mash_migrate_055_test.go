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
	_, after, ok := strings.Cut(string(b), `CDPATH= cd -P -- "$dir"`)
	if !ok {
		t.Fatal("script does not CDPATH= cd -P into the folder")
	}
	for i, l := range strings.Split(after, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		// messages may show $dir, $file and $backup; operations may not build a path from them
		// only the text before "refuse " (the operation) or before "printf" (its arguments) is
		// checked; what follows is a message
		if j := strings.Index(l, "refuse "); j >= 0 {
			l = l[:j]
		}
		if j := strings.Index(l, "printf"); j >= 0 {
			l = l[:j]
		}
		for _, v := range []string{`$dir/`, `${dir}/`, `$file`, `${file}`, `$backup`, `${backup}`} {
			if strings.Contains(l, v) {
				t.Errorf("line %d after the cd still uses %s outside a message: %s", i, v, l)
			}
		}
	}
	if strings.Contains(after, `mktemp "$dir`) {
		t.Error("mktemp still uses $dir")
	}
}

const migrateIn = `{"url":"nats://a:4222","tls_ca":"/x"}` + "\n"
const migrateOut = `{"servers":["nats://a:4222"],"ca":"/x"}` + "\n"

func migrateGlob(t *testing.T, dir string) []string {
	t.Helper()
	left, _ := filepath.Glob(filepath.Join(dir, ".mash-migrate.*"))
	return left
}

// A fake for one external command, first on PATH, re-points the parent symlink on its first
// call and then runs the real command.  The script needs no hook for this: it runs each of
// these after it has resolved the folder.  ls is the first external command after the cd,
// before any read of config.json; mktemp, cp, tail and awk run in and around the one read.
func TestMigrate055ParentSwapDuringRun(t *testing.T) {
	for _, name := range []string{"ls", "mktemp", "cp", "tail", "awk", "ln", "mv"} {
		t.Run(name, func(t *testing.T) { migrateSwapDuringRun(t, name) })
	}
}

func migrateSwapDuringRun(t *testing.T, name string) {
	realCp, err := exec.LookPath(name)
	if err != nil {
		t.Skip("no " + name)
	}
	script, err := filepath.Abs("mash-migrate-0.5.5.sh")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	held := filepath.Join(home, "held", "mash")
	evil := filepath.Join(home, "evil", "mash")
	for _, d := range []string{held, evil} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(held, "config.json"), []byte(migrateIn), 0o600); err != nil {
		t.Fatal(err)
	}
	evilIn := `{"url":"nats://evil:4222"}` + "\n"
	if err := os.WriteFile(filepath.Join(evil, "config.json"), []byte(evilIn), 0o600); err != nil {
		t.Fatal(err)
	}
	dot := filepath.Join(home, ".config")
	if err := os.Symlink(filepath.Join(home, "held"), dot); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	fake := "#!/bin/sh\n" +
		"if [ ! -e '" + filepath.Join(bin, "swapped") + "' ]; then\n" +
		"  : > '" + filepath.Join(bin, "swapped") + "'\n" +
		"  rm -f '" + dot + "' && ln -s '" + filepath.Join(home, "evil") + "' '" + dot + "'\n" +
		"fi\n" +
		"exec '" + realCp + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, name), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "HOME="+home, "MASH_CONFIG_DIR=", "XDG_CONFIG_HOME=", "PATH="+bin+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if _, serr := os.Stat(filepath.Join(bin, "swapped")); serr != nil {
		t.Fatalf("the fake "+name+" never ran: the swap did not happen\n%s", out)
	}
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	if got, _ := os.ReadFile(filepath.Join(held, "config.json")); string(got) != migrateOut {
		t.Errorf("held folder not migrated: %q", got)
	}
	if _, err := os.Stat(filepath.Join(held, "config.json.0.5.5")); err != nil {
		t.Errorf("backup missing in the held folder: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(held, "config.json.0.5.5")); string(got) != migrateIn {
		t.Errorf("backup is not the original input: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(evil, "config.json")); string(got) != evilIn {
		t.Errorf("evil config changed: %q", got)
	}
	if _, err := os.Stat(filepath.Join(evil, "config.json.0.5.5")); err == nil {
		t.Error("a backup was written in the evil folder")
	}
	if left := migrateGlob(t, evil); len(left) != 0 {
		t.Errorf("temp files in the evil folder: %v", left)
	}
	if left := migrateGlob(t, held); len(left) != 0 {
		t.Errorf("temp files left in the held folder: %v", left)
	}
}

// With a relative HOME, a CDPATH holding a same-named subfolder must not redirect the cd or
// add a line to stdout.
func TestMigrate055CDPATHDoesNotRedirect(t *testing.T) {
	script, err := filepath.Abs("mash-migrate-0.5.5.sh")
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	decoyRoot := t.TempDir()
	mk := func(base, content string) string {
		d := filepath.Join(base, "home", ".config", "mash")
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "config.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return d
	}
	right := mk(cwd, migrateIn)
	decoyIn := `{"url":"nats://decoy:4222"}` + "\n"
	decoy := mk(decoyRoot, decoyIn)
	cmd := exec.Command("bash", script)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME=home", "MASH_CONFIG_DIR=", "XDG_CONFIG_HOME=", "CDPATH="+decoyRoot)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	cfg := "home/.config/mash"
	want := "Migrated " + cfg + "/config.json: url -> servers, tls_ca -> ca (old file kept as " + cfg + "/config.json.0.5.5)\n"
	if string(out) != want {
		t.Errorf("output %q, want %q", out, want)
	}
	if got, _ := os.ReadFile(filepath.Join(right, "config.json")); string(got) != migrateOut {
		t.Errorf("right folder not migrated: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(decoy, "config.json")); string(got) != decoyIn {
		t.Errorf("CDPATH folder was touched: %q", got)
	}
	if left := migrateGlob(t, decoy); len(left) != 0 {
		t.Errorf("temp files in the CDPATH folder: %v", left)
	}
}
