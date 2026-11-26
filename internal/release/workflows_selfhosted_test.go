package release

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ADR 0079: CI and release jobs run on self-hosted runners only, picked by four literal label lists. This guard reads
// every workflow file and fails on a job-level runs-on that is anything else than [self-hosted, rh-linux],
// [self-hosted, rh-macos], [self-hosted, rh-gate] or [self-hosted, rh-linux-eph] (ADR 0086, ADR 0123) (a reusable-workflow caller job has none), on a runs-on with a fallback
// (||), on a matrix-driven runs-on (${{ matrix.<key> }}) unless every value that matrix supplies is one of those lists, on a GitHub-hosted
// label (ubuntu-*, macos-*, windows-*) anywhere outside a comment, on a host name, and on a job-level `uses:` of a reusable
// workflow outside this repository (its jobs would run on runners this repository does not pick). A local
// ./.github/workflows/... caller is fine; step-level `uses:` of actions is not covered.

// ADR 0189 (decision 309) is the one exception: the file named exactly scorecard.yml may run its job `analysis` on
// ubuntu-latest, because the OpenSSF API takes published results only from a GitHub-hosted runner with OIDC. The exception
// holds only while the file's triggers are schedule and push (no pull request, no manual dispatch) and it has no other job.
const (
	scorecardFile   = "scorecard.yml"
	scorecardJob    = "analysis"
	scorecardRunsOn = "ubuntu-latest"
	scorecardIf     = "!github.event.repository.fork"
	scorecardCron   = "23 4 * * 1"
)

// scorecardSteps are the job's only steps, by full uses string, in order (ADR 0189). A change of pin changes this list.
var scorecardSteps = []string{
	"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1",
	"ossf/scorecard-action@23f22cd43608bae7a1f1fa2603d7314dee9538a5",
	"github/codeql-action/upload-sarif@24c54180a607b1449ed407dd24f251e4e9147c8d",
}

// scorecardStepWith is the exact with map of each step, by step index (ADR 0189).
var scorecardStepWith = []map[string]any{
	{"persist-credentials": false},
	{"results_file": "results.sarif", "results_format": "sarif", "publish_results": true, "fail_on_publish_error": true},
	{"sarif_file": "results.sarif"},
}

// scorecardJobPermissions is the exact permission set of the job analysis.
var scorecardJobPermissions = map[string]string{
	"security-events": "write",
	"id-token":        "write",
	"contents":        "read",
	"actions":         "read",
}

// scorecardIfExpr strips an optional ${{ }} wrapper and surrounding space from a job-level if.
func scorecardIfExpr(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "${{") && strings.HasSuffix(s, "}}") {
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, "${{"), "}}"))
	}
	return s
}

// linuxLabels, macosLabels, gateLabels and ephLabels are fmt.Sprint forms of the only allowed runs-on lists. rh-gate is carried only
// by small dedicated runners with no rh-linux label, for the gate jobs (TestGateJobsUseGateRunners). rh-linux-eph (ADR 0123) is carried
// only by the ephemeral one-job runners, for the advisory shadow test job (TestEphJobUsesEphemeralRunners).
const (
	linuxLabels = "[self-hosted rh-linux]"
	macosLabels = "[self-hosted rh-macos]"
	gateLabels  = "[self-hosted rh-gate]"
	ephLabels   = "[self-hosted rh-linux-eph]"
)

// hostedLabel finds a GitHub-hosted runner label (ubuntu-*, macos-*, windows-*) that does not continue a longer word,
// so an asset name such as mash-windows-amd64 is not one.
var hostedLabel = regexp.MustCompile(`(^|[^A-Za-z0-9_-])(ubuntu|macos|windows)-[A-Za-z0-9.]+`)

// hostNames are machine names no workflow may carry.
var hostNames = []string{"bf1", "hermes1"}

// thisRepository is the only owner/repo whose reusable workflows a job may call by name.
const thisRepository = "bogworks/mash"

var matrixRunsOn = regexp.MustCompile(`^\$\{\{\s*matrix\.([A-Za-z0-9_-]+)\s*\}\}$`)

// stripComment drops a whole-line or trailing YAML comment (a # at the start or after a space).
func stripComment(line string) string {
	if strings.HasPrefix(strings.TrimSpace(line), "#") {
		return ""
	}
	if i := strings.Index(line, " #"); i >= 0 {
		return line[:i]
	}
	return line
}

func allowedLabels(v any) bool {
	l, ok := v.([]any)
	if !ok {
		return false
	}
	for _, e := range l {
		if _, isStr := e.(string); !isStr {
			return false
		}
	}
	s := fmt.Sprint(v)
	return s == linuxLabels || s == macosLabels || s == gateLabels || s == ephLabels
}

// scorecardProblems lists why text is not the ADR 0189 Scorecard workflow; nothing means the exception applies to it.
func scorecardProblems(text string) []string {
	var w struct {
		On          any `yaml:"on"`
		Permissions any `yaml:"permissions"`
		Jobs        map[string]struct {
			If          string `yaml:"if"`
			RunsOn      any    `yaml:"runs-on"`
			Permissions any    `yaml:"permissions"`
			Steps       []struct {
				Uses            string         `yaml:"uses"`
				Run             string         `yaml:"run"`
				If              string         `yaml:"if"`
				ContinueOnError any            `yaml:"continue-on-error"`
				With            map[string]any `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(text), &w); err != nil {
		return []string{fmt.Sprintf("not valid YAML: %v", err)}
	}
	var out []string
	triggers, ok := w.On.(map[string]any)
	if !ok {
		out = append(out, "its on: is not a mapping of triggers")
	}
	for k := range triggers {
		if k != "schedule" && k != "push" {
			out = append(out, fmt.Sprintf("trigger %q is not allowed (only schedule and push)", k))
		}
	}
	if ok && (triggers["schedule"] == nil || triggers["push"] == nil) {
		out = append(out, "want both the schedule and push triggers")
	}
	if ok && triggers["schedule"] != nil {
		sc, _ := triggers["schedule"].([]any)
		var cron any
		if len(sc) == 1 {
			e, _ := sc[0].(map[string]any)
			cron = e["cron"]
			if len(e) != 1 {
				cron = nil
			}
		}
		if cron != scorecardCron {
			out = append(out, "the schedule must be exactly one entry with cron '"+scorecardCron+"' (weekly)")
		}
	}
	if ok && triggers["push"] != nil {
		// the push trigger selects only main: branches is exactly [main] and it has no other key
		p, _ := triggers["push"].(map[string]any)
		b, _ := p["branches"].([]any)
		if len(p) != 1 || len(b) != 1 || b[0] != "main" {
			out = append(out, "the push trigger must be exactly branches: [main] and nothing else")
		}
	}
	if len(w.Jobs) != 1 {
		out = append(out, fmt.Sprintf("want exactly the one job %s, have %d jobs", scorecardJob, len(w.Jobs)))
	}
	if j, has := w.Jobs[scorecardJob]; !has {
		out = append(out, fmt.Sprintf("has no job %s", scorecardJob))
	} else if r, _ := j.RunsOn.(string); r != scorecardRunsOn {
		out = append(out, fmt.Sprintf("job %s runs-on %v is not %s", scorecardJob, j.RunsOn, scorecardRunsOn))
	}
	if j := w.Jobs[scorecardJob]; scorecardIfExpr(j.If) != scorecardIf {
		out = append(out, fmt.Sprintf("job %s if %q is not the fork exclusion ${{ %s }}", scorecardJob, j.If, scorecardIf))
	}
	if w.Permissions != "read-all" {
		out = append(out, "the top-level permissions must be exactly read-all")
	}
	j := w.Jobs[scorecardJob]
	if pm, isMap := j.Permissions.(map[string]any); !isMap || len(pm) != len(scorecardJobPermissions) {
		out = append(out, "the job permissions must be exactly security-events: write, id-token: write, contents: read, actions: read")
	} else {
		for k, v := range scorecardJobPermissions {
			if pm[k] != v {
				out = append(out, fmt.Sprintf("the job permissions must be exactly security-events: write, id-token: write, contents: read, actions: read (%s is %v)", k, pm[k]))
			}
		}
	}
	steps := j.Steps
	if len(steps) != len(scorecardSteps) {
		out = append(out, fmt.Sprintf("the job steps must be exactly the %d approved uses in order, have %d steps", len(scorecardSteps), len(steps)))
	} else {
		for i, st := range steps {
			if st.Uses != scorecardSteps[i] || st.Run != "" {
				out = append(out, fmt.Sprintf("step %d must be exactly uses %s and no run", i+1, scorecardSteps[i]))
			}
		}
		for i, st := range steps {
			if st.ContinueOnError != nil {
				out = append(out, fmt.Sprintf("step %d must not set continue-on-error", i+1))
			}
			if want := map[bool]string{true: "always()"}[i == 2]; st.If != want {
				out = append(out, fmt.Sprintf("step %d if %q must be %q (only the upload step runs if: always(); no other step has an if)", i+1, st.If, want))
			}
		}
		for i, want := range scorecardStepWith {
			if !reflect.DeepEqual(steps[i].With, want) {
				out = append(out, fmt.Sprintf("step %d with must be exactly %v, have %v (checkout keeps persist-credentials: false; the scorecard step sets publish_results: true and fail_on_publish_error: true)", i+1, want, steps[i].With))
			}
		}
	}
	return out
}

// selfHostedViolations lists what is wrong with one workflow file's text; nothing means it complies.
func selfHostedViolations(name, text string) []string {
	var out []string
	hostedOK := false
	if name == scorecardFile {
		problems := scorecardProblems(text)
		hostedOK = len(problems) == 0
		for _, p := range problems {
			out = append(out, fmt.Sprintf("%s: breaks the ADR 0189 exception: %s", name, p))
		}
	}
	for n, line := range strings.Split(text, "\n") {
		code := stripComment(line)
		if hostedOK && strings.TrimSpace(code) == "runs-on: "+scorecardRunsOn {
			continue // the one line ADR 0189 allows; the job-level check below confirms it is job analysis
		}
		if m := hostedLabel.FindString(code); m != "" {
			out = append(out, fmt.Sprintf("%s:%d names a GitHub-hosted label %q outside a comment", name, n+1, strings.TrimLeft(m, " \t\"'[,:")))
		}
		for _, h := range hostNames {
			if strings.Contains(strings.ToLower(code), h) {
				out = append(out, fmt.Sprintf("%s:%d names the host %q", name, n+1, h))
			}
		}
	}
	var w struct {
		Jobs map[string]struct {
			Uses     string `yaml:"uses"`
			RunsOn   any    `yaml:"runs-on"`
			Strategy struct {
				Matrix map[string]any `yaml:"matrix"`
			} `yaml:"strategy"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(text), &w); err != nil {
		return append(out, fmt.Sprintf("%s: not valid YAML: %v", name, err))
	}
	for id, j := range w.Jobs {
		if j.Uses != "" {
			if !localReusableWorkflow(j.Uses) {
				out = append(out, fmt.Sprintf("%s job %s: calls the reusable workflow %q outside this repository, whose jobs run on runners we do not pick", name, id, j.Uses))
			}
			if j.RunsOn == nil {
				continue // a reusable-workflow caller job: the called workflow picks its runners
			}
		}
		switch r := j.RunsOn.(type) {
		case []any:
			if !allowedLabels(r) {
				out = append(out, fmt.Sprintf("%s job %s: runs-on %v is not %s or %s", name, id, r, linuxLabels, macosLabels))
			}
		case string:
			if hostedOK && id == scorecardJob && r == scorecardRunsOn {
				continue
			}
			if strings.Contains(r, "||") {
				out = append(out, fmt.Sprintf("%s job %s: runs-on %q has a fallback (||)", name, id, r))
			}
			m := matrixRunsOn.FindStringSubmatch(strings.TrimSpace(r))
			if m == nil {
				out = append(out, fmt.Sprintf("%s job %s: runs-on %q is not a list of the allowed labels or a matrix key", name, id, r))
				break
			}
			out = append(out, matrixValueViolations(name, id, m[1], j.Strategy.Matrix)...)
		default:
			out = append(out, fmt.Sprintf("%s job %s: runs-on is %v, want %s or %s", name, id, j.RunsOn, linuxLabels, macosLabels))
		}
	}
	return out
}

// localReusableWorkflow reports whether a job-level uses: names a workflow of this repository: ./.github/workflows/x.yml, or
// bogworks/mash/.github/workflows/x.yml@ref. Anything else is another owner or repository.
func localReusableWorkflow(uses string) bool {
	if strings.HasPrefix(uses, "./") {
		return true
	}
	path, _, _ := strings.Cut(uses, "@")
	return strings.HasPrefix(path, thisRepository+"/.github/workflows/")
}

// matrixValueViolations checks every value the matrix supplies for key: the axis list itself and each include entry.
func matrixValueViolations(name, id, key string, matrix map[string]any) []string {
	var out []string
	check := func(where string, v any) {
		if !allowedLabels(v) {
			out = append(out, fmt.Sprintf("%s job %s: matrix %s value %v is not %s or %s", name, id, where, v, linuxLabels, macosLabels))
		}
	}
	supplied := false
	if axis, ok := matrix[key]; ok {
		supplied = true
		vals, isList := axis.([]any)
		if !isList {
			check(key, axis)
		}
		for _, v := range vals {
			check(key, v)
		}
	}
	includes, _ := matrix["include"].([]any)
	for i, e := range includes {
		entry, _ := e.(map[string]any)
		v, ok := entry[key]
		if !ok {
			if _, axis := matrix[key]; !axis {
				out = append(out, fmt.Sprintf("%s job %s: matrix include entry %d has no %s, so runs-on is empty for it", name, id, i, key))
			}
			continue
		}
		supplied = true
		check(fmt.Sprintf("include[%d].%s", i, key), v)
	}
	if !supplied {
		out = append(out, fmt.Sprintf("%s job %s: matrix supplies no %s", name, id, key))
	}
	return out
}

func TestWorkflows_onlySelfHostedRunners(t *testing.T) {
	files, err := filepath.Glob("../../.github/workflows/*.yml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows: %v", err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range selfHostedViolations(filepath.Base(f), string(raw)) {
			t.Error(v)
		}
	}
}

func TestSelfHostedGuard_catchesEachViolation(t *testing.T) {
	const ok = `jobs:
  a:
    runs-on: [self-hosted, rh-linux]
    steps:
      - run: echo hi # ubuntu-latest in a comment is fine
  # macos-latest in a comment is fine
  b:
    runs-on: ${{ matrix.runs-on }}
    strategy:
      matrix:
        include:
          - {os: linux, runs-on: [self-hosted, rh-linux]}
          - {os: macos, runs-on: [self-hosted, rh-macos]}
  c:
    uses: ./.github/workflows/w.yml
  d:
    uses: bogworks/mash/.github/workflows/w.yml@main
  e:
    runs-on: [self-hosted, rh-gate]
  f:
    runs-on: [self-hosted, rh-linux-eph]
`
	if v := selfHostedViolations("ok.yml", ok); len(v) != 0 {
		t.Fatalf("a compliant workflow was refused: %v", v)
	}
	const lin = "jobs:\n  a:\n    runs-on: [self-hosted, rh-linux]\n"
	for name, c := range map[string]struct{ text, want string }{
		"a hosted runs-on":                 {"jobs:\n  a:\n    runs-on: ubuntu-latest\n", "ubuntu-latest"},
		"a macos runs-on":                  {"jobs:\n  a:\n    runs-on: macos-14\n", "macos-14"},
		"a windows runs-on":                {"jobs:\n  a:\n    runs-on: windows-2022\n", "windows-2022"},
		"a fallback":                       {"jobs:\n  a:\n    runs-on: ${{ vars.X || 'y' }}\n", "fallback"},
		"a variable":                       {"jobs:\n  a:\n    runs-on: ${{ vars.RUNNER_LINUX }}\n", "not a list"},
		"a bare self-hosted":               {"jobs:\n  a:\n    runs-on: self-hosted\n", "not a list"},
		"a wrong list":                     {"jobs:\n  a:\n    runs-on: [self-hosted, linux]\n", "is not"},
		"an extra label":                   {"jobs:\n  a:\n    runs-on: [self-hosted, rh-linux, gpu]\n", "is not"},
		"the macOS OS label":               {"jobs:\n  a:\n    runs-on: [self-hosted, macOS, rh-macos]\n", "is not"},
		"a reordered list":                 {"jobs:\n  a:\n    runs-on: [rh-linux, self-hosted]\n", "is not"},
		"a gate list with rh-linux":        {"jobs:\n  a:\n    runs-on: [self-hosted, rh-gate, rh-linux]\n", "is not"},
		"a reordered gate list":            {"jobs:\n  a:\n    runs-on: [rh-gate, self-hosted]\n", "is not"},
		"an eph list with rh-linux":        {"jobs:\n  a:\n    runs-on: [self-hosted, rh-linux-eph, rh-linux]\n", "is not"},
		"a reordered eph list":             {"jobs:\n  a:\n    runs-on: [rh-linux-eph, self-hosted]\n", "is not"},
		"a group mapping":                  {"jobs:\n  a:\n    runs-on: {group: g}\n", "want"},
		"no runs-on":                       {"jobs:\n  a:\n    steps: []\n", "want"},
		"a matrix key never set":           {"jobs:\n  a:\n    runs-on: ${{ matrix.r }}\n    strategy:\n      matrix:\n        os: [linux]\n", "supplies no r"},
		"a matrix hosted value":            {"jobs:\n  a:\n    runs-on: ${{ matrix.r }}\n    strategy:\n      matrix:\n        include:\n          - {r: [self-hosted, rh-linux]}\n          - {r: ubuntu-latest}\n", "is not"},
		"a matrix axis wrong value":        {"jobs:\n  a:\n    runs-on: ${{ matrix.r }}\n    strategy:\n      matrix:\n        r: [[self-hosted, rh-linux], [self-hosted, other]]\n", "is not"},
		"a matrix entry missing it":        {"jobs:\n  a:\n    runs-on: ${{ matrix.r }}\n    strategy:\n      matrix:\n        include:\n          - {r: [self-hosted, rh-linux]}\n          - {os: x}\n", "has no r"},
		"a matrix expression":              {"jobs:\n  a:\n    runs-on: ${{ matrix.r || 'x' }}\n", "fallback"},
		"a label outside a comment":        {lin + "    steps:\n      - run: echo ubuntu-22.04\n", "ubuntu-22.04"},
		"a host name":                      {lin + "    steps:\n      - run: ssh bf1\n", "bf1"},
		"an outside reusable":              {"jobs:\n  a:\n    uses: o/r/.github/workflows/w.yml@v1\n", "outside this repository"},
		"an outside reusable by sha":       {"jobs:\n  a:\n    uses: slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml@0123456789012345678901234567890123456789\n    with: {}\n", "outside this repository"},
		"a lookalike repository":           {"jobs:\n  a:\n    uses: bogworks/mash-evil/.github/workflows/w.yml@v1\n", "outside this repository"},
		"an outside reusable with runs-on": {"jobs:\n  a:\n    runs-on: [self-hosted, rh-linux]\n    uses: o/r/.github/workflows/w.yml@v1\n", "outside this repository"},
	} {
		v := selfHostedViolations("bad.yml", c.text)
		if len(v) == 0 || !strings.Contains(strings.Join(v, "\n"), c.want) {
			t.Errorf("%s: the guard reported %v, want a violation mentioning %q", name, v, c.want)
		}
	}
}

func TestSelfHostedGuard_scorecardException(t *testing.T) {
	const good = `name: Scorecard
on:
  schedule:
    - cron: '23 4 * * 1'
  push:
    branches: [main]
permissions: read-all
jobs:
  analysis:
    if: ${{ !github.event.repository.fork }}
    runs-on: ubuntu-latest
    permissions:
      security-events: write
      id-token: write
      contents: read
      actions: read
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - uses: ossf/scorecard-action@23f22cd43608bae7a1f1fa2603d7314dee9538a5 # v2.6.0
        with:
          results_file: results.sarif
          results_format: sarif
          publish_results: true
          fail_on_publish_error: true
      - uses: github/codeql-action/upload-sarif@24c54180a607b1449ed407dd24f251e4e9147c8d # v4.38.3
        if: always()
        with:
          sarif_file: results.sarif
`
	if v := selfHostedViolations("scorecard.yml", good); len(v) != 0 {
		t.Fatalf("the Scorecard workflow was refused: %v", v)
	}
	swap := func(old, repl string) string {
		if !strings.Contains(good, old) {
			t.Fatalf("test text has no %q", old)
		}
		return strings.Replace(good, old, repl, 1)
	}
	for name, c := range map[string]struct{ file, text, want string }{
		"the same text under another file name":   {"other.yml", good, "ubuntu-latest"},
		"a name that only ends in scorecard.yml":  {"my-scorecard.yml", good, "ubuntu-latest"},
		"a pull_request trigger":                  {"scorecard.yml", swap("  push:", "  pull_request:\n  push:"), "pull_request"},
		"a pull_request_target trigger":           {"scorecard.yml", swap("  push:", "  pull_request_target:\n  push:"), "pull_request_target"},
		"a workflow_dispatch trigger":             {"scorecard.yml", swap("  push:", "  workflow_dispatch:\n  push:"), "workflow_dispatch"},
		"a string trigger":                        {"scorecard.yml", swap("on:\n  schedule:\n    - cron: '23 4 * * 1'\n  push:\n    branches: [main]\n", "on: pull_request\n"), "not a mapping"},
		"a job name other than analysis":          {"scorecard.yml", swap("  analysis:", "  scan:"), "analysis"},
		"a second job":                            {"scorecard.yml", good + "  b:\n    runs-on: ubuntu-latest\n", "exactly the one job"},
		"runs-on macos-14":                        {"scorecard.yml", swap("ubuntu-latest", "macos-14"), "macos-14"},
		"runs-on ubuntu-22.04":                    {"scorecard.yml", swap("ubuntu-latest", "ubuntu-22.04"), "ubuntu-22.04"},
		"a fallback":                              {"scorecard.yml", swap("ubuntu-latest", "${{ vars.R || 'ubuntu-latest' }}"), "fallback"},
		"a self-hosted list":                      {"scorecard.yml", swap("ubuntu-latest", "[self-hosted, rh-linux]"), "is not"},
		"a hosted label in a step":                {"scorecard.yml", good + "      - run: echo ubuntu-latest\n", "ubuntu-latest"},
		"a host name":                             {"scorecard.yml", good + "      - run: ssh bf1\n", "bf1"},
		"push with no branches":                   {"scorecard.yml", swap("    branches: [main]\n", ""), "push trigger"},
		"push with branches main and dev":         {"scorecard.yml", swap("[main]", "[main, dev]"), "push trigger"},
		"push with branches-ignore":               {"scorecard.yml", swap("    branches: [main]\n", "    branches-ignore: [dev]\n"), "push trigger"},
		"push with tags":                          {"scorecard.yml", swap("    branches: [main]\n", "    branches: [main]\n    tags: ['v*']\n"), "push trigger"},
		"push with branches dev":                  {"scorecard.yml", swap("[main]", "[dev]"), "push trigger"},
		"a missing if":                            {"scorecard.yml", swap("    if: ${{ !github.event.repository.fork }}\n", ""), "fork exclusion"},
		"an if that is true":                      {"scorecard.yml", swap("${{ !github.event.repository.fork }}", "true"), "fork exclusion"},
		"permissions contents write":              {"scorecard.yml", swap("contents: read", "contents: write"), "permissions"},
		"an extra permission scope":               {"scorecard.yml", swap("      actions: read\n", "      actions: read\n      packages: write\n"), "permissions"},
		"a missing permission scope":              {"scorecard.yml", swap("      actions: read\n", ""), "permissions"},
		"no job permissions":                      {"scorecard.yml", swap("    permissions:\n      security-events: write\n      id-token: write\n      contents: read\n      actions: read\n", ""), "permissions"},
		"no top-level permissions":                {"scorecard.yml", swap("permissions: read-all\n", ""), "permissions"},
		"top-level write-all":                     {"scorecard.yml", swap("permissions: read-all", "permissions: write-all"), "permissions"},
		"an added run step":                       {"scorecard.yml", good + "      - run: make\n", "steps"},
		"an unpinned action":                      {"scorecard.yml", swap("actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1", "actions/checkout@v4"), "step 1"},
		"a different action":                      {"scorecard.yml", swap("ossf/scorecard-action@", "evil/scorecard-action@"), "step 2"},
		"a swapped sha":                           {"scorecard.yml", swap("upload-sarif@24c54180a607b1449ed407dd24f251e4e9147c8d", "upload-sarif@0000000000000000000000000000000000000000"), "step 3"},
		"publish_results false":                   {"scorecard.yml", swap("publish_results: true", "publish_results: false"), "publish_results"},
		"no fail_on_publish_error":                {"scorecard.yml", swap("          fail_on_publish_error: true\n", ""), "fail_on_publish_error"},
		"results_format json":                     {"scorecard.yml", swap("results_format: sarif", "results_format: json"), "step 2 with"},
		"sarif_file other.sarif":                  {"scorecard.yml", swap("sarif_file: results.sarif", "sarif_file: other.sarif"), "step 3 with"},
		"results_file other.sarif":                {"scorecard.yml", swap("results_file: results.sarif", "results_file: other.sarif"), "step 2 with"},
		"an extra with key on checkout":           {"scorecard.yml", swap("          persist-credentials: false\n", "          persist-credentials: false\n          ref: evil\n"), "step 1 with"},
		"an extra with key on scorecard":          {"scorecard.yml", swap("          fail_on_publish_error: true\n", "          fail_on_publish_error: true\n          repo_token: x\n"), "step 2 with"},
		"an extra with key on upload":             {"scorecard.yml", swap("          sarif_file: results.sarif\n", "          sarif_file: results.sarif\n          category: x\n"), "step 3 with"},
		"a removed results_format":                {"scorecard.yml", swap("          results_format: sarif\n", ""), "step 2 with"},
		"checkout persisting credentials":         {"scorecard.yml", swap("persist-credentials: false", "persist-credentials: true"), "persist-credentials"},
		"a missing step":                          {"scorecard.yml", swap("      - uses: github/codeql-action/upload-sarif@24c54180a607b1449ed407dd24f251e4e9147c8d # v4.38.3\n        if: always()\n        with:\n          sarif_file: results.sarif\n", ""), "steps"},
		"if false on the upload step":             {"scorecard.yml", swap("        if: always()\n", "        if: false\n"), "step 3 if"},
		"no if on the upload step":                {"scorecard.yml", swap("        if: always()\n", ""), "step 3 if"},
		"if always on the scorecard step":         {"scorecard.yml", swap("# v2.6.0\n", "# v2.6.0\n        if: always()\n"), "step 2 if"},
		"continue-on-error on the scorecard step": {"scorecard.yml", swap("# v2.6.0\n", "# v2.6.0\n        continue-on-error: true\n"), "step 2 must not set continue-on-error"},
		"continue-on-error on the checkout step":  {"scorecard.yml", swap("# v7.0.1\n", "# v7.0.1\n        continue-on-error: true\n"), "step 1 must not set continue-on-error"},
		"continue-on-error on the upload step":    {"scorecard.yml", swap("        if: always()\n", "        if: always()\n        continue-on-error: true\n"), "step 3 must not set continue-on-error"},
		"a cron every five minutes":               {"scorecard.yml", swap("'23 4 * * 1'", "'*/5 * * * *'"), "cron"},
		"two schedule entries":                    {"scorecard.yml", swap("    - cron: '23 4 * * 1'\n", "    - cron: '23 4 * * 1'\n    - cron: '23 5 * * 1'\n"), "cron"},
		"a different weekly cron":                 {"scorecard.yml", swap("'23 4 * * 1'", "'30 6 * * 2'"), "cron"},
		"an if comparing the repository name":     {"scorecard.yml", swap("${{ !github.event.repository.fork }}", "github.repository == 'bogworks/mash'"), "fork exclusion"},
	} {
		v := selfHostedViolations(c.file, c.text)
		if len(v) == 0 || !strings.Contains(strings.Join(v, "\n"), c.want) {
			t.Errorf("%s: the guard reported %v, want a violation mentioning %q", name, v, c.want)
		}
	}
}

// TestGateJobsUseGateRunners pins which jobs run on the small gate runners: exactly the gate jobs (adr-lint, label-reset and the changes and pr-check jobs), and no other job in
// any workflow, so a heavy test job can never occupy a gate runner.
func TestGateJobsUseGateRunners(t *testing.T) {
	want := map[string]bool{
		"label-reset.yml/label-reset": true,
		"adr-lint.yml/adr-lint":       true,
		"ci.yml/changes":              true,
		"ci.yml/docs-lint":            true,
		"ci.yml/pr-check":             true,
		"lint.yml/changes":            true,
		"security.yml/changes":        true,
	}
	files, err := filepath.Glob("../../.github/workflows/*.yml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows: %v", err)
	}
	got := map[string]bool{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var w struct {
			Jobs map[string]struct {
				RunsOn any `yaml:"runs-on"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(raw, &w); err != nil {
			t.Fatalf("%s: not valid YAML: %v", f, err)
		}
		for id, j := range w.Jobs {
			if l, ok := j.RunsOn.([]any); ok && fmt.Sprint(l) == gateLabels {
				got[filepath.Base(f)+"/"+id] = true
			}
		}
	}
	for k := range want {
		if !got[k] {
			t.Errorf("%s does not run on %s", k, gateLabels)
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("%s runs on %s but is not a gate job", k, gateLabels)
		}
	}
}

// TestEphJobUsesEphemeralRunners pins which jobs run on the ephemeral one-job runners (ADR 0123): exactly the advisory
// shadow job nightly.yml/test-eph, and no other job in any workflow.
func TestEphJobUsesEphemeralRunners(t *testing.T) {
	files, err := filepath.Glob("../../.github/workflows/*.yml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows: %v", err)
	}
	got := map[string]bool{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var w struct {
			Jobs map[string]struct {
				RunsOn any `yaml:"runs-on"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(raw, &w); err != nil {
			t.Fatalf("%s: not valid YAML: %v", f, err)
		}
		for id, j := range w.Jobs {
			if l, ok := j.RunsOn.([]any); ok && fmt.Sprint(l) == ephLabels {
				got[filepath.Base(f)+"/"+id] = true
			}
		}
	}
	if len(got) != 1 || !got["nightly.yml/test-eph"] {
		t.Errorf("jobs on %s = %v, want exactly nightly.yml/test-eph", ephLabels, got)
	}
}
