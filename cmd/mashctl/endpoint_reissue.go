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

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/enroll"
	"github.com/bogworks/mash/internal/sap"
)

const endpointReissueUsage = `usage: mashctl endpoint reissue ADDRESS (--public-key FILE | --make-key) [--jwt-out FILE]
                                [--servers N] [--sys-creds FILE] [--dry-run] [--json]
  ADDRESS is alice/dev/design. The old key is revoked on every server, then the directory record names the new key.
  --public-key FILE  sign the new public key the endpoint made; the JWT is written to --jwt-out
  --make-key         make the key on this host and replace <config dir>/<address>.creds
  The bulk forms (--all, --changed) are not available: run this once per endpoint.
  exit codes: 0 done or planned, 1 refused, 2 usage, 6 error
Example:
  mashctl endpoint reissue alice/dev/design --public-key design.pub
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
		case flag == "--all" || flag == "--changed" || flag == "--yes":
			usageCode = "bulk_unavailable"
			return usage("the bulk forms (--all, --changed) are not available; run endpoint reissue ADDRESS once per endpoint")
		case strings.HasPrefix(flag, "-"):
			usageCode = "unknown_flag"
			return usage("unknown " + config.RejectedArg(flag))
		case positional != "":
			return usage("one endpoint address only")
		default:
			positional = flag
		}
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

	burned, err := adm.IsBurned(ctx, addr)
	if err != nil {
		return epErr(doc, "reissue", "server", admin.RewordUnavailable(err.Error()), c.jsonOut, stdout, stderr)
	}
	if burned {
		return refuse(1, &epRefusal{enroll.CodeKeyRetired, fmt.Sprintf("endpoint %s was retired and its address is never reassigned", addr)})
	}
	switch _, _, err := adm.UserOf(ctx, addr); {
	case errors.Is(err, admin.ErrNoRecord):
		return refuse(1, &epRefusal{"endpoint_missing", fmt.Sprintf("endpoint %s does not exist; create it with: mashctl endpoint create %s", addr, epShortcut(addr))})
	case err != nil:
		return epErr(doc, "reissue", "server", admin.RewordUnavailable(err.Error()), c.jsonOut, stdout, stderr)
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
	// One reissue at a time per endpoint, in every key mode: enroll.Reissue and the write of the output file are one
	// step, so two runs cannot rotate the server key twice or leave the output on a key that is already revoked. The
	// lock is a directory beside the endpoint's creds path (keyed by the address); with no config directory it sits
	// beside the output file.
	lockPath := outPath + ".lock"
	if cp, cerr := credsFileFor(cfg, addr); cerr == nil {
		lockPath = cp + ".lock"
	}
	if err := epPrepDir(filepath.Dir(lockPath)); err != nil {
		return epErr(doc, "reissue", "write", fmt.Sprintf("the directory for the lock is not usable: %v; nothing was changed", err), c.jsonOut, stdout, stderr)
	}
	unlock, lerr := epLock(lockPath)
	if errors.Is(lerr, errEpBusy) {
		return refuse(1, &epRefusal{"reissue_busy", fmt.Sprintf("another reissue of %s is running; if none is (a run was killed), remove the directory %s and run this again", addr, lockPath)})
	}
	if lerr != nil {
		return epErr(doc, "reissue", "write", fmt.Sprintf("cannot take the lock %s: %v; nothing was changed", lockPath, lerr), c.jsonOut, stdout, stderr)
	}
	defer unlock()
	if !c.makeKey {
		// A run that held the lock before this one may have written the file: check again under the lock, before the
		// server key is rotated, so a refusal here changes nothing.
		if _, serr := os.Lstat(outPath); serr == nil {
			return refuse(1, &epRefusal{"file_exists", fmt.Sprintf("%s already exists and is never overwritten; nothing was changed: name another file with --jwt-out", outPath)})
		}
	}
	deps := enroll.Deps{Keys: keys, Admin: adm, Account: acct,
		Revoke: func(ctx context.Context, u string) error {
			var out bytes.Buffer
			err := revokeUsers(ctx, cfg, env, servers, []string{u}, &out, nil)
			stderr.Write(out.Bytes())
			return err
		}}
	res, err := enroll.Reissue(ctx, deps, addr, pub)
	if err != nil {
		if r, ok := epRefusalOf(err); ok {
			return refuse(1, r)
		}
		return epErr(doc, "reissue", "enroll", admin.RewordUnavailable(err.Error())+"; run the same command again to finish it", c.jsonOut, stdout, stderr)
	}
	defer clear(res.Seed)
	doc.UserPublicKey = res.PublicKey

	var werr error
	if c.makeKey {
		creds, ferr := jwt.FormatUserConfig(res.JWT, res.Seed)
		if ferr != nil {
			werr = errors.New("the creds could not be formatted")
		} else {
			werr = epReplaceCreds(outPath, creds)
			clear(creds)
		}
	} else {
		werr = epWriteNew(outPath, []byte(res.JWT+"\n"))
	}
	if errors.Is(werr, fs.ErrExist) {
		return refuse(1, &epRefusal{"file_exists", fmt.Sprintf("%s appeared while the command ran and is never overwritten; endpoint %s was reissued: run the same command again with another --jwt-out", outPath, addr)})
	}
	if werr != nil {
		finish := "run the same command with --public-key again to write the JWT"
		if c.makeKey {
			finish = fmt.Sprintf("the new key is lost; run: mashctl endpoint reissue %s --make-key", epShortcut(addr))
		}
		return epErr(doc, "reissue", "write", fmt.Sprintf("endpoint %s was reissued but %s was not written (%v); %s", addr, outPath, werr, finish), c.jsonOut, stdout, stderr)
	}
	out.written()
	doc.Outcome = "reissued"
	return epReport("reissue", doc, func(w io.Writer) {
		fmt.Fprintf(w, "reissued endpoint %s\n", epShortcut(addr))
		lines(w)
	}, 0, stdout, stderr, c.jsonOut)
}
