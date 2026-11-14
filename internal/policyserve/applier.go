package policyserve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policydoc"
	"github.com/bogworks/mash/internal/policyintake"
)

// Durations of one apply.
const (
	// ApplyTimeout bounds the whole of one document's apply: the pushes, the gate and the cleanup.
	ApplyTimeout = 10 * time.Minute
	// CleanupTimeout bounds the revocation of the probe credentials, which runs after the apply whatever its outcome
	// and with a context that does not end with it.
	CleanupTimeout = time.Minute
)

// StageSize is the stage of a refusal for a list set that does not fit the servers' limits.
const StageSize = "size"

// Applier is the policyintake.Applier: for each verified document it checks what the servers allow, opens the
// probe and witness connections, and runs policyapply.Apply with everything it needs.
type Applier struct {
	Account string
	// Range is the range this service applies list sets for (--range).
	Range string
	// Ledger is the account's ledger in the policy bucket; it keeps the record in force and writes the status.
	Ledger Ledger
	// Sys is a connection authenticated as a system account user.
	Sys *nats.Conn
	// Keys is the key store.
	Keys  Keys
	Trust apply.Trust
	// Record is the issue record; it also gives the old-key inventory of each apply.
	Record IssueRecord
	// KV is the account's policy bucket; the provisioning markers of an apply are kept in it.
	KV jetstream.KeyValue
	// Servers is the declared server count (--servers, 1 or more).
	Servers int
	// URL is the configured server URL, a comma-separated list when the deployment has several; the client URL of a
	// server that reports no host of its own comes from it (see ResolveURLs).
	URL string
	// Vet, if set, checks each client URL before anything connects to it (the TLS requirement and the loopback rule of
	// the admin connection); a URL it refuses refuses the apply.
	Vet func(url string) error
	// Options are the connection options of the deployment (the CA, the client certificate, the TLS requirement),
	// passed to every probe and witness connection.
	Options []nats.Option
	// Load, if set, opens a connection to one server (by URL) as a user of the account, so the server has the account
	// loaded and reports its JWT. The connections stay open for the apply.
	Load func(ctx context.Context, url string) (*nats.Conn, error)
	Log  *slog.Logger
	// Provisioner provisions the new addresses of a change inside the run. Nil is the production setting: the
	// service's Provisioner, and a change that adds an address is refused before the push. A test sets its own.
	Provisioner policyapply.Provisioner
	// Now gives the time; nil means time.Now.
	Now func() time.Time

	markersOnce sync.Once
	markers     policyapply.ProvisionMarkers
	markersErr  error

	// applyFn and readServers are seams for the tests; nil means policyapply.Apply and ReadServers.
	applyFn     func(context.Context, policyapply.ApplyRequest, policyapply.ApplyDeps) (policyapply.Status, error)
	readServers func(ctx context.Context) ([]ServerInfo, error)
}

var _ policyintake.Applier = (*Applier)(nil)

// IssueRecord is the issue record the Applier needs: the record of what was issued and the inventory of every entry.
type IssueRecord interface {
	policyapply.IssueRecord
	policyapply.IssueInventory
}

// markersFor builds the provisioning markers of the Applier's account once and keeps them for every document.
// A nil KV gives in-memory markers: that is a test-only path (the unit tests run over a MemLedger with no bucket);
// production always sets KV (cmd/mashctl/policy_serve.go), so its markers are always durable.
func (a *Applier) markersFor() (policyapply.ProvisionMarkers, error) {
	a.markersOnce.Do(func() {
		if a.KV == nil {
			a.markers = policyapply.NewMemProvisionMarkers()
			return
		}
		a.markers, a.markersErr = policyapply.NewKVProvisionMarkers(a.KV, a.Account, policyapply.DefaultProvisionOpTimeout)
	})
	if a.markersErr != nil {
		return nil, a.markersErr
	}
	return a.markers, nil
}

// Ledger is what the Applier needs of the account's ledger: the record and list set in force, what Apply needs, and
// the write of a refusal. *policyapply.KVLedger is one.
type Ledger interface {
	policyapply.DetailLedger
	WriteRefusal(ctx context.Context, st policyapply.Status) error
}

var _ Ledger = (*policyapply.KVLedger)(nil)

func (a *Applier) errorf(msg string, args ...any) {
	if a.Log != nil {
		a.Log.Error(msg, args...)
	}
}

// shortKeys lists public keys by their first characters only.
func shortKeys(pubs []string) []string {
	out := make([]string, len(pubs))
	for i, p := range pubs {
		out[i] = fmt.Sprintf("%.8s", p)
	}
	return out
}

// cleanupProbes revokes the probe credentials the sources minted, after the guard allowed one more write. It returns
// nothing on purpose: by now the generation is committed, and an error here would make the intake skip the rotation of
// the submitter and process the revision again. A refusal by the guard skips the push; a failed revocation is
// reported too. Either way it is logged at error level with the short keys of the probes, which expire on their own:
// a probe credential is valid for policyapply.DefaultProbeTTL (internal/policyapply/probe.go) at most.
func (a *Applier) cleanupProbes(ctx context.Context, g policyintake.Guard, rv policyapply.Revoker, sources ...policyapply.MintedSource) {
	var minted []string
	for _, s := range sources {
		if s != nil {
			minted = append(minted, s.Minted()...)
		}
	}
	if len(minted) == 0 {
		return
	}
	if err := g.Before(ctx, "revoke probes"); err != nil {
		a.errorf("probe credentials not revoked: the guard refused the revocation; they expire on their own",
			"error", err.Error(), "minted", shortKeys(minted), "expires_within", policyapply.DefaultProbeTTL.String())
		return
	}
	if err := policyapply.RevokeMinted(ctx, rv, a.Record, a.now(), sources...); err != nil {
		a.errorf("could not revoke the probe credentials; they expire on their own",
			"error", err.Error(), "minted", shortKeys(minted), "expires_within", policyapply.DefaultProbeTTL.String())
	}
}

func (a *Applier) logf(msg string, args ...any) {
	if a.Log != nil {
		a.Log.Info(msg, args...)
	}
}

// Apply implements policyintake.Applier.
//
// The cleanup of the probe credentials never changes the result: a revocation that failed, or that the guard refused,
// is logged at error level and the outcome of the apply is returned as it was.
func (a *Applier) Apply(ctx context.Context, d policyintake.Doc, g policyintake.Guard) (policyintake.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, ApplyTimeout)
	defer cancel()
	if a.Range == "" {
		return policyintake.Result{}, errors.New("the applier has no range")
	}
	doc, err := policydoc.Decode(d.Raw, policydoc.DecodeOptions{}) // the intake verified the signature
	if err != nil {
		return policyintake.Result{}, err
	}
	cur, err := a.Ledger.InForce()
	if err != nil {
		return policyintake.Result{}, err
	}
	// Only the stale base is checked here, and only so that the checks below (the sizes, the expectations) never run
	// against a list set in force that the document was not based on: it is the very test Apply makes first, and it
	// refuses nothing else. The unchanged-content rule, the rotation step (ADR 0119) and the generation arithmetic are
	// decided by policyapply.Apply alone: a submission on the generation in force, unchanged or a rotation step, passes.
	if cur.Generation != doc.BaseGeneration {
		return policyintake.Result{}, &listset.StaleBaseError{Stated: doc.BaseGeneration, InForce: cur.Generation}
	}
	var prev *listset.Compiled
	if cur.Generation > 0 {
		ls, ok, err := a.Ledger.InForceListSet()
		if err != nil {
			return policyintake.Result{}, err
		}
		if !ok {
			return policyintake.Result{}, fmt.Errorf("%w: generation %d", policyapply.ErrNoInForceListSet, cur.Generation)
		}
		if prev, err = listset.Compile(ls); err != nil {
			return policyintake.Result{}, fmt.Errorf("compile the list set in force (generation %d): %w", cur.Generation, err)
		}
	}
	req := policyapply.ApplyRequest{ListSet: doc.ListSet, Received: d.Raw, Generation: doc.Generation, BaseGeneration: doc.BaseGeneration,
		RotationStep: d.RotationStep}
	issuer := &Issuer{
		Ctx: ctx, Keys: a.Keys, Cluster: policyapply.NATSCluster{Conn: a.Sys, Inventory: a.Servers},
		Resolver: apply.NATS{Conn: a.Sys, Inventory: a.Servers}, Account: a.Account, Range: a.Range, Trust: a.Trust, Servers: a.Servers, Now: a.Now,
	}
	markers, err := a.markersFor()
	if err != nil {
		return policyintake.Result{}, fmt.Errorf("provisioning markers: %w", err)
	}
	prov := a.Provisioner
	failClosed := prov == nil
	deps := policyapply.ApplyDeps{
		Ledger: a.Ledger, Cluster: issuer.Cluster, Signer: issuer, Account: a.Account, Inventory: a.Servers,
		Provisioner: prov, ProvisionMarkers: markers, IssueRecord: a.Record, BeforeWrite: BeforeWrite(g), Now: a.Now,
	}

	target, cerr := listset.Compile(doc.ListSet)
	if failClosed {
		deps.Provisioner = Provisioner{Keys: a.Keys, Sets: []*listset.Compiled{prev, target}}
	}
	if cerr != nil {
		// A list set that does not validate is refused by Apply, with its own status, before anything is signed or pushed.
		return a.finish(a.apply(ctx, req, deps))
	}

	// What the servers allow. Fewer answers than servers is a refusal: the limits are never guessed.
	read := a.readServers
	if read == nil {
		read = func(ctx context.Context) ([]ServerInfo, error) { return ReadServers(ctx, a.Sys, a.Servers) }
	}
	servers, err := read(ctx)
	if err != nil {
		return policyintake.Result{}, fmt.Errorf("refusing to apply: %w; nothing was pushed", err)
	}
	if servers, err = ResolveURLs(servers, a.URL, a.Servers); err != nil {
		return policyintake.Result{}, fmt.Errorf("refusing to apply: %w; nothing was pushed", err)
	}
	if a.Vet != nil {
		for _, s := range servers {
			if err := a.Vet(s.URL); err != nil {
				return policyintake.Result{}, fmt.Errorf("refusing to connect to server %.64s: %w; nothing was pushed", s.Name, err)
			}
		}
	}
	if ps := listset.SizeCheck(target, Limits(servers)); len(ps) > 0 {
		st := policyapply.Status{
			Generation: doc.Generation, State: policyapply.StateRefused, UpdatedAt: a.now().UTC().Format(time.RFC3339),
		}
		for _, p := range ps {
			st.Refusals = append(st.Refusals, listset.Refusal{Stage: StageSize, Entry: p.Entry, Reason: p.Reason})
		}
		if err := a.Ledger.WriteRefusal(ctx, st); err != nil {
			return policyintake.Result{}, fmt.Errorf("the list set does not fit the servers and the refusal could not be recorded: %w", err)
		}
		return policyintake.Result{StatusWritten: true}, errors.New("the list set does not fit the servers' limits (see the status)")
	}
	// The change must not need a step that is not available.
	ex, err := policyapply.NewExpect(prev, target, nil)
	if err != nil {
		return policyintake.Result{}, err
	}
	if failClosed && len(policyapply.NewOrder(prev, target, ex).NewAddresses) > 0 {
		return policyintake.Result{}, errors.New(NewAddressDetail)
	}
	if len(ex.CredentialOnly) > 0 {
		return policyintake.Result{}, errors.New(NarrowingDetail)
	}
	if err := g.Before(ctx, "keys"); err != nil {
		return policyintake.Result{}, err
	}
	signers := map[string]string{}
	for _, tpl := range target.Templates {
		kp, err := a.Keys.GroupKey(tpl.Group, true) // never overwrites; the key of a new owner group is made here
		if err != nil {
			return policyintake.Result{}, fmt.Errorf("owner group %s: %w", tpl.Group, err)
		}
		pub, err := kp.PublicKey()
		kp.Wipe()
		if err != nil {
			return policyintake.Result{}, fmt.Errorf("owner group %s: %w", tpl.Group, err)
		}
		signers[tpl.Group] = pub
	}
	deps.Signers = signers
	if deps.KeptSigners, err = KeptSigners(a.Keys, a.Range); err != nil {
		return policyintake.Result{}, err
	}
	deps.ServerLimits = Limits(servers)
	deps.Inventory = max(a.Servers, len(servers))
	deps.Cluster = policyapply.NATSCluster{Conn: a.Sys, Inventory: deps.Inventory}

	// The probes and witnesses are opened before the push, for the senders of the generation in force.
	minter := &policyapply.ProbeMinter{Signer: GroupSigner{a.Keys}, Account: a.Account, Record: a.Record, Now: a.Now}
	prober, wit := a.transports(servers, minter)
	var loaders []*nats.Conn
	defer func() {
		for _, c := range loaders {
			c.Close()
		}
	}()
	// Whatever the outcome, the probes are revoked and the witnesses closed.
	defer func() {
		cctx, ccancel := context.WithTimeout(context.WithoutCancel(ctx), CleanupTimeout)
		defer ccancel()
		wit.Close()
		a.cleanupProbes(cctx, g, issuer, prober, wit)
	}()
	if a.Load != nil {
		for _, s := range servers {
			c, err := a.Load(ctx, s.URL)
			if err != nil {
				return policyintake.Result{}, fmt.Errorf("open a connection to server %.64s: %w; nothing was pushed", s.Name, err)
			}
			loaders = append(loaders, c)
		}
	}
	if prev != nil && len(prev.Users) > 0 {
		var creds []policyapply.ProbeCred
		for _, u := range prev.Users {
			creds = append(creds, policyapply.ProbeCred{Sender: u.Sender, Name: u.Name, Tags: slices.Clone(u.Tags)})
		}
		if err := wit.Open(ctx, URLs(servers), minter, creds); err != nil {
			return policyintake.Result{}, fmt.Errorf("open the witness connections: %w; nothing was pushed", err)
		}
	}
	deps.Prober, deps.Witnesses = prober, wit

	// Senders that leave have their keys revoked after the push.
	if len(ex.RemovedSenders) > 0 {
		plan := &policyapply.RotationPlan{
			Leaving: ex.RemovedSenders, Generation: req.Generation,
			Issuer:    policyapply.GroupUserIssuer{Signer: GroupSigner{a.Keys}, Account: a.Account},
			Deliverer: FailClosedDeliverer{}, Conns: policyapply.NATSConnections{Conn: a.Sys},
			Record: a.Record, Revoker: issuer, Now: a.Now,
		}
		deps.AfterPush = policyapply.RotationsAfterPush(plan)
	}
	return a.finish(a.apply(ctx, req, deps))
}

func (a *Applier) apply(ctx context.Context, req policyapply.ApplyRequest, deps policyapply.ApplyDeps) (policyapply.Status, error) {
	if a.applyFn != nil {
		return a.applyFn(ctx, req, deps)
	}
	return policyapply.Apply(ctx, req, deps)
}

// transports makes the live prober and witnesses, with the deployment's connection options.
func (a *Applier) transports(servers []ServerInfo, minter *policyapply.ProbeMinter) (*policyapply.LiveProber, *policyapply.LiveWitnesses) {
	return &policyapply.LiveProber{URLs: URLs(servers), Mint: minter, Options: a.Options}, &policyapply.LiveWitnesses{Options: a.Options}
}

func (a *Applier) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// finish turns the outcome of Apply into the intake's result. A status that is not applied is an error too, so the
// intake does not take the document for an applied one (and does not record a planned rotation for it).
//
// Whether the status was written is read from the status itself: a run writes one for every state but the zero Status,
// and for an applied one only when it returned no error. An applied status that came with an error is the exception
// the ledger decides: if the generation is in force by now (the commit happened and a later step failed), the policy is
// applied and the intake must not write an apply refusal over it, so the result says the status is taken care of and
// the error is only logged by the intake.
func (a *Applier) finish(st policyapply.Status, err error) (policyintake.Result, error) {
	var l inForceReader
	if a.Ledger != nil {
		l = a.Ledger
	}
	res := policyintake.Result{StatusWritten: statusWritten(st, err, l)}
	switch {
	case err != nil:
		return res, err
	case st.State != policyapply.StateApplied:
		msg := fmt.Sprintf("generation %d was not applied: state %s (see the status)", st.Generation, st.State)
		if d := detailOf(st); d != "" {
			msg += ": " + d
		}
		return res, errors.New(msg)
	}
	a.logf("applied", "generation", st.Generation)
	return res, nil
}

// inForceReader reads the record in force.
type inForceReader interface {
	InForce() (listset.Record, error)
}

// statusWritten decides whether the intake must not write a refusal over what the run left. The decision for an applied
// status that came with an error is made from the ledger's in-force record, never from the error: the generation is
// in force exactly when the record says so.
func statusWritten(st policyapply.Status, err error, l inForceReader) bool {
	if st.State == "" {
		return false
	}
	if err == nil || st.State != policyapply.StateApplied {
		return true
	}
	if l == nil {
		return false
	}
	rec, lerr := l.InForce()
	return lerr == nil && rec.Generation == st.Generation
}

// detailOf is the first failure or refusal of a status as one line, for a log.
func detailOf(st policyapply.Status) string {
	var parts []string
	for _, f := range st.Failing {
		parts = append(parts, f.Check+": "+f.Detail)
	}
	for _, r := range st.Refusals {
		parts = append(parts, r.Stage+": "+r.Reason)
	}
	return strings.Join(parts, "; ")
}

// KeptSigners is the scoped signing keys the account JWT keeps beside this range's: the key store's owner group
// keys whose group is not one of the range's ("range." prefix), the same split Issuer.Sign makes. It maps group to
// public key.
func KeptSigners(keys Keys, rng string) (map[string]string, error) {
	pubs, err := keys.GroupPublics()
	if err != nil {
		return nil, fmt.Errorf("owner group keys: %w", err)
	}
	out := map[string]string{}
	for g, pub := range pubs {
		if !strings.HasPrefix(g, rng+".") {
			out[g] = pub
		}
	}
	return out, nil
}
