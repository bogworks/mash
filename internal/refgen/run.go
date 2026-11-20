package refgen

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func sortedNames(pages map[string][]byte) []string {
	names := make([]string, 0, len(pages))
	for n := range pages {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Run generates the pages from specDir, writes them under outDir, and removes
// banner-carrying files that are no longer generated. Files without the
// banner are never removed.
func Run(specDir, outDir string) error {
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
	have, err := listFiles(outDir, true)
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

// listFiles lists, slash-separated and sorted, the files under dir. With
// bannerOnly it keeps only those whose first line is the generated-file banner.
func listFiles(dir string, bannerOnly bool) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if bannerOnly {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if !strings.HasPrefix(string(b), banner+"\n") {
				return nil
			}
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
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
// path: a page that is missing, a page that changed, and a file that is not a
// generated page, banner or not, at any depth. An empty result means refDir is
// current. A failure to generate or read is returned as a single line.
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
	have, err := listFiles(refDir, false)
	if err != nil {
		return append(out, "cannot read "+refDir+": "+err.Error())
	}
	for _, n := range have {
		if _, ok := pages[n]; !ok {
			out = append(out, "extra: "+n)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return PathOf(out[i]) < PathOf(out[j])
	})
	return out
}

// PathOf returns the path in one line of CheckDir output.
func PathOf(line string) string {
	if _, p, ok := strings.Cut(line, ": "); ok {
		return p
	}
	return line
}
