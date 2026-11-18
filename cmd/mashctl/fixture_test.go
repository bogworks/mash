package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/mint"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

// ad is the endpoint address <test owner>.g.<instance>.
func ad(instance string) sap.Address {
	return sap.Address{Owner: testenv.Owner, Group: "g", Instance: instance}
}

// privateDir is a fresh temporary directory that only its owner can use.
func privateDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if err := os.Chmod(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return d
}

// fleetEnv is a server, a config directory with a key store (account key imported) and no endpoints: a test makes the
// endpoints it needs with seed, which provisions the directory record and writes the creds file.
type fleetEnv struct {
	t   *testing.T
	e   *testenv.Env
	dir string
	env map[string]string
}

func newFleetEnv(t *testing.T) *fleetEnv {
	t.Helper()
	e := testenv.Start(t)
	dir := t.TempDir()
	// the creds directory must not be group-writable, and t.TempDir is 0777 minus the umask (0775 under umask 002).
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	h := &fleetEnv{t: t, e: e, dir: dir, env: map[string]string{
		"HOME": t.TempDir(), "MASH_URL": e.URL, "MASH_OWNER": e.Owner, "MASH_ADMIN_CREDS": e.AdminCreds, "MASH_INSECURE_PLAINTEXT": "1",
		"MASH_CONFIG_DIR": dir, "MASH_KEYS_DIR": filepath.Join(dir, "keys"),
	}}
	if _, _, code := h.run("init"); code != 0 {
		t.Fatal("init")
	}
	ka, err := ctlkeys.Open(h.env["MASH_KEYS_DIR"])
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ka.ImportAccount(e.AccountSeed); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *fleetEnv) run(args ...string) (string, string, int) {
	h.t.Helper()
	var out, errb bytes.Buffer
	code := runEnv(args, func(k string) string { return h.env[k] }, &out, &errb)
	return out.String(), errb.String(), code
}

func (h *fleetEnv) keys() *ctlkeys.Authority {
	ka, err := ctlkeys.Open(h.env["MASH_KEYS_DIR"])
	if err != nil {
		h.t.Fatal(err)
	}
	return ka
}

// creds is where the creds file of the endpoint at a lives: <config dir>/owner.group.instance.creds.
func (h *fleetEnv) creds(a sap.Address) string { return filepath.Join(h.dir, a.String()+".creds") }

// seed makes the endpoint at a: its directory record and its creds file, as an earlier create would have.
func (h *fleetEnv) seed(a sap.Address) { h.t.Helper(); h.seedWith(a, nil) }

// seedWith is seed, then applies edit to the claims and signs them again with the account key; a nil edit leaves the
// JWT as a mint made it. The directory record has the same user key.
func (h *fleetEnv) seedWith(a sap.Address, edit func(*jwt.UserClaims)) {
	h.t.Helper()
	h.seedMeta(a, admin.Meta{}, edit)
}

// seedMeta is seedWith with the metadata (role, host, labels) the record is provisioned with.
func (h *fleetEnv) seedMeta(a sap.Address, meta admin.Meta, edit func(*jwt.UserClaims)) {
	h.t.Helper()
	m, err := mint.Mint(h.keys(), a, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	creds := m.Creds
	if edit != nil {
		uc, err := jwt.DecodeUserClaims(m.JWT)
		if err != nil {
			h.t.Fatal(err)
		}
		edit(uc)
		akp, _, err := h.keys().Account()
		if err != nil {
			h.t.Fatal(err)
		}
		tok, err := uc.Encode(akp)
		if err != nil {
			h.t.Fatal(err)
		}
		kp, err := jwt.ParseDecoratedNKey(m.Creds)
		if err != nil {
			h.t.Fatal(err)
		}
		seed, _ := kp.Seed()
		if creds, err = jwt.FormatUserConfig(tok, seed); err != nil {
			h.t.Fatal(err)
		}
	}
	if err := os.WriteFile(h.creds(a), creds, 0o600); err != nil {
		h.t.Fatal(err)
	}
	adm := h.adminOf()
	if _, err := adm.Provision(context.Background(), a, m.UserPub, m.AccountPub, "", meta); err != nil {
		h.t.Fatal(err)
	}
	// the issue record, as the mint of a create writes it: public information only
	rec := store.Issued{PublicKey: m.UserPub, Address: a.Flat(), Owner: a.Owner, Kind: store.IssuedEndpoint, Tags: []string{}, Purpose: store.PurposeIssue}
	if uc, err := jwt.DecodeUserClaims(m.JWT); err == nil && uc.IssuedAt > 0 {
		rec.IssuedAt = time.Unix(uc.IssuedAt, 0).UTC().Format(time.RFC3339)
	}
	if err := adm.RecordIssued(context.Background(), rec); err != nil {
		h.t.Fatal(err)
	}
}

func (h *fleetEnv) credsClaims(a sap.Address) (*jwt.UserClaims, string) {
	h.t.Helper()
	raw, err := os.ReadFile(h.creds(a))
	if err != nil {
		h.t.Fatal(err)
	}
	tok, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		h.t.Fatal(err)
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		h.t.Fatal(err)
	}
	return uc, uc.Subject
}

var seedLike = regexp.MustCompile(`(^|[^A-Z2-7])SU[A-Z2-7]{20}`)

func noSecrets(t *testing.T, outs ...string) {
	t.Helper()
	for _, o := range outs {
		for _, bad := range []string{"BEGIN NATS USER JWT", "BEGIN USER NKEY SEED", "eyJ"} {
			if strings.Contains(o, bad) {
				t.Errorf("output holds %q: %q", bad, o)
			}
		}
		// A user seed is "SU" then base32; a bare "SU" also turns up inside a public key id (e.g. "UC7MSUKQ...") by chance.
		if seedLike.MatchString(o) {
			t.Errorf("output holds a seed: %q", o)
		}
	}
}

// records is "owner.group.instance//user" for every directory record.
func (h *fleetEnv) records() []string {
	h.t.Helper()
	recs, _, err := h.adminOf().List(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, r := range recs {
		a, err := r.Address()
		if err != nil {
			h.t.Fatal(err)
		}
		out = append(out, a.String()+"//"+r.User)
	}
	return out
}

// record is the directory record of the endpoint at a, or false when there is none.
func (h *fleetEnv) record(a sap.Address) (store.Record, bool) {
	h.t.Helper()
	recs, _, err := h.adminOf().List(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	return findRecord(recs, a)
}

// adminOf is an Admin on the fleetEnv server.
func (h *fleetEnv) adminOf() *admin.Admin {
	h.t.Helper()
	a, err := admin.New(h.e.Conn(h.t, h.e.AdminCreds), h.e.Owner)
	if err != nil {
		h.t.Fatal(err)
	}
	return a
}

func (h *fleetEnv) accountPub() string {
	h.t.Helper()
	akp, pub, err := h.keys().Account()
	if err != nil {
		h.t.Fatal(err)
	}
	akp.Wipe()
	return pub
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func issuedRecords(t *testing.T, adm *admin.Admin) ([]store.Issued, error) {
	t.Helper()
	recs, _, err := adm.Issued(context.Background())
	return recs, err
}

// extraTag makes a creds file that carries a tag the account key never gives.
func extraTag(uc *jwt.UserClaims) { uc.Tags.Add("allow:gone.x") }

type revokeCall struct {
	users []string
	at    time.Time // the revocation time the caller passed; zero when it passed none
}

// fakeAcked is the server count every test fake of revokeUsers reports as having taken the update.
// A test that checks the count sets it with setFakeAcked.
var fakeAcked = 1

func setFakeAcked(t *testing.T, n int) {
	t.Helper()
	prev := fakeAcked
	fakeAcked = n
	t.Cleanup(func() { fakeAcked = prev })
}

// fakeRevoke replaces the revocation seam for the test; err, when set, is what every call returns.
func fakeRevoke(t *testing.T, err error) *[]revokeCall {
	t.Helper()
	var calls []revokeCall
	prev := revokeUsers
	revokeUsers = func(_ context.Context, _ config.Config, _ func(string) string, _ int, users []string, _ io.Writer, now func() time.Time) (int, error) {
		c := revokeCall{users: slices.Clone(users)}
		if now != nil {
			c.at = now()
		}
		calls = append(calls, c)
		if err != nil {
			return 0, err
		}
		return fakeAcked, nil
	}
	t.Cleanup(func() { revokeUsers = prev })
	fakeKeyRevoked(t, func(string) (bool, error) { return false, nil })
	return &calls
}

// fakeKeyRevoked replaces the account revocation read for the test: f answers for a user public key.
func fakeKeyRevoked(t *testing.T, f func(pub string) (bool, error)) {
	t.Helper()
	prev := keyRevoked
	keyRevoked = func(_ context.Context, _ config.Config, _ func(string) string, _ int, pub string) (bool, error) {
		return f(pub)
	}
	t.Cleanup(func() { keyRevoked = prev })
}
