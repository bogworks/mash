package admin_test

import (
	"context"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/store"
)

// An Admin's lock expiry is judged on its own clock: the Until a holder writes is its clock plus the TTL, and a taker
// compares that with its own clock.
func TestLockExpiryHonoursUseClock(t *testing.T) {
	quickLocks(t, 300*time.Millisecond)
	e, a, _ := setup(t)
	b := secondAdmin(t, e, store.DefaultLimits())
	ctx := context.Background()
	skew := func(d time.Duration) func() time.Time { return func() time.Time { return time.Now().Add(d) } }
	cases := []struct {
		name          string
		holder, taker time.Duration
		takes         bool
	}{
		{"holder 45s behind: its Until is already past on a real clock", -45 * time.Second, 0, true},
		{"holder 45s ahead: its Until is far ahead of a real clock", 45 * time.Second, 0, false},
		{"holder real, taker 45s ahead sees it expired", 0, 45 * time.Second, true},
		{"holder real, taker 45s behind sees it held", 0, -45 * time.Second, false},
		{"both real", 0, 0, false},
		{"holder 5s behind: still held for 25s", -5 * time.Second, 0, false},
	}
	for i, c := range cases {
		name := "clock-" + string(rune('a'+i))
		a.UseClock(skew(c.holder))
		rel, err := a.AcquireLock(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		b.UseClock(skew(c.taker))
		rel2, err := b.AcquireLock(ctx, name)
		switch {
		case c.takes && err != nil:
			t.Errorf("%s: want taken over, got %v", c.name, err)
		case !c.takes && err == nil:
			t.Errorf("%s: want busy, but the lock was taken", c.name)
		}
		if rel2 != nil {
			rel2()
		}
		rel()
	}
}
