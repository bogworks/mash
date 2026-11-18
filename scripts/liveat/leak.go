package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// seedRE matches an NKey seed of any kind (S then O, A, N, C, U, P or X, then base32); jwtRE a JWT.
// A hit in captured output breaks INV-001.
var (
	seedRE = regexp.MustCompile(`S[OANCUPX][A-Z2-7]{50,}`)
	jwtRE  = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*`)
)

// Leak names a file that holds a secret and what kind. It never holds the match.
type Leak struct {
	File string
	Kind string // "seed", "jwt", or the label of a specific secret
}

// scanBytes returns the kinds of secret found in b.
func scanBytes(b []byte) []string {
	var kinds []string
	if seedRE.Match(b) {
		kinds = append(kinds, "seed")
	}
	if jwtRE.Match(b) {
		kinds = append(kinds, "jwt")
	}
	return kinds
}

// walkFiles calls fn for every regular file under root, in sorted order.
func walkFiles(root string, fn func(path string, b []byte)) error {
	var paths []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(paths)
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		fn(p, b)
	}
	return nil
}

// scanDir looks for any seed or JWT in the files under dir (the captured output).
func scanDir(dir string) ([]Leak, error) {
	var leaks []Leak
	err := walkFiles(dir, func(p string, b []byte) {
		rel, _ := filepath.Rel(dir, p)
		for _, k := range scanBytes(b) {
			leaks = append(leaks, Leak{File: rel, Kind: k})
		}
	})
	return leaks, err
}

// scanFor looks under dir for the exact bytes of each named secret (a specific seed, a specific JWT).
func scanFor(dir string, secrets map[string][]byte) ([]Leak, error) {
	var leaks []Leak
	err := walkFiles(dir, func(p string, b []byte) {
		rel, _ := filepath.Rel(dir, p)
		for label, s := range secrets {
			if len(s) > 0 && bytes.Contains(b, s) {
				leaks = append(leaks, Leak{File: rel, Kind: label})
			}
		}
	})
	sort.Slice(leaks, func(i, j int) bool {
		if leaks[i].File != leaks[j].File {
			return leaks[i].File < leaks[j].File
		}
		return leaks[i].Kind < leaks[j].Kind
	})
	return leaks, err
}

// redact replaces anything shaped like a seed or a JWT, so a record cannot carry one by mistake.
func redact(s string) string {
	s = seedRE.ReplaceAllString(s, "[seed removed]")
	return jwtRE.ReplaceAllString(s, "[jwt removed]")
}

// registerKeyLines adds every body line of a PEM private key to secrets, named label+" line N" (N counts the body
// lines from 1). Armour (-----) lines and blank lines are skipped. A line is registered when it is at least 16 bytes
// (shorter ones are not unique enough to search for); the last body line is also registered from 8 bytes, since a
// PEM's last line is usually short. Names only ever reach a message, never the text.
func registerKeyLines(secrets map[string][]byte, label string, pemText []byte) {
	var body []string
	for _, l := range strings.Split(string(pemText), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "-----") {
			continue
		}
		body = append(body, l)
	}
	for i, l := range body {
		if len(l) >= 16 || (i == len(body)-1 && len(l) >= 8) {
			secrets[label+" line "+strconv.Itoa(i+1)] = []byte(l)
		}
	}
}
