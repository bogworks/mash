// Package kit is what the stored-mail checks share: a throwaway NATS server with provisioned endpoints, a released
// `mash` run as one of them, the sends that write stored mail, and the detector for the eleventh-shape candidates. The
// commands that use it (classify, at006) run inside a throwaway container, never on a working host.
package kit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/sapsdk"
	"github.com/bogworks/mash/internal/testenv"
)

// stepTimeout is how long one mash command of the check may run before it is killed.
const stepTimeout = 60 * time.Second

// Name is an endpoint to provision: its label in the kit, and its group and instance tokens (the owner is the test
// environment's).
type Name struct{ Label, Group, Instance string }

// Sent is one message a send wrote, as the sender asked for it.
type Sent struct {
	From, To string // labels
	Body     string
	Kind     string // "" is the default kind
	Urgent   bool
	ReplyTo  int
	Relay    int
	ID       string
}

// Kit is one release's world: a server, the endpoints, and the release binary.
type Kit struct {
	Tag   string
	E     *testenv.Env
	Bin   string
	Dir   string
	Conn  *nats.Conn // the admin connection
	Adm   *admin.Admin
	Creds map[string]string
	Addrs map[string]sap.Address
	// Env is added to the environment of every `mash` run as the label's endpoint (the label's map), e.g. a launch token.
	Env map[string]map[string]string

	Sends, Refused int
	Ledger         []Sent
	Notes          []string
	basicErr       error
	stop           func()
}

// New starts a server under dir, provisions names and returns the kit. Close it when done.
func New(dir, bin, tag string, names []Name) (*Kit, error) { return NewOwner(dir, bin, tag, "", names) }

// NewOwner is New with the SAP owner of the endpoints given ("" keeps the test environment's).
func NewOwner(dir, bin, tag, owner string, names []Name) (*Kit, error) {
	e, stop, err := testenv.Launch(filepath.Join(dir, "srv"))
	if err != nil {
		return nil, err
	}
	if owner != "" {
		e.Owner = owner
	}
	k := &Kit{Tag: tag, E: e, Bin: bin, Dir: dir, Creds: map[string]string{}, Addrs: map[string]sap.Address{},
		Env: map[string]map[string]string{}, stop: stop}
	anc, err := nats.Connect(e.URL, natsauth.UserCredentials(e.AdminCreds))
	if err != nil {
		stop()
		return nil, err
	}
	k.Conn = anc
	ctx := context.Background()
	if k.Adm, err = admin.New(anc, e.Owner); err != nil {
		k.Close()
		return nil, err
	}
	if err := k.Adm.Init(ctx); err != nil {
		k.Close()
		return nil, err
	}
	for _, n := range names {
		a, err := sap.NewAddress(e.Owner, n.Group, n.Instance)
		if err != nil {
			k.Close()
			return nil, err
		}
		creds, pub, err := e.MintEndpoint(a)
		if err != nil {
			k.Close()
			return nil, err
		}
		if _, err := k.Adm.Provision(ctx, a, pub, e.Account, "", admin.Meta{}); err != nil {
			k.Close()
			return nil, fmt.Errorf("provision %s: %w", a, err)
		}
		k.Creds[n.Label], k.Addrs[n.Label] = creds, a
	}
	return k, nil
}

// Close stops the server.
func (k *Kit) Close() {
	if k.Conn != nil {
		k.Conn.Close()
	}
	if k.stop != nil {
		k.stop()
	}
}

// To is a label's address as `mash send` takes it.
func (k *Kit) To(label string) string { return k.Addrs[label].String() }

// Home is the label's private home and state directory for the release binary.
func (k *Kit) Home(label string) string { return filepath.Join(k.Dir, "home", label) }

// Mash runs the release binary as label with stdin and returns its standard output.
func (k *Kit) Mash(label, stdin string, args ...string) (string, error) {
	home := k.Home(label)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	return k.MashBin(k.Bin, label, home+"/state", stdin, args...)
}

// MashBin runs the binary bin as label with the given state directory.
func (k *Kit) MashBin(bin, label, state, stdin string, args ...string) (string, error) {
	home := k.Home(label)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = k.ProcEnv(label, state)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return "", err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return out.String(), fmt.Errorf("%v: %s", err, firstReason(errb.String()+out.String()))
		}
		return out.String(), nil
	case <-time.After(stepTimeout):
		_ = cmd.Process.Kill() // this command's own PID, recorded by os/exec
		<-done
		return "", fmt.Errorf("timed out")
	}
}

// ProcEnv is the environment a `mash` process runs in as label with the given state directory: the label's home, endpoint,
// server and creds, and the label's entries of Env. A caller that starts a long-running process (`mash serve`) uses it too.
func (k *Kit) ProcEnv(label, state string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + k.Home(label), "MASH_STATE_DIR=" + state,
		"MASH_ENDPOINT=" + k.To(label), "MASH_SERVER=" + k.E.URL, "MASH_CREDS=" + k.Creds[label],
		"MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1"}
	for n, v := range k.Env[label] {
		env = append(env, n+"="+v)
	}
	return env
}

// firstReason is the first line of a command's output that is not the warning about the missing encryption.
func firstReason(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "warning: ") {
			return l
		}
	}
	return strings.TrimSpace(out)
}

// Send has the release write one message. A basic send must succeed (its failure is returned by BasicErr); an optional send that
// fails because the release lacks its flag is counted, noted and skipped; any other failure fails the run too. The message is recorded in Ledger when it was written.
func (k *Kit) Send(basic bool, s Sent) {
	args := []string{"send", k.To(s.To)}
	stdin := ""
	switch {
	case s.Body == "." || strings.Contains(s.Body, "\n") || len(s.Body) > 4000:
		args, stdin = append(args, "--file", "-"), s.Body
	default:
		args = append(args, s.Body)
	}
	if s.Kind != "" {
		args = append(args, "--kind", s.Kind)
	}
	if s.Urgent {
		args = append(args, "--urgent")
	}
	if s.ID != "" {
		args = append(args, "--id", s.ID)
	}
	if s.ReplyTo != 0 {
		args = append(args, "--reply-to", fmt.Sprint(s.ReplyTo))
	}
	if s.Relay != 0 {
		args = append(args, "--relay", fmt.Sprint(s.Relay))
	}
	k.Sends++
	if _, err := k.Mash(s.From, stdin, args...); err != nil {
		k.Refused++
		if basic {
			if k.basicErr == nil {
				k.basicErr = fmt.Errorf("basic send %q failed: %w", s.Body, err)
			}
			return
		}
		if !LacksFlag(err) { // any other failure of a supported send is a broken send path: the run fails
			if k.basicErr == nil {
				k.basicErr = fmt.Errorf("send %v failed: %w", args[2:], err)
			}
			return
		}
		note := fmt.Sprintf("%s send (%s): this release lacks the flag: %v", k.Tag, strings.Join(args[2:], " "), err)
		k.Notes = append(k.Notes, note)
		fmt.Fprintln(os.Stderr, "note: "+note)
		return
	}
	k.Ledger = append(k.Ledger, s)
}

// LacksFlag is true when a send failed because the release does not know a flag (the only reason an optional send is skipped).
func LacksFlag(err error) bool {
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "unknown flag") || strings.Contains(m, "unknown option") ||
		strings.Contains(m, "flag provided but not defined") || strings.Contains(m, "unrecognized")
}

// BasicErr is the first failed basic send, or nil.
func (k *Kit) BasicErr() error { return k.basicErr }

// SendAll has each side send every kind this CLI writes. The two plain sends every release supports must succeed (see
// BasicErr); a send with a flag the release lacks is counted and noted, not fatal.
func (k *Kit) SendAll(a, b string) {
	long := strings.Repeat("long line of text, with unicode é世界\U0001F600 and tabs\t\n", 150)
	k.Send(true, Sent{From: a, To: b, Body: "hello bob"})
	//lint:ignore ST1018 deliberate bidi-override (U+202E) test input, written as the literal character
	k.Send(false, Sent{From: a, To: b, Body: "unicode é世界 \U0001F600 ‮ rtl", Kind: "fyi"})
	k.Send(false, Sent{From: a, To: b, Body: "."})
	k.Send(false, Sent{From: a, To: b, Body: long, Kind: "result"})
	k.Send(false, Sent{From: a, To: b, Body: "urgent one", Urgent: true, Kind: "blocker"})
	k.Send(false, Sent{From: a, To: b, Body: "keyed", ID: "storedmail-1", Kind: "question"})
	k.Send(true, Sent{From: b, To: a, Body: "hello alice"})
	k.Send(false, Sent{From: b, To: a, Body: "reply", ReplyTo: 1})
	k.Send(false, Sent{From: b, To: a, Body: "unicode é世界", Kind: "fyi"})
	k.Send(false, Sent{From: a, To: b, Body: "relayed", Relay: 1})
}

var ctl = regexp.MustCompile(`[\x00-\x1f\x7f]`)

// Candidate names the eleventh-shape candidate a header is (the cases of TestEleventhShapeCandidates: X1, X2, X3), or "".
func Candidate(vals []string, trust sap.Trust, class, subject string) string {
	if len(vals) != 1 {
		return ""
	}
	v := vals[0]
	if !utf8.ValidString(v) {
		return "eleventh-shape candidate: invalid UTF-8 in the value"
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(v), &obj) != nil {
		return ""
	}
	if raw, ok := obj["v"]; ok && string(raw) != "1" {
		if _, isNum := numeric(raw); isNum {
			return "eleventh-shape candidate: v is the number " + string(raw)
		}
	}
	var sub string
	if json.Unmarshal(obj["sub"], &sub) == nil && (sub == "" || strings.Contains(sub, "..") || strings.HasPrefix(sub, ".") ||
		strings.HasSuffix(sub, ".") || ctl.MatchString(sub)) {
		return "eleventh-shape candidate: sub with an empty token, empty, or a control character"
	}
	_, signed := obj["sig"]
	if !signed && trust == sap.TrustMalformed {
		switch class {
		case sapsdk.ClassTimeInvalid, sapsdk.ClassNonce, sapsdk.ClassUnreadable:
			return "eleventh-shape candidate: unsigned claim with a bad ts, nonce or sub"
		}
	}
	if class == sapsdk.ClassUnreadable {
		return "eleventh-shape candidate: unreadable sender header"
	}
	return ""
}

func numeric(raw json.RawMessage) (float64, bool) {
	var f float64
	return f, json.Unmarshal(raw, &f) == nil
}

// Shape is a header with every value removed: member names, kinds and lengths only. No body, no key, no signature.
func Shape(vals []string) string {
	if len(vals) != 1 {
		return fmt.Sprintf("%d Agent-Sender values", len(vals))
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(vals[0]), &obj) != nil {
		return fmt.Sprintf("not a JSON object, %d bytes", len(vals[0]))
	}
	var ks []string
	for k, raw := range obj {
		var s string
		switch {
		case k == "v":
			ks = append(ks, "v="+string(raw))
		case json.Unmarshal(raw, &s) == nil:
			ks = append(ks, fmt.Sprintf("%s:str(%d)", k, len(s)))
		default:
			ks = append(ks, k+":non-text")
		}
	}
	sort.Strings(ks)
	return "{" + strings.Join(ks, " ") + "}"
}

// SelfCheck proves the detector sees what it is for, so a clean table is not a blind one. It returns the failures.
func SelfCheck() []string {
	var f []string
	const hdr = `{"v":1.0,"account":"A","user":"U","sub":"agents.mail.x"}`
	if Candidate([]string{hdr}, sap.TrustVerified, "", "s") == "" {
		f = append(f, "self-check: v 1.0 not seen as a candidate")
	}
	if Candidate([]string{`{"v":1,"sub":"a..b"}`}, sap.TrustVerified, "", "s") == "" {
		f = append(f, "self-check: empty sub token not seen as a candidate")
	}
	if Candidate([]string{"{\"v\":1,\"x\":\"\xff\"}"}, sap.TrustVerified, "", "s") == "" {
		f = append(f, "self-check: invalid UTF-8 not seen as a candidate")
	}
	return f
}
