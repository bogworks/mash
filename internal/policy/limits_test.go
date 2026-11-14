package policy

import (
	"fmt"
	"strings"
	"testing"
)

// nameOf makes the i-th distinct lower-case name of exactly n characters: the prefix, then digits (so it is a valid
// endpoint name and zone), e.g. n=7 and i=3 with prefix "r" gives "r000003".
func nameOf(prefix string, n, i int) string {
	return (prefix + fmt.Sprintf("%0*d", n-len(prefix), i))[:n]
}

// exceptionFile writes the policy file in which one sender has exactly n exceptions. Default deny, one allow rule:
// the sender may mail n of the 2n endpoints of the receiving zone, so the compile gives it n allow tags
// (allow:<zone>.<name>) besides the placeholder deny tag. With deny set it is the mirror: default allow, one deny rule
// naming n of 2n+1 endpoints, so the cheaper encoding is the zone wildcard and n deny tags. Every name (the sender
// and each receiver) is nameLen characters and the receiving zone is zoneLen characters.
func exceptionFile(nameLen, zoneLen, n int, deny bool) (sender, src string) {
	sender = nameOf("s", nameLen, 0)
	zone := nameOf("z", zoneLen, 0)
	var b strings.Builder
	total := 2 * n
	if deny {
		b.WriteString("default: allow\n")
		total++
	} else {
		b.WriteString("default: deny\n")
	}
	b.WriteString("endpoints:\n")
	fmt.Fprintf(&b, "  %s:\n    labels: {zone: sendzone, role: sender}\n", sender)
	for i := 0; i < total; i++ {
		tier := "a"
		if i >= n {
			tier = "b"
		}
		fmt.Fprintf(&b, "  %s:\n    labels: {zone: %s, tier: %s}\n", nameOf("r", nameLen, i), zone, tier)
	}
	action := "allow"
	if deny {
		action = "deny"
	}
	fmt.Fprintf(&b, "rules:\n  - {name: r, action: %s, from: {role: sender}, to: {tier: a}}\n", action)
	return sender, b.String()
}

// measureExceptions compiles exceptionFile and measures it against the default limit, as `policy check` does.
func measureExceptions(t *testing.T, nameLen, zoneLen, n int, deny bool) (BudgetReport, error, CompiledEndpoint) {
	t.Helper()
	sender, src := exceptionFile(nameLen, zoneLen, n, deny)
	c, err := Compile(mustParse(t, src), "owner")
	if err != nil {
		t.Fatal(err)
	}
	var ep CompiledEndpoint
	for _, e := range c.Endpoints {
		if e.Name == sender {
			ep = e
		}
	}
	rep, err := Budget(c)
	return rep, err, ep
}

// largestFit is the most exceptions one sender can have at the default connect line.
func largestFit(t *testing.T, nameLen, zoneLen int, deny bool) int {
	t.Helper()
	lo, hi := 0, 400
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if _, err, _ := measureExceptions(t, nameLen, zoneLen, mid, deny); err == nil {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// documentedLimits are the numbers docs/policy-file.md gives: the most exceptions one sender can have at the default
// connect line (4,096 bytes), for names (the sender's and its receivers') of the given length and a receiving zone of
// 3 characters. One exception is one tag, whatever verbs it covers, and every sender carries one more tag, the
// placeholder deny tag. The numbers are measured with the real compile and ConnectLineBytes.
var documentedLimits = []struct{ nameLen, exceptions int }{{7, 119}, {14, 87}, {32, 52}, {48, 37}}

// documentedZoneLen is the receiving zone's length in documentedLimits.
const documentedZoneLen = 3

// AT-007: at the default connect line the largest set that fits is accepted and one more is refused by size, naming
// the sender, its count, its size and the limit. The counts are the documented numbers.
func TestDocumentedExceptionLimit(t *testing.T) {
	for _, d := range documentedLimits {
		t.Run(fmt.Sprintf("names %d", d.nameLen), func(t *testing.T) {
			checkLimit(t, d.nameLen, d.exceptions, false)
			// The measured limit is the documented number, not just consistent with it.
			if got := largestFit(t, d.nameLen, documentedZoneLen, false); got != d.exceptions {
				t.Errorf("measured limit %d, documented %d", got, d.exceptions)
			}
		})
	}
}

// A deny exception costs one tag too, one byte of JSON less than an allow one ("deny:" is a character shorter than
// "allow:"), so a sender of deny exceptions fits at least the documented number; its own largest set fits and one
// more is refused the same way.
func TestDenyExceptionsCountTheSame(t *testing.T) {
	for _, d := range documentedLimits {
		t.Run(fmt.Sprintf("names %d", d.nameLen), func(t *testing.T) {
			got := largestFit(t, d.nameLen, documentedZoneLen, true)
			if got < d.exceptions || got > d.exceptions+8 {
				t.Fatalf("deny exceptions: largest set %d, allow %d", got, d.exceptions)
			}
			checkLimit(t, d.nameLen, got, true)
		})
	}
}

// checkLimit asserts that n exceptions fit at nameLen-character names and n+1 are refused by size with the sender, the
// count, the size and the limit in the message.
func checkLimit(t *testing.T, nameLen, n int, deny bool) {
	t.Helper()
	rep, err, ep := measureExceptions(t, nameLen, documentedZoneLen, n, deny)
	if err != nil || !rep.OK {
		t.Fatalf("%d exceptions at %d-character names are refused: %v", n, nameLen, err)
	}
	if exceptionCount(ep.Tags) != n || len(ep.Tags) != n+1 {
		t.Fatalf("an exception is one tag: %d exceptions gave %d tags", n, len(ep.Tags))
	}
	if rep.WorstConnectBytes > DefaultMaxControlLine {
		t.Fatalf("accepted but %d bytes is over %d", rep.WorstConnectBytes, DefaultMaxControlLine)
	}
	rep1, err, ep1 := measureExceptions(t, nameLen, documentedZoneLen, n+1, deny)
	if err == nil || rep1.OK {
		t.Fatalf("%d exceptions at %d-character names fit", n+1, nameLen)
	}
	for _, want := range []string{
		fmt.Sprintf("sender %q", ep1.Name), fmt.Sprintf("with %d exceptions (%d tags)", n+1, n+2),
		withCommas(rep1.WorstConnectBytes) + " bytes", "allows 4,096 (max_control_line)", fmt.Sprintf("server %q", DefaultServer),
		"shared verb", "docs/policy-file.md",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q:\n%v", want, err)
		}
	}
	t.Logf("names of %d characters, deny=%v: %d exceptions fit (line %d), %d do not (line %d)", nameLen, deny, n, rep.WorstConnectBytes, n+1, rep1.WorstConnectBytes)
}

// 100 exceptions fit where the names are short (10 characters, a 3-character zone).
func TestHundredExceptionsFitShortNames(t *testing.T) {
	if _, err, _ := measureExceptions(t, 10, documentedZoneLen, 100, false); err != nil {
		t.Fatalf("100 exceptions at 10-character names: %v", err)
	}
	if _, err, _ := measureExceptions(t, 14, documentedZoneLen, 100, false); err == nil {
		t.Fatal("100 exceptions at 14-character names fit; the documentation says 87")
	}
}

// The zone is part of every tag, so a longer receiving zone name leaves room for fewer exceptions; the documented
// table of zone lengths is measured.
func TestExceptionLimitByZoneLength(t *testing.T) {
	want := map[[2]int]int{{3, 14}: 87, {14, 14}: 62, {32, 14}: 42, {3, 32}: 52, {14, 32}: 41, {32, 32}: 31}
	for k, w := range want {
		if got := largestFit(t, k[1], k[0], false); got != w {
			t.Errorf("zone of %d and names of %d characters: measured %d, documented %d", k[0], k[1], got, w)
		}
	}
}

// AT-006 at the policy level: a sender that fits the largest server but not the smallest is refused, naming that
// sender, its size, the smallest limit and only the servers it does not fit. An account JWT over one server's limit
// is refused naming the account and that server.
func TestBudgetForNamesTheServerThatDoesNotFit(t *testing.T) {
	_, src := exceptionFile(14, documentedZoneLen, 60, false)
	c, err := Compile(mustParse(t, src), "owner")
	if err != nil {
		t.Fatal(err)
	}
	rep, err := BudgetFor(c, []ServerLimits{
		{Server: "nats-1", MaxControlLine: 4096, AccountJWT: AccountJWTLimit},
		{Server: "nats-2", MaxControlLine: 3000, AccountJWT: AccountJWTLimit},
		{Server: "nats-3", MaxControlLine: 2500, AccountJWT: AccountJWTLimit},
	})
	if err == nil || rep.OK || len(rep.Refusals) != 2 {
		t.Fatalf("%+v %v", rep, err)
	}
	for _, want := range []string{`sender "s0000000000000"`, `server "nats-2"`, `server "nats-3"`, "allows 3,000 (max_control_line)", "allows 2,500 (max_control_line)", "60 exceptions (61 tags)", withCommas(rep.WorstConnectBytes)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("lacks %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), `server "nats-1"`) {
		t.Errorf("names a server that takes it:\n%v", err)
	}
	if rep.UserLimit != 2500 {
		t.Errorf("the smallest limit is %d", rep.UserLimit)
	}

	rep, err = BudgetFor(c, []ServerLimits{
		{Server: "nats-1", MaxControlLine: 4096, AccountJWT: AccountJWTLimit},
		{Server: "nats-2", MaxControlLine: 4096, AccountJWT: 500},
	})
	if err == nil || rep.OK || len(rep.Refusals) != 1 || rep.Refusals[0].Kind != "account" {
		t.Fatalf("%+v %v", rep, err)
	}
	for _, want := range []string{`account "account"`, `server "nats-2"`, "allows 500 (max_payload)", "its JWT would be"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("lacks %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), `server "nats-1"`) || strings.Contains(err.Error(), "shared verb") {
		t.Errorf("%v", err)
	}
}

func TestWithCommas(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 4096: "4,096", 1048576: "1,048,576", -4096: "-4,096"} {
		if got := withCommas(n); got != want {
			t.Errorf("%d: %q, want %q", n, got, want)
		}
	}
}
