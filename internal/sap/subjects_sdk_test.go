package sap

import (
	"errors"
	"math/rand"
	"strings"
	"testing"

	agents "github.com/bogworks/synadia-agents.go"
)

// oldSubject is the string build the SDK builder replaced. It is the oracle of the byte-for-byte tests.
func oldSubject(verb string, a Address) string {
	return "agents." + verb + "." + a.Owner + "." + a.Group + "." + a.Instance
}

func checkSDKSubjects(t *testing.T, a Address) {
	t.Helper()
	s, err := a.Subjects()
	if err != nil {
		t.Fatalf("%v: %v", a, err)
	}
	want := Subjects{Prompt: oldSubject("prompt", a), Status: oldSubject("status", a), HB: oldSubject("hb", a), Mail: oldSubject("mail", a)}
	if s != want {
		t.Fatalf("%v: got %+v, want %+v", a, s, want)
	}
	if a.MailSubject() != want.Mail || a.MailFilter() != want.Mail {
		t.Fatalf("%v: mail %q filter %q, want %q", a, a.MailSubject(), a.MailFilter(), want.Mail)
	}
}

func TestSDKSubjectsByteIdenticalGoldens(t *testing.T) {
	for _, c := range [][3]string{
		{"o", "g", "a"}, {"acme", "ops", "alpha"}, {"acme", "ops", "p1"}, {"acme", "ops", "w-1"},
		{"acme-corp", "build", "build-1"}, {"alice", "dev", "design"}, {"bob", "ops", "pager"},
		{"a", "x", "sap-go-sdk"}, {"homerange", "x", "mash-sdk"},
	} {
		a, err := NewAddress(c[0], c[1], c[2])
		if err != nil {
			t.Fatal(err)
		}
		checkSDKSubjects(t, a)
	}
}

// genToken is a deterministic valid token of length n (1 to 32): it starts with a letter, has digits and single dashes
// inside, and does not end with a dash.
func genToken(seed, n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, n)
	for i := range b {
		k := (seed*31 + i*17 + seed*i) % 7
		switch {
		case i == 0:
			b[i] = letters[(seed*13+n)%26]
		case k == 0 && i < n-1 && b[i-1] != '-':
			b[i] = '-'
		case k == 1:
			b[i] = byte('0' + (seed+i)%10)
		default:
			b[i] = letters[(seed*7+i*5)%26]
		}
	}
	return string(b)
}

func TestSDKSubjectsByteIdenticalGenerated(t *testing.T) {
	n := 0
	for seed := 0; n < 1200; seed++ {
		o, g, i := 1+seed%32, 1+(seed*7+3)%32, 1+(seed*11+5)%32
		if o+g+i+2 > MaxIdentityLen {
			continue
		}
		a, err := NewAddress(genToken(seed, o), genToken(seed+101, g), genToken(seed+202, i))
		if err != nil {
			t.Fatalf("generator made an invalid address (seed %d): %v", seed, err)
		}
		checkSDKSubjects(t, a)
		n++
	}
	if n < 600 {
		t.Fatalf("only %d addresses", n)
	}
}

func TestSDKConstantsAndMailSubjectGolden(t *testing.T) {
	if ServiceName != "agents" || QueueGroup != "agents" || ProtocolVersion != "0.3" || AgentName != "claude-code" {
		t.Fatalf("constants: %q %q %q %q", ServiceName, QueueGroup, ProtocolVersion, AgentName)
	}
	if ServiceName != agents.ServiceName || QueueGroup != agents.QueueGroup || ProtocolVersion != agents.ProtocolVersion {
		t.Fatal("a constant is not the SDK's")
	}
	a, err := NewAddress("acme", "ops", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if got := a.MailSubject(); got != "agents.mail.acme.ops.alpha" {
		t.Fatalf("mail subject %q", got)
	}
}

// The SDK builder refuses a token with a dot, a space or nothing in it. Subjects then returns no subjects and an error
// whose text is mash's own.
func TestSubjectsBuilderRefusal(t *testing.T) {
	for _, a := range []Address{
		{Owner: "acme", Group: "o.ps", Instance: "alpha"},
		{Owner: "acme", Group: "ops", Instance: "al pha"},
		{Owner: "acme", Group: "ops", Instance: ""},
		{Owner: "acme", Group: "", Instance: "alpha"},
		{Owner: "acme", Group: "ops", Instance: "$a"},
		{},
	} {
		// The SDK itself refuses it: this is the builder's own refusal, not a check of mash's.
		_, sdkErr := agents.NewAgentSubject(AgentName, a.Group, a.Instance, agents.WithSubjectToken(a.Owner))
		if sdkErr == nil {
			t.Fatalf("%+v: the SDK builder accepts it", a)
		}
		s, err := a.Subjects()
		if err == nil || s != (Subjects{}) {
			t.Fatalf("%+v: got %+v, %v", a, s, err)
		}
		if want := "no subject can be built for " + a.String(); err.Error() != want {
			t.Errorf("%+v: error %q, want %q", a, err, want)
		}
		if strings.Contains(err.Error(), sdkErr.Error()) {
			t.Errorf("%+v: the SDK's text is in %q", a, err)
		}
		var ns *NoSubjectError
		if !errors.As(err, &ns) || !errors.Is(err, agents.ErrInvalidToken) {
			t.Errorf("%+v: %T does not wrap the SDK error", a, err)
		}
		if a.MailSubject() == "" { // a mail path cannot fail
			t.Errorf("%+v: empty mail subject", a)
		}
	}
	// An empty owner is the one case the SDK builder does not refuse: it reads an empty subject token as "the agent name",
	// so it would build agents.prompt.claude-code.ops.alpha. Subjects must not return that.
	e := Address{Owner: "", Group: "ops", Instance: "alpha"}
	if s, err := e.Subjects(); err == nil || s != (Subjects{}) {
		t.Fatalf("empty owner: got %+v, %v", s, err)
	}
	// Uppercase is refused by mash's own checks, not by the SDK: Validate is the check for it.
	up := Address{Owner: "Acme", Group: "ops", Instance: "alpha"}
	if up.Validate() == nil {
		t.Fatal("Validate accepts an uppercase token")
	}
}

// An empty owner is not a token the SDK refuses: it reads it as "the agent name" and would build a claude-code subject.
func TestSubjectsEmptyOwnerNeverClaudeCode(t *testing.T) {
	s, err := Address{Owner: "", Group: "ops", Instance: "alpha"}.Subjects()
	var ns *NoSubjectError
	if !errors.As(err, &ns) {
		t.Fatalf("got %+v, %v: want a *NoSubjectError", s, err)
	}
	for _, v := range []string{s.Prompt, s.Status, s.HB, s.Mail} {
		if v != "" || strings.Contains(v, AgentName) {
			t.Fatalf("a subject came back: %+v", s)
		}
	}
}

// The characters mash accepts in a token are a-z, 0-9 and dash. SDK v0.1.1 tokens.go ValidateToken refuses a dot, space,
// tab, CR, LF, NUL, a leading $ and a whole * or >; none of those is in mash's alphabet, so the SDK builder cannot refuse
// an address mash accepted.
const tokenAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789-"

func TestAcceptedAlphabetNotRefusedBySDK(t *testing.T) {
	for _, c := range tokenAlphabet {
		for _, tok := range []string{"a" + string(c) + "a", "a" + string(c)} {
			if tok[len(tok)-1] == '-' {
				continue
			}
			if err := agents.ValidateToken(agents.RoleSubjectToken, tok); err != nil {
				t.Errorf("SDK refuses %q: %v", tok, err)
			}
		}
	}
	for _, c := range []string{".", " ", "\t", "\r", "\n", "\x00", "*", ">"} {
		if strings.Contains(tokenAlphabet, c) {
			t.Errorf("the alphabet has %q, which the SDK refuses", c)
		}
	}
	if strings.HasPrefix(tokenAlphabet, "$") || strings.Contains(tokenAlphabet, "$") {
		t.Error("the alphabet has $")
	}
}

// Every address ParseAddress accepts is built by the SDK builder, byte for byte as the old string build: the builder's
// refusal is unreachable from a parsed address, and this checks it over the whole accepted shape.
func TestEveryParsedAddressPassesSDKBuilder(t *testing.T) {
	checked := 0
	check := func(dotted string) {
		t.Helper()
		a, err := ParseAddress(dotted)
		if err != nil {
			t.Fatalf("%q: ParseAddress refuses a constructed-valid address: %v", dotted, err)
		}
		if _, err := a.Subjects(); err != nil {
			t.Fatalf("ADDRESS %q: ParseAddress accepts it and the SDK builder refuses it: %v", dotted, err)
		}
		checkSDKSubjects(t, a)
		checked++
	}
	rep := func(c byte, n int) string { return strings.Repeat(string(c), n) }

	// Every single-character token, in every slot.
	for c := byte('a'); c <= 'z'; c++ {
		x := string(c)
		check(x + ".g." + "i")
		check("o." + x + ".i")
		check("o.g." + x)
		check(x + "." + x + "." + x)
	}
	// Boundary lengths in every slot, each slot against each other slot, within the 64-character total.
	lens := []int{1, 2, 31, 32}
	for _, lo := range lens {
		for _, lg := range lens {
			for _, li := range lens {
				if lo+lg+li+2 > MaxIdentityLen {
					continue
				}
				check(rep('a', lo) + "." + rep('b', lg) + "." + rep('c', li))
				check(rep('z', lo) + "." + rep('y', lg) + "." + rep('x', li))
			}
		}
	}
	// Digits and dashes in every allowed position of a token of each length 1 to 8, and at the 32-character edge.
	shapes := []string{}
	for n := 1; n <= 8; n++ {
		for pos := 1; pos < n; pos++ {
			b := []byte(rep('a', n))
			b[pos] = '0'
			shapes = append(shapes, string(b))
			if pos < n-1 {
				b[pos] = '-'
				shapes = append(shapes, string(b))
			}
		}
	}
	shapes = append(shapes, "a"+rep('9', 31), "a-"+rep('b', 30), rep('a', 30)+"-b", "a-1-b-2-c-3", "a1-1a", "z9")
	for _, sh := range shapes {
		check(sh + ".g.i")
		check("o." + sh + ".i")
		check("o.g." + sh)
	}
	// Exactly 64 characters in total (three tokens, two dots), every split of the lengths with each token at most 32.
	at64 := 0
	for lo := 1; lo <= 32; lo++ {
		for lg := 1; lg <= 32; lg++ {
			li := MaxIdentityLen - 2 - lo - lg
			if li < 1 || li > 32 {
				continue
			}
			d := rep('a', lo) + "." + rep('b', lg) + "." + rep('c', li)
			if len(d) != MaxIdentityLen {
				t.Fatalf("%q is %d characters", d, len(d))
			}
			check(d)
			at64++
		}
	}
	if at64 == 0 {
		t.Fatal("no address at 64 characters")
	}
	t.Logf("%d addresses checked in the enumerated cases (%d at exactly %d characters)", checked, at64, MaxIdentityLen)

	// A deterministic pseudo-random sweep over the alphabet. A token starts with a letter and does not end with a dash,
	// but a dash may follow a dash and the three lengths may overflow 64, so the generator makes some invalid addresses:
	// they are counted, and most must be valid.
	rng := rand.New(rand.NewSource(20261124))
	tok := func() string {
		n := 1 + rng.Intn(MaxTokenLen)
		b := make([]byte, n)
		for i := range b {
			switch r := rng.Intn(10); {
			case i == 0 || (i == n-1 && r == 9):
				b[i] = byte('a' + rng.Intn(26))
			case r == 9:
				b[i] = '-'
			case r >= 7:
				b[i] = byte('0' + rng.Intn(10))
			default:
				b[i] = byte('a' + rng.Intn(26))
			}
		}
		return string(b)
	}
	sweep, invalid := 0, 0
	for sweep < 10000 {
		d := tok() + "." + tok() + "." + tok()
		if _, err := ParseAddress(d); err != nil {
			invalid++
			continue
		}
		check(d)
		sweep++
	}
	if total := sweep + invalid; sweep*2 < total {
		t.Fatalf("the generator is weak: %d valid of %d", sweep, total)
	}
	t.Logf("sweep: %d valid addresses checked, %d invalid generated and skipped; %d addresses checked in all", sweep, invalid, checked)
}
