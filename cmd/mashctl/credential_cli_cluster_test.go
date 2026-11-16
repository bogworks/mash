package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/credential"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/sap"
)

// The verbs end to end on two routed servers: the endpoint makes its key (the function `mash credential key` runs), mashctl issues
// for the public key with the endpoint found in the directory, the endpoint installs and connects to the other server,
// show finds the key, revoke by public key closes the connection. The seed is in no captured output.
func TestCredentialVerbsEndToEndOnTwoServers(t *testing.T) {
	e, second := newServeEnvCluster(t, false)
	owner := e.env["MASH_OWNER"]
	adm := e.adminOnly()
	sys := e.connect(natsauth.UserCredentials(e.env["MASH_SYS_CREDS"]))
	dcfg := e.directConfig(adm, sys, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*serveWait)
	defer cancel()
	old := openCredBackend
	openCredBackend = func(config.Config, func(string) string, int) (*credBackend, error) {
		return &credBackend{Ctx: ctx, Admin: adm, Close: func() {},
			Cred: func() (credential.Config, error) { return credential.FromDirect(dcfg) }}, nil
	}
	t.Cleanup(func() { openCredBackend = old })
	var outputs []string

	// The endpoint's host: key.
	dir := t.TempDir()
	seedPath := filepath.Join(dir, "ep.seed")
	var kout, kerr bytes.Buffer
	if rc := credential.KeyCmd("mash", []string{"--out", seedPath}, &kout, &kerr); rc != 0 {
		t.Fatalf("key: %s", kerr.String())
	}
	outputs = append(outputs, kout.String(), kerr.String())
	pub := strings.TrimPrefix(strings.SplitN(kout.String(), "\n", 2)[0], "public key: ")
	rawSeed, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	seed := strings.TrimSpace(string(rawSeed))

	addr := owner + ".x"
	ident, err := sap.New(sap.AgentName, owner, "x")
	if err != nil {
		t.Fatal(err)
	}
	// The address has no directory record yet: issue refuses and names --endpoint.
	out, errb, rc := e.run("credential", "issue", "--address", addr, "--key", pub, "--servers", "2")
	outputs = append(outputs, out, errb)
	if rc != 2 || !strings.Contains(errb, "--endpoint") || strings.Contains(out, "issued") {
		t.Fatalf("no directory record: rc %d %q %q", rc, out, errb)
	}
	if _, err := adm.ProvisionIdentity(ctx, ident, nil, pub, e.acctPub, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	doc := e.serveDoc(1, 0, func(m map[string]any) {
		m["groups"] = map[string]any{owner: map[string]any{"mail": "closed", "prompt": "closed", "status": "closed", "heartbeat": "closed"}}
		m["senders"] = map[string]any{addr: map[string]any{"allow": []string{}, "deny": []string{}}}
	})
	if res := directApply(t, dcfg, doc, false); res.Outcome != "applied" {
		t.Fatalf("apply: %+v", res)
	}

	// The keys' host: issue, to a file, with the endpoint taken from the directory.
	jwtPath := filepath.Join(dir, "x.jwt")
	out, errb, rc = e.run("credential", "issue", "--address", addr, "--key", pub, "--servers", "2", "--out", jwtPath)
	outputs = append(outputs, out, errb)
	if rc != 0 || !strings.Contains(out, "issued: "+addr+", key "+pub+", generation 1") || !strings.Contains(out, "jwt written to "+jwtPath) {
		t.Fatalf("issue: rc %d %q %q", rc, out, errb)
	}
	if fi, err := os.Stat(jwtPath); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("jwt file: %v %v", fi, err)
	}
	// The JSON form is one object on stdout and nothing else.
	out, errb, rc = e.run("credential", "issue", "--address", addr, "--key", pub, "--servers", "2", "--json")
	outputs = append(outputs, out, errb)
	var res credential.Result
	if err := json.Unmarshal([]byte(out), &res); rc != 0 || err != nil || res.Outcome != "issued" || res.JWT == "" || errb != "" {
		t.Fatalf("issue --json: rc %d %v %q %q", rc, err, out, errb)
	}

	// The endpoint's host: install, and connect to the other server.
	credsPath := filepath.Join(dir, "x.creds")
	var iout, ierr bytes.Buffer
	if rc := credential.InstallCmd("mash", []string{"--seed", seedPath, "--jwt", jwtPath, "--out", credsPath}, &iout, &ierr); rc != 0 {
		t.Fatalf("install: %s", ierr.String())
	}
	outputs = append(outputs, iout.String(), ierr.String())
	ep := &credEndpoint{name: "x", pub: pub, addr: addr, flat: ident.Flat()}
	ep.connect(t, second.ClientURL(), credsPath)

	// show, by key and by address.
	out, errb, rc = e.run("credential", "show", pub, "--servers", "2")
	outputs = append(outputs, out, errb)
	if rc != 0 || !strings.Contains(out, "key: "+pub+"   address: "+addr+"   revoked: no   connected: yes") {
		t.Fatalf("show key: rc %d %q %q", rc, out, errb)
	}
	out, errb, rc = e.run("credential", "show", addr, "--servers", "2", "--json")
	outputs = append(outputs, out, errb)
	var sk showKeys
	if err := json.Unmarshal([]byte(out), &sk); rc != 0 || err != nil || len(sk.Keys) != 1 || sk.Keys[0].PublicKey != pub {
		t.Fatalf("show address: rc %d %v %q %q", rc, err, out, errb)
	}
	_, other, _ := credential.NewKey()
	out, errb, rc = e.run("credential", "show", other, "--servers", "2")
	outputs = append(outputs, out, errb)
	if rc != 1 || !strings.Contains(errb, "not_found") {
		t.Fatalf("show unknown key: rc %d %q %q", rc, out, errb)
	}
	if _, _, rc = e.run("credential", "show", owner+".nobody", "--servers", "2"); rc != 1 {
		t.Fatalf("show unknown address: rc %d", rc)
	}

	time.Sleep(2 * time.Second)
	// revoke by public key: the connection closes and the record says so.
	// The package's tests stub revokeUsers; this one revokes for real, through the verb.
	prevRevoke := revokeUsers
	revokeUsers = revokeUserKeys
	t.Cleanup(func() { revokeUsers = prevRevoke })
	out, errb, rc = e.run("credential", "revoke", pub, "--servers", "2")
	outputs = append(outputs, out, errb)
	if rc != 0 || !strings.Contains(out, "revoked user key "+pub[:8]+"...: ") || !strings.Contains(out, " server(s) acknowledged") {
		t.Fatalf("revoke: rc %d %q %q", rc, out, errb)
	}
	select {
	case <-ep.closed:
	case <-time.After(serveWait):
		t.Fatalf("the revoked key's connection stayed open (%q %q)", out, errb)
	}
	rec, found, err := adm.IssuedByKey(ctx, pub)
	if err != nil || !found || rec.RevokedAt == "" {
		t.Fatalf("record after revoke: %+v %v %v", rec, found, err)
	}
	out, errb, rc = e.run("credential", "issue", "--address", addr, "--key", pub, "--servers", "2")
	outputs = append(outputs, out, errb)
	if rc != 1 || !strings.Contains(errb, "refused: key_revoked") {
		t.Fatalf("issue for a revoked key: rc %d %q %q", rc, out, errb)
	}

	for _, o := range outputs {
		if strings.Contains(o, seed) {
			t.Fatalf("the seed is in an output:\n%.300s", o)
		}
	}
}
