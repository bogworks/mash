package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policydoc"
	"github.com/bogworks/mash/internal/policyserve"
	"github.com/bogworks/mash/internal/reconcile"
)

const policyReconcileUsage = `usage: mashctl policy reconcile [--json] [--listset FILE] [--range R] [--bucket NAME] [--account KEY] [--sys-creds FILE] [--servers N]
       mashctl policy reconcile --yes --plan DIGEST [--range R] [--bucket NAME] [--account KEY] [--sys-creds FILE] [--servers N]
       (run after the first applied list set of an adopted account. Without --yes it prints the plan that removes what the
       account held before the list set and the list set does not own, and changes nothing: each signing key that is not
       one of the list set's scoped keys and still present, each recorded issued user whose sender is not in the list set
       and each connected key that is neither recorded, exempt nor issued under the list set that it revokes, and anything
       else it cannot name as a finding; it ends with plan: sha256:DIGEST. --listset FILE previews the signing keys the
       first apply would remove, before it. Exit 0 when the plan is computed; 1 when an admin or exempt user is signed by
       a signing key that does not stay, or on a failure. --yes --plan DIGEST revokes what that dry run planned, one way:
       it takes the account lock and the ledger lease, computes the plan again and refuses, changing nothing, when its
       digest is not DIGEST. --listset is for the dry run only)
`

type reconcileOpts struct {
	json, yes                     bool
	listset, rng, bucket, account string
	plan                          string
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
		case "--plan":
			o.plan, err = val()
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
	usage := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "%s%s\n%s", pfx, fmt.Sprintf(format, a...), policyReconcileUsage)
		return 2
	}
	switch {
	case o.yes && o.listset != "":
		return usage("--listset is a preview of the dry run: it cannot be used with --yes")
	case o.yes && o.plan == "":
		return usage("--yes needs --plan DIGEST from a dry run")
	case !o.yes && o.plan != "":
		return usage("--plan is for --yes")
	case o.yes && o.json:
		return usage("--json is for the dry run")
	case o.yes && !validPlanDigest(o.plan): // named, never printed
		return usage("--plan is not a plan digest, sha256: and 64 hex digits, as the dry run prints it (the value is not shown)")
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

	nc, err := connectSys(cfg, o.sysCreds, env)
	if err != nil {
		return fail("%v", err)
	}
	defer nc.Close()

	var ledger *policyapply.KVLedger
	if fileDoc == nil {
		if ledger, err = openReconcileLedger(ctx, adm.Conn(), account, o, env); err != nil {
			return fail("%v", err)
		}
	}
	// build computes the plan from what is true now: the list set and the JWT in force (or the file), the connections and
	// the issue record. The dry run calls it once; --yes calls it before it locks, to take the signers, and again under
	// the account lock and the ledger lease (reconcile.Execute).
	build := func(ctx context.Context) (reconcile.Plan, map[string]string, error) {
		in := reconcile.Input{Account: account, Held: held, Now: time.Now(), Exempt: marker.Exempt, Issued: adm, AdminUser: adminUser, AdminIssuer: adminIssuer}
		sctx, cancel := context.WithTimeout(ctx, applyDeadline)
		defer cancel()
		if fileDoc != nil {
			in.Source, in.ListSet = reconcile.SourceListSetFile, fileDoc.ListSet
			var err error
			if in.Claims, err = heldAccountClaims(sctx, nc, account, o.servers); err != nil {
				return reconcile.Plan{}, nil, err
			}
		} else {
			rec, ls, jwtIn, err := readInForce(ledger)
			if err != nil {
				return reconcile.Plan{}, nil, err
			}
			in.Source, in.Generation, in.ListSet = reconcile.SourceInForce, rec.Generation, ls
			if in.Claims, err = jwt.DecodeAccountClaims(jwtIn); err != nil {
				return reconcile.Plan{}, nil, fmt.Errorf("the account JWT in force does not decode: %v", err)
			}
		}
		groups, err := reconcile.Groups(in.ListSet)
		if err != nil {
			return reconcile.Plan{}, nil, fmt.Errorf("the list set: %v", err)
		}
		if in.Signers, err = reconcile.ExpectedSigners(groups, held); err != nil {
			return reconcile.Plan{}, nil, err
		}
		scan, err := scanConnections(sctx, nc, account, o.servers)
		if err != nil {
			return reconcile.Plan{}, nil, err
		}
		in.Servers, in.ScanIncomplete = scan.Conns, scan.Incomplete
		plan, err := reconcile.Build(ctx, in)
		return plan, in.Signers, err
	}

	if o.yes {
		return runReconcileYes(ctx, o, reconcileYes{
			account: account, adm: adm, ledger: ledger, sys: nc, keys: keys, exempt: append(slices.Clone(marker.Exempt), adminUser),
			adminUser: adminUser, adminIssuer: adminIssuer, held: held, build: build,
		}, stdout, stderr)
	}
	plan, _, err := build(ctx)
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

// validPlanDigest is true for "sha256:" and 64 lower-case hex digits.
func validPlanDigest(s string) bool {
	h, ok := strings.CutPrefix(s, "sha256:")
	if !ok || len(h) != 64 {
		return false
	}
	return strings.Trim(h, "0123456789abcdef") == ""
}

// reconcileYes is what --yes needs beyond the plan builder.
type reconcileYes struct {
	account, adminUser, adminIssuer string
	adm                             *admin.Admin
	ledger                          *policyapply.KVLedger
	sys                             *nats.Conn
	keys                            *ctlkeys.Authority
	// exempt is the adoption marker's exempt users and the admin user.
	exempt []string
	// held is the key store's GroupPublics.
	held  map[string]string
	build func(ctx context.Context) (reconcile.Plan, map[string]string, error)
}

// exemptIssuerSource reads, on each call, the issuer of every connection of every connected user in exempt: the
// source an Issuer's ExemptIssuers needs. A scan that is not the whole picture is an error, never a partial answer:
// a connection on a server that did not answer could be under a key that is about to leave.
func exemptIssuerSource(nc *nats.Conn, account string, servers int, exempt []string) func(context.Context) (map[string][]string, error) {
	return func(ctx context.Context) (map[string][]string, error) {
		scan, err := scanConnections(ctx, nc, account, servers)
		if err != nil {
			return nil, err
		}
		if scan.Incomplete != "" {
			return nil, fmt.Errorf("the connection scan is incomplete: %s", scan.Incomplete)
		}
		isExempt := map[string]bool{}
		for _, k := range exempt {
			isExempt[k] = true
		}
		out := map[string][]string{}
		for _, s := range scan.Conns {
			for _, c := range s.Conns {
				if isExempt[c.Key] {
					out[c.Key] = append(out[c.Key], c.IssuerKey)
				}
			}
		}
		return out, nil
	}
}

// runReconcileYes is `policy reconcile --yes --plan DIGEST`: it checks the plan the person saw against a fresh one,
// takes the account lock and calls reconcile.Execute, which takes the ledger lease and computes the plan a third time
// right before it signs; every one of them must have the digest. Nothing is pushed unless all do.
func runReconcileYes(ctx context.Context, o reconcileOpts, y reconcileYes, stdout, stderr io.Writer) int {
	const pfx = "mashctl: policy reconcile: "
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "%s%s\n", pfx, fmt.Sprintf(format, a...))
		return 1
	}
	changed := func() int { return fail("the plan changed since the dry run: run it again (nothing was changed)") }
	plan, signers, err := y.build(ctx)
	if err != nil {
		return fail("%v", err)
	}
	if plan.IssuerErr() != nil {
		fmt.Fprint(stdout, plan.Text())
		return fail("%v", plan.IssuerErr())
	}
	if plan.PlanDigest != o.plan {
		return changed()
	}
	trust, err := apply.LoadTrust(y.keys)
	if err != nil {
		return fail("%v", err)
	}
	signer := &policyserve.Issuer{
		Ctx: ctx, Keys: y.keys, Cluster: policyapply.NATSCluster{Conn: y.sys, Inventory: o.servers},
		Resolver: apply.NATS{Conn: y.sys, Inventory: o.servers}, Account: y.account, Range: o.rng, Trust: trust, Servers: o.servers,
		AdminUser: y.adminUser, AdminIssuer: y.adminIssuer, ExemptIssuers: exemptIssuerSource(y.sys, y.account, o.servers, y.exempt),
	}
	lock, err := y.adm.LockAccount(ctx)
	if err != nil {
		return fail("%v", err)
	}
	defer lock.Release()
	res, err := reconcile.Execute(ctx, reconcile.ExecInput{
		Build: func(ctx context.Context) (reconcile.Plan, error) {
			p, _, err := y.build(ctx)
			if err != nil {
				return p, err
			}
			if p.PlanDigest != o.plan {
				return p, reconcile.ErrPlanChanged
			}
			return p, nil
		},
		Ledger: y.ledger, Cluster: signer.Cluster, Account: y.account, Inventory: o.servers,
		Signers: signers, Held: y.held, Signer: signer, Marker: y.adm, BeforeWrite: func(ctx context.Context, _ string) error { return lock.Held(ctx) },
	})
	reasons := map[string]string{}
	for _, r := range res.Plan.Revocations {
		reasons[r.Key] = r.Reason
	}
	for _, k := range res.Revoked {
		fmt.Fprintf(stdout, "revoked %s: %s\n", k, reasons[k])
	}
	switch {
	case errors.Is(err, reconcile.ErrPlanChanged):
		return changed()
	case err != nil && len(res.Revoked) > 0:
		fmt.Fprintf(stdout, "reconciled: %d revoked\n", len(res.Revoked))
		return fail("the revocations are on every server, but: %v", err)
	case err != nil:
		return fail("%v (nothing was committed)", err)
	}
	fmt.Fprintf(stdout, "reconciled: %d revoked\n", len(res.Revoked))
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

// openReconcileLedger opens the account's ledger in the range's policy bucket.
func openReconcileLedger(ctx context.Context, nc *nats.Conn, account string, o reconcileOpts, env func(string) string) (*policyapply.KVLedger, error) {
	bucket, err := reconcileBucket(o, env)
	if err != nil {
		return nil, err
	}
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, err
	}
	return policyapply.NewKVLedger(ctx, js, account, policyapply.KVLedgerOptions{Bucket: bucket, Holder: "mashctl-policy-reconcile"})
}

// readInForce reads the list set and the account JWT in force from the ledger. It is read only: it takes no lease and
// writes nothing.
func readInForce(l *policyapply.KVLedger) (listset.Record, listset.ListSet, string, error) {
	var (
		rec listset.Record
		ls  listset.ListSet
		tok string
	)
	err := readStable(func() (uint64, error) {
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
