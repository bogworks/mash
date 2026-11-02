package apply

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nats-io/nats-server/v2/server"
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
