// Command reviewgate is the verdict engine of the in-session review gate (ADR 0080). The gate runs
// `ocr review --format json` once per configured model; this tool merges those runs, picks the gate from the tier
// policy for the files the diff touches, prints the findings the implementer must fix, and decides the verdict.
//
//	reviewgate verdict -policy FILE -changed FILE -round N [-ocr FILE]... [-title STR]
//	reviewgate record  -store DIR -slug OWNER/NAME -patchid ID -verdict pass|blocked|cap -tier NAME -round N -models "a,b" [-time RFC3339] [-covered N -total M [-covered-files FILE]] [-evidence FILE]...
//	reviewgate rounds  -file FILE -patchid ID [-bump]
//	reviewgate verify  -store DIR -slug OWNER/NAME -patchid ID -base SHA -head SHA -source-sha HEX -policy FILE -changed FILE
//
// verdict exits 0 for pass, 1 for blocked, 2 for a usage or input error (including too few complete runs: the gate
// fails closed), and 3 when the round cap is reached with blocking findings left. A blocker finding is never deferred,
// so a blocker at the cap exits 1, not 3.
//
// The policy's "models" list names the provider and model of each OCR run: the variance reviewer(s) chosen by the
// research bake-off (GLM 5.3 Flash found 4 of 17 known issues, codex plus Flash 6 of 17, about $0.05 a PR). An empty
// list would mean the gate runs OCR once, with whatever provider and model ocr is configured with. "fallback_models"
// is the watcher's replacement reviewer when codex is out (GLM 5.3); scripts/gate --review does not use it. The policy
// file is JSON and cannot carry comments, so that explanation lives here. loadPolicy validates both lists (a non-empty
// provider and model each); the gate script uses "models" to decide how many runs to make.
//
// The policy's "questions" are standing review questions (id, severity, text) that the gate scripts append to every
// review prompts and to OCR's background text (not an OCR rule file); loadPolicy rejects an empty id or text, a repeated id and an unknown severity.
//
// record writes the file the server-side watcher reads to verify the same patch was reviewed. -covered and -total say
// how many of the diff's files OCR reviewed (it leaves tests and Markdown out) and -covered-files names them (a newline
// list of repo-relative paths, one covered-file line each in the record); each -evidence file (one model run's OCR
// JSON) is copied next to the record, written before the record itself, and the record names it with its sha256 and the
// manifest's exact_range and source_artifact_sha256. verify is the server side of that record: exit 0 prints one status
// line, exit 1 prints "not verified: <reason>" (the caller then reviews the diff itself). It trusts nothing in the record
// but the names of the evidence files: it checks their sha256 and recomputes the verdict from them with the same code as
// verdict, under the given policy and changed files, round 1 (a round cap is a failure). The security-critical tier is
// never waived. A patch-id alone never verifies: the caller names the checkout it is verifying (-base and -head, the
// commits of the merge-base..head range the gate reviewed, and -source-sha, the manifest's source_artifact_sha256 for
// that checkout) and every recorded run must carry exactly that range and that source hash, so a rebase (same patch,
// new base and head) or a changed source is not verified. The patch-id stays the record's key and the round counter's.
// rounds keeps the
// per-branch round counter: one line per distinct patch-id that was reviewed with blocking findings.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---- policy ----

type tier struct {
	Name     string   `json:"name"`
	Paths    []string `json:"paths"`
	Block    []string `json:"block"`
	RoundCap int      `json:"round_cap"`

	rules []globRule
	block []int // the normalised severity ranks this tier blocks
}

type modelRef struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type policy struct {
	MinComplete int        `json:"min_complete"`
	DefaultTier string     `json:"default_tier"`
	Tiers       []*tier    `json:"tiers"`
	Models      []modelRef `json:"models"`
	// FallbackModels replace codex in the watcher when codex is out; the in-session gate never runs them.
	FallbackModels []modelRef `json:"fallback_models"`
	Questions      []question `json:"questions"`
	// ReceiptsWaiveReview lets a verified receipt stand in for the server-side review. It defaults to false, and
	// loadPolicy rejects true unless Attestation names a verifier: receipts live in a store the author can write, so
	// they are not evidence on their own (ADR 0080, known weaknesses).
	ReceiptsWaiveReview bool         `json:"receipts_waive_review"`
	Attestation         *attestation `json:"attestation"`
}

// attestation is a placeholder for the future trusted mechanism that makes a receipt independently verifiable. Nothing
// implements it yet; naming a verifier is the only thing loadPolicy checks.
type attestation struct {
	Verifier string `json:"verifier"`
}

// question is a standing review question: every review prompt asks it, and an answer of yes is a finding of
// the given severity. The gate scripts read it with jq; loadPolicy only validates it.
type question struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Text     string `json:"text"`
}

// Severity ranks. OCR's severity is a lowercase string; "critical" is a blocker, and anything unknown (info
// included) is low, so an odd value can never raise the gate on its own.
const (
	sevLow = iota + 1
	sevMedium
	sevHigh
	sevBlocker
)

var sevName = map[int]string{sevLow: "low", sevMedium: "medium", sevHigh: "high", sevBlocker: "blocker"}
var sevPrefix = map[int]string{sevLow: "L", sevMedium: "M", sevHigh: "H", sevBlocker: "B"}

func severityRank(s string) int {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "blocker", "critical":
		return sevBlocker
	case "high":
		return sevHigh
	case "medium":
		return sevMedium
	}
	return sevLow
}

func loadPolicy(path string) (*policy, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p policy
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("policy %s: %w", path, err)
	}
	if p.ReceiptsWaiveReview && (p.Attestation == nil || strings.TrimSpace(p.Attestation.Verifier) == "") {
		return nil, fmt.Errorf("policy %s: receipts_waive_review needs an attestation object with a non-empty verifier (no trusted attestation exists yet)", path)
	}
	if p.MinComplete < 1 {
		p.MinComplete = 1
	}
	seen := map[string]bool{}
	for _, t := range p.Tiers {
		if t.Name == "" || seen[t.Name] {
			return nil, fmt.Errorf("policy %s: tier name %q is empty or repeated", path, t.Name)
		}
		seen[t.Name] = true
		if t.RoundCap < 1 {
			return nil, fmt.Errorf("policy %s: tier %s needs a round_cap of at least 1", path, t.Name)
		}
		for _, s := range t.Block {
			switch strings.ToLower(s) {
			case "blocker", "critical", "high", "medium", "low":
			default:
				return nil, fmt.Errorf("policy %s: tier %s blocks unknown severity %q", path, t.Name, s)
			}
			if r := severityRank(s); !slices.Contains(t.block, r) {
				t.block = append(t.block, r)
			}
		}
		var err error
		if t.rules, err = parseGlobs(t.Paths); err != nil {
			return nil, fmt.Errorf("policy %s: tier %s: %w", path, t.Name, err)
		}
	}
	qseen := map[string]bool{}
	for _, q := range p.Questions {
		if strings.TrimSpace(q.ID) == "" || strings.TrimSpace(q.Text) == "" || qseen[q.ID] {
			return nil, fmt.Errorf("policy %s: question %q needs a unique id and non-empty text", path, q.ID)
		}
		qseen[q.ID] = true
		switch strings.ToLower(q.Severity) {
		case "blocker", "high", "medium", "low":
		default:
			return nil, fmt.Errorf("policy %s: question %s has unknown severity %q", path, q.ID, q.Severity)
		}
	}
	for _, l := range []struct {
		name string
		refs []modelRef
	}{{"models", p.Models}, {"fallback_models", p.FallbackModels}} {
		for i, m := range l.refs {
			if strings.TrimSpace(m.Provider) == "" || strings.TrimSpace(m.Model) == "" {
				return nil, fmt.Errorf("policy %s: %s[%d] needs a non-empty provider and model", path, l.name, i)
			}
		}
	}
	if !seen[p.DefaultTier] {
		return nil, fmt.Errorf("policy %s: default_tier %q is not a tier", path, p.DefaultTier)
	}
	return &p, nil
}

// blocks reports whether the tier blocks a severity. A blocker always blocks, whatever the policy lists.
func (t *tier) blocks(rank int) bool { return rank == sevBlocker || slices.Contains(t.block, rank) }

func (t *tier) blockList() string {
	var out []string
	for r := sevBlocker; r >= sevLow; r-- {
		if t.blocks(r) {
			out = append(out, sevName[r])
		}
	}
	return strings.Join(out, ", ")
}

func (t *tier) strictness() int {
	n := 0
	for r := sevLow; r <= sevBlocker; r++ {
		if t.blocks(r) {
			n++
		}
	}
	return n
}

// ---- globs ----
//
// The same small matcher as scripts/decisioncheck (both are package main, so it is copied, not imported): "**"
// crosses directories, "*" stays inside a segment, "?" is one non-slash character, a trailing "/" means "/**", and a
// leading "!" excludes a file matched by an earlier glob of the same list.

type globRule struct {
	re  *regexp.Regexp
	neg bool
}

func globToRegexp(g string) (*regexp.Regexp, error) {
	if strings.HasSuffix(g, "/") {
		g += "**"
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(g); i++ {
		switch c := g[i]; {
		case c == '*' && strings.HasPrefix(g[i:], "**/"):
			b.WriteString(`(?:.*/)?`)
			i += 2
		case c == '*' && strings.HasPrefix(g[i:], "**"):
			b.WriteString(`.*`)
			i++
		case c == '*':
			b.WriteString(`[^/]*`)
		case c == '?':
			b.WriteString(`[^/]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

func parseGlobs(globs []string) ([]globRule, error) {
	var rules []globRule
	for _, g := range globs {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		neg := strings.HasPrefix(g, "!")
		re, err := globToRegexp(strings.TrimPrefix(g, "!"))
		if err != nil {
			return nil, fmt.Errorf("glob %q: %w", g, err)
		}
		rules = append(rules, globRule{re, neg})
	}
	return rules, nil
}

// matches reports whether p matches: a positive rule sets it, a later "!" rule clears it.
func matches(rules []globRule, p string) bool {
	m := false
	for _, r := range rules {
		if r.re.MatchString(p) {
			m = !r.neg
		}
	}
	return m
}

// ---- tier choice ----

type tierPick struct {
	t       *tier
	file    string // one file that put the tier in play
	dflt    bool   // the file matched no tier, so it got the default
	nofiles bool
}

// chooseTier gives each changed file every tier whose globs match it, else the default tier. The diff's gate is the
// strictest tier any file got: the one blocking the most severities, the first in policy order on a tie. The second
// result lists the other tiers that were in play, each with one file, for the header.
func chooseTier(p *policy, changed []string) (tierPick, []tierPick) {
	var def *tier
	for _, t := range p.Tiers {
		if t.Name == p.DefaultTier {
			def = t
		}
	}
	first := map[string]tierPick{}
	for _, f := range changed {
		hit := false
		for _, t := range p.Tiers {
			if matches(t.rules, f) {
				hit = true
				if _, ok := first[t.Name]; !ok {
					first[t.Name] = tierPick{t: t, file: f}
				}
			}
		}
		if !hit {
			if _, ok := first[def.Name]; !ok {
				first[def.Name] = tierPick{t: def, file: f, dflt: true}
			}
		}
	}
	var picks []tierPick
	for _, t := range p.Tiers {
		if pk, ok := first[t.Name]; ok {
			picks = append(picks, pk)
		}
	}
	if len(picks) == 0 {
		return tierPick{t: def, nofiles: true}, nil
	}
	best := 0
	for i, pk := range picks {
		if pk.t.strictness() > picks[best].t.strictness() {
			best = i
		}
	}
	chosen := picks[best]
	return chosen, slices.Delete(picks, best, best+1)
}

// ---- OCR runs and merging ----

type comment struct {
	Path           string `json:"path"`
	Content        string `json:"content"`
	SuggestionCode string `json:"suggestion_code"`
	StartLine      int    `json:"start_line"`
	EndLine        int    `json:"end_line"`
	Category       string `json:"category"`
	Severity       string `json:"severity"`
}

type ocrOut struct {
	Status   string    `json:"status"`
	LLM      modelRef  `json:"llm"`
	Comments []comment `json:"comments"`
}

type ocrRun struct {
	label    string
	ok       bool
	reason   string
	comments []comment
}

func readRun(path string) ocrRun {
	r := ocrRun{label: filepath.Base(path)}
	b, err := os.ReadFile(path)
	if err != nil {
		r.reason = "unreadable: " + err.Error()
		return r
	}
	var o ocrOut
	if err := json.Unmarshal(b, &o); err != nil {
		r.reason = "unreadable JSON: " + err.Error()
		return r
	}
	if o.LLM.Model != "" {
		r.label = o.LLM.Model
		if o.LLM.Provider != "" {
			r.label = o.LLM.Provider + "/" + o.LLM.Model
		}
	}
	if o.Status != "complete" {
		r.reason = fmt.Sprintf("status %q, not complete", o.Status)
		return r
	}
	r.ok, r.comments = true, o.Comments
	return r
}

type finding struct {
	id         string
	path       string
	start, end int
	category   string
	rank       int
	content    string
	suggestion string
	models     []string // labels of the runs that raised it, in run order
}

func (f *finding) overlaps(c comment, start, end int) bool {
	return f.path == c.Path && f.category == c.Category && start <= f.end+3 && f.start <= end+3
}

// merge folds the comments of every complete run into findings. Two comments are one finding when the path and
// category are equal and their line ranges overlap or start within 3 lines of each other; the finding takes the
// highest severity and the text (and fix) of the highest-severity source, the earlier source on a tie.
func merge(runs []ocrRun) []*finding {
	var out []*finding
	for _, r := range runs {
		if !r.ok {
			continue
		}
		for _, c := range r.comments {
			start, end := c.StartLine, c.EndLine
			if end < start {
				end = start
			}
			rank := severityRank(c.Severity)
			var f *finding
			for _, g := range out {
				if g.overlaps(c, start, end) {
					f = g
					break
				}
			}
			if f == nil {
				out = append(out, &finding{path: c.Path, start: start, end: end, category: c.Category, rank: rank,
					content: c.Content, suggestion: c.SuggestionCode, models: []string{r.label}})
				continue
			}
			f.start, f.end = min(f.start, start), max(f.end, end)
			if !slices.Contains(f.models, r.label) {
				f.models = append(f.models, r.label)
			}
			if rank > f.rank {
				f.rank, f.content, f.suggestion = rank, c.Content, c.SuggestionCode
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.rank != b.rank {
			return a.rank > b.rank
		}
		if a.path != b.path {
			return a.path < b.path
		}
		return a.start < b.start
	})
	count := map[int]int{}
	for _, f := range out {
		count[f.rank]++
		f.id = sevPrefix[f.rank] + strconv.Itoa(count[f.rank])
	}
	return out
}

func (f *finding) where() string {
	if f.end > f.start {
		return fmt.Sprintf("%s:%d-%d", f.path, f.start, f.end)
	}
	return fmt.Sprintf("%s:%d", f.path, f.start)
}

func printFinding(w io.Writer, f *finding, n int) {
	fmt.Fprintf(w, "%s %s %s [%s] (%d of %d models: %s)\n", f.id, sevName[f.rank], f.where(), f.category, len(f.models), n, strings.Join(f.models, ", "))
	for _, l := range strings.Split(strings.TrimSpace(f.content), "\n") {
		fmt.Fprintf(w, "    %s\n", l)
	}
	if s := strings.TrimSpace(f.suggestion); s != "" {
		lines := strings.Split(s, "\n")
		if len(lines) > 6 {
			lines = lines[:6]
		}
		fmt.Fprintln(w, "    fix:")
		for _, l := range lines {
			fmt.Fprintf(w, "      %s\n", l)
		}
	}
}

// ---- verdict ----

func readLines(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out, nil
}

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

func verdict(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("reviewgate verdict", flag.ContinueOnError)
	fs.SetOutput(stderr)
	policyFile := fs.String("policy", "", "the tier policy (JSON)")
	changedFile := fs.String("changed", "", "the files the diff changes, one repo-relative path per line")
	round := fs.Int("round", 0, "the round about to be judged, 1-based")
	title := fs.String("title", "", "echoed in the header")
	var ocrs listFlag
	fs.Var(&ocrs, "ocr", "one model's `ocr review --format json` output (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *policyFile == "" || *changedFile == "" || *round < 1 {
		fmt.Fprintln(stderr, "usage: reviewgate verdict -policy FILE -changed FILE -round N [-ocr FILE]... [-title STR]")
		return 2
	}
	p, err := loadPolicy(*policyFile)
	if err != nil {
		fmt.Fprintln(stderr, "reviewgate:", err)
		return 2
	}
	changed, err := readLines(*changedFile)
	if err != nil {
		fmt.Fprintln(stderr, "reviewgate:", err)
		return 2
	}
	code, _ := judge(p, changed, ocrs, *round, *title, stdout, stderr)
	return code
}

// judge is the verdict: it reads each OCR run, prints the report and returns the exit code (see the header) and the
// tier the diff's files put it under.
func judge(p *policy, changed []string, ocrs []string, round int, title string, stdout, stderr io.Writer) (int, *tier) {
	var runs []ocrRun
	var failed []string
	complete := 0
	for _, f := range ocrs {
		r := readRun(f)
		runs = append(runs, r)
		if r.ok {
			complete++
		} else {
			failed = append(failed, r.label+": "+r.reason)
		}
	}
	if title != "" {
		fmt.Fprintf(stdout, "review: %s\n", title)
	}
	chosen, others := chooseTier(p, changed)
	because := chosen.file + " matches"
	switch {
	case chosen.nofiles:
		because = "no files changed, so the default"
	case chosen.dflt:
		because = chosen.file + " matches no tier, so the default"
	}
	fmt.Fprintf(stdout, "tier: %s (blocks %s, round cap %d), because %s\n", chosen.t.Name, chosen.t.blockList(), chosen.t.RoundCap, because)
	for _, o := range others {
		fmt.Fprintf(stdout, "  also in play, less strict: %s (%s)\n", o.t.Name, o.file)
	}
	for _, r := range runs {
		if r.ok {
			fmt.Fprintf(stdout, "models: %s ok\n", r.label)
		} else {
			fmt.Fprintf(stdout, "models: %s FAILED: %s\n", r.label, r.reason)
		}
	}
	if complete < p.MinComplete {
		fmt.Fprintf(stderr, "reviewgate: %d complete run(s), need %d; failing closed (failed: %s)\n", complete, p.MinComplete, strings.Join(failed, "; "))
		return 2, chosen.t
	}
	var blocking, rest []*finding
	blockers := 0
	for _, f := range merge(runs) {
		if chosen.t.blocks(f.rank) {
			blocking = append(blocking, f)
			if f.rank == sevBlocker {
				blockers++
			}
		} else {
			rest = append(rest, f)
		}
	}
	for _, f := range blocking {
		printFinding(stdout, f, complete)
	}
	if len(rest) > 0 {
		var parts []string
		for _, f := range rest {
			parts = append(parts, f.id+" "+f.where())
		}
		fmt.Fprintf(stdout, "also (not blocking): %s\n", strings.Join(parts, ", "))
	}
	roundCap := chosen.t.RoundCap
	switch {
	case len(blocking) == 0:
		fmt.Fprintln(stdout, "PASS")
		return 0, chosen.t
	case round >= roundCap && blockers == 0:
		fmt.Fprintf(stdout, "ROUND CAP: round %d reached the cap %d with %d blocking finding(s) left: fix only correctness on the supported path, answer the rest as deferred (a follow-up issue each, a blocker is never deferred: send it to the maintainer)\n", round, roundCap, len(blocking))
		return 3, chosen.t
	}
	fmt.Fprintf(stdout, "BLOCKED: %d blocking finding(s); fix them and rerun (round %d of %d)\n", len(blocking), round, roundCap)
	if round >= roundCap {
		fmt.Fprintf(stdout, "%d blocker(s) at the round cap: a blocker is never deferred; send it to the maintainer\n", blockers)
	}
	return 1, chosen.t
}

// ---- record ----

// writeAtomic writes through a temp file in the same directory and renames it, so a reader sees all or nothing.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), 0o644)
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
	}
	return werr
}

func safeID(id string) error {
	if id == "" || strings.ContainsAny(id, "/\\ \t\n") || strings.Contains(id, "..") {
		return fmt.Errorf("patch id %q is empty or has a slash, a space or dot-dot", id)
	}
	return nil
}

// manifestOf pulls input.exact_range and input.source_artifact_sha256 out of an OCR JSON document; a document with no
// manifest, or one that is not JSON, gives empty fields.
func manifestOf(data []byte) (exactRange, sourceSHA string) {
	var m struct {
		Manifest struct {
			Input struct {
				ExactRange string `json:"exact_range"`
				SourceSHA  string `json:"source_artifact_sha256"`
			} `json:"input"`
		} `json:"manifest"`
	}
	if json.Unmarshal(data, &m) != nil {
		return "", ""
	}
	return m.Manifest.Input.ExactRange, m.Manifest.Input.SourceSHA
}

func record(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("reviewgate record", flag.ContinueOnError)
	fs.SetOutput(stderr)
	store := fs.String("store", "", "the record directory")
	slug := fs.String("slug", "", "OWNER/NAME")
	patchID := fs.String("patchid", "", "the patch-id of the reviewed diff")
	verdictV := fs.String("verdict", "", "pass, blocked or cap")
	tierV := fs.String("tier", "", "the tier name")
	round := fs.Int("round", 0, "the round judged")
	models := fs.String("models", "", "the models, comma separated")
	when := fs.String("time", "", "RFC3339; default now (UTC)")
	covered := fs.Int("covered", -1, "files OCR reviewed (with -total)")
	total := fs.Int("total", -1, "files the diff changes (with -covered)")
	coveredFiles := fs.String("covered-files", "", "the files OCR reviewed, one repo-relative path per line (with -covered)")
	var evidence listFlag
	fs.Var(&evidence, "evidence", "an OCR JSON file to keep with the record (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "reviewgate:", err)
		return 2
	}
	if *store == "" || *slug == "" || *tierV == "" || *round < 1 {
		fmt.Fprintln(stderr, "usage: reviewgate record -store DIR -slug OWNER/NAME -patchid ID -verdict pass|blocked|cap -tier NAME -round N -models \"a,b\" [-time RFC3339] [-covered N -total M [-covered-files FILE]] [-evidence FILE]...")
		return 2
	}
	if err := safeID(*patchID); err != nil {
		return fail(err)
	}
	if !slices.Contains([]string{"pass", "blocked", "cap"}, *verdictV) {
		return fail(fmt.Errorf("verdict %q is not pass, blocked or cap", *verdictV))
	}
	parts := strings.Split(*slug, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.Contains(*slug, "..") || strings.ContainsAny(*slug, "\\ ") {
		return fail(fmt.Errorf("slug %q is not OWNER/NAME", *slug))
	}
	t := time.Now().UTC()
	if *when != "" {
		var err error
		if t, err = time.Parse(time.RFC3339, *when); err != nil {
			return fail(err)
		}
	}
	if (*covered < 0) != (*total < 0) || (*covered >= 0 && *covered > *total) {
		return fail(fmt.Errorf("-covered %d and -total %d must be given together, as numbers with covered <= total", *covered, *total))
	}
	body := fmt.Sprintf("verdict: %s\ntier: %s\nround: %d\nmodels: %s\ntime: %s\n", *verdictV, *tierV, *round, *models, t.UTC().Format(time.RFC3339))
	var names []string
	if *coveredFiles != "" {
		if *covered < 0 {
			return fail(errors.New("-covered-files needs -covered and -total"))
		}
		var err error
		if names, err = readLines(*coveredFiles); err != nil {
			return fail(err)
		}
		if err := checkCoveredNames(names); err != nil {
			return fail(err)
		}
		if len(names) != *covered {
			return fail(fmt.Errorf("-covered %d but %d file name(s) given", *covered, len(names)))
		}
	}
	if *covered >= 0 {
		body += fmt.Sprintf("covered: %d\ntotal: %d\n", *covered, *total)
	}
	for _, n := range names {
		body += "covered-file: " + n + "\n"
	}
	path := filepath.Join(*store, parts[0]+"_"+parts[1], *patchID)
	// Read every evidence file before writing anything, so a refused record leaves nothing behind.
	datas := make([][]byte, len(evidence))
	for i, f := range evidence {
		d, err := os.ReadFile(f)
		if err != nil {
			return fail(err)
		}
		datas[i] = d
	}
	if len(datas) > 0 {
		body += fmt.Sprintf("evidence: %d\n", len(datas))
	}
	for i, d := range datas {
		n := i + 1
		if err := writeAtomic(fmt.Sprintf("%s.evidence-%d.json", path, n), d); err != nil {
			return fail(err)
		}
		er, src := manifestOf(d)
		body += fmt.Sprintf("run-%d: %x %s %s\n", n, sha256.Sum256(d), er, src)
	}
	if err := writeAtomic(path, []byte(body)); err != nil {
		return fail(err)
	}
	fmt.Fprintln(stdout, path)
	return 0
}

// checkCoveredNames refuses a covered-file list with a path that is not repo-relative, has a dot-dot segment or
// surrounding space (a record line is trimmed), is empty, or repeats.
func checkCoveredNames(names []string) error {
	seen := map[string]bool{}
	for _, n := range names {
		if n == "" || strings.ContainsAny(n, "\n\r") || strings.TrimSpace(n) != n || strings.HasPrefix(n, "/") || slices.Contains(strings.Split(n, "/"), "..") {
			return fmt.Errorf("covered file %q is not a repo-relative path without dot-dot", n)
		}
		if seen[n] {
			return fmt.Errorf("covered file %q is listed twice", n)
		}
		seen[n] = true
	}
	return nil
}

// ---- verify ----

// recordFile is a parsed record (see record). Every field it names must be present for the record to count.
type recordFile struct {
	verdict, tier, models string
	round, covered, total int
	coveredFiles          []string
	runSHA                []string
	runRange, runSource   []string // from each run line, empty when the OCR manifest had none
}

// parseRecord reads the record's lines; a missing, repeated or non-numeric field makes it malformed.
func parseRecord(data []byte) (*recordFile, error) {
	kv := map[string]string{}
	var names []string
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			return nil, fmt.Errorf("malformed record: line %q has no key", l)
		}
		if k == "covered-file" { // repeatable: one line per file
			names = append(names, strings.TrimSpace(v))
			continue
		}
		if _, dup := kv[k]; dup {
			return nil, fmt.Errorf("malformed record: %q repeated", k)
		}
		kv[k] = strings.TrimSpace(v)
	}
	num := func(k string) (int, error) {
		n, err := strconv.Atoi(kv[k])
		if err != nil || n < 0 {
			return 0, fmt.Errorf("malformed record: %q is not a number", k)
		}
		return n, nil
	}
	r := &recordFile{verdict: kv["verdict"], tier: kv["tier"], models: kv["models"]}
	if r.verdict == "" || r.tier == "" {
		return nil, errors.New("malformed record: no verdict or tier")
	}
	var err error
	if r.round, err = num("round"); err != nil || r.round < 1 {
		return nil, errors.New("malformed record: bad round")
	}
	if _, ok := kv["covered"]; !ok {
		return nil, errors.New("no coverage recorded")
	}
	if r.covered, err = num("covered"); err != nil {
		return nil, err
	}
	if r.total, err = num("total"); err != nil {
		return nil, err
	}
	if r.covered > r.total {
		return nil, errors.New("malformed record: covered exceeds total")
	}
	if len(names) > 0 {
		if err := checkCoveredNames(names); err != nil {
			return nil, fmt.Errorf("malformed record: %w", err)
		}
		if len(names) != r.covered {
			return nil, errors.New("malformed record: covered does not equal the number of covered-file lines")
		}
		r.coveredFiles = names
	}
	n, ok := kv["evidence"]
	if !ok {
		return nil, errors.New("evidence missing: the record names none")
	}
	count, err := strconv.Atoi(n)
	if err != nil || count < 0 || count > 64 {
		return nil, errors.New("malformed record: bad evidence count")
	}
	for i := 1; i <= count; i++ {
		// "<sha256> <exact_range> <source sha>": split on single spaces, so an empty range keeps the source in place.
		f := strings.Split(kv[fmt.Sprintf("run-%d", i)], " ")
		if len(f[0]) != 64 || len(f) > 3 {
			return nil, fmt.Errorf("malformed record: run-%d has no sha256", i)
		}
		for len(f) < 3 {
			f = append(f, "")
		}
		r.runSHA = append(r.runSHA, f[0])
		r.runRange = append(r.runRange, f[1])
		r.runSource = append(r.runSource, f[2])
	}
	return r, nil
}

// docRules are the only files a verified receipt may leave to the in-session review: pure documentation. OCR excludes
// tests and Markdown, but a test file is code that nobody reviewed, so only these two patterns count as docs.
var docRules = func() []globRule {
	r, err := parseGlobs([]string{"**/*.md", "docs/**"})
	if err != nil {
		panic(err)
	}
	return r
}()

// isDocFile reports whether f is documentation: any Markdown file, or anything under docs/.
func isDocFile(f string) bool { return matches(docRules, f) }

// checkout names what is being verified: the resolved commits of the reviewed range and the OCR source artifact hash.
type checkout struct{ base, head, sourceSHA string }

// verifyRecord decides whether the record for a patch-id still stands. It never takes the recorded verdict on trust: the
// record only names the evidence, and the pass comes from running the verdict logic over the evidence files under the
// given policy, round 1 (a round cap counts as blocked). A matching patch-id is only the lookup: every recorded run must
// also name exactly co.base..co.head as its range and co.sourceSHA as its source hash. It returns the one status line,
// or the reason it is not verified.
func verifyRecord(store, slug, patchID string, co checkout, p *policy, changed []string) (string, error) {
	if err := safeID(patchID); err != nil {
		return "", err
	}
	if co.base == "" || co.head == "" || co.sourceSHA == "" || strings.ContainsAny(co.base+co.head+co.sourceSHA, " \t\n.") {
		return "", errors.New("the checkout's base, head and source hash are required, as bare values")
	}
	parts := strings.Split(slug, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.Contains(slug, "..") || strings.ContainsAny(slug, "\\ ") {
		return "", fmt.Errorf("slug %q is not OWNER/NAME", slug)
	}
	path := filepath.Join(store, parts[0]+"_"+parts[1], patchID)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("no record for this patch-id")
	}
	rec, err := parseRecord(data)
	if err != nil {
		return "", err
	}
	if rec.verdict != "pass" {
		return "", fmt.Errorf("recorded verdict is %s, not pass", rec.verdict)
	}
	if rec.tier == "security-critical" {
		return "", errors.New("tier security-critical is never waived")
	}
	if rec.covered == 0 {
		return "", errors.New("zero coverage: no file was reviewed")
	}
	if len(changed) == 0 || rec.total != len(changed) {
		return "", fmt.Errorf("record covers a different diff (total %d, changed %d)", rec.total, len(changed))
	}
	// The files OCR did not review are the changed files minus the recorded names. A record that gave no names cannot
	// say which files were left out, so it verifies only with full coverage.
	if rec.covered < rec.total && len(rec.coveredFiles) == 0 {
		return "", fmt.Errorf("%d changed file(s) were not reviewed by OCR and the record names no covered files; the server-side review must cover them", rec.total-rec.covered)
	}
	if len(rec.coveredFiles) > 0 {
		var uncovered []string
		for _, f := range changed {
			if !slices.Contains(rec.coveredFiles, f) && !isDocFile(f) {
				uncovered = append(uncovered, f)
			}
		}
		for _, n := range rec.coveredFiles {
			if !slices.Contains(changed, n) {
				return "", fmt.Errorf("record covers a different diff (covered file %s is not changed)", n)
			}
		}
		if len(uncovered) > 0 {
			n := len(uncovered)
			if n > 3 {
				uncovered = uncovered[:3]
			}
			return "", fmt.Errorf("%d changed file(s) were not reviewed by OCR and are not documentation: %s; the server-side review must cover them", n, strings.Join(uncovered, ", "))
		}
	}
	if len(rec.runSHA) == 0 {
		return "", errors.New("evidence missing: the record names none")
	}
	var files []string
	for i, want := range rec.runSHA {
		f := fmt.Sprintf("%s.evidence-%d.json", path, i+1)
		d, err := os.ReadFile(f)
		if err != nil {
			return "", fmt.Errorf("evidence missing: run %d", i+1)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(d)); got != want {
			return "", fmt.Errorf("evidence run %d differs from its recorded sha256", i+1)
		}
		if r := readRun(f); !r.ok {
			return "", fmt.Errorf("run %d is not complete: %s", i+1, r.reason)
		}
		// The recorded hashes are the record's claim; the manifest in the evidence (which the sha256 above pins to the record)
		// must say the same, and both must be this checkout's.
		er, src := manifestOf(d)
		if er != rec.runRange[i] || src != rec.runSource[i] {
			return "", fmt.Errorf("run %d: the record's range and source hash differ from its evidence manifest", i+1)
		}
		if want := co.base + ".." + co.head; er != want {
			return "", fmt.Errorf("run %d reviewed a different range (recorded %q, this checkout %q): a rebase or a new head needs a new review", i+1, er, want)
		}
		if src != co.sourceSHA {
			return "", fmt.Errorf("run %d reviewed a different source (recorded hash %q, this checkout %q)", i+1, src, co.sourceSHA)
		}
		files = append(files, f)
	}
	if len(files) < p.MinComplete {
		return "", fmt.Errorf("too few complete runs: %d, need %d", len(files), p.MinComplete)
	}
	code, t := judge(p, changed, files, 1, "", io.Discard, io.Discard)
	if t.Name == "security-critical" {
		return "", errors.New("tier security-critical is never waived (the changed files put the diff in it)")
	}
	if code != 0 {
		return "", fmt.Errorf("the verdict recomputed from the evidence is not a pass (exit %d, tier %s)", code, t.Name)
	}
	return fmt.Sprintf("inloop: ocr %s, covered %d of %d files, tier %s, round %d", rec.models, rec.covered, rec.total, t.Name, rec.round), nil
}

func verify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("reviewgate verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	store := fs.String("store", "", "the record directory")
	slug := fs.String("slug", "", "OWNER/NAME")
	patchID := fs.String("patchid", "", "the patch-id of the pushed diff")
	policyFile := fs.String("policy", "", "the tier policy (JSON), the base branch's")
	changedFile := fs.String("changed", "", "the files the diff changes, one repo-relative path per line")
	base := fs.String("base", "", "the checkout's base commit (the merge-base the gate reviewed from), a full sha")
	head := fs.String("head", "", "the checkout's head commit, a full sha")
	sourceSHA := fs.String("source-sha", "", "the OCR source_artifact_sha256 for this checkout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *store == "" || *slug == "" || *patchID == "" || *policyFile == "" || *changedFile == "" || *base == "" || *head == "" || *sourceSHA == "" {
		fmt.Fprintln(stderr, "usage: reviewgate verify -store DIR -slug OWNER/NAME -patchid ID -base SHA -head SHA -source-sha HEX -policy FILE -changed FILE")
		return 2
	}
	notVerified := func(err error) int {
		fmt.Fprintln(stdout, "not verified: "+err.Error())
		return 1
	}
	p, err := loadPolicy(*policyFile)
	if err != nil {
		return notVerified(err)
	}
	changed, err := readLines(*changedFile)
	if err != nil {
		return notVerified(err)
	}
	line, err := verifyRecord(*store, *slug, *patchID, checkout{*base, *head, *sourceSHA}, p, changed)
	if err != nil {
		return notVerified(err)
	}
	if !p.ReceiptsWaiveReview {
		fmt.Fprintln(stdout, "not verified: receipt checks out ("+line+"), but receipts do not waive review under this policy (no trusted attestation); the server-side review runs")
		return 1
	}
	fmt.Fprintln(stdout, line)
	return 0
}

// ---- rounds ----

// rounds prints the number the next review of a patch-id is. A patch-id already in the file is a re-review of the same
// change and keeps its number; a new one is the count of distinct ids so far, plus one.
func rounds(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("reviewgate rounds", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("file", "", "the per-branch round counter file")
	patchID := fs.String("patchid", "", "the patch-id about to be reviewed")
	bump := fs.Bool("bump", false, "record the patch-id if it is not in the file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" {
		fmt.Fprintln(stderr, "usage: reviewgate rounds -file FILE -patchid ID [-bump]")
		return 2
	}
	if err := safeID(*patchID); err != nil {
		fmt.Fprintln(stderr, "reviewgate:", err)
		return 2
	}
	raw, err := os.ReadFile(*file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(stderr, "reviewgate:", err)
		return 2
	}
	var ids []string
	for _, l := range strings.Split(string(raw), "\n") {
		if f := strings.Fields(l); len(f) > 0 && !slices.Contains(ids, f[0]) {
			ids = append(ids, f[0])
		}
	}
	n := slices.Index(ids, *patchID) + 1
	if n == 0 {
		n = len(ids) + 1
		if *bump {
			text := string(raw)
			if text != "" && !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			text += *patchID + " " + time.Now().UTC().Format(time.RFC3339) + "\n"
			if err := writeAtomic(*file, []byte(text)); err != nil {
				fmt.Fprintln(stderr, "reviewgate:", err)
				return 2
			}
		}
	}
	fmt.Fprintln(stdout, n)
	return 0
}

const usage = "usage: reviewgate verdict|record|rounds|verify [flags]"

// run is main without the process exit: the exit code, results on stdout, errors on stderr.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[0] {
	case "verdict":
		return verdict(args[1:], stdout, stderr)
	case "record":
		return record(args[1:], stdout, stderr)
	case "rounds":
		return rounds(args[1:], stdout, stderr)
	case "verify":
		return verify(args[1:], stdout, stderr)
	}
	fmt.Fprintln(stderr, usage)
	return 2
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
