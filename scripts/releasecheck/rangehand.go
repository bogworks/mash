package main

// The opt-in cross-repo step (`release-check --rangehand <checkout> <X> <version>`): rangehand's tier c, the parts that
// exercise mash itself (RANGE_LIVE_SET=mash), run against the candidate. Rangehand's runner drives it: this file builds
// the candidate for the container, hands it over by environment, and judges the results file it leaves. It never makes
// a rangehand receipt (the compat.json pin is what certifies); it only decides whether the candidate may be pinned.

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
	"sort"
	"strconv"
	"strings"
	"time"
)

// rangehandTimeout bounds the whole run, which starts a container and real Claude sessions and takes 30+ minutes.
var rangehandTimeout = 2 * time.Hour

// rangehandScript is rangehand's tier c entry point, run from the checkout.
const rangehandScript = "test/scenarios/live/run-in-container.sh"

// rangehandRun runs the runner (`sh <script> <RX> <RVersion>`; args are RX and RVersion) in dir with exactly env,
// streaming its output to out. It returns the runner's exit code; an error is a failure to start it or to finish it
// within the deadline.
type rangehandRun func(ctx context.Context, dir string, args, env []string, out io.Writer) (int, error)

// rangehandInputs resolves the checkout (absolute), RX (its HEAD commit) and RVersion (the highest vMAJOR.MINOR.PATCH tag
// reachable from HEAD, without the v). Failing to find any of them is the gate's, so an op error.
func rangehandInputs(checkout string) (dir, rx, rversion string, err error) {
	dir, err = filepath.Abs(checkout)
	if err != nil {
		return "", "", "", op(err)
	}
	if rx, err = git(dir, "rev-parse", "--verify", "HEAD^{commit}"); err != nil {
		return "", "", "", op(fmt.Errorf("resolving the rangehand checkout's HEAD: %w", err))
	}
	tags, err := git(dir, "tag", "--merged", "HEAD", "--list", "v*")
	if err != nil {
		return "", "", "", op(fmt.Errorf("listing the rangehand tags reachable from %s: %w", rx, err))
	}
	var best [3]int
	for _, tag := range strings.Fields(tags) {
		if v, ok := parseSemver(tag); ok && (rversion == "" || semverLess(best, v)) {
			best, rversion = v, strings.TrimPrefix(tag, "v")
		}
	}
	if rversion == "" {
		return "", "", "", op(fmt.Errorf("no rangehand release tag (vMAJOR.MINOR.PATCH) is reachable from %s", rx))
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

// rangehandEnv is the runner's environment: base inherited, with the candidate handed over. An inherited RANGE_LIVE_ONLY
// is dropped (rangehand rejects it together with RANGE_LIVE_SET) and the receipt variables are never set here; a
// candidate run keeps results only.
func rangehandEnv(base []string, bin, plugin, marketplace, results string) []string {
	ours := map[string]string{
		"RANGE_MASH_BIN":              bin,
		"RANGE_MASH_PLUGIN":           plugin,
		"RANGE_LIVE_SET":              "mash",
		"RANGE_SCENARIO_RESULTS":      results,
		"RANGE_LIVE_MASH_MARKETPLACE": marketplace,
	}
	var env []string
	for _, kv := range base {
		if k, _, _ := strings.Cut(kv, "="); ours[k] == "" && k != "RANGE_LIVE_ONLY" { // an inherited value of one of ours must not shadow it
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

// rangehandStep runs rangehand's tier c (mash set) against the candidate x. It needs the candidate built for the host
// by the plugin step (b.cand), so that a build that failed there is not attempted again here.
func rangehandStep(ctx context.Context, o Options, x string, b *binaries) error {
	if b.cand == "" {
		return op(errors.New("not run: the plugin step did not get a candidate binary built"))
	}
	dir, rx, rversion, err := rangehandInputs(o.Rangehand)
	if err != nil {
		return err
	}
	fmt.Fprintf(o.Out, "note  rangehand checkout %s at %s, release v%s\n", dir, rx, rversion)

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
	fmt.Fprintf(o.Out, "note  rangehand runs from a fresh clone %s at commit %s\n", rclone, rcommit)

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
	resDir, err := os.MkdirTemp("", "release-check-rangehand-")
	if err != nil {
		return op(err)
	}
	results := filepath.Join(resDir, "results.jsonl")
	fmt.Fprintf(o.Out, "note  rangehand results: %s (part logs in %s.logs)\n", results, results)

	run := o.rangehand
	if run == nil {
		run = runRangehand
	}
	timeout := o.rangehandTimeout
	if timeout == 0 {
		timeout = rangehandTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if bin, err = filepath.Abs(bin); err != nil { // the exact string the results must be stamped with
		return op(err)
	}
	env := rangehandEnv(os.Environ(), bin, filepath.Join(clone, "plugin"), clone, results)
	fmt.Fprintf(o.Out, "note  running rangehand's tier c (mash set); this takes a while, its output follows\n")
	code, err := run(runCtx, rclone, []string{rx, rversion}, env, o.Out)
	if err != nil {
		return op(fmt.Errorf("%w (results: %s)", err, results))
	}
	return rangehandVerdict(code, results, bin)
}

// rangehandVerdict judges the runner's exit code and results file. 0 is a pass only with evidence: a results file whose
// every part ended in pass (a start line is the part's own beginning, not a result) and whose every line is stamped with
// stamp, the RANGE_MASH_BIN the gate passed. 3 is a refusal only when a failed part can be read from the results: the
// runner also exits 3 for setup failures (missing credentials, a docker build), where no part failed. Any other code, 2
// (usage: docker is missing, an invalid variable) among them, is a failure of the run: nothing was found out about the candidate.
func rangehandVerdict(code int, results, stamp string) error {
	switch code {
	case 0:
		return rangehandPassed(results, stamp)
	case 3:
		failed := rangehandFailed(results)
		if len(failed) == 0 {
			return op(fmt.Errorf("the rangehand runner exited 3 but no failing part could be read from the results: a setup failure, not a refusal (results: %s)", results))
		}
		return fmt.Errorf("rangehand's tier c (mash set) failed in %s; results %s", strings.Join(failed, ", "), results)
	}
	return op(fmt.Errorf("the rangehand runner exited %d: the run failed (results: %s)", code, results))
}

// rangehandLine is the part of a results line the verdict reads.
type rangehandLine struct {
	ID            string `json:"id"`
	Part          string `json:"part"`
	Result        string `json:"result"`
	CandidateMash string `json:"candidate_mash"`
}

func (l rangehandLine) name() string {
	if l.Part == "" {
		return l.ID
	}
	return l.ID + "/" + l.Part
}

// readRangehandResults reads the JSONL results; on a bad line it returns the lines before it and the error.
func readRangehandResults(results string) ([]rangehandLine, error) {
	f, err := os.Open(results)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []rangehandLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var l rangehandLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return lines, fmt.Errorf("results line %q: %w", sc.Text(), err)
		}
		lines = append(lines, l)
	}
	return lines, sc.Err()
}

// rangehandFailed names the parts ("id/part result") with a result line that is neither pass nor start.
func rangehandFailed(results string) []string {
	lines, _ := readRangehandResults(results)
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

// rangehandMashSet mirrors rangehand's mashSet in test/scenarios/runlive/candidate.go (rangehand 053c5ff).
var rangehandMashSet = []string{"TestV3_MailLive", "TestV10_MashCrashLive", "TestV11_TwoHerdsLive", "TestV23_HookDeliveryLive"}

// rangehandPassed is the evidence a 0 exit needs: at least one line, every part has a pass line and no other final
// result, every line stamped with exactly the RANGE_MASH_BIN the gate passed, and every part of rangehandMashSet among them. A 0 with no evidence is not a pass.
func rangehandPassed(results, stamp string) error {
	lines, err := readRangehandResults(results)
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
	for _, p := range rangehandMashSet {
		if !ran[p] {
			problems = append(problems, p+" did not run")
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("rangehand's results do not show a pass (%s): %s", results, strings.Join(problems, "; "))
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
	return 0, fmt.Errorf("starting the rangehand runner: %w", err)
}
