package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policydoc"
	"github.com/bogworks/mash/internal/policyserve"
	"github.com/bogworks/mash/internal/policyview"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

const (
	serveRange         = "foo"
	serveGroup         = "foo.c"
	serveBucketPattern = "ops_{range}_policy"
	serveBucket        = "ops_foo_policy"
	// serveWait bounds every wait of these tests; a loaded runner under -race has taken several seconds for one apply.
	serveWait = 60 * time.Second
	// serveStopTry bounds one request after the service stopped; serveStopPoll is the wait between tries.
	serveStopTry  = time.Second
	serveStopPoll = 50 * time.Millisecond
	// servePoll is how often a test reads the status again.
	servePoll = 50 * time.Millisecond
	// serveStopWait bounds how long the service has to stop once it is told to.
	serveStopWait = 30 * time.Second
)

// recordingProv stands in for the provisioner of new addresses, which production does not have yet: it records the
// addresses it was asked for and provisions nothing.
type recordingProv struct {
	mu      sync.Mutex
	created []string
	issued  []string
}

func (p *recordingProv) CreateInbox(_ context.Context, a string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.created = append(p.created, a)
	return nil
}

func (p *recordingProv) IssueCredential(_ context.Context, a string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.issued = append(p.issued, a)
	return nil
}

func withProvisioner(t *testing.T, p policyapply.Provisioner) {
	t.Helper()
	old := policyServeProvisioner
	policyServeProvisioner = p
	t.Cleanup(func() { policyServeProvisioner = old })
}

// serveEnv is an operator-mode server with an adopted account, the policy bucket, and `policy serve` running on it.
type serveEnv struct {
	*applyEnv
	sub    nkeys.KeyPair
	subPub string
	nc     *nats.Conn         // the admin's connection
	kv     jetstream.KeyValue // the bucket as the admin sees it: it reads the status
	// subNC and subKV are the outside system's connection and bucket: its user may publish only to the list-set keys.
	subNC   *nats.Conn
	subKV   jetstream.KeyValue
	subErrs chan error // the asynchronous errors of subNC (a refused publish is one)
	logs    *syncBuf
	stop    context.CancelFunc
	done    chan int
}

// newServeEnvNoService builds the server, the bucket and the adoption; the service is not started.
func newServeEnvNoService(t *testing.T, adopt bool) *serveEnv {
	t.Helper()
	return newServeEnvOpts(t, adopt, nil)
}

// submitterPerms are the permissions of the outside system's user (docs/operations.md, List-set intake): it may write
// only the list-set keys of the bucket. The KV API puts by publishing to $KV.<bucket>.<key> and reads the reply on the
// user's own inbox; binding to the bucket asks for the stream's info.
func submitterPerms() jwt.Permissions {
	var p jwt.Permissions
	p.Pub.Allow.Add("$KV."+serveBucket+".listset.>", "$JS.API.STREAM.INFO.KV_"+serveBucket)
	p.Sub.Allow.Add("_INBOX.>")
	return p
}

// newServeEnvOpts is newServeEnvNoService with a hook on the server options.
func newServeEnvOpts(t *testing.T, adopt bool, optsHook func(o *server.Options)) *serveEnv {
	t.Helper()
	return newServeEnvFull(t, adopt, nil, optsHook, nil)
}

// newServeEnvFull is newServeEnvOpts with a hook that runs after the files are written and before the server starts
// (tweak), and one that runs after the server is started and before the directory is made (afterStart), where a test
// brings up the rest of a cluster.
func newServeEnvFull(t *testing.T, adopt bool, tweak func(e *applyEnv), optsHook func(o *server.Options), afterStart func(e *applyEnv)) *serveEnv {
	t.Helper()
	e := &serveEnv{applyEnv: newApplyEnvOpts(t, tweak, optsHook), logs: &syncBuf{}}
	if afterStart != nil {
		afterStart(e.applyEnv)
	}
	e.env["MASH_POLICY_BUCKET"] = serveBucketPattern
	if _, errb, code := e.run("init"); code != 0 {
		t.Fatalf("init: %s", errb)
	}
	e.sub = mustT(nkeys.CreateAccount()) // the outside system's key: a key mash does not hold
	e.subPub = mustT(e.sub.PublicKey())
	if adopt {
		if out, errb, code := e.run("policy", "adopt", "--submitter", e.subPub); code != 0 {
			t.Fatalf("adopt: %d %s %s", code, out, errb)
		}
	}
	e.nc = mustT(nats.Connect(e.srv.ClientURL(), natsauth.UserCredentials(e.env["MASH_ADMIN_CREDS"])))
	t.Cleanup(e.nc.Close)
	js := mustT(jetstream.New(e.nc))
	ctx, cancel := context.WithTimeout(context.Background(), serveWait)
	defer cancel()
	e.kv = mustT(js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: serveBucket}))

	// The outside system's user, signed by the account key like any user, limited as the docs say.
	akp, _, err := e.ks.Account()
	if err != nil {
		t.Fatal(err)
	}
	ukp := mustT(nkeys.CreateUser())
	uc := jwt.NewUserClaims(mustT(ukp.PublicKey()))
	uc.Name = "submitter"
	uc.Permissions = submitterPerms()
	tok := mustT(uc.Encode(akp))
	e.subErrs = make(chan error, 16)
	e.subNC = mustT(nats.Connect(e.srv.ClientURL(),
		nats.UserJWT(func() (string, error) { return tok, nil }, func(n []byte) ([]byte, error) { return ukp.Sign(n) }),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			select {
			case e.subErrs <- err:
			default:
			}
		})))
	t.Cleanup(e.subNC.Close)
	e.subKV = mustT(mustT(jetstream.New(e.subNC)).KeyValue(ctx, serveBucket))
	return e
}

// start runs `policy serve` in process and waits until it is watching.
func (e *serveEnv) start(extra ...string) {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	e.stop = cancel
	ready := make(chan struct{})
	var once sync.Once
	oldCtx, oldReady := policyServeContext, policyServeReady
	policyServeContext = func() (context.Context, context.CancelFunc) { return ctx, cancel }
	policyServeReady = func() { once.Do(func() { close(ready) }) }
	e.t.Cleanup(func() { policyServeContext, policyServeReady = oldCtx, oldReady })
	e.done = make(chan int, 1)
	args := append([]string{"policy", "serve", "--range", serveRange}, extra...)
	if !slices.Contains(extra, "--servers") {
		args = append(args, "--servers", "1") // the declared server count is required
	}
	go func() {
		var out bytes.Buffer
		e.done <- runEnv(args, func(k string) string { return e.env[k] }, &out, e.logs)
	}()
	select {
	case <-ready:
	case code := <-e.done:
		e.t.Fatalf("service exited %d: %s", code, e.logs.String())
	case <-time.After(serveWait):
		e.t.Fatal("service did not start")
	}
	e.t.Cleanup(func() { cancel(); <-e.done })
}

// shutdown stops the service and returns its exit code.
func (e *serveEnv) shutdown() int {
	e.t.Helper()
	e.stop()
	select {
	case code := <-e.done:
		e.done <- code // the cleanup reads it again
		return code
	case <-time.After(serveStopWait):
		e.t.Fatal("service did not stop")
		return -1
	}
}

// serveDoc is the document of one generation; mod may change the fields.
func (e *serveEnv) serveDoc(gen, base int, mod func(m map[string]any)) []byte {
	m := map[string]any{
		"schema": policydoc.DocumentSchema, "account": e.acctPub, "range": serveRange, "generation": gen, "base_generation": base,
		"issued_at": "2026-11-14T12:00:00Z",
		"groups": map[string]any{
			"c": map[string]any{"mail": "closed", "prompt": "closed", "status": "shared", "heartbeat": "closed"},
		},
		"senders": map[string]any{
			"foo.c.core": map[string]any{"allow": []string{"foo.c.lead"}, "deny": []string{}},
			"foo.c.lead": map[string]any{"allow": []string{}, "deny": []string{}},
			"foo.c.sdk":  map[string]any{"allow": []string{}, "deny": []string{}},
		},
	}
	if mod != nil {
		mod(m)
	}
	return mustT(json.Marshal(m))
}

// signed is payload as a compact JWS signed by kp.
func signed(t *testing.T, kp nkeys.KeyPair, payload []byte) []byte {
	t.Helper()
	enc := base64.RawURLEncoding
	in := enc.EncodeToString([]byte(`{"alg":"EdDSA","typ":"JWT"}`)) + "." + enc.EncodeToString(payload)
	sig, err := kp.Sign([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	return []byte(in + "." + enc.EncodeToString(sig))
}

// put writes the document to the account's list-set key as the outside system's user.
func (e *serveEnv) put(doc []byte) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), serveWait)
	defer cancel()
	if _, err := e.subKV.Put(ctx, "listset."+e.acctPub, doc); err != nil {
		e.t.Fatal(err)
	}
}

// statusValue is the part of the status value these tests read.
type statusValue struct {
	InForce *struct {
		Generation uint64 `json:"generation"`
	} `json:"in_force"`
	Status *policyapply.Status `json:"status"`
}

func (e *serveEnv) status() (statusValue, bool) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), serveWait)
	defer cancel()
	ent, err := e.kv.Get(ctx, policyapply.StatusKey(e.acctPub))
	if err != nil {
		return statusValue{}, false
	}
	var v statusValue
	if err := json.Unmarshal(ent.Value(), &v); err != nil {
		e.t.Fatalf("status value: %v", err)
	}
	return v, true
}

// waitStatus waits until the status satisfies ok and returns it.
func (e *serveEnv) waitStatus(what string, ok func(st *policyapply.Status) bool) *policyapply.Status {
	e.t.Helper()
	deadline := time.Now().Add(serveWait)
	for time.Now().Before(deadline) {
		if v, found := e.status(); found && v.Status != nil && ok(v.Status) {
			return v.Status
		}
		time.Sleep(servePoll)
	}
	v, _ := e.status()
	e.t.Fatalf("timed out waiting for %s; status %+v; log:\n%s", what, v.Status, e.logs.String())
	return nil
}

func (e *serveEnv) inForce() uint64 {
	e.t.Helper()
	v, found := e.status()
	if !found || v.InForce == nil {
		return 0
	}
	return v.InForce.Generation
}

// scopedFor returns the template permissions the account JWT carries for the group, and how many signing keys it has.
func (e *serveEnv) scopedFor(group string) (perms *jwt.UserScope, keys int) {
	c := e.claims()
	for _, s := range scopedSigners(c) {
		if s.Role == group {
			perms = s
		}
	}
	return perms, len(c.SigningKeys)
}

// shape is the account JWT's signing keys with their templates: what a list set changes. (The JWT as a whole also
// changes when the probe credentials of an apply are revoked, which happens just after the status is written.)
func (e *serveEnv) shape() string {
	e.t.Helper()
	c := e.claims()
	return string(mustT(json.Marshal(c.SigningKeys)))
}

func TestPolicyServeAppliesGenerations(t *testing.T) {
	prov := &recordingProv{}
	withProvisioner(t, prov)
	e := newServeEnvNoService(t, true)
	e.start()

	// Generation 1: the group's scoped key and template reach the servers, and the status says applied.
	e.put(signed(t, e.sub, e.serveDoc(1, 0, nil)))
	st := e.waitStatus("generation 1 applied", func(s *policyapply.Status) bool { return s.Generation == 1 && s.State == policyapply.StateApplied })
	if !st.InForce || len(st.Failing) != 0 {
		t.Fatalf("status %+v", st)
	}
	us, n := e.scopedFor(serveGroup)
	if us == nil || n != 1 {
		t.Fatalf("scoped signer for %s: %v, signing keys %d", serveGroup, us, n)
	}
	gk := mustT(e.ks.GroupKey(serveGroup, false))
	if gpub := mustT(gk.PublicKey()); us.Key != gpub {
		t.Errorf("the scoped key is %s, want the key store's group key", us.Key)
	}
	if e.inForce() != 1 {
		t.Errorf("generation in force %d", e.inForce())
	}
	first := e.shape()
	firstTemplate := mustT(json.Marshal(us.Template))
	prov.mu.Lock()
	if len(prov.created) != 3 || len(prov.issued) != 3 {
		t.Errorf("provisioned %v and %v", prov.created, prov.issued)
	}
	prov.mu.Unlock()

	// Generation 2: the status verb closes (a template change) and core may mail sdk (an added allow).
	doc2 := signed(t, e.sub, e.serveDoc(2, 1, func(m map[string]any) {
		m["groups"].(map[string]any)["c"].(map[string]any)["status"] = "closed"
		m["senders"].(map[string]any)["foo.c.core"] = map[string]any{"allow": []string{"foo.c.lead", "foo.c.sdk"}, "deny": []string{}}
	}))
	e.put(doc2)
	st = e.waitStatus("generation 2 applied", func(s *policyapply.Status) bool { return s.Generation == 2 && s.State == policyapply.StateApplied })
	if !st.InForce {
		t.Fatalf("status %+v", st)
	}
	second := e.shape()
	if second == first {
		t.Fatal("the signing keys did not change")
	}
	if e.inForce() != 2 {
		t.Fatalf("generation in force %d", e.inForce())
	}
	us2, n2 := e.scopedFor(serveGroup)
	if us2 == nil || n2 != 1 || us2.Key != us.Key || string(mustT(json.Marshal(us2.Template))) == string(firstTemplate) {
		t.Errorf("after generation 2: scoped signer %v, signing keys %d", us2, n2)
	}
	prov.mu.Lock()
	if len(prov.created) != 3 {
		t.Errorf("generation 2 provisioned more: %v", prov.created)
	}
	prov.mu.Unlock()

	// A document signed by another key is refused and changes nothing.
	other := mustT(nkeys.CreateAccount())
	e.put(signed(t, other, e.serveDoc(3, 2, nil)))
	st = e.waitStatus("the wrong-key refusal", func(s *policyapply.Status) bool { return s.State == policyapply.StateRefused })
	if len(st.Refusals) != 1 || st.Refusals[0].Stage != "signature" {
		t.Fatalf("refusals %+v", st.Refusals)
	}
	if e.shape() != second || e.inForce() != 2 {
		t.Error("a refused document changed the account")
	}

	// A stale document (generation 2 again, based on 1, a different document) is refused at the apply stage.
	stale := signed(t, e.sub, e.serveDoc(2, 1, func(m map[string]any) { m["issued_at"] = "2026-11-14T13:00:00Z" }))
	e.put(stale)
	st = e.waitStatus("the stale refusal", func(s *policyapply.Status) bool {
		return s.State == policyapply.StateRefused && len(s.Refusals) == 1 && s.Refusals[0].Stage == "apply"
	})
	if !strings.Contains(strings.ToLower(st.Refusals[0].Reason), "stale") && !strings.Contains(st.Refusals[0].Reason, "base") {
		t.Errorf("refusal %+v", st.Refusals[0])
	}
	if e.shape() != second || e.inForce() != 2 {
		t.Error("a stale document changed the account")
	}
	e.noSeeds(e.logs.String())

	// The service stops cleanly on its context.
	if code := e.shutdown(); code != 0 {
		t.Fatalf("exit %d: %s", code, e.logs.String())
	}
	if strings.Contains(e.logs.String(), "could not revoke") {
		t.Errorf("the probe credentials were not revoked: %s", e.logs.String())
	}
	if !strings.Contains(e.logs.String(), "policy service stopped") {
		t.Errorf("log: %s", e.logs.String())
	}
}

func TestPolicyServeRefusesWhenFewerServersAnswer(t *testing.T) {
	e := newServeEnvNoService(t, true)
	e.start("--servers", "2") // the deployment is declared to have two servers; one answers
	before := e.shape()
	e.put(signed(t, e.sub, e.serveDoc(1, 0, nil)))
	st := e.waitStatus("the refusal", func(s *policyapply.Status) bool { return s.State == policyapply.StateRefused })
	if len(st.Refusals) != 1 || st.Refusals[0].Stage != "apply" || !strings.Contains(st.Refusals[0].Reason, "1 of 2 servers") {
		t.Fatalf("refusals %+v", st.Refusals)
	}
	if e.shape() != before || e.inForce() != 0 {
		t.Error("the account changed although a server did not answer")
	}
}

// ownerDoc is generation 1 for the deployment's own owner (the owner `mashctl add` mints for): one group, and the
// senders given, each allowed to mail the next.
func (e *serveEnv) ownerDoc(gen, base int, names ...string) []byte {
	return e.serveDoc(gen, base, func(m map[string]any) {
		owner := e.env["MASH_OWNER"]
		m["groups"] = map[string]any{owner: map[string]any{"mail": "closed", "prompt": "closed", "status": "shared", "heartbeat": "closed"}}
		senders := map[string]any{}
		for i, n := range names {
			allow := []string{}
			if i+1 < len(names) {
				allow = append(allow, serveRange+"."+owner+"."+names[i+1])
			}
			senders[serveRange+"."+owner+"."+n] = map[string]any{"allow": allow, "deny": []string{}}
		}
		m["senders"] = senders
	})
}

// adoptLater adopts the account after its endpoints were made: `mashctl add` refuses an adopted account, so an
// account that has endpoints made by add is adopted afterwards.
func (e *serveEnv) adoptLater() {
	e.t.Helper()
	if out, errb, code := e.run("policy", "adopt", "--submitter", e.subPub); code != 0 {
		e.t.Fatalf("adopt: %d %s %s", code, out, errb)
	}
}

// add runs `mashctl add NAME` in the serve environment, before the account is adopted.
func (e *serveEnv) add(name string) string {
	e.t.Helper()
	out, errb, code := e.run("add", name)
	if code != 0 {
		e.t.Fatalf("add %s: %d %s %s", name, code, out, errb)
	}
	return filepath.Join(e.env["MASH_CONFIG_DIR"], name+".creds")
}

// A list set that adds an address with no inbox is refused before anything is pushed, and says why in plain words.
func TestPolicyServeRefusesAnAddressWithNoInbox(t *testing.T) {
	e := newServeEnvNoService(t, false)
	e.add("core") // "ghost" has no endpoint
	e.adoptLater()
	e.start()
	before := e.shape()
	e.put(signed(t, e.sub, e.ownerDoc(1, 0, "core", "ghost")))
	st := e.waitStatus("the refusal", func(s *policyapply.Status) bool { return s.State == policyapply.StateRefused })
	if len(st.Refusals) != 1 || st.Refusals[0].Stage != "apply" {
		t.Fatalf("refusals %+v", st.Refusals)
	}
	r := st.Refusals[0].Reason
	if !strings.Contains(r, serveRange+"."+e.env["MASH_OWNER"]+".ghost") || !strings.Contains(r, "no inbox yet") ||
		!strings.Contains(r, "credential") || strings.Contains(r, ".core,") {
		t.Fatalf("reason %q", r)
	}
	if e.shape() != before || e.inForce() != 0 {
		t.Error("the account changed")
	}
}

// The first list set of an account whose endpoints were made with `mashctl add` is applied by the service with no
// provisioner of its own: each address already has the inbox `add` made.
func TestPolicyServeAppliesAFirstListSetOfAddMadeEndpoints(t *testing.T) {
	e := newServeEnvNoService(t, false)
	corePath := e.add("core")
	e.add("lead")
	e.adoptLater()
	e.start()
	owner := e.env["MASH_OWNER"]
	group := serveRange + "." + owner
	e.put(signed(t, e.sub, e.ownerDoc(1, 0, "core", "lead")))
	st := e.waitStatus("generation 1 applied", func(s *policyapply.Status) bool { return s.Generation == 1 && s.State == policyapply.StateApplied })
	if !st.InForce || len(st.Failing) != 0 || len(st.Refusals) != 0 {
		t.Fatalf("status %+v", st)
	}
	if e.inForce() != 1 {
		t.Fatalf("generation in force %d", e.inForce())
	}
	us, n := e.scopedFor(group)
	if us == nil || n != 1 {
		t.Fatalf("scoped signer for %s: %v, signing keys %d", group, us, n)
	}
	gk := mustT(e.ks.GroupKey(group, false))
	if us.Key != mustT(gk.PublicKey()) {
		t.Errorf("the scoped key is not the key store's group key")
	}
	if len(us.Template.Pub.Allow) == 0 {
		t.Errorf("the scoped key carries no template: %+v", us.Template)
	}
	// What add made is untouched: the directory still has both endpoints and an add-made credential still connects.
	nc, err := nats.Connect(e.srv.ClientURL(), natsauth.UserCredentials(corePath), nats.Timeout(serveWait))
	if err != nil {
		t.Fatalf("an add-made credential no longer connects: %v", err)
	}
	nc.Close()
	e.noSeeds(e.logs.String())
	if code := e.shutdown(); code != 0 {
		t.Fatalf("exit %d: %s", code, e.logs.String())
	}
}

// With a change that adds an address, the refusal text says an inbox is missing; what the user can do about it is
// in the text too (the endpoint first, then the credential).
func TestPolicyServeRefusalForAnUnprovisionedAddressSaysWhatToDo(t *testing.T) {
	e := newServeEnvNoService(t, true)
	e.start()
	e.put(signed(t, e.sub, e.serveDoc(1, 0, func(m map[string]any) {
		m["senders"] = map[string]any{"foo.c.core": map[string]any{"allow": []string{}, "deny": []string{}}}
	})))
	st := e.waitStatus("the refusal", func(s *policyapply.Status) bool { return s.State == policyapply.StateRefused })
	if len(st.Refusals) != 1 || !strings.Contains(st.Refusals[0].Reason, "provisioned first") || e.inForce() != 0 {
		t.Fatalf("refusals %+v", st.Refusals)
	}
}

func TestPolicyServeRefusesAnAccountThatIsNotAdopted(t *testing.T) {
	e := newServeEnvNoService(t, false)
	var out, errb bytes.Buffer
	done := make(chan int, 1)
	ctx, cancel := context.WithTimeout(context.Background(), serveWait)
	defer cancel()
	old := policyServeContext
	policyServeContext = func() (context.Context, context.CancelFunc) { return ctx, cancel }
	t.Cleanup(func() { policyServeContext = old })
	go func() {
		done <- runEnv([]string{"policy", "serve", "--range", serveRange, "--servers", "1"}, func(k string) string { return e.env[k] }, &out, &errb)
	}()
	select {
	case code := <-done:
		if code != 1 || !strings.Contains(errb.String(), "not adopted") {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
	case <-time.After(serveWait):
		t.Fatal("the service did not refuse")
	}
	if _, found := e.status(); found {
		t.Error("a status was written")
	}
}

func TestPolicyServeRefusesBadArguments(t *testing.T) {
	e := newServeEnvNoService(t, true)
	for _, c := range []struct {
		args []string
		env  map[string]string
		want string
	}{
		{[]string{"policy", "serve"}, nil, "--range is required"},
		{[]string{"policy", "serve", "--range", "foo", "--servers", "0"}, nil, "whole number of servers"},
		{[]string{"policy", "serve", "--range", "foo"}, map[string]string{"MASH_SERVERS": ""}, "--servers (or MASH_SERVERS) is required"},
		{[]string{"policy", "serve", "--range", "foo", "--bogus", "z"}, nil, "unknown"},
		{[]string{"policy", "serve", "--range", "foo"}, map[string]string{"MASH_POLICY_BUCKET": ""}, "MASH_POLICY_BUCKET"},
	} {
		saved := map[string]string{}
		for k, v := range c.env {
			saved[k] = e.env[k]
			e.env[k] = v
		}
		_, errb, code := e.run(c.args...)
		for k, v := range saved {
			e.env[k] = v
		}
		if code != 2 || !strings.Contains(errb, c.want) {
			t.Errorf("%v: exit %d: %s", c.args, code, errb)
		}
	}
}

// stable returns the MASH account's JWT once it has not changed for stableFor: the probe credentials of an apply are
// revoked just after its status is written, so the JWT settles a moment later.
func (e *serveEnv) stable() string {
	e.t.Helper()
	const stableFor = 3 * time.Second
	deadline := time.Now().Add(serveWait)
	last, since := e.accountJWT(), time.Now()
	for time.Now().Before(deadline) {
		time.Sleep(servePoll * 5)
		if cur := e.accountJWT(); cur != last {
			last, since = cur, time.Now()
		} else if time.Since(since) >= stableFor {
			return last
		}
	}
	e.t.Fatal("the account JWT kept changing")
	return ""
}

func TestPolicyServeRefusesNarrowingAndChangesNothing(t *testing.T) {
	withProvisioner(t, &recordingProv{})
	e := newServeEnvNoService(t, true)
	e.start()

	// Generation 1, then generation 2 gives core an allow of sdk (more tags): both apply.
	e.put(signed(t, e.sub, e.serveDoc(1, 0, nil)))
	e.waitStatus("generation 1 applied", func(s *policyapply.Status) bool { return s.Generation == 1 && s.State == policyapply.StateApplied })
	closeStatus := func(m map[string]any) {
		m["groups"].(map[string]any)["c"].(map[string]any)["status"] = "closed" // a template change, as the apply test's generation 2
	}
	widen := func(m map[string]any) {
		closeStatus(m)
		m["senders"].(map[string]any)["foo.c.core"] = map[string]any{"allow": []string{"foo.c.lead", "foo.c.sdk"}, "deny": []string{}}
	}
	e.put(signed(t, e.sub, e.serveDoc(2, 1, widen)))
	e.waitStatus("generation 2 applied", func(s *policyapply.Status) bool { return s.Generation == 2 && s.State == policyapply.StateApplied })
	jwtBefore := e.stable()
	groupsBefore := mustT(e.ks.GroupPublics())
	statusBefore, _ := e.status()

	// Generation 3 takes the allow of sdk away again: that narrows an existing sender by tags.
	e.put(signed(t, e.sub, e.serveDoc(3, 2, closeStatus)))
	st := e.waitStatus("the narrowing refusal", func(s *policyapply.Status) bool { return s.Generation == 3 && s.State == policyapply.StateRefused })
	if len(st.Refusals) != 1 || st.Refusals[0].Stage != "apply" {
		t.Fatalf("refusals %+v", st.Refusals)
	}
	reason := st.Refusals[0].Reason
	for _, want := range []string{"narrows", "not part of this release", "nothing was pushed"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the reason %q does not say %q", reason, want)
		}
	}
	// Nothing was changed or half-applied: the account JWT, the generation in force and the key store are as they were.
	if jwtAfter := e.stable(); jwtAfter != jwtBefore {
		t.Error("the account JWT changed")
	}
	if e.inForce() != 2 {
		t.Errorf("generation in force %d, want 2", e.inForce())
	}
	if statusBefore.InForce == nil || statusBefore.InForce.Generation != 2 {
		t.Errorf("status before %+v", statusBefore)
	}
	if after := mustT(e.ks.GroupPublics()); len(after) != len(groupsBefore) || after[serveGroup] != groupsBefore[serveGroup] {
		t.Errorf("the key store changed: %v then %v", groupsBefore, after)
	}
	e.noSeeds(e.logs.String())
}

func TestPolicyServeSubmitterWritesOnlyListSets(t *testing.T) {
	e := newServeEnvNoService(t, true)
	e.start()
	e.put(signed(t, e.sub, e.serveDoc(1, 0, func(m map[string]any) {
		m["senders"] = map[string]any{} // no sender: nothing to provision
	})))
	e.waitStatus("the first status", func(s *policyapply.Status) bool { return s.Generation == 1 })
	ctx, cancel := context.WithTimeout(context.Background(), serveWait)
	defer cancel()
	statusKey := policyapply.StatusKey(e.acctPub)
	before, err := e.kv.Get(ctx, statusKey)
	if err != nil {
		t.Fatal(err)
	}

	// The submitter writes to the status key: the server refuses the publish.
	for len(e.subErrs) > 0 {
		<-e.subErrs
	}
	wctx, wcancel := context.WithTimeout(ctx, 2*time.Second)
	defer wcancel()
	if _, err := e.subKV.Put(wctx, statusKey, []byte(`{"in_force":{"generation":99}}`)); err == nil {
		t.Fatal("the submitter wrote a status key")
	}
	select {
	case err := <-e.subErrs:
		if !strings.Contains(strings.ToLower(err.Error()), "permissions violation") {
			t.Errorf("error %v", err)
		}
	case <-time.After(serveWait):
		t.Fatal("no permissions violation was reported")
	}
	after, err := e.kv.Get(ctx, statusKey)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision() != before.Revision() || !bytes.Equal(after.Value(), before.Value()) {
		t.Error("the status key changed")
	}
	// It may still write its own key.
	if _, err := e.subKV.Put(ctx, "listset."+e.acctPub, signed(t, e.sub, e.serveDoc(2, 1, func(m map[string]any) { m["senders"] = map[string]any{} }))); err != nil {
		t.Errorf("the submitter cannot write its list set: %v", err)
	}
}

const (
	// serveLargeLine and serveSmallLine are the max_control_line of the two servers of the limits test. The default
	// is 4096.
	serveLargeLine = 4096
	serveSmallLine = 1024
	// serveManyAllows is how many addresses core is allowed in the limits test, which makes its connect line longer
	// than serveSmallLine and shorter than serveLargeLine.
	serveManyAllows = 24
)

// startSecondServer starts a second server, joined to e's by a leaf node of the system account, with the same operator and
// account JWTs and the given max_control_line. The system account sees both servers. It has no JetStream: the limits
// query and the size check need only the system account.
func startSecondServer(t *testing.T, e *serveEnv, leafPort int, maxControlLine int32) {
	t.Helper()
	opts, err := server.ProcessConfigFile(e.pl.ConfPath())
	if err != nil {
		t.Fatal(err)
	}
	res := &server.MemAccResolver{}
	for _, pub := range []string{e.pl.System, e.acctPub} {
		tok, err := os.ReadFile(filepath.Join(e.pl.Dir, "resolver", pub+".jwt"))
		if err != nil {
			t.Fatal(err)
		}
		if err := res.Store(pub, string(tok)); err != nil {
			t.Fatal(err)
		}
	}
	opts.Port, opts.NoLog, opts.NoSigs = -1, true, true
	opts.ServerName = "second"
	opts.AccountResolver = res
	opts.JetStream, opts.StoreDir = false, ""
	opts.MaxControlLine = maxControlLine
	opts.LeafNode.Remotes = []*server.RemoteLeafOpts{{
		URLs:         []*url.URL{{Scheme: "nats", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(leafPort))}},
		LocalAccount: e.pl.System, Credentials: e.env["MASH_SYS_CREDS"],
	}}
	s, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(serveWait) {
		t.Fatal("the second server did not start")
	}
	t.Cleanup(func() { s.Shutdown(); s.WaitForShutdown() })
}

// The two servers report different limits through the system account; the smaller one decides the size check, and the
// refusal names it.
func TestPolicyServeServerLimitsSmallerWins(t *testing.T) {
	l := mustT(net.Listen("tcp", "127.0.0.1:0"))
	leafPort := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	e := newServeEnvOpts(t, true, func(o *server.Options) {
		o.ServerName = "first"
		o.MaxControlLine = serveLargeLine
		o.LeafNode.Host, o.LeafNode.Port = "127.0.0.1", leafPort
	})
	startSecondServer(t, e, leafPort, serveSmallLine)

	// Both servers answer the limits query through the system account, each with its own limit.
	sys := e.connect(natsauth.UserCredentials(e.env["MASH_SYS_CREDS"]))
	deadline := time.Now().Add(serveWait)
	var got []policyserve.ServerInfo
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), serveWait)
		got, _ = policyserve.ReadServers(ctx, sys, 2)
		cancel()
		if len(got) == 2 {
			break
		}
		time.Sleep(servePoll)
	}
	if len(got) != 2 {
		t.Fatalf("limits read from %d servers, want 2", len(got))
	}
	lines := map[string]int{}
	for _, s := range got {
		lines[s.Name] = s.Limits.MaxConnectLine
	}
	if lines["first"] != serveLargeLine || lines["second"] != serveSmallLine {
		t.Fatalf("limits read: %v", lines)
	}

	// A list set whose core credential fits the larger limit and not the smaller one.
	allows := make([]string, 0, serveManyAllows)
	senders := map[string]any{"foo.c.lead": map[string]any{"allow": []string{}, "deny": []string{}}}
	for i := range serveManyAllows {
		a := fmt.Sprintf("foo.c.peer%02d", i)
		allows = append(allows, a)
		senders[a] = map[string]any{"allow": []string{}, "deny": []string{}}
	}
	senders["foo.c.core"] = map[string]any{"allow": allows, "deny": []string{}}
	e.start("--servers", "2")
	before := e.shape()
	e.put(signed(t, e.sub, e.serveDoc(1, 0, func(m map[string]any) { m["senders"] = senders })))
	st := e.waitStatus("the size refusal", func(s *policyapply.Status) bool { return s.State == policyapply.StateRefused })
	if len(st.Refusals) == 0 {
		t.Fatalf("status %+v", st)
	}
	for _, r := range st.Refusals {
		if r.Stage != "size" {
			t.Errorf("stage %q: %+v", r.Stage, r)
		}
		if !strings.Contains(r.Reason, fmt.Sprintf("over the %d byte limit of server \"second\"", serveSmallLine)) {
			t.Errorf("the refusal does not name the smaller limit and its server: %q", r.Reason)
		}
		if strings.Contains(r.Reason, fmt.Sprintf("%d byte limit", serveLargeLine)) {
			t.Errorf("the refusal names the larger limit: %q", r.Reason)
		}
	}
	if e.shape() != before || e.inForce() != 0 {
		t.Error("the account changed")
	}
}

// Every URL derived for a server goes through the TLS and loopback rule of the admin connection.
func TestPolicyServeVetsEveryDerivedURL(t *testing.T) {
	for _, c := range []struct {
		url      string
		insecure bool
		ok       bool
	}{
		{"tls://10.0.0.5:4222", false, true},
		{"nats://10.0.0.5:4222", false, false}, // plain TCP to a remote host
		{"nats://127.0.0.1:4222", false, true},
		{"nats://127.0.0.1:4222", true, true},
		{"nats://10.0.0.5:4222", true, false}, // insecure loopback never reaches a remote host
		{"tls://10.0.0.5:4222", true, false},
	} {
		if err := policyServeVet(c.insecure)(c.url); (err == nil) != c.ok {
			t.Errorf("%s insecure=%v: %v", c.url, c.insecure, err)
		}
	}
}

// The service answers a signed `show` of an endpoint of the account once generation 1 is applied.
func TestPolicyServeAnswersPolicyShow(t *testing.T) {
	e := newServeEnvNoService(t, false)
	// `add` makes legacy endpoints, which the directory resolver names no address for; the responder answers
	// three-token endpoints, so these are provisioned with the agent token set.
	seeds := map[string][]byte{}
	e.env["MASH_AGENT_TOKEN"] = serveRange
	for _, n := range []string{"core", "lead"} {
		kp := mustT(nkeys.CreateUser())
		seeds[n] = mustT(kp.Seed())
		if out, errb, code := e.run("provision", n, "--agent", serveRange, "--user", mustT(kp.PublicKey()), "--account", e.acctPub); code != 0 {
			t.Fatalf("provision %s: %d %s %s", n, code, out, errb)
		}
	}
	delete(e.env, "MASH_AGENT_TOKEN")
	e.adoptLater()
	e.start()
	e.put(signed(t, e.sub, e.ownerDoc(1, 0, "core", "lead")))
	e.waitStatus("generation 1 applied", func(s *policyapply.Status) bool { return s.Generation == 1 && s.State == policyapply.StateApplied })

	id := mustT(sap.NewIdentity(seeds["core"], e.acctPub))
	subject := policyview.Subject(serveRange)
	payload := mustT(json.Marshal(policyview.Request{Op: policyview.OpShow}))
	h := mustT(id.Sign(subject, payload, "core", time.Now(), ""))
	m := nats.NewMsg(subject)
	m.Data = payload
	m.Header.Set(sap.HeaderName, h.Encode())
	ctx, cancel := context.WithTimeout(context.Background(), serveWait)
	defer cancel()
	resp, err := e.nc.RequestMsgWithContext(ctx, m)
	if err != nil {
		t.Fatalf("no answer: %v", err)
	}
	var r policyview.ShowReply
	if err := json.Unmarshal(resp.Data, &r); err != nil || r.Sender != serveRange+"."+e.env["MASH_OWNER"]+".core" || r.Generation != 1 || !r.InForce {
		t.Fatalf("answer %s (%v)", resp.Data, err)
	}
	if code := e.shutdown(); code != 0 {
		t.Fatalf("exit %d: %s", code, e.logs.String())
	}
	// The server drops the stopped responder's subscription asynchronously, so a request right after the stop can still
	// reach it and get no answer: ask again, each try bounded, until the server answers that nobody listens.
	var last error
	for deadline := time.Now().Add(serveWait); time.Now().Before(deadline); time.Sleep(serveStopPoll) {
		m2 := nats.NewMsg(subject)
		m2.Data = payload
		tryCtx, tryCancel := context.WithTimeout(context.Background(), serveStopTry)
		_, last = e.nc.RequestMsgWithContext(tryCtx, m2)
		tryCancel()
		if errors.Is(last, nats.ErrNoResponders) {
			return
		}
	}
	t.Errorf("after the service stopped: %v, want no responders", last)
}

// Without the directory bucket the service refuses to start and says how to make it.
func TestPolicyServeRefusesWithoutTheDirectoryBucket(t *testing.T) {
	e := newServeEnvNoService(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), serveWait)
	defer cancel()
	if err := mustT(jetstream.New(e.nc)).DeleteKeyValue(ctx, store.DirectoryBucket); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	old := policyServeContext
	policyServeContext = func() (context.Context, context.CancelFunc) { return ctx, cancel }
	t.Cleanup(func() { policyServeContext = old })
	code := runEnv([]string{"policy", "serve", "--range", serveRange, "--servers", "1"}, func(k string) string { return e.env[k] }, &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), store.DirectoryBucket) || !strings.Contains(errb.String(), "mashctl init") {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
}
