package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
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

// retireNow is the clock for the renamed creds file's suffix; a test replaces it.
var retireNow = time.Now

// retireRevokeNow is the clock for the revocation time the future-dated creds check compares against; a test replaces it.
var retireRevokeNow = time.Now

// noRevokeNote is printed after a delete with --no-revoke: the user can still connect.
const noRevokeNote = "its user key is not revoked (--no-revoke): its creds still authenticate until the key is revoked in the account JWT (nsc, or your operator tooling) or the signing key is rotated"

// retireOpts are the choices of one removal and the words its messages use. `mashctl endpoint delete` words them.
type retireOpts struct {
	deleteCreds, force, noRevoke, dryRun bool
	verb                                 string // the command as messages name it: "endpoint delete"
	label                                string // how messages name the endpoint
	full                                 string // the first line of the report names the endpoint this way
	again                                string // the command that finishes a partial removal
	done                                 string // the report's first word: "deleted"
	listHint                             string // added to the missing-record refusal, when set
	usage                                string
}

// retireResult is what one removal did. kind is deleted, unchanged, refused, partial, error or planned.
type retireResult struct {
	kind         string
	refusalCode  string
	detail       string
	user         string
	credsPath    string
	credsAction  string // renamed, removed or none
	renamedTo    string
	nextSteps    []string
	acked        int // servers that took the revocation of the user key; set when this run revoked it
	pendingUsage bool
}

// ok is true for the kinds that end without a problem.
func (r retireResult) ok() bool {
	return r.kind == "deleted" || r.kind == "unchanged" || r.kind == "planned"
}

// retireAddress removes the endpoint at addr as opts says and reports on stdout and stderr. A bad server list is a
// usage error (pendingUsage).
func retireAddress(addr sap.Address, opts retireOpts, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) retireResult {
	name := addr.String()
	label := opts.label
	deleteCreds, force, noRevoke := opts.deleteCreds, opts.force, opts.noRevoke
	servers, err := parseServers(env)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: %s: %v\n%s", opts.verb, err, opts.usage)
		return retireResult{kind: "error", detail: err.Error(), pendingUsage: true}
	}
	var user string
	stop := func(kind, code string, next []string, format string, a ...any) retireResult {
		msg := admin.RewordUnavailable(fmt.Sprintf(format, a...))
		fmt.Fprintf(stderr, "mashctl: %s %s: %s\n", opts.verb, label, msg)
		return retireResult{kind: kind, refusalCode: code, detail: msg, user: user, nextSteps: next}
	}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return retireResult{kind: "error", detail: err.Error()}
	}
	credsPath, credsErr := credsFileFor(cfg, addr)
	if credsErr != nil && deleteCreds {
		return stop("refused", "no_creds_file", []string{"run again without --delete-creds"}, "%v", credsErr)
	}
	if credsErr != nil {
		credsPath = ""
	}

	// (1) the guarded server removal. The user key comes from the directory record, so a record someone replaced
	// meanwhile is never deleted.
	announceTarget(cfg, env, stderr, nil)
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return stop("error", "", nil, "%v", err)
	}
	defer closeAdmin()
	recs, bad, err := adm.List(ctx)
	if err != nil {
		if !noRevoke {
			return stop("error", "", nil, "%v\n%s", err, ctl.NotRevoked)
		}
		return stop("error", "", nil, "%v", err)
	}
	for _, b := range bad {
		b.Warn(stderr, "mashctl")
	}
	if r, ok := findRecord(recs, addr); ok {
		user = r.User
	}
	burnedAlready := false
	cleaned := false // the server-side cleanup of an interrupted retirement did work
	if user == "" {
		// No record. A run killed after the record was purged leaves the retirement marker pending, and maybe the lease
		// and the inbox stream. The same command finishes it (ADR 0088), with or without a creds file.
		state, serr := adm.RetireState(ctx, addr)
		if serr != nil {
			return stop("error", "server", nil, "%v", serr)
		}
		burned := state != ""
		burnedAlready = burned
		if !burned && !force {
			hint := ""
			if opts.listHint != "" {
				hint = opts.listHint + "; "
			}
			return stop("refused", "not_found", []string{"mashctl endpoint list shows the endpoints that exist"}, "no directory record on the server (%s--force handles the creds file anyway); if the endpoint was partly removed, finish with mashctl endpoint delete %s", hint, name)
		}
		if burned && !opts.dryRun {
			left, eerr := adm.Exists(ctx, addr)
			if eerr != nil {
				return stop("error", "server", nil, "%v", eerr)
			}
			if state == "pending" || left {
				fmt.Fprintf(stderr, "mashctl: %s %s: the address is already retired; finishing the retirement on the server\n", opts.verb, label)
				if derr := adm.Deprovision(ctx, addr); derr != nil {
					return stop("error", "server", []string{"run " + opts.again + " again once the cause is fixed"}, "finishing the retirement failed: %v", derr)
				}
				cleaned = true
			}
		}
	}
	if opts.dryRun {
		return planRetire(opts, user, credsPath, stdout)
	}
	var revokeOut bytes.Buffer
	revokedOK := false // the revocation completed on every server
	revokeAcked := 0   // the servers that took it
	if user != "" {
		// The user key is revoked inside the guarded removal, under the name's lock and only for the record it accepts:
		// a failed revocation removes nothing, a refused removal revokes nothing.
		var revokeErr, checkErr error
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
				n, err := revokeUsers(ctx, cfg, env, servers, []string{u}, &revokeOut, func() time.Time { return revokeAt })
				if err != nil {
					revokeErr = err
					return fmt.Errorf("user key %s...: %w", ctl.ShortKey(u), err)
				}
				revokedOK = true
				revokeAcked = n
				ctl.MarkRevoked(ctx, recorder, u, revokeAt, stderr)
				return nil
			})
		}
		if afterRetireRead != nil {
			afterRetireRead()
		}
		// The server set is checked before the directory record, the lease or the inbox is touched: a short set
		// changes nothing.
		if !noRevoke {
			if err := checkServerSet(ctx, cfg, env, servers); err != nil {
				fmt.Fprintf(stderr, "mashctl: %s %s: %v\n%s\n", opts.verb, label, err, ctl.NotRevoked)
				return retireResult{kind: "refused", refusalCode: "server_set", detail: err.Error(), user: user,
					nextSteps: []string{"start the missing servers or name them with --servers, then run " + opts.again}}
			}
		}
		// partialNote says what a revocation that completed left behind, when the removal then did not finish.
		partialNote := func() {
			if !revokedOK {
				return
			}
			io.WriteString(stdout, revokeOut.String())
			fmt.Fprintf(stderr, "mashctl: %s %s: PARTIAL: revoked: user key %s... (the acknowledgement line above gives the server count); still pending: the removal of the directory record, the lease fence and the inbox; running it again finishes it\n", opts.verb, label, ctl.ShortKey(user))
		}
		again := []string{"run " + opts.again}
		if err := deprovisionIfUser(adm, ctx, addr, user); err != nil {
			if admin.IsRefusal(err) {
				if checkErr != nil && errors.Is(err, checkErr) {
					return stop("refused", "future_issued", nil, "%v", checkErr)
				}
				if revokeErr != nil && errors.Is(err, revokeErr) {
					if errors.Is(revokeErr, apply.ErrNotConfirmed) {
						return stop("refused", "revoke_not_confirmed", []string{"run " + opts.again + " --no-revoke to remove without revoking"}, "%v: the revocation may already be in force on some servers; the directory record was not changed (--no-revoke retires without revoking)", err)
					}
					return stop("refused", "revoke_failed", []string{"run " + opts.again + " --no-revoke to remove without revoking"}, "%v: %s; the directory record was not changed (--no-revoke retires without revoking)", err, ctl.NotRevoked)
				}
				return stop("refused", "refused", nil, "%v: nothing was changed", err)
			}
			if admin.IsPartial(err) {
				partialNote()
				return stop("partial", "", again, "%v; run %s again", err, opts.again)
			}
			if !noRevoke && !revokedOK {
				fmt.Fprintf(stderr, "mashctl: %s %s: %v\n%s; run the command again once the cause is fixed\n", opts.verb, label, err, ctl.NotRevoked)
				return retireResult{kind: "error", detail: err.Error(), user: user, nextSteps: []string{"run the command again once the cause is fixed"}}
			}
			partialNote()
			return stop("partial", "", []string{"finish with: mashctl endpoint delete " + name}, "PARTIAL: the directory record may already be removed but the lease fence or the inbox delete did not complete: %v. Finish with: mashctl endpoint delete %s (it removes what is left)", err, name)
		}
	} else {
		fmt.Fprintf(stderr, "mashctl: %s %s: no directory record on the server, so no user key is known and nothing was revoked; only the creds file is handled\n", opts.verb, label)
	}

	// (2) the creds file, last; a symlink or anything but a regular file is left alone. By default it is renamed
	// aside; --delete-creds removes it.
	res := retireResult{kind: "deleted", user: user, credsPath: credsPath, credsAction: "none"}
	credsFail := func(format string, a ...any) {
		fmt.Fprintf(stderr, "mashctl: %s %s: "+format+"\n", append([]any{opts.verb, label}, a...)...)
		res.kind = "partial"
		res.nextSteps = append(res.nextSteps, "handle the creds file by hand")
	}
	if credsPath != "" {
		fi, err := os.Lstat(credsPath)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			credsFail("%v", err)
		case !fi.Mode().IsRegular():
			credsFail("%s is not a regular file: left in place", credsPath)
		case deleteCreds:
			if err := os.Remove(credsPath); err != nil {
				credsFail("%v", err)
			} else {
				res.credsAction = "removed"
			}
		default:
			dest := credsPath + ".retired-" + retireNow().UTC().Format("20060102T150405Z")
			final, err := archiveNoReplace(credsPath, dest)
			if err != nil {
				credsFail("%v", err)
			} else {
				res.credsAction = "renamed"
				res.renamedTo = final
			}
		}
	}
	if user == "" && !cleaned && res.kind == "deleted" && res.credsAction == "none" {
		res.kind = "unchanged"
	}

	// (3) report.
	fmt.Fprintf(stdout, "%s %s\n", opts.done, opts.full)
	if res.renamedTo != "" {
		fmt.Fprintf(stdout, "creds file renamed to %s (--delete-creds removes it instead)\n", res.renamedTo)
	}
	switch {
	case user == "" && cleaned:
		fmt.Fprintln(stdout, "finished the retirement on the server: the marker is final and the lease fence and the inbox are removed")
	case user == "" && burnedAlready:
		fmt.Fprintln(stdout, "the address was already retired and nothing was left on the server")
	case user == "":
	case noRevoke:
		fmt.Fprintf(stdout, "%s: %s\n", label, noRevokeNote)
		fmt.Fprint(stderr, ctl.NoRevokeWarning(name, user))
	default:
		res.acked = revokeAcked
		io.WriteString(stdout, revokeOut.String())
		fmt.Fprintf(stdout, "user key %s... revoked in the account JWT: its creds stop working when the server takes the update; the creds file is separate\n", ctl.ShortKey(user))
	}
	return res
}

// planRetire prints what a removal would do and changes nothing.
func planRetire(opts retireOpts, user, credsPath string, stdout io.Writer) retireResult {
	res := retireResult{kind: "planned", user: user, credsPath: credsPath, credsAction: "none"}
	fmt.Fprintf(stdout, "would delete %s (nothing was changed)\n", opts.full)
	switch {
	case user == "":
		fmt.Fprintln(stdout, "no directory record: no user key is known and none would be revoked")
	case opts.noRevoke:
		fmt.Fprintf(stdout, "would not revoke user key %s... (--no-revoke)\n", ctl.ShortKey(user))
	default:
		fmt.Fprintf(stdout, "would revoke user key %s...\n", ctl.ShortKey(user))
	}
	if user != "" {
		fmt.Fprintln(stdout, "would remove the directory record and the inbox, and burn the address")
	}
	if credsPath != "" {
		if fi, err := os.Lstat(credsPath); err == nil && fi.Mode().IsRegular() {
			if opts.deleteCreds {
				res.credsAction = "removed"
				fmt.Fprintf(stdout, "would remove creds file %s\n", credsPath)
			} else {
				res.credsAction = "renamed"
				fmt.Fprintf(stdout, "would rename creds file %s to %s.retired-<time>\n", credsPath, credsPath)
			}
		}
	}
	if res.credsAction == "none" {
		fmt.Fprintln(stdout, "no creds file would be changed")
	}
	return res
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
