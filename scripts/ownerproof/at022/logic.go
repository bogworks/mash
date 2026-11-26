package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/sapsdk"
)

// ParseSums reads a SHA256SUMS file ("<hex>  <name>" per line) into name -> digest.
func ParseSums(text string) (map[string]string, error) {
	m := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 || len(f[0]) != 64 {
			return nil, fmt.Errorf("SHA256SUMS: unreadable line %q", sc.Text())
		}
		m[strings.TrimPrefix(f[1], "*")] = f[0]
	}
	return m, sc.Err()
}

// SamePermissions returns "" when got carries exactly the allow and deny lists of want and no response permission,
// and otherwise says which list differs. The order of a list does not matter.
func SamePermissions(who string, got, want *jwt.Permissions, resp *jwt.ResponsePermission) string {
	if want == nil || got == nil {
		return who + ": a permission set is missing"
	}
	if resp != nil {
		return fmt.Sprintf("%s: the JWT carries a response permission %+v", who, *resp)
	}
	for _, l := range []struct {
		name     string
		got, exp []string
	}{
		{"publish allow", got.Pub.Allow, want.Pub.Allow},
		{"subscribe allow", got.Sub.Allow, want.Sub.Allow},
		{"publish deny", got.Pub.Deny, want.Pub.Deny},
		{"subscribe deny", got.Sub.Deny, want.Sub.Deny},
	} {
		g, w := append([]string(nil), l.got...), append([]string(nil), l.exp...)
		sort.Strings(g)
		sort.Strings(w)
		if strings.Join(g, "\n") != strings.Join(w, "\n") {
			return fmt.Sprintf("%s: %s is %q, admin.Permissions gives %q", who, l.name, g, w)
		}
	}
	return ""
}

// Violated reports whether errs holds a server permissions violation of kind ("subscription" or "publish") that names
// subject.
func Violated(errs []string, kind, subject string) bool {
	want, sub := "permissions violation for "+kind, strings.ToLower(subject)
	for _, e := range errs {
		l := strings.ToLower(e)
		if strings.Contains(l, want) && strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// Violations are the permission violations among errs.
func Violations(errs []string) []string {
	var out []string
	for _, e := range errs {
		if strings.Contains(strings.ToLower(e), "permissions violation") {
			out = append(out, e)
		}
	}
	return out
}

// Errors is a goroutine-safe list of the errors a connection reported.
type Errors struct {
	mu   sync.Mutex
	list []string
}

// Add records one error.
func (e *Errors) Add(s string) { e.mu.Lock(); e.list = append(e.list, s); e.mu.Unlock() }

// All returns a copy of the list.
func (e *Errors) All() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.list...)
}

const nuidDigits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// NextNUIDs lists the NUID tokens that can follow tok (12-character prefix and a 10-character base-62 sequence, steps
// from..to). A token that is not 22 characters gives none.
func NextNUIDs(tok string, from, to int) []string {
	if len(tok) != 22 {
		return nil
	}
	var seq int64
	for _, c := range tok[12:] {
		seq = seq*62 + int64(strings.IndexRune(nuidDigits, c))
	}
	var out []string
	for k := from; k <= to; k++ {
		n, b := seq+int64(k), make([]byte, 10)
		for i := 9; i >= 0; i-- {
			b[i], n = nuidDigits[n%62], n/62
		}
		out = append(out, tok[:12]+string(b))
	}
	return out
}

// IncrementHex reads tok as a lowercase hex number and returns tok+n (same width, wrapping).
func IncrementHex(tok string, n int) string {
	const digits = "0123456789abcdef"
	b := []byte(tok)
	for ; n > 0; n-- {
		for i := len(b) - 1; i >= 0; i-- {
			j := strings.IndexByte(digits, b[i])
			if j < 15 {
				b[i] = digits[j+1]
				break
			}
			b[i] = digits[0]
		}
	}
	return string(b)
}

// RandomToken is a guess: a random token of 32 hex characters.
func RandomToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// replyTokenRE is the token of a mash reply subject: 32 lowercase hex characters.
var replyTokenRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

// CheckReplySubjects checks the reply subjects an observer saw from one caller: each must be prefix, a dot and exactly
// one token of 32 lowercase hex characters (mash's reply subjects carry 128 random bits, see secretInbox in
// internal/endpoint), and none may appear twice. An empty list is not an error here.
func CheckReplySubjects(prefix string, seen []string) error {
	got := map[string]bool{}
	for _, s := range seen {
		tok, ok := strings.CutPrefix(s, prefix+".")
		if !ok || !replyTokenRE.MatchString(tok) {
			return fmt.Errorf("reply subject %q is not %s. plus 32 lowercase hex characters", s, prefix)
		}
		if got[s] {
			return fmt.Errorf("reply subject %q was used for more than one request", s)
		}
		got[s] = true
	}
	return nil
}

// Guesses lists the subjects a same-owner observer who saw the reply subject seen would try: the NUID successors of
// the first 22 characters of its last token, simple increments of that token, and random tokens of its form, all
// under the same prefix. The subject itself is never among them; a caller that reuses an earlier subject is caught by
// flooding the earlier subjects as well (see observe) and by CheckReplySubjects.
func Guesses(seen string) []string {
	dot := strings.LastIndex(seen, ".")
	if dot < 0 {
		return nil
	}
	prefix, token := seen[:dot], seen[dot+1:]
	var toks []string
	if len(token) >= 22 {
		toks = append(toks, NextNUIDs(token[:22], 1, 700)...)
	}
	for n := 1; n <= 200; n++ {
		toks = append(toks, IncrementHex(token, n))
	}
	for i := 0; i < 100; i++ {
		toks = append(toks, RandomToken())
	}
	var out []string
	for _, tk := range toks {
		if s := prefix + "." + tk; s != seen {
			out = append(out, s)
		}
	}
	return out
}

// WhoDoc is the part of `mash who --json` this proof reads.
type WhoDoc struct {
	Endpoints []struct {
		Address string `json:"address"`
		Online  bool   `json:"online"`
	} `json:"endpoints"`
	// NotVerified is the count of discovery answers that failed verification; nil when the build does not print it.
	NotVerified *int `json:"answers_not_verified"`
}

// ParseWho reads the JSON object `mash who --json` prints.
func ParseWho(b []byte) (WhoDoc, error) {
	var d WhoDoc
	if err := json.Unmarshal(b, &d); err != nil {
		return d, fmt.Errorf("mash who --json: %w", err)
	}
	return d, nil
}

// Online says whether the document lists address, and whether as online.
func (d WhoDoc) Online(address string) (listed, online bool) {
	for _, e := range d.Endpoints {
		if e.Address == address {
			return true, e.Online
		}
	}
	return false, false
}

// Count renders the not-verified count: a number, or "absent" when the build prints none.
func (d WhoDoc) Count() string {
	if d.NotVerified == nil {
		return "absent"
	}
	return fmt.Sprint(*d.NotVerified)
}

// Answer is one forged discovery answer.
type Answer struct {
	Name string
	Data []byte
}

// Forgeries builds, from the genuine registration of bob (the bytes his own service answers a discovery with), the
// three answers of AT-022 item 2, each naming bob's address: unsigned (no id_sig), forged-sig (an id_sig with a
// flipped byte) and wrong-key (validly signed by carol's key, for bob's address). bobSubject is bob's prompt subject.
func Forgeries(genuine []byte, bobSubject string, carol sapsdk.Identity) ([]Answer, error) {
	forge := func(name string, signer *sapsdk.Identity, mod func(md map[string]string)) (Answer, error) {
		var info map[string]any
		if err := json.Unmarshal(genuine, &info); err != nil {
			return Answer{}, err
		}
		raw, _ := info["metadata"].(map[string]any)
		if raw == nil {
			return Answer{}, fmt.Errorf("the genuine registration has no metadata")
		}
		md := map[string]string{}
		for k, v := range raw {
			md[k], _ = v.(string)
		}
		if mod != nil {
			mod(md)
		}
		if signer != nil {
			md["user_nkey"], md["account"] = signer.User, signer.Account
			sig, err := signer.IDSig(md["agent"], md["owner"], bobSubject)
			if err != nil {
				return Answer{}, err
			}
			md["id_sig"] = sig
		}
		info["metadata"] = md
		data, err := json.Marshal(info)
		return Answer{Name: name, Data: data}, err
	}
	var out []Answer
	for _, c := range []struct {
		name   string
		signer *sapsdk.Identity
		mod    func(map[string]string)
	}{
		{"unsigned", nil, func(md map[string]string) { delete(md, "id_sig") }},
		{"forged-sig", nil, func(md map[string]string) {
			sig, _ := base64.RawURLEncoding.DecodeString(md["id_sig"])
			if len(sig) > 0 {
				sig[0] ^= 0xff
			}
			md["id_sig"] = base64.RawURLEncoding.EncodeToString(sig)
		}},
		{"wrong-key", &carol, nil},
	} {
		a, err := forge(c.name, c.signer, c.mod)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// PromptSubjectOf is the subject of the prompt endpoint in a registration.
func PromptSubjectOf(data []byte) (string, error) {
	var info struct {
		Endpoints []struct{ Name, Subject string } `json:"endpoints"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return "", err
	}
	for _, e := range info.Endpoints {
		if e.Name == "prompt" {
			return e.Subject, nil
		}
	}
	return "", fmt.Errorf("no prompt endpoint in the registration")
}

// Item4 is the recorded result of the same-owner reply race on one build.
type Item4 struct {
	Build           string `json:"build"`
	Ran             bool   `json:"ran"`
	PromptExit      int    `json:"prompt_exit"`
	PromptStdout    string `json:"prompt_stdout"`
	PromptStderr    string `json:"prompt_stderr"`
	CarolViolations int    `json:"carol_violations"`
	// Subject is the live reply subject carol was given. It differs on every run and is not compared.
	Subject string `json:"live_reply_subject"`
}

// DiffItem4 lists the differences between two builds' results, leaving out what is random.
func DiffItem4(a, b Item4) []string {
	var d []string
	add := func(what string, x, y any) {
		if fmt.Sprint(x) != fmt.Sprint(y) {
			d = append(d, fmt.Sprintf("%s: %s = %v, %s = %v", what, a.Build, x, b.Build, y))
		}
	}
	add("ran", a.Ran, b.Ran)
	add("prompt exit status", a.PromptExit, b.PromptExit)
	add("prompt standard output", fmt.Sprintf("%q", a.PromptStdout), fmt.Sprintf("%q", b.PromptStdout))
	add("prompt standard error", fmt.Sprintf("%q", a.PromptStderr), fmt.Sprintf("%q", b.PromptStderr))
	add("carol's permission violations", a.CarolViolations, b.CarolViolations)
	return d
}

// NormalStderr drops the warning about the missing NATS encryption (every run in the guest has it) and blank lines.
func NormalStderr(s string) string {
	var keep []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "warning: ") {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}
