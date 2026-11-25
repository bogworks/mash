// Command at006 is the stored-mail proof of AT-006 (REQ-007) and its X1 to X3 condition. Run it by
// scripts/storedmail/at006.sh, inside a throwaway container: it derives the release list from the published releases,
// downloads each release's binary and checks its digest, has each release's own `mash` write stored mail, reads that
// mail with this tree's endpoint code and `mash inbox`, and counts the X1 to X3 candidates.
//
//	at006 -mash <this tree's mash> -skipped <docs/skipped-releases.txt> -work <dir>
//
// Env: AT006_TAGS (space-separated tags) replaces the derived list, for debugging only: the run then ends non-zero.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/sapsdk"
	"github.com/bogworks/mash/scripts/storedmail/kit"
)

const releasesURL = "https://api.github.com/repos/bogworks/mash/releases?per_page=100"

type apiRelease struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name   string `json:"name"`
		Digest string `json:"digest"`
		URL    string `json:"browser_download_url"`
	} `json:"assets"`
}

type release struct {
	tag, sha, digest, url string
	bin                   string
}

// report is what one release's run leaves for the receipt.
type report struct {
	tag                         string
	sends, refusedSends, stored int
	delivered, refusals         int
	inbox                       int
	candidates                  int
	headers                     map[string]int
	notes, failures             []string
}

func main() {
	treeMash := flag.String("mash", "", "this tree's mash binary")
	skippedFile := flag.String("skipped", "docs/skipped-releases.txt", "the skipped-releases file")
	work := flag.String("work", "", "a directory this run may create files in (it is the container's)")
	flag.Parse()
	if *treeMash == "" || *work == "" {
		fmt.Fprintln(os.Stderr, "usage: at006 -mash <bin> -skipped <file> -work <dir>")
		os.Exit(2)
	}
	override := strings.Fields(os.Getenv("AT006_TAGS"))
	rels, err := derive(*skippedFile, override, filepath.Join(*work, "dl"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("RELEASES (derived at run time from " + releasesURL + "):")
	for _, r := range rels {
		fmt.Printf("  %s  sha256 %s  (%s)\n", r.tag, r.sha, r.digest)
	}
	var reps []*report
	bad := false
	for _, r := range rels {
		rep := runRelease(r, *treeMash, filepath.Join(*work, "run"))
		reps = append(reps, rep)
		bad = bad || len(rep.failures) > 0
	}
	fmt.Println()
	fmt.Printf("%-8s %5s %6s %6s %9s %8s %5s %10s  %s\n", "release", "sends", "refsnd", "stored", "delivered", "refusals", "inbox", "X1-X3", "Mash-* headers stored")
	for _, p := range reps {
		fmt.Printf("%-8s %5d %6d %6d %9d %8d %5d %10d  %s\n", p.tag, p.sends, p.refusedSends, p.stored, p.delivered, p.refusals, p.inbox, p.candidates, counts(p.headers))
	}
	for _, p := range reps {
		for _, n := range p.notes {
			fmt.Printf("NOTE %s: %s\n", p.tag, n)
		}
		for _, f := range p.failures {
			fmt.Printf("FAIL %s: %s\n", p.tag, f)
		}
	}
	if len(override) > 0 {
		fmt.Println("OVERRIDE: AT006_TAGS is set; this is not a proof run. Exiting non-zero on purpose.")
		os.Exit(1)
	}
	if bad {
		fmt.Println("RESULT: FAIL")
		os.Exit(1)
	}
	fmt.Println("RESULT: PASS (every release's stored mail read with zero refusals; X1 to X3 count 0)")
}

func counts(m map[string]int) string {
	var ks []string
	for k, n := range m {
		ks = append(ks, fmt.Sprintf("%s=%d", k, n))
	}
	sort.Strings(ks)
	return strings.Join(ks, ",")
}

var tagRE = regexp.MustCompile(`^v0\.5\.(\d+)$`)

// derive is REQ-007: every published release v0.5.N, N >= 8, minus the skipped list; fetched here, in one call. It
// downloads each mash-linux-amd64 into dir and checks its digest.
func derive(skippedFile string, override []string, dir string) ([]release, error) {
	skipped, err := readSkipped(skippedFile)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequest("GET", releasesURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "storedmail-check")
	hc := &http.Client{Timeout: 60 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("releases list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return nil, fmt.Errorf("releases list: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var all []apiRelease
	if err := json.NewDecoder(resp.Body).Decode(&all); err != nil {
		return nil, fmt.Errorf("releases list: %w", err)
	}
	byTag := map[string]apiRelease{}
	reachedFloor := false
	for _, a := range all {
		byTag[a.Tag] = a
		if m := tagRE.FindStringSubmatch(a.Tag); m == nil || func() bool { n, _ := strconv.Atoi(m[1]); return n < 8 }() {
			reachedFloor = true // an older release is on the page: the page reaches below 0.5.8
		}
	}
	if len(all) >= 100 && !reachedFloor {
		return nil, errors.New("the releases page is full and holds nothing below v0.5.8: the list may be cut off")
	}
	var tags []string
	if len(override) > 0 {
		fmt.Println("OVERRIDE: AT006_TAGS is set, so this run is not a proof run and will exit non-zero.")
		tags = override
	} else {
		type nt struct {
			n   int
			tag string
		}
		var l []nt
		for _, a := range all {
			m := tagRE.FindStringSubmatch(a.Tag)
			if m == nil || a.Draft || a.Prerelease || skipped[a.Tag] {
				continue
			}
			if n, _ := strconv.Atoi(m[1]); n >= 8 {
				l = append(l, nt{n, a.Tag})
			}
		}
		sort.Slice(l, func(i, j int) bool { return l[i].n < l[j].n })
		for _, x := range l {
			tags = append(tags, x.tag)
		}
	}
	if len(tags) == 0 {
		return nil, errors.New("no release derived: the check would prove nothing")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var out []release
	for _, t := range tags {
		r := release{tag: t, url: "https://github.com/bogworks/mash/releases/download/" + t + "/mash-linux-amd64", digest: "no digest in the release data"}
		want := ""
		if a, ok := byTag[t]; ok {
			found := false
			for _, as := range a.Assets {
				if as.Name == "mash-linux-amd64" {
					found, r.url, want = true, as.URL, as.Digest
				}
			}
			if !found {
				return nil, fmt.Errorf("%s has no mash-linux-amd64 asset", t)
			}
		}
		if err := os.MkdirAll(filepath.Join(dir, t), 0o755); err != nil {
			return nil, err
		}
		r.bin = filepath.Join(dir, t, "mash-linux-amd64")
		if r.sha, err = download(hc, r.url, r.bin); err != nil {
			return nil, fmt.Errorf("%s: %w", t, err)
		}
		if want != "" {
			if want != "sha256:"+r.sha {
				return nil, fmt.Errorf("sha256 mismatch for %s: got %s, the release says %s", t, r.sha, want)
			}
			r.digest = "matches the release digest " + want
		}
		out = append(out, r)
	}
	return out, nil
}

func readSkipped(p string) (map[string]bool, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	m := map[string]bool{}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		m[strings.Fields(l)[0]] = true
	}
	return m, nil
}

func download(hc *http.Client, url, to string) (string, error) {
	resp, err := hc.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download %s: %s", url, resp.Status)
	}
	f, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o755)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// The endpoints of one release's world. carol holds a lease with a launch token while it sends, so its mail carries the
// signed instance; longA and longB have tokens at the length limits; every endpoint is a recipient too.
func names() []kit.Name {
	long := func(c string, n int) string { return strings.Repeat(c, n) }
	// owner "o" + group + instance + 2 separators is at most MaxIdentityLen; the instance of the second fills the rest.
	groupB := long("g", sap.MaxTokenLen)
	instB := long("i", sap.MaxIdentityLen-len("o")-2-len(groupB))
	return []kit.Name{
		{Label: "alice", Group: "dev", Instance: "alice"},
		{Label: "bob", Group: "dev", Instance: "bob"},
		{Label: "carol", Group: "dev", Instance: "carol"},
		{Label: "longA", Group: "dev", Instance: long("a", sap.MaxTokenLen)},
		{Label: "longB", Group: groupB, Instance: instB},
	}
}

const launchToken = "at006-launch-token"

func runRelease(r release, treeMash, workRoot string) *report {
	rep := &report{tag: r.tag, headers: map[string]int{}}
	fail := func(f string, a ...any) { rep.failures = append(rep.failures, fmt.Sprintf(f, a...)) }
	if err := os.MkdirAll(workRoot, 0o755); err != nil {
		fail("%v", err)
		return rep
	}
	dir, err := os.MkdirTemp(workRoot, r.tag+"-") // this run's own directory, removed by name below
	if err != nil {
		fail("%v", err)
		return rep
	}
	defer os.RemoveAll(dir)
	k, err := kit.New(dir, r.bin, r.tag, names())
	if err != nil {
		fail("start: %v", err)
		return rep
	}
	defer k.Close()
	ctx := context.Background()

	// carol holds a lease carrying the launch token while her sends are written, so a release that signs the sender's
	// instance does. Open takes the lease and consumes nothing.
	k.Env["carol"] = map[string]string{"MASH_LAUNCH_TOKEN": launchToken}
	cep, err := endpoint.Open(ctx, cfgFor(k, "carol", filepath.Join(dir, "state-lease"), launchToken))
	if err != nil {
		fail("carol's lease: %v", err)
		return rep
	}
	sendSet(k)
	cep.Close()
	if err := k.BasicErr(); err != nil {
		fail("%v", err)
	}
	rep.sends, rep.refusedSends = k.Sends, k.Refused
	rep.notes = append(rep.notes, k.Notes...)

	stored, err := readStreams(ctx, k)
	if err != nil {
		fail("%v", err)
		return rep
	}
	for _, label := range sortedKeys(stored) {
		rep.stored += len(stored[label])
	}
	if ok := rep.sends - rep.refusedSends; rep.stored != ok {
		fail("%d messages stored, but %d sends succeeded", rep.stored, ok)
	}
	matchLedger(k, stored, rep)
	classifyStored(stored, rep)
	for _, f := range kit.SelfCheck() {
		fail("%s", f)
	}
	for _, label := range sortedKeys(stored) {
		readAs(ctx, k, label, stored[label], treeMash, dir, rep)
	}
	return rep
}

func cfgFor(k *kit.Kit, label, state, token string) config.Config {
	a := k.Addrs[label]
	return config.Config{URL: k.E.URL, SkipEncryption: true, Owner: a.Owner, Group: a.Group, Name: a.Instance, Creds: k.Creds[label],
		AcceptUnverified: false, StateDir: state, SettleMS: 50, BatchMsgs: 1000, BatchBytes: 64 << 20, LaunchToken: token}
}

// sendSet is classify's set plus every Mash-* signature, tokens at the length limits and non-ASCII text.
func sendSet(k *kit.Kit) {
	k.SendAll("alice", "bob")
	k.Send(true, kit.Sent{From: "alice", To: "carol", Body: "hello carol"})
	// every mark together, from the plain sender
	k.Send(false, kit.Sent{From: "alice", To: "bob", Body: "all marks, plain sender", Kind: "blocker", Urgent: true, ReplyTo: 1, Relay: 1})
	k.Send(false, kit.Sent{From: "bob", To: "alice", Body: "is this answered, and urgent?", Kind: "question", Urgent: true, ReplyTo: 1})
	// the instance mark, and the marks with it
	k.Send(false, kit.Sent{From: "carol", To: "bob", Body: "carol, instance only"})
	k.Send(false, kit.Sent{From: "carol", To: "bob", Body: "carol, every mark", Kind: "task", Urgent: true, ReplyTo: 1, Relay: 1})
	k.Send(false, kit.Sent{From: "carol", To: "alice", Body: "carol, urgent", Urgent: true, Kind: "blocker"})
	// non-ASCII where mash carries it: the body, and the send id (the nonce) if the release lets it
	k.Send(false, kit.Sent{From: "alice", To: "bob", Body: "é ​‍ zero-width, combining, CJK 日本語, Arabic مرحبا, emoji 👩‍💻"})
	k.Send(false, kit.Sent{From: "bob", To: "alice", Body: "multi-line\nпривет\n世界\n\ttabbed\n"})
	k.Send(false, kit.Sent{From: "alice", To: "bob", Body: "unicode id", ID: "ключ-é世"})
	// names at the length limits, as sender and as recipient
	k.Send(true, kit.Sent{From: "longA", To: "longB", Body: "limit: longA to longB"})
	k.Send(true, kit.Sent{From: "longB", To: "longA", Body: "limit: longB to longA"})
	k.Send(false, kit.Sent{From: "alice", To: "longA", Body: "limit: alice to longA", Urgent: true})
	k.Send(false, kit.Sent{From: "longB", To: "bob", Body: "limit: longB to bob", Kind: "result", ReplyTo: 1})
	k.Send(false, kit.Sent{From: "bob", To: "longB", Body: "limit: bob to longB, unicode é世界", Kind: "fyi"})
}

type storedMsg struct {
	seq     uint64
	data    []byte
	header  map[string][]string
	subject string
	to      string // label of the stream
	led     *kit.Sent
}

func sortedKeys[V any](m map[string]V) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// readStreams reads every message of every endpoint's inbox stream.
func readStreams(ctx context.Context, k *kit.Kit) (map[string][]*storedMsg, error) {
	js, err := jetstream.New(k.Conn)
	if err != nil {
		return nil, err
	}
	out := map[string][]*storedMsg{}
	for label, a := range k.Addrs {
		s, err := js.Stream(ctx, a.InboxStream())
		if err != nil {
			return nil, fmt.Errorf("stream %s: %w", a.InboxStream(), err)
		}
		st := s.CachedInfo().State
		for seq := st.FirstSeq; seq != 0 && seq <= st.LastSeq; seq++ {
			m, err := s.GetMsg(ctx, seq)
			if err != nil {
				if errors.Is(err, jetstream.ErrMsgNotFound) {
					continue
				}
				return nil, fmt.Errorf("get %s #%d: %w", a.InboxStream(), seq, err)
			}
			out[label] = append(out[label], &storedMsg{seq: seq, data: m.Data, header: m.Header, subject: m.Subject, to: label})
		}
		if n := uint64(len(out[label])); n != st.Msgs {
			return nil, fmt.Errorf("stream %s holds %d, read %d", a.InboxStream(), st.Msgs, n)
		}
	}
	return out, nil
}

// matchLedger ties each stored message to the send that wrote it (by recipient and body), and checks that each send that
// succeeded is stored once, that the marks it asked for are on the message, and counts the Mash-* headers present.
func matchLedger(k *kit.Kit, stored map[string][]*storedMsg, rep *report) {
	used := map[*storedMsg]bool{}
	for i := range k.Ledger {
		s := &k.Ledger[i]
		var hit []*storedMsg
		for _, m := range stored[s.To] {
			// mash cuts the trailing newlines of a body it reads from a file
			if (bytes.Equal(m.data, []byte(s.Body)) || bytes.Equal(m.data, []byte(strings.TrimRight(s.Body, "\n")))) && !used[m] {
				hit = append(hit, m)
			}
		}
		if len(hit) != 1 {
			rep.failures = append(rep.failures, fmt.Sprintf("send to %s %q: %d stored messages match", s.To, short(s.Body), len(hit)))
			continue
		}
		used[hit[0]] = true
		hit[0].led = s
		h := hit[0].header
		if s.Kind != "" && first(h, endpoint.KindHeader) != s.Kind {
			rep.failures = append(rep.failures, fmt.Sprintf("send to %s %q asked kind %s, stored kind %q", s.To, short(s.Body), s.Kind, first(h, endpoint.KindHeader)))
		}
		if s.Urgent && first(h, endpoint.UrgentHeader) == "" {
			rep.failures = append(rep.failures, fmt.Sprintf("send to %s %q asked urgent, no %s stored", s.To, short(s.Body), endpoint.UrgentHeader))
		}
		if s.ReplyTo != 0 && !strings.HasPrefix(first(h, endpoint.InReplyToHeader), strconv.Itoa(s.ReplyTo)+".") {
			rep.failures = append(rep.failures, fmt.Sprintf("send to %s %q asked reply-to %d, stored %q", s.To, short(s.Body), s.ReplyTo, "<"+endpoint.InReplyToHeader+">"))
		}
		if s.Relay != 0 && first(h, endpoint.ViaHeader) == "" {
			rep.failures = append(rep.failures, fmt.Sprintf("send to %s %q asked relay, no %s stored", s.To, short(s.Body), endpoint.ViaHeader))
		}
	}
	for _, to := range sortedKeys(stored) {
		for _, m := range stored[to] {
			if m.led == nil {
				rep.failures = append(rep.failures, fmt.Sprintf("stored message %s #%d was written by no send", to, m.seq))
			}
			for _, name := range []string{endpoint.KindHeader, endpoint.UrgentHeader, endpoint.InReplyToHeader, endpoint.ViaHeader, endpoint.InstanceHeader} {
				if first(m.header, name) != "" {
					rep.headers[strings.TrimPrefix(name, "Mash-")]++
				}
			}
		}
	}
	for _, n := range []string{"Urgent", "In-Reply-To", "Via", "Sender-Instance"} {
		if rep.headers[n] == 0 {
			rep.failures = append(rep.failures, "no stored message carries Mash-"+n+": AT-006 needs every Mash-* signature in each release's mail")
		}
	}
}

func first(h map[string][]string, name string) string {
	if v := h[name]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func short(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if r := []rune(s); len(r) > 30 {
		return string(r[:30]) + "..."
	}
	return s
}

// classifyStored runs the SDK classifier over the stored messages for the X1 to X3 count: a message whose header is any
// case of TestEleventhShapeCandidates. It also requires every message to be verified with no class.
func classifyStored(stored map[string][]*storedMsg, rep *report) {
	for _, to := range sortedKeys(stored) {
		for _, m := range stored[to] {
			vals := m.header[sap.HeaderName]
			trust, class := sapsdk.Classify(m.subject, vals, m.data)
			if why := kit.Candidate(vals, trust, class, m.subject); why != "" {
				rep.candidates++
				rep.failures = append(rep.failures, fmt.Sprintf("%s: %s #%d, class %q, header %s: goes back to the planner", why, to, m.seq, class, kit.Shape(vals)))
			}
			if trust != sap.TrustVerified || class != "" {
				rep.failures = append(rep.failures, fmt.Sprintf("classifier gave %s %q for %s #%d", trust, class, to, m.seq))
			}
		}
	}
}

type recorder struct {
	mu    sync.Mutex
	texts []string
	seqs  []string
}

func (r *recorder) deliver(text string, meta map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.texts = append(r.texts, text)
	r.seqs = append(r.seqs, meta["seqs"])
	return nil
}

func (r *recorder) snapshot() ([]string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.texts...), append([]string(nil), r.seqs...)
}

var blockHead = regexp.MustCompile(`(?m)^--- #(\d+) from (.*) \[([^\]]+)\] at (\S+)$`)

type block struct {
	seq         uint64
	trust, kind string
	urgent      bool
	answers     string
	relayed     bool
	instance    bool
	quote       string
}

// parseBlocks reads the full-form messages out of the delivered texts.
func parseBlocks(texts []string) []block {
	var out []block
	for _, t := range texts {
		idx := blockHead.FindAllStringSubmatchIndex(t, -1)
		for i, ix := range idx {
			end := len(t)
			if i+1 < len(idx) {
				end = idx[i+1][0]
			}
			b := block{trust: t[ix[6]:ix[7]]}
			b.seq, _ = strconv.ParseUint(t[ix[2]:ix[3]], 10, 64)
			rest := strings.TrimPrefix(t[ix[1]:end], "\n")
			lines := strings.Split(rest, "\n")
			n := 0
			for ; n < len(lines) && !strings.HasPrefix(lines[n], "> ") && lines[n] != ">"; n++ {
				switch l := lines[n]; {
				case strings.HasPrefix(l, "kind: "):
					b.kind = strings.TrimPrefix(l, "kind: ")
				case l == "urgent: yes":
					b.urgent = true
				case strings.HasPrefix(l, "answers #"):
					b.answers = strings.TrimPrefix(l, "answers #")
				case strings.HasPrefix(l, "relayed from "):
					b.relayed = true
				case strings.HasPrefix(l, "instance: "):
					b.instance = true
				}
			}
			b.quote = strings.TrimRight(strings.Join(lines[n:], "\n"), "\n")
			out = append(out, b)
		}
	}
	return out
}

// readAs opens label's endpoint with this tree's code and a fresh state directory, waits for the one-time scan and the
// delivery of every stored message, and checks them; then lists the inbox with this tree's `mash inbox --json`.
func readAs(ctx context.Context, k *kit.Kit, label string, want []*storedMsg, treeMash, dir string, rep *report) {
	fail := func(f string, a ...any) {
		rep.failures = append(rep.failures, fmt.Sprintf("%s: ", label)+fmt.Sprintf(f, a...))
	}
	state := filepath.Join(dir, "state-read-"+label)
	cfg := cfgFor(k, label, state, "")
	rctx, cancel := context.WithCancel(ctx)
	ep, err := endpoint.Open(rctx, cfg)
	if err != nil {
		cancel()
		fail("open: %v", err)
		return
	}
	rec := &recorder{}
	done := make(chan error, 1)
	go func() { done <- ep.Run(rctx, rec.deliver) }()
	spoolDir, err := endpoint.SpoolDir(cfg)
	if err != nil {
		cancel()
		<-done
		ep.Close()
		fail("state dir: %v", err)
		return
	}
	malformed, unverified := filepath.Join(spoolDir, "refused-malformed.seqs"), filepath.Join(spoolDir, "refused-unverified.seqs")
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			fail("the endpoint stopped: %v", err)
			done <- err
			deadline = time.Time{}
		default:
		}
		_, seqs := rec.snapshot()
		if scanned(malformed) && !ep.StoredScanRunning() && deliveredCount(seqs) >= len(want) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(2 * time.Second) // nothing further may arrive: an extra delivery would show as a duplicate
	texts, seqs := rec.snapshot()
	cancel()
	<-done
	ep.Close()

	if !scanned(malformed) {
		fail("the one-time scan did not finish (no scanned line)")
	}
	if n := entries(malformed) + entries(unverified); n != 0 {
		fail("%d refusal entries (malformed %d, unverified %d)", n, entries(malformed), entries(unverified))
	}
	rep.refusals += entries(malformed) + entries(unverified)
	blocks := parseBlocks(texts)
	meta := map[uint64]int{}
	for _, s := range seqs {
		for _, f := range strings.Split(s, ",") {
			if n, err := strconv.ParseUint(strings.TrimSpace(f), 10, 64); err == nil {
				meta[n]++
			}
		}
	}
	shown := map[uint64][]block{}
	for _, b := range blocks {
		shown[b.seq] = append(shown[b.seq], b)
	}
	have := map[uint64]bool{}
	for _, m := range want {
		have[m.seq] = true
		bs := shown[m.seq]
		switch {
		case len(bs) != 1 || meta[m.seq] != 1:
			fail("#%d delivered %d times (event list %d): want exactly once", m.seq, len(bs), meta[m.seq])
			continue
		}
		b := bs[0]
		wantKind := first(m.header, endpoint.KindHeader)
		if wantKind == "" {
			wantKind = "task"
		}
		if b.trust != string(sap.TrustVerified) {
			fail("#%d trust %s, want verified", m.seq, b.trust)
		}
		if b.kind != wantKind {
			fail("#%d kind %q, want %q", m.seq, b.kind, wantKind)
		}
		if q := render.Quote(m.data); b.quote != strings.TrimRight(q, "\n") {
			fail("#%d body differs: shown %q, stored %q", m.seq, short(b.quote), short(q))
		}
		if b.urgent != (first(m.header, endpoint.UrgentHeader) != "") {
			fail("#%d urgent %v, stored header says %v", m.seq, b.urgent, first(m.header, endpoint.UrgentHeader) != "")
		}
		if b.instance != (first(m.header, endpoint.InstanceHeader) != "") {
			fail("#%d instance shown %v, stored header says %v", m.seq, b.instance, first(m.header, endpoint.InstanceHeader) != "")
		}
		if b.relayed != (first(m.header, endpoint.ViaHeader) != "") {
			fail("#%d relayed shown %v, stored header says %v", m.seq, b.relayed, first(m.header, endpoint.ViaHeader) != "")
		}
		wantAns := ""
		if h := first(m.header, endpoint.InReplyToHeader); h != "" {
			wantAns, _, _ = strings.Cut(h, ".")
		}
		if b.answers != wantAns {
			fail("#%d answers %q, stored %q", m.seq, b.answers, wantAns)
		}
	}
	for seq := range shown {
		if !have[seq] {
			fail("#%d was delivered but is not in the stream", seq)
		}
	}
	rep.delivered += len(shown)

	// The listing, by this tree's own mash, over the same state directory.
	out, err := k.MashBin(treeMash, label, state, "", "inbox", "--json", "--limit", "100")
	if err != nil {
		fail("mash inbox --json: %v", err)
		return
	}
	var inbox struct {
		Total    int `json:"total"`
		Messages []struct {
			Number int    `json:"number"`
			Trust  string `json:"trust"`
			State  string `json:"state"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out), &inbox); err != nil {
		fail("mash inbox --json: not JSON: %v", err)
		return
	}
	if inbox.Total != len(want) || len(inbox.Messages) != len(want) {
		fail("mash inbox lists %d of total %d, want %d", len(inbox.Messages), inbox.Total, len(want))
	}
	listed := map[int]bool{}
	for _, m := range inbox.Messages {
		listed[m.Number] = true
		if m.Trust != string(sap.TrustVerified) || strings.Contains(m.State, "refus") {
			fail("mash inbox: #%d trust %q state %q", m.Number, m.Trust, m.State)
		}
	}
	for _, m := range want {
		if !listed[int(m.seq)] {
			fail("mash inbox does not list #%d", m.seq)
		}
	}
	rep.inbox += len(inbox.Messages)
}

func deliveredCount(seqs []string) int {
	n := 0
	for _, s := range seqs {
		for _, f := range strings.Split(s, ",") {
			if strings.TrimSpace(f) != "" {
				n++
			}
		}
	}
	return n
}

// scanned reports whether the list file has its "scanned" line (the one-time scan finished).
func scanned(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "scanned ") {
			return true
		}
	}
	return false
}

// entries counts the entry lines of a list file: a header ("stream ...") and the "scanned" line are not entries, and a
// missing file has none.
func entries(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "stream ") || strings.HasPrefix(l, "scanned ") {
			continue
		}
		n++
	}
	return n
}
