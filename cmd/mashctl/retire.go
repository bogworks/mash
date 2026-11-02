package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/policy"
	"github.com/bogworks/mash/internal/policyfile"
)

const retireUsage = `usage: mashctl retire NAME [--policy FILE] [--delete-creds] [--force]
`

// afterRetireRead is a test seam called between the directory read and the guarded delete.
var afterRetireRead func()

// deprovisionIfUser is the guarded server removal; a test replaces it to fail after the record is gone.
var deprovisionIfUser = (*admin.Admin).DeprovisionIfUser

// revocationNote is printed after every retire: mashctl does not revoke users.
const revocationNote = "revocation is not implemented yet: delete the creds file and rotate the zone key if the user must be cut off"

// runRetire is `mashctl retire`: the server removal comes first, the policy save second and the creds file last, so a
// failure leaves the earlier, safer state and never a policy that names an endpoint the server still serves.
func runRetire(args []string, env func(string) string, stdout, stderr io.Writer) int {
	var name, file string
	var deleteCreds, force bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--delete-creds":
			deleteCreds = true
		case a == "--force":
			force = true
		case a == "--policy" || strings.HasPrefix(a, "--policy="):
			if v, has := strings.CutPrefix(a, "--policy="); has {
				file = v
			} else if i+1 < len(args) {
				i++
				file = args[i]
			}
			if file == "" {
				fmt.Fprintf(stderr, "mashctl: retire: --policy needs a file\n%s", retireUsage)
				return 2
			}
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "mashctl: retire: unknown option %q\n%s", a, retireUsage)
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
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "mashctl: retire %s: %s\n", name, fmt.Sprintf(format, a...))
		return 1
	}
	if !config.ValidName(name) {
		return fail("endpoint name %q: 1-48 lower-case letters, digits and single hyphens", name)
	}
	cfg, err := loadCtlConfig(env, true)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	path, err := policyPath(file, env, cfg)
	if err != nil {
		return fail("no policy file: use --policy FILE or MASH_POLICY (%v)", err)
	}
	credsPath := ""
	if deleteCreds {
		if credsPath, err = credsFileFor(cfg, name); err != nil {
			return fail("%v", err)
		}
	}

	// (1) the policy file, locked for the whole command.
	doc, err := policyfile.Load(path)
	if err != nil {
		return fail("%v", err)
	}
	defer doc.Close()
	beforeBytes, _ := doc.Bytes()
	beforeFile, before, err := compileBytes(beforeBytes, cfg.Owner)
	if err != nil {
		return fail("%s: %v", path, err)
	}
	_, declared := beforeFile.Endpoints[name]
	if !declared && !force {
		return fail("endpoint %q is not declared in %s (--force removes it from the server only)", name, path)
	}

	// (2) the entry removed in memory.
	var after *policy.Compiled
	editPolicy := declared
	if declared {
		refs, err := doc.RemoveEndpoint(name)
		if err != nil {
			return fail("%v", err)
		}
		if len(refs) > 0 {
			if !force {
				return fail("tests still name %s: %s; remove them first", name, strings.Join(refs, ", "))
			}
			// The file would not parse with those tests: it is left as it is.
			editPolicy = false
			fmt.Fprintf(stderr, "mashctl: retire %s: tests still name %s: %s; the policy file is left as it is\n", name, name, strings.Join(refs, ", "))
		} else {
			afterBytes, _ := doc.Bytes()
			if _, after, err = compileBytes(afterBytes, cfg.Owner); err != nil {
				return fail("the edit does not make a valid policy: %v", err)
			}
		}
	}

	// (3) the guarded server removal. The user key comes from the directory record, so a record someone replaced
	// meanwhile is never deleted.
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
	for _, r := range recs {
		if r.Name == name {
			user = r.User
		}
	}
	switch {
	case user != "":
		if afterRetireRead != nil {
			afterRetireRead()
		}
		if err := deprovisionIfUser(adm, ctx, name, user); err != nil {
			if admin.IsRefusal(err) {
				return fail("%v: nothing was changed", err)
			}
			return fail("PARTIAL: the directory record may already be removed but the lease fence or the inbox delete did not complete: %v. Finish with: mashctl deprovision %s (it removes what is left); the policy file was not changed. Then run: mashctl retire %s --force (it removes the policy entry)", err, name, name)
		}
	case force:
		fmt.Fprintf(stderr, "mashctl: retire %s: no directory record on the server; only the policy file and creds are handled\n", name)
	default:
		return fail("no directory record on the server (--force edits the policy file anyway); if the endpoint was partly removed, finish with mashctl deprovision %s", name)
	}

	// (4) the policy file.
	if declared && !editPolicy {
		fmt.Fprintf(stderr, "mashctl: retire %s: removed from the server, but still declared in %s: remove the tests that name it and its entry by hand\n", name, path)
		return 1
	}
	if editPolicy {
		if err := doc.Save(); err != nil {
			if errors.Is(err, policyfile.ErrChanged) {
				return fail("%v: %s is already removed from the server (its record and inbox are gone) but is still declared in %s: edit the file by hand to remove its entry", err, name, path)
			}
			return fail("%v: %s is already removed from the server but is still declared in %s: edit the file by hand to remove its entry", err, name, path)
		}
	}

	// (5) the creds file, last; a symlink or anything but a regular file is left alone.
	code := 0
	if deleteCreds {
		fi, err := os.Lstat(credsPath)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			fmt.Fprintf(stderr, "mashctl: retire %s: %v\n", name, err)
			code = 1
		case !fi.Mode().IsRegular():
			fmt.Fprintf(stderr, "mashctl: retire %s: %s is not a regular file: left in place\n", name, credsPath)
			code = 1
		default:
			if err := os.Remove(credsPath); err != nil {
				fmt.Fprintf(stderr, "mashctl: retire %s: %v\n", name, err)
				code = 1
			}
		}
	}

	// (6) report.
	fmt.Fprintf(stdout, "retired %s\n", name)
	if after != nil {
		for _, l := range consequences(before, after) {
			fmt.Fprintln(stdout, l)
		}
	}
	fmt.Fprintf(stdout, "%s's user is not revoked at the server: %s\n", name, revocationNote)
	return code
}
