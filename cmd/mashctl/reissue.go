package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/mint"
	"github.com/bogworks/mash/internal/policy"
	"github.com/bogworks/mash/internal/policyfile"
	"github.com/bogworks/mash/internal/store"
)

const reissueUsage = `usage: mashctl reissue NAME... [--policy FILE]
       mashctl reissue --changed [--policy FILE]
`

// runReissue is `mashctl reissue`. Every reissue signs with the creds file's stored user seed, so it first connects
// with the admin credentials and reads the endpoint's directory record: a seed whose name has no record (retired) or
// whose record names another user key is never reissued. A reissue of a creds file that already matches (signed by the account key, with
// the endpoint's permissions and name) keeps the user key, unless the account JWT revokes that key (it then rotates, and
// if that cannot be read it refuses). A creds file issued for another account is refused. A
// reissue of a creds file that differs (another signer, such as a zone key from an older release, other permissions or
// another name) rotates the user key instead, because a user JWT never expires and the old one would stay valid: it revokes the old key
// (system account credentials, as retire), then points the record at the new key, then replaces the file. The new creds are
// first staged in <creds>.next, so a rotation interrupted after the revocation is finished by running the reissue again.
// The new creds are staged in <creds>.next before anything irreversible, so an interrupted rotation is finished by running the reissue again.
func runReissue(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
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
	cfg, err := loadCtlConfig(env, lookup, false)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	guard, ok := accountGuard(cfg, "reissue", stderr)
	if !ok {
		return 1
	}
	defer guard.Release()
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

	code := 0
	if changed {
		servers, serversErr := parseServers(env) // read lazily below: only an endpoint that needs the revocation check uses it
		for _, e := range c.Endpoints {
			stale, err := staleCreds(keys, cfg, cfg.Owner, e)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				fmt.Fprintf(stderr, "mashctl: reissue: skipped %s: no creds file here\n", e.Name)
				continue
			case err != nil:
				fmt.Fprintf(stderr, "mashctl: reissue %s: %v\n", e.Name, err)
				return 1
			case stale:
				names = append(names, e.Name)
				continue
			}
			// The JWT matches the policy; the endpoint still needs a reissue when a rotation was interrupted (its staged
			// file is there: the reissue resumes it or refuses a stale one) or when its user key is revoked.
			p, err := credsFileFor(cfg, e.Name)
			if err != nil {
				fmt.Fprintf(stderr, "mashctl: reissue %s: %v\n", e.Name, err)
				return 1
			}
			if _, err := os.Lstat(stagePath(p)); err == nil {
				names = append(names, e.Name)
				continue
			}
			pub, err := credsUserKey(p)
			if err == nil && serversErr != nil {
				err = serversErr
			}
			revoked := false
			if err == nil {
				revoked, err = keyRevoked(context.Background(), cfg, env, servers, pub)
			}
			switch {
			case err != nil:
				fmt.Fprintf(stderr, "mashctl: reissue %s: cannot tell whether its user key is revoked: %v\n", e.Name, err)
				code = 1
			case revoked:
				names = append(names, e.Name)
			}
		}
		if len(names) == 0 {
			if code == 0 {
				fmt.Fprintln(stdout, "nothing to reissue")
			}
			return code
		}
	}
	for _, name := range names {
		rotated, err := reissueOne(guard, keys, cfg, env, c, name, stdout, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "mashctl: reissue %s: %v\n", name, err)
			code = 1
			continue
		}
		if rotated {
			fmt.Fprintf(stdout, "reissued %s (new user key, old key revoked)\n", name)
		} else {
			fmt.Fprintf(stdout, "reissued %s\n", name)
		}
	}
	return code
}

// credsUserKey is the user public key (the JWT's subject) of the creds file at p; no seed is kept.
func credsUserKey(p string) (string, error) {
	raw, err := readCreds(p)
	if err != nil {
		return "", err
	}
	defer clear(raw)
	token, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		return "", fmt.Errorf("%s: %w", p, err)
	}
	uc, err := jwt.DecodeUserClaims(token)
	if err != nil {
		return "", fmt.Errorf("%s: %w", p, err)
	}
	return uc.Subject, nil
}

// credsAccountKey is the public key of the account a creds file's user belongs to: the account the user JWT names as
// its issuing account (a signing key issues on behalf of it), else its issuer.
func credsAccountKey(p string) (string, error) {
	raw, err := readCreds(p)
	if err != nil {
		return "", err
	}
	defer clear(raw)
	token, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		return "", fmt.Errorf("%s: %w", p, err)
	}
	uc, err := jwt.DecodeUserClaims(token)
	if err != nil {
		return "", fmt.Errorf("%s: %w", p, err)
	}
	if uc.IssuerAccount != "" {
		return uc.IssuerAccount, nil
	}
	return uc.Issuer, nil
}

// readCreds reads name's creds file at path, refusing one other users can read.
func readCreds(path string) ([]byte, error) {
	return config.ReadCredsFile(path)
}

// credsDiff is how a creds file's JWT differs from what a mint now would issue for the endpoint.
type credsDiff struct {
	WrongSigner  bool   // the issuer is not the account signing key (a zone key of an older release, or another key)
	Issuer       string // the JWT's issuer (a public key)
	WrongPerms   bool   // the JWT's permissions are not the endpoint's (a scoped user carries none: the key's template applied)
	WrongAccount bool   // the account the JWT was issued for is not the account's key
	AccountPub   string // that account: issuer_account when the JWT has one, otherwise the issuer (a key signing directly is the account)
	WrongName    bool   // the JWT's name is not the endpoint's
	HasTags      bool   // the JWT carries tags, which only a zone key's template read (ADR 0091)
}

func (d credsDiff) stale() bool {
	return d.WrongSigner || d.WrongPerms || d.WrongAccount || d.WrongName || d.HasTags
}

// text is the difference in words, "" when there is none.
func (d credsDiff) text() string {
	var parts []string
	if d.WrongSigner {
		parts = append(parts, fmt.Sprintf("signed by %s, not the account signing key", d.Issuer))
	}
	if d.WrongPerms {
		parts = append(parts, "its permissions are not the endpoint's")
	}
	if d.HasTags {
		parts = append(parts, "it carries tags (a zone key's exceptions)")
	}
	if d.WrongAccount {
		parts = append(parts, "issued for another account")
	}
	if d.WrongName {
		parts = append(parts, "carries another name")
	}
	return strings.Join(parts, "; ")
}

// staleCreds reports whether e's creds file holds a JWT that differs from what a mint now would issue: another signer,
// other permissions, another issuer account or another name. A missing file wraps fs.ErrNotExist.
func staleCreds(keys mint.Keys, cfg config.Config, owner string, e policy.CompiledEndpoint) (bool, error) {
	p, err := credsFileFor(cfg, e.Name)
	if err != nil {
		return false, err
	}
	d, err := compareCreds(keys, p, owner, e)
	return d.stale(), err
}

// compareCreds is the comparison behind `reissue --changed` and `policy drift` for the creds file at p.
func compareCreds(keys mint.Keys, p, owner string, e policy.CompiledEndpoint) (credsDiff, error) {
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
	akp, apub, err := keys.Account()
	if err != nil {
		return d, err
	}
	akp.Wipe()
	d.Issuer = uc.Issuer
	d.WrongSigner = uc.Issuer != apub
	// A JWT the account key signs names no issuer account; a signing key's names the account it signs for. A JWT signed
	// directly by some other account's key also names none, and then its issuer is that account.
	d.AccountPub = uc.IssuerAccount
	if d.AccountPub == "" {
		d.AccountPub = uc.Issuer
	}
	d.WrongAccount = d.AccountPub != apub
	d.WrongName = uc.Name != e.Name
	d.HasTags = len(uc.Tags) > 0
	d.WrongPerms = !samePermissions(uc.Permissions, admin.Permissions(owner, e.Name))
	return d, nil
}

// samePermissions compares two permission sets as sets: the order of a list means nothing.
func samePermissions(a, b jwt.Permissions) bool {
	same := func(x, y jwt.StringList) bool {
		xs, ys := slices.Clone([]string(x)), slices.Clone([]string(y))
		slices.Sort(xs)
		slices.Sort(ys)
		return slices.Equal(slices.Compact(xs), slices.Compact(ys))
	}
	return same(a.Pub.Allow, b.Pub.Allow) && same(a.Pub.Deny, b.Pub.Deny) &&
		same(a.Sub.Allow, b.Sub.Allow) && same(a.Sub.Deny, b.Sub.Deny) &&
		a.Resp == b.Resp
}

// rotates reports whether the difference means the old JWT must stop working and the user key must change: every
// difference does, since the old JWT never expires and would stay valid beside the new one.
func (d credsDiff) rotates() bool { return d.stale() }

// reissueOne mints name's user again and replaces the creds file atomically. Before anything is minted it checks that
// the creds file's seed is the one the directory record names (see runReissue). It keeps the creds file's user key
// when the creds file already matches what a mint issues now and the account JWT does not revoke that key (keyRevoked; if that cannot be
// established it refuses). Otherwise (rotated) it mints a fresh user key and stages its creds in <creds>.next, has the old key revoked
// (confirmed), points the directory record at the new key and only then replaces the file. The record check, the
// revocation check, the mint, the revocation, the record update and the file rewrite all run under the endpoint's name
// lock (admin.WithNameLock), so a concurrent reissue of the same name cannot interleave.
func reissueOne(guard *ctl.Guard, keys mint.Keys, cfg config.Config, env func(string) string, c *policy.Compiled, name string, stdout, stderr io.Writer) (rotated bool, err error) {
	ep, ok := findEndpoint(c, name)
	if !ok {
		return false, fmt.Errorf("endpoint %q is not declared in the policy file", name)
	}
	p, err := credsFileFor(cfg, name)
	if err != nil {
		return false, err
	}
	raw, err := readCreds(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("no creds file for %s: retire and add it again", name)
	}
	if err != nil {
		return false, err
	}
	kp, err := jwt.ParseDecoratedNKey(raw)
	if err != nil {
		clear(raw)
		return false, fmt.Errorf("%s holds no user seed: retire and add it again", p)
	}
	defer kp.Wipe()
	seed, err := kp.Seed()
	if err != nil {
		clear(raw)
		return false, fmt.Errorf("%s holds no user seed: retire and add it again", p)
	}
	seed = append([]byte(nil), seed...)
	defer clear(seed)
	seedPub, err := kp.PublicKey()
	if err != nil {
		clear(raw)
		return false, fmt.Errorf("%s holds no user seed: retire and add it again", p)
	}
	token, terr := jwt.ParseDecoratedJWT(raw)
	clear(raw)
	var oldClaims *jwt.UserClaims
	if terr == nil {
		oldClaims, terr = jwt.DecodeUserClaims(token)
	}
	if terr != nil {
		return false, fmt.Errorf("%s: %w", p, terr)
	}
	oldPub := oldClaims.Subject
	if oldPub != seedPub {
		return false, fmt.Errorf("%s: its seed (user key %s...) is not the key its JWT names (%s...); not reissued", p, ctl.ShortKey(seedPub), ctl.ShortKey(oldPub))
	}
	d, err := compareCreds(keys, p, c.Owner, ep)
	if err != nil {
		return false, err
	}

	// The record check: the seed signs the new JWT, so it must be the key the directory names for this name.
	akp, accountPub, err := keys.Account()
	if err != nil {
		return false, err
	}
	akp.Wipe()
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return false, fmt.Errorf("a reissue first checks %s's directory record, which needs the server and the admin credentials: %v; nothing was changed", name, err)
	}
	defer closeAdmin()
	// The name's lock is held from the record check to the file rewrite (ADR 0058: fn's context ends before the lock
	// expires, and each write re-checks it), so another reissue of the same name cannot rotate the record and write
	// its creds between our check and our write.
	err = adm.WithNameLock(ctx, name, func(ctx context.Context, lock admin.NameLock) error {
		var err error
		rotated, err = reissueLocked(ctx, guard, adm, lock, keys, cfg, env, c, name, ep, p, seed, seedPub, oldPub, oldClaims, accountPub, d, stdout, stderr)
		return err
	})
	if err != nil {
		return false, err
	}
	return rotated, nil
}

// reissueLocked is the part of reissueOne that runs under name's lock.
func reissueLocked(ctx context.Context, guard *ctl.Guard, adm *admin.Admin, lock admin.NameLock, keys mint.Keys, cfg config.Config, env func(string) string, c *policy.Compiled, name string, ep policy.CompiledEndpoint, p string, seed []byte, seedPub, oldPub string, oldClaims *jwt.UserClaims, accountPub string, d credsDiff, stdout, stderr io.Writer) (rotated bool, err error) {
	recUser, recAccount, err := adm.UserOfLocked(ctx, lock, name)
	if errors.Is(err, admin.ErrNoRecord) {
		return false, fmt.Errorf("%s has no directory record (retired or never added): add it again; a leftover creds file is not reissued", name)
	}
	if err != nil {
		return false, fmt.Errorf("%v; nothing was changed", err)
	}
	if recAccount != accountPub {
		return false, fmt.Errorf("the directory record of %s belongs to account %s..., not this account's key %s...; not reissued", name, ctl.ShortKey(recAccount), ctl.ShortKey(accountPub))
	}
	// A rotation that was interrupted left its new creds in the staged file. It is resumable when the record still
	// names the creds file's key (rotation not yet recorded) or already names the staged key (recorded, file not swapped).
	staged, err := readStaged(p, name, accountPub, seedPub)
	if err != nil {
		return false, err
	}
	defer staged.wipe()
	resume := false
	switch {
	case recUser == seedPub:
		resume = staged != nil
	case staged != nil && recUser == staged.pub:
		resume = true
	default:
		return false, fmt.Errorf("the directory record of %s names user key %s..., not the creds file's %s...; the file is not the endpoint's current creds, so it is not reissued (retire and add it again)", name, ctl.ShortKey(recUser), ctl.ShortKey(seedPub))
	}

	// The old JWT can be revoked only in this account: a creds file issued for another account is refused.
	if d.WrongAccount {
		return false, fmt.Errorf("%s's creds file was issued for another account (%s...); mashctl can only revoke in this account, so it cannot cut that JWT off: revoke it where it was issued, then retire and add %s again", name, ctl.ShortKey(d.AccountPub), name)
	}

	servers, err := parseServers(env)
	if err != nil {
		return false, fmt.Errorf("%v; nothing was changed", err)
	}
	// A key the account JWT revokes is never re-signed: revocation is by key and time, so a new JWT under it would
	// work again. Not knowing is a refusal, before anything is minted.
	revoked := false
	if !d.rotates() {
		revoked, err = keyRevoked(ctx, cfg, env, servers, oldPub)
		if err != nil {
			return false, fmt.Errorf("cannot tell whether the user key %s... is revoked: %v; nothing was changed", ctl.ShortKey(oldPub), err)
		}
	}
	recorded := staged != nil && recUser == staged.pub // the record already names the staged key: the rotation must finish
	if !d.rotates() && !revoked && !recorded {
		m, err := mint.Mint(keys, c, name, seed)
		if err != nil {
			return false, err
		}
		defer clear(m.Creds)
		if err := guard.Before("the issue record"); err != nil { // the account lock, before the first write
			return false, fmt.Errorf("%v; the creds file was not rewritten", err)
		}
		if err := adm.RecordIssued(ctx, issuedOf(m, cfg.Owner, store.PurposeIssue)); err != nil {
			return false, fmt.Errorf("%v; the creds file was not rewritten", err)
		}
		if err := guard.Before("the creds file replacement"); err != nil {
			return false, fmt.Errorf("%v; the creds file was not rewritten (the new key is recorded as issued; run mashctl reissue %s again)", err, name)
		}
		if err := lock.Check(ctx); err != nil {
			return false, fmt.Errorf("%v; the creds file was not rewritten", err)
		}
		if err := writeCreds(p, m.Creds); err != nil {
			return false, err
		}
		return false, nil
	}

	// A revocation covers only JWTs issued at or before its time: an old JWT that claims a later issue time (a clock
	// that was ahead or has since been set back) would stay valid, so the rotation would report success without
	// cutting it off. Refused before anything is staged or revoked, on the revoked-key path too.
	if err := refuseFutureIssued(name, oldClaims, reissueNow()); err != nil {
		return false, err
	}

	// Rotating: (a) the new key, staged on disk (a resumed rotation re-signs the staged key with the current policy),
	// (b) the revocation of the old key, (c) the record, (d) the file, which consumes the staged file. Nothing
	// irreversible happens before the new seed is on disk.
	var newSeed []byte
	if resume {
		newSeed = staged.seed
	}
	m, err := mint.Mint(keys, c, name, newSeed)
	if err != nil {
		return false, err
	}
	defer clear(m.Creds)
	// The new key is in the issue record before it is staged or written anywhere: a key that can connect
	// is always recorded. The old key's record stays (revocation is separate; the record is append-only).
	if err := guard.Before("the issue record of the new key"); err != nil { // the account lock, before the first write of the rotation
		return false, fmt.Errorf("%v; nothing was staged or changed", err)
	}
	if err := adm.RecordIssued(ctx, issuedOf(m, cfg.Owner, store.PurposeRotation)); err != nil {
		return false, fmt.Errorf("%v; nothing was staged or changed", err)
	}
	sp := stagePath(p)
	// The lock may have expired while the key was minted: another reissue could hold it and own the staged file now.
	if err := nameLockCheck(ctx, lock); err != nil {
		return false, fmt.Errorf("%v; nothing was staged or changed", err)
	}
	if err := guard.Before("the staging of the new creds"); err != nil {
		return false, fmt.Errorf("%v; the new key is recorded as issued but nothing was staged, and the old creds and key are unchanged; run mashctl reissue %s again", err, name)
	}
	if err := stageCreds(p, m.Creds); err != nil {
		return false, fmt.Errorf("the new creds could not be staged in %s (%v); nothing was changed", sp, err)
	}
	if err := guard.Before("the revocation of the old user key"); err != nil {
		return false, fmt.Errorf("%v; the new key is recorded as issued and its creds are staged in %s, but the old key %s... is not revoked, the directory record and the old creds file are unchanged (the old creds still work); run mashctl reissue %s again", err, sp, ctl.ShortKey(oldPub), name)
	}
	if err := revokeUsers(ctx, cfg, env, servers, []string{oldPub}, stdout, nil); err != nil {
		why := "the creds file's JWT is wider than the policy now allows (" + d.text() + ")"
		if revoked {
			why = "the user key is already revoked in the account JWT"
		}
		return false, fmt.Errorf("%s, so its user key must be replaced and the old key revoked, and the revocation was not confirmed (%v); the directory record and the creds file are unchanged (the old creds still work) and the new key is staged in %s; run the reissue again", why, err, sp)
	}
	finish := fmt.Sprintf("the old key %s... is revoked, so the endpoint is cut off; the rotation is staged in %s, and running mashctl reissue %s again finishes it", ctl.ShortKey(oldPub), sp, name)
	if err := guard.Before("the record of the revocation"); err != nil { // the old key is revoked: the staged rotation is finished by a later run
		return false, fmt.Errorf("%s: the revocation was not recorded in the issue record and the directory record was not updated (%v)", finish, err)
	}
	ctl.MarkRevoked(ctx, adm, oldPub, reissueNow(), stderr)
	if err := guard.Before("the directory record update"); err != nil {
		return false, fmt.Errorf("%s: its directory record was not updated (%v)", finish, err)
	}
	if _, err := adm.SetUserLocked(ctx, lock, name, m.AccountPub, oldPub, m.UserPub); err != nil {
		return false, fmt.Errorf("%s: its directory record was not updated (%v)", finish, err)
	}
	if err := lock.Check(ctx); err != nil {
		return false, fmt.Errorf("%s: the record names the new key, but %v", finish, err)
	}
	if err := guard.Before("the creds file replacement"); err != nil {
		return false, fmt.Errorf("%s: the record names the new key, but %v", finish, err)
	}
	if err := writeCreds(p, m.Creds); err != nil {
		return false, fmt.Errorf("%s: the new creds file could not be written (%v)", finish, err)
	}
	os.Remove(sp) // best effort: a leftover holding the creds file's own key is ignored by the next reissue
	return true, nil
}

// reissueNow and nameLockCheck are seams for tests.
var (
	reissueNow    = time.Now
	nameLockCheck = func(ctx context.Context, l admin.NameLock) error { return l.Check(ctx) }
)

// stagePath is the staged-rotation file beside the creds file at p.
func stagePath(p string) string { return p + ".next" }

// stageCreds writes the new creds of a rotation to the staged file beside p, atomically with mode 0600 and synced.
func stageCreds(p string, creds []byte) error { return replaceCreds(stagePath(p), creds) }

// stagedRotation is a parsed staged file: the new key's public key and seed.
type stagedRotation struct {
	pub  string
	seed []byte
}

func (s *stagedRotation) wipe() {
	if s != nil {
		clear(s.seed)
	}
}

// readStaged reads the staged file of the creds file at p, nil when there is none (or when it holds the creds file's
// own key: the leftover of a rotation whose file was swapped). A file that is unreadable, loose, not a user seed, whose
// JWT does not name its seed, or that was issued for another account or name is refused.
func readStaged(p, name, accountPub, mainPub string) (*stagedRotation, error) {
	sp := stagePath(p)
	stale := func(why string) error {
		return fmt.Errorf("the staged file %s %s; remove the stale staged file %s and run again", sp, why, sp)
	}
	if _, err := os.Lstat(sp); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	raw, err := readCreds(sp)
	if err != nil {
		return nil, stale("cannot be read (" + err.Error() + ")")
	}
	defer clear(raw)
	kp, err := jwt.ParseDecoratedNKey(raw)
	if err != nil {
		return nil, stale("holds no user seed")
	}
	defer kp.Wipe()
	pub, err := kp.PublicKey()
	if err != nil || !nkeys.IsValidPublicUserKey(pub) {
		return nil, stale("holds no user seed")
	}
	sd, err := kp.Seed()
	if err != nil {
		return nil, stale("holds no user seed")
	}
	token, err := jwt.ParseDecoratedJWT(raw)
	var uc *jwt.UserClaims
	if err == nil {
		uc, err = jwt.DecodeUserClaims(token)
	}
	if err != nil {
		return nil, stale("holds no readable JWT")
	}
	switch {
	case uc.Subject != pub:
		return nil, stale("has a JWT for a different key than its seed")
	case !(uc.IssuerAccount == accountPub || (uc.IssuerAccount == "" && uc.Issuer == accountPub)):
		return nil, stale("was issued for another account")
	case uc.Name != name:
		return nil, stale("was issued for another name")
	}
	if pub == mainPub {
		return nil, nil
	}
	return &stagedRotation{pub: pub, seed: append([]byte(nil), sd...)}, nil
}

// writeCreds is how a reissue replaces a creds file; a test replaces it.
var writeCreds = replaceCreds

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
