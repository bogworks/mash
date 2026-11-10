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
	ok := []string{"ops1", "mash-a", "acme_team1_ops1", "codex_o_n", "a-b_c-d_e-f"}
	bad := []string{"", "a_b", "cc_o_n", "Acme_team1_ops1", "acme_Team1_ops1", "a_b_c_d", "acme_team1_",
		"_a_b", "acme_team1_" + strings.Repeat("n", 33), strings.Repeat("a", 33) + "_o_n", "acme_1team_ops", "OPS1"}
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
	subj, data := "agents.mail.cc.acme.me", []byte("hi")
	h, err := id.Sign(subj, data, name, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	return nats.Header{sap.HeaderName: []string{h.Encode()}}, subj, data
}

func TestClassifyNewFormKeys(t *testing.T) {
	const key = "acme_team1_ops1"
	nf, lg := newFormIdentity(t), newFormIdentity(t)
	e := keyEndpoint(map[string]store.Record{
		key:    {Name: "ops1", User: nf.User},
		"ops1": {Name: "ops1", User: lg.User},
	})
	cases := []struct {
		desc  string
		id    sap.Identity
		name  string
		trust sap.Trust
		from  string
	}{
		{"new-form signs its key", nf, key, sap.TrustVerified, key},
		{"new-form signs the bare name token", nf, "ops1", sap.TrustUnknownSigner, "ops1"},
		{"legacy signs its own name", lg, "ops1", sap.TrustVerified, "ops1"},
		{"legacy signs the new-form key", lg, key, sap.TrustUnknownSigner, ""},
	}
	for _, c := range cases {
		hdr, subj, data := signedHeader(t, c.id, c.name)
		from, trust, _, ok := e.classify(subj, hdr, data, time.Now())
		if !ok || trust != c.trust || (c.from != "" && from != c.from) || (c.trust == sap.TrustUnknownSigner && from == key) {
			t.Errorf("%s: got (%q,%s,%v) want (%q,%s)", c.desc, from, trust, ok, c.from, c.trust)
		}
	}
	// Only the new-form record: the bare name token alone is nobody.
	e = keyEndpoint(map[string]store.Record{key: {Name: "ops1", User: nf.User}})
	hdr, subj, data := signedHeader(t, nf, "ops1")
	if _, trust, _, _ := e.classify(subj, hdr, data, time.Now()); trust != sap.TrustUnknownSigner {
		t.Errorf("name token alone: %s", trust)
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

func TestApplyPresenceNewFormKey(t *testing.T) {
	const key = "acme_team1_ops1"
	nf, lg := newFormIdentity(t), newFormIdentity(t)
	e := keyEndpoint(map[string]store.Record{
		key:    {Name: "ops1", User: nf.User},
		"ops1": {Name: "ops1", User: lg.User},
	})
	// An answer for the new-form subject marks only the new-form record.
	if n := e.applyPresence(presenceMsg(t, nf, "acme", "agents.prompt.acme.team1.ops1")); n != 1 {
		t.Fatalf("new-form answer counted %d", n)
	}
	if e.lastSeen[key].IsZero() {
		t.Fatal("new-form record not seen")
	}
	if !e.lastSeen["ops1"].IsZero() {
		t.Fatal("legacy record with the same name token marked online")
	}
	// A new-form key's user answering for the legacy subject is not the legacy record's user.
	e.lastSeen = map[string]time.Time{}
	if n := e.applyPresence(presenceMsg(t, nf, "cc", "agents.prompt.cc.acme.ops1")); n != 0 {
		t.Fatalf("new-form user on the legacy subject counted %d", n)
	}
	// The legacy answer is unchanged and does not mark the new-form record.
	if n := e.applyPresence(presenceMsg(t, lg, "cc", "agents.prompt.cc.acme.ops1")); n != 1 {
		t.Fatalf("legacy answer counted %d", n)
	}
	if e.lastSeen["ops1"].IsZero() || !e.lastSeen[key].IsZero() {
		t.Fatalf("legacy presence: %v", e.lastSeen)
	}
}
