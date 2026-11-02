package apply

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRunRefusedServerSetPushesNothing(t *testing.T) {
	ks, fk, c := setup(t)
	fk.servers = ServerSet{Names: []string{"s1"}, Expected: 1}
	fk.serversErr = fmt.Errorf("%w: too many", ErrServerSetRefused)
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	var out bytes.Buffer
	code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk, VerifyTries: 1, VerifyWait: 1}, &out)
	if code != 1 || fk.pushes != 0 || !strings.Contains(out.String(), "refusing") || !strings.Contains(out.String(), "nothing was pushed") {
		t.Fatalf("code %d pushes %d\n%s", code, fk.pushes, out.String())
	}
}

func TestRevokeRefusedServerSetPushesNothing(t *testing.T) {
	ks, fk, _ := setup(t)
	fk.servers = ServerSet{Names: []string{"s1"}, Expected: 1}
	fk.serversErr = fmt.Errorf("%w: too many", ErrServerSetRefused)
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	_, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: fk, Now: func() time.Time { return revokeAt }}, []string{userKey(t)})
	if err == nil || !errors.Is(err, ErrServerSetRefused) || !strings.Contains(err.Error(), "nothing was changed") || fk.pushes != 0 {
		t.Fatalf("err %v pushes %d", err, fk.pushes)
	}
}

func TestDriftRefusedServerSet(t *testing.T) {
	ks, fk, c := setup(t)
	fk.servers = ServerSet{Names: []string{"s1"}, Expected: 1}
	fk.serversErr = fmt.Errorf("%w: too many", ErrServerSetRefused)
	_, err := AccountFindings(context.Background(), Params{Compiled: c, Keys: ks, Resolver: fk})
	if !errors.Is(err, ErrServerSetRefused) {
		t.Fatalf("err %v", err)
	}
}
