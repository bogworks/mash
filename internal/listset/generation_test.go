package listset

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

func based(ls ListSet, base uint64) ListSet { ls.Base = base; return ls }

// recv derives the received bytes from a list set: a deterministic encoding, so the bytes relate to the submission.
func recv(ls ListSet) []byte {
	b, err := json.Marshal(ls)
	if err != nil {
		panic(err)
	}
	return b
}

// AT-016: a list set based on N-1 (or N+1) when N is in force is refused; nothing is returned.
func TestAT016_StaleBaseRefused(t *testing.T) {
	inForce := Record{Generation: 5, Content: DigestListSet(at001()), Signed: Digest{1}}
	for _, base := range []uint64{4, 6, 0} {
		rec, c, err := Accept(inForce, based(at001(), base), recv(based(at001(), base)))
		if rec != (Record{}) || c != nil {
			t.Fatalf("base %d: Accept returned output for a stale list set", base)
		}
		var se *StaleBaseError
		if !errors.As(err, &se) || se.Stated != base || se.InForce != 5 {
			t.Fatalf("base %d: want *StaleBaseError{%d, 5}, got %v", base, base, err)
		}
		if !errors.Is(err, ErrStaleBase) {
			t.Fatalf("base %d: error does not match ErrStaleBase", base)
		}
	}
}

// The base is judged before the list set is validated: a bad list set on a stale base is a stale base error.
func TestAccept_StaleBaseBeforeValidation(t *testing.T) {
	ls := at001()
	ls.Range = ""
	ls.Base = 3
	if _, _, err := Accept(Record{Generation: 1}, ls, recv(ls)); !errors.Is(err, ErrStaleBase) {
		t.Fatalf("want ErrStaleBase, got %v", err)
	}
}

func TestAccept_FirstAndChain(t *testing.T) {
	rec, c, err := Accept(Record{}, at001(), recv(at001()))
	if err != nil || c == nil {
		t.Fatalf("first acceptance: %v", err)
	}
	if rec.Generation != 1 || rec.Content != DigestListSet(at001()) || rec.Received != sha256.Sum256(recv(at001())) || rec.Signed != DigestCompiled(c) || rec.Content.IsZero() || rec.Received.IsZero() || rec.Signed.IsZero() {
		t.Fatalf("unexpected first record %+v", rec)
	}
	for want := uint64(2); want <= 4; want++ {
		next, _, err := Accept(rec, based(at001(), rec.Generation), recv(based(at001(), rec.Generation)))
		if err != nil {
			t.Fatal(err)
		}
		if next.Generation != want {
			t.Fatalf("generation %d, want %d", next.Generation, want)
		}
		if next.Content == rec.Content {
			t.Fatal("content digest should change with the base")
		}
		rec = next
	}
	// A repeat of the generation just replaced is now stale.
	if _, _, err := Accept(rec, based(at001(), rec.Generation-1), recv(at001())); !errors.Is(err, ErrStaleBase) {
		t.Fatalf("want ErrStaleBase, got %v", err)
	}
}

func TestAccept_ValidationFailureReturnsNoRecord(t *testing.T) {
	ls := at001()
	ls.Senders[0].Allow = append(ls.Senders[0].Allow, "foo.c.*")
	ls.Base = 2
	rec, c, err := Accept(Record{Generation: 2}, ls, recv(ls))
	var ve *ValidationError
	if !errors.As(err, &ve) || rec != (Record{}) || c != nil {
		t.Fatalf("want a ValidationError and no record, got %v %+v %v", err, rec, c)
	}
	if errors.Is(err, ErrStaleBase) {
		t.Fatal("a validation error must not match ErrStaleBase")
	}
}

func TestAccept_GenerationOverflow(t *testing.T) {
	rec, c, err := Accept(Record{Generation: math.MaxUint64}, based(at001(), math.MaxUint64), recv(at001()))
	if !errors.Is(err, ErrGenerationOverflow) || rec != (Record{}) || c != nil {
		t.Fatalf("want ErrGenerationOverflow, got %v", err)
	}
}

func TestDigest_String(t *testing.T) {
	s := DigestListSet(at001()).String()
	if !strings.HasPrefix(s, "sha256:") || len(s) != len("sha256:")+64 {
		t.Fatalf("bad digest string %q", s)
	}
}

func TestDigestListSet_OrderInsensitive(t *testing.T) {
	a := at003Deny()
	a.Senders[0].Allow = []string{"foo.c.lead", "foo.c.core"}
	want := DigestListSet(a)

	b := at003Deny()
	b.Senders[0].Allow = []string{"foo.c.core", "foo.c.lead"}
	b.Switches[0], b.Switches[len(b.Switches)-1] = b.Switches[len(b.Switches)-1], b.Switches[0]
	b.Senders[0], b.Senders[3] = b.Senders[3], b.Senders[0]
	if got := DigestListSet(b); got != want {
		t.Fatalf("reordering changed the digest: %v != %v", got, want)
	}
	if DigestListSet(a) != want {
		t.Fatal("digest not stable")
	}
	// Digesting must not reorder the caller's lists.
	if a.Senders[0].Allow[0] != "foo.c.lead" {
		t.Fatal("DigestListSet changed its input")
	}
}

func TestDigestListSet_ChangesWithAnyEntry(t *testing.T) {
	base := DigestListSet(at003Deny())
	cases := map[string]func(*ListSet){
		"base":          func(l *ListSet) { l.Base = 1 },
		"range":         func(l *ListSet) { l.Range = "bar" },
		"switch value":  func(l *ListSet) { l.Switches[0].Switch = Closed },
		"switch verb":   func(l *ListSet) { l.Switches[0].Verb = Status },
		"switch group":  func(l *ListSet) { l.Switches[0].Group = "foo.e" },
		"sender":        func(l *ListSet) { l.Senders[1].Sender = "foo.c.other" },
		"extra sender":  func(l *ListSet) { l.Senders = append(l.Senders, SenderLists{Sender: "foo.c.new"}) },
		"allow entry":   func(l *ListSet) { l.Senders[1].Allow = []string{"foo.c.core"} },
		"deny entry":    func(l *ListSet) { l.Senders[0].Deny = []string{"foo.c.lead"} },
		"deny to allow": func(l *ListSet) { l.Senders[0].Allow, l.Senders[0].Deny = l.Senders[0].Deny, nil },
		"moved entry": func(l *ListSet) {
			l.Senders[1].Allow = []string{"foo.c.sdk"}
			l.Senders[2].Allow = nil
		},
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			ls := at003Deny()
			mut(&ls)
			if DigestListSet(ls) == base {
				t.Fatal("digest did not change")
			}
		})
	}
}

func TestDigestCompiled(t *testing.T) {
	c1, err := Compile(at001())
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := Compile(at001())
	if DigestCompiled(c1) != DigestCompiled(c2) || DigestCompiled(c1).IsZero() {
		t.Fatal("compiled digest not deterministic")
	}

	// Different tags differ.
	ls := at001()
	ls.Senders[1].Allow = []string{"foo.c.sdk"}
	c3, err := Compile(ls)
	if err != nil {
		t.Fatal(err)
	}
	if DigestCompiled(c3) == DigestCompiled(c1) {
		t.Fatal("digest did not change when tags changed")
	}

	// User order does not matter; a changed template line does.
	c4, _ := Compile(at003Deny())
	d := DigestCompiled(c4)
	c4.Users[0], c4.Users[1] = c4.Users[1], c4.Users[0]
	if DigestCompiled(c4) != d {
		t.Fatal("user order changed the digest")
	}
	c4.Templates[0].Permissions.Pub.Allow = append(c4.Templates[0].Permissions.Pub.Allow, "x.y")
	if DigestCompiled(c4) == d {
		t.Fatal("template change did not change the digest")
	}
	_ = DigestCompiled(nil)
}

func TestDigestCompiled_UserPermissions(t *testing.T) {
	c, err := Compile(at001())
	if err != nil {
		t.Fatal(err)
	}
	// Pinned: a Compiled with no user permissions digests as this. The pin moved when the policy responder grant joined the template.
	if got, want := DigestCompiled(c).String(), "sha256:962836c55117c8d5645fff84ecb3973eb3bed08328b4b6f6113e315dccd00192"; got != want {
		t.Fatalf("digest of a normal Compile output changed: %s", got)
	}
	d := DigestCompiled(c)
	c.Users[0].Permissions.Pub.Allow = []string{"x.y"}
	d2 := DigestCompiled(c)
	if d2 == d {
		t.Fatal("a user permission claim did not change the digest")
	}
	c.Users[0].Permissions.Pub.Allow = []string{"x.z"}
	if DigestCompiled(c) == d2 {
		t.Fatal("a different user permission claim did not change the digest")
	}
}

func TestAccept_ReceivedDigests(t *testing.T) {
	a1, _, err := Accept(Record{}, at001(), []byte("encoding one"))
	if err != nil {
		t.Fatal(err)
	}
	a2, _, _ := Accept(Record{}, at001(), []byte("encoding one"))
	if a1.Received != a2.Received || a1.Received != sha256.Sum256([]byte("encoding one")) || a1.Received.IsZero() {
		t.Fatal("same bytes must give the same Received, the SHA-256 of the bytes")
	}
	b, _, _ := Accept(Record{}, at001(), []byte("encoding two"))
	if b.Received == a1.Received {
		t.Fatal("different bytes must give a different Received")
	}
	if b.Content != a1.Content || b.Content != DigestListSet(at001()) {
		t.Fatal("same content must give the same Content whatever the bytes")
	}
}

// Nil or empty received bytes are refused by Accept, which returns nothing.
func TestAccept_NoReceivedBytesRefused(t *testing.T) {
	for name, b := range map[string][]byte{"nil": nil, "empty": {}} {
		rec, c, err := Accept(Record{}, at001(), b)
		if !errors.Is(err, ErrNoReceivedBytes) || rec != (Record{}) || c != nil {
			t.Fatalf("%s: want ErrNoReceivedBytes and no output, got %v %+v %v", name, err, rec, c)
		}
	}
	// The base is judged first: a stale base with no bytes is a stale base error.
	if _, _, err := Accept(Record{Generation: 1}, based(at001(), 0), nil); !errors.Is(err, ErrStaleBase) {
		t.Fatalf("want ErrStaleBase, got %v", err)
	}
	// Bytes are checked before compiling: no bytes on an invalid list set reports the missing bytes.
	bad := at001()
	bad.Range = ""
	if _, _, err := Accept(Record{}, bad, nil); !errors.Is(err, ErrNoReceivedBytes) {
		t.Fatalf("want ErrNoReceivedBytes, got %v", err)
	}
}
