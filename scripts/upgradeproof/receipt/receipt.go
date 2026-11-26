// Package receipt is the AT-011 receipt the upgrade-proof runner writes, in exactly the shape the release gate reads
// (scripts/releasecheck/receipts.go, REQ-019). The gate is package main and cannot be imported, so the field names and
// JSON tags are repeated here; scripts/releasecheck/receipts_at011runner_test.go reads a receipt this package wrote and
// holds it to the gate's own check, so a drift between the two fails there.
package receipt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
)

// Kind and the results the gate recognises.
const (
	KindAT011    = "at-011"
	ResultPassed = "passed"
	ResultFailed = "failed"
)

// BuildConfig is the build configuration of an executable: toolchain, build tags and flags.
type BuildConfig struct {
	Toolchain string   `json:"toolchain"`
	Tags      []string `json:"tags"`
	Flags     []string `json:"flags"`
}

// Exec is one executable on one platform, with its digest and build configuration.
type Exec struct {
	Name   string      `json:"name"`
	GOOS   string      `json:"goos"`
	GOARCH string      `json:"goarch"`
	SHA256 string      `json:"sha256"`
	Build  BuildConfig `json:"build"`
}

// Prior is the digest of one binary of the previous published release.
type Prior struct {
	Name   string `json:"name"`
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
	SHA256 string `json:"sha256"`
}

// Receipt is one AT-011 receipt. Only the fields an AT-011 receipt uses are here; the gate's reader refuses unknown
// fields, and exit_status is the one it always carries.
type Receipt struct {
	Kind          string  `json:"kind"`
	Commit        string  `json:"commit"`
	Environment   string  `json:"environment"`
	Result        string  `json:"result"`
	Executables   []Exec  `json:"executables,omitempty"`
	InstallSHA256 string  `json:"install_sh_sha256,omitempty"`
	SumsSHA256    string  `json:"sha256sums_sha256,omitempty"`
	PrevVersion   string  `json:"previous_version,omitempty"`
	Prior         []Prior `json:"previous_artifacts,omitempty"`
	UpgradePage   string  `json:"upgrade_page_blob,omitempty"`
	ExitStatus    int     `json:"exit_status"`
}

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Problems lists what is missing or malformed in r on the fields the gate binds. A receipt with a problem is never
// written as passed.
func (r Receipt) Problems() []string {
	var ps []string
	bad := func(f, why string) { ps = append(ps, f+": "+why) }
	if r.Kind != KindAT011 {
		bad("kind", "not "+KindAT011)
	}
	if len(r.Commit) != 40 {
		bad("commit", "not a full commit id")
	}
	if r.Environment == "" {
		bad("environment", "empty")
	}
	if !hex64.MatchString(r.InstallSHA256) {
		bad("install_sh_sha256", "missing or not a sha256")
	}
	if !hex64.MatchString(r.SumsSHA256) {
		bad("sha256sums_sha256", "missing or not a sha256")
	}
	if r.PrevVersion == "" {
		bad("previous_version", "empty")
	}
	if r.UpgradePage == "" {
		bad("upgrade_page_blob", "empty")
	}
	if len(r.Executables) == 0 {
		bad("executables", "none")
	}
	for _, e := range r.Executables {
		if !hex64.MatchString(e.SHA256) || e.Build.Toolchain == "" {
			bad("executable "+e.Name+" "+e.GOOS+"/"+e.GOARCH, "no sha256 or toolchain")
		}
	}
	if len(r.Prior) == 0 {
		bad("previous_artifacts", "none")
	}
	for _, p := range r.Prior {
		if !hex64.MatchString(p.SHA256) {
			bad("previous artifact "+p.Name+" "+p.GOOS+"/"+p.GOARCH, "no sha256")
		}
	}
	return ps
}

// Marshal is r as the indented JSON the gate's reader takes, ending in a newline. Tags is never null.
func Marshal(r Receipt) ([]byte, error) {
	for i := range r.Executables {
		if r.Executables[i].Build.Tags == nil {
			r.Executables[i].Build.Tags = []string{}
		}
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("receipt: %w", err)
	}
	return append(b, '\n'), nil
}

// FileName is the name of the receipt of a platform, in the directory the gate reads.
func FileName(goos, goarch string) string { return "at-011-" + goos + "-" + goarch + ".json" }

// Unmarshal reads a receipt this package wrote, refusing fields it does not know, as the gate's reader does.
func Unmarshal(b []byte) (Receipt, error) {
	var r Receipt
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return Receipt{}, fmt.Errorf("receipt: %w", err)
	}
	return r, nil
}
