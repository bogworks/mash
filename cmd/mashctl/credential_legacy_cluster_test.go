package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/credential"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/sap"
)

// An endpoint made by the real `add` is legacy and owner-wide. This is the whole path of governing it in place (ADR 0138):
// x, y and z are added before any policy; a list set is applied (x may reach y, z may not) and z's old add credential
// STILL reaches y, which is the silent failure the guard exists for; add and reissue then refuse and change nothing;
// each endpoint makes its own key, a credential for that public key is signed for the legacy endpoint cc.<owner>.<name>
// and connects on the OTHER server, where x reaches y and z does not and z still reaches its own inbox; the old keys
// that add minted are revoked with the existing revoke path and their connections close; no seed is in any output.
func TestCredentialForALegacyAddEndpointOnTwoRoutedServers(t *testing.T) {
	e, second := newServeEnvCluster(t, false)
	owner := e.env["MASH_OWNER"]
	adm := e.adminOnly()
	sys := e.connect(natsauth.UserCredentials(e.env["MASH_SYS_CREDS"]))
	cfg := e.directConfig(adm, sys, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 6*serveWait)
	defer cancel()

	logs := &lockedBuffer{}
	var outputs []string // every Result, Report, log and command output: no seed may be in any
	record := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, string(b))
	}

	// x, y and z are made by the real add, before any policy: legacy, owner-wide, untagged.
	names := []string{"x", "y", "z"}
	old := map[string]*credEndpoint{}
	eps := map[string]*credEndpoint{}
	oldPaths := map[string]string{}
	for _, n := range names {
		oldPaths[n] = e.add(n)
		pub, err := credsUserKey(oldPaths[n])
		if err != nil {
			t.Fatal(err)
		}
		old[n] = &credEndpoint{name: n, pub: pub, addr: owner + "." + n, flat: n}
	}
	other := second.ClientURL()
	if other == e.srv.ClientURL() {
		t.Fatal("the two servers share a URL")
	}
	legacyID := func(n string) sap.EndpointIdentity {
		id, err := sap.Legacy(owner, n)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	mailY, promptY := legacyID("y").Subjects().Mail, legacyID("y").Subjects().Prompt

	// The list set: x may reach y, z may not. Every verb closed for the group, so reach is the allow lists only.
	doc := e.serveDoc(1, 0, func(m map[string]any) {
		m["groups"] = map[string]any{owner: map[string]any{"mail": "closed", "prompt": "closed", "status": "closed", "heartbeat": "closed"}}
		m["senders"] = map[string]any{
			owner + ".x": map[string]any{"allow": []string{owner + ".y"}, "deny": []string{}},
			owner + ".y": map[string]any{"allow": []string{}, "deny": []string{}},
			owner + ".z": map[string]any{"allow": []string{}, "deny": []string{}},
		}
	})
	applied := directApply(t, cfg, doc, false)
	record(applied)
	if applied.Outcome != "applied" {
		t.Fatalf("apply: %+v", applied)
	}

	// The observer is on s1 and listens on y's mail and prompt subjects; the endpoints publish from s2.
	got := make(chan string, 256)
	for _, subj := range []string{mailY, promptY} {
		if _, err := e.nc.Subscribe(subj, func(m *nats.Msg) { got <- m.Subject + "|" + string(m.Data) }); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	drain := func() {
		for {
			select {
			case s := <-got:
				seen[s] = true
			default:
				return
			}
		}
	}
	// reaches publishes payload to y's subjects from ep until both arrive, or the wait ends.
	reaches := func(ep *credEndpoint, payload string) bool {
		deadline := time.Now().Add(serveWait)
		for !(seen[mailY+"|"+payload] && seen[promptY+"|"+payload]) && time.Now().Before(deadline) {
			for _, s := range []string{mailY, promptY} {
				_ = ep.nc.Publish(s, []byte(payload))
			}
			_ = ep.nc.Flush()
			time.Sleep(100 * time.Millisecond)
			drain()
		}
		return seen[mailY+"|"+payload] && seen[promptY+"|"+payload]
	}

	// BEFORE any credential is issued through the primitive: z's old add credential still reaches y. The list set applied
	// and changed nothing for an endpoint add made (the silent failure of spike 8). A reissue could not fix it either:
	// add and reissue mint the same owner-wide credential.
	for _, n := range names {
		old[n].connect(t, other, oldPaths[n])
	}
	if !reaches(old["z"], "z-old") {
		t.Fatalf("z's old add credential did not reach y: the policy was expected to leave it ungoverned (violations %v)", old["z"].violations)
	}
	if old["z"].denied(mailY) || old["z"].denied(promptY) {
		t.Fatalf("z's old credential was refused: %v", old["z"].violations)
	}

	// add of a new name and reissue of x refuse under the list set, with the pinned text, and change nothing.
	adm2 := e.adminOnly()
	issuedBefore, _, err := adm2.Issued(ctx)
	if err != nil {
		t.Fatal(err)
	}
	recsBefore, err := adm2.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfgDir, keysDir := e.env["MASH_CONFIG_DIR"], e.env["MASH_KEYS_DIR"]
	dirBefore := snapshotTree(t, cfgDir)
	keysBefore := map[string]string{}
	if keysDir != "" {
		if _, err := os.Stat(keysDir); err == nil {
			keysBefore = snapshotTree(t, keysDir)
		}
	}
	for _, c := range []struct {
		verb string
		args []string
	}{{"add", []string{"add", "w"}}, {"reissue", []string{"reissue", "x"}}} {
		out, errb, code := e.run(c.args...)
		outputs = append(outputs, out, errb)
		if code != 1 || !strings.Contains(errb, listSetRefusal(c.verb, 1)) || strings.Contains(out, "added") || strings.Contains(out, "reissued") {
			t.Fatalf("%s under a list set: %d\n%s\n%s", c.verb, code, out, errb)
		}
	}
	if _, err := os.Lstat(filepath.Join(cfgDir, "w.creds")); err == nil {
		t.Fatal("a refused add wrote a creds file")
	}
	if got := snapshotTree(t, cfgDir); !sameFiles(got, dirBefore) {
		t.Fatal("a refused command changed the config directory")
	}
	if keysDir != "" {
		if _, err := os.Stat(keysDir); err == nil && !sameFiles(snapshotTree(t, keysDir), keysBefore) {
			t.Fatal("a refused command changed the key store")
		}
	}
	issuedAfter, _, _ := adm2.Issued(ctx)
	recsAfter, _ := adm2.List(ctx)
	if len(issuedAfter) != len(issuedBefore) || len(recsAfter) != len(recsBefore) {
		t.Fatalf("issue records %d (was %d), directory records %d (was %d)", len(issuedAfter), len(issuedBefore), len(recsAfter), len(recsBefore))
	}

	// Each endpoint makes its own key. The credential names cc.<owner>.<name>: the same address and the same legacy
	// endpoint the directory record holds, for a new public key (the record still names the old user key).
	ccfg, err := credential.FromDirect(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ccfg.Log = slog.New(slog.NewTextHandler(logs, nil))
	for _, n := range names {
		seed, pub, err := credential.NewKey()
		if err != nil {
			t.Fatal(err)
		}
		eps[n] = &credEndpoint{name: n, seed: seed, pub: pub, addr: owner + "." + n, flat: n}
	}
	req := func(n string) credential.Request {
		return credential.Request{Address: eps[n].addr, Endpoint: "cc." + eps[n].addr, PublicKey: eps[n].pub}
	}
	for _, n := range names {
		ep := eps[n]
		out, err := credential.Issue(ctx, ccfg, req(n))
		record(out)
		if err != nil || out.Outcome != credential.OutcomeIssued || out.JWT == "" || out.ExitCode() != 0 || out.Generation != 1 {
			t.Fatalf("issue %s: %+v, %v", n, out, err)
		}
		if !slices.Contains(out.Tags, "ep:"+n) {
			t.Fatalf("the credential of %s carries tags %v, want ep:%s", n, out.Tags, n)
		}
		creds, err := credential.Assemble(out.JWT, ep.seed)
		if err != nil {
			t.Fatalf("assemble %s: %v", n, err)
		}
		path := filepath.Join(t.TempDir(), n+".creds")
		if err := os.WriteFile(path, creds, 0o600); err != nil {
			t.Fatal(err)
		}
		clear(creds)
		ep.connect(t, other, path)
	}

	// A legacy endpoint the directory does not back is refused: no record of that name, another owner's address.
	_, strayPub, _ := credential.NewKey()
	for _, r := range []credential.Request{
		{Address: owner + ".w", Endpoint: "cc." + owner + ".w", PublicKey: strayPub},
		{Address: "someone." + names[0], Endpoint: "cc.someone." + names[0], PublicKey: strayPub},
	} {
		res, err := credential.Issue(ctx, ccfg, r)
		record(res)
		if err != nil || res.Outcome != credential.OutcomeRefused || res.Refusal == nil || res.JWT != "" {
			t.Fatalf("an endpoint the directory does not back (%s): %+v, %v", r.Endpoint, res, err)
		}
	}

	// Reach: x is allowed.
	if !reaches(eps["x"], "x") {
		t.Fatalf("x did not reach y's subjects: %v (violations %v)", seen, eps["x"].violations)
	}
	if eps["x"].denied(mailY) || eps["x"].denied(promptY) {
		t.Errorf("x was refused a subject it may reach: %v", eps["x"].violations)
	}
	// z is not: the server refuses each publish and nothing is delivered.
	for _, s := range []string{mailY, promptY} {
		_ = eps["z"].nc.Publish(s, []byte("z"))
	}
	_ = eps["z"].nc.Flush()
	for deadline := time.Now().Add(serveWait); !(eps["z"].denied(mailY) && eps["z"].denied(promptY)) && time.Now().Before(deadline); {
		time.Sleep(servePoll)
	}
	if !eps["z"].denied(mailY) || !eps["z"].denied(promptY) {
		t.Fatalf("z was not refused y's subjects: %v", eps["z"].violations)
	}
	time.Sleep(500 * time.Millisecond)
	drain()
	if seen[mailY+"|z"] || seen[promptY+"|z"] {
		t.Fatalf("z's message reached y: %v", seen)
	}
	// z still reaches its own inbox (inbox-z) and not y's.
	jsz, err := jetstream.New(eps["z"].nc)
	if err != nil {
		t.Fatal(err)
	}
	sctx, scancel := context.WithTimeout(ctx, 20*time.Second)
	if _, err := jsz.Stream(sctx, sap.InboxStream("z")); err != nil {
		t.Errorf("z cannot reach its own inbox: %v", err)
	}
	scancel()
	sctx, scancel = context.WithTimeout(ctx, 3*time.Second)
	if _, err := jsz.Stream(sctx, sap.InboxStream("y")); err == nil {
		t.Error("z reached y's inbox stream")
	}
	scancel()

	// Show finds each key by its public key alone, and its record carries the legacy tag.
	for _, n := range names {
		rep, err := credential.Show(ctx, ccfg, eps[n].pub)
		record(rep)
		if err != nil || !rep.Recorded || rep.Record == nil || rep.Record.Address != eps[n].addr || rep.AccountRevokes != credential.StateNo ||
			rep.Connection.State != credential.StateYes || !rep.Complete || !rep.Clean || !slices.Contains(rep.Record.Tags, "ep:"+n) {
			t.Fatalf("show %s: %+v, %v", n, rep, err)
		}
	}

	// The old add-minted keys are revoked with the existing revoke path: their connections close, the new ones stay.
	env := func(k string) string { return e.env[k] }
	mcfg, err := config.Load(config.LookupFromFunc(env), e.env["HOME"])
	if err != nil {
		t.Fatal(err)
	}
	var oldPubs []string
	for _, n := range names {
		oldPubs = append(oldPubs, old[n].pub)
	}
	var revOut bytes.Buffer
	if err := revokeUserKeys(ctx, mcfg, env, 2, oldPubs, &revOut, nil); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	outputs = append(outputs, revOut.String())
	for _, n := range names {
		select {
		case <-old[n].closed:
		case <-time.After(serveWait):
			t.Fatalf("the old key of %s stayed connected after its revocation (status %v, revoke output %q)", n, old[n].nc.Status(), revOut.String())
		}
	}
	for _, n := range names {
		if !eps[n].nc.IsConnected() {
			t.Errorf("revoking the old keys closed the new connection of %s", n)
		}
	}
	// y's new connection still works after the revocation.
	if _, err := jetstream.New(eps["y"].nc); err != nil {
		t.Fatal(err)
	}

	// No seed is in any result, report, log or output the test saw.
	outputs = append(outputs, logs.String())
	for _, n := range names {
		for _, o := range outputs {
			if strings.Contains(o, string(eps[n].seed)) {
				t.Fatalf("the seed of %s is in an output:\n%.300s", n, o)
			}
		}
	}
	for _, o := range outputs {
		e.noSeeds(o)
	}
}
