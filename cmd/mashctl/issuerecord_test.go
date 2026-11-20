package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/natsfixture"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// issued reads the issue record through the admin connection, by public key.
func (h *fleetEnv) issued() map[string]store.Issued {
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

func (h *fleetEnv) issuedHistory(pub string) int {
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

// mintRogue signs a user JWT for name with the account signing key, as a holder of a copy of that key outside mashctl
// would, and returns a connect option for it.
func (e *applyEnv) mintRogue(name string) nats.Option {
	e.t.Helper()
	akp, _, err := e.ks.Account()
	if err != nil {
		e.t.Fatal(err)
	}
	ukp, _ := nkeys.CreateUser()
	upub, _ := ukp.PublicKey()
	uc := jwt.NewUserClaims(upub)
	uc.Name = name
	uc.Permissions = *admin.Permissions(sap.Address{Owner: "alice", Group: "g", Instance: name}) // NewUserClaims leaves the limits unlimited
	tok, err := uc.Encode(akp)
	if err != nil {
		e.t.Fatal(err)
	}
	return nats.UserJWT(func() (string, error) { return tok, nil }, func(n []byte) ([]byte, error) { return ukp.Sign(n) })
}

// A server with more connections than one page is read whole, page by page, with no connection dropped or repeated.
func TestConnzReadsEveryPage(t *testing.T) {
	e := newXYEnv(t)
	for i := 0; i < 4; i++ {
		e.connect(e.mintRogue("p"))
	}
	nc := e.connect(natsauth.UserCredentials(e.sysCreds))
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

// retire marks the key revoked after the revocation succeeded; a failed revocation marks nothing; a
// failed mark is a warning and the revocation still stands; a key mash never recorded is no warning.
func TestRetireMarksTheKeyRevoked(t *testing.T) {
	t.Run("retire", func(t *testing.T) {
		h := newFleetEnv(t)
		h.seedMeta(ad("n"), admin.Meta{Labels: map[string]string{"net": "green"}}, nil)
		_, pub := h.credsClaims(ad("n"))
		s := stubRevoke(t)
		s.err = errors.New("no ack")
		if _, _, code := h.run("endpoint", "delete", "o.g.n", "--yes"); code == 0 {
			t.Fatal("retire passed with the revocation failing")
		}
		if h.issued()[pub].RevokedAt != "" {
			t.Fatal("marked although the revocation failed")
		}
		s.err = nil
		if out, errb, code := h.run("endpoint", "delete", "o.g.n", "--yes"); code != 0 {
			t.Fatalf("%d %s %s", code, out, errb)
		}
		if len(s.calls) < 2 || s.calls[len(s.calls)-1] != pub {
			t.Fatalf("revoke calls %v", s.calls)
		}
		if h.issued()[pub].RevokedAt == "" {
			t.Errorf("not marked: %+v", h.issued()[pub])
		}
	})
	t.Run("mark failure is a warning", func(t *testing.T) {
		h := newFleetEnv(t)
		h.seedMeta(ad("n"), admin.Meta{Labels: map[string]string{"net": "green"}}, nil)
		_, pub := h.credsClaims(ad("n"))
		stubRevoke(t)
		prev := ctl.IssuedMarkRevoked
		ctl.IssuedMarkRevoked = func(context.Context, *admin.Admin, string, time.Time) error { return errors.New("record down") }
		t.Cleanup(func() { ctl.IssuedMarkRevoked = prev })
		out, errb, code := h.run("endpoint", "delete", "o.g.n", "--yes")
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
		h := newFleetEnv(t)
		u := h.provisionDeclared("a") // provisioned without a minted credential: not in the issue record
		stubRevoke(t)
		out, errb, code := h.run("endpoint", "delete", "o.g.a", "--yes")
		// The pre-revocation check warns once that the key has no record; marking the revocation says nothing more.
		if code != 0 || strings.Count(errb, "issue record") != 1 || !strings.Contains(errb, "has no issue record") || strings.Contains(errb, "was not updated") {
			t.Fatalf("%d %s %s", code, out, errb)
		}
		if _, ok := h.issued()[u]; ok {
			t.Error("retire created a record")
		}
	})
}

// --- no exemption, init records the admin, incompleteness --------------------------------------------------------

// mintUnrecordedAdmin writes a new admin creds file for the account that mash never recorded and returns its path and key.
func mintUnrecordedAdmin(t *testing.T, e *applyEnv) (string, string) {
	t.Helper()
	akp, _, err := e.ks.Account()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := natsfixture.MintAdminCreds(akp)
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

// `init` records the admin user of the creds it is given, and running it again keeps the one record.
func TestInitRecordsTheAdmin(t *testing.T) {
	e := newApplyEnv(t)
	a := newIniter(t, e)
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errb)
	}
	pub, err := issuedCredsKey(a.admin)
	if err != nil {
		t.Fatal(err)
	}
	check := func(when string) {
		t.Helper()
		adm, ctx, done := e.admin()
		recs, present, err := adm.Issued(ctx)
		done()
		if err != nil || !present || len(recs) != 1 || recs[0].PublicKey != pub || recs[0].Kind != store.IssuedAdmin || recs[0].Purpose != store.PurposeIssue || recs[0].Owner != "alice" {
			t.Fatalf("record %s: %+v %v %v", when, recs, present, err)
		}
	}
	check("after init")
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("second init %d\n%s\n%s", code, out, errb)
	}
	check("after a second init")
}

// If the issue record cannot be made safely (its bucket allows rollup), init fails and says so.
func TestInitFailsWhenTheRecordCannotBeWritten(t *testing.T) {
	e := newApplyEnv(t)
	nc := e.connect(natsauth.UserCredentials(e.pl.Creds))
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateKeyValue(context.Background(), store.IssuedConfig()); err != nil { // allows rollup: not hardened
		t.Fatal(err)
	}
	a := newIniter(t, e)
	out, errb, code := a.init()
	if code == 0 || !strings.Contains(out+errb, "not hardened") {
		t.Fatalf("%d\n%s\n%s", code, out, errb)
	}
}
