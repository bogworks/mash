// Command release-check is mash's release gate.
//
//	release-check <X> <version>            judge candidate X for release v<version>
//	release-check --assets <version> <X>   after CI drafted the release: is the tag right and are the assets ours?
//	release-check --consumer <checkout> <X> <version>   as the first form, plus the consumer's check run against X (maintainers only)
//	release-check publish [--wait <d>] <version> <X>     the whole path, resumable: gate, signed tag, draft, --assets gate, publish, verify
//
// It verifies the candidate is on main of the release repository; CI's check runs on X succeeded; X builds, from
// fresh clones in different directories, to identical bytes whose `version` line is exactly "mash <version> (commit X)".
// With --assets it verifies the remote tag v<version> is annotated, signed by a key this machine trusts and points at
// X, and that the draft release carries exactly the four binaries and SHA256SUMS, each equal to our own build of X.
// It then signs SHA256SUMS (ssh-keygen -Y sign, namespace mash-release) after checking every line against those builds, verifies
// the signature against ci/release.allowed_signers at X and uploads SHA256SUMS.sig; RELEASE_SIGNING_KEY names the key (the one that signs the tag).
// With --consumer (maintainers only: needs a downstream consumer's checkout) it also runs that checkout's own check of mash against X, as its .mash-consumer-check manifest directs (docs/release.md): opt-in, as it can take 30+ minutes.
// It refuses a candidate that still has change-log fragments in changelog.d/ (other than README.md) or whose
// docs/decisions/INDEX.md differs from what the records at X generate (internal/adrindex), both read from git at X.
// It refuses to tag while an earlier vN.N.N tag has a release that is a draft or missing, unless docs/skipped-releases.txt
// at X records it with a reason. It reports every problem, not the first. Exit 0 passed, 3 refused, 4 the gate could not decide, 2 usage.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"strings"
	"time"

	"github.com/bogworks/mash/internal/release"
)

// Options configure a check. Repo, X and Version are required.
type Options struct {
	Repo    string // the repository holding X (its origin is not trusted: main is fetched from Remote)
	X       string
	Version string // N.N.N, without the leading v
	GH      string // the gh command; default $RELEASE_CHECK_GH, else gh
	GHRepo  string // the release repository (owner/name): default $RELEASE_CHECK_REPO, else origin of Repo when it is on github.com, else bogworks/mash
	Remote  string // the release repository, for main and the tag; default https://github.com/<GHRepo>.git
	NoFetch bool   // do not fetch main first (tests that supply their own origin/main)
	// Signers is the allowed_signers file a release tag must verify against: default docs/allowed_signers of Repo,
	// the checkout running the check. Never the machine's own git config, so the gate is the same for anyone who
	// runs it, and a key a machine happens to trust cannot pass a release.
	Signers string
	Out     io.Writer
	// Consumer is a consumer checkout: when set, check adds the step that runs the consumer's check against
	// the candidate (release-check --consumer). Empty: no such step.
	Consumer string
	// Publish selects `release-check publish`; Wait is how long it waits for release.yml's draft (default 45m).
	Publish bool
	Wait    time.Duration

	prevFetched *string // the tag already fetched into prevTagRef, so the plugin and render-contract steps fetch it once; nil: always fetch

	build           func(ctx context.Context, src, out, goos, goarch, version, commit string) (string, error) // test seam: default release.Build
	afterAssets     func()                                                                                    // test seam: called after the release assets were read
	afterTagList    func()                                                                                    // test seam: called between listing the tag and fetching it
	prevBinary      func(ctx context.Context, tag, dir string) (string, error)                                // test seam: the previous release's binary for this host, in dir; default gh release download
	candBinary      func(ctx context.Context, dir string) (string, error)                                     // test seam: the candidate's binary for this host, in dir; default a fresh build of X
	measure         measureFunc                                                                               // test seam: the delivery-latency measurement of a binary; default measureLatencies
	consumer        consumerRun                                                                               // test seam: the consumer's script runner; default sh <manifest command> in a fresh clone
	consumerTimeout time.Duration                                                                             // test seam: the script's deadline; default consumerTimeout
	hostGOOS        string                                                                                    // test seam: the host's GOOS, which decides whether the candidate's host build serves the linux container; default runtime.GOOS
}

var versionRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func main() { os.Exit(mainRun(os.Args[1:], os.Stdout)) }

// versionTimeout bounds the candidate's `version` run.
var versionTimeout = 30 * time.Second

// parseArgs reads the command line: the modes do not combine, and --assets and --consumer are only the first argument.
func parseArgs(args []string) (o Options, assets bool, err error) {
	o = Options{Repo: "."}
	if len(args) > 0 && args[0] == "publish" {
		o.Publish, o.Wait = true, defaultWait
		rest := args[1:]
		var pos []string
		for i := 0; i < len(rest); i++ {
			a := rest[i]
			switch {
			case a == "--assets" || a == "--consumer" || a == "publish":
				return o, false, errors.New("publish does not combine with other modes")
			case a == "--wait" || strings.HasPrefix(a, "--wait="):
				v := strings.TrimPrefix(a, "--wait=")
				if a == "--wait" {
					if i+1 >= len(rest) {
						return o, false, errors.New("--wait needs a duration")
					}
					i++
					v = rest[i]
				}
				d, derr := time.ParseDuration(v)
				if derr != nil || d <= 0 {
					return o, false, fmt.Errorf("--wait %q is not a positive duration", v)
				}
				o.Wait = d
			case strings.HasPrefix(a, "-"):
				return o, false, fmt.Errorf("unknown flag %s", a)
			default:
				pos = append(pos, a)
			}
		}
		if len(pos) != 2 {
			return o, false, errors.New("usage: release-check publish [--wait <duration>] <version> <X>")
		}
		return withArgs(o, pos[1], pos[0]), false, nil
	}
	if len(args) > 0 && (args[0] == "--assets" || args[0] == "--consumer") {
		for _, a := range args[1:] {
			if a == "--assets" || a == "--consumer" {
				return o, false, errors.New("--assets and --consumer do not combine")
			}
		}
	}
	switch {
	case len(args) > 0 && args[0] == "--assets":
		if len(args) != 3 {
			return o, false, errors.New("usage: release-check --assets <version> <X>")
		}
		return withArgs(o, args[2], args[1]), true, nil
	case len(args) > 0 && args[0] == "--consumer":
		if len(args) != 4 {
			return o, false, errors.New("usage: release-check --consumer <checkout> <X> <version>")
		}
		o.Consumer = args[1]
		return withArgs(o, args[2], args[3]), false, nil
	case len(args) != 2:
		return o, false, errors.New("usage: release-check <X> <version>")
	}
	return withArgs(o, args[0], args[1]), false, nil
}

func withArgs(o Options, x, version string) Options {
	o.X, o.Version = x, version
	return o
}

func mainRun(args []string, out io.Writer) int {
	o, assets, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	o.Out = out
	if !versionRE.MatchString(o.Version) {
		fmt.Fprintf(os.Stderr, "release-check: version %q is not N.N.N\n", o.Version)
		return 2
	}
	if o.Publish {
		err = publish(context.Background(), o)
	} else if assets {
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
		o.GHRepo = os.Getenv("RELEASE_CHECK_REPO")
	}
	if o.GHRepo == "" {
		if u, err := git(o.Repo, "remote", "get-url", "origin"); err == nil {
			if r, ok := parseGitHubRepo(u); ok {
				o.GHRepo = r
			}
		}
	}
	if o.GHRepo == "" {
		o.GHRepo = "bogworks/mash"
	}
	if o.Signers == "" {
		o.Signers = filepath.Join(o.Repo, "docs", "allowed_signers")
	}
	if s, err := filepath.Abs(o.Signers); err == nil {
		o.Signers = s
	}
	if o.Remote == "" {
		o.Remote = "https://github.com/" + o.GHRepo + ".git"
	}
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.prevFetched == nil {
		o.prevFetched = new(string)
	}
	if o.X == "" || o.Version == "" {
		return errors.New("a commit and a version are required")
	}
	return nil
}

// parseGitHubRepo extracts owner/name from a github.com remote URL: https://github.com/o/n(.git),
// git@github.com:o/n(.git) or ssh://git@github.com/o/n(.git).
func parseGitHubRepo(u string) (string, bool) {
	u = strings.TrimSpace(u)
	var path string
	switch {
	case strings.HasPrefix(u, "git@github.com:"):
		path = strings.TrimPrefix(u, "git@github.com:")
	case strings.HasPrefix(u, "https://github.com/"):
		path = strings.TrimPrefix(u, "https://github.com/")
	case strings.HasPrefix(u, "ssh://git@github.com/"):
		path = strings.TrimPrefix(u, "ssh://git@github.com/")
	default:
		return "", false
	}
	path = strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return parts[0] + "/" + parts[1], true
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

// ok is whether every step so far passed.
func (r *report) ok() bool { return len(r.problems) == 0 }

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

	// No earlier release was left a draft or unreleased.
	r.step("earlier releases are published or recorded as skipped", earlierReleases(o, x))
	// And this one is above the latest published release.
	r.step("the version is above the latest published release", aboveLatestPublished(o))

	// Reproducible: two fresh clones in different directories give the same bytes, and the binary says what it is.
	r.step("X builds reproducibly and reports its version", reproducible(ctx, o, x))
	// The plugin manifest, as committed at X, names the release: Claude Code updates an installed plugin only when
	// its version changes.
	manifest, merr := git(o.Repo, "show", x+":"+release.PluginManifest)
	if merr != nil {
		merr = fmt.Errorf("%s is missing at the candidate: %w", release.PluginManifest, merr)
	} else {
		merr = release.CheckPluginVersion([]byte(manifest), o.Version)
	}
	r.step("the plugin manifest names the release version", merr)
	// Release preparation ran: the change log fragments are assembled and the ADR index is regenerated (ADR 0068).
	r.step("the change log fragments are assembled", changelogAssembled(o, x))
	r.step("the ADR index is current", adrIndexFresh(o, x))
	// The candidate and the previous release's binaries are built and downloaded once, by the plugin step, and the
	// latency step reuses them.
	bins := &binaries{}
	defer bins.close()
	r.step("the plugin runs against the candidate and the previous release, and the previous plugin against the candidate", pluginCompat(ctx, o, x, bins))
	r.step("the render contract is unchanged since the previous release, or the change is called out", renderContract(o, x))
	r.step("fresh-session mail arrives no slower than the previous release", latencyStep(ctx, o, bins))
	if o.Consumer != "" { // opt-in: it takes 30+ minutes,
		r.step("the consumer's check passes against the candidate", consumerStep(ctx, o, x, bins))
	}
	if err := r.result(x); err != nil {
		return err
	}
	fmt.Fprintf(o.Out, "release-check: %s passes for v%s\n", x, o.Version)
	fmt.Fprintf(o.Out, "version: %s commit: %s\n", o.Version, x)
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
		var te *release.ToolError
		if errors.As(err, &te) {
			return "", op(err) // git failed: nothing was found out about the candidate
		}
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
			return "", op(err)
		}
		if err := os.WriteFile(keep, b, 0o755); err != nil {
			return "", op(err)
		}
	}
	return sum, nil
}

// reproducible builds x twice for the host in two different fresh clones: the hashes must match, and the binary's
// `version` must print exactly "mash <version> (commit x)".
func reproducible(ctx context.Context, o Options, x string) error {
	// A name nobody can predict, created exclusively (0600): a planted symlink at a guessable path would otherwise be
	// written through, and the file could be swapped between the write and the run.
	kf, err := os.CreateTemp("", "release-check-*-mash")
	if err != nil {
		return op(fmt.Errorf("a temporary file for the built binary: %w", err))
	}
	keep := kf.Name()
	if err := kf.Chmod(0o700); err != nil { // CreateTemp makes it 0600; it must be runnable by us, and only by us
		kf.Close()
		return op(err)
	}
	kf.Close()
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
	// The candidate is unvetted code: it gets a deadline, and a hang is a finding about it.
	runCtx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, keep, "version")
	cmd.WaitDelay = 2 * time.Second // a child that keeps the pipe open must not outlive the kill
	got, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		switch {
		case runCtx.Err() != nil && ctx.Err() == nil:
			return fmt.Errorf("the built binary's version did not finish within %v", versionTimeout)
		case errors.As(err, &ee):
			return fmt.Errorf("running the built binary: %w", err)
		case errors.Is(err, exec.ErrWaitDelay): // it exited, but left a process holding its output open
			return fmt.Errorf("the built binary's version left a process running: %w", err)
		default:
			return op(fmt.Errorf("starting the built binary: %w", err)) // could not run it: says nothing about it
		}
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

// verifyTag fetches exactly the tag object that was listed (object) and has git verify its signature against
// o.Signers (docs/allowed_signers): a lightweight tag has no signature, a signature by a key git does not trust fails, and a tag
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
	// allowedSignersFile governs SSH signatures only: git would verify a PGP or X.509 signature with this machine's
	// own keyrings, so only an SSH-signed tag is let through to verify-tag (codex H1 on #24).
	body, err := git(o.Repo, "cat-file", "tag", object)
	if err != nil {
		return op(err)
	}
	if !strings.Contains(body, "\n-----BEGIN SSH SIGNATURE-----\n") || strings.Contains(body, "-----BEGIN PGP SIGNATURE-----") ||
		strings.Contains(body, "-----BEGIN SIGNED MESSAGE-----") {
		return fmt.Errorf("tag %s has no valid signature from a trusted key: it is not SSH-signed, and only docs/allowed_signers' SSH keys sign releases", tag)
	}
	if _, err := git(o.Repo, "-c", "gpg.ssh.allowedSignersFile="+o.Signers, "verify-tag", object); err != nil {
		return fmt.Errorf("tag %s has no valid signature from a trusted key: %w", tag, err)
	}
	return nil
}

// checkAssets verifies the draft release for v<version> belongs to X: the tag is annotated, signed by a trusted key and
// points at X (and still does after the assets were read), and the release carries exactly the four binaries and
// SHA256SUMS, each equal to our own fresh build of X for that target.
func checkAssets(ctx context.Context, o Options) error { return checkAssetsRecord(ctx, o, nil) }

// checkAssetsRecord is checkAssets that also leaves what it established in rec (when not nil), for publish to bind to.
func checkAssetsRecord(ctx context.Context, o Options, rec *gateRecord) error {
	if rec != nil {
		*rec = gateRecord{} // only a gate that passes in full leaves a record
	}
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
	// The quickstart script is a release asset: its expected hash is the tagged source's.
	var scriptHash string
	script, found, serr := fileAt(o.Repo, x, "scripts/"+release.ScriptName)
	switch {
	case serr != nil:
		r.step("scripts/"+release.ScriptName+" at X", serr)
	case !found:
		r.step("scripts/"+release.ScriptName+" at X", fmt.Errorf("the script is missing at %s, so the release cannot carry it", x))
	default:
		sum := sha256.Sum256([]byte(script))
		scriptHash = hex.EncodeToString(sum[:])
		r.step("scripts/"+release.ScriptName+" at X", nil)
	}

	b, err := gh(o, "release", "view", tag, "--repo", o.GHRepo, "--json", "assets,isDraft")
	if !r.step("the release exists", op(err)) {
		return r.result("release " + tag + " assets")
	}
	var resp struct {
		IsDraft bool `json:"isDraft"`
		Assets  []struct {
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
	if buildOK && scriptHash != "" {
		if r.step("the release carries exactly our build of X and the script at X", release.CheckAssets(have, hashes, scriptHash)) && terr == nil && r.ok() {
			// Not skippable: no flag reaches past it. Only a release whose tag, assets and builds all passed is signed.
			// Attestations, SBOMs and the build provenance are verified first, so no maintainer signature is ever made over
			// assets the gate rejected. They also cover a resumed or hand-made draft that release.yml's attest job never
			// saw, including one that already carries a verifying SHA256SUMS.sig.
			r.step("build provenance is attested for every binary", verifyAttestations(o, tag))
			if r.ok() {
				r.step("every binary has an attested CycloneDX SBOM and build provenance that verifies", verifySupplyChain(o, tag))
			}
			// The signature is made (or an attached one verified) only after all of the above passed.
			if r.ok() {
				r.step("SHA256SUMS is checked against our builds and the script at X, and signed by a release signer", signedSums(o, tag, x, resp.IsDraft, have, hashes, scriptHash, rec))
			}
		}
	}
	return r.result("release " + tag + " assets")
}

// downloadPrevious fetches the release's binary for this host into dir with gh; a failure is the gate's, not the candidate's.
func downloadPrevious(o Options, tag, dir string) (string, error) {
	asset := "mash-" + runtime.GOOS + "-" + runtime.GOARCH
	if _, err := gh(o, "release", "download", tag, "--repo", o.GHRepo, "-p", asset, "-D", dir); err != nil {
		return "", op(err)
	}
	p := filepath.Join(dir, asset)
	if _, err := os.Stat(p); err != nil {
		return "", op(fmt.Errorf("release %s has no %s: %w", tag, asset, err))
	}
	return p, nil
}

// sample hook inputs, as Claude Code sends them on stdin.
var hookInputs = map[string]string{
	"PostToolUse":      `{"hook_event_name":"PostToolUse","session_id":"s","cwd":"/","tool_name":"Bash","tool_input":{"command":"true"},"tool_response":{}}`,
	"UserPromptSubmit": `{"hook_event_name":"UserPromptSubmit","session_id":"s","cwd":"/","prompt":"hello"}`,
}

// remoteTags lists the release repository's v* tags, not the checkout's: the release procedure fetches main only.
func remoteTags(o Options) ([]string, error) {
	listing, err := git(o.Repo, "ls-remote", "--tags", "--refs", o.Remote, "v*")
	if err != nil {
		return nil, op(err)
	}
	var tags []string
	for _, line := range strings.Split(listing, "\n") {
		if f := strings.Fields(line); len(f) == 2 && strings.HasPrefix(f[1], "refs/tags/") {
			tags = append(tags, strings.TrimPrefix(f[1], "refs/tags/"))
		}
	}
	return tags, nil
}

// skippedReleases is where a candidate records the earlier tags that were never published, one `vX.Y.Z reason` per line
// (# comments allowed; a line without a reason does not count); the file is read at X, so a skip is reviewed on main like any other change.
const skippedReleases = "docs/skipped-releases.txt"

type releaseRow struct {
	TagName string `json:"tagName"`
	IsDraft bool   `json:"isDraft"`
}

// listReleases is `gh release list` of the release repository: every release, drafts included.
func listReleases(o Options) ([]releaseRow, error) {
	out, err := gh(o, "release", "list", "--repo", o.GHRepo, "--limit", "1000", "--json", "tagName,isDraft")
	if err != nil {
		return nil, op(err)
	}
	var releases []releaseRow
	if err := json.Unmarshal(out, &releases); err != nil {
		return nil, op(fmt.Errorf("reading gh release list: %w", err))
	}
	return releases, nil
}

// aboveLatestPublished is ordering rule (b): v<version> is refused unless <version> is strictly above the latest
// published (non-draft) release. Rule (a), no lower tag left unpublished, is earlierReleases.
func aboveLatestPublished(o Options) error {
	releases, err := listReleases(o)
	if err != nil {
		return err
	}
	return orderAbove(o.Version, releases)
}

// orderAbove is the pure part of rule (b). Non-semver tags are not releases here.
func orderAbove(version string, releases []releaseRow) error {
	cur, ok := parseSemver("v" + version)
	if !ok {
		return fmt.Errorf("version %q is not N.N.N", version)
	}
	var latest [3]int
	var latestTag string
	for _, r := range releases {
		v, ok := parseSemver(r.TagName)
		if r.IsDraft || !ok {
			continue
		}
		if latestTag == "" || semverLess(latest, v) {
			latest, latestTag = v, r.TagName
		}
	}
	if latestTag != "" && !semverLess(latest, cur) {
		return fmt.Errorf("v%s is not above the latest published release %s", version, latestTag)
	}
	return nil
}

// earlierReleases refuses a release while any earlier vN.N.N tag of the release repository has a release that is a
// draft or missing (v0.2.8 and v0.2.9 were tagged and never published, and v0.2.10 shipped) unless X's skippedReleases
// lists it with a reason. Pre-release and non-semver tags are not releases; every offender is named.
func earlierReleases(o Options, x string) error {
	tags, err := remoteTags(o)
	if err != nil {
		return err
	}
	cur, ok := parseSemver("v" + o.Version)
	if !ok {
		return fmt.Errorf("version %q is not N.N.N", o.Version)
	}
	var earlier []string
	for _, t := range tags {
		if v, ok := parseSemver(t); ok && semverLess(v, cur) {
			earlier = append(earlier, t)
		}
	}
	if len(earlier) == 0 {
		return nil
	}
	skipped := map[string]bool{}
	var noReason []string
	body, has, err := fileAt(o.Repo, x, skippedReleases)
	if err != nil {
		return err
	}
	if has {
		for _, line := range strings.Split(body, "\n") {
			f := strings.Fields(line)
			if len(f) == 0 || strings.HasPrefix(f[0], "#") {
				continue
			}
			if len(f) < 2 {
				noReason = append(noReason, f[0])
				continue
			}
			skipped[f[0]] = true
		}
	}
	releases, err := listReleases(o)
	if err != nil {
		return err
	}
	published := map[string]bool{}
	for _, r := range releases {
		published[r.TagName] = !r.IsDraft
	}
	sort.Slice(earlier, func(i, j int) bool {
		a, _ := parseSemver(earlier[i])
		b, _ := parseSemver(earlier[j])
		return semverLess(a, b)
	})
	var bad []string
	for _, t := range earlier {
		if !published[t] && !skipped[t] {
			state := "has no release"
			if _, drafted := published[t]; drafted {
				state = "is a draft"
			}
			if slices.Contains(noReason, t) {
				state += " and its line in " + skippedReleases + " is missing its reason"
			}
			bad = append(bad, t+" "+state)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("earlier tags are not released: %s: publish it (gh release edit <tag> --draft=false) or record it in %s with a reason", strings.Join(bad, ", "), skippedReleases)
	}
	return nil
}

// pluginCompat runs the plugin of X (its hooks.json and .mcp.json as committed) against the candidate's binary and the
// previous release's, since an installed plugin at N runs against mash N and N-1 (see runPluginAgainst), and the
// previous release's plugin against the candidate's binary, since Claude Code updates an installed plugin only when
// its version changes, so users run an older plugin with a newer mash. No previous release: only the candidate.
func pluginCompat(ctx context.Context, o Options, x string, b *binaries) error {
	tags, err := remoteTags(o)
	if err != nil {
		return err
	}
	prev, hasPrev := release.PreviousTag(tags, o.Version)
	b.prevTag, b.hasPrev = prev, hasPrev
	hooks, herr := git(o.Repo, "show", x+":"+release.PluginHooks)
	if herr != nil {
		return fmt.Errorf("%s is missing at the candidate: %w", release.PluginHooks, herr)
	}
	cmds, err := release.HookCommands([]byte(hooks))
	if err != nil {
		return err
	}
	if err := release.CheckHookMatchers(cmds); err != nil {
		return err
	}
	mcp, merr := git(o.Repo, "show", x+":"+release.PluginMCP)
	if merr != nil {
		return fmt.Errorf("%s is missing at the candidate: %w", release.PluginMCP, merr)
	}
	mcpCmd, mcpArgs, mcpEnv, err := release.MCPServer([]byte(mcp))
	if err != nil {
		return err
	}
	if !release.AcceptedMCPCommand(mcpCmd) { // the plugin interface: bare mash, or the plugin's launcher script
		return fmt.Errorf("%s launches %q, not mash or %s: the plugin cannot be checked against a mash binary", release.PluginMCP, mcpCmd, release.LauncherCommand)
	}
	b.mcpArgs, b.mcpEnv = mcpArgs, mcpEnv
	spec := &serveSpec{Args: mcpArgs, Env: mcpEnv}

	work, err := os.MkdirTemp("", "release-check-prev-")
	if err != nil {
		return op(err)
	}
	b.work = work
	// A plugin that runs the launcher needs its own files: the plugin directory at the candidate commit.
	var candRoot string
	if needsPluginRoot(cmds, mcpCmd) {
		if candRoot, err = materializePlugin(o.Repo, x, filepath.Join(work, "cand-plugin")); err != nil {
			return err
		}
		if release.IsLauncherCommand(mcpCmd) {
			spec.Root = candRoot
			b.mcpRoot = candRoot
		}
	}

	// The candidate, built for this host the way reproducible() builds it.
	cdir := filepath.Join(work, "cand")
	if err := os.MkdirAll(cdir, 0o755); err != nil {
		return op(err)
	}
	var cgot string
	if o.candBinary != nil {
		cgot, err = o.candBinary(ctx, cdir)
	} else {
		cgot = filepath.Join(cdir, "built")
		_, err = buildFresh(ctx, o, x, "", "", cgot)
	}
	if err != nil {
		return err // a build failure is reported as reproducible() reports it
	}
	cbin, err := stageMash(work, "cand-bin", cgot)
	if err != nil {
		return err
	}
	b.cand = cbin
	problems := runPluginAgainst(ctx, cbin, "the candidate", true, false, cmds, spec, candRoot)

	if !hasPrev {
		fmt.Fprintf(o.Out, "note  no previous release below v%s: nothing more to run the plugin against\n", o.Version)
		return errors.Join(problems...)
	}
	pp, pspec, err := previousPluginAgainst(ctx, o, work, prev, cbin)
	b.prevSpec = pspec
	if err != nil {
		return err
	}
	problems = append(problems, pp...)
	dl := o.prevBinary
	if dl == nil {
		dl = func(_ context.Context, tag, dir string) (string, error) { return downloadPrevious(o, tag, dir) }
	}
	dlDir := filepath.Join(work, "dl")
	if err := os.MkdirAll(dlDir, 0o755); err != nil {
		return op(err)
	}
	got, err := dl(ctx, prev, dlDir)
	if err != nil {
		return op(err)
	}
	bin, err := stageMash(work, "prev-bin", got)
	if err != nil {
		return err
	}
	b.prev = bin
	fmt.Fprintf(o.Out, "note  running the plugin against the previous release %s\n", prev)
	problems = append(problems, runPluginAgainst(ctx, bin, "the previous release "+prev, false, release.TagBelow(prev, release.HookSince), cmds, spec, candRoot)...)
	return errors.Join(problems...)
}

// previousPluginAgainst runs the previous release's plugin (hooks.json and .mcp.json at its tag) against the candidate's
// binary cbin, as strictly as the candidate's own plugin. What cannot be run is a note, not a finding: a shipped plugin
// cannot be fixed (a release before hooks has no hooks.json; an unreadable file, or a server that is not mash, is
// skipped). An error is the gate's. The serve spec it read is returned (nil when it could not be read), for the latency
// step to launch the candidate's serve as that plugin does.
func previousPluginAgainst(ctx context.Context, o Options, work, prev, cbin string) ([]error, *serveSpec, error) {
	ref, err := fetchPrevTag(o, prev)
	if err != nil {
		return nil, nil, err
	}
	rev := ref + "^{commit}"
	label := "the candidate, run by the previous release's plugin (" + prev + ")"
	note := func(format string, a ...any) {
		fmt.Fprintf(o.Out, "note  the previous release's plugin ("+prev+"): "+format+"\n", a...)
	}
	var cmds []release.HookCommand
	hooks, ok, err := fileAt(o.Repo, rev, release.PluginHooks)
	switch {
	case err != nil:
		return nil, nil, err
	case !ok:
		note("%s is absent (a release before hooks): running only its MCP server", release.PluginHooks)
	default:
		if cmds, err = release.HookCommands([]byte(hooks)); err != nil {
			note("%v: not running its hooks", err)
			cmds = nil
		}
	}
	var spec *serveSpec
	mcp, ok, err := fileAt(o.Repo, rev, release.PluginMCP)
	switch {
	case err != nil:
		return nil, nil, err
	case !ok:
		note("%s is absent: not running its MCP server", release.PluginMCP)
	default:
		cmd, args, env, merr := release.MCPServer([]byte(mcp))
		switch {
		case merr != nil:
			note("%v: not running its MCP server", merr)
		case !release.AcceptedMCPCommand(cmd):
			note("%s launches %q, not mash or %s: not running its MCP server", release.PluginMCP, cmd, release.LauncherCommand)
		default:
			spec = &serveSpec{Args: args, Env: env}
			if release.IsLauncherCommand(cmd) {
				if spec.Root, err = materializePlugin(o.Repo, rev, filepath.Join(work, "prev-plugin")); err != nil {
					return nil, nil, err
				}
			}
		}
	}
	root := spec.rootOrEmpty()
	if root == "" && needsPluginRoot(cmds, "mash") {
		if root, err = materializePlugin(o.Repo, rev, filepath.Join(work, "prev-plugin")); err != nil {
			return nil, nil, err
		}
	}
	if len(cmds) == 0 && spec == nil {
		note("nothing to run against the candidate")
		return nil, spec, nil
	}
	fmt.Fprintf(o.Out, "note  running the previous release's plugin (%s) against the candidate\n", prev)
	return runPluginAgainst(ctx, cbin, label, true, false, cmds, spec, root), spec, nil
}

// binaries are the staged mash binaries (named mash in their own directories) the plugin step builds and downloads, and
// what the latency step reads from the plugin's .mcp.json; both steps share them so neither is built twice.
type binaries struct {
	work    string // removed by close
	cand    string // the candidate's binary; empty until built
	prev    string // the previous release's binary; empty when there is none or it was not fetched
	prevTag string
	hasPrev bool // a previous release exists, whether or not its binary was fetched
	mcpArgs []string
	mcpEnv  map[string]string
	mcpRoot string // the candidate's plugin directory when its MCP command is the launcher; empty otherwise
	// prevSpec is how the previous release's plugin launches serve (its .mcp.json at its tag); nil when there is no
	// previous release or its .mcp.json could not be read (the plugin step said why).
	prevSpec *serveSpec
}

func (b *binaries) close() {
	if b.work != "" {
		os.RemoveAll(b.work)
	}
}

// stageMash copies the binary at src to work/dir/mash, the name the plugin's commands find on PATH.
func stageMash(work, dir, src string) (string, error) {
	b, err := os.ReadFile(src)
	if err != nil {
		return "", op(err)
	}
	d := filepath.Join(work, dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", op(err)
	}
	bin := filepath.Join(d, "mash")
	if err := os.WriteFile(bin, b, 0o755); err != nil {
		return "", op(err)
	}
	return bin, nil
}

// runPluginAgainst runs every hook command and the MCP command against bin (named mash, alone on PATH). Every hook
// command must exit 0 with empty stdout and leave the hook log empty, since the shipped hook command swallows `mash
// hook`'s failures into it; noHook (a release older than release.HookSince) may log `unknown command "hook"` and nothing
// else. strict (the candidate) also demands that `mash hook` itself exits 0 with nothing on either stream. The MCP
// command must answer an MCP initialize (mcpHandshake), launched with the plugin's env block; a nil mcp runs no MCP command. A failure to start anything is an op error, alone.
func runPluginAgainst(ctx context.Context, bin, label string, strict, noHook bool, hooks []release.HookCommand, mcp *serveSpec, pluginRoot string) []error {
	home, err := os.MkdirTemp("", "release-check-home-")
	if err != nil {
		return []error{op(err)}
	}
	defer os.RemoveAll(home)
	state := filepath.Join(home, "state")
	env := []string{"PATH=" + filepath.Dir(bin) + ":/usr/bin:/bin", "HOME=" + home, "XDG_STATE_HOME=" + state}
	if pluginRoot != "" { // the launcher form: Claude Code sets CLAUDE_PLUGIN_ROOT, and the gate points the launcher at the binary under test
		env = append(env, "CLAUDE_PLUGIN_ROOT="+pluginRoot, "MASH_BIN="+bin)
	}
	logPath := filepath.Join(state, "mash", "hook.log")
	var problems []error
	for _, h := range hooks {
		stdout, stderr, code, err := runBounded(ctx, "sh", []string{"-c", h.Command}, env, hookInputs[h.Event])
		if err != nil {
			return []error{op(err)}
		}
		if code != 0 || stdout != "" {
			problems = append(problems, fmt.Errorf("the %s hook %q ran against %s exits %d with stdout %q (stderr %q): it must exit 0 and print nothing", h.Event, h.Command, label, code, stdout, stderr))
		}
		log, _ := os.ReadFile(logPath)
		os.Remove(logPath)
		if len(log) != 0 && !(noHook && onlyUnknownHook(string(log))) {
			problems = append(problems, fmt.Errorf("the %s hook %q ran against %s left %q in its hook log: `mash hook` must not fail", h.Event, h.Command, label, log))
		}
		if strict && release.IsMashHook(h.Command) { // not the launcher's other subcommands (SessionStart __prefetch)
			stdout, stderr, code, err := runBounded(ctx, bin, []string{"hook"}, env, hookInputs[h.Event])
			if err != nil {
				return []error{op(err)}
			}
			if code != 0 || stdout != "" || stderr != "" {
				problems = append(problems, fmt.Errorf("`mash hook` of %s fed a %s input exits %d with stdout %q and stderr %q: it must exit 0 and print nothing", label, h.Event, code, stdout, stderr))
			}
		}
	}
	if mcp == nil { // no MCP server to run (a previous plugin without a usable .mcp.json)
		return problems
	}
	if err := mcpHandshake(ctx, bin, *mcp, label); err != nil {
		var oe opError
		if errors.As(err, &oe) {
			return []error{err}
		}
		problems = append(problems, err)
	}
	return problems
}

// runBounded runs cmd with exactly env and stdin, under the version deadline. A deadline is reported as code -1 with
// no error (a server that keeps running); an error is only a failure to start it.
func runBounded(ctx context.Context, name string, args, env []string, stdin string) (stdout, stderr string, code int, err error) {
	runCtx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	c := exec.CommandContext(runCtx, name, args...)
	c.Env = env
	c.WaitDelay = 2 * time.Second
	c.Stdin = strings.NewReader(stdin) // empty: closed at once
	var o, e bytes.Buffer
	c.Stdout, c.Stderr = &o, &e
	switch err := c.Run(); {
	case err == nil:
		return o.String(), e.String(), 0, nil
	case runCtx.Err() != nil && ctx.Err() == nil:
		return o.String(), e.String(), -1, nil
	default:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return o.String(), e.String(), ee.ExitCode(), nil
		}
		return "", "", 0, fmt.Errorf("starting %s: %w", name, err)
	}
}

// onlyUnknownHook reports whether every non-blank line of a hook log is a pre-`hook` mash's refusal of the command.
func onlyUnknownHook(log string) bool {
	for _, l := range strings.Split(log, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.Contains(l, `unknown command "hook"`) {
			return false
		}
	}
	return true
}

const mcpInitialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"release-check","version":"1"}}}` + "\n"

// mcpHandshake starts bin with spec.Args (serve, inert: no MASH_NAME) under a minimal environment plus the plugin's env
// block (which cannot replace PATH or HOME, the isolation), sends an MCP initialize
// and requires a valid answer within the deadline. A failure to start it is an op error; anything else is a finding.
func mcpHandshake(ctx context.Context, bin string, spec serveSpec, label string) error {
	args := spec.Args
	home, err := os.MkdirTemp("", "release-check-mcp-")
	if err != nil {
		return op(err)
	}
	defer os.RemoveAll(home)
	runCtx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	name0, extra := spec.launch(bin)
	c := exec.CommandContext(runCtx, name0, args...)
	c.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home}
	c.Env = append(c.Env, spec.envPairs()...)
	c.Env = append(c.Env, extra...)
	c.WaitDelay = 2 * time.Second
	in, err := c.StdinPipe()
	if err != nil {
		return op(err)
	}
	out, err := c.StdoutPipe()
	if err != nil {
		return op(err)
	}
	if err := c.Start(); err != nil {
		return op(fmt.Errorf("starting %s: %w", bin, err))
	}
	defer func() { cancel(); c.Wait() }()
	io.WriteString(in, mcpInitialize) // a server that already exited is the "no response" below
	line, rerr := bufio.NewReader(out).ReadString('\n')
	in.Close()
	name := "the MCP command mash " + strings.Join(args, " ")
	if strings.TrimSpace(line) == "" {
		return fmt.Errorf("%s of %s sent no response to an MCP initialize (%v)", name, label, rerr)
	}
	if err := checkInitializeResponse(line); err != nil {
		return fmt.Errorf("%s of %s: %w", name, label, err)
	}
	return nil
}

// checkInitializeResponse requires one JSON-RPC line answering initialize id 1 from a server named mash.
func checkInitializeResponse(line string) error {
	var r struct {
		ID     json.RawMessage `json:"id"`
		Error  json.RawMessage `json:"error"`
		Result *struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &r); err != nil {
		return fmt.Errorf("answered with bad JSON %q: %v", strings.TrimSpace(line), err)
	}
	switch {
	case string(r.ID) != "1":
		return fmt.Errorf("answered id %s, not 1", r.ID)
	case len(r.Error) != 0 && string(r.Error) != "null":
		return fmt.Errorf("answered initialize with an error: %s", r.Error)
	case r.Result == nil || r.Result.ProtocolVersion == "":
		return fmt.Errorf("answered without a result carrying a protocolVersion")
	case r.Result.ServerInfo.Name != "mash":
		return fmt.Errorf("answered as server %q, not mash", r.Result.ServerInfo.Name)
	}
	return nil
}

// renderContractPath is the golden file pinning render.One and render.Line, which a downstream parser (for example another program) may depend on.
const renderContractPath = "internal/render/testdata/contract.txt"

// renderContractPrefix starts the subject of a commit that calls a render-contract change out; the subject lands in
// the release notes (release.yml builds them from the commit subjects).
const renderContractPrefix = "render-contract:"

// fileAt is path's content at rev, and whether rev has it; an error only when git could not say.
func fileAt(repo, rev, path string) (string, bool, error) {
	listed, err := git(repo, "ls-tree", "--name-only", rev, "--", path)
	if err != nil {
		return "", false, op(err)
	}
	if listed == "" {
		return "", false, nil
	}
	cmd := exec.Command("git", "-C", repo, "show", rev+":"+path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output() // untrimmed: the contract is compared byte for byte
	if err != nil {
		return "", false, op(fmt.Errorf("git show %s:%s: %w: %s", rev, path, err, strings.TrimSpace(stderr.String())))
	}
	return string(out), true, nil
}

// prevTagRef is where the previous release's tag is fetched to, for reading its files.
const prevTagRef = "refs/mash/release-check/prev-tag"

// fetchPrevTag fetches the tag from the release repository into prevTagRef (the checkout holds main only) and returns
// the ref; a tag already fetched by this check (Options.prevFetched) is not fetched again.
func fetchPrevTag(o Options, tag string) (string, error) {
	if o.prevFetched != nil && *o.prevFetched == tag {
		return prevTagRef, nil
	}
	if _, err := git(o.Repo, "fetch", "-q", "--no-tags", o.Remote, "+refs/tags/"+tag+":"+prevTagRef); err != nil {
		return "", op(err)
	}
	if o.prevFetched != nil {
		*o.prevFetched = tag
	}
	return prevTagRef, nil
}

// renderContract refuses a candidate whose render contract differs from the previous release's unless a commit since
// then calls the change out.
func renderContract(o Options, x string) error {
	cur, ok, err := fileAt(o.Repo, x, renderContractPath)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s is missing at the candidate", renderContractPath)
	}
	tags, err := remoteTags(o)
	if err != nil {
		return err
	}
	prevTag, hasPrev := release.PreviousTag(tags, o.Version)
	var prev *string
	var callouts []string
	if hasPrev {
		// The tag may not be in the checkout (main only is fetched): fetch it from the release repository.
		ref, err := fetchPrevTag(o, prevTag)
		if err != nil {
			return err
		}
		pc, ok, err := fileAt(o.Repo, ref+"^{commit}", renderContractPath)
		if err != nil {
			return err
		}
		if ok {
			prev = &pc
		}
		if prev != nil && pc != cur {
			log, err := git(o.Repo, "log", "--no-merges", "--format=%h %s", ref+"^{commit}.."+x)
			if err != nil {
				return op(err)
			}
			callouts = renderContractCallouts(strings.Split(log, "\n"))
		}
	}
	note, err := contractVerdict(&cur, prev, hasPrev, prevTag, callouts)
	if note != "" {
		fmt.Fprintf(o.Out, "note  %s\n", note)
	}
	return err
}

// renderContractCallouts keeps the "<hash> <subject>" lines whose subject starts with render-contract:.
func renderContractCallouts(lines []string) []string {
	var out []string
	for _, l := range lines {
		if _, subject, ok := strings.Cut(l, " "); ok && strings.HasPrefix(subject, renderContractPrefix) {
			out = append(out, l)
		}
	}
	return out
}

// contractVerdict decides the render-contract step. atX and atPrev are the file's content at the candidate and at the
// previous release (nil: absent); callouts are the "<hash> <subject>" lines of commits since the previous release
// whose subject starts with render-contract:. A note is for the gate's output; an error is a refusal.
func contractVerdict(atX, atPrev *string, hasPrev bool, prevTag string, callouts []string) (note string, err error) {
	switch {
	case atX == nil:
		return "", fmt.Errorf("%s is missing at the candidate", renderContractPath)
	case !hasPrev:
		return "no previous release: the render contract at the candidate is the first", nil
	case atPrev == nil:
		return fmt.Sprintf("%s has no %s: the render contract at the candidate is the first", prevTag, renderContractPath), nil
	case *atX == *atPrev:
		return "", nil
	case len(callouts) > 0:
		return fmt.Sprintf("the render contract changed since %s and is called out by: %s", prevTag, strings.Join(callouts, "; ")), nil
	}
	return "", fmt.Errorf("the render contract (%s) changed since %s and no commit since then has a subject starting with %q: "+
		"add a %s commit describing the change (it lands in the release notes) and tell downstream parsers "+
		"before releasing", renderContractPath, prevTag, renderContractPrefix, renderContractPrefix)
}

// needsPluginRoot is whether the plugin runs files of its own (the launcher script) and so must be materialized.
func needsPluginRoot(hooks []release.HookCommand, mcpCmd string) bool {
	if release.IsLauncherCommand(mcpCmd) {
		return true
	}
	for _, h := range hooks {
		if strings.Contains(h.Command, "CLAUDE_PLUGIN_ROOT") {
			return true
		}
	}
	return false
}

// materializePlugin writes the plugin/ tree of rev to dir and returns the plugin root (dir/plugin), as Claude Code
// installs it; the launcher script is made executable, which is how Claude Code runs it.
func materializePlugin(repo, rev, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", op(err)
	}
	archive := exec.Command("git", "-C", repo, "archive", "--format=tar", rev, "plugin")
	var stderr bytes.Buffer
	archive.Stderr = &stderr
	tarCmd := exec.Command("tar", "-x", "-C", dir)
	pipe, err := archive.StdoutPipe()
	if err != nil {
		return "", op(err)
	}
	tarCmd.Stdin = pipe
	var tarErr bytes.Buffer
	tarCmd.Stderr = &tarErr
	if err := tarCmd.Start(); err != nil {
		return "", op(err)
	}
	if err := archive.Start(); err != nil {
		tarCmd.Process.Kill()
		tarCmd.Wait()
		return "", op(err)
	}
	aerr := archive.Wait()
	terr := tarCmd.Wait()
	if aerr != nil {
		return "", op(fmt.Errorf("git archive %s plugin: %w: %s", rev, aerr, strings.TrimSpace(stderr.String())))
	}
	if terr != nil {
		return "", op(fmt.Errorf("extracting the plugin of %s: %w: %s", rev, terr, strings.TrimSpace(tarErr.String())))
	}
	root := filepath.Join(dir, "plugin")
	if err := os.Chmod(filepath.Join(root, "scripts", "mash"), 0o755); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", op(err)
	}
	return root, nil
}
