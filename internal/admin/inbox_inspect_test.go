package admin

import (
	"reflect"
	"testing"
)

func TestDeliveryStateBoundaries(t *testing.T) {
	c := &InspectedConsumer{AckFloor: Cursor{Stream: 5}, Delivered: Cursor{Stream: 8}}
	for _, tc := range []struct {
		seq  uint64
		want string
	}{
		{1, StateAcked}, {5, StateAcked}, {6, StateAckUnsure}, {8, StateAckUnsure}, {9, StateUndelivered}, {100, StateUndelivered},
	} {
		if got := DeliveryState(tc.seq, c); got != tc.want {
			t.Errorf("seq %d: %s, want %s", tc.seq, got, tc.want)
		}
	}
	// nothing delivered yet
	fresh := &InspectedConsumer{}
	if got := DeliveryState(1, fresh); got != StateUndelivered {
		t.Errorf("fresh consumer: %s", got)
	}
	// ack floor equal to delivered: everything up to it is acked, the rest undelivered
	drained := &InspectedConsumer{AckFloor: Cursor{Stream: 3}, Delivered: Cursor{Stream: 3}}
	if DeliveryState(3, drained) != StateAcked || DeliveryState(4, drained) != StateUndelivered {
		t.Error("drained consumer")
	}
	// no consumer: every message is undelivered
	if got := DeliveryState(1, nil); got != StateUndelivered {
		t.Errorf("no consumer: %s", got)
	}
}

func TestClampRangeAndSeqs(t *testing.T) {
	for _, tc := range []struct {
		from, to, first, last uint64
		lo, hi                uint64
		ok                    bool
	}{
		{0, 0, 3, 10, 3, 10, true},
		{1, 99, 3, 10, 3, 10, true},   // clamped to first and last
		{5, 7, 3, 10, 5, 7, true},     // inside
		{0, 4, 3, 10, 3, 4, true},     // only --to
		{8, 0, 3, 10, 8, 10, true},    // only --from
		{11, 20, 3, 10, 0, 0, false},  // above the stream
		{1, 2, 3, 10, 0, 0, false},    // below the stream
		{0, 0, 0, 0, 0, 0, false},     // empty stream
		{10, 10, 3, 10, 10, 10, true}, // one message
	} {
		lo, hi, ok := ClampRange(tc.from, tc.to, tc.first, tc.last)
		if ok != tc.ok || (ok && (lo != tc.lo || hi != tc.hi)) {
			t.Errorf("%+v: %d %d %v", tc, lo, hi, ok)
		}
	}
	if got := inspectSeqs(3, 10, 3, true); !reflect.DeepEqual(got, []uint64{3, 4, 5}) {
		t.Errorf("upward: %v", got)
	}
	if got := inspectSeqs(3, 10, 3, false); !reflect.DeepEqual(got, []uint64{10, 9, 8}) {
		t.Errorf("downward: %v", got)
	}
	if got := inspectSeqs(3, 4, 20, false); !reflect.DeepEqual(got, []uint64{4, 3}) {
		t.Errorf("short range: %v", got)
	}
}

func TestParseInboxAddress(t *testing.T) {
	for _, tc := range []struct{ in, key, owner string }{
		{"alpha", "alpha", ""},
		{"my-box", "my-box", ""},
		{"claude_team1_ops1", "claude_team1_ops1", ""},
		{"claude.team1.ops1", "claude_team1_ops1", "team1"},
		{"cc.acme.ops1", "ops1", "acme"},
	} {
		key, owner, err := parseInboxAddress(tc.in)
		if err != nil || key != tc.key || owner != tc.owner {
			t.Errorf("%q: %q %q %v, want %q %q", tc.in, key, owner, err, tc.key, tc.owner)
		}
	}
	for _, bad := range []string{"", "a.b", "a.b.c.d", "Claude.x.y", "a_b", "a_b_c_d", "Bad", "bad name", "-x", "claude..x"} {
		if k, _, err := parseInboxAddress(bad); err == nil {
			t.Errorf("%q accepted as %q", bad, k)
		}
	}
}
