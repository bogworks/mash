package main

import (
	"fmt"
	"path"
	"strings"

	"github.com/bogworks/mash/internal/adrindex"
)

// Release preparation writes the change log and the ADR index once, so a pull request never edits them (ADR 0068).
// The gate proves, from git at X (never the working tree), that preparation happened.

const (
	changelogDir = "changelog.d"
	decisionsDir = "docs/decisions"
	adrIndexFile = decisionsDir + "/INDEX.md"
)

// changelogAssembled refuses a candidate that still has a fragment under changelog.d/ (README.md is the convention's
// description, not a fragment): `changelog assemble` was not run.
func changelogAssembled(o Options, x string) error {
	out, err := git(o.Repo, "ls-tree", "-r", "--name-only", x, "--", changelogDir)
	if err != nil {
		return op(err)
	}
	var left []string
	for _, p := range strings.Split(out, "\n") {
		if p = strings.TrimSpace(p); p != "" && p != changelogDir+"/README.md" {
			left = append(left, path.Base(p))
		}
	}
	if len(left) > 0 {
		return fmt.Errorf("changelog fragments were not assembled: run go run ./scripts/changelog assemble --version %s --date YYYY-MM-DD (still in %s/: %s)", o.Version, changelogDir, strings.Join(left, ", "))
	}
	return nil
}

// adrIndexFresh refuses a candidate whose docs/decisions/INDEX.md is missing or differs from what adrindex generates
// from the records at X. A candidate without records has nothing to index.
func adrIndexFresh(o Options, x string) error {
	out, err := git(o.Repo, "ls-tree", "--name-only", x, decisionsDir+"/")
	if err != nil {
		return op(err)
	}
	var files []adrindex.File
	for _, p := range strings.Split(out, "\n") {
		n := path.Base(strings.TrimSpace(p))
		if !adrindex.NameRE.MatchString(n) {
			continue
		}
		body, has, err := fileAt(o.Repo, x, decisionsDir+"/"+n)
		if err != nil {
			return err
		}
		if has {
			files = append(files, adrindex.File{Name: n, Content: body})
		}
	}
	if len(files) == 0 {
		return nil
	}
	have, has, err := fileAt(o.Repo, x, adrIndexFile)
	if err != nil {
		return err
	}
	if !has || have != adrindex.Generate(files) {
		return fmt.Errorf("%s is stale: run go run ./scripts/adrlint index --write", adrIndexFile)
	}
	return nil
}
