package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/store"
)

const unzoneUsage = "usage: mashctl unzone NAME [--sys-creds FILE] [--no-policy-check]\n  unzone does not reissue the endpoint's user; afterwards run: mashctl permissions NAME --nsc\n"

// runUnzone is `mashctl unzone` (ADR 0091): it makes a zoned record five-token, moves its inbox stream's subjects to the
// five-token mail subject without losing stored mail (admin.Unzone) and says how to reissue its permissions. It
// refuses when a zone policy is applied: mash builds no migration for one. Re-running it is safe at any point.
//
// The applied-policy check reads the account JWT, which needs the system account credentials and the key store. It
// fails safe: when either is not configured here, or the account JWT cannot be read or verified, the command refuses
// and changes nothing. Only --no-policy-check skips the check, and it says on stderr that it did.
func runUnzone(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	var name, sysCreds string
	var noPolicyCheck bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--no-policy-check":
			noPolicyCheck = true
		case a == "--sys-creds" && i+1 < len(args):
			i++
			sysCreds = args[i]
		case strings.HasPrefix(a, "--sys-creds=") && len(a) > len("--sys-creds="):
			sysCreds = strings.TrimPrefix(a, "--sys-creds=")
		case strings.HasPrefix(a, "-") || name != "":
			fmt.Fprintf(stderr, "mashctl: unzone: unknown %s\n%s", config.RejectedArg(a), unzoneUsage)
			return 2
		default:
			name = a
		}
	}
	if name == "" {
		fmt.Fprint(stderr, unzoneUsage)
		return 2
	}
	if !config.ValidName(name) {
		fmt.Fprintln(stderr, "mashctl: unzone: NAME is not a valid endpoint name: 1-48 lower-case letters, digits and single hyphens (the value is not shown)")
		return 2
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "mashctl: unzone %s: %s\n", name, fmt.Sprintf(format, a...))
		return 1
	}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if cfg.Owner == "" {
		return fail(`no owner (config.json "owner" or MASH_OWNER)`)
	}
	announceTarget(cfg, env, stderr, &sysTarget{sysCreds})

	// The applied-policy check comes first: a policy deployment is refused before anything changes.
	if noPolicyCheck {
		fmt.Fprintln(stderr, "mashctl: unzone: skipping the applied-zone-policy check (--no-policy-check): if a zone policy is applied, stay on the last release that has zones")
	} else if _, serr := sysCredsPath(sysCreds, env, cfg); serr != nil {
		return fail("cannot check whether a zone policy is applied (%v); configure the system credentials, or pass --no-policy-check to skip the check; nothing was changed", serr)
	} else if keys, kerr := openKeys(cfg, env); kerr != nil {
		return fail("cannot check whether a zone policy is applied (%v); configure the key store, or pass --no-policy-check to skip the check; nothing was changed", kerr)
	} else {
		servers, perr := parseServers(env)
		if perr != nil {
			return fail("%v", perr)
		}
		nc, cerr := connectSys(cfg, sysCreds, env)
		if cerr != nil {
			return fail("cannot tell whether a zone policy is applied: %v; nothing was changed", cerr)
		}
		ctx, cancel := context.WithTimeout(context.Background(), applyDeadline)
		rerr := apply.RefuseZonePolicy(ctx, apply.NATS{Conn: nc, Inventory: servers}, keys, servers, store.ErrZonePolicyApplied)
		cancel()
		nc.Close()
		if rerr != nil {
			return fail("refusing: %v (nothing was changed)", rerr)
		}
	}

	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return fail("%v", err)
	}
	defer closeAdmin()
	changed, err := adm.Unzone(ctx, name)
	if err != nil {
		return fail("%v", err)
	}
	if !changed {
		fmt.Fprintf(stdout, "%s is already five-token\n", name)
		return 0
	}
	fmt.Fprintf(stdout, "unzoned %s: its record and inbox are five-token and its stored mail is kept\n", name)
	fmt.Fprintf(stdout, "reissue its user with the five-token permissions (mash holds no user seed to do it): mashctl permissions %s --nsc\n", name)
	return 0
}
