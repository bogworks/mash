package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// refusalCase is one line of AT-003's list that has a CLI form.
type refusalCase struct {
	Name   string
	Label  string
	Home   Home     // where the command runs
	Extra  []string // environment added to the connection settings
	Args   []string // the command (after the binary)
	Mash   bool     // run mash rather than mashctl
	Exit   int
	Code   string     // the refusal code in --json; "" means any non-empty code; unused when NoJSON
	Hint   [][]string // the message must hold, for each group, at least one of its strings: what to type
	NoJSON bool
}

// refusal runs the case between two snapshots and asserts the exit code, the refusal code, the hint and that nothing
// was written.
func (a *AT) refusal(ctlHome Home, trees []Home, c refusalCase) {
	before := a.Snapshot(c.Label+"-before", ctlHome, trees...)
	var res Result
	extra := append(a.Conn(), c.Extra...)
	if c.Mash {
		res = a.Mash(c.Label, c.Home, extra, c.Args...)
	} else {
		res = a.Ctl(c.Label, c.Home, extra, c.Args...)
	}
	after := a.Snapshot(c.Label+"-after", ctlHome, trees...)
	a.C.Check(c.Name+": exit code "+fmt.Sprint(c.Exit), res.Code == c.Exit, exitDetail(res))
	if !c.NoJSON {
		got := refusalCode(res.Stdout)
		ok := got == c.Code || (c.Code == "" && got != "")
		want := c.Code
		if want == "" {
			want = "(any)"
		}
		a.C.Check(c.Name+": --json refusal code "+want, ok, "refusal "+orNone(got)+", "+exitDetail(res))
	}
	text := res.Text()
	var missing []string
	for _, grp := range c.Hint {
		found := false
		for _, s := range grp {
			if strings.Contains(text, s) {
				found = true
			}
		}
		if !found {
			missing = append(missing, "one of "+strings.Join(grp, " | "))
		}
	}
	a.C.Check(c.Name+": the message names what to type", len(missing) == 0 && strings.TrimSpace(text) != "", "missing: "+strings.Join(missing, "; "))
	diff := before.Diff(after)
	bad := append(before.Bad, after.Bad...)
	a.C.Check(c.Name+": nothing was written (directory, issue record, streams, leases, files byte-identical"+exclusionNote()+")", len(diff) == 0 && len(bad) == 0, "changed: "+orNone(strings.Join(diff, ", "))+"; failed reads: "+orNone(strings.Join(bad, ", ")))
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func writeFile(path, text string) error { return os.WriteFile(path, []byte(text), 0o600) }

// AT003 is every refusal can fail.
var AT003 = Scenario{
	ID: "at003",
	Scenario: "AT-003, every refusal can fail: one local server; endpoints alice/dev/design, alice/dev/review, a deleted alice/dev/gone and bob/dev/build. " +
		"Each refusal of the contract's list that has a CLI form is run between two snapshots (mashctl list, audit --json, issued list --json, " +
		"and a digest of each home's files): exit code, refusal code in --json, a message that names what to type, nothing written. " +
		"A plain nats:// URL to loopback is refused by mash (session start and a command) and by mashctl, exit 2, and no connection is made; with the override the connection is made and the warning is printed (REQ-014). " +
		"Then bob's credentials publish to alice's mail subject and request her prompt subject.",
	Body: func(a *AT) {
		ha, e1 := a.Home("home-a") // mashctl host
		he, e2 := a.Home("home-e") // endpoint machine
		hn, e3 := a.Home("home-n") // a host with no account key
		if !a.C.Check("three scratch homes are made", e1 == nil && e2 == nil && e3 == nil, "") || !a.StartServer(ha) {
			return
		}
		trees := []Home{ha, he, hn}
		adminCreds := filepath.Join(a.srvDir, "mashadmin.creds")

		// Setup: four endpoints made the contract's way, one of them deleted so that its address is burned.
		design, ok := a.provision(ha, he, "alice", "dev", "design", filepath.Join(he.Dir, "design.creds"), false)
		if !ok {
			return
		}
		review, ok := a.provision(ha, he, "alice", "dev", "review", filepath.Join(he.Dir, "review.creds"), false)
		if !ok {
			return
		}
		if _, ok = a.provision(ha, he, "alice", "dev", "gone", "", true); !ok {
			return
		}
		r := a.Ctl("endpoint-delete-gone", ha, a.Conn(), "endpoint", "delete", "alice.dev.gone", "--yes")
		if !a.C.Check("setup: mashctl endpoint delete alice/dev/gone burns the address", r.Code == 0, exitDetail(r)) {
			return
		}
		bob, ok := a.provision(ha, he, "bob", "dev", "build", filepath.Join(he.Dir, "bob.creds"), false)
		if !ok {
			return
		}
		r = a.Mash("key-create-fresh", he, a.Conn(), "key", "create", "--out", filepath.Join(he.Dir, "fresh.seed"))
		if !a.C.Check("setup: a key that has never been issued", r.Code == 0, exitDetail(r)) {
			return
		}
		fresh := filepath.Join(he.Dir, "fresh.pub")
		bad := filepath.Join(he.Dir, "bad.pub")
		rule := filepath.Join(he.Dir, "rule.json")
		taken := filepath.Join(he.Dir, "taken.join")
		if err := writeFile(bad, "this is not a public key\n"); err != nil {
			a.C.Check("setup: the scratch files are written", false, err.Error())
			return
		}
		_ = writeFile(taken, "an existing file\n")
		_ = writeFile(rule, `{"version":1,"rules":[{"id":"no-scratch","match":{"group":{"is":"scratch"}},"message":"the group scratch is not for real work"}]}`+"\n")

		// A refusal of create is `error: <what>` and at most one `hint: <command>` on standard error, exit 1; a wrong command
		// line is exit 2 with the hint `mashctl endpoint create --help`. Neither carries a refusal code, so these cases
		// check the exit code and the text.
		create := func(addr string, more ...string) []string {
			return append([]string{"endpoint", "create", addr}, more...)
		}
		out := func(name string) []string { return []string{"--out", filepath.Join(he.Dir, name)} }
		pk := append([]string{"--public-key", fresh}, out("pk.join")...)
		cases := []refusalCase{
			{Name: "an address that is not owner.group.name", Label: "no-group", Home: ha, Args: create("design", pk...), Exit: 2, NoJSON: true,
				Hint: [][]string{{"owner.group.name"}, {"mashctl endpoint create --help"}}},
			{Name: "the slash form of an address", Label: "slash-form", Home: ha, Args: create("alice/dev/design", pk...), Exit: 2, NoJSON: true,
				Hint: [][]string{{"owner.group.name"}, {"mashctl endpoint create --help"}}},
			{Name: "--public-key without --out", Label: "key-without-out", Home: ha, Args: create("alice.dev.solo", "--public-key", fresh), Exit: 2, NoJSON: true,
				Hint: [][]string{{"--public-key needs --out"}, {"mashctl endpoint create --help"}}},
			{Name: "a removed option", Label: "removed-option", Home: ha, Args: create("alice.dev.solo", "--make-key"), Exit: 2, NoJSON: true,
				Hint: [][]string{{"unknown option"}, {"mashctl endpoint create --help"}}},
			{Name: "a token with a capital", Label: "name-capital", Home: ha, Args: create("alice.dev.Design", pk...), Exit: 2, NoJSON: true,
				Hint: [][]string{{"owner.group.name"}}},
			{Name: "a token with a double dash", Label: "name-dashes", Home: ha, Args: create("alice.dev.a--b", pk...), Exit: 2, NoJSON: true,
				Hint: [][]string{{"owner.group.name"}}},
			{Name: "a token of 33 characters", Label: "name-long", Home: ha, Args: create("alice.dev."+strings.Repeat("a", 33), pk...), Exit: 2, NoJSON: true,
				Hint: [][]string{{"owner.group.name"}}},
			{Name: "a token the name rule file refuses", Label: "name-rule", Home: ha, Extra: []string{"MASH_NAME_RULE_FILE=" + rule}, Args: create("alice.scratch.x", pk...), Exit: 1, NoJSON: true,
				Hint: [][]string{{"no-scratch"}}},
			{Name: "an address that exists", Label: "address-taken", Home: ha, Args: create("alice.dev.design", append([]string{"--public-key", fresh}, out("taken-address.join")...)...), Exit: 1, NoJSON: true,
				Hint: [][]string{{"alice.dev.design already exists"}}},
			{Name: "a deleted, burned address", Label: "key-retired", Home: ha, Args: create("alice.dev.gone", append([]string{"--public-key", fresh}, out("burned-address.join")...)...), Exit: 1, NoJSON: true,
				Hint: [][]string{{"alice.dev.gone was deleted and its address cannot be used again"}}},
			{Name: "a malformed public key file", Label: "pubkey-invalid", Home: ha, Args: create("alice.dev.other", append([]string{"--public-key", bad}, out("bad.join")...)...), Exit: 1, NoJSON: true,
				Hint: [][]string{{"mash key create"}}},
			{Name: "a public key already issued at another address", Label: "pubkey-issued", Home: ha, Args: create("alice.dev.other", append([]string{"--public-key", design.pub}, out("issued.join")...)...), Exit: 1, NoJSON: true,
				Hint: [][]string{{"mash key create"}}},
			{Name: "a join file path that exists", Label: "file-exists", Home: ha, Args: create("alice.dev.other", "--public-key", fresh, "--out", taken), Exit: 1, NoJSON: true,
				Hint: [][]string{{"already exists"}}},
			{Name: "no account key on the mashctl host", Label: "no-account-key", Home: hn, Extra: []string{"MASH_ADMIN_CREDS=" + adminCreds}, Args: create("alice.dev.other", pk...), Exit: 1, NoJSON: true,
				Hint: [][]string{{"mashctl init", "mashctl adopt", "MASH_KEYS_DIR", "account key"}}},
			{Name: "mash credential create with a seed and a JWT for different keys", Label: "creds-mismatch", Home: he, Mash: true, NoJSON: true, Exit: 1,
				Args: []string{"credential", "create", "--seed", design.seed, "--jwt", review.jwt, "--out", filepath.Join(he.Dir, "mismatch.creds")},
				Hint: [][]string{{"mash key create", "--seed", "--jwt", "same key", "different", "belong together"}}},
		}
		for _, c := range cases {
			a.refusal(ha, trees, c)
		}

		// A session whose endpoint has no group.
		before := a.Snapshot("no-group-session-before", ha, trees...)
		gextra := append(a.Conn(), "MASH_ENDPOINT=alice.ghost", "MASH_STATE_DIR="+filepath.Join(he.State, "ghost"))
		r = a.Mash("session-no-group", he, gextra, "serve")
		after := a.Snapshot("no-group-session-after", ha, trees...)
		a.C.Check("a session whose endpoint has no group: exit code 1", r.Code == 1, exitDetail(r))
		a.C.Check("a session whose endpoint has no group: standard error names MASH_ENDPOINT", strings.Contains(r.Stderr, "MASH_ENDPOINT"), "")
		a.C.Check("a session whose endpoint has no group: no directory record, stream, consumer, lease or service was made for it",
			len(before.Diff(after)) == 0 && !strings.Contains(after.Raw["audit"], "ghost") && !strings.Contains(after.Raw["list"], "ghost"), "changed: "+orNone(strings.Join(before.Diff(after), ", ")))

		// The three removed commands.
		for _, c := range []refusalCase{
			{Name: "mashctl add", Label: "removed-add", Home: ha, Args: []string{"add", "alice.dev.old"}, Exit: 2, NoJSON: true, Hint: [][]string{{"endpoint create"}}},
			{Name: "mashctl reissue", Label: "removed-reissue", Home: ha, Args: []string{"reissue", "alice.dev.design"}, Exit: 2, NoJSON: true, Hint: [][]string{{"endpoint reissue"}}},
			{Name: "mashctl retire", Label: "removed-retire", Home: ha, Args: []string{"retire", "alice.dev.design"}, Exit: 2, NoJSON: true, Hint: [][]string{{"endpoint delete"}}},
		} {
			a.refusal(ha, trees, c)
		}

		a.plainText(ha, he, design)
		a.crossOwner(ha, he, bob, design, trees)
	},
}

// crossOwner is the planted cross-owner attempt: bob's credentials publish to alice's mail subject and request her
// prompt subject; the server must refuse both, and nothing may reach alice's inbox.
func (a *AT) crossOwner(ha, he Home, bob, alice ep, trees []Home) {
	const probe = "cross-owner probe text"
	errs := make(chan error, 16)
	flat := "bob_dev_build"
	nc, err := nats.Connect(a.tlsURL(), nats.RootCAs(a.caPath()), nats.UserCredentials(bob.creds), nats.CustomInboxPrefix("_INBOX.bob."+flat),
		nats.MaxReconnects(0), nats.Timeout(5*time.Second),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, e error) {
			select {
			case errs <- e:
			default:
			}
		}))
	if !a.C.Check("bob connects with his own credentials", err == nil, errDetail(err)) {
		return
	}
	defer nc.Close()
	_ = nc.Publish("agents.mail.alice.dev.design", []byte(probe))
	_ = nc.Flush()
	_, _ = nc.Request("agents.prompt.alice.dev.design", []byte(probe), 2*time.Second)
	var mail, prompt bool
	deadline := time.After(3 * time.Second)
collect:
	for {
		select {
		case e := <-errs:
			m := strings.ToLower(e.Error())
			if strings.Contains(m, "permissions violation") {
				mail = mail || strings.Contains(m, "agents.mail.alice.dev.design")
				prompt = prompt || strings.Contains(m, "agents.prompt.alice.dev.design")
			}
		case <-deadline:
			break collect
		}
	}
	a.C.Check("INV-002: the server refuses bob's publish to agents.mail.alice.dev.design with a permissions violation", mail, "")
	a.C.Check("INV-002: the server refuses bob's request to agents.prompt.alice.dev.design with a permissions violation", prompt, "")
	aliceEnv := append(a.Conn(), "MASH_ENDPOINT=alice.dev.design", "MASH_CREDS="+alice.creds, "MASH_STATE_DIR="+filepath.Join(he.State, "design"))
	a.forgedReplies(he, nc, errs, aliceEnv)
	r := a.Mash("inbox-alice", he, aliceEnv, "inbox")
	a.C.Check("alice's inbox is read with her own credentials", r.Code == 0, exitDetail(r))
	a.C.Check("nothing arrived in alice's inbox from bob", r.Code == 0 && !strings.Contains(r.Text(), probe), "")
}

// forgedReplies is REQ-005's negative case: bob subscribes to $SRV.>, sees the reply subject of alice's discovery
// request (the presence probe of `mash who`), and publishes into it a forged discovery answer that claims her offline
// endpoint is up, and a second answer shaped like a replayed registration. The server must refuse both with a
// permissions violation (bob's publish rights stop at _INBOX.bob.>), and who must not show her endpoints online.
func (a *AT) forgedReplies(he Home, nc *nats.Conn, errs chan error, aliceEnv []string) {
	spy, err := nc.SubscribeSync("$SRV.>")
	if err == nil {
		err = nc.Flush()
	}
	if !a.C.Check("REQ-005: bob subscribes to $SRV.> and sees discovery requests", err == nil, errDetail(err)) {
		return
	}
	forged := []byte(`{"name":"agents","type":"io.nats.micro.v1.info_response","metadata":{"owner":"alice","group":"dev","instance":"design","agent":"claude-code"},` +
		`"endpoints":[{"name":"prompt","subject":"agents.prompt.alice.dev.design"}]}`)
	replay := []byte(`{"name":"agents","type":"io.nats.micro.v1.info_response","metadata":{"owner":"alice","group":"dev","instance":"review","agent":"claude-code",` +
		`"user_nkey":"UREPLAYED","account":"AREPLAYED","id_sig":"replayed"},"endpoints":[{"name":"prompt","subject":"agents.prompt.alice.dev.review"}]}`)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			m, err := spy.NextMsg(50 * time.Millisecond)
			select {
			case <-stop:
				return
			default:
			}
			if err == nil && strings.HasPrefix(m.Subject, "$SRV.INFO") && strings.HasPrefix(m.Reply, "_INBOX.alice.") {
				_ = nc.Publish(m.Reply, forged)
				_ = nc.Publish(m.Reply, replay)
				_ = nc.Flush()
			}
		}
	}()
	es, r := a.who(he, aliceEnv)
	close(stop)
	<-done
	_ = nc.Flush()
	refused := 0
	deadline := time.After(3 * time.Second)
collect:
	for refused < 2 {
		select {
		case e := <-errs:
			if m := strings.ToLower(e.Error()); strings.Contains(m, "permissions violation for publish") && strings.Contains(m, "_inbox.alice.") {
				refused++
			}
		case <-deadline:
			break collect
		}
	}
	a.C.Check("REQ-005: alice's mash who ran while bob answered", r.Code == 0 && len(es) > 0, exitDetail(r))
	a.C.Check("REQ-005: the server refuses bob's forged discovery answer and his replayed presence answer with permissions violations", refused >= 2, fmt.Sprintf("%d violation(s) seen", refused))
	online := ""
	for _, e := range es {
		if e.Online {
			online += e.Address + " "
		}
	}
	a.C.Check("REQ-005: alice's who does not list her offline endpoints as online", online == "", "online: "+orNone(online))
}

func errDetail(err error) string {
	if err == nil {
		return ""
	}
	return redact(err.Error())
}
