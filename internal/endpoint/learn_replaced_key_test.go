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
func TestPresenceReplacedKeyIsNotCounted(t *testing.T) {
	k1, k2 := newFormIdentity(t), newFormIdentity(t)
	subject := "agents.prompt.acme.team1.bob"
	sig, err := k1.IDSig(sap.AgentName, "acme", subject)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{
		"metadata": map[string]string{"owner": "acme", "agent": sap.AgentName, "account": k1.Account,
			"user_nkey": k1.User, "id_sig": sig},
		"endpoints": []map[string]string{{"name": "prompt", "subject": subject}},
	})
	if err != nil {
		t.Fatal(err)
	}
	e := keyEndpoint(nil)
	e.learn(map[string]recordResult{lrName: {rec: lrRecord(k1.User)}})
	if got := e.applyPresence(&nats.Msg{Data: data}); got != 1 {
		t.Fatalf("the record's own key counted %d, want 1", got)
	}
	delete(e.lastSeen, lrName)
	e.learn(map[string]recordResult{lrName: {rec: lrRecord(k2.User)}})
	if got := e.applyPresence(&nats.Msg{Data: data}); got != 0 {
		t.Errorf("the replaced key counted %d, want 0", got)
	}
	if _, seen := e.lastSeen[lrName]; seen {
		t.Error("lastSeen holds bob after an answer signed by the replaced key")
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
	if from, trust, _, _ := e.classify(subj, hdr, data, time.Now()); trust != sap.TrustVerified || from != lrName {
		t.Fatalf("before the replace: from %q trust %v, want %s verified", from, trust, lrName)
	}
	e.learn(map[string]recordResult{lrName: {rec: lrRecord(k2.User)}})
	_, trust, _, ok := e.classify(subj, hdr, data, time.Now())
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
