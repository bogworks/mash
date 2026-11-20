package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	specDir := flag.String("spec", "spec", "folder holding the spec JSON files")
	outDir := flag.String("out", "docs/reference", "folder to write the pages into")
	flag.Parse()
	if err := run(*specDir, *outDir); err != nil {
		fmt.Fprintln(os.Stderr, "refgen:", err)
		os.Exit(1)
	}
}

func sortedNames(pages map[string][]byte) []string {
	names := make([]string, 0, len(pages))
	for n := range pages {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// run generates the pages, writes them, and removes banner-carrying files
// that are no longer generated.
func run(specDir, outDir string) error {
	pages, err := Generate(specDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	for _, n := range sortedNames(pages) {
		dst := filepath.Join(outDir, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if old, err := os.ReadFile(dst); err == nil && string(old) == string(pages[n]) {
			continue
		}
		if err := os.WriteFile(dst, pages[n], 0o644); err != nil {
			return err
		}
	}
	have, err := bannerFiles(outDir)
	if err != nil {
		return err
	}
	for _, n := range have {
		if _, ok := pages[n]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(outDir, filepath.FromSlash(n))); err != nil {
			return err
		}
	}
	return nil
}

// bannerFiles lists, slash-separated and sorted, the files under dir whose
// first line is the generated-file banner.
func bannerFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.HasPrefix(string(b), banner+"\n") {
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	sort.Strings(out)
	return out, err
}

// CheckDir regenerates the reference from specDir in memory and compares it
// with refDir byte for byte. It returns one line per difference, sorted by
// path: a page that is missing, a page that changed, and a banner-carrying
// file that is no longer generated. An empty result means refDir is current.
// A failure to generate or read is returned as a single line.
func CheckDir(specDir, refDir string) []string {
	pages, err := Generate(specDir)
	if err != nil {
		return []string{"cannot generate: " + err.Error()}
	}
	var out []string
	for _, n := range sortedNames(pages) {
		got, err := os.ReadFile(filepath.Join(refDir, filepath.FromSlash(n)))
		switch {
		case err != nil:
			out = append(out, "missing: "+n)
		case string(got) != string(pages[n]):
			out = append(out, "changed: "+n)
		}
	}
	have, err := bannerFiles(refDir)
	if err != nil {
		return append(out, "cannot read "+refDir+": "+err.Error())
	}
	for _, n := range have {
		if _, ok := pages[n]; !ok {
			out = append(out, "extra: "+n)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return pathOf(out[i]) < pathOf(out[j])
	})
	return out
}

func pathOf(line string) string {
	if _, p, ok := strings.Cut(line, ": "); ok {
		return p
	}
	return line
}
