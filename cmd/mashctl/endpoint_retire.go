package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/sap"
)

// credsFileFor is where the endpoint's creds live: an existing owner.group.instance.creds in the config directories.
func credsFileFor(cfg config.Config, addr sap.Address) (string, error) {
	p := cfg.Find(addr.String() + ".creds")
	if p == "" {
		return "", config.ErrNoConfigDir
	}
	return p, nil
}

// afterRetireRead is a test seam called between the directory read and the guarded delete.
var afterRetireRead func()

// beforeRetireRevoke is a test seam called inside the guarded removal, under the name's lock, before the creds check
// and the revoke.
var beforeRetireRevoke func()

// deprovisionIfUser is the guarded server removal; a test replaces it to fail after the record is gone.
var deprovisionIfUser = (*admin.Admin).DeprovisionIfUser

// retireRevokeNow is the clock for the revocation time the future-dated creds check compares against; a test replaces it.
var retireRevokeNow = time.Now

// retireOpts are the choices of one removal.
type retireOpts struct {
	dryRun, yes bool
	again       string                      // the command that finishes a partial removal
	confirm     func(messages uint64) error // asks the person; called when neither --yes nor --dry-run was given
}

// retireResult is what one removal did.
type retireResult struct {
	planned   bool   // --dry-run: nothing was changed
	messages  uint64 // the messages the inbox held when the removal began
	renamedTo string // where the creds file was put aside, when it was
}

// plainMsg drops the program prefix the libraries put on a message.
func plainMsg(m string) string {
	return strings.TrimPrefix(strings.TrimPrefix(m, "mash: "), "mashctl: ")
}

// retireAddress removes the endpoint at addr as opts says. Its failures are *cliframe.Error values: exit 2 for a missing terminal, exit 1 for the rest. A removal that did part of its work says so, and running the
// same command again finishes it.
func retireAddress(addr sap.Address, opts retireOpts, env func(string) string, lookup config.Lookup, stderr io.Writer) (retireResult, error) {
	name := addr.String()
	fail := func(format string, a ...any) *cliframe.Error {
		return cliframe.Failf("%s", plainMsg(admin.RewordUnavailable(fmt.Sprintf(format, a...))))
	}
	// partial is a removal that did part of its work: one line naming the address the user typed and the cause in
	// plain words (the error behind it is never printed), then the command that finishes it.
	partial := func(cause string) *cliframe.Error {
		return cliframe.Failf("%s is partly deleted: %s", name, cause).WithHint(opts.again)
	}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		return retireResult{}, failErr(err)
	}
	servers := serverCount(cfg)
	credsPath, credsErr := credsFileFor(cfg, addr)
	if credsErr != nil {
		credsPath = ""
	}

	// (1) the guarded server removal. The user key comes from the directory record, so a record someone replaced
	// meanwhile is never deleted.
	announceTarget(cfg, env, stderr)
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return retireResult{}, failErr(err)
	}
	defer closeAdmin()
	recs, bad, err := adm.List(ctx)
	if err != nil {
		return retireResult{}, fail("%v; %s", err, ctl.NotRevoked)
	}
	for _, b := range bad {
		b.Warn(stderr, "mashctl")
	}
	var user string
	if r, ok := findRecord(recs, addr); ok {
		user = r.User
	}
	notFound := cliframe.Failf("%s does not exist", name).WithHint("mashctl endpoint list")
	cleanup := false // the server holds the leftovers of an interrupted removal that this run finishes
	if user == "" {
		// No record. A run killed after the record was purged leaves the retirement marker pending, and maybe the lease
		// and the inbox stream. The same command finishes it (ADR 0088), with or without a creds file.
		state, serr := adm.RetireState(ctx, addr)
		if serr != nil {
			return retireResult{}, fail("%v", serr)
		}
		if state == "" {
			return retireResult{}, notFound
		}
		left, eerr := adm.Exists(ctx, addr)
		if eerr != nil {
			return retireResult{}, fail("%v", eerr)
		}
		cleanup = state == "pending" || left
		if !cleanup && credsPath == "" {
			return retireResult{}, notFound
		}
	}
	res := retireResult{}
	if res.messages, err = adm.InboxMessages(ctx, addr); err != nil {
		return retireResult{}, fail("%v", err)
	}
	if opts.dryRun {
		res.planned = true
		return res, nil
	}
	if !opts.yes {
		if err := opts.confirm(res.messages); err != nil {
			return retireResult{}, err
		}
	}
	if cleanup {
		if derr := adm.Deprovision(ctx, addr); derr != nil {
			return retireResult{}, partial(admin.CauseEarlierRemovalStuck)
		}
	}
	var revokeOut bytes.Buffer
	revokedOK := false // the revocation completed on every server
	if user != "" {
		// The user key is revoked inside the guarded removal, under the name's lock and only for the record it accepts:
		// a failed revocation removes nothing, a refused removal revokes nothing.
		var revokeErr, checkErr error
		recorder := adm
		adm = adm.WithRevoker(func(ctx context.Context, u string) error {
			if beforeRetireRevoke != nil {
				beforeRetireRevoke()
			}
			// Inside the guarded removal, under the name's lock, just before the revoke: the creds file is an
			// optional source of the user's JWT. A JWT dated after the revocation time, or a creds file that exists
			// but cannot be checked, refuses the removal, so nothing is removed or revoked.
			revokeAt := retireRevokeNow() // one reading: the check and the revocation use the same time
			// The issue record is read first: a key whose recorded history is future-dated, or cannot be shown to be
			// complete, refuses the removal in the same way.
			if err := ctl.CheckNotFutureIssued(ctx, recorder, []ctl.RevokeTarget{{Name: name, Key: u}}, revokeAt, stderr); err != nil {
				checkErr = err
				return err
			}
			if credsPath != "" {
				claims, err := credsIssuedClaims(credsPath, u)
				if err == nil {
					err = refuseFutureIssued(name, claims, revokeAt)
				} else {
					err = fmt.Errorf("%w; nothing was changed", err)
				}
				if err != nil {
					checkErr = err
					return fmt.Errorf("creds check: %w", err)
				}
			}
			_, err := revokeUsers(ctx, cfg, env, servers, []string{u}, &revokeOut, func() time.Time { return revokeAt })
			if err != nil {
				revokeErr = err
				return fmt.Errorf("user key %s...: %w", ctl.ShortKey(u), err)
			}
			revokedOK = true
			ctl.MarkRevoked(ctx, recorder, u, revokeAt, stderr)
			return nil
		})
		if afterRetireRead != nil {
			afterRetireRead()
		}
		// The server set is checked before the directory record, the lease or the inbox is touched: a short set
		// changes nothing.
		if err := checkServerSet(ctx, cfg, env, servers); err != nil {
			return retireResult{}, fail("%v; %s", err, ctl.NotRevoked)
		}
		if err := deprovisionIfUser(adm, ctx, addr, user); err != nil {
			if admin.IsRefusal(err) {
				if revokeErr != nil && errors.Is(err, revokeErr) && errors.Is(revokeErr, ctlkeys.ErrSigningKeyLost) {
					return retireResult{}, failErr(revokeErr) // the key store's own error: / hint: lines
				}
				if checkErr != nil && errors.Is(err, checkErr) {
					return retireResult{}, fail("%v", checkErr)
				}
				if revokeErr != nil && errors.Is(err, revokeErr) {
					if errors.Is(revokeErr, apply.ErrNotConfirmed) {
						if k, n, ok := apply.ConfirmCounts(revokeErr); ok {
							if miss := n - k; miss > 0 {
								return retireResult{}, cliframe.Failf("%s was not deleted: %d of %d servers did not confirm its key was cancelled", name, miss, n).WithWrapAt(68).WithHint(opts.again)
							}
							return retireResult{}, cliframe.Failf("%s was not deleted: %d servers took the cancellation but it could not be read back", name, n).WithHint(opts.again)
						}
						return retireResult{}, fail("%v", err).WithHint(opts.again)
					}
					return retireResult{}, fail("%v; %s", err, ctl.NotRevoked)
				}
				return retireResult{}, fail("%v: nothing was changed", err)
			}
			if admin.IsPartial(err) {
				cause, _ := admin.PartialCause(err)
				return retireResult{}, partial(cause)
			}
			if !revokedOK {
				return retireResult{}, fail("%v; %s", err, ctl.NotRevoked).WithHint(opts.again)
			}
			return retireResult{}, partial(admin.CauseUnknown)
		}
		// A warning from the revocation is kept; the rest of its lines are the acknowledgement counts.
		for _, l := range strings.Split(revokeOut.String(), "\n") {
			if strings.HasPrefix(l, "warning: ") {
				fmt.Fprintln(stderr, l)
			}
		}
	}

	// (2) the creds file, last, renamed aside; a symlink or anything but a regular file is left alone.
	if credsPath != "" {
		fi, err := os.Lstat(credsPath)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			return retireResult{}, partial("its creds file was not moved")
		case !fi.Mode().IsRegular():
			return retireResult{}, partial("its creds path is not a file")
		default:
			dest := credsPath + ".deleted"
			final, err := archiveNoReplace(credsPath, dest)
			if err != nil {
				return retireResult{}, partial("its creds file was not moved")
			}
			res.renamedTo = final
		}
	}
	if user == "" && !cleanup && res.renamedTo == "" {
		return retireResult{}, notFound
	}
	return res, nil
}

// archiveNoReplace moves src to dest without ever replacing an existing file. If dest exists it tries dest.1,
// dest.2, ... up to dest.100, then fails with src left in place. It returns the path the file ended up at.
func archiveNoReplace(src, dest string) (string, error) {
	fi, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	for i := 0; i <= 100; i++ {
		d := dest
		if i > 0 {
			d = fmt.Sprintf("%s.%d", dest, i)
		}
		err := os.Link(src, d)
		if err == nil {
			// The new name must be durable before the old one goes: a crash or a failed sync here leaves both
			// names, never neither. On a sync failure the original stays and the link is reported.
			if err := syncDir(filepath.Dir(d)); err != nil {
				return "", fmt.Errorf("archived to %s but could not make it durable (%w); %s was left in place", d, err, src)
			}
			if err := os.Remove(src); err != nil {
				return "", fmt.Errorf("archived to %s but could not remove %s: %w", d, src, err)
			}
			_ = syncDir(filepath.Dir(d)) // best effort: the removal
			return d, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		// Link unsupported here (EPERM, EXDEV, ENOTSUP, ...): copy into a new file, never over an existing one.
		out, cerr := os.OpenFile(d, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm())
		if cerr != nil {
			if errors.Is(cerr, os.ErrExist) {
				continue
			}
			return "", cerr
		}
		if cerr := copyAndRemove(src, out, fi.Mode().Perm()); cerr != nil {
			os.Remove(d)
			return "", cerr
		}
		return d, nil
	}
	return "", fmt.Errorf("could not archive %s: %s and %s.1 to %s.100 all exist; nothing was moved", src, dest, dest, dest)
}

// syncFile flushes a file to stable storage; a variable so tests can make it fail.
var syncFile = func(f *os.File) error { return f.Sync() }

// syncDir fsyncs a directory so a created or removed entry is durable. Filesystems that cannot fsync a
// directory (EINVAL, ENOTSUP, ENOSYS) are not an error.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.ENOSYS) {
		return nil
	}
	return err
}

// copyAndRemove copies src into out, makes the archive durable (file, then its directory), and only then
// removes src. On any failure before removal the half-written archive is deleted and src is left in place.
func copyAndRemove(src string, out *os.File, mode os.FileMode) error {
	fail := func(err error) error {
		out.Close()
		os.Remove(out.Name())
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return fail(err)
	}
	defer in.Close()
	if _, err := io.Copy(out, in); err != nil {
		return fail(err)
	}
	if err := out.Chmod(mode); err != nil {
		return fail(err)
	}
	if err := syncFile(out); err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		os.Remove(out.Name())
		return err
	}
	dir := filepath.Dir(out.Name())
	if err := syncDir(dir); err != nil {
		os.Remove(out.Name())
		return err
	}
	if err := os.Remove(src); err != nil {
		return err
	}
	// Best effort: the archive is already durable, and failing here must not make the caller delete it.
	_ = syncDir(dir)
	return nil
}
