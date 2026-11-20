package main

// The output half of the conformance test: every x-output case of every command of release 0.5.6 in spec/mashctl.json
// (text, stream and exit code) is either run against the build and compared exactly, or listed in confUnchecked with
// the reason it cannot be run here. A case that is neither fails; a listed case that is checked after all fails, so the
// table only shrinks.
//
// The cases run in process against the embedded server of the other tests (no binary, no outside network): a world is
// a server set up by `mashctl init` for owner acme, with the endpoints a case needs, and the folder is ~/.config/mash
// as in the specification. The addresses of the specification are used as they are. What cannot be the same is
// substituted in the specification's text: the server address (the specification's tls://localhost:4222 is the test
// server's own), and the version and commit. Two shapes vary by nature and match as patterns: a date and time
// (2026-11-19 09:40) and a public key.
//
// How the specification's stream is read: stream is where the whole text goes, except that a case that says so in its name
// ("the line on stdout, the hint on stderr") has its last line, the hint, on stderr. (rules.json says that the hint of
// status and doctor always goes to stderr; the x-output of doctor puts it in the stdout text, and the build follows the
// x-output: reported as a contradiction.) A usage
// error (exit 2) may carry the rule's hint, `hint: mashctl <command> --help`, which the specification's texts leave out.
// A case whose text is an excerpt (doctor's "A problem" shows the failing row, not all six) is matched as an ordered
// subsequence of lines.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// confUnchecked: key is "<command> <case>" as the specification names them; the value is why the case cannot be run
// here.
var confUnchecked = map[string]string{
	"status Several servers: one line each":                                              "the first line must say '3 endpoints, 1 online' of a second, working server at a.example; one test server answers under one address",
	"endpoint delete A server did not confirm the key was cancelled: nothing is deleted": "needs a cluster of two servers, one of which does not confirm the cancelling of the key",
	"endpoint delete Cancelled everywhere, then a later step failed":                     "needs a server fault injected between the cancelling of the key and the removal of the inbox",
	"endpoint inbox Messages":                                                            "the rows hold times relative to now ('2m ago', '1m ago') and the trust of messages signed by two senders, one of them an endpoint not on this server",
	"endpoint permissions Permissions":                                                   "the text describes the output ('the same JSON as --json'); it is not the output",
	"endpoint reissue With --verbose":                                                    "'servers acknowledged: 2' needs a cluster of two servers",
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
	e := newApplyEnv(t)
	a := newIniter(t, e)
	a.env["MASH_CONFIG_DIR"] = filepath.Join(a.root, ".config", "mash")
	a.vals["owner"] = "acme"
	w := &confWorld{t: t, a: a, url: e.srv.ClientURL(), ctx: context.Background()}
	if init {
		if _, errb, code := a.init(); code != 0 {
			t.Fatalf("init: %d %s", code, errb)
		}
	}
	nc, err := nats.Connect(w.url, nats.UserCredentials(e.pl.AdminCredsPath()))
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
	w.do("endpoint", "create", "acme.demo.alice")
	w.do("endpoint", "create", "acme.demo.bob")
	w.do("endpoint", "create", "acme.ops.carol")
	w.do("endpoint", "set", "acme.demo.alice", "label.team=blue")
	w.do("endpoint", "suspend", "acme.ops.carol")
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

// inTmp makes the working directory a fresh one for the test, for the cases whose output names a relative file.
func inTmp(t *testing.T) { t.Helper(); t.Chdir(t.TempDir()) }

// confOutRun is what a case runner returns: what was run, which substitutions turn the specification's text into the
// test's (old, new pairs), and whether the specification's text is an excerpt.
type confOutRun struct {
	ran     confRan
	subs    []string
	excerpt bool
}

// confOutputRunners: key is "<command> <case>". A case not here and not in confUnchecked fails.
var confOutputRunners = map[string]func(t *testing.T) confOutRun{}

func init() {
	reg := func(key string, f func(t *testing.T) confOutRun) { confOutputRunners[key] = f }
	urlSub := func(w *confWorld) []string { return []string{"tls://localhost:4222", w.url} }
	dead := "nats://127.0.0.1:1"

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
		ks, err := ctlkeys.Open(w.a.env["MASH_KEYS_DIR"])
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := ks.InitOperator(); err != nil {
			t.Fatal(err)
		}
		return confOutRun{ran: w.run("init", "--config", w.a.setupPath())}
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
	reg("status Not working (the line on stdout, the hint on stderr)", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.a.env["MASH_SERVER"] = dead
		return confOutRun{ran: w.run("status"), subs: []string{"tls://localhost:4222", dead}}
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
		return confOutRun{ran: w.run("doctor"), subs: urlSub(w), excerpt: true}
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
		return confOutRun{ran: newConfWorld(t, true).run("endpoint", "create", "acme.demo.bob")}
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
	reg("endpoint delete With --yes", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.do("endpoint", "create", "acme.demo.bob")
		return confOutRun{ran: w.run("endpoint", "delete", "acme.demo.bob", "--yes")}
	})
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
		w.fleet()
		return confOutRun{ran: w.run("endpoint", "list")}
	})
	reg("endpoint list With --wide", func(t *testing.T) confOutRun {
		w := newConfWorld(t, true)
		w.fleet()
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

// confSubsequence reports whether every line of want is in got, in order.
func confSubsequence(got, want string) bool {
	g := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	i := 0
	for _, w := range strings.Split(strings.TrimSuffix(want, "\n"), "\n") {
		for i < len(g) && !regexp.MustCompile("^"+confPattern(w)+"$").MatchString(g[i]) {
			i++
		}
		if i == len(g) {
			return false
		}
		i++
	}
	return true
}

// confOutputProblems compares a case's run with the specification: exact text on its stream, and on the other stream
// only what the rules above allow, and the exit code.
func confOutputProblems(path string, oc confOutCase, r confOutRun) []string {
	text := strings.NewReplacer(r.subs...).Replace(oc.Text)
	if len(r.subs) == 0 {
		text = oc.Text
	}
	wantOut, wantErr := "", ""
	if oc.Stream == "stderr" {
		wantErr = text
	} else {
		lines := strings.Split(text, "\n")
		if n := len(lines); n > 0 && strings.HasPrefix(lines[n-1], "hint: ") && strings.Contains(oc.Case, "the hint on stderr") {
			wantErr, lines = lines[n-1], lines[:n-1]
		}
		wantOut = strings.Join(lines, "\n")
	}
	var p []string
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
		if r.excerpt {
			if !confSubsequence(got, want) {
				p = append(p, fmt.Sprintf("%s: want the lines %q in order, got %q", stream, want, got))
			}
			return
		}
		if !regexp.MustCompile("^" + confPattern(want) + "\n$").MatchString(got) {
			p = append(p, fmt.Sprintf("%s: want %q, got %q", stream, want+"\n", got))
		}
	}
	gotErr := r.ran.errs
	if oc.Exit == 2 && oc.Stream == "stderr" && !strings.Contains(wantErr, "\nhint: ") {
		// a usage error carries the hint of its command's help (rules.json), which the case's text leaves out
		gotErr = strings.TrimSuffix(gotErr, "hint: mashctl "+path+" --help\n")
	}
	check("stdout", wantOut, r.ran.out)
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
				run, ok := confOutputRunners[key]
				reason, listed := confUnchecked[key]
				switch {
				case ok && listed:
					t.Errorf("%s: it is checked, so it must not be in confUnchecked (%s)", key, reason)
				case !ok && !listed:
					t.Errorf("%s: neither checked nor listed in confUnchecked: no case is skipped silently", key)
				case !ok:
					t.Logf("confUnchecked: %s: %s", key, reason)
				default:
					checked[key] = true
					r := run(t)
					confCheck(t, "output "+key, confOutputProblems(sc.path, oc, r))
				}
			})
		}
	}
	t.Run("table", func(t *testing.T) {
		for key := range confUnchecked {
			found := false
			for _, sc := range tracked {
				for _, oc := range sc.c.outputs() {
					found = found || sc.path+" "+oc.Case == key
				}
			}
			if !found {
				t.Errorf("confUnchecked has %q, which is no x-output case of a 0.5.6 command", key)
			}
		}
		for key := range confOutputRunners {
			if !checked[key] {
				t.Errorf("a runner for %q, which is no x-output case of a 0.5.6 command (or did not run)", key)
			}
		}
	})
}
