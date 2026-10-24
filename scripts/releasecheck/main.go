// Command release-check is mash's release gate.
//
//	release-check <X> <version>            judge candidate X for release v<version>
//	release-check --assets <version> <X>   after CI drafted the release: is the tag right and are the assets ours?
//
// It verifies the candidate is on main of the release repository; CI's check runs on X succeeded; X builds, from
// fresh clones in different directories, to identical bytes whose `version` line is exactly "mash <version> (commit X)".
// With --assets it verifies the remote tag v<version> is annotated, signed by a key this machine trusts and points at
// X, and that the draft release carries exactly the four binaries and SHA256SUMS, each equal to our own build of X.
// It reports every problem, not the first. Exit 0 passed, 3 refused, 4 the gate could not decide, 2 usage.
package main

import (
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
	"strings"

	"github.com/randybias/mash/internal/release"
)

// Options configure a check. Repo, X and Version are required.
type Options struct {
	Repo    string // the repository holding X (its origin is not trusted: main is fetched from Remote)
	X       string
	Version string // N.N.N, without the leading v
	GH      string // the gh command; default $RELEASE_CHECK_GH, else gh
	GHRepo  string // default randybias/mash
	Remote  string // the release repository, for main and the tag; default https://github.com/<GHRepo>.git
	NoFetch bool   // do not fetch main first (tests that supply their own origin/main)
	Out     io.Writer

	build        func(ctx context.Context, src, out, goos, goarch, version, commit string) (string, error) // test seam: default release.Build
	afterAssets  func()                                                                                    // test seam: called after the release assets were read
	afterTagList func()                                                                                    // test seam: called between listing the tag and fetching it
}

var versionRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func main() { os.Exit(mainRun(os.Args[1:], os.Stdout)) }

func mainRun(args []string, out io.Writer) int {
	o := Options{Repo: ".", Out: out}
	assets := false
	if len(args) > 0 && args[0] == "--assets" {
		assets = true
		if len(args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: release-check --assets <version> <X>")
			return 2
		}
		o.Version, o.X = args[1], args[2]
	} else {
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: release-check <X> <version>")
			return 2
		}
		o.X, o.Version = args[0], args[1]
	}
	if !versionRE.MatchString(o.Version) {
		fmt.Fprintf(os.Stderr, "release-check: version %q is not N.N.N\n", o.Version)
		return 2
	}
	var err error
	if assets {
		err = checkAssets(context.Background(), o)
	} else {
		err = check(context.Background(), o)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "release-check:", err)
	}
	return exitCode(err)
}

// gateError is a failure of the gate itself (staging the receipt, say) after every check had passed: nothing was
// refused, so it must not read as a refusal.
type gateError struct{ error }

func (g gateError) Unwrap() error { return g.error }

// exitCode: 0 passed, 3 the candidate was refused (a judgment the steps reported), 4 the gate itself failed.
func exitCode(err error) int {
	var g gateError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &g):
		return 4
	}
	return 3
}

// mainRef is where main of the release repository was fetched to.
func mainRef(o Options) string {
	if o.NoFetch {
		return "origin/main"
	}
	return "refs/mash/release-check/main"
}

func (o *Options) defaults() error {
	if o.Repo == "" {
		o.Repo = "."
	}
	if o.GH == "" {
		o.GH = os.Getenv("RELEASE_CHECK_GH")
	}
	if o.GH == "" {
		o.GH = "gh"
	}
	if o.GHRepo == "" {
		o.GHRepo = "randybias/mash"
	}
	if o.Remote == "" {
		o.Remote = "https://github.com/" + o.GHRepo + ".git"
	}
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.X == "" || o.Version == "" {
		return errors.New("a commit and a version are required")
	}
	return nil
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

func gh(o Options, args ...string) ([]byte, error) {
	cmd := exec.Command(o.GH, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", o.GH, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// opError is a failure to find something out (a fetch, gh, the release repository), as opposed to something found
// to be wrong with the candidate: the gate could not decide, which is not a refusal.
type opError struct{ error }

func (o opError) Unwrap() error { return o.error }

func op(err error) error {
	if err == nil {
		return nil
	}
	return opError{err}
}

type report struct {
	out      io.Writer
	problems []string
	refused  int // problems that are judgments about the candidate
}

func (r *report) step(name string, err error) bool {
	if err != nil {
		fmt.Fprintf(r.out, "FAIL  %s\n", name)
		r.problems = append(r.problems, fmt.Sprintf("%s: %v", name, err))
		var oe opError
		if !errors.As(err, &oe) {
			r.refused++
		}
		return false
	}
	fmt.Fprintf(r.out, "ok    %s\n", name)
	return true
}

func (r *report) result(title string) error {
	if len(r.problems) == 0 {
		return nil
	}
	err := fmt.Errorf("refused %s:\n  %s", title, strings.Join(r.problems, "\n  "))
	if r.refused == 0 { // nothing was found wrong: the gate could not find out
		return gateError{fmt.Errorf("could not decide %s:\n  %s", title, strings.Join(r.problems, "\n  "))}
	}
	return err
}

func check(ctx context.Context, o Options) error {
	if err := o.defaults(); err != nil {
		return err
	}
	x, err := git(o.Repo, "rev-parse", "--verify", o.X+"^{commit}")
	if err != nil {
		return fmt.Errorf("resolving %s: %w", o.X, err)
	}
	r := &report{out: o.Out}
	fmt.Fprintf(o.Out, "candidate %s, release v%s\n", x, o.Version)

	// The candidate is on main of the release repository.
	var onMain error
	if !o.NoFetch {
		_, onMain = git(o.Repo, "fetch", "-q", o.Remote, "+refs/heads/main:refs/mash/release-check/main")
		onMain = op(onMain)
	}
	if onMain == nil {
		if _, err := git(o.Repo, "merge-base", "--is-ancestor", x, mainRef(o)); err != nil {
			onMain = errors.New("the candidate is not an ancestor of main")
		}
	}
	r.step("candidate is on main", onMain)

	// CI.
	if runs, err := checkRuns(o, x); err != nil {
		r.step("CI check runs", op(err))
	} else {
		r.step("CI check runs succeeded", release.CheckCI(runs, x))
	}

	// Reproducible: two fresh clones in different directories give the same bytes, and the binary says what it is.
	r.step("X builds reproducibly and reports its version", reproducible(ctx, o, x))
	if err := r.result(x); err != nil {
		return err
	}
	fmt.Fprintf(o.Out, "release-check: %s passes for v%s\n", x, o.Version)
	fmt.Fprintf(o.Out, "compat.json: {\"version\": %q, \"commit\": %q}\n", o.Version, x)
	return nil
}

// freshClone clones the repository into its own temporary directory and checks out x.
func freshClone(repo, x string) (src, work string, err error) {
	work, err = os.MkdirTemp("", "release-check-")
	if err != nil {
		return "", "", op(err)
	}
	src = filepath.Join(work, "src")
	if out, err := exec.Command("git", "clone", "-q", "--no-hardlinks", repo, src).CombinedOutput(); err != nil {
		os.RemoveAll(work)
		return "", "", op(fmt.Errorf("clone: %w\n%s", err, out))
	}
	if _, err := git(src, "checkout", "-q", "--detach", x); err != nil {
		os.RemoveAll(work)
		return "", "", op(err)
	}
	return src, work, nil
}

// buildFresh builds x for the target from a fresh clone and returns the binary's hash (and keeps the binary at
// keep when it is not empty).
func buildFresh(ctx context.Context, o Options, x, goos, goarch, keep string) (string, error) {
	src, work, err := freshClone(o.Repo, x)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	bin := filepath.Join(work, "out", "mash")
	if err := release.CheckSource(src, x, bin); err != nil {
		return "", err
	}
	build := o.build
	if build == nil {
		build = release.Build
	}
	sum, err := build(ctx, src, bin, goos, goarch, o.Version, x)
	if err != nil {
		return "", err
	}
	if keep != "" {
		b, err := os.ReadFile(bin)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(keep, b, 0o755); err != nil {
			return "", err
		}
	}
	return sum, nil
}

// reproducible builds x twice for the host in two different fresh clones: the hashes must match, and the binary's
// `version` must print exactly "mash <version> (commit x)".
func reproducible(ctx context.Context, o Options, x string) error {
	keep := filepath.Join(os.TempDir(), fmt.Sprintf("release-check-%d-mash", os.Getpid()))
	defer os.Remove(keep)
	a, err := buildFresh(ctx, o, x, "", "", keep)
	if err != nil {
		return err
	}
	b, err := buildFresh(ctx, o, x, "", "", "")
	if err != nil {
		return err
	}
	if a != b {
		return fmt.Errorf("not reproducible: two fresh builds of %s give %s and %s", x, a, b)
	}
	got, err := exec.Command(keep, "version").Output()
	if err != nil {
		return fmt.Errorf("running the built binary: %w", err)
	}
	if want := fmt.Sprintf("mash %s (commit %s)\n", o.Version, x); string(got) != want {
		return fmt.Errorf("the binary prints %q, want exactly %q", got, want)
	}
	return nil
}

// checkRuns reads every page of the candidate's check runs. The response must be complete: it needs a
// total_count, pages must keep delivering until that many distinct runs have arrived, and a page that comes up
// empty early is a truncated listing, not the end.
func checkRuns(o Options, x string) ([]release.CheckRun, error) {
	const perPage = 100
	var all []release.CheckRun
	seen := map[int64]bool{}
	for page := 1; page <= 50; page++ {
		b, err := gh(o, "api", fmt.Sprintf("repos/%s/commits/%s/check-runs?per_page=%d&page=%d", o.GHRepo, x, perPage, page))
		if err != nil {
			return nil, err
		}
		var resp struct {
			Total     *int               `json:"total_count"`
			CheckRuns []release.CheckRun `json:"check_runs"`
		}
		if err := json.Unmarshal(b, &resp); err != nil {
			return nil, err
		}
		if resp.Total == nil || *resp.Total < 0 {
			return nil, errors.New("the response has no usable total_count")
		}
		if *resp.Total == 0 { // a complete, empty listing: CI has not run on this commit, which CheckCI refuses
			return nil, nil
		}
		for _, r := range resp.CheckRuns {
			if !seen[r.ID] {
				seen[r.ID] = true
			}
			all = append(all, r)
		}
		switch {
		case len(seen) >= *resp.Total:
			return all, nil
		case len(resp.CheckRuns) == 0:
			return nil, fmt.Errorf("truncated listing: %d of %d check runs arrived before an empty page", len(seen), *resp.Total)
		}
	}
	return nil, errors.New("more than 50 pages of check runs")
}

// tagRefs lists the tag in the release repository: the object the tag name points at (the tag object when it is
// annotated) and the commit it leads to.
func tagRefs(repo, remote, tag string) (object, commit string, err error) {
	out, err := git(repo, "ls-remote", remote, "refs/tags/"+tag, "refs/tags/"+tag+"^{}")
	if err != nil {
		return "", "", op(err)
	}
	var peeled string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		switch f[1] {
		case "refs/tags/" + tag:
			object = f[0]
		case "refs/tags/" + tag + "^{}":
			peeled = f[0]
		}
	}
	if object == "" {
		return "", "", fmt.Errorf("tag %s does not exist in %s", tag, remote)
	}
	if peeled == "" {
		peeled = object // a lightweight tag points at the commit itself
	}
	return object, peeled, nil
}

// verifyTag fetches exactly the tag object that was listed (object) and has git verify its signature with the keys
// of this machine: a lightweight tag has no signature, a signature by a key git does not trust fails, and a tag
// that was swapped after the listing is not the object that was listed. The signed object must lead to commit x.
func verifyTag(o Options, tag, object, x string) error {
	ref := "refs/mash/release-check/tags/" + tag
	if _, err := git(o.Repo, "fetch", "-q", o.Remote, "+refs/tags/"+tag+":"+ref); err != nil {
		return op(err)
	}
	if got, err := git(o.Repo, "rev-parse", ref); err != nil || got != object {
		return fmt.Errorf("tag %s changed between listing and fetching it (listed %s, fetched %s %v)", tag, object, got, err)
	}
	if kind, err := git(o.Repo, "cat-file", "-t", object); err != nil || kind != "tag" {
		return fmt.Errorf("tag %s is a %q, not an annotated tag, so it has no signature (%v)", tag, kind, err)
	}
	if c, err := git(o.Repo, "rev-parse", object+"^{commit}"); err != nil || c != x {
		return fmt.Errorf("the signed tag object %s leads to %s, not the candidate %s (%v)", object, c, x, err)
	}
	if _, err := git(o.Repo, "verify-tag", object); err != nil {
		return fmt.Errorf("tag %s has no valid signature from a trusted key: %w", tag, err)
	}
	return nil
}

// checkAssets verifies the draft release for v<version> belongs to X: the tag is annotated, signed by a trusted key and
// points at X (and still does after the assets were read), and the release carries exactly the four binaries and
// SHA256SUMS, each equal to our own fresh build of X for that target.
func checkAssets(ctx context.Context, o Options) error {
	if err := o.defaults(); err != nil {
		return err
	}
	x, err := git(o.Repo, "rev-parse", "--verify", o.X+"^{commit}")
	if err != nil {
		return fmt.Errorf("resolving %s: %w", o.X, err)
	}
	r := &report{out: o.Out}
	tag := "v" + o.Version
	tagObj, c, terr := tagRefs(o.Repo, o.Remote, tag)
	if terr != nil {
		r.step("tag "+tag+" exists in the release repository", terr)
	} else if c != x {
		r.step("tag "+tag+" points at the candidate", fmt.Errorf("it points at %s, not %s", c, x))
	} else {
		r.step("tag "+tag+" points at the candidate", nil)
	}
	if terr == nil {
		if o.afterTagList != nil {
			o.afterTagList()
		}
		r.step("tag "+tag+" is signed by a trusted key", verifyTag(o, tag, tagObj, x))
	}

	// Our own build of X for every target.
	hashes := map[release.Target]string{}
	var berrs []error
	for _, t := range release.Targets {
		h, err := buildFresh(ctx, o, x, t.GOOS, t.GOARCH, "")
		if err != nil {
			berrs = append(berrs, fmt.Errorf("%s/%s: %w", t.GOOS, t.GOARCH, err))
			continue
		}
		hashes[t] = h
	}
	buildOK := r.step("fresh build of X for every target", errors.Join(berrs...))

	b, err := gh(o, "release", "view", tag, "--repo", o.GHRepo, "--json", "assets")
	if !r.step("the release exists", op(err)) {
		return r.result("release " + tag + " assets")
	}
	var resp struct {
		Assets []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		return gateError{fmt.Errorf("could not read the release's assets: %w", err)}
	}
	have := map[string]string{}
	for _, a := range resp.Assets {
		have[a.Name] = a.Digest
	}
	if o.afterAssets != nil {
		o.afterAssets()
	}
	// The tag must still be the verified object, leading to the same commit: it can be moved while we read.
	if terr == nil {
		obj, c, err := tagRefs(o.Repo, o.Remote, tag)
		switch {
		case err != nil: // keeps its classification: a lookup that failed is not a moved tag
			r.step("tag "+tag+" still is the verified object", err)
		case c != x || obj != tagObj:
			r.step("tag "+tag+" still is the verified object", fmt.Errorf("now %q -> %q", obj, c))
		}
	}
	if buildOK {
		r.step("the release carries exactly our build of X", release.CheckAssets(have, hashes))
	}
	return r.result("release " + tag + " assets")
}

var _ = bytes.NewReader
