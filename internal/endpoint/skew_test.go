package endpoint

import (
	"testing"
	"time"
)

// The skew is the server's clock minus the local midpoint of the request, signed.
func TestSkewOf(t *testing.T) {
	t0 := time.Unix(1000, 0)
	t1 := t0.Add(200 * time.Millisecond)
	if d, ok := skewOf(t0.Add(45*time.Second+100*time.Millisecond), t0, t1); !ok || d != 45*time.Second {
		t.Fatalf("ahead: %v %v", d, ok)
	}
	if d, ok := skewOf(t0.Add(-30*time.Second+100*time.Millisecond), t0, t1); !ok || d != -30*time.Second {
		t.Fatalf("behind: %v %v", d, ok)
	}
	if _, ok := skewOf(time.Time{}, t0, t1); ok {
		t.Fatal("a zero server time is no reading")
	}
}

// Each lease refresh reads the consumer info, whose server timestamp gives the skew with no extra call; against the
// in-process server it is near zero.
func TestClockSkewFromLeaseRefresh(t *testing.T) {
	oldR := LeaseRefresh
	LeaseRefresh = 50 * time.Millisecond
	t.Cleanup(func() { LeaseRefresh = oldR })
	f := newFixture(t, "a")
	a, _, _ := f.runSpooled(t, "a")
	until(t, "a skew reading", func() bool { _, ok := a.ClockSkew(); return ok })
	if d, _ := a.ClockSkew(); d.Abs() > 2*time.Second {
		t.Fatalf("skew %v against a local server", d)
	}
}
