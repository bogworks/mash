// Package policydirect is the direct driver of the policy engine: a caller that holds the credentials of an account (the
// key store, the admin connection, the system-account connection) hands it a list-set document as plain JSON, and it
// applies the policy to the account's servers with the same engine the policy service uses (policyserve.Applier).
//
// What the service does around the engine is not here: no submission bucket, no adoption marker to verify a submitter
// with, no document signature, no intake queue and no process that stays up. The caller holds the keys, so the
// document needs no signature to be believed. What stays is what keeps the account safe: an account that carries an
// adoption marker is managed through the list-set path and is refused here, and the account lock, which `policy adopt`
// and every guarded policy change also take, is held for the whole of one apply and checked before each protected write.
//
// Nothing here is a command: the package takes a document and a Config and gives a Result. A verb, its flags and the
// choice of what the first policy of an account says are not decided here.
package policydirect

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policydoc"
	"github.com/bogworks/mash/internal/policyintake"
	"github.com/bogworks/mash/internal/policyserve"
)

// SetupTimeout bounds what Apply does before the engine runs: the marker read, the lock and the ledger open. The
// engine's own run is bounded by policyserve.ApplyTimeout.
const SetupTimeout = time.Minute

// Holder names the ledger in its lease value.
const Holder = "policy-direct"

// Config is everything the caller holds. The key store, the admin and the system connection are the caller's own: Apply
// uses them and closes none of them.
type Config struct {
	// Account is the account's public key; the key store's account key.
	Account string
	// Admin is the admin connection of the account: the directory and the issue record, the inbox checks, the account
	// lock and the adoption marker read are over it.
	Admin *admin.Admin
	// Sys is a connection authenticated as a system account user.
	Sys *nats.Conn
	// Keys is the key store.
	Keys  policyserve.Keys
	Trust apply.Trust
	// Servers is the declared number of servers in the deployment (1 or more); every apply reads the limits and the
	// account JWT from every one and refuses when any does not answer.
	Servers int
	// URL is the configured server URL, a comma-separated list when the deployment has several.
	URL string
	// Vet, if set, checks each client URL before anything connects to it.
	Vet func(url string) error
	// Options are the connection options of the deployment (the CA, the client certificate, the TLS requirement),
	// passed to every probe and witness connection.
	Options []nats.Option
	// Load opens a connection to one server (by URL) as a user of the account, so the server has the account loaded
	// and reports its JWT; the connections stay open for the apply. Nil opens none.
	Load func(ctx context.Context, url string) (*nats.Conn, error)
	// Bucket is the name of the policy bucket; "" is the default (policyapply.DefaultPolicyBucket). Apply creates the
	// bucket when it does not exist; a dry run never does.
	Bucket string
	// AdminUser and AdminIssuer are the public key and the `iss` of the admin credentials' user JWT. With AdminIssuer
	// set, the first generation's apply drops the account JWT's signing keys that are not mash's once the admin user
	// and ExemptUsers stay signed by a kept key (policyserve.Applier.AdminUser); without it such a key refuses the
	// apply. There is no adoption marker here to list the exempt users, so the caller names them.
	AdminUser, AdminIssuer string
	// ExemptUsers are the public keys of the other users whose connections must stay signed by a kept key. They count
	// only when AdminIssuer is set.
	ExemptUsers []string
	Log         *slog.Logger
	// Step is a test seam, nil in production: policyserve.Applier.Step.
	Step func(ctx context.Context, step string) error
}

// Options are the options of one Apply.
type Options struct {
	// DryRun computes and returns the plan and runs the checks that change nothing: no push, no ledger write, and the
	// policy bucket is not created.
	DryRun bool
}

// ledger is what the driver needs of the account's ledger: the engine's, and the status it wrote.
type ledger interface {
	policyserve.Ledger
	policyapply.StatusReader
}

// env is what the run needs, as functions: Apply builds it from a Config and the tests build it from fakes.
type env struct {
	account string
	// adopted reads whether the account carries an adoption marker.
	adopted func(ctx context.Context) (bool, error)
	// lock takes the account lock; held says whether it is still this holder's.
	lock func(ctx context.Context) (release func(), held func(context.Context) error, err error)
	// openLedger opens the account's ledger; with create false a bucket that does not exist is an error wrapping
	// policyapply.ErrNoPolicyBucket.
	openLedger func(ctx context.Context, create bool) (ledger, error)
	// servers reads what the servers report (their limits and client URLs), checks every URL and returns an error
	// when fewer servers answer than the declared count.
	servers func(ctx context.Context) ([]policyserve.ServerInfo, error)
	inboxes policyserve.InboxChecker
	// applier is the engine over the opened ledger.
	applier func(ctx context.Context, l ledger, g policyintake.Guard) (policyintake.Applier, error)
}

var errConfig = errors.New("policydirect: invalid configuration")

func (c Config) check() error {
	switch {
	case c.Account == "":
		return fmt.Errorf("%w: no account", errConfig)
	case c.Admin == nil:
		return fmt.Errorf("%w: no admin connection", errConfig)
	case c.Sys == nil:
		return fmt.Errorf("%w: no system-account connection", errConfig)
	case c.Keys == nil:
		return fmt.Errorf("%w: no key store", errConfig)
	case c.Servers < 1:
		return fmt.Errorf("%w: the declared server count must be 1 or more", errConfig)
	}
	return nil
}

// Apply applies the list-set document doc, plain JSON, to the account's servers, or with opts.DryRun reports what it
// would do. The Result carries the outcome for every way the document or the servers can answer; the error is not nil
// only when cfg is unusable (the Result's outcome is then error). Apply never reads a signature: a compact JWS is
// refused as invalid.
func Apply(ctx context.Context, cfg Config, doc []byte, opts Options) (Result, error) {
	if err := cfg.check(); err != nil {
		r := newResult(opts)
		r.fail(OutcomeError, StageApply, err.Error())
		return r, err
	}
	return run(ctx, cfg.env(), doc, opts), nil
}

func (c Config) bucket() (string, error) {
	if c.Bucket == "" {
		return policyapply.PolicyBucket("")
	}
	return c.Bucket, nil
}

// env builds the production environment of a Config.
func (c Config) env() env {
	var js jetstream.JetStream
	var jsErr error
	var once sync.Once
	stream := func() (jetstream.JetStream, error) {
		once.Do(func() { js, jsErr = jetstream.New(c.Admin.Conn()) })
		return js, jsErr
	}
	record := policyapply.NewAdminIssueRecord(c.Admin)
	open := func(ctx context.Context, create bool) (ledger, error) {
		bucket, err := c.bucket()
		if err != nil {
			return nil, err
		}
		j, err := stream()
		if err != nil {
			return nil, err
		}
		return policyapply.NewKVLedger(ctx, j, c.Account, policyapply.KVLedgerOptions{Bucket: bucket, Create: create, Holder: Holder})
	}
	return env{
		account: c.Account,
		adopted: func(ctx context.Context) (bool, error) {
			_, found, err := c.Admin.Adoption(ctx)
			return found, err
		},
		lock: func(ctx context.Context) (func(), func(context.Context) error, error) {
			lk, err := c.Admin.LockAccount(ctx)
			if err != nil {
				return nil, nil, err
			}
			return lk.Release, lk.Held, nil
		},
		openLedger: open,
		servers: func(ctx context.Context) ([]policyserve.ServerInfo, error) {
			servers, err := policyserve.ReadServers(ctx, c.Sys, c.Servers)
			if err != nil {
				return nil, fmt.Errorf("read the servers: %w", err)
			}
			if servers, err = policyserve.ResolveURLs(servers, c.URL, c.Servers); err != nil {
				return nil, err
			}
			if c.Vet != nil {
				for _, s := range servers {
					if err := c.Vet(s.URL); err != nil {
						return nil, fmt.Errorf("refusing to connect to server %.64s: %w", s.Name, err)
					}
				}
			}
			return servers, nil
		},
		inboxes: c.Admin,
		applier: func(ctx context.Context, l ledger, _ policyintake.Guard) (policyintake.Applier, error) {
			bucket, err := c.bucket()
			if err != nil {
				return nil, err
			}
			j, err := stream()
			if err != nil {
				return nil, err
			}
			kv, err := j.KeyValue(ctx, bucket)
			if err != nil {
				return nil, fmt.Errorf("policy bucket %s: %w", bucket, err)
			}
			app := &policyserve.Applier{
				Account: c.Account, Ledger: l, Sys: c.Sys, Keys: c.Keys, Trust: c.Trust, Record: record, KV: kv,
				Servers: c.Servers, URL: c.URL, Vet: c.Vet, Options: c.Options, Log: c.Log, Inboxes: c.Admin, Load: c.Load, Step: c.Step,
			}
			if c.AdminIssuer != "" {
				app.AdminUser, app.AdminIssuer = c.AdminUser, c.AdminIssuer
				exempt := append([]string{c.AdminUser}, c.ExemptUsers...)
				app.ExemptIssuers = policyserve.ExemptIssuerSource(c.Sys, c.Account, c.Servers, exempt)
			}
			return app, nil
		},
	}
}

func newResult(opts Options) Result {
	return Result{DryRun: opts.DryRun, Checks: []Check{
		{Name: CheckDocument, Result: ResultNotRun},
		{Name: CheckBaseGeneration, Result: ResultNotRun},
		{Name: CheckSize, Result: ResultNotRun},
		{Name: CheckServers, Result: ResultNotRun},
		{Name: CheckSignature, Result: ResultNotApplicable, Detail: "a direct caller holds the keys and submits no signature"},
	}}
}

func (r *Result) check(name, result, detail string) {
	for i := range r.Checks {
		if r.Checks[i].Name == name {
			r.Checks[i].Result, r.Checks[i].Detail = result, detail
			return
		}
	}
}

// fail sets the outcome of a refusal or failure.
func (r *Result) fail(outcome, stage, msg string) {
	r.Outcome, r.Stage, r.Message = outcome, stage, msg
	r.Refused = refusedOutcome(outcome)
}

// finish settles the checks the outcome decides.
func (r *Result) finish() {
	r.Refused = refusedOutcome(r.Outcome)
	switch {
	case r.Outcome == OutcomeApplied, r.Outcome == OutcomePlanned:
		r.check(CheckSize, ResultPass, "")
		r.check(CheckServers, ResultPass, "")
	case r.Outcome == OutcomePartial:
		r.check(CheckSize, ResultPass, "")
		r.check(CheckServers, ResultFail, r.Message)
	case r.Stage == policyserve.StageSize:
		r.check(CheckSize, ResultFail, r.Message)
	}
}

func shortSum(raw []byte) string {
	h := sha256.Sum256(raw)
	return fmt.Sprintf("%x", h[:8])
}

// lockGuard is the policyintake.Guard of a direct apply: it holds the account lock check and nothing of an adoption
// marker (the account carries none, or Apply refused). Once it refuses it keeps refusing.
type lockGuard struct {
	held    func(context.Context) error
	mu      sync.Mutex
	refused error
}

func (g *lockGuard) Before(ctx context.Context, what string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.refused != nil {
		return g.refused
	}
	ctx, cancel := context.WithTimeout(ctx, policyintake.GuardTimeoutDefault)
	defer cancel()
	if err := g.held(ctx); err != nil {
		g.refused = fmt.Errorf("the account lock is no longer held (before %s): %w", what, err)
		return g.refused
	}
	return nil
}

// decode reads the plain JSON document; a refusal is a Result that says why.
func decode(raw []byte, account string) (policydoc.Document, *Result) {
	bad := func(stage, format string, a ...any) (policydoc.Document, *Result) {
		r := &Result{}
		r.fail(OutcomeInvalid, stage, fmt.Sprintf(format, a...))
		return policydoc.Document{}, r
	}
	if t := bytes.TrimSpace(raw); len(t) == 0 || t[0] != '{' {
		return bad(policyintake.StageDocument, "the document is not plain JSON: a signed document (a compact JWS) is not accepted here, give the list-set document itself")
	}
	d, err := policydoc.Decode(raw, policydoc.DecodeOptions{})
	if err != nil {
		return bad(policyintake.StageDocument, "%v", err)
	}
	if d.Account != account {
		return bad(policyintake.StageAccount, "the document names another account than the one this account key holds")
	}
	if d.NextSubmitter != "" {
		return bad(policyintake.StageDocument, "next_submitter plans the rotation of a submitter, which a direct apply has none of: remove the field")
	}
	return d, nil
}

// run is Apply over an environment.
func run(ctx context.Context, e env, doc []byte, opts Options) Result {
	res := newResult(opts)
	setupCtx, cancel := context.WithTimeout(ctx, SetupTimeout)
	defer cancel()

	// An adopted account is managed through the list-set path. A real run takes the account lock first, as every guarded
	// policy change does, so that an adoption cannot start while it runs; a dry run changes nothing and takes none.
	var held func(context.Context) error
	if !opts.DryRun {
		release, h, err := e.lock(setupCtx)
		if err != nil {
			if errors.Is(err, admin.ErrAccountBusy) {
				res.fail(OutcomeError, StageApply, "another policy change or an adoption is in progress; nothing was changed")
			} else {
				res.fail(OutcomeError, StageApply, fmt.Sprintf("cannot take the account lock: %v; nothing was changed", err))
			}
			return res
		}
		defer release()
		held = h
	}
	adopted, err := e.adopted(setupCtx)
	switch {
	case err != nil:
		res.fail(OutcomeError, StageApply, fmt.Sprintf("cannot tell whether this account is adopted: %v; nothing was changed", err))
		return res
	case adopted:
		res.fail(OutcomeBlocked, policyintake.StageAdoption,
			"this account is adopted by an outside system: its list sets come only through the list-set path, so a direct apply is refused; nothing was changed")
		return res
	}

	d, bad := decode(doc, e.account)
	if bad != nil {
		bad.DryRun, bad.Checks = res.DryRun, res.Checks
		bad.check(CheckDocument, ResultFail, bad.Message)
		bad.finish()
		return *bad
	}
	res.Generation = d.Generation
	res.check(CheckDocument, ResultPass, "")

	l, err := e.openLedger(setupCtx, !opts.DryRun)
	var inForceL ledger
	switch {
	case err == nil:
		inForceL = l
	case opts.DryRun && errors.Is(err, policyapply.ErrNoPolicyBucket):
		// Nothing has been applied here yet: nothing is in force, and a dry run does not make the bucket.
	default:
		res.fail(OutcomeError, StageApply, fmt.Sprintf("the policy ledger: %v", err))
		return res
	}

	var cur listset.Record
	var prevLS *listset.ListSet
	var prev *listset.Compiled
	if inForceL != nil {
		if cur, err = inForceL.InForce(); err != nil {
			res.fail(OutcomeError, StageApply, fmt.Sprintf("read the generation in force: %v", err))
			return res
		}
		if cur.Generation > 0 {
			ls, ok, err := inForceL.InForceListSet()
			if err == nil && !ok {
				err = fmt.Errorf("%w: generation %d", policyapply.ErrNoInForceListSet, cur.Generation)
			}
			if err == nil {
				prev, err = listset.Compile(ls)
			}
			if err != nil {
				res.fail(OutcomeError, listset.StageInForce, fmt.Sprintf("the list set in force (generation %d) cannot be read: %v", cur.Generation, err))
				return res
			}
			prevLS = &ls
		}
	}

	// The document in force again is unchanged: the engine would refuse it as a stale base, and "nothing to do" is the
	// truer answer for a replay.
	if cur.Generation > 0 && listset.Digest(sha256.Sum256(doc)) == cur.Received {
		res.check(CheckBaseGeneration, ResultPass, "")
		res.Outcome, res.Message, res.Generation = OutcomeUnchanged, fmt.Sprintf("this is the document in force (generation %d, sha256 %s); nothing was changed", cur.Generation, shortSum(doc)), cur.Generation
		res.Stage = policyapply.StageUnchanged
		if st, ok, err := inForceL.LatestStatus(setupCtx); err == nil && ok {
			res.Status = &st
		}
		res.finish()
		return res
	}
	if cur.Generation != d.BaseGeneration {
		err := &listset.StaleBaseError{Stated: d.BaseGeneration, InForce: cur.Generation}
		res.check(CheckBaseGeneration, ResultFail, err.Error())
		res.fail(OutcomeStale, policydoc.StageBase, err.Error())
		return res
	}
	res.check(CheckBaseGeneration, ResultPass, "")
	if d.Generation != d.BaseGeneration+1 || d.ListSet.Base != d.BaseGeneration {
		res.fail(OutcomeInvalid, policyintake.StageDocument, fmt.Sprintf("%v: generation %d, base %d, list set base %d",
			policyapply.ErrBadGeneration, d.Generation, d.BaseGeneration, d.ListSet.Base))
		res.check(CheckDocument, ResultFail, res.Message)
		return res
	}

	if opts.DryRun {
		dryRun(setupCtx, e, d, doc, cur, prevLS, prev, &res)
		res.finish()
		return res
	}
	realRun(ctx, e, d, doc, held, l, prev, &res)
	res.finish()
	return res
}

// dryRun fills res from the plan and the checks that change nothing: the list set's own checks, the sizes against the
// servers' limits, the inbox of every added address and the steps the engine does not have.
func dryRun(ctx context.Context, e env, d policydoc.Document, raw []byte, cur listset.Record, prevLS *listset.ListSet,
	prev *listset.Compiled, res *Result) {
	target, cerr := listset.Compile(d.ListSet)
	if cerr != nil {
		plan := listset.DryRun(prevLS, d.ListSet, nil)
		res.Plan = &plan
		planRefusal(&plan, res)
		return
	}
	servers, err := e.servers(ctx)
	if err != nil {
		res.check(CheckServers, ResultFail, err.Error())
		res.fail(OutcomeError, StageApply, err.Error())
		return
	}
	plan := listset.DryRun(prevLS, d.ListSet, policyserve.Limits(servers))
	res.Plan = &plan
	if plan.Refused {
		planRefusal(&plan, res)
		return
	}
	if _, _, err := listset.Accept(cur, d.ListSet, raw); err != nil {
		var ue *listset.UnchangedError
		if errors.As(err, &ue) {
			res.Outcome, res.Stage, res.Generation = OutcomeUnchanged, policyapply.StageUnchanged, ue.InForce
			res.Message = fmt.Sprintf("the content is the content of generation %d in force; nothing would change", ue.InForce)
			return
		}
	}
	refusals, err := policyserve.FailClosed(ctx, prev, target, e.inboxes, true)
	if err != nil {
		res.fail(OutcomeError, StageApply, err.Error())
		return
	}
	if len(refusals) > 0 {
		refuse(res, refusals)
		return
	}
	res.Outcome = OutcomePlanned
	res.Message = fmt.Sprintf("a dry run: generation %d would be applied; nothing was changed", d.Generation)
}

// planRefusal turns the refusals of a dry run's plan into the result.
func planRefusal(p *listset.Plan, res *Result) {
	refuse(res, p.Refusals)
}

// refuse sets the result from a list of refusals: the first decides the outcome and the stage.
func refuse(res *Result, rs []listset.Refusal) {
	if len(rs) == 0 {
		res.fail(OutcomeError, StageApply, "refused with no reason on record")
		return
	}
	st := policyapply.Status{State: policyapply.StateRefused, Refusals: rs}
	o, stage, msg := outcomeOfStatus(st)
	res.fail(o, stage, msg)
	if o == OutcomeInvalid {
		res.check(CheckDocument, ResultFail, msg)
	}
}

// realRun applies the document through the engine and maps what came back.
func realRun(ctx context.Context, e env, d policydoc.Document, raw []byte, held func(context.Context) error, l ledger,
	prev *listset.Compiled, res *Result) {
	// The checks of the engine that report no status of their own are made first, so that each gets its own outcome: the
	// inbox of an added address and the narrowing of an existing sender. The engine makes them again before it pushes.
	if target, cerr := listset.Compile(d.ListSet); cerr == nil {
		refusals, err := policyserve.FailClosed(ctx, prev, target, e.inboxes, true)
		if err != nil {
			res.fail(OutcomeError, StageApply, err.Error())
			return
		}
		if len(refusals) > 0 {
			refuse(res, refusals)
			return
		}
	}
	g := &lockGuard{held: held}
	app, err := e.applier(ctx, l, g)
	if err != nil {
		res.fail(OutcomeError, StageApply, err.Error())
		return
	}
	ar, err := app.Apply(ctx, policyintake.Doc{
		Account: d.Account, Generation: d.Generation, BaseGeneration: d.BaseGeneration, Raw: raw,
	}, g)
	mapRun(ctx, l, d, ar, err, res)
}

// mapRun turns what the engine returned into the result. The error text alone says little, so the sentinels are read
// first; then, when the engine wrote a status of its own, the status; any other error is an error outcome.
func mapRun(ctx context.Context, l ledger, d policydoc.Document, ar policyintake.Result, err error, res *Result) {
	res.Generation = d.Generation
	if err == nil {
		if st, ok := runStatus(ctx, l, ar); ok {
			res.Status = &st
			if o, stage, msg := outcomeOfStatus(st); o != OutcomeApplied {
				res.fail(o, stage, msg)
				return
			}
		}
		res.Outcome, res.Message = OutcomeApplied, fmt.Sprintf("generation %d is applied on every server", d.Generation)
		return
	}
	var stale *listset.StaleBaseError
	switch {
	case errors.As(err, &stale):
		res.fail(OutcomeStale, policydoc.StageBase, err.Error())
		res.check(CheckBaseGeneration, ResultFail, err.Error())
		return
	case errors.Is(err, policyapply.ErrPendingGeneration):
		res.fail(OutcomeStale, policydoc.StagePending, err.Error())
		return
	case errors.Is(err, policyapply.ErrBadGeneration):
		res.fail(OutcomeInvalid, policyintake.StageDocument, err.Error())
		return
	case errors.Is(err, policyapply.ErrAccountJWTTooLarge):
		res.fail(OutcomeBlocked, policyserve.StageSize, err.Error())
		return
	}
	if st, ok := runStatus(ctx, l, ar); ok {
		res.Status = &st
		o, stage, msg := outcomeOfStatus(st)
		if o == OutcomeApplied {
			// The generation was committed and a later step failed: the policy is applied, and the failure is said.
			msg += fmt.Sprintf("; a later step reported: %v", err)
		}
		res.Outcome, res.Stage, res.Message = o, stage, msg
		res.Refused = refusedOutcome(o)
		return
	}
	res.fail(OutcomeError, StageApply, strings.TrimSpace(err.Error()))
}

// runStatus is the status the run wrote, when the engine says it wrote one.
func runStatus(ctx context.Context, l ledger, ar policyintake.Result) (policyapply.Status, bool) {
	if !ar.StatusWritten {
		return policyapply.Status{}, false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), policyserve.CleanupTimeout)
	defer cancel()
	st, ok, err := l.LatestStatus(ctx)
	if err != nil || !ok {
		return policyapply.Status{}, false
	}
	return st, true
}
