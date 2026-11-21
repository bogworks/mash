package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/enroll"
	"github.com/bogworks/mash/internal/sap"
)

const endpointReissueUsage = `usage: mashctl endpoint reissue <address> [--out PATH] [--public-key PATH]
       [--dry-run] [--json]
  address            the endpoint, as owner.group.name
  --out PATH         write a join file for another machine and install
                     nothing here
  --public-key PATH  sign this public key; needs --out
  --dry-run          print what would change, and change nothing
  --json             print the result as JSON
  With no option the key pair is made here and <address>.creds in the config
  folder is replaced.
  The old key is cancelled on every server before anything is removed. A run
  that was stopped is finished by running the same command again. Only one
  reissue of an address runs at a time.
Example:
  mashctl endpoint reissue acme.demo.bob
  mashctl endpoint reissue acme.demo.bob --out acme.demo.bob.join
`

var endpointReissueSpec = epSpec{
	name: "reissue", usage: endpointReissueUsage, flags: []string{"--dry-run"}, values: []string{"--out", "--public-key"},
	min: 1, max: 1, need: "the endpoint address (owner.group.name)", check: checkIssueArgs,
}

// errEpBusy is returned by epLock when the lock stayed held for epLockWait.
var errEpBusy = errors.New("lock busy")

// epLockWait is how long epLock waits for a lock held by another run.
var epLockWait = 10 * time.Second

// errEpOldLock is returned by epLock when a directory is at the lock path: a lock left or held by an older mash.
var errEpOldLock = errors.New("lock is a directory")

// epBusyDetail is the reissue_busy refusal text. The info line in the lock file is read for the message only.
func epBusyDetail(addr sap.Address, lockPath string, oldDir bool) string {
	if oldDir {
		return fmt.Sprintf("another reissue of %s holds the lock, or an older mash left the directory %s; if no reissue is running, remove it once", addr, lockPath)
	}
	head := fmt.Sprintf("another reissue of %s holds the lock", addr)
	if b, err := os.ReadFile(lockPath); err == nil {
		f := map[string]string{}
		for _, kv := range strings.Fields(string(b)) {
			if k, v, ok := strings.Cut(kv, "="); ok {
				f[k] = v
			}
		}
		if f["pid"] != "" && f["host"] != "" {
			head += fmt.Sprintf(" (pid %s on %s)", f["pid"], f["host"])
		}
	}
	return head + "; the lock is released when that run ends or is killed; run this again later"
}

// runEndpointReissue is `mashctl endpoint reissue`.
func runEndpointReissue(args []string, g cliframe.Global, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	if helpAsked(args) {
		fmt.Fprint(stdout, endpointReissueUsage)
		return 0
	}
	in, err := parseEpInput(endpointReissueSpec, args)
	if err != nil {
		return cliframe.Report(stdout, stderr, err, jsonRequested(args, "--out", "--public-key"))
	}
	t, res, err := epReissue(&in, env, lookup, stderr)
	if err != nil {
		return cliframe.Report(stdout, stderr, err, in.json)
	}
	epPrint(stdout, "reissue", t, res, g.Detail, in.json)
	return 0
}

// epReissue gives the endpoint new credentials, or with --dry-run checks that it could.
func epReissue(in *epInput, env func(string) string, lookup config.Lookup, stderr io.Writer) (epTarget, epResult, error) {
	addr, dry := in.addr, in.flags["--dry-run"]
	t, err := epLoadTarget("reissue", in, env, lookup)
	if err != nil {
		return t, epResult{}, err
	}
	cfg, outPath, pub := t.cfg, t.outPath, t.pub
	servers := serverCount(cfg)
	refuse := func(r *epRefusal) (epTarget, epResult, error) { return t, epResult{}, r.err(addr) }
	if t.join {
		// A join file is never written over a file. The one exception is the staged key of a killed run (below).
		if _, perr := os.Lstat(outPath + ".pending"); !t.makeKey || perr != nil {
			if ref := epPathFree(outPath); ref != nil {
				return refuse(ref)
			}
		}
	}
	keys, acct, err := epAccountOf(cfg, env)
	if err != nil {
		return refuse(&epRefusal{"no_account_key", err.Error()})
	}
	announceTarget(cfg, env, stderr)
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return t, epResult{}, epErrOf(addr, "server", admin.RewordUnavailable(err.Error())+"; nothing was changed")
	}
	defer closeAdmin()

	run := epRun{ctx: ctx, adm: adm, cfg: cfg, env: env, servers: servers, keys: keys, acct: acct, stderr: stderr,
		base: t.base, join: t.join, again: t.again}
	failed := func(f *epFail) (epTarget, epResult, error) {
		e := f.err(addr)
		if f.again && f.hint == "" {
			e = e.WithHint(t.again)
		}
		return t, epResult{}, e
	}
	if f := run.preflight(addr); f != nil {
		return failed(f)
	}

	resume := false // --public-key names the directory's key and a matching rotation marker is there
	if !t.makeKey {
		// The supplied key is never signed (or re-signed, in the resume and restore cases of epKeyUnchanged, where it
		// is the directory's current key) while the account JWT revokes it. A refusal here leaves any marker as it is.
		if f := epKeyRevokedFail(ctx, cfg, env, servers, pub); f != nil {
			return failed(f)
		}
		ref, res, err := epKeyUnchanged(ctx, adm, addr, pub, outPath)
		if err != nil {
			return t, epResult{}, epErrOf(addr, "server", admin.RewordUnavailable(err.Error()))
		} else if ref != nil {
			return refuse(ref)
		}
		resume = res
	}
	if dry {
		if !t.makeKey {
			if ref, err := epIssuedRefusal(ctx, adm, addr, pub); err != nil {
				return t, epResult{}, epErrOf(addr, "server", admin.RewordUnavailable(err.Error()))
			} else if ref != nil {
				return refuse(ref)
			}
		}
		return t, epResult{planned: true}, nil
	}

	if err := checkServerSet(ctx, cfg, env, servers); err != nil {
		return t, epResult{}, cliframe.Failf("%v", err).WithHint("mashctl doctor")
	}
	userPub, acked, f := run.reissue(addr, t.makeKey, pub, outPath, resume)
	if f != nil {
		return failed(f)
	}
	return t, epResult{userPub: userPub, acked: acked}, nil
}

// epMarker is what the rotation marker <out>.pending holds: the public key being rotated to and the address.
func epMarker(pub string, addr sap.Address) []byte { return []byte(pub + "\n" + addr.String() + "\n") }

// epMarkerMatches reports whether the marker file for outPath names pub and addr.
func epMarkerMatches(outPath, pub string, addr sap.Address) bool {
	fi, err := os.Lstat(outPath + ".pending")
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	b, err := os.ReadFile(outPath + ".pending")
	return err == nil && bytes.Equal(b, epMarker(pub, addr))
}

// epKeyUnchanged is the refusal key_unchanged when pub is the key the directory already names for addr: enroll.Reissue
// revokes nothing then, so old credentials would stay usable. Two exceptions go on: the restore case, when the key's
// issue record is missing or has a malformed issued_at (giving the current key records it again), and a verified
// resume (resume is true), when <outPath>.pending names this same key and address: an earlier run switched the
// directory to the key and was stopped before it wrote the JWT, so the rerun writes it.
func epKeyUnchanged(ctx context.Context, adm *admin.Admin, addr sap.Address, pub, outPath string) (ref *epRefusal, resume bool, err error) {
	cur, _, err := adm.UserOf(ctx, addr)
	if err != nil || cur != pub {
		return nil, false, err
	}
	if epMarkerMatches(outPath, pub, addr) {
		return nil, true, nil
	}
	if _, found, err := adm.IssuedByKey(ctx, pub); err != nil || !found {
		return nil, false, err // no record: the restore case
	}
	_, _, err = adm.IssuedHistoryByKey(ctx, pub)
	var bad *admin.MalformedIssuedAtError
	if errors.As(err, &bad) {
		return nil, false, nil
	}
	var incomplete *admin.IncompleteHistoryError
	if err != nil && !errors.As(err, &incomplete) {
		return nil, false, err
	}
	return &epRefusal{"key_unchanged", "the public key is already this endpoint's key, so nothing would be rotated; make a new key with: mash key create --out NAME.seed"}, false, nil
}

// epRevokeNow is the clock for the revocation time of a reissue: the future-issued check and the revocation use one
// reading of it. A test replaces it.
var epRevokeNow = time.Now

// epStagedPub is the public key of the seed in the pending file, or "" when there is none or it holds no seed.
func epStagedPub(pending string) string {
	b, err := os.ReadFile(pending)
	if err != nil {
		return ""
	}
	defer clear(b)
	kp, err := nkeys.FromSeed(bytes.TrimSpace(b))
	if err != nil {
		return ""
	}
	defer kp.Wipe()
	pub, _ := kp.PublicKey()
	return pub
}

// epCheckCreds checks that the creds file at path, when there is one, is the current creds of addr: its JWT is a user
// JWT named for addr, its subject is the seed's public key and (unless staged) the directory's user key, and the
// account that signed it is the one the record names. staged means a staged key already is the directory's key (an
// earlier run was stopped after the directory moved), so the file still holds the previous key. No file is fine.
func epCheckCreds(path string, addr sap.Address, dirUser, dirAcct string, staged bool) *epFail {
	unreadable := func(why string) *epFail {
		return &epFail{refused: true, code: "creds_unreadable", detail: fmt.Sprintf("the creds file %s exists but %s; nothing was changed", path, why), hint: fmt.Sprintf("mv %s %s.old", path, path)}
	}
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return unreadable("cannot be read")
	}
	if !fi.Mode().IsRegular() {
		return unreadable("is not a regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return unreadable("cannot be read")
	}
	defer clear(raw)
	tok, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		return unreadable("holds no readable JWT")
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		return unreadable("holds a JWT that is not a user JWT")
	}
	kp, err := jwt.ParseDecoratedUserNKey(raw)
	if err != nil {
		return unreadable("holds no readable user seed")
	}
	defer kp.Wipe()
	seedPub, err := kp.PublicKey()
	if err != nil {
		return unreadable("holds no readable user seed")
	}
	issuer := uc.IssuerAccount
	if issuer == "" {
		issuer = uc.Issuer
	}
	why := ""
	switch {
	case uc.Name != addr.String():
		why = fmt.Sprintf("its JWT is named %q, not %s", uc.Name, addr)
	case uc.Subject != seedPub:
		why = "its JWT and its seed are for different keys"
	case !staged && uc.Subject != dirUser:
		why = "its key is not the key the directory names for this endpoint"
	case issuer != dirAcct:
		why = "its JWT was signed for another account than the one the directory names"
	}
	if why == "" {
		return nil
	}
	return &epFail{refused: true, code: "creds_mismatch", detail: fmt.Sprintf("the creds file %s is not this endpoint's current creds: %s; nothing was changed", path, why), hint: fmt.Sprintf("mv %s %s.old", path, path)}
}

// epRun is what one reissue needs: the open admin connection, the keys and the server settings.
type epRun struct {
	ctx     context.Context
	adm     *admin.Admin
	cfg     config.Config
	env     func(string) string
	servers int
	keys    *ctlkeys.Authority
	acct    string
	stderr  io.Writer
	base    joinBase // the servers and CA of a join file
	join    bool     // --out: the output is a join file and nothing is installed here
	again   string   // the command that finishes a run that did part of its work
}

// epBeforeRevokeCheck is a test seam called inside the revoke hook, under the name's lock, before its future-issued check.
var epBeforeRevokeCheck func()

// epFutureIssuedError is the revoke hook's refusal of a key whose issue history is dated after the revocation time.
type epFutureIssuedError struct{ err error }

func (e *epFutureIssuedError) Error() string { return e.err.Error() }
func (e *epFutureIssuedError) Unwrap() error { return e.err }

// preflight is the checks before a reissue changes anything: the address is not burned and has a directory record.
func (r epRun) preflight(addr sap.Address) *epFail {
	burned, err := r.adm.IsBurned(r.ctx, addr)
	if err != nil {
		return &epFail{code: "server", detail: admin.RewordUnavailable(err.Error())}
	}
	if burned {
		return &epFail{refused: true, code: enroll.CodeKeyRetired, detail: fmt.Sprintf("endpoint %s was retired and its address is never reassigned", addr)}
	}
	switch _, _, err := r.adm.UserOf(r.ctx, addr); {
	case errors.Is(err, admin.ErrNoRecord):
		return &epFail{refused: true, code: "endpoint_missing", detail: ""}
	case err != nil:
		return &epFail{code: "server", detail: admin.RewordUnavailable(err.Error())}
	}
	return nil
}

// reissue gives addr a new key and writes the result: with makeKey the key is made on this host and replaces the creds
// file at outPath, else pub is signed and the JWT is written new to outPath. It returns the new user public key.
func (r epRun) reissue(addr sap.Address, makeKey bool, pub, outPath string, resume bool) (string, int, *epFail) {
	var seed []byte
	pending := outPath + ".pending"
	// One reissue at a time per endpoint, in every key mode: enroll.Reissue and the write of the output file are one
	// step, so two runs cannot rotate the server key twice or leave the output on a key that is already revoked. The
	// lock is a file beside the endpoint's creds path (keyed by the address), held with flock; with no config
	// directory it sits beside the output file.
	lockPath := outPath + ".lock"
	if cp, cerr := credsFileFor(r.cfg, addr); cerr == nil {
		lockPath = cp + ".lock"
	}
	if err := epPrepDir(filepath.Dir(lockPath)); err != nil {
		return "", 0, &epFail{code: "write", detail: fmt.Sprintf("the directory for the lock is not usable: %v; nothing was changed", err)}
	}
	unlock, lerr := epLock(lockPath)
	if errors.Is(lerr, errEpBusy) || errors.Is(lerr, errEpOldLock) {
		return "", 0, &epFail{refused: true, code: "reissue_busy", detail: epBusyDetail(addr, lockPath, errors.Is(lerr, errEpOldLock))}
	}
	if lerr != nil {
		return "", 0, &epFail{code: "write", detail: fmt.Sprintf("cannot take the lock %s: %v; nothing was changed", lockPath, lerr)}
	}
	defer unlock()
	old, dirAcct, uerr := r.adm.UserOf(r.ctx, addr)
	if errors.Is(uerr, admin.ErrNoRecord) {
		return "", 0, &epFail{refused: true, code: "endpoint_missing", detail: ""}
	}
	if uerr != nil {
		return "", 0, &epFail{code: "server", detail: admin.RewordUnavailable(uerr.Error())}
	}
	rotating := true // the directory key will be revoked: false for a resume, which only writes the output
	written := false // the join file already holds the staged key: an earlier run wrote it and was stopped
	if makeKey {
		// The creds file is checked under the lock, before anything is staged or revoked: a file that is not this
		// endpoint's current creds is never overwritten.
		stagedPub := epStagedPub(pending)
		if stagedPub != "" {
			// A staged key that the account JWT has revoked can never be used: refuse before anything changes.
			revoked, rerr := keyRevoked(r.ctx, r.cfg, r.env, r.servers, stagedPub)
			if rerr != nil {
				return "", 0, &epFail{code: "revocation_unknown", detail: fmt.Sprintf("cannot tell whether the staged key %s is revoked: %v; nothing was changed", pending, rerr)}
			}
			if revoked {
				return "", 0, &epFail{refused: true, code: "staged_key_revoked", detail: fmt.Sprintf("the staged key %s was revoked", pending), hint: "rm " + pending}
			}
		}
		staged := stagedPub != "" && stagedPub == old
		if r.join {
			// Nothing is installed here: the join file is the output, and only the staged key of a killed run may
			// have written it already.
			if _, serr := os.Lstat(outPath); serr == nil {
				have, herr := epOutUser(outPath, addr, true)
				if herr != nil || !staged || have != stagedPub {
					return "", 0, &epFail{refused: true, code: "file_exists", detail: fmt.Sprintf("%s already exists", outPath)}
				}
				written = true
			}
		} else if f := epCheckCreds(outPath, addr, old, dirAcct, staged); f != nil {
			return "", 0, f
		}
		rotating = !staged
	} else {
		// A run that held the lock before this one may have written the file: check again under the lock, before the
		// server key is rotated, so a refusal here changes nothing.
		if _, serr := os.Lstat(outPath); serr == nil {
			return "", 0, &epFail{refused: true, code: "file_exists", detail: fmt.Sprintf("%s already exists", outPath)}
		}
		if resume {
			if old != pub || !epMarkerMatches(outPath, pub, addr) {
				return "", 0, &epFail{refused: true, code: "key_unchanged", detail: "the public key is already this endpoint's key, so nothing would be rotated; make a new key with: mash key create --out NAME.seed"}
			}
			rotating = false
		} else {
			rotating = old != pub
		}
	}
	revokeAt := epRevokeNow() // one reading: the future-issued check and the revocation use the same time
	if rotating {
		// The old key's issue record must not be dated after the revocation time, or the revocation would not cover
		// that credential. Refused here, nothing has been changed.
		if err := ctl.CheckNotFutureIssued(r.ctx, r.adm, []ctl.RevokeTarget{{Name: addr.String(), Key: old}}, revokeAt, r.stderr); err != nil {
			return "", 0, &epFail{refused: true, code: "future_issued", detail: err.Error()}
		}
	}
	marker := outPath + ".pending"
	if !makeKey && !resume {
		// The rotation is recorded before the directory moves, and removed only after the JWT is written: a run stopped
		// in between is finished by the same command, which finds the marker.
		os.Remove(marker)
		if err := epWriteNew(marker, epMarker(pub, addr)); err != nil {
			return "", 0, &epFail{code: "write", detail: fmt.Sprintf("cannot record the rotation in %s: %v; nothing was changed", marker, err)}
		}
	}
	if makeKey {
		var err error
		if pub, seed, _, err = epStageKey(pending); err != nil {
			return "", 0, &epFail{code: "write", detail: fmt.Sprintf("cannot stage the new key in %s: %v; nothing was changed", pending, err)}
		}
		defer clear(seed)
		if epAfterStage != nil {
			epAfterStage()
		}
	}
	acked := 0
	deps := enroll.Deps{Keys: r.keys, Admin: r.adm, Account: r.acct,
		Revoke: func(ctx context.Context, u string) error {
			if epBeforeRevokeCheck != nil {
				epBeforeRevokeCheck()
			}
			// Under the name's lock, for exactly the key about to be revoked: another reissue may have changed the key
			// since the early look, so this is the check that counts. A refusal revokes nothing.
			if err := ctl.CheckNotFutureIssued(ctx, r.adm, []ctl.RevokeTarget{{Name: addr.String(), Key: u}}, revokeAt, r.stderr); err != nil {
				return &epFutureIssuedError{err}
			}
			var out bytes.Buffer
			n, err := revokeUsers(ctx, r.cfg, r.env, r.servers, []string{u}, &out, func() time.Time { return revokeAt })
			// A warning from the revocation is kept; the rest of its lines are the acknowledgement counts.
			for _, l := range strings.Split(out.String(), "\n") {
				if strings.HasPrefix(l, "warning: ") {
					fmt.Fprintln(r.stderr, l)
				}
			}
			acked = n
			return err
		}}
	res, err := enrollReissue(r.ctx, deps, addr, pub)
	if err != nil {
		var fi *epFutureIssuedError
		if errors.As(err, &fi) {
			if makeKey {
				os.Remove(pending) // a refusal changed nothing: the staged key is not needed
			} else if !resume {
				os.Remove(marker)
			}
			return "", 0, &epFail{refused: true, code: "future_issued", detail: fi.err.Error()}
		}
		if ref, ok := epRefusalOf(err); ok {
			if makeKey {
				os.Remove(pending) // a refusal changed nothing: the staged key is not needed
			} else if !resume {
				os.Remove(marker)
			}
			return "", 0, &epFail{refused: true, code: ref.Code, detail: ref.Detail}
		}
		return "", 0, &epFail{code: "server", detail: admin.RewordUnavailable(err.Error()) + "; the endpoint may be partly reissued", again: true}
	}
	var werr error
	switch {
	case makeKey && written:
		// The join file already holds this key: nothing to write.
	case makeKey && r.join:
		werr = epPublishStagedJoin(r.base, addr, res.JWT, seed, outPath, pending, false)
	case makeKey:
		werr = epPublishStaged(res.JWT, seed, outPath, pending, true, false)
	default:
		if epAfterEnroll != nil {
			epAfterEnroll()
		}
		werr = writeJoinFile(outPath, r.base.withJWT(addr.String(), res.JWT))
		if werr == nil {
			os.Remove(marker)
		}
	}
	if errors.Is(werr, fs.ErrExist) {
		return "", 0, &epFail{refused: true, code: "file_exists", detail: fmt.Sprintf("%s appeared while the command ran; endpoint %s was reissued but %s was not written", outPath, addr, outPath), again: true}
	}
	if werr != nil {
		joinHint := ""
		if r.join {
			joinHint = fmt.Sprintf("remove %s or choose another --out", outPath)
		}
		kept := ""
		if makeKey {
			kept = fmt.Sprintf("; the new key is kept in %s", pending)
		}
		return "", 0, &epFail{code: "write", detail: fmt.Sprintf("endpoint %s was reissued but %s was not written (%v)%s", addr, outPath, werr, kept), again: true, hint: joinHint}
	}
	// enroll.Reissue released the server's name lock before the file was written, and the lock above is local to this
	// host: a run on another host may have changed the key since. Read the record again and compare.
	if epBeforeVerify != nil {
		epBeforeVerify()
	}
	var cur string
	var verr error
	if epVerifyFail != nil {
		verr = epVerifyFail()
	}
	if verr == nil {
		cur, _, verr = r.adm.UserOf(r.ctx, addr)
	}
	if verr != nil {
		kept := ""
		if makeKey {
			kept = fmt.Sprintf(" The staged key %s was kept; the new key is not revoked.", pending)
		}
		return "", 0, &epFail{code: "server", detail: fmt.Sprintf("endpoint %s was reissued and %s was written, but the result could not be confirmed (%s) and the file was kept: run mashctl endpoint show %s and compare its key with the one in the file.%s", addr, outPath, admin.RewordUnavailable(verr.Error()), addr, kept), again: true}
	}
	if cur != res.PublicKey {
		os.Remove(outPath)
		os.Remove(marker)
		if makeKey {
			os.Remove(pending)
		}
		return "", 0, &epFail{code: "superseded", detail: fmt.Sprintf("another reissue changed %s's key while this one ran, so the credentials this run wrote are for a key that is no longer the endpoint's; they were removed", addr), again: true}
	}
	if makeKey {
		// The readback confirmed the key: the staged seed is not needed any more. Removed only now, so a run stopped
		// before this point finds the staged key and finishes without revoking the key it installed.
		os.Remove(pending)
	}
	return res.PublicKey, acked, nil
}

// epAfterStage and epAfterEnroll are test seams: after the key is staged, and after the directory is written and
// before the creds file is. A hook that panics stops the run at that point, as a kill would. epBeforeVerify runs
// right before the directory record is read again to confirm the key.
var (
	epAfterStage   func()
	epAfterEnroll  func()
	epBeforeVerify func()
	// epVerifyFail, when set, returns the error the readback of the directory record fails with.
	epVerifyFail func() error
)

// epStageKey returns the key a --make-key run uses, kept in the pending file at path so a run killed at any later
// point finds the same key when it runs again. An existing pending file is read and its key reused; otherwise a new
// key is made and written (mode 0600, never over an existing file). made is true when this call wrote the file.
func epStageKey(path string) (pub string, seed []byte, made bool, err error) {
	for try := 0; try < 2; try++ {
		fi, lerr := os.Lstat(path)
		switch {
		case lerr == nil:
			if !fi.Mode().IsRegular() {
				return "", nil, false, fmt.Errorf("%s is not a regular file; remove it", path)
			}
			if !epOwnedByCaller(fi) {
				return "", nil, false, fmt.Errorf("%s is owned by another user; remove it and run the command again", path)
			}
			if fi.Mode().Perm()&0o077 != 0 {
				// A staged seed that others can read is not a secret: never revoke a working key for it.
				return "", nil, false, fmt.Errorf("%s has mode %04o and exposes the seed; remove it and run the command again", path, fi.Mode().Perm())
			}
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				return "", nil, false, rerr
			}
			seed = bytes.TrimSpace(b)
			kp, kerr := nkeys.FromSeed(seed)
			if kerr != nil {
				return "", nil, false, fmt.Errorf("%s does not hold a seed; remove it", path)
			}
			defer kp.Wipe()
			pub, err = kp.PublicKey()
			return pub, seed, false, err
		case !errors.Is(lerr, fs.ErrNotExist):
			return "", nil, false, lerr
		}
		kp, kerr := nkeys.CreateUser()
		if kerr != nil {
			return "", nil, false, kerr
		}
		sd, _ := kp.Seed()
		s := append([]byte(nil), sd...) // Wipe clears the key's own copy
		pub, _ = kp.PublicKey()
		kp.Wipe()
		werr := epWriteNew(path, s)
		if werr == nil {
			return pub, s, true, nil
		}
		clear(s)
		if !errors.Is(werr, fs.ErrExist) {
			return "", nil, false, werr
		}
	}
	return "", nil, false, fmt.Errorf("%s keeps appearing; remove it and run again", path)
}

// epPublishStaged writes the creds file for jwtText and seed and then, with removePending, removes the pending file.
// With replace the file at path is replaced, else it is never overwritten.
func epPublishStaged(jwtText string, seed []byte, path, pending string, replace, removePending bool) error {
	if epAfterEnroll != nil {
		epAfterEnroll()
	}
	creds, err := jwt.FormatUserConfig(jwtText, seed)
	if err != nil {
		return errors.New("the creds could not be formatted")
	}
	defer clear(creds)
	if replace {
		err = epReplaceCreds(path, creds)
	} else {
		err = epWriteCredsNew(path, creds)
	}
	if err != nil {
		return err
	}
	// The new creds must be durable before the staged seed goes: a crash after the removal must not leave the old file
	// with the seed gone. A failed sync keeps the pending file.
	if err := epSyncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("the creds file %s was written but the directory could not be synced (%w); the staged key %s was kept", path, err, pending)
	}
	if removePending {
		os.Remove(pending)
	}
	return nil
}

// epSyncDir syncs a directory; a test replaces it to record the order against the removal of the staged seed.
var epSyncDir = syncDir
