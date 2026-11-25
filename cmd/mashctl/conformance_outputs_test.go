package main

// The output half of the conformance test: every x-output case of every command of release 0.5.9 in spec/mashctl.json
// (text, stream and exit code) is run against the build and compared exactly, with one exception the specification's
// rules make: a case whose whole text stands in round brackets is a description and is skipped (confIsDescription). A
// case that is not run and not such a description fails: there are no waivers.
//
// The cases run in process against the embedded server of the other tests (no binary, no outside network): a world is
// a server set up by `mashctl init` for owner acme, with the endpoints a case needs, and the folder is ~/.config/mash
// as in the specification. The addresses of the specification are used as they are. What cannot be the same is
// substituted in the specification's text: the server address (the specification's tls://localhost:4222 is the test
// server's own; of the two servers of the status case with several, tls://a.example:4222 is the test server and
// tls://b.example:4222 is an address nothing listens on, as the case says it did not answer), and the version and
// commit. Two shapes vary by nature and match as patterns: a date and time
// (2026-11-19 09:40) and a public key. The folder placeholder <config dir> of the create case is the world's real folder.
//
// How the specification's stream is read (rules.json, "Where a hint is printed"): at a shell every `hint:` line goes to
// stderr, whatever stream the rest uses. So the text of a case is split into its hint lines and the rest; the rest is
// compared exactly with the case's stream, the hint lines exactly with stderr (when the case's stream is stderr the
// hint is simply the tail of the same text). A usage error (exit 2) carries the rule's hint, `hint: mashctl <command>
// --help`, which the specification's texts may leave out. A case's text is the whole output.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// confIsDescription: a case whose whole text stands in round brackets describes the output in words; it is not the
// output, and the conformance test skips it (rules.json, "A case written in brackets is a description").
func confIsDescription(text string) bool {
	t := strings.TrimSpace(text)
	if len(t) < 2 || t[0] != '(' || t[len(t)-1] != ')' {
		return false
	}
	// the opening bracket must close at the very end: "(a) and (b)" is not one pair around the whole text
	depth := 0
	for i := 0; i < len(t); i++ {
		switch t[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 && i != len(t)-1 {
				return false
			}
		}
	}
	return depth == 0
}

// confOutCase is one x-output case.
type confOutCase struct {
	Case   string `json:"case"`
	Text   string `json:"text"`
	Stream string `json:"stream"`
	Exit   int    `json:"exit"`
}

func (c confCmd) outputs() []confOutCase {
	for _, m := range c.Metadata {
		if m.Name == "x-output" {
			var o []confOutCase
			if err := json.Unmarshal(m.Value, &o); err != nil {
				panic(err)
			}
			return o
		}
	}
	return nil
}

// confWorld is a server set up by mashctl init for owner acme, in ~/.config/mash.
type confWorld struct {
	t   *testing.T
	a   *initer
	url string
	js  jetstream.JetStream
	adm *admin.Admin
	ctx context.Context
}

func newConfWorld(t *testing.T, init bool) *confWorld {
	t.Helper()
	return newConfWorldOn(t, newApplyEnv(t), init, nil)
}

// newConfWorldOn is newConfWorld on a given server; prep may change the initer before init runs.
func newConfWorldOn(t *testing.T, e *applyEnv, init bool, prep func(a *initer)) *confWorld {
	t.Helper()
	a := newIniter(t, e)
	if prep != nil {
		prep(a)
	}
	a.env["MASH_CONFIG_DIR"] = filepath.Join(a.root, ".config", "mash")
	a.vals["owner"] = "acme"
	w := &confWorld{t: t, a: a, url: e.srv.ClientURL(), ctx: context.Background()}
	if init {
		if _, errb, code := a.init(); code != 0 {
			t.Fatalf("init: %d %s", code, errb)
		}
	}
	nc, err := nats.Connect(w.url, nats.Timeout(15*time.Second), nats.UserCredentials(a.vals["admin_creds"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	if w.js, err = jetstream.New(nc); err != nil {
		t.Fatal(err)
	}
	if w.adm, err = admin.New(nc, "acme"); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *confWorld) run(args ...string) confRan {
	w.t.Helper()
	o, e, c := w.a.run(args...)
	return confRan{o, e, c}
}

// do runs args and fails the test if the command did not succeed: it is setup.
func (w *confWorld) do(args ...string) {
	w.t.Helper()
	if r := w.run(args...); r.code != 0 {
		w.t.Fatalf("setup %v: exit %d: %s%s", args, r.code, r.out, r.errs)
	}
}

// fleet is the endpoints of the specification's examples: acme.demo.alice (online, with a label), acme.demo.bob (one
// message waiting) and acme.ops.carol (suspended).
func (w *confWorld) fleet() {
	w.t.Helper()
	w.do("endpoint", "create", "acme.ops.carol")
	w.do("endpoint", "suspend", "acme.ops.carol")
	w.pair()
}

// pair is acme.demo.alice (online, with a label) and acme.demo.bob (one message waiting): the two endpoints of the
// specification's list.
func (w *confWorld) pair() {
	w.t.Helper()
	w.do("endpoint", "create", "acme.demo.alice")
	w.do("endpoint", "create", "acme.demo.bob")
	w.do("endpoint", "set", "acme.demo.alice", "label.team=blue")
	w.messages("acme.demo.bob", 1)
	kv, err := w.js.KeyValue(w.ctx, store.LeaseBucket)
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := kv.Put(w.ctx, "acme_demo_alice", []byte("held")); err != nil {
		w.t.Fatal(err)
	}
}

func (w *confWorld) messages(addr string, n int) {
	w.t.Helper()
	a, err := sap.ParseAddress(addr)
	if err != nil {
		w.t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := w.js.Publish(w.ctx, a.Subjects().Mail, []byte(fmt.Sprintf("m%d", i))); err != nil {
			w.t.Fatal(err)
		}
	}
}

// confInboxMessagesRun builds the world of the case "Messages" of `endpoint inbox` and runs the command with args: two
// messages, numbers 7 (signed by acme.demo.alice, 48 B, read and acknowledged) and 8 (no sender header, 61 B, not
// delivered), the clock a minute and a second after message 8 was stored.
func confInboxMessagesRun(t *testing.T, args ...string) confOutRun {
	t.Helper()
	w := newConfWorld(t, true)
	w.do("endpoint", "create", "acme.demo.alice")
	w.do("endpoint", "create", "acme.demo.bob")
	bob, _ := sap.ParseAddress("acme.demo.bob")
	subject := bob.Subjects().Mail
	raw, err := os.ReadFile(filepath.Join(w.a.env["MASH_CONFIG_DIR"], "acme.demo.alice.creds"))
	if err != nil {
		t.Fatal(err)
	}
	alice, err := sap.IdentityFromCredsAt("alice.creds", raw)
	if err != nil {
		t.Fatal(err)
	}
	pad := func(n int) []byte { return []byte(strings.Repeat("x", n)) }
	// An inbox denies delete and purge, so numbers 1 to 6 cannot be stored and removed. The case shows 7 and 8, so
	// the test makes bob's inbox again, with the same configuration and consumer, starting at 7.
	st, err := w.js.Stream(w.ctx, bob.InboxStream())
	if err != nil {
		t.Fatal(err)
	}
	cons, err := w.js.Consumer(w.ctx, bob.InboxStream(), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	scfg, ccfg := st.CachedInfo().Config, cons.CachedInfo().Config
	if err := w.js.DeleteStream(w.ctx, bob.InboxStream()); err != nil {
		t.Fatal(err)
	}
	scfg.FirstSeq = 7
	if st, err = w.js.CreateStream(w.ctx, scfg); err != nil {
		t.Fatal(err)
	}
	if _, err = w.js.CreateConsumer(w.ctx, bob.InboxStream(), ccfg); err != nil {
		t.Fatal(err)
	}
	body := pad(48)
	sendSigned(t, w.js, subject, body, signedHdr(t, alice, subject, body, "acme_demo_alice", time.Now(), "msg-7", "task"))
	sendSigned(t, w.js, subject, pad(61), nats.Header{"Nats-Msg-Id": {"msg-8"}, "Mash-Kind": {"task"}}) // no sender header
	c, err := w.js.Consumer(w.ctx, bob.InboxStream(), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for m := range b.Messages() {
		if err := m.DoubleAck(w.ctx); err != nil {
			t.Fatal(err)
		}
	}
	last, err := st.GetMsg(w.ctx, 8)
	if err != nil {
		t.Fatal(err)
	}
	prev := inboxNow
	inboxNow = func() time.Time { return last.Time.Add(time.Minute + time.Second) }
	t.Cleanup(func() { inboxNow = prev })
	return confOutRun{ran: w.run(append([]string{"endpoint", "inbox", "acme.demo.bob"}, args...)...)}
}

// inTmp makes the working directory a fresh one for the test, for the cases whose output names a relative file.
func inTmp(t *testing.T) { t.Helper(); t.Chdir(t.TempDir()) }

// confOutRun is what a case runner returns: what was run, which substitutions turn the specification's text into the
// test's (old, new pairs).
type confOutRun struct {
	ran  confRan
	subs []string
	// alsoOut, set for a case whose text is stderr only, is the start of the result line printed beside it on stdout
	alsoOut string
}

// confOutputRunners: key is "<command> <case>". A case not here, and not a description, fails.
var confOutputRunners = map[string]func(t *testing.T) confOutRun{}

func init() {
	reg := func(key string, f func(t *testing.T) confOutRun) { confOutputRunners[key] = f }
	urlSub := func(w *confWorld) []string { return []string{"tls://localhost:4222", w.url} }
	dead := "nats://127.0.0.1:1"

	reg("endpoint inbox Messages", func(t *testing.T) confOutRun { return confInboxMessagesRun(t) })
	reg("init Set up", func(t *testing.T) confOutRun {
		w := newConfWorld(t, false)
		return confOutRun{ran: w.run("init", "--config", w.a.setupPath()), subs: urlSub(w)}
	})
	reg("init Already set up", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		return confOutRun{ran: w.run("init", "--config", w.a.setupPath()), subs: urlSub(w)}
	})
	reg("init This folder was set up for a different server", func(t *testing.T) confOutRun {
		w := newConfWorld(t, false)
		ks, err := ctlkeys.Open(w.a.env["MASH_KEYS_DIR"], "")
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := ks.InitOperator(); err != nil {
			t.Fatal(err)
		}
		return confOutRun{ran: w.run("init", "--config", w.a.setupPath())}
	})
	reg("init A strict operator and no signing_key", func(t *testing.T) confOutRun {
		e, sk, osk := strictEnv(t)
		w := newConfWorldOn(t, e, false, func(a *initer) {
			if err := os.WriteFile(a.opSeed, mustRead(t, seedFile(t, t.TempDir(), "op", osk)), 0o600); err != nil {
				t.Fatal(err)
			}
			sc := filepath.Join(a.root, "in", "signer.creds")
			signingKeyCreds(t, e.acctPub, sk, sc)
			a.vals["admin_creds"] = sc
		})
		return confOutRun{ran: w.run("init", "--config", w.a.setupPath()), subs: []string{"mash-setup.yaml", w.a.setupPath(), "tls://localhost:4222", w.url}, alsoOut: "Initialized mash on "}
	})
	reg("init Cannot reach the server", func(t *testing.T) confOutRun {
		w := newConfWorld(t, false)
		w.a.vals["servers"] = []string{dead}
		return confOutRun{ran: w.run("init", "--config", w.a.setupPath()), subs: []string{"tls://localhost:4222", dead}}
	})

	reg("status Working", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.fleet()
		return confOutRun{ran: w.run("status"), subs: urlSub(w)}
	})
	reg("status Several servers: one line each", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.fleet()
		w.a.env["MASH_SERVER"] = w.url + "," + dead
		return confOutRun{ran: w.run("status"), subs: []string{"tls://a.example:4222", w.url, "tls://b.example:4222", dead}}
	})
	reg("status Not working (the line on stdout, the hint on stderr)", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.a.env["MASH_SERVER"] = dead
		return confOutRun{ran: w.run("status"), subs: []string{"tls://localhost:4222", dead}}
	})

	reg("status No server is configured", func(t *testing.T) confOutRun {
		w := newConfWorld(t, false)
		return confOutRun{ran: w.run("status")}
	})

	reg("doctor All well", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.fleet()
		return confOutRun{ran: w.run("doctor"), subs: urlSub(w)}
	})
	reg("doctor A problem", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.fleet()
		a, _ := sap.ParseAddress("acme.demo.dave")
		if _, err := w.adm.EnsureInbox(w.ctx, a); err != nil { // an inbox with no endpoint record
			t.Fatal(err)
		}
		return confOutRun{ran: w.run("doctor"), subs: urlSub(w)}
	})

	reg("doctor Another tool's user is connected (not a problem)", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.fleet()
		w.a.src.connect(w.a.src.mintRogue("tool-a"))
		return confOutRun{ran: w.run("doctor"), subs: urlSub(w)}
	})
	reg("doctor No server is configured", func(t *testing.T) confOutRun {
		w := newConfWorld(t, false)
		return confOutRun{ran: w.run("doctor")}
	})
	reg("doctor --audit-users finds a user mashctl did not issue", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.fleet()
		w.a.src.connect(w.a.src.mintRogue("tool-a"))
		return confOutRun{ran: w.run("doctor", "--audit-users"), subs: urlSub(w)}
	})

	reg("doctor A revoked key is connected and mashctl's users keep connecting and leaving", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.fleet()
		_, key := credsSubject(t, filepath.Join(w.a.env["MASH_CONFIG_DIR"], "acme.demo.alice.creds"))
		if err := w.adm.MarkRevoked(w.ctx, key, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
		scriptScans(t, keyScan(true, key), keyScan(true), keyScan(true, key))
		return confOutRun{ran: w.run("doctor"), subs: urlSub(w)}
	})
	reg("doctor --audit-users: two servers whose users keep changing", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.fleet()
		sc := keyScan(true)
		sc.Incomplete = "server s1's connected users kept changing\nserver NABCDEFGHI...LMNOPQRSTUV's connected users kept changing"
		scriptScans(t, sc)
		return confOutRun{ran: w.run("doctor", "--audit-users"), subs: urlSub(w)}
	})

	// strictWorld is an initialised world on a strict operator with a signing key, three endpoints, and the key as
	// config.json records it.
	strictWorld := func(t *testing.T) (*confWorld, *applyEnv, nkeys.KeyPair, nkeys.KeyPair) {
		e, sk, osk := strictEnv(t)
		w := newConfWorldOn(t, e, true, func(a *initer) {
			if err := os.WriteFile(a.opSeed, mustRead(t, seedFile(t, t.TempDir(), "op", osk)), 0o600); err != nil {
				t.Fatal(err)
			}
			sc := filepath.Join(a.root, "in", "signer.creds")
			signingKeyCreds(t, e.acctPub, sk, sc)
			a.vals["admin_creds"] = sc
			a.vals["signing_key"] = seedFile(t, filepath.Join(a.root, "in"), "signing.seed", sk)
		})
		w.fleet()
		return w, e, sk, osk
	}
	reg("doctor The signing key is not in the key store", func(t *testing.T) confOutRun {
		w, _, _, _ := strictWorld(t)
		for _, f := range []string{"signing.nk", "signing.pub"} {
			if err := os.Remove(filepath.Join(w.a.env["MASH_KEYS_DIR"], f)); err != nil {
				t.Fatal(err)
			}
		}
		return confOutRun{ran: w.run("doctor"), subs: urlSub(w)}
	})
	reg("doctor The signing key is not listed in the account", func(t *testing.T) confOutRun {
		w, e, sk, osk := strictWorld(t)
		skPub, _ := sk.PublicKey()
		fk, _ := nkeys.CreateAccount() // the admin creds move to another plain signing key, so that sign-in still works
		fkPub, _ := fk.PublicKey()
		fkCreds := filepath.Join(w.a.root, "in", "other.creds")
		signingKeyCreds(t, e.acctPub, fk, fkCreds)
		w.a.env["MASH_ADMIN_CREDS"] = fkCreds
		ac := e.claims()
		ac.SigningKeys.Add(fkPub)
		delete(ac.SigningKeys, skPub)
		tok, err := ac.Encode(osk)
		if err != nil {
			t.Fatal(err)
		}
		pushAccountJWT(t, e, tok)
		return confOutRun{ran: w.run("doctor"), subs: urlSub(w)}
	})

	reg("limits show Limits", func(t *testing.T) confOutRun {
		return confOutRun{ran: newConfWorld(t, true).run("limits", "show")}
	})
	reg("limits apply Nothing to do", func(t *testing.T) confOutRun {
		return confOutRun{ran: newConfWorld(t, true).run("limits", "apply")}
	})
	reg("limits apply Applied", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.do("endpoint", "create", "acme.demo.alice")
		w.do("endpoint", "create", "acme.demo.bob")
		for _, n := range []string{"alice", "bob"} {
			a, _ := sap.ParseAddress("acme.demo." + n)
			st, err := w.js.Stream(w.ctx, a.InboxStream())
			if err != nil {
				t.Fatal(err)
			}
			cfg := st.CachedInfo().Config
			cfg.MaxBytes = 1 << 20 // below the limit in force
			if _, err := w.js.UpdateStream(w.ctx, cfg); err != nil {
				t.Fatal(err)
			}
		}
		return confOutRun{ran: w.run("limits", "apply")}
	})

	reg("endpoint create Created, credentials installed on this machine", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		return confOutRun{ran: w.run("endpoint", "create", "acme.demo.bob"), subs: []string{"<config dir>", w.a.env["MASH_CONFIG_DIR"]}}
	})
	reg("endpoint create Created, with a join file", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		inTmp(t)
		return confOutRun{ran: w.run("endpoint", "create", "acme.demo.bob", "--out", "acme.demo.bob.join")}
	})
	reg("endpoint create With --dry-run", func(t *testing.T) confOutRun {
		return confOutRun{ran: newConfWorld(t, true).run("endpoint", "create", "acme.demo.bob", "--dry-run")}
	})
	reg("endpoint create --public-key without --out", func(t *testing.T) confOutRun {
		return confOutRun{ran: newConfWorld(t, true).run("endpoint", "create", "acme.demo.bob", "--public-key", "bob.pub")}
	})
	reg("endpoint create The address exists", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.do("endpoint", "create", "acme.demo.bob")
		return confOutRun{ran: w.run("endpoint", "create", "acme.demo.bob")}
	})
	reg("endpoint create The address was deleted before", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.do("endpoint", "create", "acme.demo.bob")
		w.do("endpoint", "delete", "acme.demo.bob", "--yes")
		return confOutRun{ran: w.run("endpoint", "create", "acme.demo.bob")}
	})

	// the terminal: the prompt is on stdout, the typed text and its newline are the terminal's echo
	typing := func(t *testing.T, input string) {
		t.Helper()
		prevTerm, prevIn := stdinIsTerminal, confirmStdin
		stdinIsTerminal = func() bool { return true }
		confirmStdin = func() io.Reader { return strings.NewReader(input) }
		t.Cleanup(func() { stdinIsTerminal, confirmStdin = prevTerm, prevIn })
	}
	reg("endpoint delete Asked, then deleted", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.do("endpoint", "create", "acme.demo.bob")
		w.messages("acme.demo.bob", 1)
		typing(t, "acme.demo.bob\n")
		r := w.run("endpoint", "delete", "acme.demo.bob")
		r.out = strings.Replace(r.out, "confirm: ", "confirm: acme.demo.bob\n", 1)
		return confOutRun{ran: r}
	})
	reg("endpoint delete A server did not confirm the key was cancelled: nothing is deleted", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.do("endpoint", "create", "acme.demo.bob")
		s := stubRevoke(t)
		s.err = &apply.ConfirmError{Confirmed: 1, Listed: 2, Msg: "revoke: the revocation was pushed but not confirmed: one server"}
		return confOutRun{ran: w.run("endpoint", "delete", "acme.demo.bob", "--yes")}
	})
	reg("endpoint delete With --yes", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.do("endpoint", "create", "acme.demo.bob")
		return confOutRun{ran: w.run("endpoint", "delete", "acme.demo.bob", "--yes")}
	})
	for _, pc := range partialCauses {
		pc := pc
		reg("endpoint delete Cancelled, then a later step failed: "+pc.letter+". "+pc.cause, func(t *testing.T) confOutRun {
			w := newConfWorld(t, true)
			w.do("endpoint", "create", "acme.demo.bob")
			return confOutRun{ran: pc.drive(t, w)}
		})
	}
	reg("endpoint delete Wrong text typed", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.do("endpoint", "create", "acme.demo.bob")
		typing(t, "bob\n")
		r := w.run("endpoint", "delete", "acme.demo.bob")
		r.out = regexp.MustCompile(`(?s)^This deletes acme\.demo\.bob and .*Type the address to confirm: `).ReplaceAllString(r.out, "") // the prompt is not part of the case
		return confOutRun{ran: r}
	})
	reg("endpoint delete No terminal and no --yes", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.do("endpoint", "create", "acme.demo.bob")
		prev := stdinIsTerminal
		stdinIsTerminal = func() bool { return false }
		t.Cleanup(func() { stdinIsTerminal = prev })
		return confOutRun{ran: w.run("endpoint", "delete", "acme.demo.bob")}
	})

	reg("endpoint list Endpoints", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.pair()
		return confOutRun{ran: w.run("endpoint", "list")}
	})
	reg("endpoint list With --wide", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.pair()
		return confOutRun{ran: w.run("endpoint", "list", "--wide")}
	})
	reg("endpoint list None", func(t *testing.T) confOutRun {
		return confOutRun{ran: newConfWorld(t, true).run("endpoint", "list")}
	})
	reg("endpoint show An endpoint", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.fleet()
		w.do("endpoint", "set", "acme.demo.bob", "label.team=blue")
		return confOutRun{ran: w.run("endpoint", "show", "acme.demo.bob")}
	})
	reg("endpoint set Updated", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.do("endpoint", "create", "acme.demo.bob")
		return confOutRun{ran: w.run("endpoint", "set", "acme.demo.bob", "role=reviewer")}
	})
	reg("endpoint unset Updated", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.do("endpoint", "create", "acme.demo.bob")
		w.do("endpoint", "set", "acme.demo.bob", "role=reviewer")
		return confOutRun{ran: w.run("endpoint", "unset", "acme.demo.bob", "role")}
	})
	reg("endpoint suspend Suspended", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.do("endpoint", "create", "acme.demo.bob")
		return confOutRun{ran: w.run("endpoint", "suspend", "acme.demo.bob")}
	})
	reg("endpoint resume Resumed", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.do("endpoint", "create", "acme.demo.bob")
		w.do("endpoint", "suspend", "acme.demo.bob")
		return confOutRun{ran: w.run("endpoint", "resume", "acme.demo.bob")}
	})
	reg("endpoint reissue Reissued", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.do("endpoint", "create", "acme.demo.bob")
		inTmp(t)
		return confOutRun{ran: w.run("endpoint", "reissue", "acme.demo.bob", "--out", "acme.demo.bob.join")}
	})
	reg("endpoint reissue With --detail", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.do("endpoint", "create", "acme.demo.bob")
		inTmp(t)
		// the specification's cluster of two servers acknowledges twice; the test server is one
		ran := w.run("endpoint", "reissue", "acme.demo.bob", "--out", "acme.demo.bob.join", "--detail")
		return confOutRun{ran: ran, subs: []string{"servers acknowledged: 2", "servers acknowledged: 1"}}
	})

	reg("version Version", func(t *testing.T) confOutRun {
		prevV, prevC, prevB := endpoint.CurrentVersion(), commit, readBuildInfo
		endpoint.SetVersion("0.5.6")
		commit = "3f9a41c0d2e8b7a65f4c3b2a1908d7e6f5a4b3c2"
		readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }
		t.Cleanup(func() { endpoint.SetVersion(prevV); commit, readBuildInfo = prevC, prevB })
		return confOutRun{ran: confRun(t, "version")}
	})
	reg("version A build with no version stamped", func(t *testing.T) confOutRun {
		prevV, prevC, prevB := endpoint.CurrentVersion(), commit, readBuildInfo
		endpoint.SetVersion("0.0.0-dev")
		commit = ""
		readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }
		t.Cleanup(func() { endpoint.SetVersion(prevV); commit, readBuildInfo = prevC, prevB })
		return confOutRun{ran: confRun(t, "version")}
	})
}

var (
	confTimeRE = regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}`)
	confKeyRE  = regexp.MustCompile(`[A-Z2-7]{56}`)
)

// confPattern is the text as a pattern: literal, except a date and time and a public key, which match their shapes.
func confPattern(text string) string {
	var b strings.Builder
	for len(text) > 0 {
		t, k := confTimeRE.FindStringIndex(text), confKeyRE.FindStringIndex(text)
		loc, shape := t, `\d{4}-\d{2}-\d{2} \d{2}:\d{2}`
		if t == nil || (k != nil && k[0] < t[0]) {
			loc, shape = k, `[A-Z2-7]{56}`
		}
		if loc == nil {
			b.WriteString(regexp.QuoteMeta(text))
			break
		}
		b.WriteString(regexp.QuoteMeta(text[:loc[0]]) + shape)
		text = text[loc[1]:]
	}
	return b.String()
}

// confOutputProblems compares a case's run with the specification: exact text on its stream, and on the other stream
// only what the rules above allow, and the exit code.
func confOutputProblems(path string, oc confOutCase, r confOutRun) []string {
	text := strings.NewReplacer(r.subs...).Replace(oc.Text)
	if len(r.subs) == 0 {
		text = oc.Text
	}
	var p []string
	wantOut, wantErr := "", ""
	if oc.Stream == "stderr" {
		wantErr = text
	} else {
		var rest, hints []string
		for _, l := range strings.Split(text, "\n") {
			if strings.HasPrefix(l, "hint: ") {
				hints = append(hints, l)
			} else {
				rest = append(rest, l)
			}
		}
		wantOut, wantErr = strings.Join(rest, "\n"), strings.Join(hints, "\n")
	}
	if r.ran.code != oc.Exit {
		p = append(p, fmt.Sprintf("exit: want %d, got %d", oc.Exit, r.ran.code))
	}
	check := func(stream, want, got string) {
		if want == "" {
			if got != "" {
				p = append(p, fmt.Sprintf("%s: want nothing, got %q", stream, got))
			}
			return
		}
		pat := confPattern(want)
		if !regexp.MustCompile("^" + pat + "\n$").MatchString(got) {
			p = append(p, fmt.Sprintf("%s: want %q, got %q", stream, want+"\n", got))
		}
	}
	gotErr := r.ran.errs
	if oc.Exit == 2 && !strings.Contains(wantErr, "hint: ") {
		// a usage error carries the hint of its command's help (rules.json), which the case's text leaves out
		gotErr = strings.TrimSuffix(gotErr, "hint: mashctl "+path+" --help\n")
	}
	if r.alsoOut != "" && wantOut == "" {
		// a warning case: the result goes to stdout as in the case the runner names, and the text is the stderr part
		if !strings.HasPrefix(r.ran.out, r.alsoOut) {
			p = append(p, fmt.Sprintf("stdout: want a start of %q, got %q", r.alsoOut, r.ran.out))
		}
	} else {
		check("stdout", wantOut, r.ran.out)
	}
	check("stderr", wantErr, gotErr)
	return p
}

// confOutputs walks the x-output cases of the tracked commands.
func confOutputs(t *testing.T, tracked []specCommand) {
	checked := map[string]bool{}
	for _, sc := range tracked {
		for _, oc := range sc.c.outputs() {
			sc, oc := sc, oc
			key := sc.path + " " + oc.Case
			t.Run(key, func(t *testing.T) {
				if confIsDescription(oc.Text) {
					if confOutputRunners[key] != nil {
						t.Errorf("%s: a description is skipped, so it needs no runner", key)
					}
					t.Skipf("%s: the text is a description, not the output", key)
				}
				run, ok := confOutputRunners[key]
				if !ok {
					t.Fatalf("%s: no runner and not a description: no case is skipped silently", key)
				}
				checked[key] = true
				confFail(t, "output "+key, confOutputProblems(sc.path, oc, run(t)))
			})
		}
	}
	t.Run("table", func(t *testing.T) {
		for key := range confOutputRunners {
			if !checked[key] {
				t.Errorf("a runner for %q, which is no x-output case of a 0.5.8 command (or did not run)", key)
			}
		}
	})
}
