package main

// The opt-in downstream step (`release-check --consumer <checkout> <X> <version>`): the consumer checkout's own check
// of mash, run against the candidate. The consumer drives it: its manifest (.mash-consumer-check, see consumerManifest)
// names its entry point and which environment variables receive the candidate. This file builds the candidate for the
// consumer's container, hands it over by environment, and judges the results file the consumer leaves. It never makes
// a consumer receipt (the consumer's own pin is what certifies); it only decides whether the candidate may be pinned.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// consumerTimeout bounds the whole run, which may start a container and real agent sessions and take 30+ minutes.
var consumerTimeout = 2 * time.Hour

// consumerManifestFile is the manifest in the consumer checkout's root, read from the commit that runs.
const consumerManifestFile = ".mash-consumer-check"

// consumerManifest is the consumer's contract, read from consumerManifestFile: lines `key=value` (blank lines and lines
// starting with # are ignored; a value may be wrapped in one pair of single or double quotes).
//
//	command         the script, relative to the checkout, run as `sh <command> <commit> <version>` from a fresh clone at
//	                the checkout's HEAD commit (<version> is its highest reachable vMAJOR.MINOR.PATCH tag, without the v)
//	bin_env         the variable that receives the absolute path of the binary under test (a linux build)
//	plugin_env      the variable that receives mash's plugin directory, from a fresh clone of the candidate
//	set_env         the variable that receives the value of `set`
//	set             the test set to run: the part of the consumer's tests that exercise mash
//	results_env     the variable that receives the path the consumer writes its JSONL results to
//	marketplace_env optional: the variable that receives the root of that clone of the candidate
//	drop_env        optional: comma-separated variables removed from the inherited environment (those the consumer
//	                rejects together with set_env)
//	parts           comma-separated parts of the set that must each appear in the results
//
// All but marketplace_env and drop_env are required. The results file holds one JSON object per line with id, part,
// result (start, pass or anything else for a failure) and candidate_mash (the bin_env value the part ran against).
type consumerManifest struct {
	Command, BinEnv, PluginEnv, SetEnv, Set, ResultsEnv, MarketplaceEnv string
	DropEnv, Parts                                                      []string
}

var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func unquoteValue(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

func splitList(v string) []string {
	var out []string
	for _, f := range strings.Split(v, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// parseConsumerManifest reads and validates the manifest text.
func parseConsumerManifest(text string) (consumerManifest, error) {
	var m consumerManifest
	seen := map[string]bool{}
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k, v = strings.TrimSpace(k), unquoteValue(v)
		if !ok {
			return m, fmt.Errorf("%s line %d: want key=value", consumerManifestFile, n+1)
		}
		if seen[k] {
			return m, fmt.Errorf("%s line %d: %s given twice", consumerManifestFile, n+1, k)
		}
		seen[k] = true
		switch k {
		case "command":
			m.Command = v
		case "bin_env":
			m.BinEnv = v
		case "plugin_env":
			m.PluginEnv = v
		case "set_env":
			m.SetEnv = v
		case "set":
			m.Set = v
		case "results_env":
			m.ResultsEnv = v
		case "marketplace_env":
			m.MarketplaceEnv = v
		case "drop_env":
			m.DropEnv = splitList(v)
		case "parts":
			m.Parts = splitList(v)
		default:
			return m, fmt.Errorf("%s line %d: unknown key %q", consumerManifestFile, n+1, k)
		}
	}
	for _, r := range []struct{ key, val string }{{"command", m.Command}, {"bin_env", m.BinEnv}, {"plugin_env", m.PluginEnv}, {"set_env", m.SetEnv}, {"set", m.Set}, {"results_env", m.ResultsEnv}} {
		if r.val == "" {
			return m, fmt.Errorf("%s: %s is required", consumerManifestFile, r.key)
		}
	}
	if len(m.Parts) == 0 {
		return m, fmt.Errorf("%s: parts is required", consumerManifestFile)
	}
	if c := filepath.Clean(m.Command); filepath.IsAbs(m.Command) || c == ".." || strings.HasPrefix(c, ".."+string(filepath.Separator)) {
		return m, fmt.Errorf("%s: command %q must be a path inside the checkout", consumerManifestFile, m.Command)
	}
	names := append([]string{m.BinEnv, m.PluginEnv, m.SetEnv, m.ResultsEnv, m.MarketplaceEnv}, m.DropEnv...)
	for i, n := range names {
		if n == "" && i == 4 {
			continue // marketplace_env is optional
		}
		if !envNameRE.MatchString(n) {
			return m, fmt.Errorf("%s: %q is not an environment variable name", consumerManifestFile, n)
		}
	}
	targets := map[string]bool{}
	for _, n := range []string{m.BinEnv, m.PluginEnv, m.SetEnv, m.ResultsEnv, m.MarketplaceEnv} {
		if n == "" {
			continue
		}
		if targets[n] {
			return m, fmt.Errorf("%s: %s is named for two values", consumerManifestFile, n)
		}
		targets[n] = true
	}
	return m, nil
}

// consumerRun runs the script (`sh <script> <commit> <version>`; args are those two) in dir with exactly env,
// streaming its output to out. It returns the script's exit code; an error is a failure to start it or to finish it
// within the deadline.
type consumerRun func(ctx context.Context, dir, script string, args, env []string, out io.Writer) (int, error)

// consumerInputs resolves the checkout (absolute), RX (its HEAD commit) and RVersion (the highest vMAJOR.MINOR.PATCH tag
// reachable from HEAD, without the v). Failing to find any of them is the gate's, so an op error.
func consumerInputs(checkout string) (dir, rx, rversion string, err error) {
	dir, err = filepath.Abs(checkout)
	if err != nil {
		return "", "", "", op(err)
	}
	if rx, err = git(dir, "rev-parse", "--verify", "HEAD^{commit}"); err != nil {
		return "", "", "", op(fmt.Errorf("resolving the consumer checkout's HEAD: %w", err))
	}
	tags, err := git(dir, "tag", "--merged", "HEAD", "--list", "v*")
	if err != nil {
		return "", "", "", op(fmt.Errorf("listing the consumer tags reachable from %s: %w", rx, err))
	}
	var best [3]int
	for _, tag := range strings.Fields(tags) {
		if v, ok := parseSemver(tag); ok && (rversion == "" || semverLess(best, v)) {
			best, rversion = v, strings.TrimPrefix(tag, "v")
		}
	}
	if rversion == "" {
		return "", "", "", op(fmt.Errorf("no consumer release tag (vMAJOR.MINOR.PATCH) is reachable from %s", rx))
	}
	return dir, rx, rversion, nil
}

var semverTag = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)$`)

// parseSemver reads a release tag vMAJOR.MINOR.PATCH; anything else (a pre-release, a stray tag) is not one.
func parseSemver(tag string) ([3]int, bool) {
	m := semverTag.FindStringSubmatch(tag)
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return [3]int{}, false
		}
		v[i] = n
	}
	return v, true
}

// semverLess is the numeric order of two versions (1.9.0 is below 1.10.0).
func semverLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// consumerEnv is the script's environment: base inherited, with the candidate handed over under the variable names the
// manifest gives. Variables the manifest drops are removed, and no receipt variable is ever set here; a candidate run
// keeps results only.
func consumerEnv(base []string, m consumerManifest, bin, plugin, marketplace, results string) []string {
	ours := map[string]string{
		m.BinEnv:     bin,
		m.PluginEnv:  plugin,
		m.SetEnv:     m.Set,
		m.ResultsEnv: results,
	}
	if m.MarketplaceEnv != "" {
		ours[m.MarketplaceEnv] = marketplace
	}
	var env []string
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if _, mine := ours[k]; !mine && !slices.Contains(m.DropEnv, k) { // an inherited value of one of ours must not shadow it
			env = append(env, kv)
		}
	}
	keys := make([]string, 0, len(ours))
	for k := range ours {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+ours[k])
	}
	return env
}

// consumerStep runs the consumer's check against the candidate x. It needs the candidate built for the host
// by the plugin step (b.cand), so that a build that failed there is not attempted again here.
func consumerStep(ctx context.Context, o Options, x string, b *binaries) error {
	if b.cand == "" {
		return op(errors.New("not run: the plugin step did not get a candidate binary built"))
	}
	dir, rx, rversion, err := consumerInputs(o.Consumer)
	if err != nil {
		return err
	}
	fmt.Fprintf(o.Out, "note  consumer checkout %s at %s, release v%s\n", dir, rx, rversion)

	// The runner builds from the tree it runs in, so it runs from a fresh clone at RX: uncommitted changes in the
	// checkout cannot alter the checks. RVersion stays the checkout's label (tags need not come through a clone).
	rclone, rwork, err := freshClone(dir, rx)
	if err != nil {
		return err
	}
	defer os.RemoveAll(rwork)
	rcommit, err := git(rclone, "rev-parse", "HEAD")
	if err != nil {
		return op(err)
	}
	fmt.Fprintf(o.Out, "note  consumer runs from a fresh clone %s at commit %s\n", rclone, rcommit)
	mtext, err := os.ReadFile(filepath.Join(rclone, consumerManifestFile))
	if errors.Is(err, os.ErrNotExist) {
		return op(fmt.Errorf("the consumer checkout has no %s at %s: it is a committed file there that names the consumer's entry point and environment (see docs/release.md); nothing was run", consumerManifestFile, rx))
	} else if err != nil {
		return op(err)
	}
	m, err := parseConsumerManifest(string(mtext))
	if err != nil {
		return op(err)
	}
	if st, err := os.Stat(filepath.Join(rclone, m.Command)); err != nil || !st.Mode().IsRegular() {
		return op(fmt.Errorf("%s names the command %q, which is not a file in the consumer checkout at %s", consumerManifestFile, m.Command, rx))
	}

	// The candidate's plugin and marketplace come from a fresh clone of X: never the checkout running the gate.
	clone, work, err := freshClone(o.Repo, x)
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	// The container is linux, whatever the host is: the candidate for linux on the host's architecture. A linux host's
	// own build of the candidate is that binary.
	hostOS := o.hostGOOS
	if hostOS == "" {
		hostOS = runtime.GOOS
	}
	bin := b.cand
	if hostOS != "linux" {
		bin = filepath.Join(work, "linux-mash")
		if _, err := buildFresh(ctx, o, x, "linux", runtime.GOARCH, bin); err != nil {
			return err
		}
	}

	// Kept after the run, whatever its outcome: the evidence is the only thing a candidate run leaves.
	resDir, err := os.MkdirTemp("", "release-check-consumer-")
	if err != nil {
		return op(err)
	}
	results := filepath.Join(resDir, "results.jsonl")
	fmt.Fprintf(o.Out, "note  consumer results: %s (part logs in %s.logs)\n", results, results)

	run := o.consumer
	if run == nil {
		run = runConsumer
	}
	timeout := o.consumerTimeout
	if timeout == 0 {
		timeout = consumerTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if bin, err = filepath.Abs(bin); err != nil { // the exact string the results must be stamped with
		return op(err)
	}
	env := consumerEnv(os.Environ(), m, bin, filepath.Join(clone, "plugin"), clone, results)
	fmt.Fprintf(o.Out, "note  running the consumer's check (set %s); this takes a while, its output follows\n", m.Set)
	code, err := run(runCtx, rclone, m.Command, []string{rx, rversion}, env, o.Out)
	if err != nil {
		return op(fmt.Errorf("%w (results: %s)", err, results))
	}
	return consumerVerdict(code, results, bin, m.Parts)
}

// consumerVerdict judges the runner's exit code and results file. 0 is a pass only with evidence: a results file whose
// every part ended in pass (a start line is the part's own beginning, not a result) and whose every line is stamped with
// stamp, the bin_env value the gate passed. 3 is a refusal only when a failed part can be read from the results: the
// runner also exits 3 for setup failures (missing credentials, a docker build), where no part failed. Any other code, 2
// (usage: docker is missing, an invalid variable) among them, is a failure of the run: nothing was found out about the candidate.
func consumerVerdict(code int, results, stamp string, parts []string) error {
	switch code {
	case 0:
		return consumerPassed(results, stamp, parts)
	case 3:
		failed := consumerFailed(results)
		if len(failed) == 0 {
			return op(fmt.Errorf("the consumer script exited 3 but no failing part could be read from the results: a setup failure, not a refusal (results: %s)", results))
		}
		return fmt.Errorf("the consumer's check failed in %s; results %s", strings.Join(failed, ", "), results)
	}
	return op(fmt.Errorf("the consumer script exited %d: the run failed (results: %s)", code, results))
}

// consumerLine is the part of a results line the verdict reads.
type consumerLine struct {
	ID            string `json:"id"`
	Part          string `json:"part"`
	Result        string `json:"result"`
	CandidateMash string `json:"candidate_mash"`
}

func (l consumerLine) name() string {
	if l.Part == "" {
		return l.ID
	}
	return l.ID + "/" + l.Part
}

// readConsumerResults reads the JSONL results; on a bad line it returns the lines before it and the error.
func readConsumerResults(results string) ([]consumerLine, error) {
	f, err := os.Open(results)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []consumerLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var l consumerLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return lines, fmt.Errorf("results line %q: %w", sc.Text(), err)
		}
		lines = append(lines, l)
	}
	return lines, sc.Err()
}

// consumerFailed names the parts ("id/part result") with a result line that is neither pass nor start.
func consumerFailed(results string) []string {
	lines, _ := readConsumerResults(results)
	var failed []string
	seen := map[string]bool{}
	for _, l := range lines {
		if l.Result != "pass" && l.Result != "start" && !seen[l.name()] {
			seen[l.name()] = true
			failed = append(failed, l.name()+" "+l.Result)
		}
	}
	return failed
}

// consumerPassed is the evidence a 0 exit needs: at least one line, every part has a pass line and no other final
// result, every line stamped with exactly the bin_env value the gate passed, and every required part (the manifest's parts) among them. A 0 with no evidence is not a pass.
func consumerPassed(results, stamp string, required []string) error {
	lines, err := readConsumerResults(results)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("the runner exited 0 but wrote no results file (%s): a 0 with no evidence is not a pass", results)
	case err != nil:
		return fmt.Errorf("the runner exited 0 but its results are unreadable: %v (%s)", err, results)
	case len(lines) == 0:
		return fmt.Errorf("the runner exited 0 but the results file is empty (%s): a 0 with no evidence is not a pass", results)
	}
	var problems []string
	passed, parts, ran := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, l := range lines {
		parts[l.name()], ran[l.Part] = true, true
		switch l.Result {
		case "pass":
			passed[l.name()] = true
		case "start":
		default:
			problems = append(problems, fmt.Sprintf("%s ended %q", l.name(), l.Result))
		}
		switch {
		case l.CandidateMash == "":
			problems = append(problems, fmt.Sprintf("%s has no candidate_mash: it did not run against the candidate", l.name()))
		case l.CandidateMash != stamp:
			problems = append(problems, fmt.Sprintf("%s ran against %q, not the candidate %q", l.name(), l.CandidateMash, stamp))
		}
	}
	for p := range parts {
		if !passed[p] {
			problems = append(problems, p+" never passed")
		}
	}
	for _, p := range required {
		if !ran[p] {
			problems = append(problems, p+" did not run")
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("consumer's results do not show a pass (%s): %s", results, strings.Join(problems, "; "))
	}
	return nil
}

// exitCode0 turns the result of waiting for the runner into its exit code, or an error when it did not exit on its own.
func exitCode0(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return 0, fmt.Errorf("starting the consumer script: %w", err)
}
