package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	madmin "github.com/randybias/mash/internal/admin"
	"github.com/randybias/mash/internal/config"
)

func adminCmd(cfg config.Config, args []string, stdout, stderr io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
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
	if cfg.URL == "" || cfg.AdminCreds == "" {
		return fail(errors.New("mash: admin commands need MASH_URL and MASH_ADMIN_CREDS"))
	}
	nc, err := nats.Connect(cfg.URL, nats.UserCredentials(cfg.AdminCreds), nats.Name("mash admin"))
	if err != nil {
		return fail(fmt.Errorf("mash: connect %s: %w", cfg.URL, err))
	}
	defer nc.Close()
	a, err := madmin.New(nc, cfg.Owner)
	if err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
			return fail(errors.New("usage: mash admin provision <name> --user U --account A [--note N]"))
		}
		rec, err := a.Provision(ctx, args[1], opt("--user"), opt("--account"), opt("--note"))
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "provisioned %s: inbox inbox-%s, mail %s\n", rec.Name, rec.Name, rec.MailSubject)
	case "deprovision":
		if len(args) != 2 {
			return fail(errors.New("usage: mash admin deprovision <name>"))
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
