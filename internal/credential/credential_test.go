package credential

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/issue"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyserve"
	"github.com/bogworks/mash/internal/store"
)

type fakeSet struct {
	c   *listset.Compiled
	err error
}

func (f fakeSet) InForce(context.Context) (*listset.Compiled, int, error) { return f.c, 7, f.err }

type fakeSigner struct{ kp nkeys.KeyPair }

func (f fakeSigner) GroupKey(string) (nkeys.KeyPair, error) {
	seed, _ := f.kp.Seed()
	return nkeys.FromSeed(seed)
}

type fakeRecord struct {
	mu   sync.Mutex
	recs map[string]store.Issued
}

func (f *fakeRecord) Lookup(_ context.Context, k string) (*store.Issued, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.recs[k]; ok {
		return &r, nil
	}
	return nil, nil
}

func (f *fakeRecord) RecordChecked(_ context.Context, r store.Issued) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if cur, ok := f.recs[r.PublicKey]; ok {
		if cur.RevokedAt != "" {
			return store.ErrKeyRevoked
		}
		if cur.Address != r.Address {
			return store.ErrKeyBoundElsewhere
		}
	}
	f.recs[r.PublicKey] = r
	return nil
}

type fakeClaims struct{ ac *jwt.AccountClaims }

func (f fakeClaims) Fetch(context.Context) (*jwt.AccountClaims, error) { return f.ac, nil }

type fixture struct {
	cfg    Config
	rec    *fakeRecord
	claims *jwt.AccountClaims
	logs   *bytes.Buffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	c, err := listset.Compile(listset.ListSet{
		Switches: []listset.SwitchEntry{
			{Group: "c", Verb: listset.Mail, Switch: listset.Closed}, {Group: "c", Verb: listset.Prompt, Switch: listset.Closed},
			{Group: "c", Verb: listset.Status, Switch: listset.Closed}, {Group: "c", Verb: listset.Heartbeat, Switch: listset.Closed},
		},
		Senders: []listset.SenderLists{{Sender: "c.core", Allow: []string{"c.lead"}}, {Sender: "c.lead"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	acct, _ := nkeys.CreateAccount()
	apub, _ := acct.PublicKey()
	gk, _ := nkeys.CreateAccount()
	gpub, _ := gk.PublicKey()
	var tpl jwt.Permissions
	for _, tt := range c.Templates {
		if tt.Group == "c" {
			tpl = tt.Permissions
		}
	}
	ac := jwt.NewAccountClaims(apub)
	us := jwt.NewUserScope()
	us.Key = gpub
	us.Template.Permissions = tpl
	ac.SigningKeys.AddScopedSigner(us)
	f := &fixture{rec: &fakeRecord{recs: map[string]store.Issued{}}, claims: ac, logs: &bytes.Buffer{}}
	f.cfg = Config{
		Account: apub, ListSet: fakeSet{c: c}, Signer: fakeSigner{gk}, Record: f.rec, Claims: fakeClaims{ac},
		Log: slog.New(slog.NewTextHandler(f.logs, nil)),
	}
	return f
}

func newReq(t *testing.T, address string) Request {
	t.Helper()
	_, pub, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	return Request{Address: address, Endpoint: "claude-code." + address, PublicKey: pub}
}

func refusalCode(r Result) string {
	if r.Refusal == nil {
		return ""
	}
	return r.Refusal.Code
}

func TestIssueSignsRecordsAndAssembles(t *testing.T) {
	f := newFixture(t)
	seed, pub, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Address: "c.core", Endpoint: "claude-code.c.core", PublicKey: pub}
	res, err := Issue(context.Background(), f.cfg, req)
	if err != nil || res.Outcome != OutcomeIssued || res.ExitCode() != 0 || res.JWT == "" {
		t.Fatalf("%+v, %v", res, err)
	}
	uc, err := jwt.DecodeUserClaims(res.JWT)
	if err != nil || uc.Subject != pub {
		t.Fatalf("subject %v, %v", uc, err)
	}
	if len(uc.UserPermissionLimits.Pub.Allow) != 0 || len(uc.UserPermissionLimits.Sub.Allow) != 0 {
		t.Error("the JWT carries permissions of its own")
	}
	if res.Generation != 7 || res.IssuedAt == "" || len(res.Tags) == 0 || res.Address != "c.core" || res.PublicKey != pub {
		t.Errorf("%+v", res)
	}
	if rec := f.rec.recs[pub]; rec.Address != "c.core" || rec.Purpose != store.PurposeIssue {
		t.Errorf("record %+v", rec)
	}
	creds, err := Assemble(res.JWT, seed)
	if err != nil || !bytes.Contains(creds, []byte(res.JWT)) || !bytes.Contains(creds, seed) {
		t.Fatalf("assemble: %v", err)
	}
	// Reissue: the same key at the same address is a new credential; rotation is accepted as a purpose.
	req.Purpose = PurposeRotation
	if res, _ = Issue(context.Background(), f.cfg, req); res.Outcome != OutcomeIssued {
		t.Errorf("reissue: %+v", res)
	}
	if f.rec.recs[pub].Purpose != store.PurposeRotation {
		t.Errorf("purpose %q", f.rec.recs[pub].Purpose)
	}
	b, _ := json.Marshal(res)
	for _, k := range []string{`"outcome":"issued"`, `"address":"c.core"`, `"public_key"`, `"jwt"`, `"tags"`, `"generation":7`, `"issued_at"`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("json lacks %s: %s", k, b)
		}
	}
}

func TestIssueRefusals(t *testing.T) {
	ctx := context.Background()
	t.Run("no list set", func(t *testing.T) {
		f := newFixture(t)
		f.cfg.ListSet = fakeSet{}
		res, err := Issue(ctx, f.cfg, newReq(t, "c.core"))
		if err != nil || res.Outcome != OutcomeRefused || refusalCode(res) != issue.CodeNoListSet || res.ExitCode() != 1 || res.JWT != "" {
			t.Fatalf("%+v, %v", res, err)
		}
		f.cfg.ListSet = fakeSet{err: errors.New("no bucket")}
		if res, _ = Issue(ctx, f.cfg, newReq(t, "c.core")); refusalCode(res) != issue.CodeNoListSet {
			t.Errorf("a failed read: %+v", res)
		}
	})
	t.Run("unlisted", func(t *testing.T) {
		f := newFixture(t)
		res, _ := Issue(ctx, f.cfg, newReq(t, "c.other"))
		if refusalCode(res) != issue.CodeUnlisted || len(f.rec.recs) != 0 {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("revoked in the record", func(t *testing.T) {
		f := newFixture(t)
		req := newReq(t, "c.core")
		f.rec.recs[req.PublicKey] = store.Issued{PublicKey: req.PublicKey, Address: "c.core", RevokedAt: "2026-11-16T00:00:00Z"}
		res, _ := Issue(ctx, f.cfg, req)
		if refusalCode(res) != issue.CodeRevoked || res.JWT != "" {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("revoked in the account JWT", func(t *testing.T) {
		f := newFixture(t)
		req := newReq(t, "c.core")
		f.claims.Revocations = jwt.RevocationList{req.PublicKey: time.Now().Unix()}
		res, _ := Issue(ctx, f.cfg, req)
		if refusalCode(res) != issue.CodeRevoked {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("bound elsewhere", func(t *testing.T) {
		f := newFixture(t)
		req := newReq(t, "c.core")
		if res, _ := Issue(ctx, f.cfg, req); res.Outcome != OutcomeIssued {
			t.Fatalf("%+v", res)
		}
		req.Address, req.Endpoint = "c.lead", "claude-code.c.lead"
		res, _ := Issue(ctx, f.cfg, req)
		if refusalCode(res) != issue.CodeBound || res.JWT != "" {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("endpoint mismatch", func(t *testing.T) {
		f := newFixture(t)
		req := newReq(t, "c.core")
		req.Endpoint = "claude-code.c.lead"
		res, _ := Issue(ctx, f.cfg, req)
		if refusalCode(res) != issue.CodeEndpoint || len(f.rec.recs) != 0 {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("the signing key is not scoped", func(t *testing.T) {
		f := newFixture(t)
		f.cfg.Claims = fakeClaims{jwt.NewAccountClaims(f.cfg.Account)}
		res, _ := Issue(ctx, f.cfg, newReq(t, "c.core"))
		if refusalCode(res) != issue.CodeNoScope {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("a purpose, an address and a key that are not", func(t *testing.T) {
		f := newFixture(t)
		for _, c := range []struct {
			mod  func(*Request)
			code string
		}{
			{func(r *Request) { r.Purpose = "probe" }, issue.CodeBadPurpose},
			{func(r *Request) { r.Address = "nodot" }, issue.CodeMalformed},
			{func(r *Request) { r.Endpoint = "bad" }, issue.CodeMalformed},
			{func(r *Request) { r.PublicKey = "UNOTAKEY" }, issue.CodeBadKey},
		} {
			req := newReq(t, "c.core")
			c.mod(&req)
			if res, _ := Issue(ctx, f.cfg, req); res.Outcome != OutcomeRefused || refusalCode(res) != c.code {
				t.Errorf("want %s: %+v", c.code, res)
			}
		}
	})
	t.Run("an unusable configuration is an error", func(t *testing.T) {
		res, err := Issue(ctx, Config{}, newReq(t, "c.core"))
		if err == nil || res.Outcome != OutcomeError || res.ExitCode() != 6 {
			t.Fatalf("%+v, %v", res, err)
		}
	})
}

// A seed in any field is refused with its own code, and is in no Result and no log line.
func TestASeedInAnyFieldIsRefusedAndNeverEchoed(t *testing.T) {
	seed, _, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	s := string(seed)
	for _, field := range []string{"address", "endpoint", "public_key", "purpose"} {
		for _, wrap := range []string{"%s", "x " + "%s" + " y"} {
			f := newFixture(t)
			req := newReq(t, "c.core")
			text := strings.Replace(wrap, "%s", s, 1)
			switch field {
			case "address":
				req.Address = text
			case "endpoint":
				req.Endpoint = text
			case "public_key":
				req.PublicKey = text
			case "purpose":
				req.Purpose = text
			}
			if rf := req.Validate(); rf == nil || rf.Code != CodeSeed || strings.Contains(rf.Detail, s) {
				t.Fatalf("%s: validate %v", field, rf)
			}
			res, err := Issue(context.Background(), f.cfg, req)
			if err != nil || res.Outcome != OutcomeRefused || refusalCode(res) != CodeSeed {
				t.Fatalf("%s: %+v, %v", field, res, err)
			}
			b, _ := json.Marshal(res)
			if strings.Contains(string(b), s) || strings.Contains(f.logs.String(), s) {
				t.Fatalf("%s: the seed is in the result or the log:\n%s\n%s", field, b, f.logs.String())
			}
			if len(f.rec.recs) != 0 {
				t.Errorf("%s: something was recorded", field)
			}
		}
	}
	// In JSON: a known field, an unknown one, and a seed that is not in a string position.
	for _, doc := range []string{
		`{"address":"c.core","endpoint":"claude-code.c.core","public_key":"` + s + `"}`,
		`{"address":"c.core","endpoint":"claude-code.c.core","public_key":"UX","seed":"` + s + `"}`,
		`{"` + s + `":1}`,
	} {
		if _, rf := Parse([]byte(doc)); rf == nil || rf.Code != CodeSeed || strings.Contains(rf.Detail, s) {
			t.Errorf("parse %.40s...: %v", doc, rf)
		}
	}
}

func TestParse(t *testing.T) {
	_, pub, _ := NewKey()
	ok := `{"address":"c.core","endpoint":"claude-code.c.core","public_key":"` + pub + `"}`
	r, rf := Parse([]byte(ok))
	if rf != nil || r.EffectivePurpose() != PurposeIssue || r.Address != "c.core" {
		t.Fatalf("%+v, %v", r, rf)
	}
	for doc, code := range map[string]string{
		`not json`: issue.CodeMalformed, `{"address":"c.core","tags":["x"]}`: issue.CodeMalformed, ok + ` {}`: issue.CodeMalformed,
		`{"address":"c.core","endpoint":"claude-code.c.core","public_key":"` + pub + `","purpose":"x"}`: issue.CodeBadPurpose,
		strings.Repeat(" ", MaxRequestBytes+1): issue.CodeTooLarge,
	} {
		if _, rf := Parse([]byte(doc)); rf == nil || rf.Code != code {
			t.Errorf("%.30q: %v, want %s", doc, rf, code)
		}
	}
}

func TestNewKeyAndAssemble(t *testing.T) {
	seed, pub, err := NewKey()
	if err != nil || !nkeys.IsValidPublicUserKey(pub) {
		t.Fatalf("%v", err)
	}
	if _, _, err := nkeys.DecodeSeed(seed); err != nil {
		t.Fatalf("not a seed: %v", err)
	}
	if kp, _ := nkeys.FromSeed(seed); func() string { p, _ := kp.PublicKey(); return p }() != pub {
		t.Error("the public key is not the seed's")
	}
	// A JWT for this key, signed by any account key.
	signer, _ := nkeys.CreateAccount()
	tok, err := jwt.NewUserClaims(pub).Encode(signer)
	if err != nil {
		t.Fatal(err)
	}
	creds, err := Assemble(tok, seed)
	if err != nil {
		t.Fatal(err)
	}
	gotTok, err := jwt.ParseDecoratedJWT(creds)
	if err != nil || gotTok != tok {
		t.Errorf("the creds file does not hold the JWT: %v", err)
	}
	gotKP, err := jwt.ParseDecoratedUserNKey(creds)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := gotKP.PublicKey(); p != pub {
		t.Error("the creds file holds another key")
	}

	// A JWT for another key is refused, and so is a seed that is not a user seed, junk, and a JWT that does not decode.
	otherSeed, _, _ := NewKey()
	acctKP, _ := nkeys.CreateAccount()
	acctSeed, _ := acctKP.Seed()
	for name, c := range map[string]struct {
		tok  string
		seed []byte
	}{
		"mismatch": {tok, otherSeed}, "account seed": {tok, acctSeed}, "junk seed": {tok, []byte("nope")}, "junk jwt": {"nope", seed},
	} {
		out, err := Assemble(c.tok, c.seed)
		if err == nil || out != nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.Contains(err.Error(), string(c.seed)) || strings.Contains(err.Error(), c.tok) {
			t.Errorf("%s: the error echoes its input: %v", name, err)
		}
	}
}

type fakeRecords struct {
	rec      store.Issued
	found    bool
	err      error
	hist     admin.IssuedTimes
	histErr  error
	histFind bool
}

func (f fakeRecords) IssuedByKey(context.Context, string) (store.Issued, bool, error) {
	return f.rec, f.found, f.err
}
func (f fakeRecords) IssuedHistoryByKey(context.Context, string) (admin.IssuedTimes, bool, error) {
	return f.hist, f.histFind, f.histErr
}

func showCfg(recs fakeRecords, revoked bool, revErr error, scan policyserve.ConnScan, scanErr error) Config {
	return Config{
		Records: recs,
		Revoked: func(context.Context, string) (bool, error) { return revoked, revErr },
		Scan:    func(context.Context) (policyserve.ConnScan, error) { return scan, scanErr },
	}
}

func TestShowFromThePublicKeyAlone(t *testing.T) {
	_, pub, _ := NewKey()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	recs := fakeRecords{rec: store.Issued{PublicKey: pub, Address: "c.core", Kind: store.IssuedEndpoint}, found: true,
		hist: admin.IssuedTimes{Latest: now, Max: now, Revisions: 2}, histFind: true}
	conn := policyserve.ConnScan{Conns: []apply.ConnzServer{{Name: "s2", Conns: []apply.ConnzConn{{Key: pub}}}, {Name: "s1"}},
		Answered: []string{"s1", "s2"}, Expected: 2}
	ctx := context.Background()

	rep, err := Show(ctx, showCfg(recs, false, nil, conn, nil), pub)
	if err != nil || !rep.Recorded || rep.Record.Address != "c.core" || rep.History == nil || rep.History.Revisions != 2 ||
		rep.AccountRevokes != StateNo || rep.Connection.State != StateYes || len(rep.Connection.Servers) != 1 || rep.Connection.Servers[0] != "s2" ||
		!rep.Complete || !rep.Clean {
		t.Fatalf("%+v, %v", rep, err)
	}
	if b, err := json.Marshal(rep); err != nil || !strings.Contains(string(b), `"account_revokes":"no"`) {
		t.Errorf("json %s, %v", b, err)
	}

	// Revoked and still connected is not clean.
	if rep, _ = Show(ctx, showCfg(recs, true, nil, conn, nil), pub); rep.AccountRevokes != StateYes || rep.Clean || !rep.Complete {
		t.Errorf("revoked and connected: %+v", rep)
	}
	// Not connected on a complete scan is a no; on a partial scan it is unknown, and a partial answer is not clean.
	none := policyserve.ConnScan{Answered: []string{"s1", "s2"}, Expected: 2}
	if rep, _ = Show(ctx, showCfg(recs, false, nil, none, nil), pub); rep.Connection.State != StateNo || !rep.Clean {
		t.Errorf("not connected: %+v", rep)
	}
	part := policyserve.ConnScan{Answered: []string{"s1"}, Missing: []string{"s2"}, Expected: 2, Incomplete: "s2 did not answer"}
	if rep, _ = Show(ctx, showCfg(recs, false, nil, part, nil), pub); rep.Connection.State != StateUnknown || rep.Clean || rep.Complete || len(rep.Notes) == 0 {
		t.Errorf("partial scan: %+v", rep)
	}
	// A key seen on a partial scan is connected, but the answer is still not clean.
	partSeen := part
	partSeen.Conns = conn.Conns[:1]
	if rep, _ = Show(ctx, showCfg(recs, false, nil, partSeen, nil), pub); rep.Connection.State != StateYes || rep.Clean {
		t.Errorf("partial scan, seen: %+v", rep)
	}
	// An unreadable account JWT, record or scan is unknown with a note, never a no.
	if rep, _ = Show(ctx, showCfg(recs, false, errors.New("one server silent"), conn, nil), pub); rep.AccountRevokes != StateUnknown || rep.Clean {
		t.Errorf("account JWT unreadable: %+v", rep)
	}
	if rep, _ = Show(ctx, showCfg(fakeRecords{err: errors.New("down")}, false, nil, conn, nil), pub); rep.Recorded || rep.Clean || len(rep.Notes) == 0 {
		t.Errorf("record unreadable: %+v", rep)
	}
	if rep, _ = Show(ctx, showCfg(recs, false, nil, policyserve.ConnScan{}, errors.New("no sys")), pub); rep.Connection.State != StateUnknown || rep.Clean {
		t.Errorf("scan failed: %+v", rep)
	}
	// A key the record does not hold is a finding of its own, not an error.
	if rep, err = Show(ctx, showCfg(fakeRecords{}, false, nil, none, nil), pub); err != nil || rep.Recorded || rep.Record != nil {
		t.Errorf("unrecorded: %+v, %v", rep, err)
	}
	// Not a user public key, and an unusable config, are errors.
	if _, err := Show(ctx, showCfg(recs, false, nil, none, nil), "nope"); err == nil {
		t.Error("a bad key was accepted")
	}
	if _, err := Show(ctx, Config{}, pub); err == nil {
		t.Error("an empty config was accepted")
	}
}

func TestResultExitCodes(t *testing.T) {
	for o, code := range map[string]int{OutcomeIssued: 0, OutcomeRefused: 1, OutcomeError: 6, "": 6, "mystery": 6} {
		if got := (Result{Outcome: o}).ExitCode(); got != code {
			t.Errorf("%q: %d, want %d", o, got, code)
		}
	}
}
