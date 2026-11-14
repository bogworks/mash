package apply

import (
	"context"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
)

func threeServers() (ServerSet, []ServerAck) {
	return ServerSet{Names: []string{"s1", "s2", "s3"}, Expected: 3},
		[]ServerAck{{Server: "s1", Code: 200}, {Server: "s2", Code: 200}, {Server: "s3", Code: 200}}
}

func TestDriftAsksEveryServer(t *testing.T) {
	ks, fk := setup(t)
	fk.servers, fk.acks = threeServers()
	tok := fk.token
	op, _, _ := ks.Operator()
	cl, err := jwt.DecodeAccountClaims(tok)
	if err != nil {
		t.Fatal(err)
	}
	cl.Name = "older"
	old, err := cl.Encode(op)
	if err != nil {
		t.Fatal(err)
	}
	kinds := func(fs []Finding) (m map[string]string) {
		m = map[string]string{}
		for _, f := range fs {
			m[f.Kind] = f.Detail
		}
		return
	}
	find := func(all []string) map[string]string {
		fk.all = all
		fs, err := AccountFindings(context.Background(), fk, ks, 0)
		if err != nil {
			t.Fatal(err)
		}
		return kinds(fs)
	}
	if k := find(nil); len(k) != 0 {
		t.Fatalf("all servers agree: %v", k)
	}
	k := find([]string{tok, tok, old})
	if d := k[KindServersDiffer]; !strings.Contains(d, "3 of 3 servers answered; 2 distinct account JWTs") || !strings.Contains(d, "iat") || !strings.Contains(d, "on 2 server(s)") || strings.Contains(d, tok) {
		t.Fatalf("differ: %v", k)
	}
	if k := find([]string{tok, tok}); !strings.Contains(k[KindServersSilent], "only 2 of 3 servers answered") || k[KindServersDiffer] != "" {
		t.Fatalf("silent: %v", k)
	}
	// --servers larger than the ping found: the larger count is expected
	fk.servers = ServerSet{Names: []string{"s1", "s2"}, Expected: 2}
	fs, err := AccountFindings(context.Background(), fk, ks, 3)
	if err != nil || !strings.Contains(kinds(fs)[KindServersSilent], "only 2 of 3") {
		t.Fatalf("inventory: %v %v", err, fs)
	}
	// an unverifiable set is said so, with apply's reason
	fk.all = nil
	fk.servers = ServerSet{Names: []string{"s1"}, Expected: 1, Cluster: "c"}
	fs, err = AccountFindings(context.Background(), fk, ks, 0)
	if err != nil || !strings.Contains(kinds(fs)[KindServersUnverified], "pass --servers N") {
		t.Fatalf("unverified: %v %v", err, fs)
	}
}
