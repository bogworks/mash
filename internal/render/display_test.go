package render

import (
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/sap"
)

// A three-token sender is shown in the canonical agent.owner.name form on every human surface (ADR 0081); the
// legacy forms are pinned by contract.txt and render_test.go and do not change.
const tokKey, tokDisplay = "bogworks_x_sap-go-sdk", "bogworks.x.sap-go-sdk"

func tokMail(seq uint64) Mail {
	m := mail(seq, tokKey, "hello")
	m.Owner = "x"
	return m
}

func TestOneShowsCanonicalSender(t *testing.T) {
	out := One(tokMail(3))
	if !strings.HasPrefix(out, "--- #3 from "+tokDisplay+" [verified] at 2026-10-22T10:00:00Z\n") {
		t.Fatalf("header: %s", out)
	}
	if strings.Contains(out, tokKey) {
		t.Fatalf("flat key shown: %s", out)
	}
}

func TestOneShowsCanonicalRelayOrigin(t *testing.T) {
	m := tokMail(4)
	m.Via = tokKey + " #9"
	if out := One(m); !strings.Contains(out, "\nrelayed from "+tokDisplay+" #9\n") || strings.Contains(out, tokKey) {
		t.Fatalf("via: %s", out)
	}
	m.Via = "team-a #9"
	if out := One(m); !strings.Contains(out, "\nrelayed from team-a #9\n") {
		t.Fatalf("legacy via: %s", out)
	}
	if got := Line(func() Mail { m.Via = tokKey + " #9"; return m }()); !strings.Contains(got, "\trelayed from "+tokDisplay+" #9") {
		t.Fatalf("line via: %s", got)
	}
}

func TestLineShowsCanonicalSender(t *testing.T) {
	got := Line(tokMail(7))
	if !strings.HasPrefix(got, "#7\tfrom "+tokDisplay+" [verified]\t") || strings.Contains(got, tokKey) {
		t.Fatalf("line: %s", got)
	}
}

func TestBatchShowsCanonicalSenders(t *testing.T) {
	full := Batch("a", "acme", []Mail{tokMail(1), mail(2, "alice", "hi")}, 10, 16384)
	for _, want := range []string{"2 messages from " + tokDisplay + ", alice\n", "--- #1 from " + tokDisplay + " [verified]"} {
		if !strings.Contains(full, want) {
			t.Errorf("full: missing %q in:\n%s", want, full)
		}
	}
	dig := HookBatch("a", "x", []Mail{tokMail(1), tokMail(2)}, 1, 16384)
	for _, want := range []string{"2 messages, digest, from " + tokDisplay + "\n", "#1\tfrom " + tokDisplay + " [verified]\t"} {
		if !strings.Contains(dig, want) {
			t.Errorf("digest: missing %q in:\n%s", want, dig)
		}
	}
	if strings.Contains(full+dig, tokKey) {
		t.Errorf("flat key shown:\n%s\n%s", full, dig)
	}
}

// A sender of another owner is shown without an owner/ prefix when it is three-token: the owner is already a token.
func TestSenderCanonicalHasNoOwnerPrefix(t *testing.T) {
	m := tokMail(1)
	m.Owner = "x"
	if got := Sender("acme", m); got != tokDisplay {
		t.Fatalf("cross-owner: %q", got)
	}
	if got := Sender("x", m); got != tokDisplay {
		t.Fatalf("same owner: %q", got)
	}
	m.Owner = ""
	if got := Sender("acme", m); got != tokDisplay {
		t.Fatalf("unknown owner: %q", got)
	}
	// legacy: unchanged
	l := mail(1, "nats-ops", "x")
	l.Owner = "devops"
	if got := Sender("acme", l); got != "devops/nats-ops" {
		t.Fatalf("legacy: %q", got)
	}
}

func TestUnverifiedCanonicalSenderKeepsLabel(t *testing.T) {
	m := tokMail(1)
	m.Trust = sap.TrustClaimed
	if out := Batch("a", "x", []Mail{m}, 10, 16384); !strings.HasPrefix(out, "1 message from "+tokDisplay+" [claimed]\n") {
		t.Fatalf("heading: %s", out)
	}
}
