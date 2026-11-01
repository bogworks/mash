package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/natsauth"
	"github.com/nats-io/nats.go"

	madmin "github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/store"
)

const (
	limitsUsage    = "usage: mash admin limits [name...] | mash admin limits --show"
	provisionUsage = "usage: mash admin provision <name> --user U --account A [--note N] [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]..."
	setMetaUsage   = "usage: mash admin set-meta <name> [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]... [--uncap C]...   (at least one; a value of - clears the field; --label k=- clears that label; --cap C adds a capability, --uncap C removes one, --cap - clears them all)"
)

func adminCmd(cfg config.Config, args []string, stdout, stderr io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if cfg.Limits == (store.Limits{}) { // a Config not made by Load
		cfg.Limits = store.DefaultLimits()
	}
	if cfg.Owner == "" {
		return fail(errors.New("mash: no owner (config.json \"owner\" or MASH_OWNER)"))
	}
	if args[0] == "permissions" {
		if len(args) < 2 || !config.ValidName(args[1]) {
			return fail(errors.New("usage: mash admin permissions <name> [--nsc]"))
		}
		if len(args) == 3 && args[2] == "--nsc" {
			// One argument per line: mapfile -t flags < <(mash admin permissions n --nsc)
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
	if cfg.URL == "" || cfg.AdminCreds == "" {
		return fail(errors.New("mash: admin commands need MASH_URL and MASH_ADMIN_CREDS"))
	}
	if err := config.CheckCredsMode(cfg.AdminCreds); err != nil {
		return fail(err)
	}
	if raw, err := os.ReadFile(cfg.AdminCreds); err != nil {
		return fail(fmt.Errorf("mash: admin credentials: %w", err))
	} else if err := natsauth.CheckCreds(cfg.AdminCreds, raw); err != nil {
		return fail(err)
	}
	nc, err := nats.Connect(cfg.URL, append(natsauth.TLSOptions(cfg.TLSCA, cfg.TLSCert, cfg.TLSKey), natsauth.UserCredentials(cfg.AdminCreds), nats.Name("mash admin"))...)
	if err != nil {
		return fail(fmt.Errorf("mash: connect %s: %w", cfg.URL, natsauth.TLSHint(err)))
	}
	defer nc.Close()
	a, err := madmin.New(nc, cfg.Owner)
	if err != nil {
		return fail(err)
	}
	if err := a.SetLimits(cfg.Limits); err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), adminDeadline(cfg))
	defer cancel()

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
		fmt.Fprintln(stdout, "mash: directory and lease buckets ready")
	case "provision":
		if len(args) < 2 {
			return fail(errors.New(provisionUsage))
		}
		meta, err := provisionMeta(args, opt)
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
				return fail(errors.New(limitsUsage))
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
			return fail(errors.New(setMetaUsage))
		}
		u, err := setMetaUpdate(args)
		if err != nil {
			return fail(err)
		}
		rec, err := a.SetMeta(ctx, args[1], u)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "updated %s: role %q, host %q, labels %s%s\n", rec.Name, rec.Role, rec.Host, labelList(rec.Labels), metaSuffix(rec))
	case "deprovision":
		const deprovisionUsage = "usage: mash admin deprovision <name> [--user <public-key>]"
		if len(args) != 2 && !(len(args) == 4 && args[2] == "--user") {
			return fail(errors.New(deprovisionUsage))
		}
		if len(args) == 4 {
			// A public user key (U + 55 base32 characters); the guard compares it with the record's user.
			if !validUserKeyShape(args[3]) {
				return fail(errors.New(deprovisionUsage + "\nmash: --user wants a NATS user public key (U, then 55 upper-case base32 characters)"))
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
			fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", r.Name, r.User, r.Created, r.Note)
		}
	default:
		fmt.Fprintf(stderr, "mash: unknown admin command %q\n", args[0])
		return 2
	}
	return 0
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
	return b.String()
}

// provisionMeta reads the optional metadata flags of `provision`. opt returns a single-valued flag's value.
func provisionMeta(args []string, opt func(string) string) (madmin.Meta, error) {
	labels := map[string]string{}
	var caps []string
	for i := 2; i+1 < len(args); i++ {
		switch args[i] {
		case "--label":
			k, v, ok := strings.Cut(args[i+1], "=")
			if !ok || v == "-" {
				return madmin.Meta{}, errors.New(provisionUsage)
			}
			labels[k] = v
		case "--cap":
			caps = append(caps, args[i+1])
		}
	}
	return madmin.Meta{Role: opt("--role"), Host: opt("--host"), Labels: labels, Description: opt("--describe"), Caps: caps}, nil
}

// setMetaUpdate reads set-meta's flags (args[0] is "set-meta", args[1] the name) into a MetaUpdate.
func setMetaUpdate(args []string) (madmin.MetaUpdate, error) {
	var u madmin.MetaUpdate
	bad := errors.New(setMetaUsage)
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
