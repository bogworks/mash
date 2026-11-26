package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The REQ-019 gate check: the upgrade proofs (AT-011, AT-012) are bound to the candidate. The gate computes what a
// receipt must say (Expected, from its own builds of the candidate) and checkReceipts holds every receipt to it. A
// receipt that passes but differs on any field, or covers fewer executables or platforms than the release ships,
// counts as missing, and so does a failed one. Every problem is reported, and anything missing or empty fails closed.

// proofLine is the release line whose gate runs the check.
type proofLine int

const (
	// lineAT011 is the 0.5.x candidate that carries S1 to S3: AT-011 receipts only.
	lineAT011 proofLine = iota + 1
	// lineSwitchOn is the switch-on release: AT-011 and AT-012 receipts.
	lineSwitchOn
)

const (
	kindAT011 = "at-011"
	kindAT012 = "at-012"

	resultPassed = "passed"
	resultFailed = "failed"
)

// Platform is one OS and architecture of the release build.
type Platform struct {
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
}

func (p Platform) String() string { return p.GOOS + "/" + p.GOARCH }

// BuildConfig is the build configuration of an executable: toolchain, build tags and flags.
type BuildConfig struct {
	Toolchain string   `json:"toolchain"`
	Tags      []string `json:"tags"`
	Flags     []string `json:"flags"`
}

func (b BuildConfig) diff(o BuildConfig) string {
	if b.Toolchain != o.Toolchain {
		return "toolchain differs"
	}
	if !sameSet(b.Tags, o.Tags) {
		return "build tags differ"
	}
	if !sameList(b.Flags, o.Flags) {
		return "build flags differ"
	}
	return ""
}

// Exec is one executable (mash or mashctl) on one platform, with its digest and build configuration.
type Exec struct {
	Name   string      `json:"name"`
	GOOS   string      `json:"goos"`
	GOARCH string      `json:"goarch"`
	SHA256 string      `json:"sha256"`
	Build  BuildConfig `json:"build"`
}

func (e Exec) label() string { return e.Name + " " + e.GOOS + "/" + e.GOARCH }

// Prior is the digest of one binary of the previous published release.
type Prior struct {
	Name   string `json:"name"`
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
	SHA256 string `json:"sha256"`
}

// Receipt is one upgrade-proof receipt. Kind selects the fields that apply: AT-011 uses Executables, InstallSHA256,
// SumsSHA256, PrevVersion, Prior and UpgradePage; AT-012 uses OlderVersion, NewerVersion, the four binary digests,
// CorpusSHA256, GOOS, GOARCH and ExitStatus. Commit, Environment and Result apply to both.
type Receipt struct {
	Kind        string `json:"kind"`
	Commit      string `json:"commit"`
	Environment string `json:"environment"`
	Result      string `json:"result"`

	// AT-011.
	Executables   []Exec  `json:"executables,omitempty"`
	InstallSHA256 string  `json:"install_sh_sha256,omitempty"`
	SumsSHA256    string  `json:"sha256sums_sha256,omitempty"`
	PrevVersion   string  `json:"previous_version,omitempty"`
	Prior         []Prior `json:"previous_artifacts,omitempty"`
	UpgradePage   string  `json:"upgrade_page_blob,omitempty"`

	// AT-012.
	OlderVersion    string `json:"older_version,omitempty"`
	NewerVersion    string `json:"newer_version,omitempty"`
	OlderMashSHA256 string `json:"older_mash_sha256,omitempty"`
	OlderMashctlSHA string `json:"older_mashctl_sha256,omitempty"`
	NewerMashSHA256 string `json:"newer_mash_sha256,omitempty"`
	NewerMashctlSHA string `json:"newer_mashctl_sha256,omitempty"`
	CorpusSHA256    string `json:"mail_corpus_sha256,omitempty"`
	GOOS            string `json:"goos,omitempty"`
	GOARCH          string `json:"goarch,omitempty"`
	ExitStatus      int    `json:"exit_status"`
}

// Expected is what the gate computed itself, from its own build of the candidate.
type Expected struct {
	Commit      string
	Platforms   []Platform
	Executables []Exec // mash and mashctl on every platform (the eight binaries)
	InstallSHA  string
	SumsSHA     string
	PrevVersion string
	Prior       []Prior // each binary of the previous published release
	UpgradePage string  // git blob id of the upgrade page in the candidate

	// AT-012 only (lineSwitchOn).
	OlderVersion string
	NewerVersion string
	CorpusSHA    string
}

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

var executableNames = []string{"mash", "mashctl"}

// validate reports what is missing or malformed in the Expected itself; with any problem here the gate cannot pass.
func (exp Expected) validate(line proofLine) []string {
	var ps []string
	bad := func(f, why string) { ps = append(ps, "expected "+f+": "+why) }
	if exp.Commit == "" {
		bad("candidate commit", "empty")
	}
	if len(exp.Platforms) == 0 {
		bad("platforms", "none")
	}
	if exp.UpgradePage == "" {
		bad("upgrade page blob id", "empty")
	}
	if exp.PrevVersion == "" {
		bad("previous version", "empty")
	}
	for _, f := range []struct{ n, v string }{{"install.sh sha256", exp.InstallSHA}, {"SHA256SUMS sha256", exp.SumsSHA}} {
		if !hex64.MatchString(f.v) {
			bad(f.n, "missing or not a sha256")
		}
	}
	for _, p := range exp.Platforms {
		if p.GOOS == "" || p.GOARCH == "" {
			bad("platform "+p.String(), "empty goos or goarch")
			continue
		}
		for _, n := range executableNames {
			e, ok := findExec(exp.Executables, n, p)
			if !ok {
				bad(n+" "+p.String(), "no build digest")
				continue
			}
			if !hex64.MatchString(e.SHA256) {
				bad(e.label()+" sha256", "missing or not a sha256")
			}
			if e.Build.Toolchain == "" {
				bad(e.label()+" toolchain", "empty")
			}
			pr, ok := findPrior(exp.Prior, n, p)
			if !ok || !hex64.MatchString(pr.SHA256) {
				bad("previous release "+n+" "+p.String(), "no sha256")
			}
		}
	}
	if line == lineSwitchOn {
		if exp.OlderVersion == "" {
			bad("older version", "empty")
		}
		if exp.NewerVersion == "" {
			bad("newer version", "empty")
		}
		if !hex64.MatchString(exp.CorpusSHA) {
			bad("mail corpus sha256", "missing or not a sha256")
		}
	}
	return ps
}

func findExec(es []Exec, name string, p Platform) (Exec, bool) {
	for _, e := range es {
		if e.Name == name && e.GOOS == p.GOOS && e.GOARCH == p.GOARCH {
			return e, true
		}
	}
	return Exec{}, false
}

func findPrior(ps []Prior, name string, p Platform) (Prior, bool) {
	for _, e := range ps {
		if e.Name == name && e.GOOS == p.GOOS && e.GOARCH == p.GOARCH {
			return e, true
		}
	}
	return Prior{}, false
}

func sameList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameSet(a, b []string) bool {
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	return sameList(x, y)
}

// loadReceipts reads every *.json file of dir. An unreadable directory or file, or a file that is not a receipt,
// is an error, so the gate fails closed. Unknown fields are an error too.
func loadReceipts(dir string) ([]Receipt, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("receipts: %w", err)
	}
	var rs []Receipt
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("receipts: %w", err)
		}
		var r Receipt
		dec := json.NewDecoder(strings.NewReader(string(b)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&r); err != nil {
			return nil, fmt.Errorf("receipts: %s: %w", path, err)
		}
		if dec.More() {
			return nil, fmt.Errorf("receipts: %s: trailing data after the receipt", path)
		}
		rs = append(rs, r)
	}
	return rs, nil
}

// checkReceipts holds rs to exp for the release line and returns every problem; none means the proofs are bound.
func checkReceipts(exp Expected, rs []Receipt, line proofLine) []string {
	if line != lineAT011 && line != lineSwitchOn {
		return []string{"receipts: unknown release line"}
	}
	if ps := exp.validate(line); len(ps) > 0 {
		return ps
	}
	ps := checkAT011(exp, rs)
	if line == lineSwitchOn {
		ps = append(ps, checkAT012(exp, rs)...)
	}
	return ps
}

// receiptLevel11 lists why an AT-011 receipt does not match exp on the fields that are not per executable.
func receiptLevel11(exp Expected, r Receipt) []string {
	var why []string
	if r.Result != resultPassed {
		why = append(why, "result is not passed")
	}
	if r.Commit != exp.Commit {
		why = append(why, "candidate commit differs")
	}
	if r.Environment == "" {
		why = append(why, "environment empty")
	}
	if r.InstallSHA256 != exp.InstallSHA {
		why = append(why, "install.sh sha256 differs")
	}
	if r.SumsSHA256 != exp.SumsSHA {
		why = append(why, "SHA256SUMS sha256 differs")
	}
	if r.PrevVersion != exp.PrevVersion {
		why = append(why, "previous version differs")
	}
	if !samePrior(r.Prior, exp.Prior) {
		why = append(why, "previous artifact digests differ")
	}
	if r.UpgradePage != exp.UpgradePage {
		why = append(why, "upgrade page revision differs")
	}
	return why
}

func samePrior(a, b []Prior) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[Prior]int{}
	for _, p := range a {
		m[p]++
	}
	for _, p := range b {
		m[p]--
	}
	for _, n := range m {
		if n != 0 {
			return false
		}
	}
	return true
}

// execWhy says why a receipt's entry e differs from the gate's build want; empty means it matches.
func execWhy(want, e Exec) string {
	if e.SHA256 != want.SHA256 {
		return "sha256 differs"
	}
	return want.Build.diff(e.Build)
}

// entryProblems lists the executable entries of r that do not match the gate's build (or are not a shipped
// executable at all), each as a reason.
func entryProblems(exp Expected, r Receipt) []string {
	var why []string
	seen := map[string]bool{}
	for _, e := range r.Executables {
		want, ok := findExec(exp.Executables, e.Name, Platform{e.GOOS, e.GOARCH})
		if !ok {
			why = append(why, e.label()+": not a shipped executable")
			continue
		}
		if seen[e.label()] {
			why = append(why, e.label()+": listed twice")
		}
		seen[e.label()] = true
		if w := execWhy(want, e); w != "" {
			why = append(why, e.label()+": "+w)
		}
	}
	return why
}

func checkAT011(exp Expected, rs []Receipt) []string {
	var ps []string
	var all []Receipt
	for _, r := range rs {
		if r.Kind == kindAT011 {
			all = append(all, r)
		}
	}
	// A receipt counts only when it matches whole; the rest are missing.
	var good []Receipt
	for _, r := range all {
		if len(receiptLevel11(exp, r)) == 0 && len(entryProblems(exp, r)) == 0 {
			good = append(good, r)
		}
	}
	for _, p := range exp.Platforms {
		for _, n := range executableNames {
			want, _ := findExec(exp.Executables, n, p)
			covered := false
			for _, r := range good {
				if _, ok := findExec(r.Executables, n, p); ok {
					covered = true
					break
				}
			}
			if covered {
				continue
			}
			ps = append(ps, fmt.Sprintf("AT-011 %s: no passing receipt matches (%s)", want.label(), whyExec11(exp, all, want)))
		}
	}
	// The install.sh and SHA256SUMS records ride in every receipt; one whole matching receipt is needed.
	if len(good) == 0 {
		for _, f := range []struct{ n, what string }{{"install.sh", "install.sh sha256 differs"}, {"SHA256SUMS", "SHA256SUMS sha256 differs"}} {
			ok := false
			for _, r := range all {
				if r.Result == resultPassed && !contains(receiptLevel11(exp, r), f.what) {
					ok = true
					break
				}
			}
			if !ok {
				reason := "no receipt"
				if len(all) > 0 {
					reason = "no passing receipt records the candidate's file (sha256 differs)"
				}
				ps = append(ps, fmt.Sprintf("AT-011 %s: %s", f.n, reason))
			}
		}
	}
	return ps
}

// whyExec11 explains, for one executable, the closest receipt's mismatch.
func whyExec11(exp Expected, all []Receipt, want Exec) string {
	p := Platform{want.GOOS, want.GOARCH}
	var best []string
	found := false
	for _, r := range all {
		e, ok := findExec(r.Executables, want.Name, p)
		if !ok {
			continue
		}
		var why []string
		if w := execWhy(want, e); w != "" {
			why = append(why, w)
		}
		why = append(why, receiptLevel11(exp, r)...)
		for _, w := range entryProblems(exp, r) {
			if !strings.HasPrefix(w, want.label()+":") {
				why = append(why, w)
			}
		}
		if !found || len(why) < len(best) {
			best, found = why, true
		}
	}
	if !found {
		return "no receipt covers it"
	}
	return strings.Join(dedupe(best), "; ")
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func dedupe(ss []string) []string {
	var out []string
	for _, s := range ss {
		if !contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// why12 lists why an AT-012 receipt does not match exp on platform p.
func why12(exp Expected, p Platform, r Receipt) []string {
	var why []string
	if r.Result != resultPassed {
		why = append(why, "result is not passed")
	}
	if r.ExitStatus != 0 {
		why = append(why, "exit status is not 0")
	}
	if r.Commit != exp.Commit {
		why = append(why, "candidate commit differs")
	}
	if r.Environment == "" {
		why = append(why, "environment empty")
	}
	if r.OlderVersion != exp.OlderVersion {
		why = append(why, "older version differs")
	}
	if r.NewerVersion != exp.NewerVersion {
		why = append(why, "newer version differs")
	}
	om, _ := findPrior(exp.Prior, "mash", p)
	oc, _ := findPrior(exp.Prior, "mashctl", p)
	nm, _ := findExec(exp.Executables, "mash", p)
	nc, _ := findExec(exp.Executables, "mashctl", p)
	if r.OlderMashSHA256 != om.SHA256 {
		why = append(why, "older mash sha256 differs")
	}
	if r.OlderMashctlSHA != oc.SHA256 {
		why = append(why, "older mashctl sha256 differs")
	}
	if r.NewerMashSHA256 != nm.SHA256 {
		why = append(why, "newer mash sha256 differs from the gate's build")
	}
	if r.NewerMashctlSHA != nc.SHA256 {
		why = append(why, "newer mashctl sha256 differs from the gate's build")
	}
	if r.CorpusSHA256 != exp.CorpusSHA {
		why = append(why, "mail corpus sha256 differs")
	}
	return why
}

func checkAT012(exp Expected, rs []Receipt) []string {
	var ps []string
	for _, p := range exp.Platforms {
		var best []string
		found, ok := false, false
		for _, r := range rs {
			if r.Kind != kindAT012 || r.GOOS != p.GOOS || r.GOARCH != p.GOARCH {
				continue
			}
			why := why12(exp, p, r)
			if len(why) == 0 {
				ok = true
				break
			}
			if !found || len(why) < len(best) {
				best, found = why, true
			}
		}
		if ok {
			continue
		}
		reason := "no receipt for the platform"
		if found {
			reason = strings.Join(best, "; ")
		}
		ps = append(ps, fmt.Sprintf("AT-012 %s: no passing receipt matches (%s)", p, reason))
	}
	return ps
}
