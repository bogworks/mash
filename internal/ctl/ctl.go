// Package ctl is the admin verb set of mashctl.
package ctl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/sap"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"

	madmin "github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/store"
)

// NotRevoked is the line printed when a command stopped before it revoked anything.
const NotRevoked = "nothing was revoked; the key is still valid"

// NoRevokeWarning is the stderr block for a removal with --no-revoke: the user key keeps working.
func NoRevokeWarning(name, user string) string {
	return "WARNING: --no-revoke: " + name + "'s NATS user key " + config.Clean(user) + " is NOT revoked and can still connect, publish and receive until it is revoked in the account JWT; credentials issued to it stay valid. (--no-revoke is a temporary flag name: it will be renamed with the CLI redesign.)\n"
}

// ShortKey is the first 8 characters of a key for messages, or all of it when it is shorter: a user key read from a
// directory record may be short or malformed, and slicing it must never panic.
func ShortKey(k string) string { return config.Clean(fmt.Sprintf("%.8s", k)) }

func limitsUsage(prog string) string {
	return "usage: " + prog + " limits [address...] | " + prog + " limits --show"
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

// Usage is the full verb listing for prog.
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
	if prog == "mashctl" {
		line("audit [--json] [--strict] [--creds-dir DIR]", "read only: list every directory record, inbox stream, lease and creds file with their state, the orphans; --strict exits 1 on an orphan, an unreadable item, or unread mail lost to eviction (a lower bound from consumer sequences: mail fetched but never shown, or redelivered, can make it undercount, so a clean audit does not prove nothing was lost)")
		adoptLines(line)
		issuedLines(line)
		endpointLines(line)
		line("version [--json]", "print the version and the commit")
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
	line("endpoint list [prefix] [--wide]", "the endpoints in the directory")
	line("endpoint show ADDRESS", "one endpoint: its state, labels and public key")
	line("endpoint create [ADDRESS] [--owner O] [--group G] [--name N] (--public-key FILE | --make-key) [--jwt-out FILE] [--label k=v]... [--tool] [--note N] [--describe T] [--cap C]... [--servers N] [--sys-creds FILE] [--dry-run] [--json]", "sign the endpoint's key and write its directory record, for example acme.demo.bob")
	line("endpoint reissue ADDRESS (--public-key FILE | --make-key) [--jwt-out FILE] [--servers N] [--sys-creds FILE] [--dry-run] [--json]", "revoke the old key on every server and name a new one")
	line("endpoint delete ADDRESS [--delete-creds] [--force] [--no-revoke] [--dry-run] [--json] [--servers N] [--sys-creds FILE]", "revoke the endpoint's user key, remove its record and inbox, burn the address, rename its creds file")
	line("endpoint set ADDRESS key=value... [--dry-run]", "set role, description, host, capability or label.NAME")
	line("endpoint unset ADDRESS key... [--dry-run]", "remove those values")
	line("endpoint suspend ADDRESS [--dry-run]", "refuse new mail to an endpoint; its stored mail and credentials stay")
	line("endpoint resume ADDRESS [--dry-run]", "undo suspend")
	line("endpoint inbox ADDRESS [--limit n]", "what is in an endpoint's inbox, without any message text")
	line("endpoint permissions ADDRESS", "the NATS permissions an endpoint's user needs")
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
		return nil, nil, nil, errors.New(pfx + ": no owner: set \"owner\" in config.json, or MASH_ENDPOINT or --endpoint (its owner part is the owner these commands act for)")
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

// Run executes one admin verb. prog is the command prefix used in usage and error text.
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
	if cfg.Owner == "" {
		return fail(errors.New(pfx + ": no owner: set \"owner\" in config.json, or MASH_ENDPOINT or --endpoint (its owner part is the owner these commands act for)"))
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
	default:
		fmt.Fprintf(stderr, "%s: unknown command %q\n\n%s", prog, args[0], Usage(prog))
		return 2
	}
	return 0
}

func initUsage(prog string) string {
	return "usage: " + prog + " init   |   " + prog + " init --local [--dir D] [--owner O] [--port P] [--start]"
}

// adminDeadline is the total deadline of one admin command: admin_timeout, else 30 seconds.
func adminDeadline(cfg config.Config) time.Duration {
	if cfg.AdminTimeout > 0 {
		return cfg.AdminTimeout
	}
	return config.DefaultAdminTimeout
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
