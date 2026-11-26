package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/sapsdk"
	"github.com/bogworks/mash/internal/testenv"
)

// The S5 switch is set by the shipped binary at startup, not by a test (#1853). enableVerifiedDiscovery is what main
// calls first; this test process does not go through main, so it starts with the switch off.
func TestEnableVerifiedDiscoverySetsTheSwitch(t *testing.T) {
	if endpoint.VerifiedDiscoveryEnabled() {
		t.Skip("the switch is already set in this process")
	}
	enableVerifiedDiscovery()
	t.Cleanup(func() { endpoint.DisableVerifiedDiscoveryForTest() })
	if !endpoint.VerifiedDiscoveryEnabled() {
		t.Fatal("enableVerifiedDiscovery left the S5 switch off")
	}
}

// forgedAnswer is a $SRV.INFO answer that names bob's prompt subject and fails verification: "unsigned" has no id_sig,
// "wrong-key" is validly signed, by carol's key, for bob's address.
func forgedAnswer(t *testing.T, kind string, carolCreds string, bob sap.Address) []byte {
	t.Helper()
	subj, err := bob.Subjects()
	if err != nil {
		t.Fatal(err)
	}
	id, err := sapsdk.IdentityFromCredsAt(carolCreds, mustReadFile(t, carolCreds))
	if err != nil {
		t.Fatal(err)
	}
	md := map[string]string{"agent": sap.AgentName, "owner": bob.Owner, "group": bob.Group, "instance": bob.Instance,
		"protocol_version": sap.ProtocolVersion, "user_nkey": id.User, "account": id.Account, "mail_subject": subj.Mail}
	if kind == "wrong-key" {
		if md["id_sig"], err = id.IDSig(sap.AgentName, bob.Owner, subj.Prompt); err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(map[string]any{"name": sap.ServiceName, "id": "forged", "type": "io.nats.micro.v1.info_response",
		"version": "0.0.0", "metadata": md,
		"endpoints": []map[string]any{{"name": "prompt", "subject": subj.Prompt}}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustReadFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type whoJSON struct {
	Endpoints []struct {
		Address string `json:"address"`
		Online  bool   `json:"online"`
	} `json:"endpoints"`
	AnswersNotVerified *int `json:"answers_not_verified"`
}

// The built binary, not the hook: `mash who --json` is run as a process with a directory of alice, bob and carol, nobody
// serving, and a peer (carol's connection) that answers the discovery request with a registration that fails
// verification and names bob. Nothing in this test process sets the S5 switch; only main of the binary does. The forged
// answer is counted and bob is not shown online; with no forged answer the count is 0.
func TestBuiltBinaryWhoCountsForgedDiscoveryAnswers(t *testing.T) {
	bin := mashBinary(t)
	e := testenv.Start(t)
	adm := standaloneAdmin(t, e)
	creds := map[string]string{}
	var aliceEnv []string
	for _, n := range []string{"alice", "bob", "carol"} {
		c, user, err := e.MintEndpoint(tAddr(n))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := adm.Provision(context.Background(), tAddr(n), user, e.Account, "", admin.Meta{}); err != nil {
			t.Fatal(err)
		}
		creds[n] = c
		if n == "alice" {
			aliceEnv = scrubbedEnv(t, map[string]string{"MASH_SERVER": e.URL, "MASH_ENDPOINT": e.Owner + "." + testGroup + "." + n,
				"MASH_CREDS": c, "MASH_SETTLE_MS": "20"})
		}
	}

	who := func(t *testing.T) whoJSON {
		t.Helper()
		out, errOut, code := mashRunSplit(t, bin, aliceEnv, "", "who", "--json")
		if code != 0 {
			t.Fatalf("mash who --json: exit %d, stdout %q, stderr %q", code, out, errOut)
		}
		var w whoJSON
		if err := json.Unmarshal([]byte(out), &w); err != nil {
			t.Fatalf("who --json is not JSON: %v: %q", err, out)
		}
		if w.AnswersNotVerified == nil {
			t.Fatalf("who --json carries no answers_not_verified: %q", out)
		}
		return w
	}
	online := func(w whoJSON, name string) bool {
		for _, ep := range w.Endpoints {
			if ep.Address == tAddr(name).String() {
				return ep.Online
			}
		}
		t.Fatalf("who --json lists no %s: %+v", name, w.Endpoints)
		return false
	}

	t.Run("no-forged-answer", func(t *testing.T) {
		w := who(t)
		if *w.AnswersNotVerified != 0 || online(w, "bob") {
			t.Fatalf("answers_not_verified %d, bob online %v; want 0 and offline", *w.AnswersNotVerified, online(w, "bob"))
		}
	})
	for _, kind := range []string{"unsigned", "wrong-key"} {
		t.Run(kind, func(t *testing.T) {
			data := forgedAnswer(t, kind, creds["carol"], tAddr("bob"))
			nc := e.ConnAs(t, creds["carol"], tAddr("carol").Flat())
			sub, err := nc.Subscribe("$SRV.INFO."+sap.ServiceName, func(m *nats.Msg) {
				if m.Reply != "" {
					_ = nc.Publish(m.Reply, data)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := nc.FlushTimeout(5 * time.Second); err != nil {
				t.Fatal(err)
			}
			defer sub.Unsubscribe()
			w := who(t)
			if *w.AnswersNotVerified < 1 {
				t.Fatalf("answers_not_verified %d for a %s answer naming bob; want at least 1 (the binary did not verify)", *w.AnswersNotVerified, kind)
			}
			if online(w, "bob") {
				t.Fatalf("bob is shown online on the strength of a %s answer", kind)
			}
		})
	}
}
