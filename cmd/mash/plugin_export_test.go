package main

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/bogworks/mash/plugin"
)

var pluginFiles = []string{".claude-plugin/plugin.json", ".mcp.json", "hooks/hooks.json", "scripts/mash"}

func TestPluginExport(t *testing.T) {
	dir := t.TempDir()
	if err := exportPlugin(dir, plugin.Files); err != nil {
		t.Fatal(err)
	}
	for _, f := range pluginFiles {
		want, err := os.ReadFile(filepath.Join("../../plugin", f))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dir, "plugin", f))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s: err %v, differs from the repo file", f, err)
		}
	}
	if runtime.GOOS != "windows" {
		for f, want := range map[string]os.FileMode{"scripts/mash": 0o755, ".mcp.json": 0o644, "hooks/hooks.json": 0o644} {
			fi, err := os.Stat(filepath.Join(dir, "plugin", f))
			if err != nil || fi.Mode().Perm() != want {
				t.Fatalf("%s: mode %v err %v, want %v", f, fi.Mode().Perm(), err, want)
			}
		}
	}
	var mkt struct {
		Name    string                                   `json:"name"`
		Plugins []struct{ Name, Source, Version string } `json:"plugins"`
	}
	readJSON(t, filepath.Join(dir, ".claude-plugin/marketplace.json"), &mkt)
	var pl struct{ Version string }
	readJSON(t, "../../plugin/.claude-plugin/plugin.json", &pl)
	if mkt.Name != "mash" || len(mkt.Plugins) != 1 || mkt.Plugins[0].Name != "mash" ||
		mkt.Plugins[0].Source != "./plugin" || mkt.Plugins[0].Version != pl.Version || pl.Version == "" {
		t.Fatalf("marketplace %+v, plugin version %q", mkt, pl.Version)
	}
}

func TestPluginExportOverwrites(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "plugin", "keep.txt")
	os.MkdirAll(filepath.Dir(other), 0o755)
	os.WriteFile(other, []byte("mine"), 0o644)
	os.MkdirAll(filepath.Join(dir, "plugin"), 0o755)
	stale := filepath.Join(dir, "plugin", ".mcp.json")
	os.WriteFile(stale, []byte("stale"), 0o644)
	if err := exportPlugin(dir, plugin.Files); err != nil {
		t.Fatal(err)
	}
	want, _ := plugin.Files.ReadFile(".mcp.json")
	if got, _ := os.ReadFile(stale); !bytes.Equal(got, want) {
		t.Fatalf("not overwritten: %q", got)
	}
	if got, _ := os.ReadFile(other); string(got) != "mine" {
		t.Fatalf("unrelated file touched: %q", got)
	}
}

func TestPluginExportUsage(t *testing.T) {
	for _, args := range [][]string{{"plugin"}, {"plugin", "bogus"}, {"plugin", "export"}, {"plugin", "export", "a", "b"}} {
		var out, errb bytes.Buffer
		if code := run(args, func(string) string { return "" }, nil, &out, &errb); code != 2 || !strings.Contains(errb.String(), "usage") {
			t.Fatalf("%v: %d %q", args, code, errb.String())
		}
	}
}

func TestPluginExportUnwritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	dir := t.TempDir()
	os.Chmod(dir, 0o500)
	defer os.Chmod(dir, 0o700)
	var out, errb bytes.Buffer
	if code := run([]string{"plugin", "export", dir}, func(string) string { return "" }, nil, &out, &errb); code != 1 || !strings.HasPrefix(errb.String(), "mash: ") {
		t.Fatalf("%d %q", code, errb.String())
	}
}

func TestPluginExportBinaryEmptyEnv(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(mashBinary(t), "plugin", "export", dir)
	cmd.Env = []string{}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	abs, _ := filepath.Abs(dir)
	if strings.TrimSpace(string(out)) != abs {
		t.Fatalf("stdout %q, want %q", out, abs)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugin", ".mcp.json")); err != nil {
		t.Fatal(err)
	}
}

// Every non-Go file under plugin/ is embedded, so a new plugin file cannot be left out of the export. The Pi
// extension has its own embed (PiFiles); its test file is not shipped.
func TestPluginEmbedCoversDirectory(t *testing.T) {
	err := filepath.WalkDir("../../plugin", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(p, ".go") {
			return err
		}
		rel, _ := filepath.Rel("../../plugin", p)
		if rel == "scripts/pin-plugin" || rel == "scripts/test-launcher.sh" || rel == "scripts/mash.pins" {
			return nil // release tooling and the post-release pin, not embedded (embedding the pin would make a checksum cycle)
		}
		if rel == "pi/mash.ts" {
			_, err := fs.Stat(plugin.PiFiles, rel)
			if err != nil {
				t.Errorf("%s is not embedded in PiFiles", rel)
			}
			return nil
		}
		if rel == "pi/mash.test.ts" {
			return nil
		}
		if _, err := fs.Stat(plugin.Files, filepath.ToSlash(rel)); err != nil {
			t.Errorf("%s is not embedded: add it to plugin/embed.go", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPluginExportPi(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	if code := run([]string{"plugin", "export", "--host", "pi", dir}, func(string) string { return "" }, nil, &out, &errb); code != 0 {
		t.Fatalf("%d %q", code, errb.String())
	}
	want, err := os.ReadFile("../../plugin/pi/mash.ts")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "mash.ts"))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("mash.ts: err %v, differs from the embedded file", err)
	}
	if des, _ := os.ReadDir(dir); len(des) != 1 {
		t.Fatalf("exported more than mash.ts: %v", des)
	}
}

// The default export is the Claude plugin alone: the Pi extension is not part of it.
func TestPluginExportDefaultHasNoPiFile(t *testing.T) {
	dir := t.TempDir()
	if err := exportPlugin(dir, plugin.Files); err != nil {
		t.Fatal(err)
	}
	var got []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			got = append(got, filepath.ToSlash(rel))
		}
		return nil
	})
	want := []string{".claude-plugin/marketplace.json"}
	for _, f := range pluginFiles {
		want = append(want, "plugin/"+f)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("default export wrote %v, want %v", got, want)
	}
}

func TestPluginExportUnknownHost(t *testing.T) {
	for _, args := range [][]string{{"--host", "codex", "d"}, {"--host"}, {"--host", "pi"}, {"--host", "pi", "a", "b"}} {
		var out, errb bytes.Buffer
		dir := t.TempDir()
		a := append([]string{"plugin", "export"}, args...)
		for i, x := range a {
			if x == "d" {
				a[i] = dir
			}
		}
		if code := run(a, func(string) string { return "" }, nil, &out, &errb); code != 2 || errb.Len() == 0 {
			t.Fatalf("%v: %d %q", a, code, errb.String())
		}
		if des, _ := os.ReadDir(dir); len(des) != 0 {
			t.Fatalf("%v wrote %v", a, des)
		}
	}
}

// The extension's own tests run with Node's built-in runner (Node 22.18 or later strips types). Skipped when node is
// missing or too old, so the Go suite does not depend on a toolchain CI may not have.
func TestPiExtensionNodeTests(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	v, _ := exec.Command(node, "-p", "process.features.typescript ? 1 : 0").Output()
	if strings.TrimSpace(string(v)) != "1" {
		t.Skip("node cannot strip types")
	}
	cmd := exec.Command(node, "--test", "plugin/pi/mash.test.ts") // a file: newer Node treats arguments as globs, not directories
	cmd.Dir = "../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node --test: %v\n%s", err, out)
	}
}
