package apply

import (
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

func TestAddPingReplyCapsAndCleansNames(t *testing.T) {
	var set ServerSet
	seen := map[string]bool{}
	addPingReply(&set, seen, pingReply(t, "", "evil\x1b[2K\r\nname", "ID"))
	if len(set.Names) != 1 || set.Names[0] != `evil\x1b[2K\x0d\x0aname` {
		t.Fatalf("names %q", set.Names)
	}
	long := strings.Repeat("n", 1000)
	addPingReply(&set, seen, pingReply(t, "", long, "ID2"))
	if got := set.Names[1]; len(got) != 259 || !strings.HasSuffix(got, "...") {
		t.Fatalf("a long name is capped: %d bytes", len(got))
	}
	set, seen = ServerSet{}, map[string]bool{}
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
	if a.Server != `s\x1b[2K\x0d1` || a.Message != `ok\x1b[31m\x0aREFUSED` {
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
