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
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/enroll"
	"github.com/bogworks/mash/internal/sap"
)

const endpointReissueUsage = `usage: mashctl endpoint reissue ADDRESS (--public-key FILE | --make-key) [--jwt-out FILE]
                                [--servers N] [--sys-creds FILE] [--dry-run] [--json]
       mashctl endpoint reissue (--all | --changed) --make-key [--yes] [--servers N] [--sys-creds FILE] [--dry-run] [--json]
  ADDRESS is alice/dev/design. The old key is revoked on every server, then the directory record names the new key.
  --public-key FILE  sign the new public key the endpoint made; the JWT is written to --jwt-out
  --make-key         make the key on this host and replace <config dir>/<address>.creds
  --all              every endpoint that has a creds file on this host (same-host only: --make-key, never --public-key);
                     an endpoint with no creds file here is skipped (no_local_creds), not rotated
  --changed          every endpoint whose creds file on this host has a permission set that differs from the current one;
                     an endpoint with no creds file here is skipped (no_local_creds)
  --yes              do the bulk reissue; without it (or with --dry-run) the command lists what it would reissue
  A bulk run reissues one endpoint at a time in address order; a refusal or error on one does not stop the others.
  The bulk JSON has schema mashctl-endpoint-bulk@1.
  A key made by --make-key is kept in <creds file>.pending (mode 0600) until the creds file is written, so the same
  command run again after a kill finishes with the same key.
  A rerun of --public-key FILE --jwt-out FILE after a run that switched the directory to the key but did not write the
  JWT is a resume: <jwt-out>.pending (mode 0600) records the rotation, and the rerun writes the JWT and removes it.
  --public-key FILE is checked first: a key the account JWT revokes is refused (key_revoked), also when it is the
  endpoint's current key (a resume or a restore never signs a revoked key; make a new key with mash key create);
  a check that cannot be made is an error (revocation_unknown), and nothing is changed.
  A staged key that the account revokes is refused (staged_key_revoked): remove the .pending file and run again;
  a check that cannot be made is an error (revocation_unknown).
  --changed also reissues an endpoint with a staged <creds file>.pending (rotation_pending) or whose current key is
  revoked (key_revoked); one whose revocation cannot be checked is an error, revocation_unknown, never "unchanged".
  Refusals include key_unchanged: --public-key names the key the endpoint already has, so nothing would be rotated.
  Other refusals: future_issued (the old key's issue record is dated after the revocation time; fix the clock),
  creds_mismatch (--make-key found a creds file that is not this endpoint's current creds: its name, key or account
  differs; move it aside with mv FILE FILE.old and run again, or use --public-key), creds_unreadable (the file exists
  and cannot be read or parsed).
  Errors include superseded: another reissue changed the endpoint's key while this one ran, so the file this run wrote
  was removed; run the command again.
  exit codes: 0 done, planned or unchanged, 1 refused, 2 usage, 5 partial (bulk), 6 error
Example:
  mashctl endpoint reissue alice/dev/design --public-key design.pub
  mashctl endpoint reissue --changed --make-key --yes
`

// errEpBusy is returned by epLock when the lock stayed held for epLockWait.
var errEpBusy = errors.New("lock busy")

// epLockWait is how long epLock waits for a lock held by another run.
var epLockWait = 10 * time.Second

// epLock takes the lock directory at path: os.Mkdir is exclusive, the attempt repeats every 50 ms for epLockWait. The
// returned function removes the directory this call made.
func epLock(path string) (unlock func(), err error) {
	deadline := time.Now().Add(epLockWait)
	for {
		err := os.Mkdir(path, 0o700)
		if err == nil {
			return func() { os.Remove(path) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, errEpBusy
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// runEndpointReissue is `mashctl endpoint reissue`.
func runEndpointReissue(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(stdout, endpointReissueUsage)
		return 0
	}
	var c epCommon
	var positional string
	var all, changed, yes bool
	c.jsonOut = jsonRequested(args, epValueFlags...)
	doc := epDocFor(sap.Address{})
	usageCode := "usage"
	usage := func(msg string) int {
		code := usageCode
		usageCode = "usage"
		return epUsage("reissue", endpointReissueUsage, doc, code, msg, c.jsonOut, stdout, stderr)
	}
	for i := 0; i < len(args); i++ {
		flag, inline, hasInline := splitFlag(args, i)
		get := func() (string, error) { return flagValue(args, &i, flag, inline, hasInline) }
		if ok, err := c.take(flag, get); ok {
			if err != nil {
				return usage(err.Error())
			}
			continue
		}
		switch {
		case flag == "--all":
			all = true
		case flag == "--changed":
			changed = true
		case flag == "--yes":
			yes = true
		case strings.HasPrefix(flag, "-"):
			usageCode = "unknown_flag"
			return usage("unknown " + config.RejectedArg(flag))
		case positional != "":
			return usage("one endpoint address only")
		default:
			positional = flag
		}
	}
	if all || changed || yes {
		return runEndpointBulk(bulkArgs{all: all, changed: changed, yes: yes, positional: positional, c: c}, env, lookup, stdout, stderr)
	}
	var addr sap.Address
	refuse := func(code int, ref *epRefusal) int {
		doc.Refusal = ref
		if code == 2 {
			doc.Outcome = "error"
		}
		return epReport("reissue", doc, nil, code, stdout, stderr, c.jsonOut)
	}
	if positional == "" {
		return refuse(2, &epRefusal{"address_missing", "no endpoint address: write alice/dev/design"})
	}
	addr, err := parseEndpointRef(positional)
	if err != nil {
		return refuse(2, &epRefusal{"address_invalid", err.Error()})
	}
	doc.setAddr(addr)
	switch {
	case c.makeKey && c.publicKey != "":
		return refuse(2, &epRefusal{"key_both", "give one of --public-key FILE and --make-key, not both"})
	case !c.makeKey && c.publicKey == "":
		return refuse(2, &epRefusal{"key_missing", "a reissue needs a new key: write --public-key FILE (the endpoint's new public key, from mash key create) or --make-key (make the key on this host)"})
	}
	servers, err := parseServers(env)
	if err != nil {
		return refuse(2, &epRefusal{"servers_invalid", err.Error()})
	}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		return epErr(doc, "reissue", "config", err.Error(), c.jsonOut, stdout, stderr)
	}

	var pub, outPath string
	resume := false // --public-key names the directory's key and a matching rotation marker is there
	out := epOutput{doc: &doc, creds: c.makeKey}
	var ref *epRefusal
	if c.makeKey {
		p, err := credsFileFor(cfg, addr)
		if err != nil {
			return refuse(1, &epRefusal{"no_config_dir", "no config directory to write the creds file in; set MASH_CONFIG_DIR"})
		}
		outPath = p
		out.set(p)
	} else {
		if pub, ref = epReadPublicKey(c.publicKey); ref != nil {
			return refuse(1, ref)
		}
		outPath = c.jwtOut
		display := c.jwtOut
		if outPath == "" {
			outPath = addr.String() + ".jwt"
			display = "./" + outPath
		}
		if ref := epPathFree(outPath); ref != nil {
			return refuse(1, ref)
		}
		out.set(display)
	}
	keys, acct, err := epAccountOf(cfg, env)
	if err != nil {
		return refuse(1, &epRefusal{"no_account_key", err.Error()})
	}
	announceTarget(cfg, env, stderr, nil)
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return epErr(doc, "reissue", "server", admin.RewordUnavailable(err.Error())+"; nothing was changed", c.jsonOut, stdout, stderr)
	}
	defer closeAdmin()

	run := epRun{ctx: ctx, adm: adm, cfg: cfg, env: env, servers: servers, keys: keys, acct: acct, stderr: stderr}
	failed := func(f *epFail) int {
		if f.refused {
			return refuse(1, &epRefusal{f.code, f.detail})
		}
		return epErr(doc, "reissue", f.code, f.detail, c.jsonOut, stdout, stderr)
	}
	if f := run.preflight(addr); f != nil {
		return failed(f)
	}

	if !c.makeKey {
		// The supplied key is never signed (or re-signed, in the resume and restore cases of epKeyUnchanged, where it
		// is the directory's current key) while the account JWT revokes it. A refusal here leaves any marker as it is.
		if f := epKeyRevokedFail(ctx, cfg, env, servers, pub); f != nil {
			return failed(f)
		}
		ref, res, err := epKeyUnchanged(ctx, adm, addr, pub, outPath)
		if err != nil {
			return epErr(doc, "reissue", "server", admin.RewordUnavailable(err.Error()), c.jsonOut, stdout, stderr)
		} else if ref != nil {
			return refuse(1, ref)
		}
		resume = res
	}

	var step epStep
	if c.makeKey {
		step = epStep{Sender: "endpoint", Step: "session", Action: "restart the sessions of this endpoint so they read the new creds",
			Address: addr.String(), Purpose: "use", Detail: "creds file " + out.path()}
	} else {
		step = epNextCredential(addr, out.path())
	}
	doc.NextSteps = []epStep{step}
	lines := func(w io.Writer) {
		fmt.Fprintf(w, "  address  %s\n", addr)
		if c.makeKey {
			fmt.Fprintf(w, "  creds    %s\n", out.path())
			fmt.Fprintln(w, "next: restart the sessions of this endpoint so they read the new creds")
		} else {
			fmt.Fprintf(w, "  jwt      %s\n", out.path())
			fmt.Fprintln(w, "next: on the endpoint's machine, run: "+step.Detail)
		}
	}
	if c.dryRun && !c.makeKey {
		if ref, err := epIssuedRefusal(ctx, adm, addr, pub); err != nil {
			return epErr(doc, "reissue", "server", admin.RewordUnavailable(err.Error()), c.jsonOut, stdout, stderr)
		} else if ref != nil {
			return refuse(1, ref)
		}
	}
	if c.dryRun {
		doc.Outcome = "planned"
		return epReport("reissue", doc, func(w io.Writer) {
			fmt.Fprintf(w, "would reissue endpoint %s\n", epShortcut(addr))
			lines(w)
		}, 0, stdout, stderr, c.jsonOut)
	}

	if err := checkServerSet(ctx, cfg, env, servers); err != nil {
		return epErr(doc, "reissue", "servers", fmt.Sprintf("%v; %s", err, ctl.NotRevoked), c.jsonOut, stdout, stderr)
	}
	userPub, acked, f := run.reissue(addr, c.makeKey, pub, outPath, resume)
	if f != nil {
		return failed(f)
	}
	doc.UserPublicKey = userPub
	doc.ServersAcknowledged = &acked
	out.written()
	doc.Outcome = "reissued"
	return epReport("reissue", doc, func(w io.Writer) {
		fmt.Fprintf(w, "reissued endpoint %s\n", epShortcut(addr))
		lines(w)
		fmt.Fprintf(w, "servers acknowledged the account update: %d\n", acked)
	}, 0, stdout, stderr, c.jsonOut)
}

// epMarker is what the rotation marker <jwt-out>.pending holds: the public key being rotated to and the address.
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
		return &epFail{refused: true, code: "creds_unreadable", detail: fmt.Sprintf("the creds file %s exists but %s; nothing was changed: move the file aside (mv %s %s.old) and run again, or use --public-key", path, why, path, path)}
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
	return &epFail{refused: true, code: "creds_mismatch", detail: fmt.Sprintf("the creds file %s is not this endpoint's current creds: %s; nothing was changed: move the file aside (mv %s %s.old) and run again, or use --public-key", path, why, path, path)}
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
}

// epBeforeRevokeCheck is a test seam called inside the revoke hook, under the name's lock, before its future-issued check.
var epBeforeRevokeCheck func()

// epFutureIssuedError is the revoke hook's refusal of a key whose issue history is dated after the revocation time.
type epFutureIssuedError struct{ err error }

func (e *epFutureIssuedError) Error() string { return e.err.Error() }
func (e *epFutureIssuedError) Unwrap() error { return e.err }

// epFail is a refusal (refused, exit 1) or an error (exit 6) of one endpoint, by its code.
type epFail struct {
	refused      bool
	code, detail string
}

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
		return &epFail{refused: true, code: "endpoint_missing", detail: fmt.Sprintf("endpoint %s does not exist; create it with: mashctl endpoint create %s", addr, epShortcut(addr))}
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
	// lock is a directory beside the endpoint's creds path (keyed by the address); with no config directory it sits
	// beside the output file.
	lockPath := outPath + ".lock"
	if cp, cerr := credsFileFor(r.cfg, addr); cerr == nil {
		lockPath = cp + ".lock"
	}
	if err := epPrepDir(filepath.Dir(lockPath)); err != nil {
		return "", 0, &epFail{code: "write", detail: fmt.Sprintf("the directory for the lock is not usable: %v; nothing was changed", err)}
	}
	unlock, lerr := epLock(lockPath)
	if errors.Is(lerr, errEpBusy) {
		return "", 0, &epFail{refused: true, code: "reissue_busy", detail: fmt.Sprintf("another reissue of %s is running; if none is (a run was killed), remove the directory %s and run this again", addr, lockPath)}
	}
	if lerr != nil {
		return "", 0, &epFail{code: "write", detail: fmt.Sprintf("cannot take the lock %s: %v; nothing was changed", lockPath, lerr)}
	}
	defer unlock()
	old, dirAcct, uerr := r.adm.UserOf(r.ctx, addr)
	if errors.Is(uerr, admin.ErrNoRecord) {
		return "", 0, &epFail{refused: true, code: "endpoint_missing", detail: fmt.Sprintf("endpoint %s does not exist; create it with: mashctl endpoint create %s", addr, epShortcut(addr))}
	}
	if uerr != nil {
		return "", 0, &epFail{code: "server", detail: admin.RewordUnavailable(uerr.Error())}
	}
	rotating := true // the directory key will be revoked: false for a resume, which only writes the output
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
				return "", 0, &epFail{refused: true, code: "staged_key_revoked", detail: fmt.Sprintf("the staged key %s was revoked; remove %s and run the command again", pending, pending)}
			}
		}
		staged := stagedPub != "" && stagedPub == old
		if f := epCheckCreds(outPath, addr, old, dirAcct, staged); f != nil {
			return "", 0, f
		}
		rotating = !staged
	} else {
		// A run that held the lock before this one may have written the file: check again under the lock, before the
		// server key is rotated, so a refusal here changes nothing.
		if _, serr := os.Lstat(outPath); serr == nil {
			return "", 0, &epFail{refused: true, code: "file_exists", detail: fmt.Sprintf("%s already exists and is never overwritten; nothing was changed: name another file with --jwt-out", outPath)}
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
			r.stderr.Write(out.Bytes())
			acked = n
			return err
		}}
	res, err := enroll.Reissue(r.ctx, deps, addr, pub)
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
		return "", 0, &epFail{code: "enroll", detail: admin.RewordUnavailable(err.Error()) + "; run the same command again to finish it"}
	}
	var werr error
	if makeKey {
		werr = epPublishStaged(res.JWT, seed, outPath, pending, true)
	} else {
		if epAfterEnroll != nil {
			epAfterEnroll()
		}
		werr = epWriteNew(outPath, []byte(res.JWT+"\n"))
		if werr == nil {
			os.Remove(marker)
		}
	}
	if errors.Is(werr, fs.ErrExist) {
		return "", 0, &epFail{refused: true, code: "file_exists", detail: fmt.Sprintf("%s appeared while the command ran and is never overwritten; endpoint %s was reissued: run the same command again with another --jwt-out", outPath, addr)}
	}
	if werr != nil {
		finish := "run the same command with --public-key again to write the JWT"
		if makeKey {
			finish = fmt.Sprintf("the new key is kept in %s; run the same command again to finish it", pending)
		}
		return "", 0, &epFail{code: "write", detail: fmt.Sprintf("endpoint %s was reissued but %s was not written (%v); %s", addr, outPath, werr, finish)}
	}
	// enroll.Reissue released the server's name lock before the file was written, and the lock above is local to this
	// host: a run on another host may have changed the key since. Read the record again and compare.
	if epBeforeVerify != nil {
		epBeforeVerify()
	}
	cur, _, verr := r.adm.UserOf(r.ctx, addr)
	if verr != nil {
		return "", 0, &epFail{code: "server", detail: fmt.Sprintf("endpoint %s was reissued and %s was written, but the result could not be confirmed (%s) and the file was kept: run mashctl endpoint show %s and compare its key with the one in the file", addr, outPath, admin.RewordUnavailable(verr.Error()), epShortcut(addr))}
	}
	if cur != res.PublicKey {
		os.Remove(outPath)
		os.Remove(marker)
		if makeKey {
			os.Remove(pending)
		}
		return "", 0, &epFail{code: "superseded", detail: fmt.Sprintf("another reissue changed %s's key while this one ran, so the credentials this run wrote are for a key that is no longer the endpoint's; they were removed; run the command again", addr)}
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

// epPublishStaged writes the creds file for jwtText and seed and then removes the pending file. With replace the file
// at path is replaced, else it is never overwritten.
func epPublishStaged(jwtText string, seed []byte, path, pending string, replace bool) error {
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
	os.Remove(pending)
	return nil
}

// epSyncDir syncs a directory; a test replaces it to record the order against the removal of the staged seed.
var epSyncDir = syncDir
