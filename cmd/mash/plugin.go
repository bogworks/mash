package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/bogworks/mash/plugin"
)

const pluginUsage = "usage: mash plugin export <dir>"

// pluginCmd runs `mash plugin ...`. It needs no config: it runs during an install, before any endpoint exists.
func pluginCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "export" {
		fmt.Fprintln(stderr, pluginUsage)
		return 2
	}
	dir, err := filepath.Abs(args[1])
	if err == nil {
		err = exportPlugin(dir, plugin.Files)
	}
	if err != nil {
		fmt.Fprintf(stderr, "mash: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, dir)
	return 0
}

// exportPlugin writes the plugin in files, and a one-entry marketplace that points at it, into dir. Only those
// files are written (each atomically); anything else in dir is left alone.
func exportPlugin(dir string, files fs.FS) error {
	b, err := fs.ReadFile(files, ".claude-plugin/plugin.json")
	if err != nil {
		return err
	}
	mkt, err := marketplaceJSON(b)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, ".claude-plugin", "marketplace.json"), mkt, 0o644); err != nil {
		return err
	}
	return fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(files, p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if p == "scripts/mash" {
			mode = 0o755 // the MCP server runs the launcher directly
		}
		return writeFileAtomic(filepath.Join(dir, "plugin", filepath.FromSlash(p)), data, mode)
	})
}

// marketplaceJSON builds the one-entry marketplace.json for the plugin whose plugin.json is pluginJSON. The owner
// is the plugin's author; a missing author name falls back to the plugin name and a missing url is omitted.
func marketplaceJSON(pluginJSON []byte) ([]byte, error) {
	var pl struct {
		Description, Version string
		Author               struct{ Name, URL string }
	}
	if err := json.Unmarshal(pluginJSON, &pl); err != nil {
		return nil, fmt.Errorf("plugin.json: %w", err)
	}
	type entry struct {
		Name        string `json:"name"`
		Source      string `json:"source"`
		Description string `json:"description"`
		Version     string `json:"version"`
	}
	owner := map[string]string{"name": pl.Author.Name}
	if pl.Author.Name == "" {
		owner["name"] = "mash"
	}
	if pl.Author.URL != "" {
		owner["url"] = pl.Author.URL
	}
	mkt, _ := json.MarshalIndent(map[string]any{
		"name":    "mash",
		"owner":   owner,
		"plugins": []entry{{"mash", "./plugin", pl.Description, pl.Version}},
	}, "", "  ")
	return append(mkt, '\n'), nil
}

// writeFileAtomic writes path (given mode, parents 0755) through a temp file and a rename, so a reader never sees half a file.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".mash-export-*")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), mode)
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}
