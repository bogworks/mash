package enroll_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/enroll"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

type env struct {
	t       *testing.T
	e       *testenv.Env
	adm     *admin.Admin
	d       enroll.Deps
	revoked []string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := testenv.Start(t)
	ka, err := ctlkeys.Open(filepath.Join(t.TempDir(), "keys"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ka.ImportAccount(e.AccountSeed); err != nil {
		t.Fatal(err)
	}
	adm, err := admin.New(e.Conn(t, e.AdminCreds), e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := adm.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := &env{t: t, e: e, adm: adm}
	h.d = enroll.Deps{Keys: ka, Admin: adm, Account: e.Account, Revoke: func(_ context.Context, u string) error {
		h.revoked = append(h.revoked, u)
		return nil
	}}
	return h
}

func addr(instance string) sap.Address {
	return sap.Address{Owner: testenv.Owner, Group: "g", Instance: instance}
}

func newKey(t *testing.T) (nkeys.KeyPair, string) {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	return kp, pub
}

// connect assembles creds from the token and the key pair and connects with them.
func (h *env) connect(token string, kp nkeys.KeyPair) (*nats.Conn, error) {
	seed, _ := kp.Seed()
	creds, err := jwt.FormatUserConfig(token, seed)
	if err != nil {
		h.t.Fatal(err)
	}
	p := filepath.Join(h.t.TempDir(), "x.creds")
	if err := os.WriteFile(p, creds, 0o600); err != nil {
		h.t.Fatal(err)
	}
	nc, err := nats.Connect(h.e.URL, append(natsauth.TLSOptions(h.e.CAFile, h.e.ClientCert, h.e.ClientKey), natsauth.UserCredentials(p), nats.MaxReconnects(0))...)
	if err == nil {
		h.t.Cleanup(nc.Close)
	}
	return nc, err
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var r *enroll.Refusal
	if !errors.As(err, &r) || r.Code != code {
		t.Fatalf("want refusal %s, got %v", code, err)
	}
}

func TestCreateFromPublicKey(t *testing.T) {
	h := newEnv(t)
	ctx := context.Background()
	a := addr("one")
	kp, pub := newKey(t)
	res, err := enroll.Create(ctx, h.d, enroll.Request{Addr: a, PublicKey: pub, Note: "n"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Seed != nil || res.PublicKey != pub || res.Reissued || res.Addr != a {
		t.Fatalf("result %+v", res)
	}
	uc, err := jwt.DecodeUserClaims(res.JWT)
	if err != nil {
		t.Fatal(err)
	}
	if uc.Name != a.String() || uc.Subject != pub || uc.Issuer != h.e.Account {
		t.Fatalf("claims name=%q sub=%q iss=%q", uc.Name, uc.Subject, uc.Issuer)
	}
	if !reflect.DeepEqual(uc.Permissions, *admin.Permissions(a)) || len(uc.Tags) != 0 {
		t.Fatal("permissions or tags differ")
	}
	if u, acct, err := h.adm.UserOf(ctx, a); err != nil || u != pub || acct != h.e.Account {
		t.Fatalf("directory: %q %q %v", u, acct, err)
	}
	if ok, err := h.adm.Exists(ctx, a); err != nil || !ok {
		t.Fatalf("exists %v %v", ok, err)
	}
	js, err := jetstream.New(h.adm.Conn())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.Stream(ctx, "inbox-"+a.Flat()); err != nil {
		t.Fatalf("inbox stream: %v", err)
	}
	rec, found, err := h.adm.IssuedByKey(ctx, pub)
	if err != nil || !found || rec.Address != a.Flat() || rec.Kind != store.IssuedEndpoint {
		t.Fatalf("issue record %+v %v %v", rec, found, err)
	}
	nc, err := h.connect(res.JWT, kp)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	nc.Close()
}

func TestCreateRefusals(t *testing.T) {
	h := newEnv(t)
	ctx := context.Background()
	kp, _ := newKey(t)
	seed, _ := kp.Seed()
	acct, _ := nkeys.CreateAccount()
	acctPub, _ := acct.PublicKey()
	for name, k := range map[string]string{"malformed": "UABC", "seed": string(seed), "account": acctPub} {
		_, err := enroll.Create(ctx, h.d, enroll.Request{Addr: addr("bad"), PublicKey: k})
		wantCode(t, err, "public_key_invalid")
		if strings.Contains(err.Error(), string(seed)) {
			t.Fatalf("%s: refusal repeats the seed", name)
		}
	}
	if ok, _ := h.adm.Exists(ctx, addr("bad")); ok {
		t.Fatal("refused create left a record")
	}
	_, err := h.d.SignUser(addr("bad"), acctPub)
	wantCode(t, err, "public_key_invalid")
}

func TestKeyIssuedToAnotherAddress(t *testing.T) {
	h := newEnv(t)
	ctx := context.Background()
	_, pub := newKey(t)
	if _, err := enroll.Create(ctx, h.d, enroll.Request{Addr: addr("a"), PublicKey: pub}); err != nil {
		t.Fatal(err)
	}
	_, err := enroll.Create(ctx, h.d, enroll.Request{Addr: addr("b"), PublicKey: pub})
	wantCode(t, err, "public_key_issued")
	if ok, _ := h.adm.Exists(ctx, addr("b")); ok {
		t.Fatal("refused create left a record")
	}
}

func TestKeyRetired(t *testing.T) {
	h := newEnv(t)
	ctx := context.Background()
	a := addr("gone")
	_, pub := newKey(t)
	if _, err := enroll.Create(ctx, h.d, enroll.Request{Addr: a, PublicKey: pub}); err != nil {
		t.Fatal(err)
	}
	if err := h.adm.Deprovision(ctx, a); err != nil {
		t.Fatal(err)
	}
	_, pub2 := newKey(t)
	_, err := enroll.Create(ctx, h.d, enroll.Request{Addr: a, PublicKey: pub2})
	wantCode(t, err, "key_retired")
	if _, found, _ := h.adm.IssuedByKey(ctx, pub2); found {
		t.Fatal("a burned address left an issue record")
	}
}

func TestRerunAfterKillCompletes(t *testing.T) {
	h := newEnv(t)
	ctx := context.Background()
	a := addr("killed")
	kp, pub := newKey(t)
	// the run died after the issue record, before the directory record
	if err := h.adm.RecordIssuedChecked(ctx, store.Issued{PublicKey: pub, Address: a.Flat(), Owner: a.Owner, Kind: store.IssuedEndpoint, Tags: []string{}, Purpose: store.PurposeIssue}); err != nil {
		t.Fatal(err)
	}
	res, err := enroll.Create(ctx, h.d, enroll.Request{Addr: a, PublicKey: pub})
	if err != nil {
		t.Fatal(err)
	}
	if u, _, err := h.adm.UserOf(ctx, a); err != nil || u != pub {
		t.Fatalf("directory %q %v", u, err)
	}
	// and a run that had finished is finished again, with the same shape
	again, err := enroll.Create(ctx, h.d, enroll.Request{Addr: a, PublicKey: pub})
	if err != nil || again.PublicKey != res.PublicKey {
		t.Fatalf("second run: %+v %v", again, err)
	}
	recs, err := h.adm.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range recs {
		if r.Instance == "killed" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d identities", n)
	}
	nc, err := h.connect(again.JWT, kp)
	if err != nil {
		t.Fatal(err)
	}
	nc.Close()
}

func TestSameHost(t *testing.T) {
	h := newEnv(t)
	ctx := context.Background()
	a := addr("local")
	res, err := enroll.Create(ctx, h.d, enroll.Request{Addr: a})
	if err != nil {
		t.Fatal(err)
	}
	kp, err := nkeys.FromSeed(res.Seed)
	if err != nil {
		t.Fatal(err)
	}
	if pub, _ := kp.PublicKey(); pub != res.PublicKey {
		t.Fatal("seed does not match the public key")
	}
	nc, err := h.connect(res.JWT, kp)
	if err != nil {
		t.Fatal(err)
	}
	nc.Close()
}

func TestReissue(t *testing.T) {
	h := newEnv(t)
	ctx := context.Background()
	a := addr("rot")
	_, old := newKey(t)
	if _, err := enroll.Create(ctx, h.d, enroll.Request{Addr: a, PublicKey: old}); err != nil {
		t.Fatal(err)
	}
	// reissue of an endpoint that does not exist
	if _, err := enroll.Reissue(ctx, h.d, addr("nope"), ""); err == nil {
		t.Fatal("reissue of a missing endpoint succeeded")
	}
	kp, nw := newKey(t)
	res, err := enroll.Reissue(ctx, h.d, a, nw)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Reissued || res.PublicKey != nw || res.Seed != nil {
		t.Fatalf("result %+v", res)
	}
	if u, _, _ := h.adm.UserOf(ctx, a); u != nw {
		t.Fatalf("directory names %q", u)
	}
	if len(h.revoked) != 1 || h.revoked[0] != old {
		t.Fatalf("revoked %v", h.revoked)
	}
	if rec, found, _ := h.adm.IssuedByKey(ctx, old); !found || rec.RevokedAt == "" {
		t.Fatalf("old key not marked revoked: %+v", rec)
	}
	if rec, found, _ := h.adm.IssuedByKey(ctx, nw); !found || rec.RevokedAt != "" || rec.Address != a.Flat() {
		t.Fatalf("new key record %+v", rec)
	}
	nc, err := h.connect(res.JWT, kp)
	if err != nil {
		t.Fatal(err)
	}
	nc.Close()
	// the old key can never come back
	_, err = enroll.Reissue(ctx, h.d, a, old)
	if err == nil {
		t.Fatal("a revoked key was accepted")
	}
	// the new key is bound to its address
	_, err = enroll.Reissue(ctx, h.d, addr("rot"), "")
	if err != nil {
		t.Fatal(err)
	}
	_, other := newKey(t)
	if _, err := enroll.Create(ctx, h.d, enroll.Request{Addr: addr("o2"), PublicKey: other}); err != nil {
		t.Fatal(err)
	}
	_, err = enroll.Reissue(ctx, h.d, a, other)
	wantCode(t, err, "public_key_issued")
}

func TestReissueSameHostReturnsSeed(t *testing.T) {
	h := newEnv(t)
	ctx := context.Background()
	a := addr("sh")
	if _, err := enroll.Create(ctx, h.d, enroll.Request{Addr: a}); err != nil {
		t.Fatal(err)
	}
	res, err := enroll.Reissue(ctx, h.d, a, "")
	if err != nil {
		t.Fatal(err)
	}
	kp, err := nkeys.FromSeed(res.Seed)
	if err != nil {
		t.Fatal(err)
	}
	if pub, _ := kp.PublicKey(); pub != res.PublicKey {
		t.Fatal("seed does not match")
	}
}

func TestCreateAddressTakenWritesNothing(t *testing.T) {
	h := newEnv(t)
	ctx := context.Background()
	a := addr("taken")
	_, first := newKey(t)
	if _, err := enroll.Create(ctx, h.d, enroll.Request{Addr: a, PublicKey: first}); err != nil {
		t.Fatal(err)
	}
	_, other := newKey(t)
	_, err := enroll.Create(ctx, h.d, enroll.Request{Addr: a, PublicKey: other})
	wantCode(t, err, enroll.CodeAddressTaken)
	if _, found, _ := h.adm.IssuedByKey(ctx, other); found {
		t.Fatal("a refused create bound the key to the address")
	}
	// a rerun with the same key still completes
	if _, err := enroll.Create(ctx, h.d, enroll.Request{Addr: a, PublicKey: first}); err != nil {
		t.Fatal(err)
	}
}

func TestCreateRefusalOrder(t *testing.T) {
	h := newEnv(t)
	ctx := context.Background()
	// retired beats taken
	gone := addr("gone2")
	_, k1 := newKey(t)
	if _, err := enroll.Create(ctx, h.d, enroll.Request{Addr: gone, PublicKey: k1}); err != nil {
		t.Fatal(err)
	}
	if err := h.adm.Deprovision(ctx, gone); err != nil {
		t.Fatal(err)
	}
	_, k2 := newKey(t)
	_, err := enroll.Create(ctx, h.d, enroll.Request{Addr: gone, PublicKey: k2})
	wantCode(t, err, enroll.CodeKeyRetired)
	// taken beats issued: k3 is issued to a, and b is taken by k4
	a, b := addr("oa"), addr("ob")
	_, k3 := newKey(t)
	_, k4 := newKey(t)
	if _, err := enroll.Create(ctx, h.d, enroll.Request{Addr: a, PublicKey: k3}); err != nil {
		t.Fatal(err)
	}
	if _, err := enroll.Create(ctx, h.d, enroll.Request{Addr: b, PublicKey: k4}); err != nil {
		t.Fatal(err)
	}
	_, err = enroll.Create(ctx, h.d, enroll.Request{Addr: b, PublicKey: k3})
	wantCode(t, err, enroll.CodeAddressTaken)
	// issued
	_, err = enroll.Create(ctx, h.d, enroll.Request{Addr: addr("oc"), PublicKey: k3})
	wantCode(t, err, enroll.CodePublicKeyIssued)
	// invalid beats retired
	_, err = enroll.Create(ctx, h.d, enroll.Request{Addr: gone, PublicKey: "UABC"})
	wantCode(t, err, enroll.CodePublicKeyInvalid)
}

func TestCreateKeyRevokedTyped(t *testing.T) {
	h := newEnv(t)
	ctx := context.Background()
	_, old := newKey(t)
	a := addr("rv")
	if _, err := enroll.Create(ctx, h.d, enroll.Request{Addr: a, PublicKey: old}); err != nil {
		t.Fatal(err)
	}
	_, nw := newKey(t)
	if _, err := enroll.Reissue(ctx, h.d, a, nw); err != nil {
		t.Fatal(err)
	}
	_, err := enroll.Create(ctx, h.d, enroll.Request{Addr: addr("rv2"), PublicKey: old})
	wantCode(t, err, enroll.CodeKeyRevoked)
}

func TestReissueRequiresRevoke(t *testing.T) {
	h := newEnv(t)
	ctx := context.Background()
	a := addr("nr")
	_, old := newKey(t)
	if _, err := enroll.Create(ctx, h.d, enroll.Request{Addr: a, PublicKey: old}); err != nil {
		t.Fatal(err)
	}
	d := h.d
	d.Revoke = nil
	_, nw := newKey(t)
	if _, err := enroll.Reissue(ctx, d, a, nw); err == nil {
		t.Fatal("reissue without Revoke succeeded")
	}
	if _, found, _ := h.adm.IssuedByKey(ctx, nw); found {
		t.Fatal("a refused reissue wrote an issue record")
	}
	if rec, _, _ := h.adm.IssuedByKey(ctx, old); rec.RevokedAt != "" {
		t.Fatal("old key marked revoked")
	}
}

func TestReissueBurnedRefused(t *testing.T) {
	h := newEnv(t)
	ctx := context.Background()
	a := addr("bu")
	_, old := newKey(t)
	if _, err := enroll.Create(ctx, h.d, enroll.Request{Addr: a, PublicKey: old}); err != nil {
		t.Fatal(err)
	}
	if err := h.adm.Deprovision(ctx, a); err != nil {
		t.Fatal(err)
	}
	_, nw := newKey(t)
	_, err := enroll.Reissue(ctx, h.d, a, nw)
	wantCode(t, err, enroll.CodeKeyRetired)
	if _, found, _ := h.adm.IssuedByKey(ctx, nw); found {
		t.Fatal("a burned address left an issue record")
	}
}

func TestWrongDepsAccount(t *testing.T) {
	h := newEnv(t)
	ctx := context.Background()
	other, _ := nkeys.CreateAccount()
	otherPub, _ := other.PublicKey()
	d := h.d
	d.Account = otherPub
	_, pub := newKey(t)
	_, err := enroll.Create(ctx, d, enroll.Request{Addr: addr("wa"), PublicKey: pub})
	if err == nil || !strings.Contains(err.Error(), "Deps.Account") {
		t.Fatalf("create: %v", err)
	}
	if _, found, _ := h.adm.IssuedByKey(ctx, pub); found {
		t.Fatal("wrong account left an issue record")
	}
	if _, err := enroll.Create(ctx, h.d, enroll.Request{Addr: addr("wb"), PublicKey: pub}); err != nil {
		t.Fatal(err)
	}
	_, nw := newKey(t)
	_, err = enroll.Reissue(ctx, d, addr("wb"), nw)
	if err == nil || !strings.Contains(err.Error(), "Deps.Account") {
		t.Fatalf("reissue: %v", err)
	}
	if _, found, _ := h.adm.IssuedByKey(ctx, nw); found {
		t.Fatal("wrong account left an issue record")
	}
}

// secondDeps is another admin on the same server with its own clock (offset from the real one) and its own Deps.
func (h *env) secondDeps(skew time.Duration) enroll.Deps {
	h.t.Helper()
	adm, err := admin.New(h.e.Conn(h.t, h.e.AdminCreds), h.e.Owner)
	if err != nil {
		h.t.Fatal(err)
	}
	adm.UseClock(func() time.Time { return time.Now().Add(skew) })
	h.smallInboxes(adm)
	d := h.d
	d.Admin = adm
	return d
}

// smallInboxes gives adm the smallest inbox cap allowed, so many inboxes fit the test server's disk quota.
func (h *env) smallInboxes(adm *admin.Admin) {
	h.t.Helper()
	l := store.DefaultLimits()
	l.InboxMaxBytes = 2 * store.MaxMsgSize
	if err := adm.SetLimits(l); err != nil {
		h.t.Fatal(err)
	}
}

// directoryKeys is how many records the directory holds.
func (h *env) directoryKeys() int {
	h.t.Helper()
	js, err := jetstream.New(h.adm.Conn())
	if err != nil {
		h.t.Fatal(err)
	}
	kv, err := js.KeyValue(context.Background(), store.DirectoryBucket)
	if err != nil {
		h.t.Fatal(err)
	}
	keys, err := kv.Keys(context.Background())
	if err != nil && !errors.Is(err, jetstream.ErrNoKeysFound) {
		h.t.Fatal(err)
	}
	return len(keys)
}

// REQ-003 / AT-004: two machines creating one address at once, whatever their clocks say: exactly one succeeds, the
// other is refused address_taken, and the directory names the winner's key only (the loser's key is not bound).
func TestConcurrentCreateOfOneAddressNeverBothSucceed(t *testing.T) {
	const rounds = 20
	for _, skew := range []time.Duration{0, 5 * time.Second, 25 * time.Second, -25 * time.Second, 45 * time.Second, -45 * time.Second} {
		t.Run(skew.String(), func(t *testing.T) {
			h := newEnv(t)
			h.smallInboxes(h.adm)
			deps := [2]enroll.Deps{h.d, h.secondDeps(skew)}
			ctx := context.Background()
			for r := 0; r < rounds; r++ {
				a := addr(fmt.Sprintf("race%d", r))
				_, k1 := newKey(t)
				_, k2 := newKey(t)
				keys := [2]string{k1, k2}
				var errs [2]error
				start := make(chan struct{})
				var wg sync.WaitGroup
				for i := range 2 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start
						_, errs[i] = enroll.Create(ctx, deps[i], enroll.Request{Addr: a, PublicKey: keys[i]})
					}()
				}
				close(start)
				wg.Wait()
				win := -1
				for i, err := range errs {
					if err == nil {
						if win >= 0 {
							t.Fatalf("round %d: both Creates succeeded", r)
						}
						win = i
					}
				}
				if win < 0 {
					t.Fatalf("round %d: neither succeeded: %v / %v", r, errs[0], errs[1])
				}
				// Within the lock bound the loser is refused address_taken at once. Past it (the two clocks disagree by more than
				// lockTTL) one admin sees the other's lock as expired and takes it over, the first one's lock check fails, and the
				// loser may get the retryable lock-lost error instead; running its Create again then gives address_taken. Neither
				// case lets both succeed and the directory always names the winner.
				var ref *enroll.Refusal
				loser := errs[1-win]
				if !errors.As(loser, &ref) || ref.Code != enroll.CodeAddressTaken {
					if skew.Abs() <= 25*time.Second || !errors.Is(loser, admin.ErrLockLost) {
						t.Fatalf("round %d: loser got %v, want address_taken", r, loser)
					}
					_, again := enroll.Create(ctx, deps[1-win], enroll.Request{Addr: a, PublicKey: keys[1-win]})
					if !errors.As(again, &ref) || ref.Code != enroll.CodeAddressTaken {
						t.Fatalf("round %d: the loser's second Create got %v, want address_taken", r, again)
					}
				}
				user, _, err := h.adm.UserOf(ctx, a)
				if err != nil || user != keys[win] {
					t.Fatalf("round %d: directory user %q (err %v), want the winner's key", r, user, err)
				}
				if n := h.directoryKeys(); n != r+1 {
					t.Fatalf("round %d: %d directory records, want %d", r, n, r+1)
				}
			}
		})
	}
}
