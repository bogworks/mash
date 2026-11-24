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
// CLAUDE_PLUGIN_DATA), broken to 80 columns.
const binaryShellRow = "binary    skip  not run by the plugin: CLAUDE_PLUGIN_DATA is not set, so the\n" +
	"                plugin's binary cannot be found from a shell\n"

// The tests run as a shell does: no plugin data, no MASH_BIN, whatever the environment of the run.
func init() {
	binaryPluginData = func() string { return "" }
	binaryMashBin = func() string { return "" }
}

// injectBinary sets the plugin data directory, MASH_BIN and this mash's version for one test.
func injectBinary(t *testing.T, data, mashBin, own string) {
	t.Helper()
	oldData, oldBin, oldOwn := binaryPluginData, binaryMashBin, binaryOwnVersion
	binaryPluginData = func() string { return data }
	binaryMashBin = func() string { return mashBin }
	binaryOwnVersion = func() string { return own }
	t.Cleanup(func() { binaryPluginData, binaryMashBin, binaryOwnVersion = oldData, oldBin, oldOwn })
}

// pluginBin makes <data>/bin with the named files, each with mode.
func pluginBin(t *testing.T, files map[string]os.FileMode) (data string) {
	t.Helper()
	data = t.TempDir()
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
	return data
}

func TestBinaryCheck(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs unix modes")
	}
	const x, nx = os.FileMode(0o755), os.FileMode(0o644)
	for _, tc := range []struct {
		name    string
		data    func(t *testing.T) string
		mashBin string
		own     string
		state   string
		detail  func(data string) string // the whole detail, or "" to use contains
		contain []string
	}{
		{name: "unset env", data: func(*testing.T) string { return "" }, own: "0.5.9", state: "skip",
			detail: func(string) string {
				return "not run by the plugin: CLAUDE_PLUGIN_DATA is not set, so the plugin's binary cannot be found from a shell"
			}},
		{name: "empty bin dir", data: func(t *testing.T) string { return pluginBin(t, nil) }, own: "0.5.9", state: "skip",
			detail: func(d string) string {
				return "the plugin has no downloaded mash in " + filepath.Join(d, "bin") + " (unpinned, or it uses MASH_BIN or the one on PATH)"
			}},
		{name: "no bin dir", data: func(t *testing.T) string { return t.TempDir() }, own: "0.5.9", state: "skip", contain: []string{"has no downloaded mash"}},
		{name: "MASH_BIN set", data: func(t *testing.T) string { return pluginBin(t, map[string]os.FileMode{"mash-0.5.8": x}) }, mashBin: "/opt/mash", own: "0.5.9", state: "skip",
			detail: func(string) string { return "MASH_BIN is set; the plugin runs it" }},
		{name: "matching", data: func(t *testing.T) string { return pluginBin(t, map[string]os.FileMode{"mash-0.5.9": x}) }, own: "0.5.9", state: "ok",
			detail: func(string) string { return "0.5.9" }},
		{name: "matching with a v and a commit suffix", data: func(t *testing.T) string { return pluginBin(t, map[string]os.FileMode{"mash-0.5.9": x}) }, own: "v0.5.9+abc1234", state: "ok",
			detail: func(string) string { return "0.5.9" }},
		{name: "differing", data: func(t *testing.T) string { return pluginBin(t, map[string]os.FileMode{"mash-0.5.8": x}) }, own: "0.5.9", state: "warn",
			contain: []string{"the plugin runs 0.5.8 (", "bin/mash-0.5.8); this mash is 0.5.9", "install the matching release, or set MASH_BIN to the one you want"}},
		{name: "two copies, one differs", data: func(t *testing.T) string {
			return pluginBin(t, map[string]os.FileMode{"mash-0.5.9": x, "mash-0.5.8": x})
		}, own: "0.5.9", state: "warn", contain: []string{"0.5.8 (", "this mash is 0.5.9"}},
		{name: "unparseable own version", data: func(t *testing.T) string { return pluginBin(t, map[string]os.FileMode{"mash-0.5.8": x}) }, own: "dev", state: "skip",
			detail: func(string) string { return "this mash has no release version to compare" }},
		{name: "dev build", data: func(t *testing.T) string { return pluginBin(t, map[string]os.FileMode{"mash-0.5.8": x}) }, own: "0.0.0-dev", state: "skip",
			detail: func(string) string { return "this mash has no release version to compare" }},
		{name: "a stray .dl-failed file is ignored", data: func(t *testing.T) string {
			return pluginBin(t, map[string]os.FileMode{".dl-failed-0.5.8": x, "mash-0.5.9": x})
		}, own: "0.5.9", state: "ok", detail: func(string) string { return "0.5.9" }},
		{name: "other names are ignored", data: func(t *testing.T) string {
			return pluginBin(t, map[string]os.FileMode{"mash-0.5.8.tmp": x, "mash-latest": x, "mash-0.5": x, "mash": x})
		}, own: "0.5.9", state: "skip", contain: []string{"has no downloaded mash"}},
		{name: "a non-executable copy is ignored", data: func(t *testing.T) string { return pluginBin(t, map[string]os.FileMode{"mash-0.5.8": nx}) }, own: "0.5.9", state: "skip",
			contain: []string{"has no downloaded mash"}},
		{name: "a non-executable differing copy beside a matching one", data: func(t *testing.T) string {
			return pluginBin(t, map[string]os.FileMode{"mash-0.5.8": nx, "mash-0.5.9": x})
		}, own: "0.5.9", state: "ok", detail: func(string) string { return "0.5.9" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.data(t)
			injectBinary(t, data, tc.mashBin, tc.own)
			c := binaryCheck(context.Background())
			if c.Name != cmds.CheckBinary || c.State != tc.state || c.Fix != "" {
				t.Fatalf("%+v", c)
			}
			if tc.detail != nil {
				if want := tc.detail(data); c.Detail != want {
					t.Fatalf("detail %q, want %q", c.Detail, want)
				}
			}
			for _, s := range tc.contain {
				if !strings.Contains(c.Detail, s) {
					t.Errorf("detail %q lacks %q", c.Detail, s)
				}
			}
		})
	}
}

// A directory that cannot be read is a skip with the reason, never a failure.
func TestBinaryCheckUnreadableDir(t *testing.T) {
	data := t.TempDir()
	// bin is a file, so reading it as a directory fails with something other than not-exist
	if err := os.WriteFile(filepath.Join(data, "bin"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	injectBinary(t, data, "", "0.5.9")
	c := binaryCheck(context.Background())
	if c.State != "skip" || !strings.Contains(c.Detail, "could not be read") {
		t.Fatalf("%+v", c)
	}
}

func TestDoctorPathChecksBinary(t *testing.T) {
	injectBinary(t, "", "", "0.5.9")
	for name, got := range map[string]cmds.HealthCheck{
		"shell":   (&cliSvc{}).BinaryCheck(context.Background()),
		"session": sessionSvc{}.BinaryCheck(context.Background()),
	} {
		if got.Name != "binary" || got.State != "skip" {
			t.Errorf("%s: %+v", name, got)
		}
	}
}
