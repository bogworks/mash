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

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/mint"
	"github.com/bogworks/mash/internal/policy"
	"github.com/bogworks/mash/internal/policyfile"
)

const reissueUsage = `usage: mashctl reissue NAME... [--policy FILE]
       mashctl reissue --changed [--policy FILE]
`

// runReissue is `mashctl reissue`. Every reissue signs with the creds file's stored user seed, so it first connects
// with the admin credentials and reads the endpoint's directory record: a seed whose name has no record (retired) or
// whose record names another user key is never reissued. A reissue that only adds allow tags then keeps the user key, unless the account JWT revokes that key (it then rotates, and
// if that cannot be read it refuses). A creds file issued for another account is refused. A
// reissue that narrows what the creds file's JWT carries (a tag removed or a deny tag added, another signer or name) rotates the
// user key instead, because a user JWT never expires and the old, wider one would stay valid: it revokes the old key
// (system account credentials, as retire), then points the record at the new key, then replaces the file.
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
			fmt.Fprintf(stderr, "mashctl: reissue: unknown %s\n%s", config.RejectedArg(a), reissueUsage)
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
	doc.Close()                                // nothing is written back
	f, c, err := compileBytes(data, cfg.Owner) // Parse runs Check: a file that does not check is refused here
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: reissue: %s: %v\n", path, err)
		return 1
	}
	// The file's own tests gate the mint exactly as they gate apply: no creds file is read or rewritten first.
	if failed, total := policyTestFailures(f); len(failed) > 0 {
		fmt.Fprintf(stderr, "mashctl: reissue: %s\n", refuseFailedTests(failed, total, path, "nothing was changed, no creds file was rewritten", stdout))
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
	warned := false
	for _, name := range names {
		if ep, ok := findEndpoint(c, name); ok {
			// Measure this endpoint alone: Budget reports only the worst user.
			one := *c
			one.Endpoints = []policy.CompiledEndpoint{ep}
			rep, berr := policy.Budget(&one)
			if berr != nil && rep.WorstUserJWTBytes > rep.UserLimit {
				fmt.Fprintf(stderr, "mashctl: reissue %s: its user JWT would be %d bytes with %d tags, over the %d byte limit of the connect line; nothing was changed\n",
					name, rep.WorstUserJWTBytes, len(ep.Tags), rep.UserLimit)
				code = 1
				continue
			}
			if w := budgetWarning(rep); w != "" && berr == nil && !warned {
				fmt.Fprintln(stderr, w)
				warned = true
			}
		}
		tags, rotated, err := reissueOne(keys, cfg, env, c, name, stdout)
		if err != nil {
			fmt.Fprintf(stderr, "mashctl: reissue %s: %v\n", name, err)
			code = 1
			continue
		}
		if len(tags) == 0 {
			tags = []string{"none"}
		}
		if rotated {
			fmt.Fprintf(stdout, "reissued %s (tags: %s; new user key, old key revoked)\n", name, strings.Join(tags, ", "))
		} else {
			fmt.Fprintf(stdout, "reissued %s (tags: %s)\n", name, strings.Join(tags, ", "))
		}
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

// credsDiff is how a creds file's JWT differs from what a mint now would issue for the endpoint.
type credsDiff struct {
	TagsAdd, TagsRemove []string // tags the policy now gives the endpoint that the JWT lacks; tags the JWT carries that it no longer has
	WrongSigner         bool     // the issuer is not the zone key's public key
	Issuer, ZoneKey     string   // the JWT's issuer and the zone key it should have (public keys)
	WrongAccount        bool     // the issuer account is not the account's key
	WrongName           bool     // the JWT's name is not the endpoint's
}

func (d credsDiff) stale() bool {
	return len(d.TagsAdd) > 0 || len(d.TagsRemove) > 0 || d.WrongSigner || d.WrongAccount || d.WrongName
}

// text is the difference in words, "" when there is none.
func (d credsDiff) text() string {
	var parts []string
	if len(d.TagsAdd) > 0 {
		parts = append(parts, "tags to add: "+strings.Join(d.TagsAdd, ", "))
	}
	if len(d.TagsRemove) > 0 {
		parts = append(parts, "tags to remove: "+strings.Join(d.TagsRemove, ", "))
	}
	if d.WrongSigner {
		parts = append(parts, fmt.Sprintf("signed by %s, not the zone key %s", d.Issuer, d.ZoneKey))
	}
	if d.WrongAccount {
		parts = append(parts, "issued for another account")
	}
	if d.WrongName {
		parts = append(parts, "carries another name")
	}
	return strings.Join(parts, "; ")
}

// staleCreds reports whether e's creds file holds a JWT that differs from what a mint now would issue: other tags, a
// different issuer than the zone key's, a different issuer account or another name. A missing file wraps fs.ErrNotExist.
func staleCreds(keys mint.Keys, cfg config.Config, e policy.CompiledEndpoint) (bool, error) {
	p, err := credsFileFor(cfg, e.Name)
	if err != nil {
		return false, err
	}
	d, err := compareCreds(keys, p, e)
	return d.stale(), err
}

// compareCreds is the comparison behind `reissue --changed` and `policy drift` for the creds file at p.
func compareCreds(keys mint.Keys, p string, e policy.CompiledEndpoint) (credsDiff, error) {
	var d credsDiff
	raw, err := readCreds(p)
	if err != nil {
		return d, err
	}
	token, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		return d, fmt.Errorf("%s: %w", p, err)
	}
	uc, err := jwt.DecodeUserClaims(token)
	if err != nil {
		return d, fmt.Errorf("%s: %w", p, err)
	}
	zkp, err := keys.ZoneKey(e.Zone, false)
	if err != nil {
		return d, err
	}
	zpub, err := zkp.PublicKey()
	zkp.Wipe()
	if err != nil {
		return d, err
	}
	akp, apub, err := keys.Account()
	if err != nil {
		return d, err
	}
	akp.Wipe()
	have := slices.Clone([]string(uc.Tags))
	slices.Sort(have)
	for _, t := range e.Tags {
		if !slices.Contains(have, t) {
			d.TagsAdd = append(d.TagsAdd, t)
		}
	}
	for _, t := range slices.Compact(have) {
		if !slices.Contains(e.Tags, t) {
			d.TagsRemove = append(d.TagsRemove, t)
		}
	}
	if !slices.Equal(have, e.Tags) && len(d.TagsAdd) == 0 && len(d.TagsRemove) == 0 {
		d.TagsRemove = []string{"(a repeated tag)"} // only a repeated tag differs: still stale
	}
	d.Issuer, d.ZoneKey = uc.Issuer, zpub
	d.WrongSigner = uc.Issuer != zpub
	d.WrongAccount = uc.IssuerAccount != apub
	d.WrongName = uc.Name != e.Name
	return d, nil
}

// rotates reports whether the difference narrows what the old JWT grants (anything but added allow tags), so that the
// old JWT must stop working and the user key must change. An added deny tag narrows (the old JWT lacks it), except the
// placeholder admin.NoDenyTag, which addresses nothing.
func (d credsDiff) rotates() bool {
	for _, t := range d.TagsAdd {
		if strings.HasPrefix(t, admin.DenyTag+":") && t != admin.NoDenyTag {
			return true
		}
	}
	return len(d.TagsRemove) > 0 || d.WrongSigner || d.WrongAccount || d.WrongName
}

// reissueOne mints name's user again and replaces the creds file atomically. Before anything is minted it checks that
// the creds file's seed is the one the directory record names (see runReissue). It keeps the creds file's user key
// when the policy only adds allow tags and the account JWT does not revoke that key (keyRevoked; if that cannot be
// established it refuses). Otherwise (rotated) it mints a fresh user key in memory, has the old key revoked
// (confirmed), points the directory record at the new key and only then replaces the file.
func reissueOne(keys mint.Keys, cfg config.Config, env func(string) string, c *policy.Compiled, name string, stdout io.Writer) (tags []string, rotated bool, err error) {
	ep, ok := findEndpoint(c, name)
	if !ok {
		return nil, false, fmt.Errorf("endpoint %q is not declared in the policy file", name)
	}
	p, err := credsFileFor(cfg, name)
	if err != nil {
		return nil, false, err
	}
	raw, err := readCreds(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, fmt.Errorf("no creds file for %s: retire and add it again", name)
	}
	if err != nil {
		return nil, false, err
	}
	kp, err := jwt.ParseDecoratedNKey(raw)
	if err != nil {
		clear(raw)
		return nil, false, fmt.Errorf("%s holds no user seed: retire and add it again", p)
	}
	defer kp.Wipe()
	seed, err := kp.Seed()
	if err != nil {
		clear(raw)
		return nil, false, fmt.Errorf("%s holds no user seed: retire and add it again", p)
	}
	seed = append([]byte(nil), seed...)
	defer clear(seed)
	seedPub, err := kp.PublicKey()
	if err != nil {
		clear(raw)
		return nil, false, fmt.Errorf("%s holds no user seed: retire and add it again", p)
	}
	token, terr := jwt.ParseDecoratedJWT(raw)
	clear(raw)
	var oldClaims *jwt.UserClaims
	if terr == nil {
		oldClaims, terr = jwt.DecodeUserClaims(token)
	}
	if terr != nil {
		return nil, false, fmt.Errorf("%s: %w", p, terr)
	}
	oldPub := oldClaims.Subject
	if oldPub != seedPub {
		return nil, false, fmt.Errorf("%s: its seed (user key %s...) is not the key its JWT names (%s...); not reissued", p, ctl.ShortKey(seedPub), ctl.ShortKey(oldPub))
	}
	d, err := compareCreds(keys, p, ep)
	if err != nil {
		return nil, false, err
	}

	// The record check: the seed signs the new JWT, so it must be the key the directory names for this name.
	akp, accountPub, err := keys.Account()
	if err != nil {
		return nil, false, err
	}
	akp.Wipe()
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return nil, false, fmt.Errorf("a reissue first checks %s's directory record, which needs the server and the admin credentials: %v; nothing was changed", name, err)
	}
	defer closeAdmin()
	recUser, recAccount, err := adm.UserOf(ctx, name)
	if errors.Is(err, admin.ErrNoRecord) {
		return nil, false, fmt.Errorf("%s has no directory record (retired or never added): add it again; a leftover creds file is not reissued", name)
	}
	if err != nil {
		return nil, false, fmt.Errorf("%v; nothing was changed", err)
	}
	if recAccount != accountPub {
		return nil, false, fmt.Errorf("the directory record of %s belongs to account %s..., not this account's key %s...; not reissued", name, ctl.ShortKey(recAccount), ctl.ShortKey(accountPub))
	}
	if recUser != seedPub {
		return nil, false, fmt.Errorf("the directory record of %s names user key %s..., not the creds file's %s...; the file is not the endpoint's current creds, so it is not reissued (retire and add it again)", name, ctl.ShortKey(recUser), ctl.ShortKey(seedPub))
	}

	// The old JWT can be revoked only in this account: a creds file issued for another account is refused.
	if d.WrongAccount {
		return nil, false, fmt.Errorf("%s's creds file was issued for another account (%s...); mashctl can only revoke in this account, so it cannot cut that JWT off: revoke it where it was issued, then retire and add %s again", name, ctl.ShortKey(oldClaims.IssuerAccount), name)
	}

	servers, err := parseServers(env)
	if err != nil {
		return nil, false, fmt.Errorf("%v; nothing was changed", err)
	}
	// A key the account JWT revokes is never re-signed: revocation is by key and time, so a new JWT under it would
	// work again. Not knowing is a refusal, before anything is minted.
	revoked := false
	if !d.rotates() {
		revoked, err = keyRevoked(ctx, cfg, env, servers, oldPub)
		if err != nil {
			return nil, false, fmt.Errorf("cannot tell whether the user key %s... is revoked: %v; nothing was changed", ctl.ShortKey(oldPub), err)
		}
	}
	if !d.rotates() && !revoked {
		m, err := mint.Mint(keys, c, name, seed, false)
		if err != nil {
			return nil, false, err
		}
		defer clear(m.Creds)
		if err := replaceCreds(p, m.Creds); err != nil {
			return nil, false, err
		}
		return m.Tags, false, nil
	}

	// Rotating: (a) a fresh key in memory, (b) the revocation of the old key, (c) the record, (d) the file.
	m, err := mint.Mint(keys, c, name, nil, false)
	if err != nil {
		return nil, false, err
	}
	defer clear(m.Creds)
	if err := revokeUsers(ctx, cfg, env, servers, []string{oldPub}, stdout); err != nil {
		why := "the creds file's JWT is wider than the policy now allows (" + d.text() + ")"
		if revoked {
			why = "the user key is already revoked in the account JWT"
		}
		return nil, false, fmt.Errorf("%s, so its user key must be replaced and the old key revoked, and the revocation was not confirmed (%v); nothing was changed (the old creds still work); run the reissue again", why, err)
	}
	if _, err := adm.SetUser(ctx, name, m.AccountPub, oldPub, m.UserPub); err != nil {
		return nil, false, fmt.Errorf("the old key %s... is revoked, so the endpoint is cut off, but its directory record was not updated (%v); the creds file and the record still agree, so run the reissue again to finish; if that keeps failing, run mashctl retire %s then mashctl add %s", ctl.ShortKey(oldPub), err, name, name)
	}
	if err := replaceCreds(p, m.Creds); err != nil {
		return nil, false, fmt.Errorf("the old key %s... is revoked and the record names the new key, but the new creds file could not be written (%v), so the endpoint is cut off; a plain reissue is now refused because the file and the record disagree: run mashctl retire %s then mashctl add %s", ctl.ShortKey(oldPub), err, name, name)
	}
	return m.Tags, true, nil
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
	if err := ctlkeys.SyncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync %s: %w", filepath.Dir(path), err)
	}
	return nil
}
