package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policydoc"
	"github.com/bogworks/mash/internal/sap"
)

// listSetOpts are the flags and positional arguments of a list-set verb.
type listSetOpts struct {
	json                            bool
	rng, bucket, account, submitter string
	args                            []string
}

func parseListSetArgs(args []string, allowSubmitter bool) (listSetOpts, error) {
	var o listSetOpts
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, inline, hasInline := strings.Cut(a, "=")
		val := func() (string, error) {
			if hasInline {
				if inline == "" {
					return "", fmt.Errorf("%s needs a value", name)
				}
				return inline, nil
			}
			if i+1 < len(args) && args[i+1] != "" {
				i++
				return args[i], nil
			}
			return "", fmt.Errorf("%s needs a value", name)
		}
		var err error
		switch {
		case name == "--json":
			if hasInline {
				return o, fmt.Errorf("unexpected %s", config.RejectedArg(a))
			}
			o.json = true
		case name == "--range":
			o.rng, err = val()
		case name == "--bucket":
			o.bucket, err = val()
		case name == "--account":
			o.account, err = val()
		case name == "--submitter" && allowSubmitter:
			o.submitter, err = val()
		case strings.HasPrefix(a, "-"):
			return o, fmt.Errorf("unexpected %s", config.RejectedArg(a))
		default:
			o.args = append(o.args, a)
		}
		if err != nil {
			return o, err
		}
	}
	return o, nil
}

// resolveListSetBucket names the policy bucket: --bucket, else the MASH_POLICY_BUCKET setting as a pattern with
// --range filling {range}. It prints its own errors and never prints a value it was given.
func resolveListSetBucket(pfx string, o listSetOpts, env func(string) string, stderr io.Writer) (string, bool) {
	pattern, source := o.bucket, "--bucket"
	if pattern == "" {
		pattern, source = env(policyapply.PolicyBucketEnv), policyapply.PolicyBucketEnv
	}
	if pattern == "" {
		fmt.Fprintf(stderr, "%sno policy bucket: set %s to the bucket name (it may contain {range}) or pass --bucket NAME\n", pfx, policyapply.PolicyBucketEnv)
		return "", false
	}
	hasRange := strings.Contains(pattern, "{range}")
	switch {
	case hasRange && o.rng == "":
		fmt.Fprintf(stderr, "%sno range: %s has {range}, so pass --range RANGE\n", pfx, source)
		return "", false
	case !hasRange && o.rng != "":
		fmt.Fprintf(stderr, "%s--range is given but %s has no {range} (the value is not shown)\n", pfx, source)
		return "", false
	}
	name, err := policyapply.PolicyBucket(pattern, o.rng)
	if err != nil { // the error quotes the value: name the source only
		fmt.Fprintf(stderr, "%s%s is not a valid KV bucket name (letters, digits, '-' and '_', and {range}) (the value is not shown)\n", pfx, source)
		return "", false
	}
	return name, true
}

// ledgerReader is the read-only view of the account's ledger: policyapply.KVLedger's reads, made with no lease and no
// write (NewKVLedger only opens the bucket), and the account the ledger is for.
type ledgerReader struct {
	*policyapply.KVLedger
	account string
}

func (r ledgerReader) Account() string { return r.account }

// listSetReader is the ledger of the account in the range's policy bucket, and the context to read it in.
type listSetReader struct {
	r   policydoc.Reader
	ctx context.Context
	// adm is the admin connection (it reads the adoption marker); cfg is the configuration it was opened from.
	adm *admin.Admin
	cfg config.Config
}

// openListSetReader connects with the admin credentials (the connection every admin verb uses), finds the account
// (--account, else the key store's account key) and the range (--range) and opens the ledger. It prints its own
// errors; the returned func closes the connection.
func openListSetReader(pfx string, o listSetOpts, env func(string) string, lookup config.Lookup, stderr io.Writer) (*listSetReader, func(), bool) {
	fail := func(format string, a ...any) (*listSetReader, func(), bool) {
		fmt.Fprintf(stderr, pfx+format+"\n", a...)
		return nil, nil, false
	}
	if o.rng != "" && !config.ValidName(o.rng) { // named, never printed: a mistyped argument may be a secret
		return fail("--range is not a valid range name: 1-48 lower-case letters, digits and single hyphens (the value is not shown)")
	}
	bucket, ok := resolveListSetBucket(pfx, o, env, stderr)
	if !ok {
		return nil, nil, false
	}
	cfg, err := loadConfig(env, lookup)
	if err != nil && !errors.Is(err, config.ErrNoConfigDir) {
		fmt.Fprintln(stderr, err)
		return nil, nil, false
	}
	account := o.account
	if account == "" {
		ks, err := openKeys(cfg, env)
		if err != nil {
			return fail("no account key: pass --account KEY or run `mashctl init` (%v)", err)
		}
		if _, account, err = ks.Account(); err != nil {
			return fail("no account key: pass --account KEY or run `mashctl init` (%v)", err)
		}
	} else if !nkeys.IsValidPublicAccountKey(account) {
		return fail("--account is not an account public key (the value is not shown)")
	}
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return fail("%v", err)
	}
	js, err := jetstream.New(adm.Conn())
	if err != nil {
		closeAdmin()
		return fail("%v", err)
	}
	l, err := policyapply.NewKVLedger(ctx, js, account, policyapply.KVLedgerOptions{Bucket: bucket})
	if err != nil {
		closeAdmin()
		return fail("%v", err)
	}
	return &listSetReader{r: ledgerReader{KVLedger: l, account: account}, ctx: ctx, adm: adm, cfg: cfg}, closeAdmin, true
}

func printListSetJSON(stdout io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\n", b)
	return err
}

// listSetUsageFail prints err (when there is one) and the usage, and returns the usage exit code.
func listSetUsageFail(pfx string, err error, stderr io.Writer) int {
	if err != nil {
		fmt.Fprintf(stderr, "%s%v\n", pfx, err)
	}
	fmt.Fprint(stderr, policyUsage)
	return 2
}

// checkListSetScope validates --account and --range before any connection is opened: a bad one is a usage error
// (exit 2). It never prints a value it was given.
func checkListSetScope(pfx string, o listSetOpts, stderr io.Writer) int {
	if o.account != "" && !nkeys.IsValidPublicAccountKey(o.account) {
		fmt.Fprintf(stderr, "%s--account is not an account public key (the value is not shown)\n", pfx)
		return 2
	}
	if o.rng != "" && !config.ValidName(o.rng) {
		fmt.Fprintf(stderr, "%s--range is not a valid range name: 1-48 lower-case letters, digits and single hyphens (the value is not shown)\n", pfx)
		return 2
	}
	return 0
}

// listSetPrint writes v as JSON or text and returns the exit code of a failed write.
func listSetPrint(pfx string, asJSON bool, v any, text string, stdout, stderr io.Writer) int {
	var err error
	if asJSON {
		err = printListSetJSON(stdout, v)
	} else {
		_, err = fmt.Fprint(stdout, text)
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s%v\n", pfx, err)
		return 1
	}
	return 0
}

// policyListSetShow is `mashctl policy show`: the list set in force and how far it is applied.
func policyListSetShow(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	const pfx = "mashctl: policy show: "
	o, err := parseListSetArgs(args, false)
	if err != nil || len(o.args) != 0 {
		return listSetUsageFail(pfx, err, stderr)
	}
	if code := checkListSetScope(pfx, o, stderr); code != 0 {
		return code
	}
	rd, closeFn, ok := openListSetReader(pfx, o, env, lookup, stderr)
	if !ok {
		return 1
	}
	defer closeFn()
	res, err := policydoc.Show(rd.ctx, rd.r)
	if err != nil {
		fmt.Fprintf(stderr, "%s%v\n", pfx, err)
		return 1
	}
	return listSetPrint(pfx, o.json, res, policydoc.RenderShow(res), stdout, stderr)
}

// policyListSetExplain is `mashctl policy explain SENDER RECEIVER`.
func policyListSetExplain(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	const pfx = "mashctl: policy explain: "
	o, err := parseListSetArgs(args, false)
	if err != nil || len(o.args) != 2 {
		return listSetUsageFail(pfx, err, stderr)
	}
	for i, a := range o.args {
		if _, err := sap.ParseCanonical(a); err != nil { // named, never printed: a mistyped argument may be a secret
			fmt.Fprintf(stderr, "%s%s is not a three-part address (range.owner.name) (the value is not shown)\n", pfx, []string{"SENDER", "RECEIVER"}[i])
			return 2
		}
	}
	if code := checkListSetScope(pfx, o, stderr); code != 0 {
		return code
	}
	rd, closeFn, ok := openListSetReader(pfx, o, env, lookup, stderr)
	if !ok {
		return 1
	}
	defer closeFn()
	res, err := policydoc.Explain(rd.ctx, rd.r, o.args[0], o.args[1])
	if err != nil {
		fmt.Fprintf(stderr, "%s%v\n", pfx, err)
		return 1
	}
	return listSetPrint(pfx, o.json, res, policydoc.RenderExplain(res), stdout, stderr)
}

// listSetServerLimits are the limits a diff checks sizes against. mashctl has no way to learn a server's limits, so
// the defaults stand in: a nil slice is one server with the default limits.
func listSetServerLimits() []listset.ServerLimits { return nil }

// policyListSetDiff is `mashctl policy diff FILE`: what applying FILE would do. It exits 1 when the plan is refused.
func policyListSetDiff(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	const pfx = "mashctl: policy diff: "
	o, err := parseListSetArgs(args, true)
	if err != nil || len(o.args) != 1 {
		return listSetUsageFail(pfx, err, stderr)
	}
	if o.submitter != "" && !nkeys.IsValidPublicKey(o.submitter) {
		fmt.Fprintf(stderr, "%s--submitter is not a public key (the value is not shown)\n", pfx)
		return 2
	}
	file, err := os.ReadFile(o.args[0])
	if err != nil {
		fmt.Fprintf(stderr, "%s%v\n", pfx, err)
		return 1
	}
	if o.submitter != "" { // policydoc.Diff reads the file without a signature check: verify it here
		if _, err := policydoc.Decode(file, policydoc.DecodeOptions{Submitter: o.submitter}); err != nil {
			fmt.Fprintf(stderr, "%s%v\n", pfx, err)
			return 1
		}
	}
	if code := checkListSetScope(pfx, o, stderr); code != 0 {
		return code
	}
	rd, closeFn, ok := openListSetReader(pfx, o, env, lookup, stderr)
	if !ok {
		return 1
	}
	defer closeFn()
	plan, err := policydoc.Diff(rd.ctx, rd.r, file, o.rng, listSetServerLimits(), rotationContext(rd, file, o.submitter, env))
	if err != nil {
		fmt.Fprintf(stderr, "%s%v\n", pfx, err)
		return 1
	}
	if code := listSetPrint(pfx, o.json, plan, policydoc.RenderPlan(plan), stdout, stderr); code != 0 {
		return code
	}
	if plan.Refused {
		return 1
	}
	return 0
}

// rotationContext reads what the intake checks a next_submitter against: the adoption marker over the admin connection
// and the keys mash holds in the key store. A read that fails is carried in the context, and the diff refuses a
// document with a next_submitter then, as the intake does. A document that is not readable, or has no next_submitter
// and no pending rotation to complete, needs nothing read; Diff reports a bad document itself.
func rotationContext(rd *listSetReader, file []byte, signer string, env func(string) string) *policydoc.RotationContext {
	rc := &policydoc.RotationContext{Signer: signer}
	m, found, err := rd.adm.Adoption(rd.ctx)
	if err != nil {
		rc.Err = fmt.Errorf("the adoption marker cannot be read: %w", err)
		return rc
	}
	rc.Adopted, rc.Marker = found, m
	if d, err := policydoc.Decode(file, policydoc.DecodeOptions{}); err != nil || d.NextSubmitter == "" || m.NextSubmitter != "" {
		return rc // the held keys only matter for a rotation that would start
	}
	keys, err := openKeys(rd.cfg, env)
	if err == nil {
		var held map[string]string
		if _, held, err = keys.HeldKeys(); err == nil {
			for k := range held {
				rc.Held = append(rc.Held, k)
			}
		}
	}
	if err != nil {
		rc.Err = fmt.Errorf("the keys mash holds cannot be read: %w", err)
	}
	return rc
}
