package natsfixture

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/sap"
)

type setup struct {
	root, serverDir, keyDir, cfgPath string
	ks                               *ctlkeys.Authority
	pl                               *Plan
}

func newSetup(t *testing.T) *setup {
	t.Helper()
	root := t.TempDir()
	s := &setup{root: root, serverDir: filepath.Join(root, "cfg", "server"), keyDir: filepath.Join(root, "keys"),
		cfgPath: filepath.Join(root, "cfg", "config.json")}
	ks, err := ctlkeys.Open(s.keyDir, "")
	if err != nil {
		t.Fatal(err)
	}
	s.ks = ks
	return s
}

// testDisk keeps the generated account's JetStream limit small, so a test does not depend on the host's free disk.
const testDisk = 256 << 20

func (s *setup) params() Params {
	return Params{Dir: s.serverDir, Owner: "alice", Port: 4222, ConfigPath: s.cfgPath, JetStreamDisk: testDisk}
}

func (s *setup) generate(t *testing.T) {
	t.Helper()
	pl, err := Build(s.params(), s.ks)
	if err != nil {
		t.Fatal(err)
	}
	if err := pl.Write(); err != nil {
		t.Fatal(err)
	}
	s.pl = pl
}

// snapshot is the content hash and mode of every file and directory under root.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		h := ""
		if fi.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			h = hex.EncodeToString(sum[:])
		}
		out[p] = fi.Mode().String() + " " + h
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func startInProcess(t *testing.T, conf string) *server.Server {
	t.Helper()
	opts, err := server.ProcessConfigFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	opts.Port = -1 // the file says 4222; the test must not claim it
	opts.NoLog, opts.NoSigs = true, true
	srv, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("server did not start")
	}
	t.Cleanup(func() { srv.Shutdown(); srv.WaitForShutdown() })
	return srv
}

func TestGeneratedServerWorks(t *testing.T) {
	s := newSetup(t)
	s.generate(t)
	srv := startInProcess(t, s.pl.ConfPath())

	nc, err := nats.Connect(srv.ClientURL(), nats.Timeout(15*time.Second), natsauth.UserCredentials(s.pl.Creds), nats.RootCAs(s.pl.CA))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := js.AccountInfo(ctx); err != nil {
		t.Fatalf("JetStream is not enabled for the MASH account: %v", err)
	}

	a, err := admin.New(nc, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	ukp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	upub, _ := ukp.PublicKey()
	if _, err := a.Provision(ctx, sap.Address{Owner: "alice", Group: "g", Instance: "coder"}, upub, s.pl.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}

	sub, err := nc.SubscribeSync("hello.local")
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Publish("hello.local", []byte("hi")); err != nil {
		t.Fatal(err)
	}
	if m, err := sub.NextMsg(5 * time.Second); err != nil || string(m.Data) != "hi" {
		t.Fatalf("pub/sub: %v", err)
	}

	sys, err := nats.Connect(srv.ClientURL(), nats.Timeout(15*time.Second), natsauth.UserCredentials(filepath.Join(s.serverDir, SysCreds)), nats.RootCAs(s.pl.CA))
	if err != nil {
		t.Fatalf("the sys user cannot connect: %v", err)
	}
	sys.Close()
}

func TestSecondRunRefusesAndChangesNothing(t *testing.T) {
	s := newSetup(t)
	s.generate(t)
	before := snapshot(t, s.root)
	_, err := Build(s.params(), s.ks)
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("second run: %v", err)
	}
	if after := snapshot(t, s.root); len(after) != len(before) {
		t.Fatalf("files changed: %d -> %d", len(before), len(after))
	} else {
		for k, v := range before {
			if after[k] != v {
				t.Errorf("%s changed", k)
			}
		}
	}
}

func TestPartialDirectoryRefused(t *testing.T) {
	s := newSetup(t)
	if err := os.MkdirAll(s.serverDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.serverDir, SysCreds), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(s.params(), s.ks); err == nil || !strings.Contains(err.Error(), SysCreds) {
		t.Fatalf("expected a refusal naming %s: %v", SysCreds, err)
	}
	if b, _ := os.ReadFile(filepath.Join(s.serverDir, SysCreds)); string(b) != "mine" {
		t.Fatal("existing file was touched")
	}
	if _, err := os.Stat(s.keyDir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("keys were created before the refusal")
	}
}

func TestModes(t *testing.T) {
	s := newSetup(t)
	s.generate(t)
	for p := range snapshot(t, s.root) {
		fi, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		if p == s.root || p == filepath.Join(s.root, "cfg") { // made by the test and MkdirAll's parents
			continue
		}
		want := os.FileMode(0o600)
		if fi.IsDir() {
			want = 0o700
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s has mode %v, want %v", p, fi.Mode().Perm(), want)
		}
	}
}

func TestConfigIsWritten(t *testing.T) {
	s := newSetup(t)
	s.generate(t)
	if _, err := os.Stat(filepath.Join(filepath.Dir(s.cfgPath), "policy.yaml")); err == nil {
		t.Fatal("a policy file was written: the policy file is gone")
	}
	cfg, err := os.ReadFile(s.cfgPath)
	if err != nil || !strings.Contains(string(cfg), `"tls://127.0.0.1:4222"`) || !strings.Contains(string(cfg), `"owner": "alice"`) || !strings.Contains(string(cfg), s.pl.Creds) || !strings.Contains(string(cfg), `"ca": "`+filepath.Join(s.serverDir, "ca.pem")+`"`) || strings.Contains(string(cfg), "insecure_loopback") {
		t.Fatalf("config.json: %s %v", cfg, err)
	}
	// The written config.json is one mash loads.
	env := func(k string) string {
		if k == "MASH_CONFIG_DIR" {
			return filepath.Dir(s.cfgPath)
		}
		return ""
	}
	c, err := config.Load(config.LookupFromFunc(env), "")
	if err != nil || c.AdminCreds != s.pl.Creds || c.URL != s.pl.URL || c.TLSCA != s.pl.CA {
		t.Fatalf("config.Load: %+v %v", c, err)
	}
}

func TestExistingConfigUntouched(t *testing.T) {
	s := newSetup(t)
	if err := os.MkdirAll(filepath.Dir(s.cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	const mine = "{\"servers\": \"tls://elsewhere:4222\", \"endpoint\": \"bob.g.a\"}\n"
	if err := os.WriteFile(s.cfgPath, []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	s.generate(t)
	if s.pl.Config != nil {
		t.Fatal("plan would write over existing files")
	}
	if b, _ := os.ReadFile(s.cfgPath); string(b) != mine {
		t.Fatal("config.json changed")
	}
}

func TestBuildValidates(t *testing.T) {
	s := newSetup(t)
	for _, p := range []Params{
		{Dir: "relative", Owner: "alice", Port: 4222},
		{Dir: s.serverDir, Owner: "Bad Name", Port: 4222},
		{Dir: s.serverDir, Owner: "alice", Port: 0},
		{Dir: s.serverDir, Owner: "alice", Port: 70000},
	} {
		if _, err := Build(p, s.ks); err == nil {
			t.Errorf("accepted %+v", p)
		}
	}
}

func TestInitReportsJetStreamThatCouldNotBeEnabled(t *testing.T) {
	s := newSetup(t)
	s.generate(t)
	opts, err := server.ProcessConfigFile(s.pl.ConfPath())
	if err != nil {
		t.Fatal(err)
	}
	opts.Port = -1
	opts.NoLog, opts.NoSigs = true, true
	opts.JetStreamMaxStore = testDisk / 2 // under the account's own limit
	srv, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("server did not start")
	}
	t.Cleanup(func() { srv.Shutdown(); srv.WaitForShutdown() })
	nc, err := nats.Connect(srv.ClientURL(), nats.Timeout(15*time.Second), natsauth.UserCredentials(s.pl.Creds), nats.RootCAs(s.pl.CA))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	a, err := admin.New(nc, "alice")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = a.Init(ctx)
	if err == nil {
		t.Fatal("Init succeeded without JetStream")
	}
	for _, want := range []string{"could not enable JetStream", "75% of the free disk", "nsc edit account NAME --js-disk-storage 1G"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if !admin.IsJetStreamNotEnabled(err) {
		t.Errorf("the original error is not reachable: %v", err)
	}
}

// accountDisk is the JetStream disk limit in the account JWT that a plan preloads into server.conf.
func accountDisk(t *testing.T, pl *Plan) int64 {
	t.Helper()
	var conf string
	for _, f := range pl.Files {
		if f.Path == pl.ConfPath() {
			conf = string(f.Data)
		}
	}
	i := strings.Index(conf, pl.Account+": ")
	if i < 0 {
		t.Fatal("no account in server.conf")
	}
	tok := strings.Trim(strings.Fields(conf[i+len(pl.Account)+2:])[0], `"'`)
	c, err := jwt.DecodeAccountClaims(tok)
	if err != nil {
		t.Fatal(err)
	}
	return c.Limits.JetStreamLimits.DiskStorage
}

func TestJetStreamDiskLimit(t *testing.T) {
	s := newSetup(t)
	p := s.params()
	pl, err := Build(p, s.ks)
	if err != nil {
		t.Fatal(err)
	}
	if got := accountDisk(t, pl); got != testDisk {
		t.Fatalf("account disk limit %d, want %d", got, int64(testDisk))
	}
	p.JetStreamDisk = 0
	p.Dir = filepath.Join(s.root, "default")
	pl, err = Build(p, s.ks)
	if err != nil {
		t.Fatal(err)
	}
	if got := accountDisk(t, pl); got != 2<<30 {
		t.Fatalf("default account disk limit %d, want 2 GiB", got)
	}
}

func loadCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := pem.Decode(b)
	if blk == nil || blk.Type != "CERTIFICATE" {
		t.Fatalf("%s is not a certificate", path)
	}
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestServerCertificateVerifiesAgainstTheCA(t *testing.T) {
	s := newSetup(t)
	s.generate(t)
	ca, srv := loadCert(t, s.pl.CA), loadCert(t, filepath.Join(s.serverDir, ServerCert))
	if !ca.IsCA || ca.Subject.CommonName != "mash local CA" || ca.KeyUsage != x509.KeyUsageCertSign|x509.KeyUsageCRLSign {
		t.Errorf("CA: %+v", ca)
	}
	if d := ca.NotAfter.Sub(ca.NotBefore); d < 9*365*24*time.Hour {
		t.Errorf("CA validity %v", d)
	}
	if d := srv.NotAfter.Sub(srv.NotBefore); d < 9*365*24*time.Hour {
		t.Errorf("server certificate validity %v", d)
	}
	if ca.SerialNumber.Cmp(srv.SerialNumber) == 0 || ca.SerialNumber.BitLen() < 64 {
		t.Errorf("serials: %v %v", ca.SerialNumber, srv.SerialNumber)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	for _, name := range []string{"localhost", "127.0.0.1", "::1"} {
		if _, err := srv.Verify(x509.VerifyOptions{Roots: pool, DNSName: name}); err != nil {
			t.Errorf("verify for %s: %v", name, err)
		}
	}
	if _, err := srv.Verify(x509.VerifyOptions{Roots: pool, DNSName: "example.com"}); err == nil {
		t.Error("certificate verifies for example.com")
	}
	for _, n := range []string{CACert, CAKey, ServerCert, ServerKey} {
		fi, err := os.Stat(filepath.Join(s.serverDir, n))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", n, fi, err)
		}
	}
	for _, n := range []string{CAKey, ServerKey} {
		b, _ := os.ReadFile(filepath.Join(s.serverDir, n))
		if blk, _ := pem.Decode(b); blk == nil || blk.Type != "PRIVATE KEY" {
			t.Errorf("%s is not a PKCS8 PRIVATE KEY", n)
		}
	}
}

func TestPreflightRefusesCertificateFiles(t *testing.T) {
	for _, n := range []string{CACert, CAKey, ServerCert, ServerKey} {
		dir := filepath.Join(t.TempDir(), "server")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, n), []byte("mine"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := Preflight(dir); err == nil || !strings.Contains(err.Error(), n) {
			t.Errorf("%s: %v", n, err)
		}
	}
}

func TestServerCertificateHandshake(t *testing.T) {
	s := newSetup(t)
	s.generate(t)
	cert, err := tls.LoadX509KeyPair(filepath.Join(s.serverDir, ServerCert), filepath.Join(s.serverDir, ServerKey))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _ = c.(*tls.Conn).Handshake(); c.Close() }()
		}
	}()
	pool := x509.NewCertPool()
	pool.AddCert(loadCert(t, s.pl.CA))
	c, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{RootCAs: pool, ServerName: "127.0.0.1"})
	if err != nil {
		t.Fatalf("handshake with the CA trusted: %v", err)
	}
	c.Close()
	if c, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{RootCAs: x509.NewCertPool(), ServerName: "127.0.0.1"}); err == nil {
		c.Close()
		t.Error("handshake succeeded without trusting the CA")
	}
}
