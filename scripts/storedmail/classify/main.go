// Command classify runs mail stored by released mash binaries through sapsdk.Classify. Run it by scripts/storedmail/run.sh,
// inside a throwaway container: it starts a NATS server, runs each release's own `mash` as alice and bob, reads every
// stored message and fails on an eleventh-shape candidate or on any class the old mash would not give its own header.
//
//	classify <bindir> <tag>...      bindir/<tag>/mash-linux-amd64
package main

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/sapsdk"
	"github.com/bogworks/mash/internal/testenv"
)

type result struct {
	tag                  string
	msgs, sends, refused int
	trust, class         map[string]int
	control, forged      int
	failures             []string
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: classify <bindir> <tag>...")
		os.Exit(2)
	}
	var all []*result
	bad := false
	for _, tag := range os.Args[2:] {
		r, err := run(filepath.Join(os.Args[1], tag, "mash-linux-amd64"), tag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", tag, err)
			os.Exit(1)
		}
		all = append(all, r)
		bad = bad || len(r.failures) > 0
	}
	fmt.Printf("%-8s %7s %5s %-28s %-24s %7s %6s\n", "release", "sent/ok", "msgs", "trust", "class (non-empty)", "control", "forged")
	for _, r := range all {
		fmt.Printf("%-8s %7s %5d %-28s %-24s %7d %6d\n", r.tag, fmt.Sprintf("%d/%d", r.sends, r.sends-r.refused), r.msgs, counts(r.trust), counts(r.class), r.control, r.forged)
		for _, f := range r.failures {
			fmt.Printf("FAIL %s: %s\n", r.tag, f)
		}
	}
	if bad {
		fmt.Println("RESULT: FAIL")
		os.Exit(1)
	}
	fmt.Println("RESULT: PASS (no candidate, no class)")
}

func counts(m map[string]int) string {
	if len(m) == 0 {
		return "-"
	}
	var ks []string
	for k, n := range m {
		ks = append(ks, fmt.Sprintf("%s=%d", k, n))
	}
	sort.Strings(ks)
	return strings.Join(ks, ",")
}

type kit struct {
	e     *testenv.Env
	bin   string
	dir   string
	creds map[string]string
}

func (k *kit) mash(who string, stdin string, args ...string) error {
	home := filepath.Join(k.dir, "home", who)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	cmd := exec.Command(k.bin, args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "MASH_STATE_DIR=" + home + "/state",
		"MASH_ENDPOINT=" + k.e.Owner + ".dev." + who, "MASH_SERVER=" + k.e.URL, "MASH_CREDS=" + k.creds[who],
		"MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1"}
	cmd.Stdin = strings.NewReader(stdin)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			line, _, _ := strings.Cut(strings.TrimSpace(strings.ReplaceAll(out.String(), "warning: no encryption to the server (MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION)\n", "")), "\n")
			return fmt.Errorf("%v: %s", err, line)
		}
		return nil
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill() // this command's own PID, recorded by os/exec
		<-done
		return fmt.Errorf("timed out")
	}
}

func run(bin, tag string) (*result, error) {
	dir, err := os.MkdirTemp("", "storedmail-"+tag+"-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(bin, 0o755); err != nil {
		copyTo := filepath.Join(dir, "mash")
		b, rerr := os.ReadFile(bin)
		if rerr != nil || os.WriteFile(copyTo, b, 0o755) != nil {
			return nil, fmt.Errorf("binary %s not usable", bin)
		}
		bin = copyTo
	}
	e, stop, err := testenv.Launch(filepath.Join(dir, "srv"))
	if err != nil {
		return nil, err
	}
	defer stop()
	ctx := context.Background()
	anc, err := nats.Connect(e.URL, natsauth.UserCredentials(e.AdminCreds))
	if err != nil {
		return nil, err
	}
	defer anc.Close()
	adm, err := admin.New(anc, e.Owner)
	if err != nil {
		return nil, err
	}
	if err := adm.Init(ctx); err != nil {
		return nil, err
	}
	k := &kit{e: e, bin: bin, dir: dir, creds: map[string]string{}}
	addrs := map[string]sap.Address{}
	for _, name := range []string{"alice", "bob"} {
		a, err := sap.NewAddress(e.Owner, "dev", name)
		if err != nil {
			return nil, err
		}
		creds, pub, err := e.MintEndpoint(a)
		if err != nil {
			return nil, err
		}
		if _, err := adm.Provision(ctx, a, pub, e.Account, "", admin.Meta{}); err != nil {
			return nil, err
		}
		k.creds[name], addrs[name] = creds, a
	}
	r := &result{tag: tag, trust: map[string]int{}, class: map[string]int{}}
	if err := k.sendAll(r, "alice", "bob"); err != nil {
		return nil, err
	}
	js, err := jetstream.New(anc)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"alice", "bob"} {
		if err := check(ctx, js, addrs[name], r); err != nil {
			return nil, err
		}
	}
	if ok := r.sends - r.refused; r.msgs < ok || r.msgs < 2 {
		return nil, fmt.Errorf("%d messages stored, but %d of %d sends succeeded (need at least that many, and 2)", r.msgs, ok, r.sends)
	}
	selfCheck(r)
	return r, nil
}

// sendAll has each side send every kind this CLI writes. The two plain sends every release supports must succeed, or it
// returns an error; a refused flag-dependent send (a flag this release lacks) is counted and noted, not fatal.
func (k *kit) sendAll(r *result, a, b string) error {
	var basicErr error
	to := func(n string) string { return k.e.Owner + ".dev." + n }
	long := strings.Repeat("long line of text, with unicode é世界\U0001F600 and tabs\t\n", 150)
	send := func(basic bool, who, stdin string, args ...string) {
		r.sends++
		if err := k.mash(who, stdin, append([]string{"send"}, args...)...); err != nil {
			if basic {
				r.refused++
				if basicErr == nil {
					basicErr = fmt.Errorf("basic send %v failed: %w", args, err)
				}
				return
			}
			r.refused++
			fmt.Fprintf(os.Stderr, "note: %s send %v refused: %v\n", r.tag, args, err)
		}
	}
	send(true, a, "", to(b), "hello bob")
	send(false, a, "", to(b), "unicode é世界 \U0001F600 ‮ rtl", "--kind", "fyi")
	send(false, a, ".", to(b), "--file", "-")
	send(false, a, long, to(b), "--file", "-", "--kind", "result")
	send(false, a, "", to(b), "urgent one", "--urgent", "--kind", "blocker")
	send(false, a, "", to(b), "keyed", "--id", "storedmail-1", "--kind", "question")
	send(true, b, "", to(a), "hello alice")
	send(false, b, "", to(a), "reply", "--reply-to", "1")
	send(false, b, "", to(a), "unicode é世界", "--kind", "fyi")
	send(false, a, "", to(b), "relayed", "--relay", "1")
	return basicErr
}

// check reads every message of one inbox stream and classifies it, with its header and without.
func check(ctx context.Context, js jetstream.JetStream, a sap.Address, r *result) error {
	s, err := js.Stream(ctx, a.InboxStream())
	if err != nil {
		return fmt.Errorf("stream %s: %w", a.InboxStream(), err)
	}
	st := s.CachedInfo().State
	for seq := st.FirstSeq; seq != 0 && seq <= st.LastSeq; seq++ {
		m, err := s.GetMsg(ctx, seq)
		if err != nil {
			if errors.Is(err, jetstream.ErrMsgNotFound) {
				continue
			}
			return fmt.Errorf("get %s seq %d: %w", a.InboxStream(), seq, err)
		}
		r.msgs++
		vals := m.Header.Values(sap.HeaderName)
		trust, class := sapsdk.Classify(m.Subject, vals, m.Data)
		r.trust[string(trust)]++
		if class != "" {
			r.class[class]++
		}
		why := candidate(vals, trust, class, m.Subject)
		if class != "" && why == "" {
			why = "class outside the CHG-4 baseline"
		}
		if trust != sap.TrustVerified && why == "" {
			why = "trust " + string(trust) + ", expected verified"
		}
		if why != "" {
			r.failures = append(r.failures, fmt.Sprintf("%s: class %q trust %s stream %s seq %d header %s", why, class, trust, a.InboxStream(), seq, shape(vals)))
		}
		if t, c := sapsdk.Classify(m.Subject, nil, m.Data); t != sap.TrustAbsent || c != "" {
			r.failures = append(r.failures, fmt.Sprintf("control (no Agent-Sender) gave %s %q at %s seq %d", t, c, a.InboxStream(), seq))
		}
		r.control++
		if len(vals) == 1 { // forged on purpose: the body changed after signing
			if t, c := sapsdk.Classify(m.Subject, vals, append(append([]byte{}, m.Data...), ' ')); t != sap.TrustForged || c != sapsdk.ClassNotVerified {
				r.failures = append(r.failures, fmt.Sprintf("harness-forged copy gave %s %q at %s seq %d", t, c, a.InboxStream(), seq))
			}
			r.forged++
		}
	}
	return nil
}

var ctl = regexp.MustCompile(`[\x00-\x1f\x7f]`)

// candidate names the eleventh-shape candidate a header is, or "".
func candidate(vals []string, trust sap.Trust, class, subject string) string {
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

// shape is a header with every value removed: member names, kinds and lengths only. No body, no key, no signature.
func shape(vals []string) string {
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

// selfCheck proves the detector sees what it is for, so a clean table is not a blind one.
func selfCheck(r *result) {
	const hdr = `{"v":1.0,"account":"A","user":"U","sub":"agents.mail.x"}`
	if candidate([]string{hdr}, sap.TrustVerified, "", "s") == "" {
		r.failures = append(r.failures, "self-check: v 1.0 not seen as a candidate")
	}
	if candidate([]string{`{"v":1,"sub":"a..b"}`}, sap.TrustVerified, "", "s") == "" {
		r.failures = append(r.failures, "self-check: empty sub token not seen as a candidate")
	}
	if candidate([]string{"{\"v\":1,\"x\":\"\xff\"}"}, sap.TrustVerified, "", "s") == "" {
		r.failures = append(r.failures, "self-check: invalid UTF-8 not seen as a candidate")
	}
}
