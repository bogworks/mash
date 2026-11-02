package apply

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/nats-io/nats-server/v2/server"

	"github.com/bogworks/mash/internal/policy"
)

func pingReply(t *testing.T, cluster, name, id string, routes ...string) []byte {
	t.Helper()
	m := server.ServerStatsMsg{Server: server.ServerInfo{Name: name, ID: id, Cluster: cluster}}
	for _, r := range routes {
		m.Stats.Routes = append(m.Stats.Routes, &server.RouteStat{Name: r})
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAddPingReplyClustered(t *testing.T) {
	var set ServerSet
	seen := map[string]bool{}
	addPingReply(&set, seen, pingReply(t, "c1", "n1", "ID1", "n2", "n3", "n2"))
	addPingReply(&set, seen, pingReply(t, "c1", "n2", "ID2", "n1", "n3"))
	if set.Cluster != "c1" || set.ClusterConflict {
		t.Fatalf("cluster = %q conflict=%v", set.Cluster, set.ClusterConflict)
	}
	if strings.Join(set.Names, ",") != "n1,n2" || set.Expected != 3 {
		t.Fatalf("names=%v expected=%d", set.Names, set.Expected)
	}
}

func TestAddPingReplyStandalone(t *testing.T) {
	var set ServerSet
	addPingReply(&set, map[string]bool{}, pingReply(t, "", "solo", "ID"))
	if set.Cluster != "" || !set.Complete(0) {
		t.Fatalf("cluster=%q complete=%v", set.Cluster, set.Complete(0))
	}
}

func TestAddPingReplyIsolatedClusterMember(t *testing.T) {
	var set ServerSet
	addPingReply(&set, map[string]bool{}, pingReply(t, "c1", "n1", "ID1"))
	if set.Complete(0) {
		t.Fatal("a clustered server with no routes and no inventory must not be complete")
	}
	if w := set.Why(0); !strings.Contains(w, "c1") || !strings.Contains(w, "--servers") {
		t.Fatalf("why = %q", w)
	}
	if !set.Complete(1) {
		t.Fatal("--servers 1 should confirm")
	}
}

func TestAddPingReplyClusterConflict(t *testing.T) {
	var set ServerSet
	seen := map[string]bool{}
	addPingReply(&set, seen, pingReply(t, "c1", "n1", "ID1"))
	addPingReply(&set, seen, pingReply(t, "c2", "n2", "ID2"))
	if set.Cluster != "c1" || !set.ClusterConflict || set.Complete(2) {
		t.Fatalf("cluster=%q conflict=%v complete=%v", set.Cluster, set.ClusterConflict, set.Complete(2))
	}
	if !strings.Contains(set.Why(2), "different clusters") {
		t.Fatal(set.Why(2))
	}
}

func TestAddPingReplyKeepsRawNames(t *testing.T) {
	var set ServerSet
	seen := map[string]bool{}
	raw := "evil\x1b[2K\r\nname"
	addPingReply(&set, seen, pingReply(t, "", raw, "ID"))
	if len(set.Names) != 1 || set.Names[0] != raw {
		t.Fatalf("a name with control characters keeps its identity: %q", set.Names)
	}
	// differs from raw only in a control character: a distinct server
	addPingReply(&set, seen, pingReply(t, "", "evil\x1b[2K\r\x0bname", "ID1"))
	if len(set.Names) != 2 {
		t.Fatalf("names %q", set.Names)
	}
	// identical for the first 300 characters: two servers
	pre := strings.Repeat("n", 300)
	addPingReply(&set, seen, pingReply(t, "", pre+"A", "ID2"))
	addPingReply(&set, seen, pingReply(t, "", pre+"B", "ID3"))
	if len(set.Names) != 4 {
		t.Fatalf("names sharing a long prefix are distinct servers: %d", len(set.Names))
	}
	// printed escaped
	acks := []ServerAck{{Server: "x", Code: 200}}
	_, why := pushAndCheck(context.Background(), &fakeAcks{acks}, "t", []string{raw, pre + "A"})
	if strings.ContainsAny(why, "\x1b\r\n") || !strings.Contains(why, `evil\x1b[2K\x0d\x0aname`) {
		t.Fatalf("why must print the name escaped: %q", why)
	}
	// over-long: refused, not cut
	if !addPingReply(&set, seen, pingReply(t, "", strings.Repeat("n", maxNameBytes+1), "ID4")) || len(set.Names) != 4 {
		t.Fatal("an over-long name is refused")
	}
	if !addPingReply(&set, seen, pingReply(t, strings.Repeat("c", maxNameBytes+1), "ok", "ID5")) {
		t.Fatal("an over-long cluster name is refused")
	}
	if addPingReply(&set, seen, pingReply(t, "", strings.Repeat("n", maxNameBytes), "ID6")) {
		t.Fatal("a name at the cap is accepted")
	}
	// cluster compared raw
	var cs ServerSet
	cseen := map[string]bool{}
	addPingReply(&cs, cseen, pingReply(t, pre+"A", "a", ""))
	addPingReply(&cs, cseen, pingReply(t, pre+"B", "b", ""))
	if !cs.ClusterConflict {
		t.Fatal("clusters sharing a long prefix are different clusters")
	}
}

type fakeAcks struct{ acks []ServerAck }

func (f *fakeAcks) Servers(context.Context) (ServerSet, error)               { return ServerSet{}, nil }
func (f *fakeAcks) Lookup(context.Context, string) (string, error)           { return "", nil }
func (f *fakeAcks) LookupAll(context.Context, string, int) ([]string, error) { return nil, nil }
func (f *fakeAcks) Push(context.Context, string, []string) ([]ServerAck, error) {
	return f.acks, nil
}

func TestAckFromOneLongNameDoesNotSatisfyAnother(t *testing.T) {
	pre := strings.Repeat("n", 300)
	acks := []ServerAck{{Server: pre + "A", Code: 200}}
	_, why := pushAndCheck(context.Background(), &fakeAcks{acks}, "t", []string{pre + "A", pre + "B"})
	if !strings.Contains(why, "no acknowledgement from") {
		t.Fatalf("B never acknowledged: %q", why)
	}
	_, why = pushAndCheck(context.Background(), &fakeAcks{acks}, "t", []string{pre + "A"})
	if why != "" {
		t.Fatal(why)
	}
	a := parseAck([]byte(`{"server":{"name":"` + pre + `A"},"data":{"code":200}}`))
	if a.Server != pre+"A" {
		t.Fatal("ack name is kept raw")
	}
	if a := parseAck([]byte(`{"server":{"name":"` + strings.Repeat("n", maxNameBytes+1) + `"},"data":{"code":200}}`)); a.Server != "" {
		t.Fatal("an over-long ack name matches nothing")
	}
}

func TestAddPingReplyServerCap(t *testing.T) {
	set, seen := ServerSet{}, map[string]bool{}
	for i := 0; i < maxServers; i++ {
		if addPingReply(&set, seen, pingReply(t, "", fmt.Sprintf("s%d", i), "")) {
			t.Fatalf("overflow at %d", i)
		}
	}
	if addPingReply(&set, seen, pingReply(t, "", "s0", "")) {
		t.Fatal("a repeat is not an overflow")
	}
	if !addPingReply(&set, seen, pingReply(t, "", "one-too-many", "")) || len(set.Names) != maxServers {
		t.Fatalf("overflow not reported, %d names", len(set.Names))
	}
}

func TestParseAckCleansServerText(t *testing.T) {
	a := parseAck([]byte(`{"server":{"name":"s\u001b[2K\r1"},"data":{"code":200,"message":"ok\u001b[31m\nREFUSED"}}`))
	if a.Server != "s\x1b[2K\r1" || serverName(a) != `s\x1b[2K\x0d1` || a.Message != `ok\x1b[31m\x0aREFUSED` {
		t.Fatalf("%+v", a)
	}
	a = parseAck([]byte(`{"server":{"id":"I"},"error":{"code":500,"description":"` + strings.Repeat("x", 5000) + `"}}`))
	if len(a.Message) != 259 {
		t.Fatalf("message not capped: %d", len(a.Message))
	}
}

func TestCheckClaimsReply(t *testing.T) {
	if err := checkClaimsReply(make([]byte, policy.AccountJWTLimit)); err != nil {
		t.Fatal(err)
	}
	err := checkClaimsReply(make([]byte, policy.AccountJWTLimit+1))
	if err == nil || !strings.Contains(err.Error(), "reply of 1048577 bytes over the limit") {
		t.Fatalf("%v", err)
	}
}
