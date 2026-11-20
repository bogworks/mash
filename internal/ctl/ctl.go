// Package ctl is the admin verb set shared by mashctl and the deprecated `mash admin`.
package ctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/sap"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	madmin "github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/store"
)

// CheckRevokeServers, when set (mashctl sets it), confirms before a deprovision changes anything that the revocation
// can reach every server. An error means nothing was revoked and nothing was changed.
var CheckRevokeServers func(ctx context.Context, cfg config.Config) error

// NotRevoked is the line printed when a command stopped before it revoked anything.
const NotRevoked = "nothing was revoked; the key is still valid"

// RevokeUsers revokes NATS user keys in the account JWT; mashctl sets it, which has the key store. The system account
// credentials come from MASH_SYS_CREDS or the init server's sys.creds, the server count from MASH_SERVERS. It stays nil
// in `mash admin`, which has neither: deprovision then only advises.
var RevokeUsers func(ctx context.Context, cfg config.Config, users []string, stdout io.Writer, now func() time.Time) error

// NoRevokeWarning is the stderr block for a removal with --no-revoke: the user key keeps working.
func NoRevokeWarning(name, user string) string {
	return "WARNING: --no-revoke: " + name + "'s NATS user key " + config.Clean(user) + " is NOT revoked and can still connect, publish and receive until it is revoked in the account JWT; credentials issued to it stay valid. (--no-revoke is a temporary flag name: it will be renamed with the CLI redesign.)\n"
}

// ShortKey is the first 8 characters of a key for messages, or all of it when it is shorter: a user key read from a
// directory record may be short or malformed, and slicing it must never panic.
func ShortKey(k string) string { return config.Clean(fmt.Sprintf("%.8s", k)) }

// splitRevokeFlags takes --no-revoke out of args (name and the rest keep their order).
func splitRevokeFlags(args []string) ([]string, bool, error) {
	noRevoke := false
	out := args[:0:0]
	for i := 0; i < len(args); i++ {
		a := args[i]
		if i < 2 {
			out = append(out, a)
			continue
		}
		switch {
		case a == "--no-revoke":
			noRevoke = true
		case strings.HasPrefix(a, "--no-revoke="):
			return nil, false, fmt.Errorf("--no-revoke takes no value")
		default:
			out = append(out, a)
		}
	}
	return out, noRevoke, nil
}

func limitsUsage(prog string) string {
	return "usage: " + prog + " limits [address...] | " + prog + " limits --show"
}

func provisionUsage(prog string) string {
	return "usage: " + prog + " provision <owner.group.instance> --user U --account A [--note N] [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]..."
}

// notAddress is the refusal of an argument that is not an endpoint address.
func notAddress(arg string) error {
	return fmt.Errorf("mash: not an endpoint address (the value is not shown): give owner.group.instance")
}

// parseAddr reads an endpoint address argument: the dotted form owner.group.instance or the flat key
// owner_group_instance. Anything else is refused.
func parseAddr(arg string) (sap.Address, error) {
	if a, err := sap.ParseAddress(arg); err == nil {
		return a, nil
	}
	if a, err := sap.AddressFromFlat(arg); err == nil {
		return a, nil
	}
	return sap.Address{}, notAddress(arg)
}

func suspendUsage(prog, verb string) string {
	return "usage: " + prog + " " + verb + " <owner.group.instance>"
}

func setMetaUsage(prog string) string {
	return "usage: " + prog + " set-meta <owner.group.instance> [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]... [--uncap C]...   (at least one; a value of - clears the field; --label k=- clears that label; --cap C adds a capability, --uncap C removes one, --cap - clears them all)"
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
	line("init --local [--dir D] [--owner O] [--port P] [--start]", "write the configuration of a local operator-mode NATS server with JetStream (loopback, TLS only), keys and credentials, for one developer; --start also starts it and creates the buckets")
	line("limits [address...]", "raise existing inboxes to the configured limits (config.json \"limits\"; never lowers)")
	line("limits --show", "print the effective limits")
	line("permissions <owner.group.instance> [--nsc]", "the NATS permissions an endpoint user needs")
	line("provision <owner.group.instance> --user U --account A [--note N] [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]...", "")
	line("set-meta <owner.group.instance> [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]... [--uncap C]...", "(a value of - clears the field; --label k=- clears that label; --cap C adds a capability, --uncap C removes one, --cap - clears them all)")
	line("suspend <owner.group.instance>", "refuse all inbound mail to an endpoint (its stored mail stays readable); prints the last sequence; the endpoint's credentials stay valid")
	line("resume <owner.group.instance>", "undo suspend")
	line("suspended <owner.group.instance>", "print whether an endpoint is suspended and its last sequence; exit 0 if suspended, 3 if not, 1 on error")
	removal := "remove the record and inbox;"
	if prog == "mashctl" {
		removal = "revoke the endpoint's user in the account JWT (--no-revoke skips it; --servers N, else MASH_SERVERS, is how many servers must acknowledge; --sys-creds FILE, else MASH_SYS_CREDS, the system account creds), then remove the record and inbox;"
	}
	line("deprovision <owner.group.instance> [--user U | --drained-to SEQ --suspension ID]"+map[bool]string{true: " [--servers N] [--sys-creds FILE]"}[prog == "mashctl"], removal+" with --user, only if the record's user is still U; with --drained-to and --suspension, only if still that suspension (ID) and at last_seq SEQ")
	line("list", "")
	if prog == "mashctl" { // verbs `mash admin` does not carry
		line("audit [--json] [--strict] [--creds-dir DIR]", "read only: list every directory record, inbox stream, lease and creds file with their state, the orphans; --strict exits 1 on an orphan, an unreadable item, or unread mail lost to eviction (a lower bound from consumer sequences: mail fetched but never shown, or redelivered, can make it undercount, so a clean audit does not prove nothing was lost)")
		line("inbox inspect <owner.group.instance> [--json] [--from SEQ] [--to SEQ] [--limit N]", "read only: one inbox's stored messages (default the last 20) with sender, trust, store time, size, kind and delivery state derived from the deliver consumer's cursors, and the consumer's totals; never creates, fetches from or acknowledges a consumer")
		adoptLines(line)
		issuedLines(line)
		endpointLines(line)
		labelLines(line)
	}
	return b.String()
}

func adoptLines(line func(left, right string)) {
	line("adopt --server URL --operator-key F --account-key F --sys-creds F [--account N] [--admin-creds F | --mint-admin] [--owner O] [--dry-run]", "take over an operator-mode server you already run: check your authority over it, then keep the keys and write the config; changes nothing on the server")
}

func issuedLines(line func(left, right string)) {
	line("issued list [--json]", "the issue record: every user JWT mashctl issued (public key, address, kind, tags, time, purpose, revoked time)")
	line("issued check [--json] [--sys-creds FILE] [--servers N]", "compare the user keys connected to every server with the record and report any key mash did not issue and any revoked key still connected; exit 1 on one, 2 when the check could not complete")
}

func endpointLines(line func(left, right string)) {
	line("endpoint create [ADDRESS] [--owner O] [--group G] [--name N] (--public-key FILE | --make-key) [--jwt-out FILE] [--label k=v]... [--tool] [--note N] [--describe T] [--cap C]... [--servers N] [--sys-creds FILE] [--dry-run] [--json]", "sign the endpoint's key and write its directory record, for example alice/dev/design")
	line("endpoint reissue ADDRESS (--public-key FILE | --make-key) [--jwt-out FILE] [--servers N] [--sys-creds FILE] [--dry-run] [--json]", "revoke the old key on every server and name a new one")
	line("endpoint delete ADDRESS [--delete-creds] [--force] [--no-revoke] [--dry-run] [--json] [--servers N] [--sys-creds FILE]", "revoke the endpoint's user key, remove its record and inbox, burn the address, rename its creds file")
	line("endpoint list [--json]", "the endpoints in the directory")
	line("endpoint show ADDRESS [--json]", "one endpoint's directory record, creds file path and issue record")
}

func labelLines(line func(left, right string)) {
	line("label <owner.group.instance> k=v... [--dry-run]", "set labels on the endpoint's directory record")
	line("unlabel <owner.group.instance> k... [--dry-run]", "remove labels from the endpoint's directory record")
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
		return nil, nil, nil, errors.New(pfx + ": no endpoint: set MASH_ENDPOINT or --endpoint (its owner part is the owner these commands act for)")
	}
	if cfg.URL == "" || cfg.AdminCreds == "" {
		return nil, nil, nil, errors.New(pfx + ": admin commands need MASH_SERVER and MASH_ADMIN_CREDS")
	}
	if raw, err := config.ReadCredsFile(cfg.AdminCreds); err != nil {
		var pe *fs.PathError
		if !errors.As(err, &pe) {
			return nil, nil, nil, err // a refusal of the file's type, mode or owner, in its own words
		}
		return nil, nil, nil, fmt.Errorf(pfx+": admin credentials: %w", err)
	} else if err := natsauth.CheckCreds(cfg.AdminCreds, raw); err != nil {
		return nil, nil, nil, err
	}
	tlsReq, warning, err := natsauth.RequireTLS(cfg.URL, cfg.InsecurePlaintext)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%s: %w", pfx, err)
	}
	if warning != "" {
		fmt.Fprintln(os.Stderr, warning)
	}
	nc, err := nats.Connect(cfg.URL, append(append(natsauth.TLSOptions(cfg.TLSCA, cfg.TLSCert, cfg.TLSKey), tlsReq...), natsauth.UserCredentials(cfg.AdminCreds), nats.Name(prog))...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf(pfx+": connect %s: %w", config.RedactURL(cfg.URL), config.RedactErr(natsauth.NoServersHint(natsauth.TLSHint(err)), cfg.URL))
	}
	a, err := madmin.New(nc, cfg.Owner)
	if err != nil {
		nc.Close()
		return nil, nil, nil, err
	}
	a = a.WithNameRule(cfg.NameRule)
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
	usage := func(err error) int { fmt.Fprintln(stderr, err); return 2 } // a misuse of the verb, not a failure of it
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
	if args[0] == "init" && len(args) > 1 {
		return usage(errors.New(initUsage(prog)))
	}
	if args[0] == "list" && len(args) > 1 {
		return usage(errors.New("usage: " + prog + " list"))
	}
	if cfg.Owner == "" {
		return fail(errors.New(pfx + ": no endpoint: set MASH_ENDPOINT or --endpoint (its owner part is the owner these commands act for)"))
	}
	if args[0] == "permissions" {
		if len(args) < 2 || len(args) > 3 || (len(args) == 3 && args[2] != "--nsc") {
			return usage(errors.New("usage: " + prog + " permissions <owner.group.instance> [--nsc]"))
		}
		addr, err := parseAddr(args[1])
		if err != nil {
			return usage(err)
		}
		// On stderr: the output is consumed whole (jq, mapfile), so a note on stdout would break it.
		fmt.Fprintln(stderr, permissionsNote(prog))
		if len(args) == 3 {
			// One argument per line: mapfile -t flags < <(mashctl permissions n --nsc)
			fmt.Fprintln(stdout, strings.Join(madmin.NSCFlags(addr), "\n"))
			return 0
		}
		b, _ := json.MarshalIndent(madmin.Permissions(addr), "", "  ")
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
	switch args[0] {
	case "init":
		if err := a.Init(ctx); err != nil {
			return fail(err)
		}
		if err := recordAdminUser(ctx, a, cfg); err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, pfx+": directory and lease buckets ready")
	case "provision":
		if len(args) < 2 {
			return usage(errors.New(provisionUsage(prog)))
		}
		addr, err := parseAddr(args[1])
		if err != nil {
			return usage(fmt.Errorf("%s\n%w", provisionUsage(prog), err))
		}
		opt := optOf(args)
		meta, err := provisionMeta(prog, args, opt)
		if err != nil {
			return usage(err)
		}
		if err := provisionKeyArgs(prog, opt("--user"), opt("--account")); err != nil {
			return usage(err)
		}
		rec, err := a.Provision(ctx, addr, opt("--user"), opt("--account"), opt("--note"), meta)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "provisioned %s: inbox inbox-%s, mail %s%s\n", config.Clean(addr.String()), config.Clean(addr.Flat()), config.Clean(rec.MailSubject), metaSuffix(rec))
	case "limits":
		var addrs []sap.Address
		for _, n := range args[1:] {
			addr, err := parseAddr(n)
			if err != nil {
				return usage(fmt.Errorf("%s\n%w", limitsUsage(prog), err))
			}
			addrs = append(addrs, addr)
		}
		lines, err := a.ApplyLimits(ctx, addrs...)
		for _, l := range lines {
			fmt.Fprintln(stdout, l)
		}
		if err != nil {
			return fail(err)
		}
	case "set-meta":
		if len(args) < 3 {
			return usage(errors.New(setMetaUsage(prog)))
		}
		addr, err := parseAddr(args[1])
		if err != nil {
			return usage(fmt.Errorf("%s\n%w", setMetaUsage(prog), err))
		}
		u, err := setMetaUpdate(prog, args)
		if err != nil {
			return usage(err)
		}
		rec, err := a.SetMeta(ctx, addr, u)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "updated %s: role %q, host %q, labels %s%s\n", config.Clean(addr.String()), config.Clean(rec.Role), config.Clean(rec.Host), labelList(rec.Labels), metaSuffix(rec))
	case "suspend", "resume", "suspended":
		if len(args) != 2 {
			return usage(errors.New(suspendUsage(prog, args[0])))
		}
		addr, err := parseAddr(args[1])
		if err != nil {
			return usage(fmt.Errorf("%s\n%w", suspendUsage(prog, args[0]), err))
		}
		switch args[0] {
		case "suspend":
			st, err := a.Suspend(ctx, addr)
			if err != nil {
				return fail(err)
			}
			fmt.Fprintf(stdout, "suspended %s: id %s, last_seq %d, msgs %d (new mail parked; the endpoint's credentials are still valid)\n", addr, config.Clean(st.ID), st.LastSeq, st.Msgs)
		case "resume":
			if err := a.Resume(ctx, addr); err != nil {
				return fail(err)
			}
			fmt.Fprintf(stdout, "resumed %s\n", addr)
		default:
			st, err := a.Suspended(ctx, addr)
			if err != nil {
				return fail(err)
			}
			if !st.Suspended {
				fmt.Fprintf(stdout, "%s is not suspended\n", addr)
				return 3
			}
			fmt.Fprintf(stdout, "suspended %s since %s: id %s, last_seq %d, msgs %d\n", addr, config.Clean(st.Since), config.Clean(st.ID), st.LastSeq, st.Msgs)
		}
	case "deprovision":
		deprovisionUsage := "usage: " + prog + " deprovision <owner.group.instance> [--user <public-key> | --drained-to SEQ --suspension ID]"
		var noRevoke bool
		if prog == "mashctl" {
			deprovisionUsage += " [--no-revoke] [--servers N] [--sys-creds FILE]"
			var err error
			if args, noRevoke, err = splitRevokeFlags(args); err != nil {
				return usage(errors.New(deprovisionUsage + "\n" + pfx + ": " + err.Error()))
			}
		}
		if len(args) < 2 {
			return usage(errors.New(deprovisionUsage))
		}
		addr, err := parseAddr(args[1])
		if err != nil {
			return usage(fmt.Errorf("%s\n%w", deprovisionUsage, err))
		}
		shown := addr.String()
		var rest []string
		if len(args) > 2 {
			rest = args[2:]
		}
		hasDrained, hasSusp := slices.Contains(rest, "--drained-to"), slices.Contains(rest, "--suspension")
		if (hasDrained || hasSusp) && slices.Contains(rest, "--user") {
			return usage(errors.New(deprovisionUsage + "\n" + pfx + ": --user and --drained-to cannot be combined"))
		}
		// The revocation runs inside the admin's removal, under the name's lock, after its guards passed and before
		// anything is removed (see madmin.RevokeHook): a refused removal revokes nothing, a failed revocation removes
		// nothing, and the key revoked is the one in the record the removal accepted.
		adm, revokedUser, revokeErr, checkErr := a, "", error(nil), error(nil)
		if RevokeUsers != nil && !noRevoke {
			adm = a.WithRevoker(func(ctx context.Context, user string) error {
				// One reading of the clock: the issue-record check and the revocation carry the same time.
				at := RevokeNow()
				if err := CheckNotFutureIssued(ctx, a, []RevokeTarget{{Name: shown, Key: user}}, at, stderr); err != nil {
					checkErr = err
					return err
				}
				if err := RevokeUsers(ctx, cfg, []string{user}, stdout, func() time.Time { return at }); err != nil {
					revokeErr = err
					return fmt.Errorf("user key %s...: %w", ShortKey(user), err)
				}
				revokedUser = user
				MarkRevoked(ctx, a, user, at, stderr)
				return nil
			})
		}
		// precheck confirms the revocation can reach every server before anything is changed.
		precheck := func() bool {
			if RevokeUsers == nil || noRevoke || CheckRevokeServers == nil {
				return true
			}
			if err := CheckRevokeServers(ctx, cfg); err != nil {
				fmt.Fprintf(stderr, "%s: deprovision %s: %v\n%s\n", pfx, shown, err, NotRevoked)
				return false
			}
			return true
		}
		// failRemoval reports a failed removal; a failed revocation also names the way around it.
		failRemoval := func(err error) int {
			if checkErr != nil && errors.Is(err, checkErr) {
				return fail(checkErr)
			}
			if revokeErr != nil && errors.Is(err, revokeErr) {
				return fail(fmt.Errorf("%w (--no-revoke deprovisions without revoking)", err))
			}
			if revokedUser != "" {
				fmt.Fprintf(stderr, "%s: deprovision %s: PARTIAL: revoked: user key %s... (the acknowledgement line above gives the server count); still pending: the removal of the directory record, the lease fence and the inbox; running the same command again finishes it\n", pfx, shown, ShortKey(revokedUser))
			} else if RevokeUsers != nil && !noRevoke {
				fmt.Fprintf(stderr, "%s: deprovision %s: %s\n", pfx, shown, NotRevoked)
			}
			return fail(err)
		}
		// note closes the success text.
		note := func() string {
			switch {
			case revokedUser != "":
				return "user key " + ShortKey(revokedUser) + "... revoked in the account JWT"
			case RevokeUsers == nil:
				return "revoke its NATS user with your operator tooling"
			case noRevoke:
				return "--no-revoke: its NATS user is not revoked; revoke it with your operator tooling"
			}
			return "no directory record, so no user key is known and nothing was revoked"
		}
		// noRevokeWarn prints the --no-revoke warning for user; the user key is informational.
		noRevokeWarn := func(user string) {
			if noRevoke && user != "" && RevokeUsers != nil {
				fmt.Fprint(stderr, NoRevokeWarning(shown, user))
			}
		}
		recordUser := func() string {
			recs, bad, err := a.List(ctx)
			if err != nil {
				return ""
			}
			warnBad(stderr, pfx, bad)
			for _, r := range recs {
				if ra, err := r.Address(); err == nil && ra == addr {
					return r.User
				}
			}
			return ""
		}
		if hasDrained || hasSusp {
			if len(args) != 6 || !hasDrained || !hasSusp {
				return usage(errors.New(deprovisionUsage + "\n" + pfx + ": --drained-to and --suspension go together (both from suspend's output)"))
			}
			var seqArg, id string
			for i := 2; i < 6; i += 2 {
				switch args[i] {
				case "--drained-to":
					seqArg = args[i+1]
				case "--suspension":
					id = args[i+1]
				}
			}
			seq, err := strconv.ParseUint(seqArg, 10, 64)
			if err != nil {
				return usage(errors.New(deprovisionUsage + "\n" + pfx + ": --drained-to wants a non-negative integer (the last_seq that suspend printed)"))
			}
			if id == "" {
				return usage(errors.New(deprovisionUsage + "\n" + pfx + ": --suspension wants the id that suspend printed"))
			}
			warnUser := ""
			if noRevoke {
				warnUser = recordUser()
				noRevokeWarn(warnUser)
			}
			if !precheck() {
				return 1
			}
			if err := adm.DeprovisionDrained(ctx, addr, id, seq); err != nil {
				return failRemoval(err)
			}
			fmt.Fprintf(stdout, "deprovisioned %s (suspension %s drained to %d; %s)\n", shown, id, seq, note())
			noRevokeWarn(warnUser)
			return 0
		}
		if len(args) != 2 && !(len(args) == 4 && args[2] == "--user") {
			return usage(errors.New(deprovisionUsage))
		}
		if len(args) == 4 {
			// A public user key (U + 55 base32 characters); the guard compares it with the record's user.
			if !validUserKeyShape(args[3]) {
				return usage(errors.New(deprovisionUsage + "\n" + pfx + ": --user wants a NATS user public key (U, then 55 upper-case base32 characters)"))
			}
			// The given key is revoked inside the removal, and only when the record names it.
			if !precheck() {
				return 1
			}
			noRevokeWarn(args[3])
			if err := adm.DeprovisionIfUser(ctx, addr, args[3]); err != nil {
				return failRemoval(err)
			}
			fmt.Fprintf(stdout, "deprovisioned %s (its record belonged to user key %s...; %s)\n", shown, ShortKey(args[3]), strings.Replace(note(), "user key "+ShortKey(args[3])+"... ", "", 1))
			noRevokeWarn(args[3])
			return 0
		}
		// Deprovision is idempotent, so rollbacks can call it freely; this verb refuses a name that was never there.
		if ok, err := a.Exists(ctx, addr); err != nil {
			if RevokeUsers != nil && !noRevoke {
				return fail(fmt.Errorf("%w\n%s", err, NotRevoked))
			}
			return fail(err)
		} else if !ok {
			fmt.Fprintf(stderr, "no endpoint named %s\n", shown)
			return 1
		}
		if !precheck() {
			return 1
		}
		warnUser := ""
		if noRevoke {
			warnUser = recordUser()
			noRevokeWarn(warnUser)
		}
		if err := adm.Deprovision(ctx, addr); err != nil {
			return failRemoval(err)
		}
		fmt.Fprintf(stdout, "deprovisioned %s (%s)\n", shown, note())
		noRevokeWarn(warnUser)
	case "list":
		recs, bad, err := a.List(ctx)
		if err != nil {
			return fail(err)
		}
		warnBad(stderr, pfx, bad)
		for _, r := range recs {
			fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", config.Clean(r.Owner+"."+r.Group+"."+r.Instance), config.Clean(r.User), config.Clean(r.Created), config.Clean(r.Note))
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

// optOf returns the lookup of a single-valued flag in args (from the third argument on).
func optOf(args []string) func(string) string {
	return func(name string) string {
		for i := 2; i+1 < len(args); i++ {
			if args[i] == name {
				return args[i+1]
			}
		}
		return ""
	}
}

// metaSuffix is the confirmation line's tail for the description and capabilities, empty when neither is set.
func metaSuffix(r store.Record) string {
	var b strings.Builder
	if r.Description != "" {
		fmt.Fprintf(&b, ", description %q", config.Clean(r.Description))
	}
	if len(r.Caps) > 0 {
		fmt.Fprintf(&b, ", caps %s", config.Clean(strings.Join(r.Caps, ",")))
	}
	return b.String()
}

// provisionMeta reads the optional metadata flags of `provision` and refuses any other flag, a flag without a value
// and a stray argument. opt returns a single-valued flag's value.
func provisionMeta(prog string, args []string, opt func(string) string) (madmin.Meta, error) {
	labels := map[string]string{}
	var caps []string
	bad := errors.New(provisionUsage(prog))
	seen := map[string]bool{}
	for i := 2; i < len(args); i += 2 {
		if i+1 >= len(args) {
			return madmin.Meta{}, bad
		}
		switch args[i] {
		case "--label":
			k, v, ok := strings.Cut(args[i+1], "=")
			if !ok || v == "-" {
				return madmin.Meta{}, bad
			}
			labels[k] = v
		case "--cap":
			caps = append(caps, args[i+1])
		case "--user", "--account", "--note", "--role", "--host", "--describe":
			if seen[args[i]] {
				return madmin.Meta{}, bad
			}
			seen[args[i]] = true
		default:
			return madmin.Meta{}, bad
		}
	}
	return madmin.Meta{Role: opt("--role"), Host: opt("--host"), Labels: labels, Description: opt("--describe"), Caps: caps}, nil
}

// provisionKeyArgs checks the keys provision needs before anything touches the server. An invalid one is named,
// never printed: a seed pasted into --user must not come back on the screen.
func provisionKeyArgs(prog, user, account string) error {
	use := provisionUsage(prog)
	switch {
	case user == "":
		return errors.New(use + "\n--user is required")
	case !nkeys.IsValidPublicUserKey(user):
		return errors.New(use + "\n--user is not a NATS user public key (U, then 55 upper-case base32 characters; the value is not shown)")
	case account == "":
		return errors.New(use + "\n--account is required")
	case !nkeys.IsValidPublicAccountKey(account):
		return errors.New(use + "\n--account is not a NATS account public key (A, then 55 upper-case base32 characters; the value is not shown)")
	}
	return nil
}

func initUsage(prog string) string {
	return "usage: " + prog + " init   |   " + prog + " init --local [--dir D] [--owner O] [--port P] [--start]"
}

// permissionsNote says what `permissions` prints.
func permissionsNote(prog string) string {
	return "note: these are the permissions of the endpoint's NATS user: the owner-line set for its address."
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
		keys[i] = config.Clean(k) + "=" + config.Clean(l[k])
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

// recordAdminUser puts the admin user of cfg's admin credentials in the issue record, kind "admin": mash's own admin
// user (the one `init --local` writes, or `adopt --mint-admin` mints) is issued outside `mint`, so it is recorded the
// first time init runs with it. Only the public key and the JWT's name and tags are read; the seed is never used.
func recordAdminUser(ctx context.Context, a *madmin.Admin, cfg config.Config) error {
	raw, err := config.ReadCredsFile(cfg.AdminCreds)
	if err != nil {
		return fmt.Errorf("mash: admin credentials: %w", err)
	}
	defer clear(raw)
	return RecordAdminCreds(ctx, a, cfg.Owner, raw)
}

// RecordAdminCreds puts the admin user held in the creds bytes raw in the issue record (kind admin, purpose issue), over
// a's connection. `mashctl init` uses it for the configured admin creds and `mashctl adopt --mint-admin` for the user it
// mints, before the creds file is written. Only the public key and the JWT's name, tags and issue time are read.
func RecordAdminCreds(ctx context.Context, a *madmin.Admin, owner string, raw []byte) error {
	tok, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		return errors.New("mash: admin credentials hold no readable JWT")
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		return errors.New("mash: admin credentials hold no readable user JWT")
	}
	issued := time.Unix(uc.IssuedAt, 0).UTC().Format(time.RFC3339)
	if uc.IssuedAt == 0 {
		issued = ""
	}
	return a.RecordIssued(ctx, store.Issued{PublicKey: uc.Subject, Address: uc.Name, Owner: owner, Kind: store.IssuedAdmin,
		Tags: []string(uc.Tags), IssuedAt: issued, Purpose: store.PurposeIssue})
}

// IssuedMarkRevoked is the write MarkRevoked makes; a test replaces it to make the write fail.
var IssuedMarkRevoked = func(ctx context.Context, a *madmin.Admin, user string, at time.Time) error {
	return a.MarkRevoked(ctx, user, at)
}

// MarkRevoked notes in the issue record that user's key was revoked at at. It runs after the revocation succeeded and
// is best effort: a key mash never recorded is no finding, any other failure is a warning on stderr, and nothing
// here fails or undoes the revocation.
func MarkRevoked(ctx context.Context, a *madmin.Admin, user string, at time.Time, stderr io.Writer) {
	err := IssuedMarkRevoked(ctx, a, user, at)
	if err == nil || errors.Is(err, madmin.ErrNotRecorded) {
		return
	}
	fmt.Fprintf(stderr, "warning: user key %s... is revoked, but the issue record was not updated with it: %v\n", ShortKey(user), err)
}

// RevokeNow is the clock a revocation takes its time from; a test replaces it.
var RevokeNow = time.Now

// RevokeTarget is a user key about to be revoked and the name to show for it in messages ("" shows the short key).
type RevokeTarget struct {
	Name, Key string
}

// CheckNotFutureIssued refuses a revocation at time at when the issue record shows a user's JWT issued after at: NATS
// honours a revocation only for JWTs issued at or before it, so that JWT would stay valid. It looks at every issue
// time the record retains of the key, not only the latest: a reissue that keeps the key writes a new revision with a
// new time, and after a clock rollback the newer revision can be the earlier time while an older, future-dated JWT is
// still held. A key whose history is at the bucket's cap (store.IssuedHistory) may have lost its oldest revisions, so
// it is refused unless a retained revision is already future-dated (the usual refusal). Every target is checked before
// the caller writes anything, and one refused key refuses them all; nothing has been changed when it returns an
// error. A key whose retained history cannot be shown to be complete from the bucket's marker
// (store.IssuedMarkerKey, madmin.IncompleteHistoryError) is refused too, with the way forward: revoke it by hand. A key with no record at all (the bucket or the key is absent: an older deployment, a key mash did not issue)
// gets one warning line on stderr and the revocation goes on. A record that exists but cannot be read, or whose
// issued_at is missing or not a time in any retained revision, cannot be ruled out and refuses the whole command.
func CheckNotFutureIssued(ctx context.Context, a *madmin.Admin, targets []RevokeTarget, at time.Time, stderr io.Writer) error {
	for _, t := range targets {
		times, found, err := a.IssuedHistoryByKey(ctx, t.Key)
		var bad *madmin.MalformedIssuedAtError
		if errors.As(err, &bad) {
			return fmt.Errorf("user key %s...: its issue record has a missing or malformed issued_at (%q) in revision %d, so a future-dated credential cannot be ruled out; restore the record with `mashctl endpoint reissue ADDRESS --public-key FILE`, giving the endpoint's current public key file (the same key is recorded again) and run again; nothing was changed", ShortKey(t.Key), bad.Value, bad.Revision)
		}
		var incomplete *madmin.IncompleteHistoryError
		if errors.As(err, &incomplete) {
			return fmt.Errorf("user key %s...: a future-dated credential cannot be ruled out: its issued history cannot be shown to be complete (%d revisions are kept and the record may have dropped older ones before its history limit was raised); revoke the key by hand in the account JWT, only after checking where it was issued; nothing was changed", ShortKey(t.Key), incomplete.Retained)
		}
		if err != nil {
			return fmt.Errorf("user key %s...: its issue record could not be read (%v), so a future-dated credential cannot be ruled out; retry, and if the record is damaged restore it with `mashctl endpoint reissue ADDRESS` (the same key is recorded again); nothing was changed", ShortKey(t.Key), err)
		}
		if !found {
			fmt.Fprintf(stderr, "warning: user key %s... has no issue record, so a future-dated credential cannot be ruled out; revoking anyway\n", ShortKey(t.Key))
			continue
		}
		who := t.Name
		if who == "" {
			who = ShortKey(t.Key) + "..."
		}
		if times.Max.After(at) {
			if times.Max.Equal(times.Latest) {
				return fmt.Errorf("%s's current creds JWT claims it was issued in the future (%s): the revocation would not cover it; fix this machine's clock (or the machine that minted it) and run again; nothing was changed", who, times.Max.Format(time.RFC3339))
			}
			return fmt.Errorf("%s: an earlier credential of this key was dated in the future (%s): the revocation would not cover it; wait until the revocation time is past that time, or fix this machine's clock (or the machine that minted it), and run again; nothing was changed", who, times.Max.Format(time.RFC3339))
		}
		if times.Truncated {
			return fmt.Errorf("%s: the key has been re-recorded %d times or more; its oldest issue times are no longer kept; cannot show that no earlier credential is dated in the future; run again once this machine's clock is known to be right and the revocation time is well past the newest recorded issue time (%s), or fix the clock; nothing was changed", who, store.IssuedHistory, times.Max.Format(time.RFC3339))
		}
	}
	return nil
}

// warnBad reports each directory record a list skipped because it fails store.ParseRecord: its key and why, on stderr.
func warnBad(stderr io.Writer, prog string, bad []madmin.BadRecord) {
	for _, b := range bad {
		b.Warn(stderr, prog)
	}
}
