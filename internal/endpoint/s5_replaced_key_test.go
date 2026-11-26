package endpoint

// A key that a record has since replaced no longer verifies: the S5 verify step compares the registration's user_nkey
// with the record's own User field, not the reverse index.

import (
	"encoding/json"
	"io"
	"log"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

const rkAccount = "ACCT"

func rkIdentity(t *testing.T) sap.Identity {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := kp.Seed()
	id, err := sap.NewIdentity(seed, rkAccount)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func rkEndpoint() *Endpoint {
	return &Endpoint{cfg: config.Config{Owner: "acme"}, log: log.New(io.Discard, "", 0),
		dir: map[string]store.Record{}, byUser: map[string]string{}, lastSeen: map[string]time.Time{}}
}

func rkRecord(user string) store.Record {
	return store.Record{Owner: "acme", Group: "ops", Instance: "bob", Agent: sap.AgentName, Account: rkAccount, User: user}
}

// rkAnswer is a $SRV.INFO answer for acme_ops_bob signed by id.
func rkAnswer(t *testing.T, id sap.Identity) (data []byte, subject string) {
	t.Helper()
	subject = "agents.prompt.acme.ops.bob"
	sig, err := id.IDSig(sap.AgentName, "acme", subject)
	if err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(map[string]any{
		"metadata": map[string]string{"owner": "acme", "agent": sap.AgentName, "account": id.Account,
			"user_nkey": id.User, "id_sig": sig},
		"endpoints": []map[string]string{{"name": "prompt", "subject": subject}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return data, subject
}

// With the S5 hook on, an answer signed by the key the record replaced is not verified, and is counted.
func TestS5ReplacedKeyDoesNotVerify(t *testing.T) {
	setS5Hook(t, s5On())
	h := s5Hook()
	k1, k2 := rkIdentity(t), rkIdentity(t)
	data, subject := rkAnswer(t, k1)
	e := rkEndpoint()
	e.learn(map[string]recordResult{"acme_ops_bob": {rec: rkRecord(k1.User)}})
	if !e.judgeAnswerS5(h, data, subject) {
		t.Fatal("the answer signed by the record's own key did not verify")
	}
	if n := e.AnswersNotVerified(); n != 0 {
		t.Fatalf("counted %d before the key was replaced", n)
	}
	e.learn(map[string]recordResult{"acme_ops_bob": {rec: rkRecord(k2.User)}})
	if e.judgeAnswerS5(h, data, subject) {
		t.Error("prompt discovery: an answer signed by the replaced key verified")
	}
	if n := e.AnswersNotVerified(); n != 1 {
		t.Errorf("prompt discovery counted %d, want 1", n)
	}
	if got := e.applyPresenceS5(h, &nats.Msg{Data: data}); got != 0 {
		t.Errorf("presence: the replaced key was recorded as seen (%d)", got)
	}
	if _, seen := e.lastSeen["acme_ops_bob"]; seen {
		t.Error("presence: lastSeen holds bob after an answer signed by the replaced key")
	}
	if n := e.AnswersNotVerified(); n != 2 {
		t.Errorf("after presence the count is %d, want 2", n)
	}
}

// The verify step runs outside e.mu and the record is read again before lastSeen is set: a record replaced while the
// step runs is not marked seen, and the registration counts as not verified. A verify step that ran under the lock
// would deadlock on the replace below.
func TestS5PresenceRechecksTheRecordAfterVerify(t *testing.T) {
	k1, k2 := rkIdentity(t), rkIdentity(t)
	data, _ := rkAnswer(t, k1)
	e := rkEndpoint()
	e.learn(map[string]recordResult{"acme_ops_bob": {rec: rkRecord(k1.User)}})
	base := s5On()
	h := &s5Hooks{secretPresence: true, promptDirectory: true,
		verify: func(reg registration, d *dirView) (string, bool) {
			name, ok := base.verify(reg, d)
			e.learn(map[string]recordResult{"acme_ops_bob": {rec: rkRecord(k2.User)}}) // replaced mid-verify
			return name, ok
		}}
	done := make(chan int, 1)
	go func() { done <- e.applyPresenceS5(h, &nats.Msg{Data: data}) }()
	select {
	case got := <-done:
		if got != 0 {
			t.Errorf("a record replaced during verify was recorded as seen (%d)", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("applyPresenceS5 held e.mu while the verify step ran")
	}
	if _, seen := e.lastSeen["acme_ops_bob"]; seen {
		t.Error("lastSeen holds bob after the record was replaced during verify")
	}
	if n := e.AnswersNotVerified(); n != 1 {
		t.Errorf("counted %d, want 1", n)
	}
}

// rkRechecksField replaces bob's record mid-verify with one that has the same user key and alter applied, and expects
// the presence to be refused.
func rkRechecksField(t *testing.T, alter func(*store.Record)) {
	t.Helper()
	k1 := rkIdentity(t)
	data, _ := rkAnswer(t, k1)
	e := rkEndpoint()
	e.learn(map[string]recordResult{"acme_ops_bob": {rec: rkRecord(k1.User)}})
	base := s5On()
	h := &s5Hooks{secretPresence: true, promptDirectory: true,
		verify: func(reg registration, d *dirView) (string, bool) {
			name, ok := base.verify(reg, d)
			changed := rkRecord(k1.User)
			alter(&changed)
			e.learn(map[string]recordResult{"acme_ops_bob": {rec: changed}}) // same key, one field changed mid-verify
			return name, ok
		}}
	if got := e.applyPresenceS5(h, &nats.Msg{Data: data}); got != 0 {
		t.Errorf("a record changed during verify was recorded as seen (%d)", got)
	}
	if _, seen := e.lastSeen["acme_ops_bob"]; seen {
		t.Error("lastSeen holds bob after the record changed during verify")
	}
	if n := e.AnswersNotVerified(); n != 1 {
		t.Errorf("counted %d, want 1", n)
	}
}

func TestS5PresenceRechecksOwnerAfterVerify(t *testing.T) {
	rkRechecksField(t, func(r *store.Record) { r.Owner = "mallory" })
}

func TestS5PresenceRechecksAccountAfterVerify(t *testing.T) {
	rkRechecksField(t, func(r *store.Record) { r.Account = rkAccount + "X" })
}
