package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/release"
	"github.com/bogworks/mash/internal/testenv"
)

// release-check --compat <older> <newer> (REQ-014, AT-012, TO-13) runs the published mash binary of each version for
// this host against a fixed corpus of mail written by the other. Its exit codes are a named exception (see main.go).

// compatMsg is one message of the corpus. Number is its place in the list, from 1; Body is the text without the
// marker the run adds to track the message.
type compatMsg struct {
	Number int    `json:"number"`
	What   string `json:"what"`
	Kind   string `json:"kind,omitempty"`
	Urgent bool   `json:"urgent,omitempty"`
	Reply  bool   `json:"reply,omitempty"` // answers a seed message in the writer's inbox
	Body   string `json:"body"`
}

// compatCorpus is the fixed corpus. mash has no subject line, so there is no empty-subject case.
var compatCorpus = []compatMsg{
	{What: "plain", Body: "Plain mail: can you look at the build when you get a moment?"},
	{What: "fyi", Kind: "fyi", Body: "For your information: the staging run finished."},
	{What: "urgent", Urgent: true, Kind: "blocker", Body: "Urgent: the release is blocked until this is answered."},
	{What: "reply", Reply: true, Kind: "result", Body: "Reply: yes, that is done."},
	{What: "question", Kind: "question", Body: "Is the second line kept?\nIt is the second line."},
	{What: "long body", Body: strings.Repeat("A long line of ordinary text, kept the same on every run.\n", 120)},
	{What: "non-ASCII body", Body: "Café — naïve résumé, 日本語, مرحبا, \U0001F600"},
	{What: "short", Kind: "task", Body: "ok"},
}

func init() {
	for i := range compatCorpus {
		compatCorpus[i].Number = i + 1
	}
}

// compatCorpusDigest is the sha256 (hex) of the corpus, which a receipt records.
func compatCorpusDigest() string {
	b, err := json.Marshal(compatCorpus)
	if err != nil {
		panic(err) // a corpus that cannot be encoded is a bug in the source
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// compatBin is one binary under test.
type compatBin struct {
	Version string // N.N.N
	Path    string
}

// compatObs is what happened to one corpus message.
type compatObs struct {
	Number  int
	Sent    bool   // the writing binary reported it stored
	Seq     int    // its stream sequence, 0 when not visible
	Copies  int    // how many times the reading binary delivered it
	Refused string // the class the reading binary gave when it refused the message; empty when it did not
}

// CompatOptions configure `release-check --compat`. Every external seam is a field a test replaces.
type CompatOptions struct {
	GH       string // the gh command; default $RELEASE_CHECK_GH, else gh
	GHRepo   string // default $RELEASE_CHECK_REPO, else bogworks/mash
	Out      io.Writer
	ErrOut   io.Writer
	Download func(ctx context.Context, tag, dir string) (string, error)                                   // test seam: the published binary of tag for this host, in dir; default gh release download
	Drive    func(ctx context.Context, writer, reader compatBin, corpus []compatMsg) ([]compatObs, error) // test seam: writer writes the corpus and reader reads it; default a throwaway NATS server and the real binaries
	Panic    bool                                                                                         // test seam: panic inside the run
}

// compatUsage is a usage error: exit 2.
type compatUsage struct{ msg string }

func (u compatUsage) Error() string { return u.msg }

// compatUndecided is a run that could not decide: exit 4.
type compatUndecided struct{ msg string }

func (u compatUndecided) Error() string { return u.msg }

// compatMain runs --compat with the arguments after the flag and returns the exit code: 0 passed, 1 unexpected
// error (a panic included), 2 usage, 3 refused, 4 could not decide.
func compatMain(args []string, o CompatOptions) (code int) {
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.ErrOut == nil {
		o.ErrOut = os.Stderr
	}
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(o.ErrOut, "error: unexpected failure: %v\n", r)
			code = 1
		}
	}()
	refused, err := compatRun(context.Background(), args, o)
	var u compatUsage
	var d compatUndecided
	switch {
	case errors.As(err, &u):
		fmt.Fprintf(o.ErrOut, "error: %s\n", u.msg)
		return 2
	case errors.As(err, &d):
		fmt.Fprintf(o.ErrOut, "compat: could not decide: %s\n", d.msg)
		return 4
	case err != nil:
		fmt.Fprintf(o.ErrOut, "error: %v\n", err)
		return 1
	case refused:
		return 3
	}
	return 0
}

func compatRun(ctx context.Context, args []string, o CompatOptions) (refused bool, err error) {
	if len(args) != 2 {
		return false, compatUsage{"--compat takes two versions: the older, then the newer"}
	}
	older, newer := args[0], args[1]
	for _, v := range []string{older, newer} {
		if !versionRE.MatchString(v) {
			return false, compatUsage{fmt.Sprintf("--compat takes two versions: %q is not N.N.N", v)}
		}
	}
	if release.TagBelow("v"+newer, older) || newer == older {
		return false, compatUsage{fmt.Sprintf("--compat takes the older version first: %s is not below %s", older, newer)}
	}
	if o.Panic {
		panic("forced by the test")
	}
	dl := o.Download
	if dl == nil {
		gho := Options{GH: o.GH, GHRepo: o.GHRepo}
		if gho.GH == "" {
			gho.GH = os.Getenv("RELEASE_CHECK_GH")
		}
		if gho.GH == "" {
			gho.GH = "gh"
		}
		if gho.GHRepo == "" {
			gho.GHRepo = os.Getenv("RELEASE_CHECK_REPO")
		}
		if gho.GHRepo == "" {
			gho.GHRepo = "bogworks/mash"
		}
		dl = func(_ context.Context, tag, dir string) (string, error) { return downloadPrevious(gho, tag, dir) }
	}
	work, err := os.MkdirTemp("", "release-check-compat-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(work)
	var bins [2]compatBin
	for i, v := range []string{newer, older} { // the newer first: it is the one a release candidate is about
		dir := filepath.Join(work, "dl-"+v)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false, err
		}
		p, derr := dl(ctx, "v"+v, dir)
		if derr != nil {
			return false, compatUndecided{v + " could not be downloaded"}
		}
		bins[i] = compatBin{Version: v, Path: p}
	}
	nb, ob := bins[0], bins[1]
	drive := o.Drive
	if drive == nil {
		drive = driveCompat
	}
	// The older reads what the newer wrote, then the newer reads what the older wrote.
	for _, d := range []struct{ writer, reader compatBin }{{nb, ob}, {ob, nb}} {
		obs, err := drive(ctx, d.writer, d.reader, compatCorpus)
		if err != nil {
			return false, err
		}
		n, reason := judgeCompat(compatCorpus, obs)
		if n == 0 {
			fmt.Fprintf(o.Out, "compat: %s reads mail written by %s: passed\n", d.reader.Version, d.writer.Version)
			continue
		}
		fmt.Fprintf(o.Out, "compat: %s reads mail written by %s: FAILED\n  message %d: %s\n", d.reader.Version, d.writer.Version, n, reason)
		return true, nil
	}
	return false, nil
}

// judgeCompat finds the first message of the corpus that was not delivered exactly once, and why: n is 0 when every
// message was. A message with no observation was never sent.
func judgeCompat(corpus []compatMsg, obs []compatObs) (n int, reason string) {
	by := map[int]compatObs{}
	for _, ob := range obs {
		by[ob.Number] = ob
	}
	for _, m := range corpus {
		ob, ok := by[m.Number]
		switch {
		case !ok || !ob.Sent:
			return m.Number, "not delivered"
		case ob.Refused != "":
			return m.Number, "refused: " + ob.Refused
		case ob.Copies == 0:
			return m.Number, "missing"
		case ob.Copies > 1:
			return m.Number, "delivered twice"
		}
	}
	return 0, ""
}

var (
	sentNumberRE = regexp.MustCompile(`\(number (\d+)\)`)
	refusedRE    = regexp.MustCompile(`(?i)refused[^:\n]*:\s*([^\n]+)`)
)

// driveCompat is the real drive: one embedded NATS server in mash's account setup, the writer's binary sending the
// corpus to the reader, the reader's binary running serve (which verifies and delivers) and then listing and reading
// its inbox. Each message is tracked by a marker in its body (sender, nonce) and the stream sequence the send reported.
func driveCompat(ctx context.Context, writer, reader compatBin, corpus []compatMsg) ([]compatObs, error) {
	dir, err := os.MkdirTemp("", "release-check-compat-lab-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	ne, stop, err := testenv.Launch(filepath.Join(dir, "nats"))
	if err != nil {
		return nil, fmt.Errorf("starting the NATS server: %w", err)
	}
	defer stop()
	nc, err := nats.Connect(ne.URL, natsauth.UserCredentials(ne.AdminCreds))
	if err != nil {
		return nil, err
	}
	defer nc.Close()
	adm, err := admin.New(nc, ne.Owner)
	if err != nil {
		return nil, err
	}
	if err := adm.Init(ctx); err != nil {
		return nil, fmt.Errorf("initialising the directory: %w", err)
	}
	legacy := func(b compatBin) bool { return release.TagBelow("v"+b.Version, release.EndpointEnvSince) }
	wl := &lab{bin: writer.Path, ne: ne, adm: adm, dir: dir, legacy: legacy(writer)}
	rl := &lab{bin: reader.Path, spec: serveSpec{Args: []string{"serve"}, Env: map[string]string{"MASH_SPOOL_FROM_START": "1"}}, ne: ne, adm: adm, dir: dir, legacy: legacy(reader)}
	if rl.senderE, err = rl.endpointEnv(ctx, "probe", false); err != nil {
		return nil, err
	}
	wenv, err := wl.endpointEnv(ctx, "writer", false)
	if err != nil {
		return nil, err
	}
	penv, err := wl.endpointEnv(ctx, "peer", false)
	if err != nil {
		return nil, err
	}
	p, renv, finding, err := rl.session(ctx, "reader")
	if err != nil {
		return nil, err
	}
	if finding != "" {
		return nil, errors.New("the reading binary " + reader.Version + ": " + finding)
	}
	defer p.stop()

	addr := func(name string) string { return ne.Owner + "." + labGroup + "." + name }
	send := func(env []string, to, text string, flags ...string) (int, bool, error) {
		out, _, code, err := runBounded(ctx, writer.Path, append(append([]string{"send", addr(to)}, flags...), text), env, "")
		if err != nil {
			return 0, false, err
		}
		if code != 0 {
			return 0, false, nil
		}
		if m := sentNumberRE.FindStringSubmatch(out); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n, true, nil
		}
		return 0, true, nil
	}
	seed, ok, err := send(penv, "writer", "compat seed: something to answer")
	if err != nil {
		return nil, err
	}
	if !ok || seed == 0 {
		return nil, errors.New("the writing binary " + writer.Version + " could not store the seed message the reply answers")
	}
	var nb [8]byte
	if _, err := rand.Read(nb[:]); err != nil {
		return nil, err
	}
	nonce := hex.EncodeToString(nb[:])
	marker := func(n int) string { return fmt.Sprintf("compat-%d-%s", n, nonce) }
	obs := make([]compatObs, len(corpus))
	for i, m := range corpus {
		flags := []string{}
		if m.Kind != "" {
			flags = append(flags, "--kind", m.Kind)
		}
		if m.Urgent {
			flags = append(flags, "--urgent")
		}
		if m.Reply {
			flags = append(flags, "--reply-to", strconv.Itoa(seed))
		}
		seq, ok, err := send(wenv, "reader", marker(m.Number)+"\n"+m.Body, flags...)
		if err != nil {
			return nil, err
		}
		obs[i] = compatObs{Number: m.Number, Sent: ok, Seq: seq}
	}
	// Wait until the reader's inbox stops growing, then read every message in it.
	var bodies map[int]string
	deadline := time.Now().Add(onlineTimeout)
	last := -1
	for {
		bodies, err = readInbox(ctx, reader.Path, renv)
		if err != nil {
			return nil, err
		}
		if len(bodies) == last || time.Now().After(deadline) {
			if len(bodies) >= len(corpus) || time.Now().After(deadline) {
				break
			}
		}
		last = len(bodies)
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	for i := range obs {
		mk := marker(obs[i].Number)
		for _, b := range bodies {
			obs[i].Copies += strings.Count(b, mk)
		}
		if obs[i].Sent && obs[i].Copies == 0 && obs[i].Seq != 0 {
			out, _, _, err := runBounded(ctx, reader.Path, []string{"read", strconv.Itoa(obs[i].Seq)}, renv, "")
			if err != nil {
				return nil, err
			}
			if m := refusedRE.FindStringSubmatch(out); m != nil {
				obs[i].Refused = strings.TrimSpace(m[1])
			}
		}
	}
	return obs, nil
}

// readInbox is the text of every message in the inbox, by number, as the reading binary shows it.
func readInbox(ctx context.Context, bin string, env []string) (map[int]string, error) {
	out, _, code, err := runBounded(ctx, bin, []string{"inbox", "--limit", "100", "--json"}, env, "")
	if err != nil {
		return nil, err
	}
	var res struct {
		Messages []struct {
			Number int `json:"number"`
		} `json:"messages"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &res) != nil {
		return nil, fmt.Errorf("the reading binary could not list its inbox (exit %d): %q", code, strings.TrimSpace(out))
	}
	bodies := map[int]string{}
	for _, m := range res.Messages {
		text, _, _, err := runBounded(ctx, bin, []string{"read", strconv.Itoa(m.Number)}, env, "")
		if err != nil {
			return nil, err
		}
		bodies[m.Number] = text
	}
	return bodies, nil
}
