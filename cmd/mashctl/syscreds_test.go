package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/bootstrap"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/mint"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// applyEnv is a local operator-mode server (bootstrap's configuration, run in process) and the environment mashctl
// needs to reach it.
type applyEnv struct {
	t       *testing.T
	root    string
	keyDir  string
	srv     *server.Server
	pl      *bootstrap.Plan
	env     map[string]string
	ks      *ctlkeys.Authority
	acctPub string
	tlsCA   string // the CA of a TLS server (empty for a plain one): the helpers that connect directly trust it
}

func newApplyEnv(t *testing.T) *applyEnv {
	t.Helper()
	return newApplyEnvTweak(t, nil)
}

// newApplyEnvTweak is newApplyEnv with a hook that runs after the server files are written and before the server
// starts, so a test can rewrite the operator or account JWTs on disk.
func newApplyEnvTweak(t *testing.T, tweak func(e *applyEnv)) *applyEnv {
	t.Helper()
	return newApplyEnvOpts(t, tweak, nil)
}

// newApplyEnvOpts is newApplyEnvTweak with a hook that may change the server options (for example to add TLS) before
// the server starts.
func newApplyEnvOpts(t *testing.T, tweak func(e *applyEnv), optsHook func(o *server.Options)) *applyEnv {
	t.Helper()
	root := t.TempDir()
	e := &applyEnv{t: t, root: root, keyDir: filepath.Join(root, "keys")}
	ks, err := ctlkeys.Open(e.keyDir)
	if err != nil {
		t.Fatal(err)
	}
	e.ks = ks
	pl, err := bootstrap.Build(bootstrap.Params{Dir: filepath.Join(root, "server"), Owner: "alice", Port: 4222, JetStreamDisk: 256 << 20}, ks)
	if err != nil {
		t.Fatal(err)
	}
	if err := pl.Write(); err != nil {
		t.Fatal(err)
	}
	e.pl = pl
	e.acctPub = pl.Account
	if tweak != nil {
		tweak(e)
	}
	opts, err := server.ProcessConfigFile(pl.ConfPath())
	if err != nil {
		t.Fatal(err)
	}
	opts.Port = -1
	opts.NoLog, opts.NoSigs = true, true
	// init --local's server.conf is TLS only; these tests drive the verbs against a plain loopback server, so they
	// drop its tls block here. The TLS behaviour of the generated server is tested in internal/bootstrap.
	opts.TLS, opts.TLSConfig, opts.TLSCert, opts.TLSKey, opts.TLSCaCert = false, nil, "", "", ""
	if optsHook != nil {
		optsHook(opts)
	}
	srv, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("server did not start")
	}
	t.Cleanup(func() { srv.Shutdown(); srv.WaitForShutdown() })
	e.srv = srv
	e.env = map[string]string{
		"HOME": root, "MASH_CONFIG_DIR": filepath.Join(root, "cfg"), "MASH_SERVER": srv.ClientURL(), "MASH_INSECURE_PLAINTEXT": "1", "MASH_ENDPOINT": "alice.dev.x",
		"MASH_KEYS_DIR": e.keyDir, "MASH_SYS_CREDS": filepath.Join(pl.Dir, bootstrap.SysCreds),
		// the admin credentials, for the commands that read the directory.
		"MASH_ADMIN_CREDS": pl.Creds,
	}
	return e
}

// withDirectory gives the env a reachable, initialised directory and the admin credentials to read it.
func (e *applyEnv) withDirectory() *applyEnv {
	e.t.Helper()
	e.env["MASH_ADMIN_CREDS"] = e.pl.Creds
	if out, errb, code := e.run("init"); code != 0 {
		e.t.Fatalf("init: %d %s %s", code, out, errb)
	}
	return e
}

func (e *applyEnv) run(args ...string) (string, string, int) {
	e.t.Helper()
	var out, errb bytes.Buffer
	code := runEnv(args, func(k string) string { return e.env[k] }, &out, &errb)
	return out.String(), errb.String(), code
}

// accountJWT reads the MASH account's JWT from the resolver through the system account.
func (e *applyEnv) accountJWT() string {
	e.t.Helper()
	nc, err := nats.Connect(e.srv.ClientURL(), append(natsauth.TLSOptions(e.tlsCA, "", ""), natsauth.UserCredentials(e.env["MASH_SYS_CREDS"]))...)
	if err != nil {
		e.t.Fatal(err)
	}
	defer nc.Close()
	m, err := nc.Request("$SYS.REQ.ACCOUNT."+e.acctPub+".CLAIMS.LOOKUP", nil, 3*time.Second)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(m.Data)
}

func (e *applyEnv) claims() *jwt.AccountClaims {
	e.t.Helper()
	c, err := jwt.DecodeAccountClaims(e.accountJWT())
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func scopedSigners(c *jwt.AccountClaims) map[string]*jwt.UserScope {
	out := map[string]*jwt.UserScope{}
	for k, s := range c.SigningKeys {
		if us, ok := s.(*jwt.UserScope); ok && us != nil {
			out[k] = us
		}
	}
	return out
}

// noSeeds fails if any key's seed text appears in s.
func (e *applyEnv) noSeeds(s string) {
	e.t.Helper()
	files, _ := filepath.Glob(filepath.Join(e.keyDir, "*.nk"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			e.t.Fatal(err)
		}
		if seed := strings.TrimSpace(string(b)); seed != "" && strings.Contains(s, seed) {
			e.t.Fatalf("seed of %s appears in output", filepath.Base(f))
		}
	}
}

func TestSysCredsDefaultsFromTheInitServerDir(t *testing.T) {
	e := newApplyEnv(t).withDirectory()
	want := filepath.Join(e.pl.Dir, bootstrap.SysCreds)
	cfg := config.Config{AdminCreds: e.pl.Creds}
	// admin_creds points into the server directory: sys.creds is found next to it.
	if got, err := sysCredsPath("", func(string) string { return "" }, cfg); err != nil || got != want {
		t.Fatalf("default: %q %v, want %q", got, err, want)
	}
	// the flag, then MASH_SYS_CREDS, win
	if got, _ := sysCredsPath("flag.creds", func(string) string { return "env.creds" }, cfg); got != "flag.creds" {
		t.Errorf("flag: %q", got)
	}
	if got, _ := sysCredsPath("", func(k string) string { return map[string]string{"MASH_SYS_CREDS": "env.creds"}[k] }, cfg); got != "env.creds" {
		t.Errorf("env: %q", got)
	}
	// none anywhere: the error says how to give them
	if _, err := sysCredsPath("", func(string) string { return "" }, config.Config{}); err == nil || !strings.Contains(err.Error(), "--sys-creds FILE or set MASH_SYS_CREDS") {
		t.Errorf("no creds: %v", err)
	}
}

// xy is the address of the test endpoints x and y of newXYEnv: alice.g.<instance>.
func xy(instance string) sap.Address {
	return sap.Address{Owner: "alice", Group: "g", Instance: instance}
}

// endpoint makes the endpoint at a on the server: it mints the user with the account key store, writes the creds file
// where credsPath looks, provisions the directory record and records the issue, as a create of an endpoint does.
func (e *applyEnv) endpoint(a sap.Address, labels map[string]string) {
	e.t.Helper()
	m, err := mint.Mint(e.ks, a, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.root, "cfg"), 0o700); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(e.credsPath(a.String()), m.Creds, 0o600); err != nil {
		e.t.Fatal(err)
	}
	adm, ctx, done := e.admin()
	defer done()
	if _, err := adm.Provision(ctx, a, m.UserPub, m.AccountPub, "", admin.Meta{Labels: labels}); err != nil {
		e.t.Fatal(err)
	}
	rec := store.Issued{PublicKey: m.UserPub, Address: a.Flat(), Owner: a.Owner, Kind: store.IssuedEndpoint, Tags: []string{}, Purpose: store.PurposeIssue}
	if uc, err := jwt.DecodeUserClaims(m.JWT); err == nil && uc.IssuedAt > 0 {
		rec.IssuedAt = time.Unix(uc.IssuedAt, 0).UTC().Format(time.RFC3339)
	}
	if err := adm.RecordIssued(ctx, rec); err != nil {
		e.t.Fatal(err)
	}
}

// newXYEnv is a bootstrap server with the directory made and endpoints alice.g.x (net a, label team=red) and alice.g.y
// (net b): the clean state.
func newXYEnv(t *testing.T) *applyEnv {
	t.Helper()
	e := newApplyEnv(t)
	e.env["MASH_ADMIN_CREDS"] = e.pl.Creds
	e.env["MASH_INSECURE_PLAINTEXT"] = "1"
	if _, errb, code := e.run("init"); code != 0 {
		t.Fatalf("init: %s", errb)
	}
	e.endpoint(xy("x"), map[string]string{"net": "a", "team": "red"})
	e.endpoint(xy("y"), map[string]string{"net": "b"})
	return e
}

func (e *applyEnv) credsPath(name string) string { return filepath.Join(e.root, "cfg", name+".creds") }

// admin opens the directory admin; the returned func closes it.
func (e *applyEnv) admin() (*admin.Admin, context.Context, func()) {
	e.t.Helper()
	nc, err := nats.Connect(e.srv.ClientURL(), natsauth.UserCredentials(e.pl.Creds))
	if err != nil {
		e.t.Fatal(err)
	}
	a, err := admin.New(nc, "alice")
	if err != nil {
		e.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	return a, ctx, func() { cancel(); nc.Close() }
}
