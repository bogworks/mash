package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policydirect"
)

// directConfig is what a caller that holds the credentials gives the direct driver: the admin and system connections
// and the key store of the environment. No `policy serve` runs and no submission bucket is used.
func (e *serveEnv) directConfig(adm *admin.Admin, sys *nats.Conn, servers int) policydirect.Config {
	e.t.Helper()
	trust, err := apply.LoadTrust(e.ks)
	if err != nil {
		e.t.Fatal(err)
	}
	tlsReq, err := natsauth.RequireTLS(e.srv.ClientURL(), true)
	if err != nil {
		e.t.Fatal(err)
	}
	opts := append(natsauth.TLSOptions(e.tlsCA, "", ""), tlsReq...)
	return policydirect.Config{
		Account: e.acctPub, Admin: adm, Sys: sys, Keys: e.ks, Trust: trust, Servers: servers, URL: e.srv.ClientURL(),
		Vet: policyServeVet(true), Options: opts,
		Load: func(ctx context.Context, u string) (*nats.Conn, error) {
			o := append(slices.Clone(opts), natsauth.UserCredentials(e.env["MASH_ADMIN_CREDS"]),
				nats.Name("policy-direct-test"), nats.Timeout(connectTimeout), nats.MaxReconnects(0))
			return nats.Connect(u, o...)
		},
	}
}

// directDoc is a list-set document for the deployment's own owner: core may mail lead; the status verb is shared unless
// closed is true (which changes the owner group's template), and lead may mail core when leadToCore is true (which
// changes no template).
func (e *serveEnv) directDoc(gen, base int, closed, leadToCore bool) []byte {
	return e.serveDoc(gen, base, func(m map[string]any) {
		owner := e.env["MASH_OWNER"]
		status := "shared"
		if closed {
			status = "closed"
		}
		m["groups"] = map[string]any{owner: map[string]any{"mail": "closed", "prompt": "closed", "status": status, "heartbeat": "closed"}}
		leadAllow := []string{}
		if leadToCore {
			leadAllow = append(leadAllow, owner+".core")
		}
		m["senders"] = map[string]any{
			owner + ".core": map[string]any{"allow": []string{owner + ".lead"}, "deny": []string{}},
			owner + ".lead": map[string]any{"allow": leadAllow, "deny": []string{}},
		}
	})
}

// directTemplates is the template the account JWT each server holds carries for the owner group, by server; a server
// that holds none is absent.
func (e *serveEnv) directTemplates(sys *nats.Conn) map[string]string {
	e.t.Helper()
	owner := e.env["MASH_OWNER"]
	gpub := mustT(mustT(e.ks.GroupKey(owner, false)).PublicKey())
	out := map[string]string{}
	for name, c := range e.accountJWTs(sys) {
		for _, us := range scopedSigners(c) {
			if us.Role == owner && us.Key == gpub {
				out[name] = string(mustT(json.Marshal(us.Template)))
			}
		}
	}
	return out
}

// directShapes is the signing keys and templates each server holds, by server: what a list set changes.
func (e *serveEnv) directShapes(sys *nats.Conn) map[string]string {
	e.t.Helper()
	out := map[string]string{}
	for name, c := range e.accountJWTs(sys) {
		out[name] = string(mustT(json.Marshal(c.SigningKeys)))
	}
	return out
}

// bucketExists reports whether the bucket is there, as the admin sees it.
func bucketExists(t *testing.T, js jetstream.JetStream, name string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), serveWait)
	defer cancel()
	_, err := js.KeyValue(ctx, name)
	switch {
	case err == nil:
		return true
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return false
	}
	t.Fatalf("bucket %s: %v", name, err)
	return false
}

// inForceDirect is the generation the ledger of the default bucket holds.
func inForceDirect(t *testing.T, js jetstream.JetStream, account string) uint64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), serveWait)
	defer cancel()
	l, err := policyapply.NewKVLedger(ctx, js, account, policyapply.KVLedgerOptions{Bucket: policyapply.DefaultPolicyBucket})
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	rec, err := l.InForce()
	if err != nil {
		t.Fatalf("in force: %v", err)
	}
	return rec.Generation
}

func directApply(t *testing.T, cfg policydirect.Config, doc []byte, dry bool) policydirect.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*serveWait)
	defer cancel()
	res, err := policydirect.Apply(ctx, cfg, doc, policydirect.Options{DryRun: dry})
	if err != nil {
		t.Fatalf("apply: %v (%+v)", err, res)
	}
	return res
}

// A caller that holds the account's keys applies a list set to two routed servers directly: no `policy serve`, no
// submission bucket, no adoption marker and no signature. The same content again is unchanged, a dry run changes
// nothing, a stale base is refused, a server held back leaves the policy partial until it is back, and an adopted
// account is refused.
func TestPolicyDirectAppliesOnTwoRoutedServers(t *testing.T) {
	e, _ := newServeEnvCluster(t, false)
	dirAny, ok := clusterSecondDir.Load(e.applyEnv)
	if !ok {
		t.Fatal("no resolver directory for the second server")
	}
	dir := dirAny.(string)
	corePath := e.add("core") // the real add path: a directory record, an inbox and a credential
	e.add("lead")
	adm := e.adminOnly()
	sys := e.connect(natsauth.UserCredentials(e.env["MASH_SYS_CREDS"]))
	js := mustT(jetstream.New(e.nc))
	cfg := e.directConfig(adm, sys, 2)
	if bucketExists(t, js, policyapply.DefaultPolicyBucket) {
		t.Fatalf("the default bucket %s exists before any policy was applied", policyapply.DefaultPolicyBucket)
	}
	before := e.directShapes(sys)

	// A dry run of the first policy: planned, nothing changed, and not even the bucket made.
	doc1 := e.directDoc(1, 0, false, false)
	res := directApply(t, cfg, doc1, true)
	if res.Outcome != policydirect.OutcomePlanned || res.ExitCode() != 0 || res.Plan == nil || res.Plan.Refused || !res.DryRun {
		t.Fatalf("dry run: %+v", res)
	}
	if bucketExists(t, js, policyapply.DefaultPolicyBucket) {
		t.Fatal("a dry run made the policy bucket")
	}
	if now := e.directShapes(sys); !slices.Equal([]string{now["s1"], now["s2"]}, []string{before["s1"], before["s2"]}) {
		t.Fatal("a dry run changed an account JWT")
	}

	// The first policy is applied on both servers.
	res = directApply(t, cfg, doc1, false)
	if res.Outcome != policydirect.OutcomeApplied || res.ExitCode() != 0 || res.Generation != 1 || res.Refused {
		t.Fatalf("apply: %+v", res)
	}
	if res.Status == nil || !res.Status.InForce || len(res.Status.Failing) != 0 || len(res.Status.Servers) != 2 {
		t.Fatalf("status: %+v", res.Status)
	}
	if !bucketExists(t, js, policyapply.DefaultPolicyBucket) {
		t.Fatalf("mash did not create the default bucket %s", policyapply.DefaultPolicyBucket)
	}
	if g := inForceDirect(t, js, e.acctPub); g != 1 {
		t.Fatalf("generation in force %d", g)
	}
	first := e.directTemplates(sys)
	if len(first) != 2 || first["s1"] != first["s2"] || first["s1"] == "" {
		t.Fatalf("the servers hold the templates %v", first)
	}
	if held := e.accountJWTs(sys); len(held) != 2 {
		t.Fatalf("account JWTs from %d servers", len(held))
	} else {
		for name, c := range held {
			var tpl *jwt.UserScope
			for _, us := range scopedSigners(c) {
				if us.Role == e.env["MASH_OWNER"] {
					tpl = us
				}
			}
			if tpl == nil || len(tpl.Template.Pub.Allow) == 0 {
				t.Errorf("server %s carries no template for the owner group", name)
			}
		}
	}
	// What add made is untouched: the credential still connects.
	nc, err := nats.Connect(e.srv.ClientURL(), natsauth.UserCredentials(corePath), nats.Timeout(serveWait))
	if err != nil {
		t.Fatalf("an add-made credential no longer connects: %v", err)
	}
	nc.Close()

	// The same document again is unchanged, and so is the same content under the next generation.
	res = directApply(t, cfg, doc1, false)
	if res.Outcome != policydirect.OutcomeUnchanged || res.ExitCode() != 0 || res.Generation != 1 {
		t.Fatalf("the same document: %+v", res)
	}
	res = directApply(t, cfg, e.serveDoc(2, 1, func(m map[string]any) {
		owner := e.env["MASH_OWNER"]
		m["groups"] = map[string]any{owner: map[string]any{"mail": "closed", "prompt": "closed", "status": "shared", "heartbeat": "closed"}}
		m["senders"] = map[string]any{
			owner + ".core": map[string]any{"allow": []string{owner + ".lead"}, "deny": []string{}},
			owner + ".lead": map[string]any{"allow": []string{}, "deny": []string{}},
		}
		m["issued_at"] = "2026-11-14T15:00:00Z"
	}), false)
	if res.Outcome != policydirect.OutcomeUnchanged || res.Stage != policyapply.StageUnchanged {
		t.Fatalf("the same content as the next generation: %+v", res)
	}
	if g := inForceDirect(t, js, e.acctPub); g != 1 {
		t.Fatalf("generation in force %d after unchanged", g)
	}

	// A dry run of a change that closes the status verb: planned, with the template change in the plan, and nothing
	// changed on either server or in the ledger.
	docTemplate := e.directDoc(2, 1, true, false)
	shapes := e.directShapes(sys)
	res = directApply(t, cfg, docTemplate, true)
	if res.Outcome != policydirect.OutcomePlanned || res.Plan == nil || len(res.Plan.Templates) == 0 {
		t.Fatalf("dry run of a change: %+v", res)
	}
	if now := e.directShapes(sys); now["s1"] != shapes["s1"] || now["s2"] != shapes["s2"] {
		t.Fatal("a dry run changed an account JWT")
	}
	if g := inForceDirect(t, js, e.acctPub); g != 1 {
		t.Fatalf("a dry run moved the generation in force to %d", g)
	}

	// A document whose base is not the generation in force is stale, with the engine's message.
	res = directApply(t, cfg, e.directDoc(1, 0, true, false), false)
	if res.Outcome != policydirect.OutcomeStale || !res.Refused || res.ExitCode() != 1 || res.Stage != "base" {
		t.Fatalf("a stale base: %+v", res)
	}
	if res.Message == "" || e.directShapes(sys)["s1"] != shapes["s1"] {
		t.Fatalf("a stale document changed something or said nothing: %+v", res)
	}

	// The second server takes no push: the change reaches the first only, and the result says which server fails.
	lockResolver(t, dir, true)
	t.Cleanup(func() { lockResolver(t, dir, false) })
	docAllow := e.directDoc(2, 1, false, true) // lead may mail core: no template changes
	res = directApply(t, cfg, docAllow, false)
	if res.Outcome != policydirect.OutcomePartial && res.Outcome != policydirect.OutcomeError {
		t.Fatalf("with a server held back: %+v", res)
	}
	t.Logf("with a server held back: outcome %s, exit %d: %s", res.Outcome, res.ExitCode(), res.Message)
	if res.Outcome == policydirect.OutcomePartial && (res.ExitCode() != 5 || res.Status == nil || res.Status.InForce) {
		t.Errorf("partial: %+v", res)
	}
	if g := inForceDirect(t, js, e.acctPub); g != 1 {
		t.Fatalf("a run that did not pass the gate put generation %d in force", g)
	}

	// The server is back: running the same document again reaches applied.
	lockResolver(t, dir, false)
	for try := 1; ; try++ {
		res = directApply(t, cfg, docAllow, false)
		if res.Outcome == policydirect.OutcomeApplied {
			break
		}
		if try == 3 {
			t.Fatalf("after the server returned, still %+v", res)
		}
		t.Logf("rerun %d: outcome %s: %s", try, res.Outcome, res.Message)
	}
	if g := inForceDirect(t, js, e.acctPub); g != 2 {
		t.Fatalf("generation in force %d, want 2", g)
	}
	if now := e.directShapes(sys); now["s1"] != now["s2"] {
		t.Fatalf("the servers differ after the rerun: %v", now)
	}

	// An account that carries an adoption marker is refused, whatever the document.
	e.adoptLater()
	for _, dry := range []bool{true, false} {
		res = directApply(t, cfg, e.directDoc(3, 2, true, true), dry)
		if res.Outcome != policydirect.OutcomeBlocked || !res.Refused || res.ExitCode() != 1 || res.Stage != "adoption" {
			t.Fatalf("adopted account (dry run %v): %+v", dry, res)
		}
	}
	if g := inForceDirect(t, js, e.acctPub); g != 2 {
		t.Fatalf("generation in force %d after refused applies", g)
	}
	e.noSeeds(res.Message)
}
