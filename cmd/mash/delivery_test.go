package main

import (
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/bogworks/mash/internal/endpoint"
)

func TestDeliveryHealthMapping(t *testing.T) {
	d := func(h, r string) endpoint.Delivery { return endpoint.Delivery{Health: h, Reason: r} }
	for _, c := range []struct {
		name string
		in   deliveryState
		want endpoint.Delivery
	}{
		{"healthy", deliveryState{}, d("ok", "")},
		{"healthy no-channel host", deliveryState{NoChannel: true}, d("ok", "")},
		{"spool failed, channel fallback", deliveryState{BindFailed: true}, d("degraded", "spool_failing")},
		{"spool failed, no-channel host", deliveryState{BindFailed: true, NoChannel: true}, d("holding", "channel_hidden")},
		{"spool failed, from-start held", deliveryState{BindFailed: true, FromStartHeld: true}, d("holding", "held_unacked")},
		{"spool required, failing at start", deliveryState{BindFailed: true, FailClosed: true, FaultActive: true}, d("holding", "channel_hidden")},
		{"spool required, failing at run time", deliveryState{FailClosed: true, FaultActive: true}, d("holding", "channel_hidden")},
		{"spool required, recovered", deliveryState{BindFailed: true, FailClosed: true}, d("ok", "")},
	} {
		got := deliveryHealth(c.in)
		if got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
		if !endpoint.KnownDelivery(got) {
			t.Errorf("%s: %+v is not a known delivery value", c.name, got)
		}
	}
}

func TestStatusExtrasDelivery(t *testing.T) {
	read := func(d endpoint.Delivery, ok bool) *atomic.Pointer[func() (endpoint.Delivery, bool)] {
		var p atomic.Pointer[func() (endpoint.Delivery, bool)]
		f := func() (endpoint.Delivery, bool) { return d, ok }
		p.Store(&f)
		return &p
	}
	detail := func() map[string]any { return map[string]any{"program_detail": map[string]string{"msg": "idle"}} }
	wire := func(m map[string]any) string {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for _, c := range []struct {
		name string
		got  map[string]any
		want string
	}{
		{"holding", statusExtras(detail, read(endpoint.Delivery{Health: "holding", Reason: "held_unacked"}, true))(),
			`{"delivery":{"health":"holding","reason":"held_unacked"},"program_detail":{"msg":"idle"}}`},
		{"ok has no reason", statusExtras(nil, read(endpoint.Delivery{Health: "ok"}, true))(), `{"delivery":{"health":"ok"}}`},
		{"ok drops a stray reason: not known, nothing", statusExtras(nil, read(endpoint.Delivery{Health: "ok", Reason: "x"}, true))(), `null`},
		{"unknown word", statusExtras(nil, read(endpoint.Delivery{Health: "SECRET"}, true))(), `null`},
		{"absent", statusExtras(detail, read(endpoint.Delivery{Health: "holding"}, false))(), `{"program_detail":{"msg":"idle"}}`},
		{"unset", statusExtras(detail, &atomic.Pointer[func() (endpoint.Delivery, bool)]{})(), `{"program_detail":{"msg":"idle"}}`},
	} {
		if got := wire(c.got); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}
