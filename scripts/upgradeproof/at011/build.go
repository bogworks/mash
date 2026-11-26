package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bogworks/mash/internal/release"
)

// candidateSet is the candidate release as the proof builds it: the eight binaries, the scripts the release carries,
// and the SHA256SUMS a release would publish for them, all in dir, the directory the mirror serves.
type candidateSet struct {
	dir      string
	hashes   map[release.Binary]string
	ci       map[string]string // script name -> sha256, from the repository at the candidate commit
	sums     string
	sumsSHA  string
	installS string // sha256 of scripts/install.sh at the candidate commit
}

// checkoutSource puts the candidate commit in a fresh git checkout at src: from a pack of its objects (pack), or by
// cloning repo. It stops unless HEAD is the full commit id.
func (r *runner) checkoutSource(src string) error {
	env := r.baseEnv()
	if err := os.MkdirAll(src, 0o755); err != nil {
		return err
	}
	switch {
	case r.cfg.pack != "":
		if res := r.run(env, src, time.Minute, "git", "init", "-q", "."); !res.ok() {
			return fmt.Errorf("git init: %s", res.Out)
		}
		f, err := os.Open(r.cfg.pack)
		if err != nil {
			return err
		}
		defer f.Close()
		r.log.printf("$ git unpack-objects -q < %s", filepath.Base(r.cfg.pack))
		cmd := exec.Command("git", "unpack-objects", "-q")
		cmd.Dir, cmd.Env, cmd.Stdin = src, env, f
		if b, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git unpack-objects: %v: %s", err, b)
		}
	case r.cfg.repo != "":
		if res := r.run(env, filepath.Dir(src), cloneTimeout, "git", "clone", "-q", "--no-checkout", r.cfg.repo, src); !res.ok() {
			return fmt.Errorf("git clone: %s", res.Out)
		}
	default:
		return fmt.Errorf("give -pack or -repo: the candidate source")
	}
	if res := r.run(env, src, checkoutTimeout, "git", "-c", "advice.detachedHead=false", "checkout", "-q", "--detach", r.cfg.commit); !res.ok() {
		return fmt.Errorf("git checkout: %s", res.Out)
	}
	res := r.run(env, src, time.Minute, "git", "rev-parse", "HEAD")
	if strings.TrimSpace(res.Out) != r.cfg.commit {
		return fmt.Errorf("HEAD is %q, not the candidate %s", strings.TrimSpace(res.Out), r.cfg.commit)
	}
	return nil
}

// buildCandidate builds the eight binaries with buildrelease (the release's own build), writes the scripts and the
// SHA256SUMS the release would publish, and returns the set.
func (r *runner) buildCandidate(src, dir string) (*candidateSet, error) {
	env := r.baseEnv()
	tools := filepath.Join(r.work, "build-tools")
	if err := os.MkdirAll(tools, 0o755); err != nil {
		return nil, err
	}
	br := filepath.Join(tools, "buildrelease")
	if res := r.run(env, src, compileTimeout, "go", "build", "-o", br, "./scripts/buildrelease"); !res.ok() {
		return nil, fmt.Errorf("go build buildrelease: %s", res.Out)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	cs := &candidateSet{dir: dir, hashes: map[release.Binary]string{}, ci: map[string]string{}}
	bins := release.Binaries()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []string
	sem := make(chan struct{}, 4)
	for _, b := range bins {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out := filepath.Join(dir, b.AssetName())
			res := r.run(env, src, buildTimeout, br, "-src", src, "-out", out, "-goos", b.GOOS, "-goarch", b.GOARCH,
				"-version", r.cfg.version, "-commit", r.cfg.commit, "-cmd", b.Program)
			mu.Lock()
			defer mu.Unlock()
			if !res.ok() {
				errs = append(errs, b.AssetName()+": "+strings.TrimSpace(res.Out))
				return
			}
			sum, err := fileSHA(out)
			if err != nil {
				errs = append(errs, b.AssetName()+": "+err.Error())
				return
			}
			if f := strings.Fields(res.Out); len(f) == 0 || f[0] != sum {
				errs = append(errs, b.AssetName()+": buildrelease printed a digest that is not the file's")
				return
			}
			cs.hashes[b] = sum
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		return nil, fmt.Errorf("build: %s", strings.Join(errs, "; "))
	}
	for _, n := range release.ScriptNamesFor(r.cfg.version) {
		res := r.run(env, src, time.Minute, "git", "show", r.cfg.commit+":scripts/"+n)
		if !res.ok() {
			return nil, fmt.Errorf("scripts/%s at the candidate: %s", n, res.Out)
		}
		if err := os.WriteFile(filepath.Join(dir, n), []byte(res.Out), 0o755); err != nil {
			return nil, err
		}
		cs.ci[n] = sumBytes([]byte(res.Out))
	}
	cs.installS = cs.ci["install.sh"]
	cs.sums = release.ExpectedSums(r.cfg.version, cs.hashes, cs.ci, false)
	cs.sumsSHA = sumBytes([]byte(cs.sums))
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(cs.sums), 0o644); err != nil {
		return nil, err
	}
	return cs, nil
}
