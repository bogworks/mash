package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
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
		archive := false
		switch {
		case errors.Is(err, os.ErrNotExist):
			// A crash after the rename leaves the creds at a staging name; rerunning finishes it.
			left, lerr := leftovers(credsPath)
			if lerr != nil {
				return retireResult{}, partial("its creds file was not moved")
			}
			archive = len(left) > 0
		case err != nil:
			return retireResult{}, partial("its creds file was not moved")
		case !fi.Mode().IsRegular():
			return retireResult{}, partial("its creds path is not a file")
		default:
			archive = true
		}
		if archive {
			final, err := archiveNoReplace(credsPath, credsPath+".deleted")
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

// archiveHook is a test seam: called by archiveNoReplace right after it has taken ownership of src (renamed it to
// a unique src+".deleting.<id>" name) and before it reads it, so a test can replace src at that moment. Nil in
// production.
var archiveHook func()

var stageSeq atomic.Uint64

// stageName is a staging name for src that no other attempt uses: the process id, a counter and random bytes.
func stageName(src string) string {
	var r [6]byte
	rand.Read(r[:])
	return fmt.Sprintf("%s.deleting.%d.%d.%x", src, os.Getpid(), stageSeq.Add(1), r)
}

// stagedSuffix matches what follows src+".deleting." in a name stageName makes: pid, counter, 12 hex digits.
var stagedSuffix = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9a-f]{12}$`)

// leftovers lists the staging files of src, the names stageName makes, left by interrupted or running attempts,
// sorted. Any other file with the src+".deleting." prefix is not ours and is left alone.
// A directory that cannot be listed is an error, not "none": a staged creds file may be in it. A missing one has none.
func leftovers(src string) ([]string, error) {
	ents, err := os.ReadDir(filepath.Dir(src))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("cannot list %s to look for staged creds files: %w", filepath.Dir(src), err)
	}
	pre := filepath.Base(src) + ".deleting."
	var out []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), pre) && stagedSuffix.MatchString(strings.TrimPrefix(e.Name(), pre)) {
			out = append(out, filepath.Join(filepath.Dir(src), e.Name()))
		}
	}
	return out, nil
}

// archiveNoReplace sets src aside at dest as a marker that keeps the endpoint's name and its JWT block and no key:
// a new file, never replacing an existing one (dest, then dest.1 to dest.100), with src's mode bits, written, synced
// and its directory synced before the creds file is removed. It returns the marker's path.
//
// It first takes ownership of src with an atomic rename to a name unique to this attempt,
// src+".deleting.<pid>.<n>.<random>", which no other attempt uses, so a rename never overwrites a file. It reads,
// stats and removes only that file, never src again, so a creds file put at src meanwhile is neither read nor
// removed. Staging files left by an interrupted run (the names stageName makes, regular files only) are each claimed by a
// rename to a fresh unique name, which only one attempt can win, and archived first, each to its own marker; if src
// is then missing, the last of those markers' path is returned, otherwise src is archived too and its marker's path
// is returned. If every name is taken, or the marker cannot be made durable, the half marker is deleted and src is
// restored, or left under its staging name if the name src was taken meanwhile (a retry finds it). The seed may
// remain in disk blocks after removal.
func archiveNoReplace(src, dest string) (string, error) {
	left := ""
	staged, err := leftovers(src)
	if err != nil {
		return "", err
	}
	for _, l := range staged {
		fi, err := os.Lstat(l)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !fi.Mode().IsRegular() {
			return "", fmt.Errorf("%s is not a file", l)
		}
		claimed := stageName(src)
		if err := os.Rename(l, claimed); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // another attempt took it
			}
			return "", err
		}
		m, err := archiveOwned(src, claimed, dest)
		if err != nil {
			return "", err
		}
		left = m
	}
	fi, err := os.Lstat(src)
	if err != nil {
		if left != "" && errors.Is(err, os.ErrNotExist) {
			return left, nil
		}
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", src)
	}
	own := stageName(src)
	if err := os.Rename(src, own); err != nil {
		return "", err
	}
	if fi, err := os.Lstat(own); err != nil || !fi.Mode().IsRegular() {
		restoreOwnership(own, src)
		if err == nil {
			err = fmt.Errorf("%s is not a regular file", src)
		}
		return "", err
	}
	if archiveHook != nil {
		archiveHook()
	}
	m, err := archiveOwned(src, own, dest)
	if err != nil {
		restoreOwnership(own, src)
		return "", err
	}
	return m, nil
}

// restoreOwnership puts own back at src, only if src does not exist: a hard link fails if src is taken, so a
// replacement is never clobbered. If src is taken, or the filesystem refuses hard links, own stays where it is for a
// retry to find: a rename after an existence check could overwrite a file put at src in between.
func restoreOwnership(own, src string) {
	if err := os.Link(own, src); err == nil {
		os.Remove(own)
	}
}

// archiveOwned writes the marker for the creds file own (named src in errors) at the first free name of dest, dest.1
// to dest.100, and removes own once the marker is durable. On failure own is left where it is.
func archiveOwned(src, own, dest string) (string, error) {
	fi, err := os.Stat(own)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(own)
	if err != nil {
		return "", err
	}
	marker := jwtBlock(raw)
	for i := 0; i <= 100; i++ {
		d := dest
		if i > 0 {
			d = fmt.Sprintf("%s.%d", dest, i)
		}
		out, err := os.OpenFile(d, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm())
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", err
		}
		if err := writeMarkerAndRemove(own, out, marker, fi.Mode().Perm()); err != nil {
			return "", err
		}
		return d, nil
	}
	return "", fmt.Errorf("could not archive %s: %s and %s.1 to %s.100 all exist; nothing was moved", src, dest, dest, dest)
}

// jwtBlock is the JWT block of a creds file, from the BEGIN line through the END line with a trailing newline, or
// nil when there is none. It copies by whitelist: only lines of the JWT alphabet between the two marker lines.
func jwtBlock(creds []byte) []byte {
	var out []byte
	in := false
	for _, l := range strings.Split(string(creds), "\n") {
		l = strings.TrimRight(l, "\r ")
		switch {
		case !in && l == "-----BEGIN NATS USER JWT-----":
			in = true
			out = append(out[:0], l...)
			out = append(out, '\n')
		case in && l == "------END NATS USER JWT------":
			return append(append(out, l...), '\n')
		case in:
			if l == "" || strings.Trim(l, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.=") != "" {
				return nil
			}
			out = append(out, l...)
			out = append(out, '\n')
		}
	}
	return nil
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

// writeMarkerAndRemove writes marker into out, makes it durable (file, then its directory), and only then removes
// own, the creds file archiveNoReplace took ownership of. On any failure before removal the half-written marker is
// deleted and own is left in place.
func writeMarkerAndRemove(own string, out *os.File, marker []byte, mode os.FileMode) error {
	fail := func(err error) error {
		out.Close()
		os.Remove(out.Name())
		return err
	}
	if _, err := out.Write(marker); err != nil {
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
	if err := os.Remove(own); err != nil {
		return err
	}
	// Best effort: the marker is already durable, and failing here must not make the caller delete it.
	_ = syncDir(dir)
	return nil
}
