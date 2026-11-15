package endpoint

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/activity"
)

func statusKeys(t *testing.T, b []byte) (map[string]json.RawMessage, []string) {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("payload %q: %v", b, err)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return m, keys
}

func without(m map[string]json.RawMessage, drop ...string) map[string]json.RawMessage {
	o := map[string]json.RawMessage{}
	for k, v := range m {
		o[k] = v
	}
	for _, k := range drop {
		delete(o, k)
	}
	return o
}

func askStatus(t *testing.T, f *fixture, ep *Endpoint) []byte {
	t.Helper()
	nc := f.e.ConnAs(t, f.creds["b"], "b")
	m, err := nc.Request(ep.subj.Status, nil, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return m.Data
}

func TestStatusReplyActivity(t *testing.T) {
	f := newFixture(t, "a", "b")
	a := f.serve(t, "a", noPrompt)
	_, hbKeys := statusKeys(t, a.heartbeat())

	cases := []struct {
		name     string
		reader   func() (activity.Activity, bool)
		wantKeys []string
		state    string
		kind     string
	}{
		{"blocked question", func() (activity.Activity, bool) {
			return activity.Activity{State: activity.StateBlocked, Kind: activity.KindQuestion, Since: time.Now(), Source: "hooks"}, true
		}, []string{"kind", "state"}, "blocked", "question"},
		{"blocked no kind", func() (activity.Activity, bool) {
			return activity.Activity{State: activity.StateBlocked}, true
		}, []string{"state"}, "blocked", ""},
		{"working", func() (activity.Activity, bool) {
			return activity.Activity{State: activity.StateWorking, Kind: activity.KindQuestion, Since: time.Now(), Source: "runtime"}, true
		}, []string{"state"}, "working", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a.SetActivity(c.reader)
			m, keys := statusKeys(t, askStatus(t, f, a))
			want := append(append([]string{}, hbKeys...), c.wantKeys...)
			sort.Strings(want)
			if !reflect.DeepEqual(keys, want) { // the leakage guard: no since, source, msg, title, or anything else
				t.Fatalf("status keys %v, want %v", keys, want)
			}
			var st, kd string
			_ = json.Unmarshal(m["state"], &st)
			_ = json.Unmarshal(m["kind"], &kd)
			if st != c.state || kd != c.kind {
				t.Fatalf("state %q kind %q, want %q %q", st, kd, c.state, c.kind)
			}
			// Shared fields are the heartbeat's (ts excepted, it is the time of building).
			hb, _ := statusKeys(t, a.heartbeat())
			if !reflect.DeepEqual(without(m, "ts", "state", "kind"), without(hb, "ts")) {
				t.Fatalf("shared fields differ: %v vs %v", m, hb)
			}
		})
	}

	t.Run("absent and nil are the heartbeat", func(t *testing.T) {
		for name, r := range map[string]func() (activity.Activity, bool){
			"absent": func() (activity.Activity, bool) { return activity.Activity{State: activity.StateIdle}, false },
			"nil":    nil,
		} {
			a.SetActivity(r)
			got := a.statusPayload()
			// Same builder, so the bytes match but for the clock; compare with ts removed from both.
			gm, gk := statusKeys(t, got)
			hm, _ := statusKeys(t, a.heartbeat())
			if !reflect.DeepEqual(gk, hbKeys) || !reflect.DeepEqual(without(gm, "ts"), without(hm, "ts")) {
				t.Fatalf("%s: status %s, want heartbeat keys %v", name, got, hbKeys)
			}
			if string(got) != string(a.heartbeat()) && string(without2(got)) != string(without2(a.heartbeat())) {
				t.Fatalf("%s: not byte-identical to heartbeat: %s", name, got)
			}
		}
	})
}

// without2 blanks the ts value so two payloads built at different seconds compare byte for byte.
func without2(b []byte) []byte {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(b, &m)
	// ts sits in the same position in both; replace its value textually.
	var ts string
	_ = json.Unmarshal(m["ts"], &ts)
	return []byte(string(b[:0]) + replaceOnce(string(b), ts, "T"))
}

func replaceOnce(s, old, new string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + new + s[i+len(old):]
		}
	}
	return s
}

func TestHeartbeatNeverCarriesActivity(t *testing.T) {
	f := newFixture(t, "a")
	a := f.serve(t, "a", noPrompt)
	a.SetActivity(func() (activity.Activity, bool) {
		return activity.Activity{State: activity.StateBlocked, Kind: activity.KindPermission, Since: time.Now(), Source: "both"}, true
	})
	nc := f.e.ConnAs(t, f.creds["a"], "a")
	sub, err := nc.SubscribeSync(a.subj.HB)
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	a.publishHeartbeat()
	msg, err := sub.NextMsg(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{"published": msg.Data, "heartbeat()": a.heartbeat()} {
		m, _ := statusKeys(t, b)
		for _, k := range []string{"state", "kind", "since", "source"} {
			if _, ok := m[k]; ok {
				t.Fatalf("%s carries %q: %s", name, k, b)
			}
		}
	}
}
