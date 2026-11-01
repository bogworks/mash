package main

import (
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/testenv"
)

func TestCLISendOverTheServersMaxPayloadSaysSo(t *testing.T) {
	w := newWorldWith(t, testenv.Options{MaxPayload: 64 << 10}, "a", "b")
	out, code := w.cli(t, "b", "send", "a", strings.Repeat("x", 70<<10))
	if code == 0 || !strings.Contains(out, "this server accepts at most 65536 (its max_payload)") || strings.Contains(out, "nats:") {
		t.Fatalf("exit %d: %s", code, out)
	}
	if out, code := w.cli(t, "b", "send", "a", strings.Repeat("x", 32<<10)); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
}
