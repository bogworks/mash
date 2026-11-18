package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"
)

// Record is the run record of contract section 8: revision, environment, scenario, expected, observed, run id.
type Record struct {
	RunID    string
	Revision string
	AT       string
	Scenario string
	Env      []string // lines: os/arch, versions, scratch names relative to the run dir
	Observed []Assertion
	Leaks    []Leak
	// LocalSubstrate: the run used --hosts local, so its result must not read as the contract's AT-005.
	LocalSubstrate bool
}

// newRunID is a UTC timestamp and a random suffix.
func newRunID(now time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:])
}

// Passed reports whether every assertion held and no secret was found.
func (r Record) Passed() bool {
	if len(r.Leaks) > 0 || len(r.Observed) == 0 {
		return false
	}
	for _, a := range r.Observed {
		if !a.Pass {
			return false
		}
	}
	return true
}

// Markdown renders the record. Everything goes through redact, so no seed or JWT can reach it.
func (r Record) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Run record %s: %s\n\n", r.AT, r.RunID)
	fmt.Fprintf(&b, "- Run id: %s\n- Revision: %s\n\n", r.RunID, r.Revision)
	b.WriteString("## Environment\n\n")
	for _, l := range r.Env {
		fmt.Fprintf(&b, "- %s\n", l)
	}
	b.WriteString("\n## Scenario\n\n" + r.Scenario + "\n\n## Expected\n\n")
	for _, a := range r.Observed {
		fmt.Fprintf(&b, "- %s\n", a.Name)
	}
	b.WriteString("\n## Observed\n\n")
	for _, a := range r.Observed {
		res := "pass"
		if !a.Pass {
			res = "FAIL"
		}
		fmt.Fprintf(&b, "- %s: %s", res, a.Name)
		if a.Detail != "" {
			fmt.Fprintf(&b, " (%s)", a.Detail)
		}
		b.WriteByte('\n')
	}
	for _, l := range r.Leaks {
		fmt.Fprintf(&b, "- FAIL: INV-001 a %s was found in %s\n", l.Kind, l.File)
	}
	res := "PASS"
	if !r.Passed() {
		res = "FAIL"
	}
	if r.LocalSubstrate {
		res = "local substrate: not AT-005 evidence (checks: " + res + ")"
	}
	fmt.Fprintf(&b, "\n## Result\n\n%s\n", res)
	return redact(b.String())
}

// WriteRecord writes the record to path with mode 0600 and refuses to overwrite.
func WriteRecord(path string, r Record) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(r.Markdown()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
