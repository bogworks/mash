package inventory

import (
	"strings"
	"testing"
)

func id(seq uint64, n string) ID { return ID{Seq: seq, Sender: "acme.demo.bob", Nonce: n} }

func inv() []Entry {
	return []Entry{
		{ID: id(1, "a"), Phase: "step2", Label: "old one", Expect: Delivered},
		{ID: id(2, "b"), Phase: "step2", Label: "in flight", Expect: Pending},
		{ID: id(3, "c"), Phase: "step2", Label: "shape 1", Expect: Refused, Reason: "header too long"},
	}
}

func good() *Tally {
	t := NewTally()
	t.AddShown(id(1, "a"))
	t.AddShown(id(2, "b"))
	t.AddRefused(3, "header too long")
	return t
}

func TestCompareExactlyOnce(t *testing.T) {
	if ps := Compare(inv(), good()); len(ps) != 0 {
		t.Fatalf("a correct run has problems: %v", ps)
	}
	if bad := Check(inv()); len(bad) != 0 {
		t.Fatalf("inventory itself: %v", bad)
	}
}

// A count that balances (three deliveries for three messages) while one message is lost and another doubled fails.
func TestLostPlusDoubledFailsWhileTheCountBalances(t *testing.T) {
	tl := NewTally()
	tl.AddShown(id(1, "a"))
	tl.AddShown(id(1, "a")) // doubled
	// id(2) lost
	tl.AddRefused(3, "header too long")
	entries := inv()
	total := 0
	for _, e := range entries {
		if e.Expect != Refused {
			total += tl.Shown(e.ID)
		}
	}
	if total != 2 {
		t.Fatalf("setup: the totals should balance at 2 deliveries for 2 messages, got %d", total)
	}
	ps := Compare(entries, tl)
	var lost, doubled bool
	for _, p := range ps {
		lost = lost || (p.ID.Seq == 2 && strings.Contains(p.What, "lost"))
		doubled = doubled || (p.ID.Seq == 1 && strings.Contains(p.What, "doubled"))
	}
	if !lost || !doubled || len(ps) != 2 {
		t.Fatalf("want one lost and one doubled, got %v", ps)
	}
}

func TestRefusedCases(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*Tally)
		want string
	}{
		{"shown though refused", func(tl *Tally) { tl.AddShown(id(3, "c")) }, "shown to a session"},
		{"listed twice", func(tl *Tally) { tl.AddRefused(3, "header too long") }, "listed as refused 2 times"},
		{"wrong reason", func(tl *Tally) { tl.refused[3] = []string{"unreadable sender header"} }, "want \"header too long\""},
		{"never listed", func(tl *Tally) { delete(tl.refused, 3) }, "never listed"},
		{"ordinary mail listed refused", func(tl *Tally) { tl.AddRefused(1, "not verified") }, "though it is not refused"},
		{"unexpected message shown", func(tl *Tally) { tl.AddShown(id(9, "z")) }, "not in the inventory"},
		{"unexpected refusal", func(tl *Tally) { tl.AddRefused(9, "x") }, "not in the inventory but listed"},
	}
	for _, c := range cases {
		tl := good()
		c.mod(tl)
		ps := Compare(inv(), tl)
		if len(ps) == 0 {
			t.Errorf("%s: no problem reported", c.name)
			continue
		}
		found := false
		for _, p := range ps {
			found = found || strings.Contains(p.What, c.want)
		}
		if !found {
			t.Errorf("%s: want %q in %v", c.name, c.want, ps)
		}
	}
}

func TestCheckCatchesABadInventory(t *testing.T) {
	bad := Check([]Entry{
		{ID: ID{Seq: 1, Sender: "s", Nonce: "n"}, Label: "x", Expect: Pending, Reason: "r"},
		{ID: ID{Seq: 1, Sender: "s", Nonce: "n"}, Label: "y", Expect: Refused},
		{ID: ID{Seq: 2}, Label: "z", Expect: Pending},
	})
	if len(bad) < 5 {
		t.Fatalf("expected several problems, got %v", bad)
	}
}

func TestSame(t *testing.T) {
	before := []Row{{1, "acme.demo.bob", false, ""}, {2, "acme.demo.bob", false, ""}, {3, "", false, ""}}
	after := []Row{{1, "acme.demo.bob", false, ""}, {2, "acme.demo.bob", false, ""}, {3, "", true, "header too long"}}
	if bad := Same(before, after, inv(), false); len(bad) != 0 {
		t.Fatalf("a correct pair differs: %v", bad)
	}
	for name, a := range map[string][]Row{
		"lost":         after[:2],
		"added":        append(append([]Row{}, after...), Row{4, "x", false, ""}),
		"not marked":   {after[0], after[1], {3, "", false, ""}},
		"wrong reason": {after[0], after[1], {3, "", true, "name too long"}},
		"sender moved": {{1, "acme.demo.carol", false, ""}, after[1], after[2]},
	} {
		if bad := Same(before, a, inv(), false); len(bad) == 0 {
			t.Errorf("%s: not reported", name)
		}
	}
}

// A refused row hides the sender the earlier release showed; that is not a difference between the listings.
func TestSameRefusedHidesSender(t *testing.T) {
	entries := []Entry{{ID: ID{Seq: 1, Sender: "acme.demo.mallory", Nonce: "s01"}, Expect: Refused, Reason: "header too long"}}
	before := []Row{{Seq: 1, Sender: "a-name-from-the-broken-header"}}
	after := []Row{{Seq: 1, Sender: "?", Refused: true, Reason: "header too long"}}
	if bad := Same(before, after, entries, false); len(bad) != 0 {
		t.Fatalf("unexpected: %v", bad)
	}
}

func TestSamePrevRefuses(t *testing.T) {
	entries := []Entry{{ID: ID{Seq: 1, Sender: "acme.demo.mallory", Nonce: "s01"}, Expect: Refused, Reason: "header too long"}}
	refusedAfter := []Row{{Seq: 1, Sender: "?", Refused: true, Reason: "header too long"}}
	if bad := Same([]Row{{Seq: 1, Refused: true, Reason: "old wording"}}, refusedAfter, entries, true); len(bad) != 0 {
		t.Fatalf("refused before and after: %v", bad)
	}
	bad := Same([]Row{{Seq: 1, Sender: "x"}}, refusedAfter, entries, true)
	if len(bad) != 1 || !strings.Contains(bad[0], "not marked refused before the upgrade") {
		t.Fatalf("not refused before: %v", bad)
	}
	bad = Same([]Row{{Seq: 1, Refused: true}}, []Row{{Seq: 1, Sender: "x"}}, entries, true)
	if len(bad) != 1 || !strings.Contains(bad[0], "not marked refused after the upgrade") {
		t.Fatalf("not refused after: %v", bad)
	}
}
