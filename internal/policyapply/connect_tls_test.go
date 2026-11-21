package policyapply

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// The suite's in-process servers speak plain text, so the package's tests run with the override on; the tests below
// set it themselves.
func TestMain(m *testing.M) {
	os.Setenv("MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION", "1")
	os.Exit(m.Run())
}

// connectTo itself enforces the encryption rule: a plain or scheme-less URL is refused unless
// MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1, and the refusal comes before any connection is tried.
func TestConnectToRefusesPlainWithoutSwitch(t *testing.T) {
	t.Setenv("MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION", "")
	for _, u := range []string{"nats://127.0.0.1:1", "127.0.0.1:1", "ws://127.0.0.1:1"} {
		_, err := connectTo(context.Background(), "s1", u, []byte("x"), time.Second)
		if err == nil || !strings.Contains(err.Error(), "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION") {
			t.Errorf("%s: err %v, want a refusal naming MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION", u, err)
		}
	}
}

func TestConnectToAllowsPlainWithSwitch(t *testing.T) {
	t.Setenv("MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION", "1")
	for _, u := range []string{"nats://127.0.0.1:1", "127.0.0.1:1"} {
		_, err := connectTo(context.Background(), "s1", u, []byte("x"), 200*time.Millisecond)
		if err == nil || strings.Contains(err.Error(), "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION") {
			t.Errorf("%s: err %v, want a connection failure (nothing listens), not a refusal", u, err)
		}
	}
}
