package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/mint"
	"github.com/bogworks/mash/internal/policy"
	"github.com/bogworks/mash/internal/policyfile"
)

const reissueUsage = `usage: mashctl reissue NAME... [--policy FILE]
       mashctl reissue --changed [--policy FILE]
`

// runReissue is `mashctl reissue`. It is offline: the directory record is unchanged (the user key is kept), so it
// needs the policy file, the key directory and the creds files, and no server.
func runReissue(args []string, env func(string) string, stdout, stderr io.Writer) int {
	var names []string
	var file string
	changed := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--changed":
			changed = true
		case a == "--policy" && i+1 < len(args):
			i++
			file = args[i]
		case strings.HasPrefix(a, "--policy=") && len(a) > len("--policy="):
			file = strings.TrimPrefix(a, "--policy=")
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "mashctl: reissue: unknown option %q\n%s", a, reissueUsage)
			return 2
		default:
			names = append(names, a)
		}
	}
	if changed == (len(names) > 0) { // exactly one of NAME... and --changed
		fmt.Fprint(stderr, reissueUsage)
		return 2
	}
	cfg, err := loadCtlConfig(env, false)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	path, err := policyPath(file, env, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: reissue: no policy file: use --policy FILE or MASH_POLICY (%v)\n", err)
		return 1
	}
	keys, err := openKeys(cfg, env)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: reissue: %v\n", err)
		return 1
	}
	doc, err := policyfile.Load(path)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: reissue: %v\n", err)
		return 1
	}
	data, _ := doc.Bytes()
	doc.Close() // nothing is written back
	_, c, err := compileBytes(data, cfg.Owner)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: reissue: %s: %v\n", path, err)
		return 1
	}

	if changed {
		for _, e := range c.Endpoints {
			stale, err := staleCreds(keys, cfg, e)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				fmt.Fprintf(stderr, "mashctl: reissue: skipped %s: no creds file here\n", e.Name)
			case err != nil:
				fmt.Fprintf(stderr, "mashctl: reissue %s: %v\n", e.Name, err)
				return 1
			case stale:
				names = append(names, e.Name)
			}
		}
		if len(names) == 0 {
			fmt.Fprintln(stdout, "nothing to reissue")
			return 0
		}
	}
	code := 0
	for _, name := range names {
		tags, err := reissueOne(keys, cfg, c, name)
		if err != nil {
			fmt.Fprintf(stderr, "mashctl: reissue %s: %v\n", name, err)
			code = 1
			continue
		}
		if len(tags) == 0 {
			tags = []string{"none"}
		}
		fmt.Fprintf(stdout, "reissued %s (tags: %s)\n", name, strings.Join(tags, ", "))
	}
	return code
}

// readCreds reads name's creds file at path, refusing one other users can read.
func readCreds(path string) ([]byte, error) {
	if err := config.CheckCredsMode(path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// staleCreds reports whether e's creds file holds a JWT that differs from what a mint now would issue: other tags, a
// different issuer than the zone key's, or a different issuer account. A missing file wraps fs.ErrNotExist.
func staleCreds(keys mint.Keys, cfg config.Config, e policy.CompiledEndpoint) (bool, error) {
	p, err := credsFileFor(cfg, e.Name)
	if err != nil {
		return false, err
	}
	raw, err := readCreds(p)
	if err != nil {
		return false, err
	}
	token, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		return false, fmt.Errorf("%s: %w", p, err)
	}
	uc, err := jwt.DecodeUserClaims(token)
	if err != nil {
		return false, fmt.Errorf("%s: %w", p, err)
	}
	zkp, err := keys.ZoneKey(e.Zone, false)
	if err != nil {
		return false, err
	}
	zpub, err := zkp.PublicKey()
	zkp.Wipe()
	if err != nil {
		return false, err
	}
	akp, apub, err := keys.Account()
	if err != nil {
		return false, err
	}
	akp.Wipe()
	have := slices.Clone([]string(uc.Tags))
	slices.Sort(have)
	return !slices.Equal(have, e.Tags) || uc.Issuer != zpub || uc.IssuerAccount != apub, nil
}

// reissueOne mints name's user again from the creds file's user key and replaces the file atomically.
func reissueOne(keys mint.Keys, cfg config.Config, c *policy.Compiled, name string) ([]string, error) {
	if _, ok := findEndpoint(c, name); !ok {
		return nil, fmt.Errorf("endpoint %q is not declared in the policy file", name)
	}
	p, err := credsFileFor(cfg, name)
	if err != nil {
		return nil, err
	}
	raw, err := readCreds(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("no creds file for %s: retire and add it again", name)
	}
	if err != nil {
		return nil, err
	}
	kp, err := jwt.ParseDecoratedNKey(raw)
	clear(raw)
	if err != nil {
		return nil, fmt.Errorf("%s holds no user seed: retire and add it again", p)
	}
	defer kp.Wipe()
	seed, err := kp.Seed()
	if err != nil {
		return nil, fmt.Errorf("%s holds no user seed: retire and add it again", p)
	}
	seed = append([]byte(nil), seed...)
	defer clear(seed)
	m, err := mint.Mint(keys, c, name, seed)
	if err != nil {
		return nil, err
	}
	defer clear(m.Creds)
	if err := replaceCreds(p, m.Creds); err != nil {
		return nil, err
	}
	return m.Tags, nil
}

// replaceCreds writes creds to a 0600 temp file beside path and renames it over path.
func replaceCreds(path string, creds []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(creds); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
