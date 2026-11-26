package endpoint

// A record replaced through learn drops its old key from the reverse index, so the old key no longer names the endpoint
// in any of the readers of byUser: presence answers, stored mail, and live prompts.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

const lrName = "acme_team1_bob"

func lrRecord(user string) store.Record {
	return store.Record{Owner: "acme", Group: "team1", Instance: "bob", Agent: sap.AgentName, Account: "acme", User: user}
}

func TestLearnReplacedRecordDropsTheOldKey(t *testing.T) {
	e := keyEndpoint(nil)
	e.learn(map[string]recordResult{lrName: {rec: lrRecord("K1")}})
	e.learn(map[string]recordResult{lrName: {rec: lrRecord("K2")}})
	if n, ok := e.byUser["K1"]; ok {
		t.Errorf("byUser still maps the replaced key K1 to %q", n)
	}
	if e.byUser["K2"] != lrName {
		t.Errorf("byUser[K2] = %q, want %s", e.byUser["K2"], lrName)
	}
	// another record that now holds K1 is not disturbed by a later replace of the first
	e.learn(map[string]recordResult{"acme_team1_carol": {rec: store.Record{Owner: "acme", Group: "team1", Instance: "carol", User: "K1"}}})
	e.learn(map[string]recordResult{lrName: {rec: lrRecord("K3")}})
	if e.byUser["K1"] != "acme_team1_carol" {
		t.Errorf("byUser[K1] = %q, want acme_team1_carol", e.byUser["K1"])
	}
}

// Presence: an $SRV.INFO answer signed by the replaced key is not counted.
// presenceFor is a presence answer for lrName signed by id.
func presenceFor(t *testing.T, id sap.Identity) *nats.Msg {
	t.Helper()
	subject := "agents.prompt.acme.team1.bob"
	sig, err := id.IDSig(sap.AgentName, "acme", subject)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{
		"metadata": map[string]string{"owner": "acme", "agent": sap.AgentName, "account": id.Account,
			"user_nkey": id.User, "id_sig": sig},
		"endpoints": []map[string]string{{"name": "prompt", "subject": subject}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &nats.Msg{Data: data}
}

func lrOnline(e *Endpoint) bool {
	for _, p := range e.Who() {
		if flatOf(p.Record) == lrName {
			return p.Online
		}
	}
	return false
}

// markedOnlineByK1 is a client that read bob's record with K1 and counted K1's presence answer.
func markedOnlineByK1(t *testing.T) (e *Endpoint, k1, k2 sap.Identity) {
	t.Helper()
	k1, k2 = newFormIdentity(t), newFormIdentity(t)
	e = keyEndpoint(nil)
	e.learn(map[string]recordResult{lrName: {rec: lrRecord(k1.User)}})
	if got := e.applyPresence(presenceFor(t, k1)); got != 1 {
		t.Fatalf("the record's own key counted %d, want 1", got)
	}
	if !lrOnline(e) {
		t.Fatal("bob is not online after K1's own answer")
	}
	return e, k1, k2
}

// Presence: an answer the replaced key signed counts nothing, and the time it set does not outlive the key.
func TestPresenceReplacedKeyIsNotCounted(t *testing.T) {
	e, k1, k2 := markedOnlineByK1(t)
	e.learn(map[string]recordResult{lrName: {rec: lrRecord(k2.User)}})
	if lrOnline(e) {
		t.Error("bob is online on the new key from an answer the old key signed (learn)")
	}
	if got := e.applyPresence(presenceFor(t, k1)); got != 0 {
		t.Errorf("the replaced key counted %d, want 0", got)
	}
	if lrOnline(e) {
		t.Error("bob is online after another answer signed by the replaced key")
	}
	if got := e.applyPresence(presenceFor(t, k2)); got != 1 || !lrOnline(e) {
		t.Errorf("the new key counted %d, online %v, want 1 and online", got, lrOnline(e))
	}
}

// The full-directory replacement (installDirectory) clears the last-seen time of a name whose key changed.
func TestInstallDirectoryReplacedKeyClearsLastSeen(t *testing.T) {
	e, _, k2 := markedOnlineByK1(t)
	e.installDirectory(map[string]recordResult{lrName: {rec: lrRecord(k2.User)}})
	if lrOnline(e) {
		t.Error("bob is online on the new key from an answer the old key signed (installDirectory)")
	}
	// The same key reinstalled keeps its time.
	if got := e.applyPresence(presenceFor(t, k2)); got != 1 {
		t.Fatalf("new key counted %d, want 1", got)
	}
	e.installDirectory(map[string]recordResult{lrName: {rec: lrRecord(k2.User)}})
	if !lrOnline(e) {
		t.Error("an unchanged key lost its last-seen time")
	}
}

// The sender reload (reloadSender, the watch path) clears the last-seen time of a name whose key changed.
// NEEDS A NATS SERVER (testenv.Start, in process).
func TestReloadSenderReplacedKeyClearsLastSeen(t *testing.T) {
	f := newFixture(t, "a", "b")
	a := f.client(t, "a")
	cur := f.record(t, tkey("b"))
	old := cur
	old.User = newFormIdentity(t).User
	a.learn(map[string]recordResult{tkey("b"): {rec: old}})
	a.mu.Lock()
	a.lastSeen[tkey("b")] = time.Now()
	a.mu.Unlock()
	if !a.reloadSender(sap.SenderHeader{V: 1, User: cur.User, Name: tkey("b")}) {
		t.Fatal("the reload did not install the new record")
	}
	a.mu.RLock()
	_, seen := a.lastSeen[tkey("b")]
	a.mu.RUnlock()
	if seen {
		t.Error("lastSeen survived a key replacement through reloadSender")
	}
}

// Stored mail: after the record's key was replaced, mail signed by the old key does not name the endpoint.
func TestClassifyReplacedKeyDoesNotNameTheEndpoint(t *testing.T) {
	h := func(string) error { return errors.New("no such record") } // the one-time reload of the sender finds nothing
	fetchRecordHook.Store(&h)
	t.Cleanup(func() { fetchRecordHook.Store(nil) })
	k1, k2 := newFormIdentity(t), newFormIdentity(t)
	e := keyEndpoint(nil)
	e.learn(map[string]recordResult{lrName: {rec: lrRecord(k1.User)}})
	hdr, subj, data := signedHeader(t, k1, lrName)
	if from, trust, _, _ := e.classify(subj, hdr.Values(sap.HeaderName), data, time.Now()); trust != sap.TrustVerified || from != lrName {
		t.Fatalf("before the replace: from %q trust %v, want %s verified", from, trust, lrName)
	}
	e.learn(map[string]recordResult{lrName: {rec: lrRecord(k2.User)}})
	_, trust, _, ok := e.classify(subj, hdr.Values(sap.HeaderName), data, time.Now())
	if !ok || trust != sap.TrustUnknownSigner {
		t.Errorf("after the replace: ok %v trust %v, want unknown-signer", ok, trust)
	}
}

// Live prompt: after the asker's record was replaced in the target's directory, a prompt signed by the old key is
// refused 403 and never reaches the session.
// NEEDS A NATS SERVER (testenv.Start, in process).
func TestPromptReplacedKeyIsRefused(t *testing.T) {
	f := newFixture(t, "a", "b")
	a := f.serve(t, "a", func(*Endpoint, PromptEvent) { t.Error("a prompt signed by a replaced key reached the session") })
	b := f.client(t, "b")
	rec := f.record(t, tkey("b"))
	a.learn(map[string]recordResult{tkey("b"): {rec: rec}})
	rec.User = newFormIdentity(t).User // b's key was reissued; a read the new record
	a.learn(map[string]recordResult{tkey("b"): {rec: rec}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	err := promptSend(ctx, b.Conn(), b.Identity(), tkey("b"), mustSubj(taddr("a")).Prompt, "hello", &out)
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("prompt signed by the replaced key: err %v, want a 403 refusal", err)
	}
}
