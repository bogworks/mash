package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// release-check publish: the whole path as one resumable command (docs/decisions/0001). It reads the world, resumes at
// the state it finds, and re-checks every step's precondition, so a rerun is judged exactly as a fresh run.

const (
	defaultWait  = 45 * time.Minute
	pollInterval = 15 * time.Second
)

// Release states, as found through gh.
const (
	relNone      = "none"
	relDraft     = "draft"
	relPublished = "published"
)

// world holds every effect of publish, so the state machine is tested against a fake.
type world interface {
	// RemoteTag looks the tag up in the release repository: whether it exists and the commit it leads to.
	RemoteTag(tag string) (exists bool, commit string, err error)
	// Release is the state of the GitHub release of the tag: relNone, relDraft or relPublished.
	Release(tag string) (string, error)
	// WorkflowFailed reports whether release.yml's latest run for the tag ended without success.
	WorkflowFailed(tag string) (bool, error)
	// PreTag runs the pre-tag gate (check).
	PreTag() error
	// CreateTag makes the signed tag at x and pushes it; it verifies the remote tag leads to x.
	CreateTag(tag, x string) error
	// Assets runs the --assets gate (checkAssets), which is read-only.
	Assets() error
	// Order re-runs the ordering checks of the pre-tag gate (the version is strictly above the latest published
	// release; every earlier release is published or recorded as skipped), without the rest of the gate.
	Order(x string) error
	// Publish turns the draft into a published release.
	Publish(tag string) error
	Now() time.Time
	Sleep(d time.Duration)
}

// publish runs release-check publish for real.
func publish(ctx context.Context, o Options) error {
	if err := o.defaults(); err != nil {
		return err
	}
	x, err := git(o.Repo, "rev-parse", "--verify", o.X+"^{commit}")
	if err != nil {
		return fmt.Errorf("resolving %s: %w", o.X, err)
	}
	if o.Wait <= 0 {
		o.Wait = defaultWait
	}
	return runPublish(o, realWorld{ctx: ctx, o: o, gate: &gateRecord{}}, x)
}

// failed marks an error as the command itself failing (exit 4, resumable) rather than a refusal (exit 3).
func failed(err error) error {
	if err == nil {
		return nil
	}
	var g gateError
	if errors.As(err, &g) {
		return err
	}
	return gateError{err}
}

// stepErr keeps a gate's own classification (a refusal is 3, could not decide is 4) and makes anything else a failure.
func stepErr(err error) error {
	if err == nil {
		return nil
	}
	var oe opError
	if errors.As(err, &oe) {
		return failed(err)
	}
	return err
}

// runPublish is the state machine. x is the resolved candidate commit.
func runPublish(o Options, w world, x string) error {
	out := o.Out
	if out == nil {
		out = io.Discard
	}
	say := func(format string, a ...any) { fmt.Fprintf(out, "publish: "+format+"\n", a...) }
	tag := "v" + o.Version
	wait := o.Wait
	if wait <= 0 {
		wait = defaultWait
	}

	gated := false // the pre-tag gate runs once per run, before the tag or before a draft is published
	exists, at, err := w.RemoteTag(tag)
	if err != nil {
		return failed(err)
	}
	if exists && at != x {
		return fmt.Errorf("refused: tag %s is at %s, not the candidate %s; nothing was changed", tag, at, x)
	}
	if !exists {
		say("no tag %s: running the pre-tag gate", tag)
		// A release without its tag is not a state this command made: refuse rather than guess.
		st, err := w.Release(tag)
		if err != nil {
			return failed(err)
		}
		if st != relNone {
			return fmt.Errorf("refused: %s has a %s release but no tag in the release repository; nothing was changed", tag, st)
		}
		if err := w.PreTag(); err != nil {
			return stepErr(err)
		}
		gated = true
		// The precondition is re-checked right before the tag is made: the remote may have changed during the gate.
		exists, at, err = w.RemoteTag(tag)
		if err != nil {
			return failed(err)
		}
		if exists {
			return fmt.Errorf("refused: tag %s appeared during the gate (at %s); rerun to resume from it", tag, at)
		}
		say("tagging %s at %s", tag, x)
		if err := w.CreateTag(tag, x); err != nil {
			return stepErr(err)
		}
		if exists, at, err = w.RemoteTag(tag); err != nil {
			return failed(err)
		} else if !exists || at != x {
			return fmt.Errorf("refused: the remote tag %s is %q (exists %v), not the candidate %s after pushing", tag, at, exists, x)
		}
	} else {
		say("tag %s is at the candidate", tag)
	}

	st, err := w.Release(tag)
	if err != nil {
		return failed(err)
	}
	if st == relNone {
		say("waiting for release.yml's draft of %s (up to %s)", tag, wait)
		if st, err = waitForDraft(w, tag, wait); err != nil {
			return err
		}
	}

	if st == relDraft {
		// A tag that was pushed by hand is not covered by the gate that precedes tagging: run it before publishing.
		// It judges this version against the others only (earlier tags, published releases), so the tag and draft
		// of this very version do not count against it.
		if !gated {
			say("draft found at an existing tag: running the pre-tag gate")
			if err := w.PreTag(); err != nil {
				return stepErr(err)
			}
		}
		say("running the --assets gate")
		if err := w.Assets(); err != nil {
			return stepErr(err)
		}
		// Re-check before the one irreversible step: the tag is still X and the release is still a draft.
		if exists, at, err = w.RemoteTag(tag); err != nil {
			return failed(err)
		} else if !exists || at != x {
			return fmt.Errorf("refused: tag %s is now %q, not the candidate %s; not publishing", tag, at, x)
		}
		if st, err = w.Release(tag); err != nil {
			return failed(err)
		}
		if st == relDraft {
			// Another version may have been published during the gates: judge the order again, right before the
			// irreversible step. Publishing across versions is not locked; this narrows the window (ADR 0001).
			if err := w.Order(x); err != nil {
				return stepErr(err)
			}
			say("publishing %s", tag)
			if err := w.Publish(tag); err != nil {
				return stepErr(err) // no verifying SHA256SUMS.sig is a refusal (3); gh failing is not
			}
		}
	}

	st, err = w.Release(tag)
	if err != nil {
		return failed(err)
	}
	if st != relPublished {
		return failed(fmt.Errorf("%s is %s after publishing, not published", tag, st))
	}
	say("verifying the published release (read-only)")
	if err := w.Assets(); err != nil {
		return stepErr(err)
	}
	say("%s is published and verified", tag)
	return nil
}

// waitForDraft polls until the release exists, the tag's workflow run failed, or the deadline passes.
func waitForDraft(w world, tag string, wait time.Duration) (string, error) {
	deadline := w.Now().Add(wait)
	for {
		st, err := w.Release(tag)
		if err != nil {
			return "", failed(err)
		}
		if st != relNone {
			return st, nil
		}
		bad, err := w.WorkflowFailed(tag)
		if err != nil {
			return "", failed(err)
		}
		if bad {
			return "", failed(fmt.Errorf("release.yml failed for %s and no draft exists; fix it and rerun to resume", tag))
		}
		if !w.Now().Before(deadline) {
			return "", failed(fmt.Errorf("no draft of %s after %s; rerun to keep waiting", tag, wait))
		}
		w.Sleep(pollInterval)
	}
}

// realWorld is the world through git and gh.
type realWorld struct {
	ctx   context.Context
	o     Options
	gitFn func(dir string, args ...string) (string, error) // nil: git
	gate  *gateRecord                                      // what this run's --assets gate established (shared by value copies)
}

func (r realWorld) git(args ...string) (string, error) {
	if r.gitFn != nil {
		return r.gitFn(r.o.Repo, args...)
	}
	return git(r.o.Repo, args...)
}

// resolveSigning picks the SSH key that signs the release tag (RELEASE_SIGNING_KEY, default ~/.ssh/id_ed25519.pub)
// and the allowed_signers file that verifies it (RELEASE_SIGNERS, default optSigners, the repository's
// docs/allowed_signers). A missing key file is a failure before any tag is made.
func resolveSigning(env func(string) string, home string, exists func(string) bool, optSigners string) (key, signers string, err error) {
	key = env("RELEASE_SIGNING_KEY")
	if key == "" {
		key = filepath.Join(home, ".ssh", "id_ed25519.pub")
	}
	if !exists(key) {
		return "", "", failed(fmt.Errorf("the signing key %s does not exist: set RELEASE_SIGNING_KEY to the release SSH public key", key))
	}
	signers = env("RELEASE_SIGNERS")
	if signers == "" {
		signers = optSigners
	}
	return key, signers, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func (r realWorld) RemoteTag(tag string) (bool, string, error) {
	out, err := git(r.o.Repo, "ls-remote", r.o.Remote, "refs/tags/"+tag)
	if err != nil {
		return false, "", err
	}
	if strings.TrimSpace(out) == "" {
		return false, "", nil
	}
	_, c, err := tagRefs(r.o.Repo, r.o.Remote, tag)
	if err != nil {
		return false, "", err
	}
	return true, c, nil
}

func (r realWorld) Release(tag string) (string, error) {
	b, err := gh(r.o, "release", "view", tag, "--repo", r.o.GHRepo, "--json", "isDraft")
	if err != nil {
		if strings.Contains(err.Error(), "release not found") {
			return relNone, nil
		}
		return "", err
	}
	var v struct {
		IsDraft bool `json:"isDraft"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return "", fmt.Errorf("reading the release of %s: %w", tag, err)
	}
	if v.IsDraft {
		return relDraft, nil
	}
	return relPublished, nil
}

func (r realWorld) WorkflowFailed(tag string) (bool, error) {
	b, err := gh(r.o, "run", "list", "--repo", r.o.GHRepo, "--workflow", "release.yml", "--branch", tag, "--json", "status,conclusion")
	if err != nil {
		return false, err
	}
	return runFailed(b)
}

// runFailed reads `gh run list --json status,conclusion` (newest first): the latest run is complete and not a success.
func runFailed(b []byte) (bool, error) {
	var runs []struct {
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	}
	if err := json.Unmarshal(b, &runs); err != nil {
		return false, fmt.Errorf("reading gh run list: %w", err)
	}
	if len(runs) == 0 {
		return false, nil
	}
	return runs[0].Status == "completed" && runs[0].Conclusion != "success", nil
}

func (r realWorld) PreTag() error { return check(r.ctx, r.o) }

func (r realWorld) CreateTag(tag, x string) error {
	home, _ := os.UserHomeDir()
	key, signers, err := resolveSigning(os.Getenv, home, fileExists, r.o.Signers)
	if err != nil {
		return err
	}
	created := false
	// A local tag from an interrupted run is reused if it is at X; anywhere else it is refused.
	if c, err := r.git("rev-parse", "-q", "--verify", "refs/tags/"+tag+"^{commit}"); err == nil {
		if c != x {
			return fmt.Errorf("a local tag %s exists at %s, not the candidate %s: delete it and rerun", tag, c, x)
		}
	} else if _, err := r.git("-c", "gpg.format=ssh", "-c", "user.signingkey="+key, "tag", "-s", tag, "-m", "mash "+r.o.Version, x); err != nil {
		return failed(err)
	} else {
		created = true
	}
	// Verify before pushing, against the release signers and not this machine's git config.
	if _, err := r.git("-c", "gpg.format=ssh", "-c", "gpg.ssh.allowedSignersFile="+signers, "verify-tag", tag); err != nil {
		msg := fmt.Sprintf("refused: the local tag %s does not verify against %s; not pushing: %v", tag, signers, err)
		if created {
			if _, derr := r.git("tag", "-d", tag); derr != nil {
				msg += fmt.Sprintf(" (and deleting it failed: %v)", derr)
			}
		}
		return errors.New(msg)
	}
	if _, err := r.git("push", r.o.Remote, "refs/tags/"+tag+":refs/tags/"+tag); err != nil {
		return failed(err)
	}
	exists, c, err := r.RemoteTag(tag)
	if err != nil {
		return failed(err)
	}
	if !exists || c != x {
		return fmt.Errorf("the remote tag %s is %q after the push, not %s", tag, c, x)
	}
	return nil
}

func (r realWorld) Assets() error {
	return checkAssetsRecord(r.ctx, r.o, r.gate)
}

func (r realWorld) Order(x string) error {
	if err := aboveLatestPublished(r.o); err != nil {
		return err
	}
	return earlierReleases(r.o, x)
}

func (r realWorld) Publish(tag string) error {
	// The one irreversible step needs a release that carries a SHA256SUMS.sig verifying against the signers file at X,
	// read back from the draft now, whatever the Assets gate before it concluded.
	x, err := git(r.o.Repo, "rev-parse", "--verify", r.o.X+"^{commit}")
	if err != nil {
		return err
	}
	// It also holds every attached asset to the signed sums by the digest GitHub reports now, so a binary changed
	// after the --assets gate is refused; the signature alone would still verify over the old checksums.
	// It is bound to this run's --assets gate: SHA256SUMS must be the bytes that gate compared with fresh builds of X, and
	// every binary must be that gate's build. With no gate result yet in this process the gate runs here, so nothing is
	// ever trusted from an earlier run.
	rec := r.gate
	if !rec.ok() {
		rec = &gateRecord{}
		if err := checkAssetsRecord(r.ctx, r.o, rec); err != nil {
			return err
		}
	}
	if err := verifyPublishable(r.o, tag, x, rec); err != nil {
		return fmt.Errorf("refused: not publishing %s: %w", tag, err)
	}
	_, err = gh(r.o, "release", "edit", tag, "--repo", r.o.GHRepo, "--draft=false")
	return op(err)
}

func (r realWorld) Now() time.Time        { return time.Now() }
func (r realWorld) Sleep(d time.Duration) { time.Sleep(d) }
