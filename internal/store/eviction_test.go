package store

import "testing"

func TestEviction(t *testing.T) {
	for _, c := range []struct {
		name            string
		in              InboxState
		dropped, unread uint64
		fill            float64
	}{
		{"fresh empty stream", InboxState{}, 0, 0, 0},
		{"nothing dropped", InboxState{FirstSeq: 1, LastSeq: 5, Bytes: 50, MaxBytes: 100, HaveConsumer: true, DeliveredStream: 2, DeliveredConsumer: 2}, 0, 0, 50},
		{"dropped after being read", InboxState{FirstSeq: 11, LastSeq: 20, HaveConsumer: true, DeliveredStream: 20, DeliveredConsumer: 20}, 10, 0, 0},
		{"dropped waiting, consumer behind", InboxState{FirstSeq: 17, LastSeq: 23, HaveConsumer: true, DeliveredStream: 2, DeliveredConsumer: 2}, 16, 14, 0},
		{"consumer jumped the gap", InboxState{FirstSeq: 17, LastSeq: 23, HaveConsumer: true, DeliveredStream: 17, DeliveredConsumer: 3}, 16, 14, 0},
		{"redelivery reads low", InboxState{FirstSeq: 17, LastSeq: 23, HaveConsumer: true, DeliveredStream: 17, DeliveredConsumer: 5}, 16, 12, 0},
		{"no consumer: nothing delivered", InboxState{FirstSeq: 6, LastSeq: 9}, 5, 5, 0},
		{"everything aged out", InboxState{FirstSeq: 8, LastSeq: 7, HaveConsumer: true, DeliveredStream: 7, DeliveredConsumer: 7}, 7, 0, 0},
		{"near full", InboxState{FirstSeq: 1, LastSeq: 3, Bytes: 95, MaxBytes: 100}, 0, 0, 95},
	} {
		got := Eviction(c.in)
		if got.Dropped != c.dropped || got.Unread != c.unread || got.Fill != c.fill {
			t.Errorf("%s: got %+v, want dropped %d unread %d fill %v", c.name, got, c.dropped, c.unread, c.fill)
		}
	}
	if !(InboxEviction{Fill: 90}).NearFull() || (InboxEviction{Fill: 89.9}).NearFull() {
		t.Error("NearFull threshold")
	}
}
