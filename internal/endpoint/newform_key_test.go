package endpoint

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

func TestValidDirectoryKey(t *testing.T) {
	ok := []string{"acme_team1_ops1", "codex_o_n", "a-b_c-d_e-f"}
	bad := []string{"", "ops1", "mash-a", "a_b", "a_b_c_d", "Acme_team1_ops1", "acme_Team1_ops1", "acme_team1_",
		"_a_b", "acme_team1_" + strings.Repeat("n", 33), strings.Repeat("a", 33) + "_o_n", "acme_1team_ops", "OPS1",
		"acme.team1.ops1"}
	for _, k := range ok {
		if !validDirectoryKey(k) {
			t.Errorf("%q rejected", k)
		}
	}
	for _, k := range bad {
		if validDirectoryKey(k) {
			t.Errorf("%q accepted", k)
		}
	}
}

func newFormIdentity(t *testing.T) sap.Identity {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := kp.Seed()
	id, err := sap.NewIdentity(seed, "acme")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// keyEndpoint is an Endpoint literal holding the given directory records, keyed as the directory is.
func keyEndpoint(recs map[string]store.Record) *Endpoint {
	e := &Endpoint{cfg: config.Config{Owner: "acme"}, dir: map[string]store.Record{}, byUser: map[string]string{},
		lastSeen: map[string]time.Time{}}
	for k, r := range recs {
		e.dir[k] = r
		e.byUser[r.User] = k
	}
	return e
}

func signedHeader(t *testing.T, id sap.Identity, name string) (nats.Header, string, []byte) {
	t.Helper()
	subj, data := "agents.mail.acme.team1.me", []byte("hi")
	h, err := id.Sign(subj, data, name, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	return nats.Header{sap.HeaderName: []string{h.Encode()}}, subj, data
}

func TestClassifyKeys(t *testing.T) {
	const key, other = "acme_team1_ops1", "acme_team1_ops2"
	a, b := newFormIdentity(t), newFormIdentity(t)
	e := keyEndpoint(map[string]store.Record{
		key:   {Owner: "acme", Group: "team1", Instance: "ops1", User: a.User},
		other: {Owner: "acme", Group: "team1", Instance: "ops2", User: b.User},
	})
	cases := []struct {
		desc  string
		id    sap.Identity
		name  string
		trust sap.Trust
		from  string
	}{
		{"signs its own key", a, key, sap.TrustVerified, key},
		{"signs the bare instance token", a, "ops1", sap.TrustUnknownSigner, ""},
		{"signs another endpoint's key", a, other, sap.TrustUnknownSigner, ""},
		{"the other endpoint signs its key", b, other, sap.TrustVerified, other},
	}
	for _, c := range cases {
		hdr, subj, data := signedHeader(t, c.id, c.name)
		from, trust, _, ok := e.classify(subj, hdr, data, time.Now())
		if !ok || trust != c.trust || (c.from != "" && from != c.from) || (c.trust == sap.TrustUnknownSigner && (from == key || from == other)) {
			t.Errorf("%s: got (%q,%s,%v) want (%q,%s)", c.desc, from, trust, ok, c.from, c.trust)
		}
	}
}

func presenceMsg(t *testing.T, id sap.Identity, agent, subject string) *nats.Msg {
	t.Helper()
	sig, err := id.IDSig(agent, "acme", subject)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{
		"metadata":  map[string]string{"owner": "acme", "agent": agent, "account": id.Account, "user_nkey": id.User, "id_sig": sig},
		"endpoints": []map[string]string{{"name": "prompt", "subject": subject}},
	})
	return &nats.Msg{Data: b}
}

func TestApplyPresenceKey(t *testing.T) {
	const key, other = "acme_team1_ops1", "acme_team1_ops2"
	a, b := newFormIdentity(t), newFormIdentity(t)
	e := keyEndpoint(map[string]store.Record{
		key:   {Owner: "acme", Group: "team1", Instance: "ops1", User: a.User},
		other: {Owner: "acme", Group: "team1", Instance: "ops2", User: b.User},
	})
	// An answer for a subject marks only that endpoint's record.
	if n := e.applyPresence(presenceMsg(t, a, sap.AgentName, "agents.prompt.acme.team1.ops1")); n != 1 {
		t.Fatalf("answer counted %d", n)
	}
	if e.lastSeen[key].IsZero() || !e.lastSeen[other].IsZero() {
		t.Fatalf("presence: %v", e.lastSeen)
	}
	// One endpoint's user answering for another's subject is not that record's user.
	e.lastSeen = map[string]time.Time{}
	if n := e.applyPresence(presenceMsg(t, a, sap.AgentName, "agents.prompt.acme.team1.ops2")); n != 0 {
		t.Fatalf("a user on another endpoint's subject counted %d", n)
	}
	// A subject that is not of the address shape counts for nothing.
	if n := e.applyPresence(presenceMsg(t, a, sap.AgentName, "agents.prompt.cc.acme.ops1")); n != 0 {
		t.Fatalf("a subject of the removed form counted %d", n)
	}
}

// A verified registration counts whatever its metadata owner says: the address is the subject's, and the id_sig
// and the directory key still decide.
func TestApplyPresenceIgnoresMetadataOwner(t *testing.T) {
	const key = "acme_team1_ops1"
	a, b := newFormIdentity(t), newFormIdentity(t)
	e := keyEndpoint(map[string]store.Record{key: {Owner: "acme", Group: "team1", Instance: "ops1", User: a.User}})
	subj := "agents.prompt.acme.team1.ops1"
	with := func(id sap.Identity, owner, signed, subject string) *nats.Msg {
		sig, err := id.IDSig(sap.AgentName, owner, signed)
		if err != nil {
			t.Fatal(err)
		}
		d, _ := json.Marshal(map[string]any{
			"metadata":  map[string]string{"owner": owner, "agent": sap.AgentName, "account": id.Account, "user_nkey": id.User, "id_sig": sig},
			"endpoints": []map[string]string{{"name": "prompt", "subject": subject}},
		})
		return &nats.Msg{Data: d}
	}
	if n := e.applyPresence(with(a, "someone-else", subj, subj)); n != 1 {
		t.Fatalf("a verified registration with another metadata owner counted %d", n)
	}
	e.lastSeen = map[string]time.Time{}
	if n := e.applyPresence(with(a, "acme", "agents.prompt.acme.team1.zzz", subj)); n != 0 {
		t.Fatalf("a bad id_sig counted %d", n)
	}
	if n := e.applyPresence(with(b, "acme", subj, subj)); n != 0 {
		t.Fatalf("another key on the subject counted %d", n)
	}
	if n := e.applyPresence(with(a, "acme", "agents.prompt.acme.team1.ops2", "agents.prompt.acme.team1.ops2")); n != 0 || !e.lastSeen[key].IsZero() {
		t.Fatalf("a subject that is not the directory key's counted %d", n)
	}
}

// A second credential answering for X's subject does not make X present: the directory key for the address is not
// its user, with or without an id_sig of its own, and an answer with no id_sig is not counted. Who then shows X
// offline with a zero LastSeen.
func TestApplyPresenceRefusesOtherCredential(t *testing.T) {
	const key = "acme_team1_ops1"
	a, b := newFormIdentity(t), newFormIdentity(t)
	e := keyEndpoint(map[string]store.Record{key: {Owner: "acme", Group: "team1", Instance: "ops1", User: a.User}})
	subj := "agents.prompt.acme.team1.ops1"
	answer := func(id sap.Identity, sign bool) *nats.Msg {
		md := map[string]string{"owner": "acme", "agent": sap.AgentName, "account": id.Account, "user_nkey": id.User}
		if sign {
			sig, err := id.IDSig(sap.AgentName, "acme", subj)
			if err != nil {
				t.Fatal(err)
			}
			md["id_sig"] = sig
		}
		d, _ := json.Marshal(map[string]any{"metadata": md, "endpoints": []map[string]string{{"name": "prompt", "subject": subj}}})
		return &nats.Msg{Data: d}
	}
	for _, c := range []struct {
		desc string
		id   sap.Identity
		sign bool
	}{
		{"other credential, valid id_sig of its own", b, true},
		{"other credential, no id_sig", b, false},
		{"the directory's own key, no id_sig", a, false},
	} {
		if n := e.applyPresence(answer(c.id, c.sign)); n != 0 || !e.lastSeen[key].IsZero() {
			t.Errorf("%s: counted %d, lastSeen %v", c.desc, n, e.lastSeen[key])
		}
	}
	for _, p := range e.Who() {
		t.Logf("Who: %s online=%v lastSeen=%v", flatOf(p.Record), p.Online, p.LastSeen)
		if p.Online || !p.LastSeen.IsZero() {
			t.Errorf("%s shows present: %+v", flatOf(p.Record), p)
		}
	}
}
