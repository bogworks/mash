package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bogworks/mash/internal/cmdcore/cmds"
	"github.com/bogworks/mash/internal/endpoint"
)

// The environment and the version are variables so tests inject them; nothing here runs a program.
var (
	// binaryPluginData is $CLAUDE_PLUGIN_DATA, the directory the plugin keeps its downloaded mash under.
	binaryPluginData = func() string { return os.Getenv("CLAUDE_PLUGIN_DATA") }
	// binaryMashBin is $MASH_BIN, the binary the plugin's launcher runs when it is set.
	binaryMashBin = func() string { return os.Getenv("MASH_BIN") }
	// binaryOwnVersion is the version of the mash that is running doctor.
	binaryOwnVersion = endpoint.CurrentVersion
)

// managedName is the file name of a copy the plugin's launcher downloads: mash-<N.N.N>, nothing else.
var managedName = regexp.MustCompile(`^mash-(\d+\.\d+\.\d+)$`)

// ownVersionRE is a release version of this build: vN.N.N, with any -dev, -rc or +commit suffix.
var ownVersionRE = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)([-+].*)?$`)

// binaryCheck is the binary row: the mash the plugin runs (its own copy under $CLAUDE_PLUGIN_DATA/bin) against the
// mash running doctor. It reads one directory and runs nothing. It never fails and gives no fix: none is a command of
// ours, so the detail says how to make the two agree.
func binaryCheck(context.Context) cmds.HealthCheck {
	c := cmds.HealthCheck{Name: cmds.CheckBinary}
	dir := binaryPluginData()
	if dir == "" {
		c.State, c.Detail = cmds.CheckSkip, "not run by the plugin: CLAUDE_PLUGIN_DATA is not set, so the plugin's binary cannot be found from a shell"
		return c
	}
	if binaryMashBin() != "" {
		c.State, c.Detail = cmds.CheckSkip, "MASH_BIN is set; the plugin runs it"
		return c
	}
	bin := filepath.Join(dir, "bin")
	entries, err := os.ReadDir(bin)
	if err != nil && !os.IsNotExist(err) {
		c.State, c.Detail = cmds.CheckSkip, fmt.Sprintf("%s could not be read: %v", bin, err)
		return c
	}
	type copyOf struct{ version, path string }
	var found []copyOf
	for _, e := range entries {
		m := managedName.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		p := filepath.Join(bin, e.Name())
		if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
			continue
		}
		found = append(found, copyOf{m[1], p})
	}
	if len(found) == 0 {
		c.State, c.Detail = cmds.CheckSkip, fmt.Sprintf("the plugin has no downloaded mash in %s (unpinned, or it uses MASH_BIN or the one on PATH)", bin)
		return c
	}
	own := ownVersionRE.FindStringSubmatch(binaryOwnVersion())
	if own == nil || own[1] == "0.0.0" {
		c.State, c.Detail = cmds.CheckSkip, "this mash has no release version to compare"
		return c
	}
	sort.Slice(found, func(i, j int) bool { return found[i].path < found[j].path })
	var differ []string
	for _, f := range found {
		if f.version != own[1] {
			differ = append(differ, fmt.Sprintf("%s (%s)", f.version, f.path))
		}
	}
	if len(differ) == 0 {
		c.State, c.Detail = cmds.CheckOK, own[1]
		return c
	}
	c.State = cmds.CheckWarn
	c.Detail = fmt.Sprintf("the plugin runs %s; this mash is %s: install the matching release, or set MASH_BIN to the one you want", strings.Join(differ, ", "), own[1])
	return c
}
