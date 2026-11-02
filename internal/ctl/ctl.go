// Package ctl is the admin verb set shared by mashctl and the deprecated `mash admin`.
package ctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/natsauth"
	"github.com/nats-io/nats.go"

	madmin "github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/store"
)

func limitsUsage(prog string) string {
	return "usage: " + prog + " limits [name...] | " + prog + " limits --show"
}

func provisionUsage(prog string) string {
	return "usage: " + prog + " provision <name> --user U --account A [--note N] [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]... [--zone Z]"
}

func setZoneUsage(prog string) string {
	return "usage: " + prog + " set-zone <name> <zone>   (moves an existing endpoint into the zone: its inbox also takes the zone mail subject, mail to the old subject still arrives; a zone is 1-32 lower-case letters, digits and hyphens)"
}

func setMetaUsage(prog string) string {
	return "usage: " + prog + " set-meta <name> [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]... [--uncap C]...   (at least one; a value of - clears the field; --label k=- clears that label; --cap C adds a capability, --uncap C removes one, --cap - clears them all)"
}

// Usage is the full verb listing for prog ("mashctl" or "mash admin").
func Usage(prog string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "usage: %s <verb> [args]\n\n", prog)
	line := func(left, right string) {
		if right == "" {
			fmt.Fprintf(&b, "  %s\n", prog+" "+left)
			return
		}
		fmt.Fprintf(&b, "  %-44s %s\n", prog+" "+left, right)
	}
	line("init", "create the directory and lease buckets (or raise their caps to the configured limits)")
	line("init --local [--dir D] [--owner O] [--port P] [--start]", "write the configuration of a local operator-mode NATS server with JetStream (loopback, no TLS), keys and credentials, for one developer; --start also starts it and creates the buckets")
	line("limits [name...]", "raise existing inboxes to the configured limits (config.json \"limits\"; never lowers)")
	line("limits --show", "print the effective limits")
	line("permissions <name> [--nsc]", "the NATS permissions an endpoint user needs")
	line("provision <name> --user U --account A [--note N] [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]... [--zone Z]", "")
	line("set-meta <name> [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]... [--uncap C]...", "(a value of - clears the field; --label k=- clears that label; --cap C adds a capability, --uncap C removes one, --cap - clears them all)")
	line("set-zone <name> <zone>", "move an endpoint into a zone; mail to its old subject still arrives")
	line("deprovision <name> [--user U]", "remove the record and inbox; with --user, only if the record's user is still U")
	line("list", "")
	if prog == "mashctl" { // verbs `mash admin` does not carry
		mintLines(line)
		retireLines(line)
		policyLines(line)
	}
	return b.String()
}

func mintLines(line func(left, right string)) {
	line("add NAME [--label k=v]... [--tool] [--zone Z] [--policy FILE] [--note N]", "declare NAME in the policy file, mint its user, provision it and write its creds file")
	line("reissue NAME... | --changed [--policy FILE]", "mint NAME's user JWT again from the policy (same user key, offline)")
}

func retireLines(line func(left, right string)) {
	line("retire NAME [--policy FILE] [--delete-creds] [--force]", "remove NAME's record and inbox, then its policy entry; its user is not revoked")
	line("label NAME k=v... [--policy FILE] [--dry-run] [--sync]", "set labels in the policy file (offline unless --sync updates the directory record)")
	line("unlabel NAME k... [--policy FILE] [--dry-run] [--sync]", "remove labels from the policy file (offline unless --sync)")
}

func policyLines(line func(left, right string)) {
	line("policy check [--policy FILE]", "validate the policy file (offline; FILE, else MASH_POLICY, else <config dir>/policy.yaml)")
	line("policy test [--policy FILE]", "run the file's tests")
	line("policy explain FROM TO [--policy FILE]", "why FROM may or may not mail TO")
	line("policy show [--json] [--budget] [--policy FILE]", "who may mail whom (needs the owner); --budget checks the JWT sizes")
	line("policy drift [--policy FILE] [--sys-creds FILE] [--creds-dir DIR] [--json]", "compare the file with the account, the key store, the directory and the creds files; fixes nothing")
	line("policy repair [--policy FILE] [--sys-creds FILE] [--servers N] [...]", "the additive repairs of drift: zone keys, the account push, directory zone and labels; lists stale creds")
}

// OpenAdmin connects to the server with the admin credentials and returns the admin handle, a context carrying the
// command's deadline (admin_timeout, else 30 seconds) and a func that releases both. prog is the command prefix of
// error text. The caller must call the func when done.
func OpenAdmin(prog string, cfg config.Config) (*madmin.Admin, context.Context, func(), error) {
	pfx, _, _ := strings.Cut(prog, " ")
	if cfg.Limits == (store.Limits{}) { // a Config not made by Load
		cfg.Limits = store.DefaultLimits()
	}
	if cfg.Owner == "" {
		return nil, nil, nil, errors.New(pfx + ": no owner (config.json \"owner\" or MASH_OWNER)")
	}
	if cfg.URL == "" || cfg.AdminCreds == "" {
		return nil, nil, nil, errors.New(pfx + ": admin commands need MASH_URL and MASH_ADMIN_CREDS")
	}
	if err := config.CheckCredsMode(cfg.AdminCreds); err != nil {
		return nil, nil, nil, err
	}
	if raw, err := os.ReadFile(cfg.AdminCreds); err != nil {
		return nil, nil, nil, fmt.Errorf(pfx+": admin credentials: %w", err)
	} else if err := natsauth.CheckCreds(cfg.AdminCreds, raw); err != nil {
		return nil, nil, nil, err
	}
	nc, err := nats.Connect(cfg.URL, append(natsauth.TLSOptions(cfg.TLSCA, cfg.TLSCert, cfg.TLSKey), natsauth.UserCredentials(cfg.AdminCreds), nats.Name(prog))...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf(pfx+": connect %s: %w", cfg.URL, natsauth.TLSHint(err))
	}
	a, err := madmin.New(nc, cfg.Owner)
	if err != nil {
		nc.Close()
		return nil, nil, nil, err
	}
	if err := a.SetLimits(cfg.Limits); err != nil {
		nc.Close()
		return nil, nil, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), adminDeadline(cfg))
	return a, ctx, func() { cancel(); nc.Close() }, nil
}

// Run executes one admin verb. prog is the command prefix used in usage and error text: "mashctl" or "mash admin".
func Run(prog string, cfg config.Config, args []string, stdout, stderr io.Writer) int {
	pfx, _, _ := strings.Cut(prog, " ") // "mash" or "mashctl", the prefix of error lines
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	if len(args) == 0 {
		fmt.Fprint(stderr, Usage(prog))
		return 2
	}
	if args[0] == "init" && slices.Contains(args[1:], "--local") {
		return runInitLocal(prog, cfg, args[1:], stdout, stderr)
	}
	if cfg.Limits == (store.Limits{}) { // a Config not made by Load
		cfg.Limits = store.DefaultLimits()
	}
	if cfg.Owner == "" {
		return fail(errors.New(pfx + ": no owner (config.json \"owner\" or MASH_OWNER)"))
	}
	if args[0] == "permissions" {
		if len(args) < 2 || !config.ValidName(args[1]) {
			return fail(errors.New("usage: " + prog + " permissions <name> [--nsc]"))
		}
		if len(args) == 3 && args[2] == "--nsc" {
			// One argument per line: mapfile -t flags < <(mashctl permissions n --nsc)
			fmt.Fprintln(stdout, strings.Join(madmin.NSCFlags(cfg.Owner, args[1]), "\n"))
			return 0
		}
		b, _ := json.MarshalIndent(madmin.Permissions(cfg.Owner, args[1]), "", "  ")
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	if args[0] == "limits" && len(args) == 2 && args[1] == "--show" {
		l := cfg.Limits
		fmt.Fprintf(stdout, "inbox_max_bytes %s\ninbox_max_age %s\ndirectory_max_bytes %s\nlease_max_bytes %s\ninbox_max_consumers %d\n",
			config.FormatSize(l.InboxMaxBytes), config.FormatAge(l.InboxMaxAge), config.FormatSize(l.DirectoryMaxBytes),
			config.FormatSize(l.LeaseMaxBytes), l.InboxMaxConsumers)
		return 0
	}
	a, ctx, closeAdmin, err := OpenAdmin(prog, cfg)
	if err != nil {
		return fail(err)
	}
	defer closeAdmin()

	opt := func(name string) string {
		for i := 2; i+1 < len(args); i++ {
			if args[i] == name {
				return args[i+1]
			}
		}
		return ""
	}
	switch args[0] {
	case "init":
		if err := a.Init(ctx); err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, pfx+": directory and lease buckets ready")
	case "provision":
		if len(args) < 2 {
			return fail(errors.New(provisionUsage(prog)))
		}
		meta, err := provisionMeta(prog, args, opt)
		if err != nil {
			return fail(err)
		}
		rec, err := a.Provision(ctx, args[1], opt("--user"), opt("--account"), opt("--note"), meta)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "provisioned %s: inbox inbox-%s, mail %s%s\n", rec.Name, rec.Name, rec.MailSubject, metaSuffix(rec))
	case "limits":
		for _, n := range args[1:] {
			if !config.ValidName(n) {
				return fail(errors.New(limitsUsage(prog)))
			}
		}
		lines, err := a.ApplyLimits(ctx, args[1:]...)
		for _, l := range lines {
			fmt.Fprintln(stdout, l)
		}
		if err != nil {
			return fail(err)
		}
	case "set-meta":
		if len(args) < 3 || !config.ValidName(args[1]) {
			return fail(errors.New(setMetaUsage(prog)))
		}
		u, err := setMetaUpdate(prog, args)
		if err != nil {
			return fail(err)
		}
		rec, err := a.SetMeta(ctx, args[1], u)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "updated %s: role %q, host %q, labels %s%s\n", rec.Name, rec.Role, rec.Host, labelList(rec.Labels), metaSuffix(rec))
	case "set-zone":
		if len(args) != 3 || !config.ValidName(args[1]) {
			return fail(errors.New(setZoneUsage(prog)))
		}
		if args[2] == "-" {
			return fail(errors.New(pfx + ": set-zone: an endpoint in a zone cannot return to the legacy subject in this version (the zone - is refused)"))
		}
		rec, err := a.SetZone(ctx, args[1], args[2])
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "updated %s: zone %s, mail %s (the old subject still arrives)\n", rec.Name, rec.Zone, rec.MailSubject)
	case "deprovision":
		deprovisionUsage := "usage: " + prog + " deprovision <name> [--user <public-key>]"
		if len(args) != 2 && !(len(args) == 4 && args[2] == "--user") {
			return fail(errors.New(deprovisionUsage))
		}
		if len(args) == 4 {
			// A public user key (U + 55 base32 characters); the guard compares it with the record's user.
			if !validUserKeyShape(args[3]) {
				return fail(errors.New(deprovisionUsage + "\n" + pfx + ": --user wants a NATS user public key (U, then 55 upper-case base32 characters)"))
			}
			if err := a.DeprovisionIfUser(ctx, args[1], args[3]); err != nil {
				return fail(err)
			}
			fmt.Fprintf(stdout, "deprovisioned %s (its record belonged to user key %s...; revoke its NATS user with your operator tooling)\n", args[1], args[3][:8])
			return 0
		}
		if err := a.Deprovision(ctx, args[1]); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "deprovisioned %s (revoke its NATS user with your operator tooling)\n", args[1])
	case "list":
		recs, err := a.List(ctx)
		if err != nil {
			return fail(err)
		}
		for _, r := range recs {
			fmt.Fprintf(stdout, "%s\t%s\t%s\t%s%s\n", r.Name, r.User, r.Created, r.Note, zoneSuffix(r))
		}
	default:
		if prog == "mash admin" {
			fmt.Fprintf(stderr, "mash: unknown admin command %q\n", args[0])
		} else {
			fmt.Fprintf(stderr, "%s: unknown command %q\n\n%s", prog, args[0], Usage(prog))
		}
		return 2
	}
	return 0
}

// zoneSuffix is a list line's tail for the zone: a tab and zone=<z>, empty for an endpoint with the legacy subject.
func zoneSuffix(r store.Record) string {
	if r.Zone == "" {
		return ""
	}
	return "\tzone=" + r.Zone
}

// metaSuffix is the confirmation line's tail for the description and capabilities, empty when neither is set.
func metaSuffix(r store.Record) string {
	var b strings.Builder
	if r.Description != "" {
		fmt.Fprintf(&b, ", description %q", r.Description)
	}
	if len(r.Caps) > 0 {
		fmt.Fprintf(&b, ", caps %s", strings.Join(r.Caps, ","))
	}
	if r.Zone != "" {
		fmt.Fprintf(&b, ", zone %s", r.Zone)
	}
	return b.String()
}

// provisionMeta reads the optional metadata flags of `provision`. opt returns a single-valued flag's value.
func provisionMeta(prog string, args []string, opt func(string) string) (madmin.Meta, error) {
	labels := map[string]string{}
	var caps []string
	for i := 2; i+1 < len(args); i++ {
		switch args[i] {
		case "--label":
			k, v, ok := strings.Cut(args[i+1], "=")
			if !ok || v == "-" {
				return madmin.Meta{}, errors.New(provisionUsage(prog))
			}
			labels[k] = v
		case "--cap":
			caps = append(caps, args[i+1])
		}
	}
	return madmin.Meta{Role: opt("--role"), Host: opt("--host"), Labels: labels, Description: opt("--describe"), Caps: caps, Zone: opt("--zone")}, nil
}

// setMetaUpdate reads set-meta's flags (args[0] is "set-meta", args[1] the name) into a MetaUpdate.
func setMetaUpdate(prog string, args []string) (madmin.MetaUpdate, error) {
	var u madmin.MetaUpdate
	bad := errors.New(setMetaUsage(prog))
	dst := map[string]**string{"--role": &u.Role, "--host": &u.Host, "--describe": &u.Description}
	for i := 2; i < len(args); i += 2 {
		if i+1 >= len(args) {
			return u, bad
		}
		val := args[i+1]
		switch args[i] {
		case "--label":
			k, v, ok := strings.Cut(val, "=")
			if !ok {
				return u, bad
			}
			if v == "-" {
				v = ""
			}
			if u.Labels == nil {
				u.Labels = map[string]string{}
			}
			if _, dup := u.Labels[k]; dup {
				return u, bad
			}
			u.Labels[k] = v
		case "--cap":
			if val == "-" {
				u.ClearCaps = true
			} else {
				u.AddCaps = append(u.AddCaps, val)
			}
		case "--uncap":
			u.DropCaps = append(u.DropCaps, val)
		default:
			p, ok := dst[args[i]]
			if !ok || *p != nil {
				return u, bad
			}
			if val == "-" {
				val = ""
			}
			*p = &val
		}
	}
	return u, nil
}

// labelList renders labels sorted as k=v, space separated; "none" when there are none.
func labelList(l map[string]string) string {
	if len(l) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		keys[i] = k + "=" + l[k]
	}
	return strings.Join(keys, " ")
}

// adminDeadline is the total deadline of one admin command: admin_timeout, else 30 seconds.
func adminDeadline(cfg config.Config) time.Duration {
	if cfg.AdminTimeout > 0 {
		return cfg.AdminTimeout
	}
	return config.DefaultAdminTimeout
}

// validUserKeyShape is a light shape check for a public NATS user key: U, then base32 upper-case, 56 characters in
// all. The directory comparison, not this, decides ownership.
func validUserKeyShape(k string) bool {
	if len(k) != 56 || k[0] != 'U' {
		return false
	}
	for _, c := range k {
		if !(c >= 'A' && c <= 'Z' || c >= '2' && c <= '7') {
			return false
		}
	}
	return true
}
