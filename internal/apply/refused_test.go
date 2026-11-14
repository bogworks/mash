package apply

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRevokeRefusedServerSetPushesNothing(t *testing.T) {
	ks, fk := setup(t)
	fk.servers = ServerSet{Names: []string{"s1"}, Expected: 1}
	fk.serversErr = fmt.Errorf("%w: too many", ErrServerSetRefused)
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	_, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: fk, Now: func() time.Time { return revokeAt }}, []string{userKey(t)})
	if err == nil || !errors.Is(err, ErrServerSetRefused) || !strings.Contains(err.Error(), "nothing was changed") || fk.pushes != 0 {
		t.Fatalf("err %v pushes %d", err, fk.pushes)
	}
}

func TestDriftRefusedServerSet(t *testing.T) {
	ks, fk := setup(t)
	fk.servers = ServerSet{Names: []string{"s1"}, Expected: 1}
	fk.serversErr = fmt.Errorf("%w: too many", ErrServerSetRefused)
	_, err := AccountFindings(context.Background(), fk, ks, 0)
	if !errors.Is(err, ErrServerSetRefused) {
		t.Fatalf("err %v", err)
	}
}
