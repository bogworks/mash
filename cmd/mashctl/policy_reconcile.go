package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policydoc"
	"github.com/bogworks/mash/internal/reconcile"
)

const policyReconcileUsage = `usage: mashctl policy reconcile [--json] [--yes] [--listset FILE] [--range R] [--bucket NAME] [--account KEY] [--sys-creds FILE] [--servers N]
       (run after the first applied list set of an adopted account: prints the plan that removes what the account held
       before it and the list set does not own, and changes nothing. The plan reports each signing key that is not one of
       the list set's scoped keys and still present, revokes recorded issued users whose sender is not in the list set
       and connected keys that are neither recorded, exempt nor issued under the list set, and reports anything else it
       cannot name as a finding. --listset FILE previews the signing keys the first apply would remove, before it. Exit
       0 when the plan is computed; 1 when an admin or exempt user is signed by a signing key that does not stay, or on
       a failure. --yes is refused in this version: no signer is configured)
`

// policyReconcileNoSigner is what --yes says until a signer is wired: the executor (internal/reconcile) is built and
// tested, and this is the one place a caller will hand it a signer.
const policyReconcileNoSigner = "mashctl: policy reconcile: no signer configured: run without --yes to see the plan"

type reconcileOpts struct {
	json, yes                     bool
	listset, rng, bucket, account string
	sysCreds                      string
	servers                       int
}

func parseReconcileArgs(args []string, env func(string) string) (reconcileOpts, error) {
	var o reconcileOpts
	serversText := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, inline, hasInline := strings.Cut(a, "=")
		val := func() (string, error) {
			switch {
			case hasInline && inline != "":
				return inline, nil
			case !hasInline && i+1 < len(args) && args[i+1] != "":
				i++
				return args[i], nil
			}
			return "", fmt.Errorf("%s needs a value", name)
		}
		var err error
		switch name {
		case "--json", "--yes":
			if hasInline {
				return o, fmt.Errorf("unexpected %s", config.RejectedArg(a))
			}
			if name == "--json" {
				o.json = true
			} else {
				o.yes = true
			}
		case "--listset":
			o.listset, err = val()
		case "--range":
			o.rng, err = val()
		case "--bucket":
			o.bucket, err = val()
		case "--account":
			o.account, err = val()
		case "--sys-creds":
			o.sysCreds, err = val()
		case "--servers":
			serversText, err = val()
		default: // a mistyped argument may be a secret: it is named, never printed
			return o, fmt.Errorf("unexpected %s", config.RejectedArg(a))
		}
		if err != nil {
			return o, err
		}
	}
	src := "--servers"
	if serversText == "" {
		serversText, src = env("MASH_SERVERS"), "MASH_SERVERS"
	}
	if serversText != "" {
		n, err := strconv.Atoi(strings.TrimSpace(serversText))
		if err != nil || n < 1 {
			return o, fmt.Errorf("%s must be a whole number of servers, 1 or more", src)
		}
		o.servers = n
	}
	return o, nil
}

// runPolicyReconcile is `mashctl policy reconcile`: the dry run of the first-list-set reconcile (AT-015). It needs the
// admin credentials (the marker, the issue record, the ledger) and the system account credentials (the connections and,
// for --listset, the account JWT the servers hold). It takes no lease and writes nothing.
func runPolicyReconcile(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	const pfx = "mashctl: policy reconcile: "
	o, err := parseReconcileArgs(args, env)
	if err != nil {
		fmt.Fprintf(stderr, "%s%v\n%s", pfx, err, policyReconcileUsage)
		return 2
	}
	if o.rng != "" && !config.ValidName(o.rng) { // named, never printed
		fmt.Fprintf(stderr, "%s--range is not a valid range name: 1-48 lower-case letters, digits and single hyphens (the value is not shown)\n%s", pfx, policyReconcileUsage)
		return 2
	}
	if o.account != "" && !nkeys.IsValidPublicAccountKey(o.account) {
		fmt.Fprintf(stderr, "%s--account is not an account public key (the value is not shown)\n%s", pfx, policyReconcileUsage)
		return 2
	}
	if o.yes {
		fmt.Fprintln(stderr, policyReconcileNoSigner)
		return 1
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "%s%s\n", pfx, fmt.Sprintf(format, a...))
		return 1
	}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	announceTarget(cfg, env, stderr, &sysTarget{o.sysCreds})
	keys, err := openKeys(cfg, env)
	if err != nil {
		return fail("%v", err)
	}
	account := o.account
	if account == "" {
		akp, pub, err := keys.Account()
		if err != nil {
			return fail("no account key: pass --account KEY or run `mashctl init` (%v)", err)
		}
		akp.Wipe()
		account = pub
	}
	held, err := keys.GroupPublics()
	if err != nil {
		return fail("%v", err)
	}
	adminUser, err := ctl.AdminUserKey(cfg)
	if err != nil {
		return fail("%v", err)
	}
	adminIssuer, err := credsIssuer(cfg.AdminCreds)
	if err != nil {
		return fail("admin credentials: %s", config.Clean(err.Error()))
	}
	var fileDoc *policydoc.Document
	if o.listset != "" {
		b, err := os.ReadFile(o.listset)
		if err != nil {
			return fail("%v", err)
		}
		doc, err := policydoc.Decode(b, policydoc.DecodeOptions{}) // a preview: no signature is checked
		if err != nil {
			return fail("%v", err)
		}
		if doc.Account != account {
			return fail("the list set names another account than this one; nothing was computed")
		}
		fileDoc = &doc
	}

	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return fail("%v", err)
	}
	defer closeAdmin()
	marker, adopted, err := adm.Adoption(ctx)
	switch {
	case err != nil:
		return fail("cannot tell whether this account is adopted: %v", err)
	case !adopted:
		return fail("this account is not adopted: there is nothing to reconcile (see `mashctl policy adopt`)")
	case marker.Account != account:
		return fail("the adoption marker is for another account than this one; nothing was computed")
	case !marker.ExemptRecorded():
		return fail("the adoption marker records no exempt keys: run `mashctl policy adopt` again before the reconcile")
	}

	in := reconcile.Input{Account: account, Held: held, Now: time.Now(), Exempt: marker.Exempt, Issued: adm, AdminUser: adminUser, AdminIssuer: adminIssuer}
	nc, err := connectSys(cfg, o.sysCreds, env)
	if err != nil {
		return fail("%v", err)
	}
	defer nc.Close()
	sctx, cancel := context.WithTimeout(ctx, applyDeadline)
	defer cancel()

	if fileDoc != nil {
		in.Source, in.ListSet = reconcile.SourceListSetFile, fileDoc.ListSet
		in.Claims, err = heldAccountClaims(sctx, nc, account, o.servers)
		if err != nil {
			return fail("%v", err)
		}
	} else {
		rec, ls, jwtIn, err := readInForce(ctx, adm.Conn(), account, o, env)
		if err != nil {
			return fail("%v", err)
		}
		in.Source, in.Generation, in.ListSet = reconcile.SourceInForce, rec.Generation, ls
		if in.Claims, err = jwt.DecodeAccountClaims(jwtIn); err != nil {
			return fail("the account JWT in force does not decode: %v", err)
		}
	}
	groups, err := reconcile.Groups(in.ListSet)
	if err != nil {
		return fail("the list set: %v", err)
	}
	if in.Signers, err = reconcile.ExpectedSigners(groups, held); err != nil {
		return fail("%v", err)
	}
	scan, err := scanConnections(sctx, nc, account, o.servers)
	if err != nil {
		return fail("%v", err)
	}
	in.Servers, in.ScanIncomplete = scan.Conns, scan.Incomplete

	plan, err := reconcile.Build(ctx, in)
	if err != nil {
		return fail("%v", err)
	}
	if o.json {
		b, err := plan.JSON()
		if err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(stdout, "%s\n", b)
	} else {
		fmt.Fprint(stdout, plan.Text())
	}
	if e := plan.IssuerErr(); e != nil {
		fmt.Fprintf(stderr, "%s%v\n", pfx, e)
		return 1
	}
	return 0
}

// reconcileBucket names the policy bucket: --bucket, else the MASH_POLICY_BUCKET setting as a pattern with --range
// filling {range}. It never prints a value it was given.
func reconcileBucket(o reconcileOpts, env func(string) string) (string, error) {
	pattern, source := o.bucket, "--bucket"
	if pattern == "" {
		pattern, source = env(policyapply.PolicyBucketEnv), policyapply.PolicyBucketEnv
	}
	if pattern == "" {
		return "", fmt.Errorf("no policy bucket: set %s to the bucket name (it may contain {range}) or pass --bucket NAME", policyapply.PolicyBucketEnv)
	}
	hasRange := strings.Contains(pattern, "{range}")
	switch {
	case hasRange && o.rng == "":
		return "", fmt.Errorf("no range: %s has {range}, so pass --range RANGE", source)
	case !hasRange && o.rng != "":
		return "", fmt.Errorf("--range is given but %s has no {range} (the value is not shown)", source)
	}
	name, err := policyapply.PolicyBucket(pattern, o.rng)
	if err != nil { // the error quotes the value: name the source only
		return "", fmt.Errorf("%s is not a valid KV bucket name (letters, digits, '-' and '_', and {range}) (the value is not shown)", source)
	}
	return name, nil
}

// readInForce opens the account's ledger in the range's policy bucket and reads the list set and the account JWT in
// force. It is read only: it takes no lease and writes nothing.
func readInForce(ctx context.Context, nc *nats.Conn, account string, o reconcileOpts, env func(string) string) (listset.Record, listset.ListSet, string, error) {
	bucket, err := reconcileBucket(o, env)
	if err != nil {
		return listset.Record{}, listset.ListSet{}, "", err
	}
	js, err := jetstream.New(nc)
	if err != nil {
		return listset.Record{}, listset.ListSet{}, "", err
	}
	l, err := policyapply.NewKVLedger(ctx, js, account, policyapply.KVLedgerOptions{Bucket: bucket})
	if err != nil {
		return listset.Record{}, listset.ListSet{}, "", err
	}
	var (
		rec listset.Record
		ls  listset.ListSet
		tok string
	)
	err = readStable(func() (uint64, error) {
		r, err := l.InForce()
		return r.Generation, err
	}, func() error {
		var ok bool
		var err error
		if rec, ls, ok, err = l.InForceView(); err != nil {
			return err
		}
		if !ok || rec.Generation == 0 {
			return errors.New("no list set is in force: apply the first list set, or pass --listset FILE to preview the signing keys the first apply would remove")
		}
		if tok, err = l.InForceJWT(); err != nil {
			return err
		}
		if tok == "" {
			return fmt.Errorf("the ledger stores no account JWT for generation %d", rec.Generation)
		}
		return nil
	})
	if err != nil {
		return rec, ls, "", err
	}
	return rec, ls, tok, nil
}

// readStable runs read between two reads of the generation in force. The list set and the JWT are separate ledger
// reads, so a generation that changed between them could pair one generation's list set with another's JWT: it
// reads again, up to 3 times, then fails. (The executor rebuilds the plan under the lease.)
func readStable(generation func() (uint64, error), read func() error) error {
	for range 3 {
		before, err := generation()
		if err != nil {
			return err
		}
		if err := read(); err != nil {
			return err
		}
		after, err := generation()
		if err != nil {
			return err
		}
		if before == after {
			return nil
		}
	}
	return errors.New("the list set in force changed while reading: run it again")
}

// heldAccountClaims reads the account JWT every server holds and returns the first, with the signing keys of all of
// them: a key one server still holds is a key that is present. It refuses an incomplete server set and a server that
// did not answer, because a key on a silent server could not be reported.
func heldAccountClaims(ctx context.Context, nc *nats.Conn, account string, servers int) (*jwt.AccountClaims, error) {
	cl := policyapply.NATSCluster{Conn: nc, Inventory: servers}
	set, err := cl.Servers(ctx)
	if err != nil {
		return nil, err
	}
	if !set.Complete(servers) {
		return nil, errors.New(set.Why(servers))
	}
	toks, err := cl.AccountJWTs(ctx, account)
	if err != nil {
		return nil, err
	}
	var out *jwt.AccountClaims
	for _, n := range set.Names {
		tok, ok := toks[n]
		if !ok {
			return nil, fmt.Errorf("server %s did not answer for the account JWT", config.Clean(n))
		}
		if tok == "" {
			continue
		}
		c, err := jwt.DecodeAccountClaims(tok)
		if err != nil {
			return nil, fmt.Errorf("the account JWT of server %s does not decode: %v", config.Clean(n), err)
		}
		if out == nil {
			out = c
			continue
		}
		for k, v := range c.SigningKeys {
			if _, has := out.SigningKeys[k]; !has {
				out.SigningKeys[k] = v
			}
		}
	}
	if out == nil {
		return nil, errors.New("no server holds the account JWT")
	}
	return out, nil
}

// credsIssuer is the `iss` of the user JWT in the creds file at path: the key that signed the user.
func credsIssuer(path string) (string, error) {
	raw, err := config.ReadCredsFile(path)
	if err != nil {
		return "", err
	}
	defer clear(raw)
	tok, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		return "", errors.New("holds no readable JWT")
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		return "", errors.New("holds no readable user JWT")
	}
	return uc.Issuer, nil
}
