package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/store"
)

// issued reads the issue record through the admin connection, by public key.
func (h *addEnv) issued() map[string]store.Issued {
	h.t.Helper()
	adm, err := admin.New(h.e.Conn(h.t, h.e.AdminCreds), h.e.Owner)
	if err != nil {
		h.t.Fatal(err)
	}
	recs, _, err := adm.Issued(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	out := map[string]store.Issued{}
	for _, r := range recs {
		out[r.PublicKey] = r
	}
	return out
}

func (h *addEnv) issuedHistory(pub string) int {
	h.t.Helper()
	js, err := jetstream.New(h.e.Conn(h.t, h.e.AdminCreds))
	if err != nil {
		h.t.Fatal(err)
	}
	kv, err := js.KeyValue(context.Background(), store.IssuedBucket)
	if err != nil {
		h.t.Fatal(err)
	}
	hist, err := kv.History(context.Background(), pub)
	if err != nil {
		h.t.Fatal(err)
	}
	return len(hist)
}

func credsSubject(t *testing.T, path string) (*jwt.UserClaims, string) {
	t.Helper()
	pub, err := issuedCredsKey(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := readCreds(path)
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := jwt.ParseDecoratedJWT(raw)
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		t.Fatal(err)
	}
	return uc, pub
}

// `mashctl add` records the user it issues, with every field, before it hands the creds out; init records the admin.
func TestAddRecordsTheIssuedUser(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	if out, errb, code := h.run("add", "g", "--zone", "green", "--tool"); code != 0 {
		t.Fatalf("%d %s %s", code, out, errb)
	}
	uc, pub := h.credsClaims("g")
	rec, ok := h.issued()[pub]
	if !ok {
		t.Fatalf("no record for %s: %+v", pub, h.issued())
	}
	if rec.Address != "g" || rec.Owner != h.e.Owner || rec.Zone != "green" || rec.Kind != store.IssuedEndpoint || rec.Generation != 0 {
		t.Errorf("record %+v", rec)
	}
	if !slices.Equal(rec.Tags, []string(uc.Tags)) || len(rec.Tags) == 0 {
		t.Errorf("tags %v, JWT %v", rec.Tags, uc.Tags)
	}
	at, err := time.Parse(time.RFC3339, rec.IssuedAt)
	if err != nil || !strings.HasSuffix(rec.IssuedAt, "Z") || at.Unix() != uc.IssuedAt {
		t.Errorf("issued_at %q (%v), JWT iat %d", rec.IssuedAt, err, uc.IssuedAt)
	}
	// The admin user is in it too, marked admin, so a check does not call mash's own user foreign.
	_, adminPub := credsSubject(t, h.e.AdminCreds)
	if a, ok := h.issued()[adminPub]; !ok || a.Kind != store.IssuedAdmin || a.Address != "admin" {
		t.Errorf("admin record %+v %v", a, ok)
	}
	// Nothing secret in what the record holds.
	b, _ := json.Marshal(h.issued())
	noSecrets(t, string(b))
}

// A record that cannot be written stops the add: no creds file, no directory record.
func TestAddIssuesNothingWhenTheRecordCannotBeWritten(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	ctx := context.Background()
	js, err := jetstream.New(h.e.Conn(t, h.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	if err := js.DeleteStream(ctx, store.IssuedStreamName); err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateKeyValue(ctx, store.IssuedConfig()); err != nil { // allows rollup: not hardened
		t.Fatal(err)
	}
	out, errb, code := h.run("add", "g", "--zone", "green")
	if code != 1 || !strings.Contains(errb, "nothing was issued") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if _, err := readCreds(h.creds("g")); err == nil {
		t.Error("a creds file was written")
	}
	if recs := h.records(); len(recs) != 0 {
		t.Errorf("directory records %v", recs)
	}
}

// A reissue that keeps the key adds a revision under the same key; a reissue that rotates records the new key and
// keeps the old key's record.
func TestReissueRecordsAndNeverRemoves(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	fakeRevoke(t, nil)
	fakeSignerPublished(t, true, nil)
	if _, errb, code := h.run("add", "n", "--zone", "green"); code != 0 {
		t.Fatal(errb)
	}
	_, pub1 := h.credsClaims("n")
	if h.issuedHistory(pub1) != 1 {
		t.Fatal("add did not record once")
	}
	if out, errb, code := h.run("reissue", "n"); code != 0 {
		t.Fatalf("%d %s %s", code, out, errb)
	}
	if _, p := h.credsClaims("n"); p != pub1 {
		t.Fatal("a keeping reissue changed the key")
	}
	if got := h.issuedHistory(pub1); got != 2 {
		t.Fatalf("history %d, want 2 revisions of the same key", got)
	}

	fakeKeyRevoked(t, true, nil) // the account JWT revokes the key: the reissue rotates
	if out, errb, code := h.run("reissue", "n"); code != 0 {
		t.Fatalf("%d %s %s", code, out, errb)
	}
	_, pub2 := h.credsClaims("n")
	if pub2 == pub1 {
		t.Fatal("no rotation")
	}
	recs := h.issued()
	if recs[pub2].Address != "n" || recs[pub2].Kind != store.IssuedEndpoint {
		t.Errorf("the new key is not recorded: %+v", recs[pub2])
	}
	if recs[pub1].Address != "n" {
		t.Errorf("the old key's record was removed: %+v", recs[pub1])
	}
}

// --- the check, against a real operator-mode server --------------------------------------------------------------

func (e *applyEnv) connect(opts ...nats.Option) *nats.Conn {
	e.t.Helper()
	nc, err := nats.Connect(e.srv.ClientURL(), opts...)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(nc.Close)
	return nc
}

func (e *applyEnv) issuedCheck(extra ...string) (issuedCheck, string, int) {
	e.t.Helper()
	out, errb, code := e.run(append([]string{"issued", "check", "--json"}, extra...)...)
	var r issuedCheck
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		e.t.Fatalf("issued check --json: %v\n%s\n%s", err, out, errb)
	}
	noSecrets(e.t, out, errb)
	e.noSeeds(out + errb)
	return r, errb, code
}

func connectedKeys(t *testing.T, e *applyEnv) []string {
	t.Helper()
	nc := e.connect(natsauth.UserCredentials(e.env["MASH_SYS_CREDS"]))
	conns, err := apply.NATS{Conn: nc}.Connz(context.Background(), e.acctPub, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, s := range conns {
		for _, c := range s.Conns {
			keys = append(keys, c.Key)
		}
	}
	return keys
}

// AT-017: users issued through mashctl and connected are clean; a user signed outside mashctl with a copy of a zone's
// signing key connects, and the check reports exactly its public key and exits 1.
func TestIssuedCheckReportsAKeyMashDidNotIssue(t *testing.T) {
	e := newDriftEnv(t)
	x := e.connect(natsauth.UserCredentials(e.credsPath("x")), nats.Name("x-client"))
	_ = x

	r, errb, code := e.issuedCheck()
	if code != 0 || !r.Clean || !r.Complete || len(r.Unrecorded) != 0 || r.Summary.Connected < 2 {
		t.Fatalf("clean state: %d %+v %s", code, r, errb) // x and mashctl's own admin connection
	}
	if r.Summary.Recorded < 3 { // x, y and the admin
		t.Errorf("recorded %d", r.Summary.Recorded)
	}
	var yKey string
	_, yKey = credsSubject(t, e.credsPath("y"))
	if !slices.ContainsFunc(r.NotConnected, func(n issuedNotConnected) bool { return n.PublicKey == yKey }) {
		t.Errorf("y is recorded and not connected, and should be listed: %+v", r.NotConnected)
	}

	// The rogue: signed by zone a's key, held outside mashctl.
	rogue := e.mint("a", "rogue")
	rc := e.connect(rogue, nats.Name("rogue-client"))
	if !rc.IsConnected() {
		t.Fatal("the rogue did not connect")
	}
	keys := connectedKeys(t, e)
	r, errb, code = e.issuedCheck()
	if code != 1 || r.Clean || len(r.Unrecorded) != 1 {
		t.Fatalf("rogue: %d %+v %s", code, r, errb)
	}
	f := r.Unrecorded[0]
	if !nkeys.IsValidPublicUserKey(f.PublicKey) || !slices.Contains(keys, f.PublicKey) || f.Name != "rogue-client" || f.Account != e.acctPub || f.Server == "" {
		t.Errorf("finding %+v (connected %v)", f, keys)
	}
	zk, _ := e.ks.ZoneKey("a", false)
	zpub, _ := zk.PublicKey()
	if f.IssuerKey != zpub {
		t.Errorf("issuer %q, want zone a's key %q", f.IssuerKey, zpub)
	}
	if !slices.Contains(f.Tags, "deny:_none") {
		t.Errorf("tags %v", f.Tags)
	}

	// The text form names the same key, and no more.
	out, errb, code := e.run("issued", "check")
	if code != 1 || !strings.Contains(out, "UNRECORDED key "+f.PublicKey) || !strings.Contains(out, `client name "rogue-client"`) || strings.Count(out, "UNRECORDED") != 1 {
		t.Fatalf("text: %d\n%s\n%s", code, out, errb)
	}
	noSecrets(t, out, errb)

	// Once it disconnects the check is clean again.
	rc.Close()
	time.Sleep(200 * time.Millisecond)
	if r, _, code = e.issuedCheck(); code != 0 || len(r.Unrecorded) != 0 {
		t.Fatalf("after the rogue left: %d %+v", code, r)
	}
}

// Fewer servers answering than the operator says there are is not a clean answer.
func TestIssuedCheckRefusesAnIncompleteServerSet(t *testing.T) {
	e := newDriftEnv(t)
	r, _, code := e.issuedCheck("--servers", "2")
	if code != 2 || r.Complete || r.Clean || !strings.Contains(r.Incomplete, "1 server(s) replied") {
		t.Fatalf("%d %+v", code, r)
	}
	out, _, code := e.run("issued", "check", "--servers", "2")
	if code != 2 || !strings.Contains(out, "INCOMPLETE") {
		t.Fatalf("%d %s", code, out)
	}
	e.env["MASH_SERVERS"] = "2"
	if _, _, code := e.issuedCheck(); code != 2 {
		t.Fatalf("MASH_SERVERS: %d", code)
	}
	e.env["MASH_SERVERS"] = "1"
	if r, _, code := e.issuedCheck(); code != 0 || !r.Complete {
		t.Fatalf("one server of one: %d %+v", code, r)
	}
	// a rogue is still reported when the set is incomplete: the finding is certain
	e.env["MASH_SERVERS"] = "2"
	e.connect(e.mint("a", "rogue"))
	if r, _, code := e.issuedCheck(); code != 1 || len(r.Unrecorded) != 1 || r.Complete {
		t.Fatalf("%d %+v", code, r)
	}
}

func TestIssuedCheckUsageAndErrors(t *testing.T) {
	e := newDriftEnv(t)
	for _, args := range [][]string{{"issued"}, {"issued", "bogus"}, {"issued", "check", "--servers", "0"}, {"issued", "check", "--servers"}, {"issued", "list", "--sys-creds", "x"}, {"issued", "check", "extra"}} {
		if _, _, code := e.run(args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	e.env["MASH_SYS_CREDS"] = "/nonexistent/sys.creds"
	if _, errb, code := e.run("issued", "check"); code != 2 || !strings.Contains(errb, "system account credentials") {
		t.Errorf("no sys creds: %d %q", code, errb)
	}
}

// The JSON of check and list keeps its shape.
func TestIssuedJSONShape(t *testing.T) {
	e := newDriftEnv(t)
	out, _, code := e.run("issued", "check", "--json")
	if code != 0 {
		t.Fatal(code)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	want := []string{"account", "clean", "complete", "known_mash_users", "record_present", "recorded_not_connected", "servers", "summary", "unrecorded"}
	if !slices.Equal(keys, want) {
		t.Errorf("keys %v, want %v", keys, want)
	}
	if u, ok := m["unrecorded"].([]any); !ok || len(u) != 0 {
		t.Errorf("unrecorded %v, want an empty list, not null", m["unrecorded"])
	}
	srv := m["servers"].(map[string]any)
	for _, k := range []string{"answered", "missing", "expected"} {
		if _, ok := srv[k]; !ok {
			t.Errorf("servers lacks %s", k)
		}
	}
	for _, k := range []string{"connected", "recorded", "unrecorded", "recorded_not_connected", "known_mash_users"} {
		if _, ok := m["summary"].(map[string]any)[k]; !ok {
			t.Errorf("summary lacks %s", k)
		}
	}

	out, _, code = e.run("issued", "list", "--json")
	var l issuedList
	if err := json.Unmarshal([]byte(out), &l); err != nil || code != 0 || !l.Present || l.Count != len(l.Issued) || l.Count < 3 {
		t.Fatalf("list: %v %d %+v", err, code, l)
	}
	for _, r := range l.Issued {
		if r.PublicKey == "" || r.Kind == "" || r.IssuedAt == "" || r.Tags == nil {
			t.Errorf("entry %+v", r)
		}
	}
	text, _, code := e.run("issued", "list")
	if code != 0 || !strings.Contains(text, "endpoint\tx\t") || !strings.Contains(text, "admin\t") {
		t.Fatalf("%d %s", code, text)
	}
	noSecrets(t, out, text)
}

// A server with more connections than one page is read whole, page by page, with no connection dropped or repeated.
func TestConnzReadsEveryPage(t *testing.T) {
	e := newDriftEnv(t)
	for i := 0; i < 4; i++ {
		e.connect(e.mint("a", "p"))
	}
	nc := e.connect(natsauth.UserCredentials(e.env["MASH_SYS_CREDS"]))
	res := apply.NATS{Conn: nc}
	all, err := res.Connz(context.Background(), e.acctPub, nil, 0)
	if err != nil || len(all) != 1 {
		t.Fatalf("%v %d", err, len(all))
	}
	paged, err := res.Connz(context.Background(), e.acctPub, nil, 2)
	if err != nil || len(paged) != 1 {
		t.Fatalf("%v %d", err, len(paged))
	}
	if len(all[0].Conns) < 4 || len(paged[0].Conns) != len(all[0].Conns) || paged[0].Total != all[0].Total {
		t.Fatalf("one page %d, paged %d, total %d", len(all[0].Conns), len(paged[0].Conns), paged[0].Total)
	}
	seen := map[uint64]bool{}
	for _, c := range paged[0].Conns {
		if seen[c.CID] {
			t.Errorf("connection %d read twice", c.CID)
		}
		seen[c.CID] = true
	}
}
