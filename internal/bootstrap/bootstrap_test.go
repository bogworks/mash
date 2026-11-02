package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/policy"
)

type setup struct {
	root, serverDir, keyDir, policy, cfgPath string
	ks                                       *ctlkeys.Authority
	pl                                       *Plan
}

func newSetup(t *testing.T) *setup {
	t.Helper()
	root := t.TempDir()
	s := &setup{root: root, serverDir: filepath.Join(root, "cfg", "server"), keyDir: filepath.Join(root, "keys"),
		policy: filepath.Join(root, "cfg", "policy.yaml"), cfgPath: filepath.Join(root, "cfg", "config.json")}
	ks, err := ctlkeys.Open(s.keyDir)
	if err != nil {
		t.Fatal(err)
	}
	s.ks = ks
	return s
}

func (s *setup) params() Params {
	return Params{Dir: s.serverDir, Owner: "alice", Port: 4222, PolicyPath: s.policy, ConfigPath: s.cfgPath}
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

	nc, err := nats.Connect(srv.ClientURL(), natsauth.UserCredentials(s.pl.Creds))
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
	if _, err := a.Provision(ctx, "coder", upub, s.pl.Account, "", admin.Meta{}); err != nil {
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

	sys, err := nats.Connect(srv.ClientURL(), natsauth.UserCredentials(filepath.Join(s.serverDir, SysCreds)))
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

func TestPolicyAndConfig(t *testing.T) {
	s := newSetup(t)
	s.generate(t)
	b, err := os.ReadFile(s.policy)
	if err != nil {
		t.Fatal(err)
	}
	f, err := policy.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if r := f.Check(); len(r.Errors)+len(r.Warnings) != 0 {
		t.Fatalf("policy does not check clean: %v", r)
	}
	cfg, err := os.ReadFile(s.cfgPath)
	if err != nil || !strings.Contains(string(cfg), `"nats://127.0.0.1:4222"`) || !strings.Contains(string(cfg), `"owner": "alice"`) || !strings.Contains(string(cfg), s.pl.Creds) {
		t.Fatalf("config.json: %s %v", cfg, err)
	}
	// The written config.json is one mash loads.
	env := func(k string) string {
		if k == "MASH_CONFIG_DIR" {
			return filepath.Dir(s.cfgPath)
		}
		return ""
	}
	c, err := config.Load(env, "")
	if err != nil || c.AdminCreds != s.pl.Creds || c.Owner != "alice" {
		t.Fatalf("config.Load: %+v %v", c, err)
	}
}

func TestExistingPolicyAndConfigUntouched(t *testing.T) {
	s := newSetup(t)
	if err := os.MkdirAll(filepath.Dir(s.cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	const mine = "{\"url\": \"tls://elsewhere:4222\", \"owner\": \"bob\"}\n"
	if err := os.WriteFile(s.cfgPath, []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.policy, []byte("default: deny\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.generate(t)
	if s.pl.Config != nil || s.pl.Policy != nil {
		t.Fatal("plan would write over existing files")
	}
	if b, _ := os.ReadFile(s.cfgPath); string(b) != mine {
		t.Fatal("config.json changed")
	}
	if b, _ := os.ReadFile(s.policy); string(b) != "default: deny\n" {
		t.Fatal("policy changed")
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

func TestStartWithoutNatsServer(t *testing.T) {
	s := newSetup(t)
	s.generate(t)
	t.Setenv("PATH", "")
	pid, err := Start(s.pl.Params)
	if !errors.Is(err, ErrNoServerBinary) || pid != 0 {
		t.Fatalf("Start: %d %v", pid, err)
	}
	for _, n := range []string{ServerConf, AdminCreds, SysCreds, OperatorJWT} {
		if _, err := os.Stat(filepath.Join(s.serverDir, n)); err != nil {
			t.Errorf("%s gone: %v", n, err)
		}
	}
	for _, n := range []string{ServerLog, ServerPID} {
		if _, err := os.Stat(filepath.Join(s.serverDir, n)); err == nil {
			t.Errorf("%s was created though nothing started", n)
		}
	}
}

// fakeNatsServer puts a nats-server shell script with the given body first on PATH.
func fakeNatsServer(t *testing.T, body string) {
	t.Helper()
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "nats-server"), []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", d+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestStartRefusesOccupiedPort(t *testing.T) {
	s := newSetup(t)
	s.generate(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
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
			c.Close()
		}
	}()
	fakeNatsServer(t, "touch "+filepath.Join(s.root, "launched")+"; sleep 5")
	p := s.pl.Params
	p.Port = ln.Addr().(*net.TCPAddr).Port
	pid, err := Start(p)
	if err == nil || pid != 0 || !strings.Contains(err.Error(), "already") || !strings.Contains(err.Error(), strconv.Itoa(p.Port)) {
		t.Fatalf("Start: %d %v", pid, err)
	}
	for _, n := range []string{ServerLog, ServerPID} {
		if _, err := os.Stat(filepath.Join(s.serverDir, n)); err == nil {
			t.Errorf("%s was created though nothing started", n)
		}
	}
	if _, err := os.Stat(filepath.Join(s.root, "launched")); err == nil {
		t.Error("a server was launched")
	}
}

func TestStartChildExitedReportsLog(t *testing.T) {
	s := newSetup(t)
	s.generate(t)
	fakeNatsServer(t, "echo 'bind: address already in use' >&2; exit 1")
	p := s.pl.Params
	p.Port = freePort(t)
	pid, err := Start(p)
	if err == nil || pid != 0 || !strings.Contains(err.Error(), "server.log") || !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("Start: %d %v", pid, err)
	}
	if _, err := os.Stat(filepath.Join(s.serverDir, ServerPID)); err == nil {
		t.Error("pid file left behind")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestCheckServerName(t *testing.T) {
	if err := checkServerName(ServerName); err != nil {
		t.Error(err)
	}
	if err := checkServerName("other"); err == nil {
		t.Error("accepted another server's name")
	}
	if err := checkServerName(""); err == nil {
		t.Error("accepted an empty name")
	}
}

func TestCheckPortFree(t *testing.T) {
	if err := checkPortFree(freePort(t)); err != nil {
		t.Error(err)
	}
}
