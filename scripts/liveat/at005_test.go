package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAT5CertificateChain(t *testing.T) {
	now := time.Now()
	ca, err := NewCA(now)
	if err != nil {
		t.Fatal(err)
	}
	var leaves []*Leaf
	for _, n := range []string{"mash-n1", "mash-n2", "mash-n3"} {
		l, err := ca.Issue(n, now, SANsFor("127.0.0.1")...)
		if err != nil {
			t.Fatal(err)
		}
		if err := ca.Verify(l, now, "127.0.0.1", "localhost"); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if d := l.Cert.NotAfter.Sub(l.Cert.NotBefore); d > certValidity+2*time.Minute || d < certValidity {
			t.Fatalf("validity %s", d)
		}
		if len(l.Cert.IPAddresses) != 1 || l.Cert.IPAddresses[0].String() != "127.0.0.1" || len(l.Cert.DNSNames) != 1 || l.Cert.DNSNames[0] != "localhost" {
			t.Fatalf("SANs: %v %v", l.Cert.IPAddresses, l.Cert.DNSNames)
		}
		leaves = append(leaves, l)
	}
	if leaves[0].Cert.SerialNumber.Cmp(leaves[1].Cert.SerialNumber) == 0 || string(leaves[0].KeyPEM) == string(leaves[1].KeyPEM) {
		t.Fatal("leaves are not distinct")
	}
	if err := ca.Verify(leaves[0], now.Add(48*time.Hour), "127.0.0.1"); err == nil {
		t.Fatal("a leaf past its validity verifies")
	}
	other, _ := NewCA(now)
	if err := other.Verify(leaves[0], now, "127.0.0.1"); err == nil {
		t.Fatal("a leaf verifies under another CA")
	}
	// The record text carries no private key and no certificate private part.
	lines := strings.Join(TLSDescribe(ca, leaves), "\n")
	if err := noPrivate(lines); err != nil {
		t.Fatal(err)
	}
	for _, l := range leaves {
		if strings.Contains(lines, strings.TrimSpace(string(l.KeyPEM))) || strings.Contains(lines, "BEGIN") {
			t.Fatal("record text holds PEM")
		}
	}
	if noPrivate(string(leaves[0].KeyPEM)) == nil {
		t.Fatal("noPrivate does not see a key")
	}
}

func TestAT5SANsFollowTheHostAddress(t *testing.T) {
	now := time.Now()
	ca, _ := NewCA(now)
	l, err := ca.Issue("n", now, SANsFor("10.89.7.12")...)
	if err != nil {
		t.Fatal(err)
	}
	if err := ca.Verify(l, now, "10.89.7.12"); err != nil {
		t.Fatal(err)
	}
	if len(l.Cert.DNSNames) != 0 || len(l.Cert.IPAddresses) != 1 || l.Cert.IPAddresses[0].String() != "10.89.7.12" {
		t.Fatalf("SANs: %v %v", l.Cert.IPAddresses, l.Cert.DNSNames)
	}
	if ca.Verify(l, now, "127.0.0.1") == nil {
		t.Fatal("a node certificate covers loopback")
	}
	if _, err := ca.Issue("n", now); err == nil {
		t.Fatal("a certificate without a SAN was issued")
	}
}

func TestAT5WriteTLSModes(t *testing.T) {
	now := time.Now()
	ca, _ := NewCA(now)
	l, _ := ca.Issue("n", now, "127.0.0.1")
	h := fakeHosts(t)[0]
	f, err := WriteTLS(h, ca, l)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{f.CA, f.Cert, f.Key} {
		st, err := os.Stat(p)
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", p, st, err)
		}
	}
	b, _ := os.ReadFile(f.CA)
	if strings.Contains(string(b), "PRIVATE") {
		t.Fatal("the CA file holds a private key")
	}
}

const tmplConf = `# template
server_name: mash-local
listen: 127.0.0.1:14222
operator: "/x/operator.jwt"
system_account: ADSYS
resolver: {
  type: full
  dir: "/x/resolver"
  allow_delete: false
}
resolver_preload: {
  ADSYS: "jwt-one"
  ADACC: "jwt-two"
}
jetstream: {
  store_dir: "/x/jetstream"
}
`

func TestAT5TemplateParts(t *testing.T) {
	sys, pre, err := templateParts(tmplConf)
	if err != nil || sys != "ADSYS" {
		t.Fatalf("%q %v", sys, err)
	}
	if !strings.HasPrefix(pre, "resolver_preload: {") || !strings.Contains(pre, "ADACC") || !strings.HasSuffix(pre, "}\n") || strings.Contains(pre, "jetstream") {
		t.Fatalf("preload %q", pre)
	}
	if _, _, err := templateParts("listen: 1"); err == nil {
		t.Fatal("no error for a template without a system account")
	}
}

func fakeHosts(t *testing.T) []Host {
	t.Helper()
	r := &Run{Dir: t.TempDir(), Mash: "/b/mash", Mashctl: "/b/mashctl", NatsBin: "/b/nats-server", Runner: &Runner{}}
	hs, err := Local{}.Open(r)
	if err != nil {
		t.Fatal(err)
	}
	return hs
}

func TestAT5ConfigRendering(t *testing.T) {
	hs := fakeHosts(t)
	tls := make([]TLSFiles, 3)
	for i := range tls {
		d := hs[i].TLSDir()
		tls[i] = TLSFiles{CA: d + "/ca.pem", Cert: d + "/cert.pem", Key: d + "/key.pem"}
	}
	ns := Nodes(hs, tls)
	sys, pre, _ := templateParts(tmplConf)
	names, clients, routeDirs := map[string]bool{}, map[int]bool{}, map[string]bool{}
	for i, n := range ns {
		c := RenderServerConf(n, sys, pre)
		names[n.Name] = true
		clients[n.Client] = true
		routeDirs[n.StoreDir] = true
		for _, want := range []string{
			"server_name: mash-n" + string(rune('1'+i)), "system_account: ADSYS", "ADACC: \"jwt-two\"", "type: full", "interval: \"5s\"",
			"tls: {", "verify: true", "name: mashc", "ca_file: ", "cert_file: ", "key_file: ", "jetstream: {", "operator: ",
		} {
			if !strings.Contains(c, want) {
				t.Errorf("node %d: missing %q", i+1, want)
			}
		}
		if (i == 0) != strings.Contains(c, "http: 127.0.0.1:18222") {
			t.Errorf("node %d: monitor line wrong", i+1)
		}
		if strings.Count(c, "nats-route://") != 2 || strings.Contains(c, "nats-route://127.0.0.1:"+itoa(n.Route)) {
			t.Errorf("node %d: routes %s", i+1, c)
		}
		if strings.Contains(c, "PRIVATE") || strings.Contains(c, "BEGIN") {
			t.Errorf("node %d: conf holds key material", i+1)
		}
		if !strings.Contains(c, hs[i].ServerDir()) || !strings.Contains(c, hs[i].TLSDir()) {
			t.Errorf("node %d: not its own directories", i+1)
		}
	}
	if len(names) != 3 || len(clients) != 3 || len(routeDirs) != 3 {
		t.Fatalf("names/ports/stores not distinct: %v %v %v", names, clients, routeDirs)
	}
	if ns[0].Client != 14222 || ns[1].Client != 14223 || ns[2].Client != 14224 || ns[2].Route != 16224 {
		t.Fatalf("ports are not the reserved set: %+v", ns)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestAT5HostAbstraction(t *testing.T) {
	hs := fakeHosts(t)
	if len(hs) != 3 {
		t.Fatal(len(hs))
	}
	seen := map[string]bool{}
	for i, h := range hs {
		if h.Role() != "M"+string(rune('1'+i)) {
			t.Errorf("role %s", h.Role())
		}
		for _, d := range []string{h.Home().Dir, h.ServerDir(), h.TLSDir()} {
			st, err := os.Stat(d)
			if err != nil || st.Mode().Perm() != 0o700 {
				t.Errorf("%s: %v %v", d, st, err)
			}
			if seen[d] {
				t.Errorf("%s is shared", d)
			}
			seen[d] = true
		}
		if h.Home().Config == h.Home().Keys || h.Addr() != "127.0.0.1" || h.Facts() == "" {
			t.Errorf("home/addr: %+v", h.Home())
		}
		if h.Bin("mash") != "/b/mash" || h.Bin("mashctl") != "/b/mashctl" || h.Bin("nats-server") != "/b/nats-server" {
			t.Error("bin paths")
		}
	}
	// Put makes private files; Remove takes exactly one path and refuses a relative one.
	p := filepath.Join(hs[1].Home().Dir, "x", "f")
	if err := hs[1].Put(p, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := hs[1].Get(p); err != nil || string(b) != "a" {
		t.Fatal("get")
	}
	if err := hs[1].Remove("relative/path"); err == nil {
		t.Fatal("Remove accepted a relative path")
	}
	if err := hs[1].Remove(filepath.Dir(p)); err == nil {
		t.Fatal("Remove removed a non-empty directory")
	}
	if err := hs[1].Remove(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(p)); err != nil {
		t.Fatal("Remove took more than the one path")
	}
}

func TestAT5SSHIsAStub(t *testing.T) {
	hs, err := SSH{Names: []string{"a", "b", "c"}}.Open(&Run{})
	if hs != nil || !errors.Is(err, errSSHNotConfigured) || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("%v %v", hs, err)
	}
	var _ Hosts = SSH{}
	var _ Hosts = Local{}
}

func TestAT5Helpers(t *testing.T) {
	if got := snip("{\n  \"outcome\": \"error\",\n  \"refusal\": {\"code\": \"enroll\", \"detail\": \"x\"}\n}"); got != "refusal enroll: x" {
		t.Fatal(got)
	}
	if got := redact(snip("SU" + strings.Repeat("A", 54))); strings.Contains(got, "SUAAAA") {
		t.Fatal("snip leaked")
	}
	if refusalCode5(`{"refusal":{"code":"address_taken"}}`) != "address_taken" || refusalCode5(`{"refusal":null}`) != "" {
		t.Fatal("refusalCode5")
	}
	if ip := primaryIPv4(); ip != "" && strings.HasPrefix(ip, "127.") {
		t.Fatal(ip)
	}
	if realMain([]string{"at005", "--bogus"}, discard{}, discard{}) != 2 {
		t.Fatal("at005 usage is not exit 2")
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func TestHostLineNamesOSKernelAndArch(t *testing.T) {
	l := hostLine()
	if !strings.HasPrefix(l, runtime.GOOS+", kernel ") || !strings.HasSuffix(l, ", "+runtime.GOARCH) {
		t.Fatalf("host line %q", l)
	}
}

func TestShowRefusalAssertsTheCode(t *testing.T) {
	good := `{"schema":"x","outcome":"refused","refusal":{"code":"not_found","detail":"no directory record"}}`
	for _, c := range []struct {
		name   string
		code   int
		out    string
		wantOK bool
	}{
		{"not_found", 1, good, true},
		{"other code", 1, `{"outcome":"refused","refusal":{"code":"unavailable","detail":"x"}}`, false},
		{"exit 1, no JSON", 1, "", false},
		{"exit 1, text only", 1, "mashctl: endpoint show: revoked", false},
		{"exit 0 with the document", 0, good, false},
		{"exit 2", 2, good, false},
		{"found endpoint", 1, `{"outcome":"ok"}`, false},
		{"refusal is a bare string", 1, `{"outcome":"refused","refusal":"not_found"}`, false},
		{"refused without refusal", 1, `{"outcome":"refused"}`, false},
	} {
		if ok, d := showRefusal(c.code, c.out); ok != c.wantOK {
			t.Errorf("%s: got %v (%s), want %v", c.name, ok, d, c.wantOK)
		}
	}
}

func TestIssuedCheckCleanAssertsTheFields(t *testing.T) {
	doc := func(clean, complete, rec, revoked string) string {
		return `{"clean":` + clean + `,"complete":` + complete + `,"record_present":` + rec + `,"revoked_connected":` + revoked + `}`
	}
	for _, c := range []struct {
		name   string
		code   int
		out    string
		wantOK bool
	}{
		{"clean", 0, doc("true", "true", "true", "[]"), true},
		{"exit 1 finding", 1, doc("false", "true", "true", `[{"address":"alice.dev.review"}]`), false},
		{"exit 2 incomplete", 2, doc("false", "false", "true", "[]"), false},
		{"exit 0 but revoked connected", 0, doc("true", "true", "true", `[{"address":"a"}]`), false},
		{"not clean", 0, doc("false", "true", "true", "[]"), false},
		{"not complete", 0, doc("true", "false", "true", "[]"), false},
		{"no record", 0, doc("true", "true", "false", "[]"), false},
		{"field missing", 0, `{"clean":true,"complete":true,"revoked_connected":[]}`, false},
		{"no JSON", 0, "all clean", false},
	} {
		if ok, d := issuedCheckClean(c.code, c.out); ok != c.wantOK {
			t.Errorf("%s: got %v (%s), want %v", c.name, ok, d, c.wantOK)
		}
	}
}

func TestIssuedListRevokedNeedsTheEntry(t *testing.T) {
	list := func(addr, at string) string {
		return `[{"address":"` + addr + `","revoked_at":"` + at + `"}]`
	}
	for _, c := range []struct {
		name   string
		code   int
		out    string
		wantOK bool
	}{
		{"revoked", 0, list("alice.dev.review", "2026-11-18T10:00:00Z"), true},
		{"entry absent", 0, list("alice.dev.design", "2026-11-18T10:00:00Z"), false},
		{"empty list", 0, "[]", false},
		{"not revoked", 0, list("alice.dev.review", ""), false},
		{"only another entry revoked", 0, `[{"address":"alice.dev.review","revoked_at":""},{"address":"alice.dev.x","revoked_at":"t"}]`, false},
		{"exit 1", 1, list("alice.dev.review", "t"), false},
		{"not JSON", 0, "nothing", false},
	} {
		if ok, d := issuedListRevoked(c.code, c.out, "alice.dev.review"); ok != c.wantOK {
			t.Errorf("%s: got %v (%s), want %v", c.name, ok, d, c.wantOK)
		}
	}
}

func TestIssuedRevocationAccountedNeedsTheTarget(t *testing.T) {
	const addr = "alice.dev.review"
	list := func(a, key, at string) string {
		return `[{"address":"` + a + `","public_key":"` + key + `","revoked_at":"` + at + `"}]`
	}
	check := func(entries string) string {
		return `{"clean":true,"complete":true,"record_present":true,"revoked_connected":[],"recorded_not_connected":` + entries + `}`
	}
	entry := func(a, key string) string { return `[{"address":"` + a + `","public_key":"` + key + `"}]` }
	rec := func(key, at string) string {
		return `{"address":"` + addr + `","public_key":"` + key + `","revoked_at":"` + at + `"}`
	}
	entries := func(keys ...string) string {
		var parts []string
		for _, k := range keys {
			parts = append(parts, `{"address":"`+addr+`","public_key":"`+k+`"}`)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	for _, c := range []struct {
		name      string
		list, chk string
		wantOK    bool
	}{
		{"reissue: old revoked, new unrevoked, only the new key listed", "[" + rec("OLD", "t") + "," + rec("NEW", "") + "]", check(entries("NEW")), false},
		{"reissue: the revoked key listed", "[" + rec("OLD", "t") + "," + rec("NEW", "") + "]", check(entries("OLD")), true},
		{"reissue: new unrevoked first in the list, only new key listed", "[" + rec("NEW", "") + "," + rec("OLD", "t") + "]", check(entries("NEW")), false},
		{"two revoked, only one listed", "[" + rec("A", "t") + "," + rec("B", "t") + "]", check(entries("A")), false},
		{"two revoked, both listed", "[" + rec("A", "t") + "," + rec("B", "t") + "]", check(entries("A", "B")), true},
		{"no revoked record", "[" + rec("A", "") + "]", check(entries("A")), false},
		{"present and revoked", list(addr, "AK1", "2026-11-18T10:00:00Z"), check(entry(addr, "AK1")), true},
		{"target missing from the check report", list(addr, "AK1", "t"), check("[]"), false},
		{"check lists another key for the address", list(addr, "AK1", "t"), check(entry(addr, "AK2")), false},
		{"check lists only another address", list(addr, "AK1", "t"), check(entry("alice.dev.design", "AK1")), false},
		{"present but not revoked", list(addr, "AK1", ""), check(entry(addr, "AK1")), false},
		{"target missing from the list", list("alice.dev.design", "AK9", "t"), check(entry(addr, "AK1")), false},
		{"list without a key", `[{"address":"` + addr + `","revoked_at":"t"}]`, check(entry(addr, "AK1")), false},
	} {
		if ok, d := issuedRevocationAccounted(0, c.list, 0, c.chk, addr); ok != c.wantOK {
			t.Errorf("%s: got %v (%s), want %v", c.name, ok, d, c.wantOK)
		}
	}
	if ok, _ := issuedRevocationAccounted(0, list(addr, "AK1", "t"), 1, check(entry(addr, "AK1")), addr); ok {
		t.Error("a failing issued check passed")
	}
}
