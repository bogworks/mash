package listset

import (
	"fmt"
	"strings"
	"testing"
)

// AT-007 as the contract states it (draft-2) is '100 exceptions at 14-character names accepted; 59 at 32-character
// names'; those two numbers do NOT hold at the 4096 default connect line (measured: 83 at 14 characters and 50 at 32,
// owner part 4); the maintainer rules on the contract's numbers; this test pins the measured ones.

const exceptionCountsChanged = "the documented exception counts changed: report to the maintainer"

// padName returns prefix padded with x to length n.
func padName(prefix string, n int) string {
	if len(prefix) >= n {
		return prefix
	}
	return prefix + strings.Repeat("x", n-len(prefix))
}

// ownerPart returns first followed by o characters up to length p.
func ownerPart(first string, p int) string {
	return first + strings.Repeat("o", p-len(first))
}

// exceptionSet builds a sender with n allow entries, all four verbs Closed, no deny entries. Names are l characters
// and owner parts p characters; the receivers sit in another owner group, which has a second sender for its template.
func exceptionSet(n, p, l int) (ListSet, string) {
	ga, gb := "foo."+ownerPart("a", p), "foo."+ownerPart("b", p)
	sender := ga + "." + padName("s0", l)
	allow := make([]string, n)
	for i := range allow {
		allow[i] = gb + "." + padName(fmt.Sprintf("r%d", i), l)
	}
	return ListSet{
		Range:    "foo",
		Switches: concat(switches(ga, Closed, Closed, Closed, Closed), switches(gb, Closed, Closed, Closed, Closed)),
		Senders: []SenderLists{
			{Sender: sender, Allow: allow},
			{Sender: gb + ".zzz"},
		},
	}, sender
}

// measureExceptions compiles the set and returns the size problems at the default limit.
func measureExceptions(t *testing.T, n, p, l int) ([]Problem, string) {
	t.Helper()
	ls, sender := exceptionSet(n, p, l)
	c, err := Compile(ls)
	if err != nil {
		t.Fatalf("Compile(n=%d, P=%d, L=%d): %v", n, p, l, err)
	}
	return SizeCheck(c, nil), sender
}

// assertLimit checks that n exceptions fit and n+1 are refused at the default limit.
func assertLimit(t *testing.T, n, p, l int) {
	t.Helper()
	if ps, _ := measureExceptions(t, n, p, l); len(ps) != 0 {
		t.Errorf("P=%d L=%d: %d exceptions should fit but SizeCheck refused them: %v; %s", p, l, n, ps, exceptionCountsChanged)
	}
	ps, sender := measureExceptions(t, n+1, p, l)
	if len(ps) != 1 {
		t.Errorf("P=%d L=%d: %d exceptions should be refused with one problem, got %v; %s", p, l, n+1, ps, exceptionCountsChanged)
		return
	}
	pr := ps[0]
	if want := fmt.Sprintf("sender %s, server %q", sender, "default"); pr.Entry != want {
		t.Errorf("P=%d L=%d: entry = %q, want %q", p, l, pr.Entry, want)
	}
	for _, piece := range []string{fmt.Sprintf("with %d tags", n+1), "plus 346 bytes of connect options", "over the 4096 byte limit"} {
		if !strings.Contains(pr.Reason, piece) {
			t.Errorf("P=%d L=%d: reason %q lacks %q", p, l, pr.Reason, piece)
		}
	}
}

func TestAT007DocumentedExceptionLimit(t *testing.T) {
	// The pinned cases at owner part 4, with the sizes measured for N (fits) and N+1 (refused).
	pinned := []struct {
		l, n, jwtN, lineN, jwtOver, lineOver int
	}{
		{l: 14, n: 83, jwtN: 3728, lineN: 4074, jwtOver: 3765, lineOver: 4111},
		{l: 32, n: 50, jwtN: 3720, lineN: 4066, jwtOver: 3781, lineOver: 4127},
	}
	for _, pc := range pinned {
		t.Run(fmt.Sprintf("P=4 L=%d", pc.l), func(t *testing.T) {
			assertLimit(t, pc.n, 4, pc.l)
			ps, _ := measureExceptions(t, pc.n+1, 4, pc.l)
			if len(ps) == 1 {
				want := fmt.Sprintf("its connect line would be %d bytes (user JWT %d bytes with %d tags", pc.lineOver, pc.jwtOver, pc.n+1)
				if !strings.Contains(ps[0].Reason, want) {
					t.Errorf("reason %q lacks %q; %s", ps[0].Reason, want, exceptionCountsChanged)
				}
			}
			// One byte under the measured line, the N case is refused and reports its own sizes.
			ls, _ := exceptionSet(pc.n, 4, pc.l)
			c, err := Compile(ls)
			if err != nil {
				t.Fatal(err)
			}
			got := SizeCheck(c, []ServerLimits{{Server: "default", MaxConnectLine: pc.lineN - 1}})
			want := fmt.Sprintf("its connect line would be %d bytes (user JWT %d bytes with %d tags", pc.lineN, pc.jwtN, pc.n)
			if len(got) != 1 || !strings.Contains(got[0].Reason, want) {
				t.Errorf("sizes of the fitting case changed, want %q: %v; %s", want, got, exceptionCountsChanged)
			}
		})
	}

	t.Run("verbatim refusal at 84 exceptions, 14-character names", func(t *testing.T) {
		ps, _ := measureExceptions(t, 84, 4, 14)
		if len(ps) != 1 {
			t.Fatalf("problems = %v, want one; %s", ps, exceptionCountsChanged)
		}
		if want := `sender foo.aooo.s0xxxxxxxxxxxx, server "default"`; ps[0].Entry != want {
			t.Errorf("entry = %q, want %q", ps[0].Entry, want)
		}
		const want = `its connect line would be 4111 bytes (user JWT 3765 bytes with 84 tags, plus 346 bytes of connect options), over the 4096 byte limit of server "default": fewer exceptions for that sender, or a shared verb instead of a long list`
		if ps[0].Reason != want {
			t.Errorf("reason = %q\nwant     %q\n%s", ps[0].Reason, want, exceptionCountsChanged)
		}
	})

	// The full measured table: the most exceptions that fit at the default 4096 connect line, by owner-part length P
	// and name length L (7, 14, 32).
	table := []struct {
		p    int
		maxN [3]int
	}{
		{4, [3]int{111, 83, 50}},
		{8, [3]int{93, 73, 46}},
		{14, [3]int{75, 61, 41}},
		{24, [3]int{57, 48, 35}},
	}
	for _, row := range table {
		for i, l := range []int{7, 14, 32} {
			t.Run(fmt.Sprintf("table P=%d L=%d", row.p, l), func(t *testing.T) {
				assertLimit(t, row.maxN[i], row.p, l)
			})
		}
	}
}
