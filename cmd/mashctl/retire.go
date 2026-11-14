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
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
)

const retireUsage = `usage: mashctl retire NAME [--delete-creds] [--force] [--no-revoke]
`

// afterRetireRead is a test seam called between the directory read and the guarded delete.
var afterRetireRead func()

// beforeRetireRevoke is a test seam called inside the guarded removal, under the name's lock, before the creds check
// and the revoke.
var beforeRetireRevoke func()

// deprovisionIfUser is the guarded server removal; a test replaces it to fail after the record is gone.
var deprovisionIfUser = (*admin.Admin).DeprovisionIfUser

// retireNow is the clock for the renamed creds file's suffix; a test replaces it.
var retireNow = time.Now

// retireRevokeNow is the clock for the revocation time the future-dated creds check compares against; a test replaces it.
var retireRevokeNow = time.Now

// noRevokeNote is printed after a retire with --no-revoke: the user can still connect.
const noRevokeNote = "its user key is not revoked (--no-revoke): its creds still authenticate until the key is revoked in the account JWT (nsc, or your operator tooling) or the signing key is rotated"

// runRetire is `mashctl retire`: the server removal (the revocation of the user key, then the directory record and
// inbox) comes first and the creds file last, so a failure leaves the earlier, safer state.
func runRetire(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	var name string
	var deleteCreds, force, noRevoke bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--delete-creds":
			deleteCreds = true
		case a == "--force":
			force = true
		case a == "--no-revoke":
			noRevoke = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "mashctl: retire: unknown %s\n%s", config.RejectedArg(a), retireUsage)
			return 2
		case name != "":
			fmt.Fprintf(stderr, "mashctl: retire: one endpoint name only\n%s", retireUsage)
			return 2
		default:
			name = a
		}
	}
	if name == "" {
		fmt.Fprintf(stderr, "mashctl: retire: no endpoint name\n%s", retireUsage)
		return 2
	}
	servers, err := parseServers(env)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: retire: %v\n%s", err, retireUsage)
		return 2
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "mashctl: retire %s: %s\n", name, fmt.Sprintf(format, a...))
		return 1
	}
	if !config.ValidName(name) {
		fmt.Fprintf(stderr, "mashctl: %s: NAME is not a valid endpoint name: 1-48 lower-case letters, digits and single hyphens (the value is not shown)\n", "retire")
		return 2
	}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	guard, ok := accountGuard(cfg, "retire", stderr)
	if !ok {
		return 1
	}
	defer guard.Release()
	credsPath, credsErr := credsFileFor(cfg, name)
	if credsErr != nil && deleteCreds {
		return fail("%v", credsErr)
	}

	// (1) the guarded server removal. The user key comes from the directory record, so a record someone replaced
	// meanwhile is never deleted.
	announceTarget(cfg, env, stderr, nil)
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return fail("%v", err)
	}
	defer closeAdmin()
	recs, err := adm.List(ctx)
	if err != nil {
		return fail("%v", err)
	}
	user := ""
	if r, ok := findRecord(recs, name); ok {
		user = r.User
	}
	var revokeOut bytes.Buffer
	switch {
	case user != "":
		// The user key is revoked inside the guarded removal, under the name's lock and only for the record it accepts:
		// a failed revocation removes nothing, a refused removal revokes nothing.
		var revokeErr, checkErr, lockErr error
		if noRevoke {
			fmt.Fprint(stderr, ctl.NoRevokeWarning(name, user))
		} else {
			recorder := adm
			adm = adm.WithRevoker(func(ctx context.Context, u string) error {
				if beforeRetireRevoke != nil {
					beforeRetireRevoke()
				}
				// Inside the guarded removal, under the name's lock, just before the revoke: the creds file is an
				// optional source of the user's JWT. A JWT dated after the revocation time, or a creds file that exists
				// but cannot be checked, refuses the removal, so nothing is removed or revoked.
				revokeAt := retireRevokeNow() // one reading: the check and the revocation use the same time
				if credsErr == nil {
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
				if err := guard.Before("the revocation of the user key"); err != nil {
					lockErr = err
					return err
				}
				if err := revokeUsers(ctx, cfg, env, servers, []string{u}, &revokeOut, func() time.Time { return revokeAt }); err != nil {
					revokeErr = err
					return fmt.Errorf("user key %s...: %w", ctl.ShortKey(u), err)
				}
				if err := guard.Before("the record of the revocation"); err != nil {
					// The key is revoked in the account JWT; only the bookkeeping record of it and the removal stop.
					lockErr = fmt.Errorf("the user key %s... is already revoked in the account JWT, but the removal stopped: %w", ctl.ShortKey(u), err)
					return lockErr
				}
				ctl.MarkRevoked(ctx, recorder, u, revokeAt, stderr)
				return nil
			})
		}
		if afterRetireRead != nil {
			afterRetireRead()
		}
		if err := guard.Before("the removal of the record and inbox"); err != nil { // before the first server write
			return fail("%v; nothing was changed", err)
		}
		if err := deprovisionIfUser(adm, ctx, name, user); err != nil {
			if admin.IsRefusal(err) {
				if msg, ok := ctl.AliasedRefusal(err, name); ok {
					return fail("%s", msg)
				}
				if lockErr != nil && errors.Is(err, lockErr) {
					return fail("%v; the directory record was not changed: run mashctl retire %s again", lockErr, name)
				}
				if checkErr != nil && errors.Is(err, checkErr) {
					return fail("%v", checkErr)
				}
				if revokeErr != nil && errors.Is(err, revokeErr) {
					return fail("%v: nothing was changed (--no-revoke retires without revoking)", err)
				}
				return fail("%v: nothing was changed", err)
			}
			if admin.IsPartial(err) {
				return fail("%v; run mashctl retire %s again", err, name)
			}
			return fail("PARTIAL: the directory record may already be removed but the lease fence or the inbox delete did not complete: %v. Finish with: mashctl deprovision %s (it removes what is left)", err, name)
		}
	case force:
		fmt.Fprintf(stderr, "mashctl: retire %s: no directory record on the server, so no user key is known and nothing was revoked; only the creds file is handled\n", name)
	default:
		return fail("no directory record on the server (--force handles the creds file anyway); if the endpoint was partly removed, finish with mashctl deprovision %s", name)
	}

	// (2) the creds file, last; a symlink or anything but a regular file is left alone. By default it is renamed
	// aside so the name can be added again at once; --delete-creds removes it.
	code := 0
	renamed := ""
	if credsErr == nil {
		if err := guard.Before("the move or removal of the creds file"); err != nil {
			return fail("%v; %s is removed from the server, but the creds file %s was left in place: rename or remove it by hand", err, name, credsPath)
		}
		fi, err := os.Lstat(credsPath)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			fmt.Fprintf(stderr, "mashctl: retire %s: %v\n", name, err)
			code = 1
		case !fi.Mode().IsRegular():
			fmt.Fprintf(stderr, "mashctl: retire %s: %s is not a regular file: left in place\n", name, credsPath)
			code = 1
		case deleteCreds:
			if err := os.Remove(credsPath); err != nil {
				fmt.Fprintf(stderr, "mashctl: retire %s: %v\n", name, err)
				code = 1
			}
		default:
			dest := credsPath + ".retired-" + retireNow().UTC().Format("20060102T150405Z")
			final, err := archiveNoReplace(credsPath, dest)
			if err != nil {
				fmt.Fprintf(stderr, "mashctl: retire %s: %v\n", name, err)
				code = 1
			} else {
				renamed = final
			}
		}
	}

	// (3) report.
	fmt.Fprintf(stdout, "retired %s\n", name)
	if renamed != "" {
		fmt.Fprintf(stdout, "creds file renamed to %s (mashctl add %s works again; --delete-creds removes it instead)\n", renamed, name)
	}
	switch {
	case user == "":
	case noRevoke:
		fmt.Fprintf(stdout, "%s: %s\n", name, noRevokeNote)
		fmt.Fprint(stderr, ctl.NoRevokeWarning(name, user))
	default:
		io.WriteString(stdout, revokeOut.String())
		fmt.Fprintf(stdout, "user key %s... revoked in the account JWT: its creds stop working when the server takes the update; the creds file is separate\n", ctl.ShortKey(user))
	}
	return code
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
