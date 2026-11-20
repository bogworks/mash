package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/natsfixture"
)

// One address against a cluster is never a complete set, so a revocation is refused, not confirmed by one server.
func TestOneConfiguredServerNeverConfirmsACluster(t *testing.T) {
	set := apply.ServerSet{Names: []string{"s1"}, Expected: 1, Cluster: "C"}
	if set.Complete(serverCount(config.Config{URL: "tls://a:4222"})) {
		t.Fatal("one configured address confirmed a cluster")
	}
	three := apply.ServerSet{Names: []string{"s1", "s2", "s3"}, Expected: 3, Cluster: "C"}
	n := serverCount(config.Config{URL: "tls://a:4222,tls://b:4222"})
	if three.Complete(n) || !strings.Contains(three.Why(n), "the configuration lists 2 servers but the cluster reports 3: list every server under `servers` in the setup file") {
		t.Fatalf("two of three: %v %q", three.Complete(n), three.Why(n))
	}
	if n = serverCount(config.Config{URL: "tls://a:4222,tls://b:4222,tls://c:4222"}); !three.Complete(n) {
		t.Fatal("all three listed must be complete")
	}
}

func TestCheckClusterListed(t *testing.T) {
	cluster := apply.ServerSet{Names: []string{"s1", "s2", "s3"}, Expected: 3, Cluster: "C"}
	err := checkClusterListed(cluster, 1, "/x/setup.yaml")
	var ce *cliframe.Error
	if !errors.As(err, &ce) || ce.Msg != "the server is part of a cluster but the setup file lists 1 of 3 servers" || ce.Hint != "mashctl init --config /x/setup.yaml" {
		t.Fatalf("%#v", err)
	}
	if checkClusterListed(cluster, 3, "f") != nil {
		t.Fatal("all listed was refused")
	}
	if checkClusterListed(apply.ServerSet{Names: []string{"s1"}, Expected: 1}, 1, "f") != nil {
		t.Fatal("a standalone server was refused")
	}
	if checkClusterListed(apply.ServerSet{Names: []string{"s1"}, Expected: 1, Cluster: "C"}, 1, "f") == nil {
		t.Fatal("one address for an isolated cluster member was accepted")
	}
	if checkClusterListed(apply.ServerSet{Names: []string{"s1"}, Expected: 1, Gateways: true}, 1, "f") == nil {
		t.Fatal("gateways with one address was accepted")
	}
}

func TestLoadSetupNeverEchoesAnInlineSecret(t *testing.T) {
	seed := "SOAFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE"
	for _, k := range []string{"operator_key", "account_key", "system_creds", "admin_creds", "ca"} {
		for _, val := range []string{seed, "-----BEGIN PRIVATE KEY-----ZZZSECRETZZZ"} {
			vals := map[string]string{"owner": "alice", "ca": "/ca.pem", "admin_creds": "/a.creds", "system_creds": "/s.creds", "operator_key": "/o.nk", "account_key": "/k.nk"}
			vals[k] = val
			doc := "servers:\n  - tls://a:4222\n"
			for key, v := range vals {
				doc += key + ": \"" + v + "\"\n"
			}
			p := filepath.Join(t.TempDir(), "setup.yaml")
			if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := loadSetup(p, "/home/x")
			if err == nil || !strings.Contains(err.Error(), k+" holds a key, not a path") {
				t.Fatalf("%s: %v", k, err)
			}
			if strings.Contains(err.Error(), "FAKE") || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "BEGIN") {
				t.Fatalf("%s: the value was echoed: %v", k, err)
			}
		}
	}
}

func TestCheckAdminCredsRefusesAnotherAccount(t *testing.T) {
	mine, _ := nkeys.CreateAccount()
	minePub, _ := mine.PublicKey()
	other, _ := nkeys.CreateAccount()
	otherPub, _ := other.PublicKey()
	dir := t.TempDir()
	// signed by the other account's identity key (no issuer_account)
	identity, err := natsfixture.MintAdminCreds(other)
	if err != nil {
		t.Fatal(err)
	}
	// signed by a signing key on behalf of the other account (issuer_account set)
	sp := filepath.Join(dir, "signer.creds")
	signingKeyCreds(t, otherPub, other, sp)
	signer, err := os.ReadFile(sp)
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{"identity key": identity, "signing key": signer} {
		_, err := checkAdminCreds(config.Config{}, "tls://127.0.0.1:1", &initState{adminRaw: raw, acctPub: minePub})
		if err == nil || !strings.Contains(err.Error(), "admin_creds belong to another account ("+otherPub+"), not "+minePub) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestInitSettingsComeFromTheSetupFileOnly(t *testing.T) {
	cfg := config.Config{TLSCA: "/env/ca.pem", TLSCert: "/old/c.pem", TLSKey: "/old/k.pem", SkipEncryption: true}
	sf := setupFile{servers: []string{"tls://a:4222"}, owner: "alice", adminCreds: "/a.creds"}
	m := initSettings(cfg, sf, &initState{sysDst: "/s/sys.creds"})
	for _, k := range []string{"ca", "insecure_plaintext", "tls_cert", "tls_key"} {
		if _, ok := m[k]; ok {
			t.Errorf("%s was written from the environment or an old config.json", k)
		}
	}
	sf.ca = "/setup/ca.pem"
	if m = initSettings(cfg, sf, &initState{}); m["ca"] != "/setup/ca.pem" {
		t.Errorf("ca = %v", m["ca"])
	}
}

func TestReadUserCredsRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.creds")
	if err := os.WriteFile(real, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.creds")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readUserCreds(link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("%v", err)
	}
}

func TestInstallSysCredsComparesAnExistingFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "server")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	st := &initState{serverDir: dir, sysDst: filepath.Join(dir, sysCredsFile), sysCredsRaw: []byte("creds-a")}
	if err := installSysCreds(st); err != nil {
		t.Fatal(err)
	}
	if err := installSysCreds(st); err != nil { // identical: a rerun
		t.Fatalf("identical content: %v", err)
	}
	st.sysCredsRaw = []byte("creds-b")
	if err := installSysCreds(st); err == nil || !strings.Contains(err.Error(), "a different system creds") {
		t.Fatalf("different content: %v", err)
	}
	if b, _ := os.ReadFile(st.sysDst); string(b) != "creds-a" {
		t.Fatalf("the file was replaced: %q", b)
	}
}

func TestStatusRowsKeepTheConfiguredOrder(t *testing.T) {
	servers := []string{"tls://a:1", "tls://b:2", "tls://c:3", "tls://d:4", "tls://e:5"}
	delays := map[string]int{"tls://a:1": 5, "tls://b:2": 1, "tls://c:3": 4, "tls://d:4": 0, "tls://e:5": 2}
	for range 20 {
		rows := statusRows(config.Config{}, servers, func(_ config.Config, s string) statusRow {
			for range delays[s] * 100000 {
			}
			return statusRow{Server: s}
		})
		for i, r := range rows {
			if r.Server != servers[i] {
				t.Fatalf("row %d is %s, want %s", i, r.Server, servers[i])
			}
		}
	}
}
