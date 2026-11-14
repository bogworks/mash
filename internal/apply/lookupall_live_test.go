package apply

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// TestLookupAllLive runs NATS.LookupAll over the real transport against an in-process operator-mode server with a
// full resolver.
func TestLookupAllLive(t *testing.T) {
	env := startLive(t)
	n := NATS{Conn: env.sysNC, Inventory: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	want, err := n.Lookup(ctx, env.acctPub)
	if err != nil || !strings.HasPrefix(want, "eyJ") {
		t.Fatalf("Lookup: %q %v", want, err)
	}
	for _, expect := range []int{1, 0} {
		got, err := n.LookupAll(ctx, env.acctPub, expect)
		if err != nil {
			t.Fatalf("expect=%d: %v", expect, err)
		}
		if len(got) != 1 || got[0] != want {
			t.Fatalf("expect=%d: got %d answers, want exactly the one Lookup JWT", expect, len(got))
		}
	}

	// An account the resolver does not know: no hang, and no valid JWT.
	kp, _ := nkeys.CreateAccount()
	unknown, _ := kp.PublicKey()
	for _, expect := range []int{1, 0} {
		start := time.Now()
		got, err := n.LookupAll(ctx, unknown, expect)
		took := time.Since(start)
		t.Logf("unknown account, expect=%d: %d answers %q, err=%v, after %v", expect, len(got), got, err, took)
		if took > 6*time.Second {
			t.Fatalf("expect=%d: took %v", expect, took)
		}
		// The real server answers an empty body ("none"), once.
		if err != nil || len(got) != 1 || got[0] != "" {
			t.Fatalf("expect=%d: want one empty answer, got %q, %v", expect, got, err)
		}
		for _, g := range got {
			if _, derr := jwt.DecodeAccountClaims(g); derr == nil {
				t.Fatalf("expect=%d: the resolver returned a valid account JWT for an unknown account", expect)
			}
		}
	}

	// AccountFindings on a live account: one server, all agreeing, so no servers-* finding.
	fs, err := AccountFindings(ctx, n, env.ks, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range fs {
		t.Logf("finding: %s %s", fd.Kind, fd.Detail)
		if strings.HasPrefix(fd.Kind, "servers-") {
			t.Fatalf("unexpected %s: %s", fd.Kind, fd.Detail)
		}
	}
	if len(fs) != 0 {
		t.Fatalf("findings on a plain account: %v", fs)
	}
}
