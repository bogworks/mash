package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// auditClean waits, bounded, until `mashctl audit --strict` finds nothing, and fails with its last output otherwise.
func (h *clusterEnv) auditClean(wait time.Duration) {
	h.t.Helper()
	deadline := time.Now().Add(wait)
	var last string
	for {
		out, errb, code := h.run("audit", "--strict")
		if code == 0 {
			return
		}
		last = out + errb
		if time.Now().After(deadline) {
			h.t.Fatalf("audit did not become clean in %s: exit %d\n%s", wait, code, last)
		}
		time.Sleep(time.Second)
	}
}

type auditProblems struct {
	Problems []struct {
		What  string `json:"what"`
		Item  string `json:"item"`
		Error string `json:"error"`
	} `json:"problems"`
}

// auditJSON runs `audit --json` and returns its findings.
func (h *clusterEnv) auditJSON() (auditProblems, string, int) {
	h.t.Helper()
	out, errb, code := h.run("audit", "--json")
	var p auditProblems
	if code == 3 || json.Unmarshal([]byte(out), &p) != nil {
		h.t.Fatalf("audit --json: %d %s %s", code, out, errb)
	}
	return p, out, code
}

// connectAs connects to one node only, with a creds file, and returns the error (nil: the node accepted the credential).
func connectAs(url, creds string) error {
	nc, err := nats.Connect(url, natsauth.UserCredentials(creds), nats.Timeout(5*time.Second), nats.NoReconnect())
	if err != nil {
		return err
	}
	nc.Close()
	return nil
}

// Three nodes, replicas auto (3), MASH_SERVERS=3 and the write quorum auto (2): with one node stopped, add, mail,
// reissue and retire all work, retire reports the stopped node pending, audit reports the revocation pending, and when
// the node is back it takes the revocation from the account resolver, refuses the retired credential, and the audit
// is clean.
func TestLiveWriteQuorumOneNodeDown(t *testing.T) {
	if testing.Short() {
		t.Skip("live three-node cluster")
	}
	h := newClusterEnv(t, 3, nil)
	if h.env["MASH_SERVERS"] != "3" || h.env["MASH_WRITE_QUORUM"] != "" {
		t.Fatalf("the harness must declare 3 servers and leave write_quorum auto: %v", h.env)
	}
	if out, errb, code := h.run("init"); code != 0 {
		t.Fatalf("init: %d %s %s", code, out, errb)
	}
	if out, errb, code := h.run("add", "a"); code != 0 {
		t.Fatalf("add a: %d %s %s", code, out, errb)
	}
	h.wantAll(3, sap.InboxStream("a"))
	h.auditClean(30 * time.Second)

	// Stop one node (n3, index 2: the record of which one).
	const down = 2
	h.c.Stop(down)
	time.Sleep(3 * time.Second) // leader election

	out, errb, code := h.run("add", "b")
	if code != 0 || !strings.Contains(out, "added b") {
		t.Fatalf("add b with a node down: %d %s %s", code, out, errb)
	}
	if err := h.send("b", "mail while a node is down"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := h.receive("b", 1); len(got) != 1 || got[0] != "mail while a node is down" {
		t.Fatalf("receive: %v", got)
	}
	credsB := filepath.Join(h.dir, "b.creds")
	raw, err := os.ReadFile(credsB)
	if err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(t.TempDir(), "b-before-retire.creds") // retire moves b's creds file aside; keep a copy to try later
	if err := os.WriteFile(kept, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, errb, code := h.run("reissue", "b"); code != 0 || !strings.Contains(out, "reissued b") {
		t.Fatalf("reissue b with a node down: %d %s %s", code, out, errb)
	}
	// two revocations in one second would carry the same issued-at: let the clock move on
	time.Sleep(1100 * time.Millisecond)
	out, errb, code = h.run("retire", "b")
	if code != 0 {
		t.Fatalf("retire b with a node down: %d %s %s", code, out, errb)
	}
	t.Logf("retire with a node down:\n%s", out)
	for _, want := range []string{"retired b", "revoked in the account JWT", "pending_servers:", "will converge when they rejoin", "2 of 3 servers hold the revocation (write quorum 2)"} {
		if !strings.Contains(out, want) {
			t.Errorf("retire's output lacks %q:\n%s", want, out)
		}
	}

	// With the node still down the revocation has not reached it: audit says so, and strict fails on it.
	p, raw2, code := h.auditJSON()
	pending := 0
	for _, f := range p.Problems {
		if f.What == "revocation_pending" {
			pending++
			if !strings.Contains(f.Error, "1 of 3 servers did not answer") {
				t.Errorf("the finding does not say how many servers did not answer: %s", f.Error)
			}
		}
	}
	if pending != 1 {
		t.Fatalf("audit with the node down: %d revocation_pending finding(s), want 1: %s", pending, raw2)
	}
	if _, _, code := h.run("audit", "--strict"); code != 1 {
		t.Fatalf("audit --strict with the revocation pending: exit %d, want 1", code)
	}

	// The node comes back; its resolver takes the account JWT from the others at its next sync.
	h.c.Start(down)
	h.current(3)
	h.auditClean(90 * time.Second)
	if err := connectAs(h.c.NodeURL(down), kept); err == nil || !strings.Contains(strings.ToLower(err.Error()), "authorization") {
		t.Fatalf("the restarted node accepted (or failed other than by authorization) the retired credential: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := connectAs(h.c.NodeURL(i), kept); err == nil || !strings.Contains(strings.ToLower(err.Error()), "authorization") {
			t.Fatalf("node %d accepted the retired credential: %v", i+1, err)
		}
	}
	// a's credential still works on the restarted node
	if err := connectAs(h.c.NodeURL(down), filepath.Join(h.dir, "a.creds")); err != nil {
		t.Fatalf("a's credential on the restarted node: %v", err)
	}
}

// Two of three nodes down is below the write quorum. The real verbs stop earlier, at the JetStream state they need (a
// replicated stream with one node left has no leader), and change nothing; the revocation itself, asked of the one node
// that is left, is refused with the quorum message before anything is pushed, and the account JWT is unchanged.
func TestLiveWriteQuorumTwoNodesDown(t *testing.T) {
	if testing.Short() {
		t.Skip("live three-node cluster")
	}
	h := newClusterEnv(t, 3, nil)
	if out, errb, code := h.run("init"); code != 0 {
		t.Fatalf("init: %d %s %s", code, out, errb)
	}
	if out, errb, code := h.run("add", "a"); code != 0 {
		t.Fatalf("add a: %d %s %s", code, out, errb)
	}
	userA := h.userKeyOf("a")
	h.c.Stop(1)
	h.c.Stop(2)
	time.Sleep(3 * time.Second)

	// What the survivor's resolver holds, before.
	before := h.accountJWT()
	// the one node that is left
	h.env["MASH_URL"] = h.c.NodeURL(0)
	if _, errb, code := h.run("add", "c"); code == 0 {
		t.Fatalf("add c succeeded with two of three nodes down: %s", errb)
	}
	if _, err := os.Lstat(filepath.Join(h.dir, "c.creds")); err == nil {
		t.Fatal("a refused add wrote a creds file")
	}
	if _, errb, code := h.run("retire", "a"); code == 0 {
		t.Fatalf("retire a succeeded with two of three nodes down: %s", errb)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "a.creds")); err != nil {
		t.Fatalf("a refused retire moved the creds file: %v", err)
	}

	cfg := config.Config{URL: h.c.NodeURL(0), Owner: h.c.Owner, InsecureLoopback: true, WriteQuorum: "auto"}
	var buf bytes.Buffer
	err := revokeUserKeys(h.ctx(), cfg, func(k string) string { return h.env[k] }, 3, []string{userA}, &buf, nil)
	if err == nil || !strings.Contains(err.Error(), "1 of 3 servers answered and the write quorum is 2: a server may be down: bring it back, or set write_quorum") || !strings.Contains(err.Error(), "nothing was revoked") {
		t.Fatalf("revocation below the quorum: %v", err)
	}
	if after := h.accountJWT(); after != before {
		t.Fatal("a refused revocation changed the account JWT")
	}
	// reading the account JWT for the reissue check is refused the same way
	if _, err := revokedInAccount(h.ctx(), cfg, func(k string) string { return h.env[k] }, 3, userA); err == nil || !strings.Contains(err.Error(), "1 of 3 servers answered and the write quorum is 2") {
		t.Fatalf("read below the quorum: %v", err)
	}
}

// userKeyOf is the user public key the directory records for endpoint name.
func (h *clusterEnv) userKeyOf(name string) string {
	h.t.Helper()
	kv, err := h.js.KeyValue(h.ctx(), store.DirectoryBucket)
	if err != nil {
		h.t.Fatal(err)
	}
	e, err := kv.Get(h.ctx(), name)
	if err != nil {
		h.t.Fatal(err)
	}
	var r store.Record
	if err := json.Unmarshal(e.Value(), &r); err != nil {
		h.t.Fatal(err)
	}
	return r.User
}

// accountJWT is the MASH account's JWT as the nodes that are up answer the claims lookup (the first reply).
func (h *clusterEnv) accountJWT() string {
	h.t.Helper()
	nc := h.c.Conn(h.t, h.c.SysCreds)
	tok, err := apply.NATS{Conn: nc}.Lookup(h.ctx(), h.c.Account)
	if err != nil {
		h.t.Fatal(err)
	}
	return tok
}
