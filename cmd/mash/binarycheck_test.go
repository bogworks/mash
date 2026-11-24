package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/cmdcore/cmds"
)

// binaryShellRow is the binary row as doctor prints it at a shell (and in these tests, which run with no
// CLAUDE_PLUGIN_DATA): one line.
const binaryShellRow = "binary    skip  the plugin's mash is not visible from a shell\n"

// The tests run as a shell does: no plugin data, no plugin root, no MASH_BIN, whatever the environment of the run.
func init() {
	binaryPluginData = func() string { return "" }
	binaryPluginRoot = func() string { return "" }
	binaryMashBin = func() string { return "" }
}

// injectBinary sets the plugin data directory, the plugin root, MASH_BIN and this mash's version for one test.
func injectBinary(t *testing.T, data, root, mashBin, own string) {
	t.Helper()
	oldData, oldRoot, oldBin, oldOwn := binaryPluginData, binaryPluginRoot, binaryMashBin, binaryOwnVersion
	binaryPluginData = func() string { return data }
	binaryPluginRoot = func() string { return root }
	binaryMashBin = func() string { return mashBin }
	binaryOwnVersion = func() string { return own }
	t.Cleanup(func() {
		binaryPluginData, binaryPluginRoot, binaryMashBin, binaryOwnVersion = oldData, oldRoot, oldBin, oldOwn
	})
}

// pluginDirs makes a plugin root whose scripts/mash.pins holds pins (none when pins is nil) and a plugin data dir whose
// bin holds the named files, each with its mode.
func pluginDirs(t *testing.T, pins []byte, files map[string]os.FileMode) (data, root string) {
	t.Helper()
	data, root = t.TempDir(), t.TempDir()
	if pins != nil {
		if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "scripts", "mash.pins"), pins, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(data, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, mode := range files {
		p := filepath.Join(bin, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
	return data, root
}

func pins(version string) []byte {
	return []byte("# pins\nversion=" + version + "\nlinux_amd64=abc\n")
}

func TestBinaryCheck(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs unix modes")
	}
	const x, nx = os.FileMode(0o755), os.FileMode(0o644)
	const none = "the plugin runs no downloaded mash"
	for _, tc := range []struct {
		name    string
		pins    []byte
		files   map[string]os.FileMode
		noData  bool
		noRoot  bool
		mashBin string
		own     string
		state   string
		detail  string
	}{
		{name: "unset env", noData: true, own: "0.5.9", state: "skip", detail: "the plugin's mash is not visible from a shell"},
		{name: "MASH_BIN set", pins: pins("0.5.8"), files: map[string]os.FileMode{"mash-0.5.8": x}, mashBin: "/opt/mash", own: "0.5.9", state: "skip",
			detail: "MASH_BIN is set; the plugin runs it"},
		{name: "no plugin root", noRoot: true, files: map[string]os.FileMode{"mash-0.5.8": x}, own: "0.5.9", state: "skip", detail: none},
		{name: "no pin file", files: map[string]os.FileMode{"mash-0.5.8": x}, own: "0.5.9", state: "skip", detail: none},
		{name: "empty pin file", pins: []byte{}, files: map[string]os.FileMode{"mash-0.5.8": x}, own: "0.5.9", state: "skip", detail: none},
		{name: "no version line", pins: []byte("linux_amd64=abc\nversion=bad!\n"), files: map[string]os.FileMode{"mash-0.5.8": x}, own: "0.5.9", state: "skip", detail: none},
		{name: "pinned copy missing", pins: pins("0.5.8"), files: map[string]os.FileMode{"mash-0.5.7": x}, own: "0.5.8", state: "skip", detail: none},
		{name: "pinned copy not executable", pins: pins("0.5.8"), files: map[string]os.FileMode{"mash-0.5.8": nx}, own: "0.5.8", state: "skip", detail: none},
		{name: "matching", pins: pins("0.5.9"), files: map[string]os.FileMode{"mash-0.5.9": x}, own: "0.5.9", state: "ok", detail: "0.5.9"},
		{name: "matching with a v and a commit suffix", pins: pins("0.5.9"), files: map[string]os.FileMode{"mash-0.5.9": x}, own: "v0.5.9+abc1234", state: "ok", detail: "0.5.9"},
		{name: "differing", pins: pins("0.5.8"), files: map[string]os.FileMode{"mash-0.5.8": x}, own: "0.5.9", state: "warn",
			detail: "the plugin runs 0.5.8; this mash is 0.5.9: install 0.5.8, or set MASH_BIN"},
		{name: "stale copy beside the pinned one, matching", pins: pins("0.5.8"), files: map[string]os.FileMode{"mash-0.5.7": x, "mash-0.5.8": x}, own: "0.5.8", state: "ok", detail: "0.5.8"},
		{name: "stale copy beside the pinned one, differing", pins: pins("0.5.8"), files: map[string]os.FileMode{"mash-0.5.7": x, "mash-0.5.8": x}, own: "0.5.9", state: "warn",
			detail: "the plugin runs 0.5.8; this mash is 0.5.9: install 0.5.8, or set MASH_BIN"},
		{name: "unparseable own version", pins: pins("0.5.8"), files: map[string]os.FileMode{"mash-0.5.8": x}, own: "dev", state: "skip", detail: "this mash has no release version"},
		{name: "dev build", pins: pins("0.5.8"), files: map[string]os.FileMode{"mash-0.5.8": x}, own: "0.0.0-dev", state: "skip", detail: "this mash has no release version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, root := pluginDirs(t, tc.pins, tc.files)
			if tc.noData {
				data = ""
			}
			if tc.noRoot {
				root = ""
			}
			injectBinary(t, data, root, tc.mashBin, tc.own)
			c := binaryCheck(context.Background())
			if c.Name != cmds.CheckBinary || c.State != tc.state || c.Fix != "" || c.Detail != tc.detail {
				t.Fatalf("%+v, want state %s detail %q", c, tc.state, tc.detail)
			}
			if strings.Contains(c.Detail, data) && data != "" || strings.Contains(c.Detail, root) && root != "" {
				t.Errorf("detail names a path: %q", c.Detail)
			}
			if len("binary    skip  "+c.Detail) > 80 && c.State == "skip" {
				t.Errorf("a skip row is over 80 columns: %q", c.Detail)
			}
		})
	}
}

// A pin file that cannot be read is a skip with the reason, never a failure.
func TestBinaryCheckUnreadablePins(t *testing.T) {
	data, root := pluginDirs(t, nil, nil)
	// scripts/mash.pins is a directory, so reading it as a file fails with something other than not-exist
	if err := os.MkdirAll(filepath.Join(root, "scripts", "mash.pins"), 0o755); err != nil {
		t.Fatal(err)
	}
	injectBinary(t, data, root, "", "0.5.9")
	c := binaryCheck(context.Background())
	if c.State != "skip" || c.Detail != "the plugin's mash could not be read" {
		t.Fatalf("%+v", c)
	}
}

// With a plugin data path of about 70 characters, every doctor line is at most 80 columns, the warn row included.
func TestBinaryRowsFitEightyColumns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs unix modes")
	}
	base := t.TempDir()
	long := filepath.Join(base, strings.Repeat("d", 70-len(base)-1))
	if err := os.MkdirAll(filepath.Join(long, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(long, "bin", "mash-0.5.8"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, root := pluginDirs(t, pins("0.5.8"), nil)
	w := tlsWorld(t, "a")
	env := strictEnv(w, "a", map[string]string{"MASH_SERVER": "tls://127.0.0.1:1", "MASH_OPEN_TIMEOUT": "2s"})
	for _, own := range []string{"0.5.9", "0.5.8", "0.0.0-dev"} {
		injectBinary(t, long, root, "", own)
		r := runMash(env, "doctor").out
		for _, l := range strings.Split(r, "\n") {
			if len(l) > 80 {
				t.Errorf("own %s: %d columns: %q", own, len(l), l)
			}
		}
		if own == "0.5.9" && !strings.Contains(r, "binary    warn  the plugin runs 0.5.8; this mash is 0.5.9: install 0.5.8, or set\n                MASH_BIN\n") {
			t.Errorf("warn row: %q", r)
		}
	}
}

func TestDoctorPathChecksBinary(t *testing.T) {
	injectBinary(t, "", "", "", "0.5.9")
	for name, got := range map[string]cmds.HealthCheck{
		"shell":   (&cliSvc{}).BinaryCheck(context.Background()),
		"session": sessionSvc{}.BinaryCheck(context.Background()),
	} {
		if got.Name != "binary" || got.State != "skip" {
			t.Errorf("%s: %+v", name, got)
		}
	}
}
