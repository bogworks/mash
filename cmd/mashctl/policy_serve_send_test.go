package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policydoc"
	"github.com/bogworks/mash/internal/policyserve"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

const (
	// sendMailWait bounds one send, one open and one read of an inbox.
	sendMailWait = 30 * time.Second
	// sendInboxLimit is how many messages one read of an inbox asks for.
	sendInboxLimit = 20
	// sendOwner is the owner token of the group the serve tests use (serveGroup is serveRange.sendOwner).
	sendOwner = "c"
)

// sendParty is one three-token endpoint of the group: its user key pair (the host's, whose seed mash never holds in
// production) and its address.
type sendParty struct {
	id   sap.EndpointIdentity
	kp   nkeys.KeyPair
	pub  string
	name string
}

// provisionParty makes the endpoint's directory record and inbox for a fresh user key, as `provision --agent` does.
func (e *serveEnv) provisionParty(adm *admin.Admin, name string) *sendParty {
	e.t.Helper()
	id := mustT(sap.New(serveRange, sendOwner, name))
	kp := mustT(nkeys.CreateUser())
	pub := mustT(kp.PublicKey())
	ctx, cancel := context.WithTimeout(context.Background(), serveWait)
	defer cancel()
	if _, err := adm.ProvisionIdentity(ctx, id, nil, pub, e.acctPub, "", admin.Meta{}); err != nil {
		e.t.Fatalf("provision %s: %v", id, err)
	}
	return &sendParty{id: id, kp: kp, pub: pub, name: name}
}

// tagsOf are the tags the signed document gives the sender, as the list set compiles them.
func (e *serveEnv) tagsOf(doc []byte, sender string) []string {
	e.t.Helper()
	d := mustT(policydoc.Decode(doc, policydoc.DecodeOptions{Submitter: e.subPub}))
	c, err := listset.Compile(d.ListSet)
	if err != nil {
		e.t.Fatalf("compile: %v", err)
	}
	for _, u := range c.Users {
		if u.Sender == sender {
			return slices.Clone(u.Tags)
		}
	}
	e.t.Fatalf("no sender %s in the list set", sender)
	return nil
}

// credsFor issues the party's credential the way serve issues one (policyapply.GroupUserIssuer over
// policyserve.GroupSigner, which signs with the group's scoped key in the key store): a scoped user with the party's own
// key, its name and exactly the tags the list set gives it, and no permissions of its own. The template on the account
// JWT supplies the permissions. It returns the path of the creds file.
func (e *serveEnv) credsFor(p *sendParty, doc []byte) string {
	e.t.Helper()
	iss := policyapply.GroupUserIssuer{Signer: policyserve.GroupSigner{Keys: e.ks}, Account: e.acctPub}
	ctx, cancel := context.WithTimeout(context.Background(), serveWait)
	defer cancel()
	tok, err := iss.Issue(ctx, p.id.String(), p.pub, p.name, e.tagsOf(doc, p.id.String()))
	if err != nil {
		e.t.Fatalf("issue %s: %v", p.id, err)
	}
	raw := mustT(jwt.FormatUserConfig(tok, mustT(p.kp.Seed())))
	path := filepath.Join(e.t.TempDir(), p.name+".creds")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		e.t.Fatal(err)
	}
	return path
}

// open connects the party as a real endpoint client under the credentials.
func (e *serveEnv) open(p *sendParty, creds string) *endpoint.Endpoint {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), sendMailWait)
	defer cancel()
	cfg := config.Config{
		URL: e.srv.ClientURL(), AgentToken: serveRange, Owner: sendOwner, Name: p.name, Creds: creds, StateDir: e.t.TempDir(),
		SettleMS: 50, BatchMsgs: 10, BatchBytes: 16384, Limits: store.DefaultLimits(),
	}
	ep, err := endpoint.OpenClient(ctx, cfg)
	if err != nil {
		e.t.Fatalf("open %s: %v", p.id, err)
	}
	e.t.Cleanup(func() { ep.Close() })
	return ep
}

// inboxHolds reports whether the receiver's inbox holds a message with the text.
func inboxHolds(rx *endpoint.Endpoint, text string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), sendMailWait)
	defer cancel()
	ms, err := rx.Inbox(ctx, sendInboxLimit)
	if err != nil {
		return false, err
	}
	for _, m := range ms {
		if strings.Contains(string(m.Body), text) {
			return true, nil
		}
	}
	return false, nil
}

// waitMail waits until the receiver's inbox holds a message with the text.
func waitMail(t *testing.T, rx *endpoint.Endpoint, text string) {
	t.Helper()
	deadline := time.Now().Add(sendMailWait)
	for time.Now().Before(deadline) {
		if ok, err := inboxHolds(rx, text); err == nil && ok {
			return
		}
		time.Sleep(servePoll)
	}
	t.Fatalf("the inbox never held %q", text)
}

// send mails text from tx to the address.
func send(tx *endpoint.Endpoint, to, text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), sendMailWait)
	defer cancel()
	_, err := tx.Send(ctx, to, text, 0)
	return err
}

// After `policy serve` applies a list set, real endpoint clients under credentials issued for that list set are bound by
// it: a permitted mail is stored in the receiver's inbox, a forbidden one is refused by the server and the sender gets
// mash's refusal; a later generation that adds an allow makes the added send work.
func TestPolicyServeAppliedListSetGovernsRealSends(t *testing.T) {
	e := newServeEnvNoService(t, false)
	adm := e.adminOnly()
	core, lead, sdk := e.provisionParty(adm, "core"), e.provisionParty(adm, "lead"), e.provisionParty(adm, "sdk")
	e.adoptLater() // the endpoints exist first: the service finds each address's inbox and provisions nothing
	e.start()

	doc1 := signed(t, e.sub, e.serveDoc(1, 0, nil)) // core may mail lead and nobody else
	e.put(doc1)
	st := e.waitStatus("generation 1 applied", func(s *policyapply.Status) bool { return s.Generation == 1 && s.State == policyapply.StateApplied })
	if !st.InForce {
		t.Fatalf("status %+v", st)
	}
	coreTx := e.open(core, e.credsFor(core, doc1))
	leadRx := e.open(lead, e.credsFor(lead, doc1))
	sdkRx := e.open(sdk, e.credsFor(sdk, doc1))

	// Permitted: stored in the receiver's inbox.
	if err := send(coreTx, lead.id.String(), "permitted one"); err != nil {
		t.Fatalf("a permitted send: %v", err)
	}
	waitMail(t, leadRx, "permitted one")

	// Forbidden: the server refuses the publish and the sender gets mash's refusal. Nothing is stored.
	err := send(coreTx, sdk.id.String(), "forbidden one")
	var re *endpoint.RefusedError
	if !errors.As(err, &re) || !strings.HasPrefix(err.Error(), "refused by policy: you may not mail ") {
		t.Fatalf("the forbidden send: %v", err)
	}
	if held, err := inboxHolds(sdkRx, "forbidden one"); err != nil || held {
		t.Fatalf("the refused mail is in the inbox (%v) or the inbox could not be read (%v)", held, err)
	}

	// Generation 2 only adds the allow: the group's template does not change and nothing is narrowed, so no witness
	// is needed and the change applies. Core's credential carries the added tag.
	doc2 := signed(t, e.sub, e.serveDoc(2, 1, func(m map[string]any) {
		m["senders"].(map[string]any)["foo.c.core"] = map[string]any{"allow": []string{"foo.c.lead", "foo.c.sdk"}, "deny": []string{}}
	}))
	e.put(doc2)
	e.waitStatus("generation 2 applied", func(s *policyapply.Status) bool { return s.Generation == 2 && s.State == policyapply.StateApplied })
	coreTx2 := e.open(core, e.credsFor(core, doc2))
	if err := send(coreTx2, sdk.id.String(), "now allowed"); err != nil {
		t.Fatalf("the newly allowed send: %v", err)
	}
	waitMail(t, sdkRx, "now allowed")
	if err := send(coreTx2, lead.id.String(), "still allowed"); err != nil {
		t.Fatalf("the kept send: %v", err)
	}
	waitMail(t, leadRx, "still allowed")
	e.noSeeds(e.logs.String())
}
