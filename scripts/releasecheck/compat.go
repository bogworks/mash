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
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/release"
	"github.com/bogworks/mash/internal/testenv"
)

// release-check --compat [--candidate <mash>] [--evidence <file>] [--commit <sha>] [--environment <text>] <older> <newer> (REQ-014, AT-012, TO-13) runs the
// published mash binary of each version for this host against a fixed corpus of mail written by the other. Every
// downloaded binary is checked against the SHA256SUMS of its release before it runs. --candidate runs the given binary
// as the newer side (an unpublished switch-on candidate); --evidence writes what was run and the result as JSON. Its
// exit codes are a named exception (see main.go).

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
	Copies  int    // how many times the message is in the reading binary's stored inbox
	Shown   int    // how many message blocks the reading binary's `mash hook` (what the session is shown) carried for it, over every hook run
	Refused string // the class the reading binary gave when it refused the message; empty when it did not
	// Corrupted: a copy of the message the reader stored or showed carried the marker but not the body that was sent, intact.
	Corrupted bool
}

// CompatOptions configure `release-check --compat`. Every external seam is a field a test replaces.
type CompatOptions struct {
	GH       string // the gh command; default $RELEASE_CHECK_GH, else gh
	GHRepo   string // default as the main gate: $RELEASE_CHECK_REPO, else origin of the checkout on github.com, else bogworks/mash
	Out      io.Writer
	ErrOut   io.Writer
	Download func(ctx context.Context, tag, dir string) (string, error)                                   // test seam: the published binary of tag for this host, in dir; default gh release download
	Sums     func(ctx context.Context, tag string) ([]byte, error)                                        // test seam: the SHA256SUMS asset of the published release tag; default gh release download
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
	ev := &compatEvidence{Receipt: Receipt{Kind: kindAT012, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, CorpusSHA256: compatCorpusDigest()}}
	refused, err := compatRun(context.Background(), args, o, ev)
	var u compatUsage
	var d compatUndecided
	switch {
	case errors.As(err, &u):
		fmt.Fprintf(o.ErrOut, "error: %s\n", u.msg)
		return 2 // a usage error ran nothing: no evidence is written
	case errors.As(err, &d):
		fmt.Fprintf(o.ErrOut, "compat: could not decide: %s\n", d.msg)
		ev.Result, code = resultFailed, 4
	case err != nil:
		fmt.Fprintf(o.ErrOut, "error: %v\n", err)
		ev.Result, code = resultFailed, 1
	case refused:
		ev.Result, code = resultFailed, 3
	default:
		ev.Result, code = resultPassed, 0
	}
	if ev.path != "" { // the exit status says which failure: 3 refused, 4 could not decide, 1 error
		ev.ExitStatus = code
		b, _ := json.MarshalIndent(ev.Receipt, "", "  ")
		if werr := os.WriteFile(ev.path, append(b, '\n'), 0o644); werr != nil {
			fmt.Fprintf(o.ErrOut, "error: writing the evidence: %v\n", werr)
			if code == 0 {
				code = 1
			}
		}
	}
	return code
}

// compatEvidence is what --evidence writes: an AT-012 Receipt (the gate's own schema, so it cannot drift) filled in as
// far as the run knows. The run drives `mash` only, so the mashctl digests stay empty; the commit and the environment
// are filled from --commit and --environment when given. The proof wrapper fills whatever is empty (the mashctl
// digests, the commit, the environment) before the file is given to the gate, which refuses a receipt with any empty
// field.
type compatEvidence struct {
	Receipt
	path string
}

// parseCompatArgs reads the two versions and the flags, which may stand anywhere: --candidate <path>, --evidence <file>,
// --commit <sha> and --environment <text> (each also as --flag=value).
func parseCompatArgs(args []string) (pos []string, f compatFlags, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		var dst *string
		var name, what string
		switch {
		case a == "--candidate" || strings.HasPrefix(a, "--candidate="):
			dst, name, what = &f.candidate, "--candidate", "a path"
		case a == "--evidence" || strings.HasPrefix(a, "--evidence="):
			dst, name, what = &f.evidence, "--evidence", "a path"
		case a == "--commit" || strings.HasPrefix(a, "--commit="):
			dst, name, what = &f.commit, "--commit", "a commit"
		case a == "--environment" || strings.HasPrefix(a, "--environment="):
			dst, name, what = &f.environment, "--environment", "a description"
		default:
			pos = append(pos, a)
			continue
		}
		if a == name {
			if i+1 >= len(args) || args[i+1] == "" {
				return nil, f, compatUsage{name + " needs " + what}
			}
			i++
			*dst = args[i]
		} else if *dst = strings.TrimPrefix(a, name+"="); *dst == "" {
			return nil, f, compatUsage{name + " needs " + what}
		}
	}
	return pos, f, nil
}

// compatFlags are the optional flags of --compat.
type compatFlags struct{ candidate, evidence, commit, environment string }

// fileSHA256 is the sha256 (hex) of a file.
func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// verifyDownloaded holds a downloaded binary to the digest the release's SHA256SUMS lists for the same asset. A missing
// SHA256SUMS, a missing line or a different digest is an error: the binary must not run.
func verifyDownloaded(ctx context.Context, sums func(context.Context, string) ([]byte, error), tag, path string) error {
	b, err := sums(ctx, tag)
	if err != nil {
		return err
	}
	asset := "mash-" + runtime.GOOS + "-" + runtime.GOARCH
	want := ""
	for _, line := range strings.Split(string(b), "\n") {
		if m := sumsLine.FindStringSubmatch(line); m != nil && m[2] == asset {
			if want != "" {
				return fmt.Errorf("SHA256SUMS lists %s more than once", asset)
			}
			want = m[1]
		}
	}
	if want == "" {
		return fmt.Errorf("SHA256SUMS of %s lists no %s", tag, asset)
	}
	got, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%s is sha256:%s, SHA256SUMS lists sha256:%s", asset, got, want)
	}
	return nil
}

func compatRun(ctx context.Context, args []string, o CompatOptions, ev *compatEvidence) (refused bool, err error) {
	pos, fl, err := parseCompatArgs(args)
	candidate := fl.candidate
	if err != nil {
		return false, err
	}
	if len(pos) != 2 {
		return false, compatUsage{"--compat takes two versions: the older, then the newer"}
	}
	older, newer := pos[0], pos[1]
	for _, v := range []string{older, newer} {
		if !versionRE.MatchString(v) {
			return false, compatUsage{fmt.Sprintf("--compat takes two versions: %q is not N.N.N", v)}
		}
	}
	if release.TagBelow("v"+newer, older) || newer == older {
		return false, compatUsage{fmt.Sprintf("--compat takes the older version first: %s is not below %s", older, newer)}
	}
	if candidate != "" {
		if st, serr := os.Stat(candidate); serr != nil || !st.Mode().IsRegular() {
			return false, compatUsage{fmt.Sprintf("--candidate %s is not a file", candidate)}
		}
	}
	ev.path, ev.OlderVersion, ev.NewerVersion = fl.evidence, older, newer
	ev.Commit, ev.Environment = fl.commit, fl.environment
	if o.Panic {
		panic("forced by the test")
	}
	gho := Options{Repo: ".", GH: o.GH, GHRepo: o.GHRepo}
	gho.resolveGH()
	dl := o.Download
	if dl == nil {
		dl = func(_ context.Context, tag, dir string) (string, error) { return downloadPrevious(gho, tag, dir) }
	}
	sums := o.Sums
	if sums == nil {
		sums = func(_ context.Context, tag string) ([]byte, error) { return downloadAsset(gho, tag, release.SumsName) }
	}
	work, err := os.MkdirTemp("", "release-check-compat-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(work)
	var bins [2]compatBin
	for i, v := range []string{newer, older} { // the newer first: it is the one a release candidate is about
		if i == 0 && candidate != "" {
			bins[i] = compatBin{Version: v, Path: candidate} // the caller's own build: nothing published to check it against
			continue
		}
		dir := filepath.Join(work, "dl-"+v)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false, err
		}
		p, derr := dl(ctx, "v"+v, dir)
		if derr != nil {
			return false, compatUndecided{v + " could not be downloaded"}
		}
		// Never run an unverified binary.
		if verr := verifyDownloaded(ctx, sums, "v"+v, p); verr != nil {
			return false, compatUndecided{v + " failed its checksum"}
		}
		bins[i] = compatBin{Version: v, Path: p}
	}
	nb, ob := bins[0], bins[1]
	var herr error
	if ev.NewerMashSHA256, herr = fileSHA256(nb.Path); herr != nil {
		return false, herr
	}
	if ev.OlderMashSHA256, herr = fileSHA256(ob.Path); herr != nil {
		return false, herr
	}
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
// message was. Observations of the reading binary decide it, and each yields its own reason:
//   - the writer's report that it stored the message (a message that was not stored, or has no observation: "not delivered");
//   - the refusal class the reader gave it ("refused: <class>");
//   - the reader's stored inbox, which must hold the message once: none is "missing", more is "delivered twice";
//   - what the session was shown, which is what `mash hook` printed for it (a channel notification is only a one-line
//     wake for an idle session and is not evidence of delivery): a message shown no time is "not delivered", one shown
//     more than once (on one hook run or across two) "delivered twice". A version without `mash hook` is judged by the
//     stored inbox alone, which the driver records as Shown;
//   - the content: a message whose marker was stored or shown but whose body differs from the one sent is "corrupted".
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
		case ob.Copies > 1 || ob.Shown > 1:
			return m.Number, "delivered twice"
		case ob.Shown == 0:
			return m.Number, "not delivered"
		case ob.Corrupted:
			return m.Number, "corrupted"
		}
	}
	return 0, ""
}

// quotedBody is the body a message block shows: render quotes every body line with "> ", so the lines that start so,
// with that prefix removed, are the body (the header lines never start so). A reader that did not quote shows no lines.
func quotedBody(block []string) string {
	var lines []string
	for _, l := range block {
		if t, ok := strings.CutPrefix(l, "> "); ok {
			lines = append(lines, t)
		} else if l == ">" {
			lines = append(lines, "")
		}
	}
	return strings.Join(lines, "\n")
}

// sentText is the text the writer sent for a message with this marker and body, as the reader shows it: render drops
// the body's trailing newlines.
func sentText(marker, body string) string { return strings.TrimRight(marker+"\n"+body, "\n") }

// bodyIntact reports whether text (a hook's context, or what `mash read` printed) shows the message carrying marker
// with exactly the body that was sent. In a full message block the whole quoted body is compared, line for line, so
// multi-line, long and non-ASCII bodies must come through unchanged. A digest block (the hook prints one for a big
// batch, by design) shows only the message's first line and its size in bytes, and no body: for it the first line must
// be the marker's and the size field, when there is one, the size sent; the full body of such a message is compared
// in the stored inbox (`mash read`), which always prints it in full.
func bodyIntact(text, marker, body string) bool {
	want := sentText(marker, body)
	for _, b := range hookBlocks(hookText(text)) {
		if !strings.Contains(strings.Join(b, "\n"), marker) {
			continue
		}
		if strings.HasPrefix(b[0], "#") && strings.Contains(b[0], "\tfrom ") { // a digest line
			if !strings.Contains(b[0], "\t"+marker) && !strings.Contains(b[0], marker) {
				return false
			}
			for _, f := range strings.Split(b[0], "\t") {
				if n, ok := strings.CutSuffix(f, " B"); ok {
					if v, err := strconv.Atoi(n); err == nil && v != len(marker)+1+len(body) {
						return false
					}
				}
			}
			continue
		}
		if quotedBody(b) != want {
			return false
		}
	}
	return true
}

// observeHooks runs the hook until every sent message was shown (or wait ends), waits settle, and runs it once more:
// that run must show nothing new. It records, on each observation, how many blocks showed the message (Shown) and
// whether any showed its body altered (Corrupted). A hook that exits non-zero on any run is an error of the driver:
// what it printed proves nothing.
func observeHooks(ctx context.Context, run func() (string, int, error), obs []compatObs, corpus []compatMsg, marker func(int) string, wait, settle time.Duration) error {
	shown := map[int]int{}
	bad := map[int]bool{}
	runHook := func() error {
		out, code, err := run()
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("`mash hook` exited %d", code)
		}
		for i, o := range obs {
			mk := marker(o.Number)
			n := shownCount(out, mk)
			shown[o.Number] += n
			if n > 0 && !bodyIntact(out, mk, corpus[i].Body) {
				bad[o.Number] = true
			}
		}
		return nil
	}
	allShown := func() bool {
		for _, o := range obs {
			if o.Sent && shown[o.Number] == 0 {
				return false
			}
		}
		return true
	}
	for nd := time.Now().Add(wait); ctx.Err() == nil; {
		if err := runHook(); err != nil {
			return err
		}
		if allShown() || time.Now().After(nd) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	select {
	case <-time.After(settle):
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := runHook(); err != nil {
		return err
	}
	for i := range obs {
		obs[i].Shown = shown[obs[i].Number]
		if bad[obs[i].Number] {
			obs[i].Corrupted = true
		}
	}
	return nil
}

// compatSettle is how long the run waits after the hook showed every message before it runs the hook once more, which
// must show nothing new: a redelivery (a nak is redelivered after 500 ms) would show a message again.
const compatSettle = 2 * time.Second

var (
	sentNumberRE = regexp.MustCompile(`\(number (\d+)[ )]`) // "(number 42 in acme.demo.alice's inbox)" and "(number 44)"
	refusedRE    = regexp.MustCompile(`(?i)refused[^:\n]*:\s*([^\n]+)`)
)

// driveCompat is the real drive: one embedded NATS server in mash's account setup, the writer's binary sending the
// corpus to the reader, the reader's binary running serve as an MCP client does (latency.go's startServe, which verifies
// and spools) and then listing and reading its inbox. Delivery is observed twice: what `mash hook` shows the session (the
// message blocks it prints, counted per message number; serve's channel notifications are only a wake and are not
// counted), and the stored inbox. Each message is tracked by a marker in its body (sender, nonce) and the stream sequence the send reported.
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
	// The delivery itself: serve holds the mail for the hook (MASH_SPOOL_FROM_START), and what the session is shown is
	// what `mash hook` prints. Run it until every stored message was shown, wait a settle period, and run it once more:
	// that run must show nothing new. A version without `mash hook` has only the stored inbox to go by (set below).
	hooked := !release.TagBelow("v"+reader.Version, release.HookSince)
	if hooked {
		run := func() (string, int, error) {
			out, _, code, err := runBounded(ctx, reader.Path, []string{"hook"}, renv, hookInputs["UserPromptSubmit"])
			return out, code, err
		}
		if err := observeHooks(ctx, run, obs, corpus, marker, onlineTimeout, compatSettle); err != nil {
			return nil, err
		}
	}
	for i := range obs {
		mk := marker(obs[i].Number)
		for _, b := range bodies {
			obs[i].Copies += strings.Count(b, mk)
			if strings.Contains(b, mk) && !bodyIntact(b, mk, corpus[i].Body) {
				obs[i].Corrupted = true
			}
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
		if !hooked {
			obs[i].Shown = obs[i].Copies
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
