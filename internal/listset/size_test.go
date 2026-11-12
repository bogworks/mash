package listset

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/policy"
)

// kind is which exceptions a measured sender carries.
type kind int

const (
	allowClosed   kind = iota // allow entries, every verb closed in the group: no deny key, no sentinel
	allowSentinel             // allow entries in a deny key: a neighbour has a deny, so the sender also carries deny:_none
	denyShared                // deny entries on a shared verb: the sender's own denies, no sentinel
)

func (k kind) String() string { return [...]string{"allow", "allow+sentinel", "deny"}[k] }

const sizeRange = "rng"

// ownerOf is a valid owner part of exactly n characters; nameOf the i-th distinct name of exactly n characters.
func ownerOf(prefix string, n int) string { return prefix + strings.Repeat("x", n-len(prefix)) }
func nameOf(n, i int) string              { return fmt.Sprintf("r%0*d", n-1, i) }

// exceptionSet is the list set in which sender "rng.<snd>.<name>" has exactly n exceptions of the given kind. An allow
// exception names a receiver "rng.<recvOwner>.<name>" in another owner group (owner part ownerLen); a deny exception
// names a member of the sender's own owner group, so the sender's owner part is ownerLen then. Every name is nameLen
// characters, the sender's too.
func exceptionSet(k kind, ownerLen, nameLen, n int) (ls ListSet, sender string) {
	own := ownerOf("s", 3)
	if k == denyShared {
		own = ownerOf("s", ownerLen)
	}
	group := sizeRange + "." + own
	sender = group + "." + nameOf(nameLen, 0)
	sl := SenderLists{Sender: sender}
	other := SenderLists{Sender: group + ".zz"}
	switch k {
	case allowClosed:
		ls.Switches = switches(group, Closed, Closed, Closed, Closed)
		for i := 1; i <= n; i++ {
			sl.Allow = append(sl.Allow, sizeRange+"."+ownerOf("o", ownerLen)+"."+nameOf(nameLen, i))
		}
	case allowSentinel:
		ls.Switches = switches(group, Shared, Closed, Closed, Closed)
		other.Deny = []string{group + ".yy"}
		for i := 1; i <= n; i++ {
			sl.Allow = append(sl.Allow, sizeRange+"."+ownerOf("o", ownerLen)+"."+nameOf(nameLen, i))
		}
	case denyShared:
		ls.Switches = switches(group, Shared, Closed, Closed, Closed)
		for i := 1; i <= n; i++ {
			sl.Deny = append(sl.Deny, group+"."+nameOf(nameLen, i))
		}
	}
	ls.Range = sizeRange
	ls.Senders = []SenderLists{sl, other}
	return ls, sender
}

// fits reports whether SizeCheck accepts the sender of the set with n exceptions at the default connect line.
func fits(t *testing.T, k kind, ownerLen, nameLen, n int) bool {
	t.Helper()
	ls, _ := exceptionSet(k, ownerLen, nameLen, n)
	c, err := Compile(ls)
	if err != nil {
		t.Fatal(err)
	}
	return len(SizeCheck(c, []ServerLimits{{Server: "probe", MaxConnectLine: policy.DefaultMaxControlLine}})) == 0
}

// largest is the most exceptions the sender can have within the default connect line, by SizeCheck.
func largest(t *testing.T, k kind, ownerLen, nameLen int) int {
	t.Helper()
	lo, hi := 0, 400
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if fits(t, k, ownerLen, nameLen, mid) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

var (
	tableOwners = []int{4, 8, 14, 24}
	tableNames  = []int{7, 14, 32}
)

// documentedExceptions is the most exceptions one sender can have at the default max_control_line (4,096 bytes),
// by kind, owner part length and name length, as SizeCheck measures it: the same table as docs/policy-file.md.
var documentedExceptions = map[kind]map[[2]int]int{
	allowClosed: {
		{4, 7}: 114, {4, 14}: 85, {4, 32}: 51,
		{8, 7}: 95, {8, 14}: 74, {8, 32}: 47,
		{14, 7}: 77, {14, 14}: 62, {14, 32}: 42,
		{24, 7}: 58, {24, 14}: 49, {24, 32}: 35,
	},
	allowSentinel: {
		{4, 7}: 113, {4, 14}: 84, {4, 32}: 50,
		{8, 7}: 95, {8, 14}: 74, {8, 32}: 46,
		{14, 7}: 76, {14, 14}: 62, {14, 32}: 41,
		{24, 7}: 58, {24, 14}: 49, {24, 32}: 35,
	},
	denyShared: {
		{4, 7}: 119, {4, 14}: 88, {4, 32}: 52,
		{8, 7}: 99, {8, 14}: 76, {8, 32}: 48,
		{14, 7}: 79, {14, 14}: 64, {14, 32}: 42,
		{24, 7}: 59, {24, 14}: 50, {24, 32}: 36,
	},
}

// Run with MASH_PRINT_TABLE=1 to print the table SizeCheck gives.
func TestDocumentedExceptionTable(t *testing.T) {
	print := os.Getenv("MASH_PRINT_TABLE") != ""
	for _, k := range []kind{allowClosed, allowSentinel, denyShared} {
		for _, o := range tableOwners {
			for _, n := range tableNames {
				got := largest(t, k, o, n)
				if print {
					fmt.Printf("TABLE %s owner=%d name=%d -> %d\n", k, o, n, got)
					continue
				}
				if want := documentedExceptions[k][[2]int{o, n}]; got != want {
					t.Errorf("%s owner %d name %d: measured %d, documented %d", k, o, n, got, want)
				}
			}
		}
	}
}

// The boundary of SizeCheck is the documented table: the largest set is accepted and one more is refused, naming the
// sender, its tag count, the size and the limit.
func TestSizeCheckBoundaryIsTheDocumentedTable(t *testing.T) {
	for _, k := range []kind{allowClosed, allowSentinel, denyShared} {
		for _, o := range []int{8, 24} {
			for _, nl := range []int{7, 32} {
				t.Run(fmt.Sprintf("%s owner %d name %d", k, o, nl), func(t *testing.T) {
					fit := documentedExceptions[k][[2]int{o, nl}]
					if !fits(t, k, o, nl, fit) {
						t.Fatalf("%d exceptions refused", fit)
					}
					ls, sender := exceptionSet(k, o, nl, fit+1)
					c, err := Compile(ls)
					if err != nil {
						t.Fatal(err)
					}
					ps := SizeCheck(c, []ServerLimits{{Server: "probe", MaxConnectLine: policy.DefaultMaxControlLine}})
					if len(ps) != 1 {
						t.Fatalf("%d exceptions: problems %v", fit+1, ps)
					}
					for _, want := range []string{sender, `"probe"`} {
						if !strings.Contains(ps[0].Entry, want) {
							t.Errorf("entry %q lacks %q", ps[0].Entry, want)
						}
					}
					if !strings.Contains(ps[0].Reason, "over the 4096 byte limit") {
						t.Errorf("reason %q", ps[0].Reason)
					}
				})
			}
		}
	}
}

// SizeCheck measures the connect line the way policy.ConnectLineBytes does, which the live calibration test pins
// against nats.go and a real server: the line is the user JWT plus the connect options, and the reason says so.
func TestSizeCheckMeasuresTheFullConnectLine(t *testing.T) {
	ls, sender := exceptionSet(allowClosed, 14, 14, 62)
	c, err := Compile(ls)
	if err != nil {
		t.Fatal(err)
	}
	jwtSizes, lineSizes, _, err := measure(c)
	if err != nil {
		t.Fatal(err)
	}
	over := lineSizes[sender] - jwtSizes[sender]
	// the signature (86 bytes) and the fixed connect fields: far more than nothing, never the old flat 346
	if over < 200 || over > 400 {
		t.Errorf("connect options cost %d bytes", over)
	}
	ps := SizeCheck(c, []ServerLimits{{Server: "s", MaxConnectLine: lineSizes[sender] - 1}})
	if len(ps) != 1 || !strings.Contains(ps[0].Reason, fmt.Sprintf("connect line would be %d bytes (user JWT %d bytes", lineSizes[sender], jwtSizes[sender])) ||
		!strings.Contains(ps[0].Reason, fmt.Sprintf("plus %d bytes of connect options", over)) {
		t.Errorf("problems %v", ps)
	}
	if ps := SizeCheck(c, []ServerLimits{{Server: "s", MaxConnectLine: lineSizes[sender]}}); len(ps) != 0 {
		t.Errorf("refused at the exact line size: %v", ps)
	}
}
