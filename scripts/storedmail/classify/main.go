// Command classify runs mail stored by released mash binaries through sapsdk.Classify. Run it by scripts/storedmail/run.sh,
// inside a throwaway container: it starts a NATS server, runs each release's own `mash` as alice and bob, reads every
// stored message and fails on an eleventh-shape candidate or on any class the old mash would not give its own header.
//
//	classify <bindir> <tag>...      bindir/<tag>/mash-linux-amd64
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/sapsdk"
	"github.com/bogworks/mash/scripts/storedmail/kit"
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

func run(bin, tag string) (*result, error) {
	if os.Getenv("MASH_STOREDMAIL_IN_CONTAINER") != "1" { // this program removes its scratch dir and runs released binaries: container only
		return nil, fmt.Errorf("refusing to run outside the container (use scripts/storedmail/run.sh)")
	}
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
	k, err := kit.New(dir, bin, tag, []kit.Name{{Label: "alice", Group: "dev", Instance: "alice"}, {Label: "bob", Group: "dev", Instance: "bob"}})
	if err != nil {
		return nil, err
	}
	defer k.Close()
	ctx := context.Background()
	r := &result{tag: tag, trust: map[string]int{}, class: map[string]int{}}
	k.SendAll("alice", "bob")
	if err := k.BasicErr(); err != nil {
		return nil, err
	}
	r.sends, r.refused = k.Sends, k.Refused
	js, err := jetstream.New(k.Conn)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"alice", "bob"} {
		if err := check(ctx, js, k.Addrs[name], r); err != nil {
			return nil, err
		}
	}
	if ok := r.sends - r.refused; r.msgs < ok || r.msgs < 2 {
		return nil, fmt.Errorf("%d messages stored, but %d of %d sends succeeded (need at least that many, and 2)", r.msgs, ok, r.sends)
	}
	r.failures = append(r.failures, kit.SelfCheck()...)
	return r, nil
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
		why := kit.Candidate(vals, trust, class, m.Subject)
		if class != "" && why == "" {
			why = "class outside the CHG-4 baseline"
		}
		if trust != sap.TrustVerified && why == "" {
			why = "trust " + string(trust) + ", expected verified"
		}
		if why != "" {
			r.failures = append(r.failures, fmt.Sprintf("%s: class %q trust %s stream %s seq %d header %s", why, class, trust, a.InboxStream(), seq, kit.Shape(vals)))
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
