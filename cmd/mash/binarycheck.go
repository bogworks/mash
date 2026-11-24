package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bogworks/mash/internal/cmdcore/cmds"
	"github.com/bogworks/mash/internal/endpoint"
)

// The environment and the version are variables so tests inject them; nothing here runs a program.
var (
	// binaryPluginData is $CLAUDE_PLUGIN_DATA, the directory the plugin keeps its downloaded mash under.
	binaryPluginData = func() string { return os.Getenv("CLAUDE_PLUGIN_DATA") }
	// binaryPluginRoot is $CLAUDE_PLUGIN_ROOT, the plugin's own directory, where scripts/mash.pins names the version.
	// The MCP server does not get that variable but $MASH_PLUGIN_ROOT, which .mcp.json sets to the same directory.
	binaryPluginRoot = pluginRootFromEnv
	// binaryMashBin is $MASH_BIN, the binary the plugin's launcher runs when it is set.
	binaryMashBin = func() string { return os.Getenv("MASH_BIN") }
	// binaryOwnVersion is the version of the mash that is running doctor.
	binaryOwnVersion = endpoint.CurrentVersion
)

// pluginRootFromEnv is $CLAUDE_PLUGIN_ROOT, else $MASH_PLUGIN_ROOT.
func pluginRootFromEnv() string {
	if r := os.Getenv("CLAUDE_PLUGIN_ROOT"); r != "" {
		return r
	}
	return os.Getenv("MASH_PLUGIN_ROOT")
}

// pinVersionRE is the launcher's rule for the version line of scripts/mash.pins (plugin/scripts/mash): the first match.
var pinVersionRE = regexp.MustCompile(`(?m)^version=([0-9][0-9A-Za-z.+-]*)$`)

// ownVersionRE is a release version of this build: vN.N.N, with any -dev, -rc or +commit suffix.
var ownVersionRE = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)([-+].*)?$`)

// binaryCheck is the binary row: the mash the plugin's launcher runs, the copy $CLAUDE_PLUGIN_DATA/bin/mash-<version>
// where <version> is the one in $CLAUDE_PLUGIN_ROOT/scripts/mash.pins, against the mash running doctor. It reads the pin
// file and stats the copy, and runs nothing. It never fails and gives no fix: none is a command of ours, so the detail
// says how to make the two agree. Every skip is one short line and the warn names no path.
func binaryCheck(context.Context) cmds.HealthCheck {
	c := cmds.HealthCheck{Name: cmds.CheckBinary}
	skip := func(why string) cmds.HealthCheck { c.State, c.Detail = cmds.CheckSkip, why; return c }
	data := binaryPluginData()
	if data == "" {
		return skip("the plugin's mash is not visible from a shell")
	}
	if binaryMashBin() != "" {
		return skip("MASH_BIN is set; the plugin runs it")
	}
	const none = "the plugin runs no downloaded mash"
	root := binaryPluginRoot()
	if root == "" {
		return skip(none)
	}
	pins, err := os.ReadFile(filepath.Join(root, "scripts", "mash.pins"))
	if err != nil {
		if os.IsNotExist(err) {
			return skip(none)
		}
		return skip("the plugin's mash could not be read")
	}
	m := pinVersionRE.FindSubmatch(pins)
	if m == nil {
		return skip(none)
	}
	pinned := string(m[1])
	var full string
	if fi, err := os.Stat(filepath.Join(data, "bin", "mash-"+pinned)); err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
		return skip(none)
	}
	// The whole version is compared, a prerelease or build suffix included: strip a leading v and cut at a space.
	full, _, _ = strings.Cut(strings.TrimPrefix(binaryOwnVersion(), "v"), " ")
	own := ownVersionRE.FindStringSubmatch(full)
	if own == nil || own[1] == "0.0.0" {
		return skip("this mash has no release version")
	}
	if pinned == full {
		c.State, c.Detail = cmds.CheckOK, full
		return c
	}
	c.State = cmds.CheckWarn
	c.Detail = fmt.Sprintf("the plugin runs %s; this mash is %s: install %s, or set MASH_BIN", pinned, full, pinned)
	return c
}
