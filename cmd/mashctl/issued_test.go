package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	"github.com/bogworks/mash/internal/bootstrap"
	"github.com/bogworks/mash/internal/ctl"
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

// issuedCredsKey is the public key of the user in the creds file at path, read from its JWT.
func issuedCredsKey(path string) (string, error) {
	raw, err := readCreds(path)
	if err != nil {
		return "", err
	}
	tok, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		return "", err
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		return "", err
	}
	return uc.Subject, nil
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
	want := []string{"account", "clean", "complete", "record_present", "recorded_not_connected", "revoked_connected", "servers", "summary", "unrecorded"}
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
	for _, k := range []string{"connected", "recorded", "unrecorded", "recorded_not_connected", "revoked_connected"} {
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
		if r.PublicKey == "" || r.Kind == "" || r.IssuedAt == "" || r.Tags == nil || r.Purpose != store.PurposeIssue || r.RevokedAt != "" {
			t.Errorf("entry %+v", r)
		}
	}
	text, _, code := e.run("issued", "list")
	if code != 0 || !strings.Contains(text, "endpoint\tx\t") || !strings.Contains(text, "admin\t") || !strings.Contains(text, "purpose issue") || strings.Contains(text, "revoked") {
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

// --- purpose and revoked_at ------------------------------------------------------------------------------------

// Each path writes its purpose: add issue, a reissue that keeps the key issue, a reissue that rotates rotation, and
// the admin user at init issue with kind admin.
func TestIssuedPurposePerPath(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	fakeRevoke(t, nil)
	fakeSignerPublished(t, true, nil)
	if _, errb, code := h.run("add", "n", "--zone", "green"); code != 0 {
		t.Fatal(errb)
	}
	_, pub1 := h.credsClaims("n")
	if r := h.issued()[pub1]; r.Purpose != store.PurposeIssue {
		t.Errorf("add: purpose %q", r.Purpose)
	}
	_, adminPub := credsSubject(t, h.e.AdminCreds)
	if r := h.issued()[adminPub]; r.Purpose != store.PurposeIssue || r.Kind != store.IssuedAdmin {
		t.Errorf("init admin: %+v", r)
	}
	if out, errb, code := h.run("reissue", "n"); code != 0 {
		t.Fatalf("%d %s %s", code, out, errb)
	}
	if r := h.issued()[pub1]; r.Purpose != store.PurposeIssue {
		t.Errorf("reissue that keeps the key: purpose %q", r.Purpose)
	}
	fakeKeyRevoked(t, true, nil) // the reissue rotates
	if out, errb, code := h.run("reissue", "n"); code != 0 {
		t.Fatalf("%d %s %s", code, out, errb)
	}
	_, pub2 := h.credsClaims("n")
	recs := h.issued()
	if recs[pub2].Purpose != store.PurposeRotation {
		t.Errorf("reissue that rotates: new key purpose %q", recs[pub2].Purpose)
	}
	if recs[pub1].Purpose != store.PurposeIssue {
		t.Errorf("the old key's purpose changed: %q", recs[pub1].Purpose)
	}
	text, _, code := h.run("issued", "list")
	if code != 0 || !strings.Contains(text, "purpose rotation") {
		t.Errorf("list: %d %s", code, text)
	}
}

// A rotating reissue marks the old key revoked after the revocation, and not the new one; a failed revocation marks nothing.
func TestReissueRotationMarksTheOldKeyRevoked(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	fakeSignerPublished(t, true, nil)
	if _, errb, code := h.run("add", "n", "--zone", "green"); code != 0 {
		t.Fatal(errb)
	}
	_, pub1 := h.credsClaims("n")
	fakeKeyRevoked(t, false, nil)

	calls := fakeRevoke(t, errors.New("no ack"))
	fakeKeyRevoked(t, true, nil)
	if _, _, code := h.run("reissue", "n"); code == 0 {
		t.Fatal("the reissue passed with the revocation failing")
	}
	if len(*calls) != 1 || h.issued()[pub1].RevokedAt != "" {
		t.Fatalf("calls %v, record %+v", *calls, h.issued()[pub1])
	}

	fakeRevoke(t, nil)
	fakeKeyRevoked(t, true, nil)
	if out, errb, code := h.run("reissue", "n"); code != 0 {
		t.Fatalf("%d %s %s", code, out, errb)
	}
	_, pub2 := h.credsClaims("n")
	recs := h.issued()
	if _, err := time.Parse(time.RFC3339, recs[pub1].RevokedAt); err != nil || !strings.HasSuffix(recs[pub1].RevokedAt, "Z") {
		t.Errorf("old key revoked_at %q", recs[pub1].RevokedAt)
	}
	if recs[pub2].RevokedAt != "" {
		t.Errorf("the new key is marked revoked: %+v", recs[pub2])
	}
	text, _, _ := h.run("issued", "list")
	if !strings.Contains(text, "revoked "+recs[pub1].RevokedAt) {
		t.Errorf("list does not show revoked_at:\n%s", text)
	}
}

// retire and deprovision mark the key revoked after the revocation succeeded; a failed revocation marks nothing; a
// failed mark is a warning and the revocation still stands; a key mash never recorded is no warning.
func TestRetireAndDeprovisionMarkTheKeyRevoked(t *testing.T) {
	t.Run("retire", func(t *testing.T) {
		h := newAddEnv(t, addPolicy)
		if _, errb, code := h.run("add", "n", "--zone", "green"); code != 0 {
			t.Fatal(errb)
		}
		_, pub := h.credsClaims("n")
		s := stubRevoke(t)
		s.err = errors.New("no ack")
		if _, _, code := h.run("retire", "n"); code == 0 {
			t.Fatal("retire passed with the revocation failing")
		}
		if h.issued()[pub].RevokedAt != "" {
			t.Fatal("marked although the revocation failed")
		}
		s.err = nil
		if out, errb, code := h.run("retire", "n"); code != 0 {
			t.Fatalf("%d %s %s", code, out, errb)
		}
		if len(s.calls) < 2 || s.calls[len(s.calls)-1] != pub {
			t.Fatalf("revoke calls %v", s.calls)
		}
		if h.issued()[pub].RevokedAt == "" {
			t.Errorf("not marked: %+v", h.issued()[pub])
		}
	})
	t.Run("deprovision", func(t *testing.T) {
		h := newAddEnv(t, addPolicy)
		if _, errb, code := h.run("add", "n", "--zone", "green"); code != 0 {
			t.Fatal(errb)
		}
		_, pub := h.credsClaims("n")
		s := stubRevoke(t)
		if out, errb, code := h.run("deprovision", "n"); code != 0 {
			t.Fatalf("%d %s %s", code, out, errb)
		}
		if len(s.calls) != 1 || s.calls[0] != pub {
			t.Fatalf("revoke calls %v", s.calls)
		}
		if h.issued()[pub].RevokedAt == "" {
			t.Errorf("not marked: %+v", h.issued()[pub])
		}
	})
	t.Run("mark failure is a warning", func(t *testing.T) {
		h := newAddEnv(t, addPolicy)
		if _, errb, code := h.run("add", "n", "--zone", "green"); code != 0 {
			t.Fatal(errb)
		}
		_, pub := h.credsClaims("n")
		stubRevoke(t)
		prev := ctl.IssuedMarkRevoked
		ctl.IssuedMarkRevoked = func(context.Context, *admin.Admin, string, time.Time) error { return errors.New("record down") }
		t.Cleanup(func() { ctl.IssuedMarkRevoked = prev })
		out, errb, code := h.run("retire", "n")
		if code != 0 {
			t.Fatalf("a failed mark failed the retire: %d %s %s", code, out, errb)
		}
		if !strings.Contains(errb, "warning") || !strings.Contains(errb, "issue record was not updated") || !strings.Contains(errb, "record down") {
			t.Errorf("no warning on stderr: %q", errb)
		}
		if h.issued()[pub].RevokedAt != "" {
			t.Error("marked despite the failure")
		}
		if len(h.records()) != 0 {
			t.Errorf("the retire did not complete: %v", h.records())
		}
	})
	t.Run("not recorded is silent", func(t *testing.T) {
		h := newAddEnv(t, addPolicy)
		u := h.provisionDeclared("a") // provisioned without mashctl add: not in the issue record
		stubRevoke(t)
		out, errb, code := h.run("retire", "a")
		// The pre-revocation check warns once that the key has no record; marking the revocation says nothing more.
		if code != 0 || strings.Count(errb, "issue record") != 1 || !strings.Contains(errb, "has no issue record") || strings.Contains(errb, "was not updated") {
			t.Fatalf("%d %s %s", code, out, errb)
		}
		if _, ok := h.issued()[u]; ok {
			t.Error("retire created a record")
		}
	})
}

// A connected key the record holds as revoked is its own finding: exit 1, listed apart from the unrecorded.
func TestIssuedCheckReportsARevokedKeyStillConnected(t *testing.T) {
	e := newDriftEnv(t)
	e.connect(natsauth.UserCredentials(e.credsPath("x")), nats.Name("x-client"))
	r, errb, code := e.issuedCheck()
	if code != 0 || !r.Clean || len(r.RevokedConn) != 0 {
		t.Fatalf("before: %d %+v %s", code, r, errb)
	}
	_, xKey := credsSubject(t, e.credsPath("x"))
	_, yKey := credsSubject(t, e.credsPath("y"))
	adm, ctx, closeAdm := e.admin()
	defer closeAdm()
	// y is revoked and not connected: no finding. x is revoked and connected: a finding.
	if err := adm.MarkRevoked(ctx, yKey, time.Now()); err != nil {
		t.Fatal(err)
	}
	if r, _, code = e.issuedCheck(); code != 0 || len(r.RevokedConn) != 0 {
		t.Fatalf("revoked, not connected: %d %+v", code, r)
	}
	if err := adm.MarkRevoked(ctx, xKey, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	r, errb, code = e.issuedCheck()
	if code != 1 || r.Clean || len(r.Unrecorded) != 0 || len(r.RevokedConn) != 1 || r.Summary.RevokedConn != 1 {
		t.Fatalf("after: %d %+v %s", code, r, errb)
	}
	f := r.RevokedConn[0]
	if f.PublicKey != xKey || f.Address != "x" || f.RevokedAt != "2026-11-01T00:00:00Z" || f.Purpose != store.PurposeIssue || f.Server == "" {
		t.Errorf("finding %+v", f)
	}
	out, _, code := e.run("issued", "check")
	if code != 1 || !strings.Contains(out, "REVOKED key "+xKey+" still connected") || strings.Contains(out, "UNRECORDED") || strings.Contains(out, "ok: ") {
		t.Fatalf("text: %d\n%s", code, out)
	}
	list, _, _ := e.run("issued", "list", "--json")
	if !strings.Contains(list, `"revoked_at": "2026-11-01T00:00:00Z"`) {
		t.Errorf("list --json lacks revoked_at:\n%s", list)
	}
	noSecrets(t, out, list)
}

// A record from before the purpose field lists as issue, in text and JSON.
func TestIssuedListShowsAMissingPurposeAsIssue(t *testing.T) {
	var buf strings.Builder
	old := store.Issued{PublicKey: "UAAA", Address: "o", Kind: "endpoint", Tags: []string{}, IssuedAt: "2026-01-01T00:00:00Z"}
	odd := store.Issued{PublicKey: "UBBB", Address: "p", Kind: "endpoint", Tags: []string{}, IssuedAt: "2026-01-01T00:00:00Z", Purpose: "future"}
	printIssuedList([]store.Issued{old, odd}, true, false, &buf)
	if !strings.Contains(buf.String(), "purpose issue") || !strings.Contains(buf.String(), "purpose future") {
		t.Errorf("%s", buf.String())
	}
	buf.Reset()
	printIssuedList([]store.Issued{old}, true, true, &buf)
	if !strings.Contains(buf.String(), `"purpose": "issue"`) || strings.Contains(buf.String(), "revoked_at") {
		t.Errorf("%s", buf.String())
	}
}

// --- no exemption, adopt records the admin, incompleteness --------------------------------------------------------

// mintUnrecordedAdmin writes a new admin creds file for the account that mash never recorded and returns its path and key.
func mintUnrecordedAdmin(t *testing.T, e *applyEnv) (string, string) {
	t.Helper()
	akp, _, err := e.ks.Account()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := bootstrap.MintAdminCreds(akp)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.root, "other-admin.creds")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	pub, err := issuedCredsKey(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, pub
}

// The admin creds the command itself connects with are not exempt: an admin key the record does not hold is reported
// (it is connected, by this very command), and once recorded it is clean.
func TestIssuedCheckHasNoExemptionForTheCommandsOwnAdmin(t *testing.T) {
	e := newDriftEnv(t)
	if r, errb, code := e.issuedCheck(); code != 0 || !r.Clean {
		t.Fatalf("clean state: %d %+v %s", code, r, errb)
	}
	path, pub := mintUnrecordedAdmin(t, e)
	e.env["MASH_ADMIN_CREDS"] = path
	r, errb, code := e.issuedCheck()
	if code != 1 || r.Clean || len(r.Unrecorded) != 1 || r.Unrecorded[0].PublicKey != pub {
		t.Fatalf("an unrecorded admin was not reported: %d %+v %s", code, r, errb)
	}
	out, _, code := e.run("issued", "check")
	if code != 1 || !strings.Contains(out, "UNRECORDED key "+pub) {
		t.Fatalf("text: %d %s", code, out)
	}

	// Record it (as init and adopt do) and the same connection is clean.
	adm, ctx, done := e.admin()
	raw, err := readCreds(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ctl.RecordAdminCreds(ctx, adm, "alice", raw); err != nil {
		t.Fatal(err)
	}
	done()
	if r, errb, code := e.issuedCheck(); code != 0 || !r.Clean || len(r.Unrecorded) != 0 {
		t.Fatalf("a recorded admin is not clean: %d %+v %s", code, r, errb)
	}
}

// `adopt --mint-admin` records the admin user it mints, before the creds file exists, and `init` afterwards still works.
func TestAdoptMintAdminRecordsTheAdmin(t *testing.T) {
	e := newApplyEnv(t, twoZones)
	a := newAdopter(t, e)
	out, errb, code := a.adopt("--mint-admin")
	if code != 0 || !strings.Contains(out, "recorded the admin user in the issue record") {
		t.Fatalf("%d\n%s\n%s", code, out, errb)
	}
	path := filepath.Join(a.env["MASH_CONFIG_DIR"], "server", "mashadmin.creds")
	pub, err := issuedCredsKey(path)
	if err != nil {
		t.Fatal(err)
	}
	adm, ctx, done := e.admin()
	recs, present, err := adm.Issued(ctx)
	done()
	if err != nil || !present || len(recs) != 1 || recs[0].PublicKey != pub || recs[0].Kind != store.IssuedAdmin || recs[0].Purpose != store.PurposeIssue || recs[0].Owner != "alice" {
		t.Fatalf("record after adopt: %+v %v %v", recs, present, err)
	}
	// An adopt again keeps the file and records again (same key, one more revision); init after that is fine.
	if out, errb, code := a.adopt("--mint-admin"); code != 0 {
		t.Fatalf("second adopt %d\n%s\n%s", code, out, errb)
	}
	if out, errb, code := a.run("init"); code != 0 {
		t.Fatalf("init %d\n%s\n%s", code, out, errb)
	}
	adm, ctx, done = e.admin()
	recs, _, err = adm.Issued(ctx)
	done()
	if err != nil || len(recs) != 1 || recs[0].PublicKey != pub {
		t.Fatalf("record after init: %+v %v", recs, err)
	}
}

// If the record cannot be written, adopt fails loudly, says the admin credentials were NOT recorded and leaves no creds file.
func TestAdoptMintAdminFailsWhenTheRecordCannotBeWritten(t *testing.T) {
	e := newApplyEnv(t, twoZones)
	nc := e.connect(natsauth.UserCredentials(e.pl.Creds))
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateKeyValue(context.Background(), store.IssuedConfig()); err != nil { // allows rollup: not hardened
		t.Fatal(err)
	}
	a := newAdopter(t, e)
	out, errb, code := a.adopt("--mint-admin")
	if code == 0 || !strings.Contains(out+errb, "NOT recorded") || !strings.Contains(out+errb, "no credential was handed out") {
		t.Fatalf("%d\n%s\n%s", code, out, errb)
	}
	if _, err := os.Stat(filepath.Join(a.env["MASH_CONFIG_DIR"], "server", "mashadmin.creds")); err == nil {
		t.Error("the creds file was left behind")
	}
}

// The scan is incomplete AND a finding exists: exit 1 (the finding wins), and the JSON and the text both still say the scan
// was incomplete.
func TestIssuedCheckSaysIncompleteAlongsideAFinding(t *testing.T) {
	e := newDriftEnv(t)
	e.env["MASH_SERVERS"] = "2"
	e.connect(e.mint("a", "rogue"))
	r, _, code := e.issuedCheck()
	if code != 1 || len(r.Unrecorded) != 1 || r.Complete || r.Clean || r.Incomplete == "" {
		t.Fatalf("%d %+v", code, r)
	}
	out, _, code := e.run("issued", "check", "--json")
	if code != 1 || !strings.Contains(out, `"incomplete": "`) || !strings.Contains(out, `"complete": false`) {
		t.Fatalf("json: %d\n%s", code, out)
	}
	text, _, code := e.run("issued", "check")
	if code != 1 || !strings.Contains(text, "UNRECORDED key") || !strings.Contains(text, "INCOMPLETE: ") {
		t.Fatalf("text: %d\n%s", code, text)
	}
	// Nothing found and still incomplete is exit 2.
	e2 := newDriftEnv(t)
	e2.env["MASH_SERVERS"] = "2"
	if r, _, code := e2.issuedCheck(); code != 2 || r.Incomplete == "" || r.Clean {
		t.Fatalf("no finding: %d %+v", code, r)
	}
}
