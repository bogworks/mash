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
  --all              every endpoint in the directory (same-host only: --make-key, never --public-key)
  --changed          every endpoint whose creds file on this host has a permission set that differs from the current one;
                     an endpoint with no creds file here is skipped (no_local_creds)
  --yes              do the bulk reissue; without it (or with --dry-run) the command lists what it would reissue
  A bulk run reissues one endpoint at a time in address order; a refusal or error on one does not stop the others.
  The bulk JSON has schema mashctl-endpoint-bulk@1.
  A key made by --make-key is kept in <creds file>.pending (mode 0600) until the creds file is written, so the same
  command run again after a kill finishes with the same key.
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
	userPub, acked, f := run.reissue(addr, c.makeKey, pub, outPath)
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
func (r epRun) reissue(addr sap.Address, makeKey bool, pub, outPath string) (string, int, *epFail) {
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
	if !makeKey {
		// A run that held the lock before this one may have written the file: check again under the lock, before the
		// server key is rotated, so a refusal here changes nothing.
		if _, serr := os.Lstat(outPath); serr == nil {
			return "", 0, &epFail{refused: true, code: "file_exists", detail: fmt.Sprintf("%s already exists and is never overwritten; nothing was changed: name another file with --jwt-out", outPath)}
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
			var out bytes.Buffer
			n, err := revokeUsers(ctx, r.cfg, r.env, r.servers, []string{u}, &out, nil)
			r.stderr.Write(out.Bytes())
			acked = n
			return err
		}}
	res, err := enroll.Reissue(r.ctx, deps, addr, pub)
	if err != nil {
		if ref, ok := epRefusalOf(err); ok {
			if makeKey {
				os.Remove(pending) // a refusal changed nothing: the staged key is not needed
			}
			return "", 0, &epFail{refused: true, code: ref.Code, detail: ref.Detail}
		}
		return "", 0, &epFail{code: "enroll", detail: admin.RewordUnavailable(err.Error()) + "; run the same command again to finish it"}
	}
	var werr error
	if makeKey {
		werr = epPublishStaged(res.JWT, seed, outPath, pending, true)
	} else {
		werr = epWriteNew(outPath, []byte(res.JWT+"\n"))
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
	return res.PublicKey, acked, nil
}

// epAfterStage and epAfterEnroll are test seams: after the key is staged, and after the directory is written and
// before the creds file is. A hook that panics stops the run at that point, as a kill would.
var (
	epAfterStage  func()
	epAfterEnroll func()
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
	os.Remove(pending)
	return nil
}
