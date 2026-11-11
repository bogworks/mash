package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/store"
)

// retiredOut is the retired section and the compare fields this file reads.
type retiredOut struct {
	Retired *struct {
		Present     bool    `json:"present"`
		Final       int     `json:"final"`
		Pending     int     `json:"pending"`
		Bytes       uint64  `json:"bytes"`
		MaxBytes    int64   `json:"max_bytes"`
		PercentUsed float64 `json:"percent_used"`
		Alarm       bool    `json:"alarm"`
		Markers     []struct {
			Key       string `json:"key"`
			Agent     string `json:"agent"`
			Owner     string `json:"owner"`
			Name      string `json:"name"`
			User      string `json:"user"`
			State     string `json:"state"`
			RetiredAt string `json:"retired_at"`
		} `json:"markers"`
	} `json:"retired"`
	Problems []struct {
		What  string `json:"what"`
		Item  string `json:"item"`
		Error string `json:"error"`
	} `json:"problems"`
	Summary struct {
		RetiredFinal   int `json:"retired_final"`
		RetiredPending int `json:"retired_pending"`
	} `json:"summary"`
	Compare *struct {
		RetiredCompared    bool     `json:"retired_compared"`
		RetiredUnverified  bool     `json:"retired_unverified"`
		RetiredAdded       []string `json:"retired_added"`
		RetiredFinalLost   []string `json:"retired_final_lost"`
		RetiredPendingGone []string `json:"retired_pending_gone"`
	} `json:"compare"`
}

func (f *auditFixture) retiredAudit(t *testing.T, args ...string) (retiredOut, string, int) {
	t.Helper()
	out, errb, code := f.run(t, append([]string{"--json"}, args...)...)
	var r retiredOut
	if code == 3 || json.Unmarshal([]byte(out), &r) != nil {
		t.Fatalf("audit: %d %q\n%s", code, errb, out)
	}
	return r, errb, code
}

func (f *auditFixture) putMarker(t *testing.T, kv jetstream.KeyValue, agent, owner, name, user, state string) {
	t.Helper()
	m := store.Retired{Agent: agent, Owner: owner, Name: name, User: user, RetiredAt: "2026-11-10T01:02:03Z", State: state}
	v, _ := json.Marshal(m)
	if _, err := kv.Put(context.Background(), agent+"_"+owner+"_"+name, v); err != nil {
		t.Fatal(err)
	}
}

func (f *auditFixture) retiredKV(t *testing.T) jetstream.KeyValue {
	t.Helper()
	kv, err := f.js.KeyValue(context.Background(), store.RetiredBucket)
	if err != nil {
		t.Fatal(err)
	}
	return kv
}

// recreateRetired replaces the bucket with one RetiredConfig builds, as mut changes it (nil mut: no bucket at all).
func (f *auditFixture) recreateRetired(t *testing.T, mut func(*jetstream.KeyValueConfig)) jetstream.KeyValue {
	t.Helper()
	ctx := context.Background()
	if err := f.js.DeleteKeyValue(ctx, store.RetiredBucket); err != nil {
		t.Fatal(err)
	}
	if mut == nil {
		return nil
	}
	cfg := store.RetiredConfig()
	mut(&cfg)
	kv, err := f.js.CreateKeyValue(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return kv
}

func TestAuditRetiredEmptyBucket(t *testing.T) {
	f := newAuditEnv(t)
	r, _, code := f.retiredAudit(t, "--strict")
	if code != 0 || r.Retired == nil || !r.Retired.Present || r.Retired.Final != 0 || r.Retired.Pending != 0 || r.Retired.Alarm ||
		r.Retired.Markers == nil || len(r.Retired.Markers) != 0 || r.Retired.MaxBytes != store.RetiredMaxBytes || r.Retired.PercentUsed != 0 {
		t.Fatalf("empty bucket: exit %d %+v", code, r.Retired)
	}
	txt, _, code := f.run(t)
	if code != 0 || !strings.Contains(txt, "retired markers 0") {
		t.Errorf("text form must say there are no markers (exit %d):\n%s", code, txt)
	}
}

func TestAuditRetiredNoBucketIsNotAnError(t *testing.T) {
	f := newAuditEnv(t)
	f.recreateRetired(t, nil)
	r, _, code := f.retiredAudit(t, "--strict")
	if code != 0 || r.Retired == nil || r.Retired.Present || len(r.Retired.Markers) != 0 || len(r.Problems) != 0 {
		t.Fatalf("no bucket: exit %d %+v problems %+v", code, r.Retired, r.Problems)
	}
	txt, _, _ := f.run(t)
	if !strings.Contains(txt, "no retired bucket") {
		t.Errorf("text form must say no retired bucket:\n%s", txt)
	}
	// and the audit did not create it
	if _, err := f.js.KeyValue(context.Background(), store.RetiredBucket); err == nil {
		t.Error("audit created the retired bucket: it must be read only")
	}
}

func TestAuditRetiredListsFinalAndPending(t *testing.T) {
	f := newAuditEnv(t)
	kv := f.retiredKV(t)
	f.putMarker(t, kv, "claude", "team1", "done", "UDONEUSERKEY", store.RetiredFinal)
	f.putMarker(t, kv, "claude", "team1", "old", "UOLDUSERKEY", "") // no state counts as final
	f.putMarker(t, kv, "acme", "team2", "half", "UHALFUSERKEY", store.RetiredPending)

	r, errb, code := f.retiredAudit(t, "--strict")
	if r.Retired == nil || !r.Retired.Present || r.Retired.Final != 2 || r.Retired.Pending != 1 || len(r.Retired.Markers) != 3 ||
		r.Summary.RetiredFinal != 2 || r.Summary.RetiredPending != 1 || r.Retired.Bytes == 0 {
		t.Fatalf("markers: %+v summary %+v", r.Retired, r.Summary)
	}
	m := r.Retired.Markers // sorted by key
	if m[0].Key != "acme_team2_half" || m[0].State != "pending" || m[1].Key != "claude_team1_done" || m[1].State != "final" ||
		m[2].Key != "claude_team1_old" || m[2].State != "final" || m[1].RetiredAt != "2026-11-10T01:02:03Z" ||
		m[1].Agent != "claude" || m[1].Owner != "team1" || m[1].Name != "done" || m[1].User != "UDONEUSERKEY" {
		t.Errorf("markers: %+v", m)
	}
	// a pending marker is an unfinished retirement: a finding, so --strict exits 1
	if code != 1 || len(r.Problems) != 1 || r.Problems[0].What != "retired" || r.Problems[0].Item != "acme_team2_half" ||
		!strings.Contains(r.Problems[0].Error, "rerun deprovision") || !strings.Contains(errb, "--strict") {
		t.Errorf("pending marker must be a finding: exit %d %+v err=%q", code, r.Problems, errb)
	}
	txt, _, _ := f.run(t)
	for _, want := range []string{"retired markers 3 (final 2, pending 1)", "retired marker claude_team1_done\tfinal\tretired_at 2026-11-10T01:02:03Z", "retired marker acme_team2_half\tPENDING", "not readable retired acme_team2_half"} {
		if !strings.Contains(txt, want) {
			t.Errorf("text form lacks %q:\n%s", want, txt)
		}
	}
	// only finals: clean
	_ = kv.Delete(context.Background(), "acme_team2_half")
	if r, _, code := f.retiredAudit(t, "--strict"); code != 0 || r.Retired.Final != 2 || r.Retired.Pending != 0 || len(r.Problems) != 0 {
		t.Errorf("finals only: exit %d %+v %+v", code, r.Retired, r.Problems)
	}
}

func TestAuditRetiredAlarmAtHalfTheCap(t *testing.T) {
	f := newAuditEnv(t)
	kv := f.recreateRetired(t, func(c *jetstream.KeyValueConfig) { c.MaxBytes = 16 << 10 })
	// below half: no alarm
	f.putMarker(t, kv, "claude", "team1", "m0", "UKEY0", store.RetiredFinal)
	r, _, code := f.retiredAudit(t, "--strict")
	if code != 0 || r.Retired.Alarm || r.Retired.MaxBytes != 16<<10 || r.Retired.PercentUsed <= 0 || r.Retired.PercentUsed >= 50 {
		t.Fatalf("below half: exit %d %+v", code, r.Retired)
	}
	for i := 1; ; i++ {
		f.putMarker(t, kv, "claude", "team1", fmt.Sprintf("m%d", i), "UKEY", store.RetiredFinal)
		st, err := kv.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if st.(*jetstream.KeyValueBucketStatus).Bytes()*2 >= 16<<10 {
			break
		}
		if i > 200 {
			t.Fatal("never reached half")
		}
	}
	r, errb, code := f.retiredAudit(t, "--strict")
	if code != 1 || !r.Retired.Alarm || r.Retired.PercentUsed < 50 || len(r.Problems) != 1 || r.Problems[0].What != "retired" ||
		r.Problems[0].Item != store.RetiredBucket || !strings.Contains(r.Problems[0].Error, "ALARM") || !strings.Contains(errb, "--strict") {
		t.Fatalf("at half: exit %d %+v %+v err=%q", code, r.Retired, r.Problems, errb)
	}
	if txt, _, _ := f.run(t); !strings.Contains(txt, "ALARM") {
		t.Errorf("text form must show the alarm:\n%s", txt)
	}
	// not strict: the finding is shown and the exit is 0
	if _, _, code := f.run(t, "--json"); code != 0 {
		t.Errorf("without --strict exit %d", code)
	}
}

func TestAuditRetiredUncappedBucketIsAProblemNotAnAlarm(t *testing.T) {
	f := newAuditEnv(t)
	kv := f.recreateRetired(t, func(c *jetstream.KeyValueConfig) { c.MaxBytes = 0 })
	f.putMarker(t, kv, "claude", "team1", "m", "UKEY", store.RetiredFinal)
	r, errb, code := f.retiredAudit(t, "--strict")
	if code != 1 || r.Retired.Alarm || r.Retired.MaxBytes != 0 || r.Retired.PercentUsed != 0 || r.Retired.Bytes == 0 ||
		len(r.Problems) != 1 || r.Problems[0].What != "retired" || r.Problems[0].Item != store.RetiredBucket ||
		!strings.Contains(r.Problems[0].Error, "no size cap") || strings.Contains(r.Problems[0].Error, "ALARM") {
		t.Fatalf("uncapped: exit %d %+v %+v err=%q", code, r.Retired, r.Problems, errb)
	}
	if txt, _, _ := f.run(t); strings.Contains(txt, "ALARM") || !strings.Contains(txt, ", no cap") {
		t.Errorf("text form must say no cap and not alarm:\n%s", txt)
	}
}

func TestAuditRetiredAlarmBoundary(t *testing.T) {
	cases := []struct {
		used  uint64
		max   int64
		alarm bool
	}{{0, 100, false}, {49, 100, false}, {50, 100, true}, {51, 100, true}, {1, 1, true}, {0, 0, false}}
	for _, c := range cases {
		if got := retiredAlarm(c.used, c.max); got != c.alarm {
			t.Errorf("retiredAlarm(%d, %d) = %t, want %t", c.used, c.max, got, c.alarm)
		}
	}
}

func TestAuditRetiredBadBucketIsAProblemNotACrash(t *testing.T) {
	f := newAuditEnv(t)
	kv := f.recreateRetired(t, func(c *jetstream.KeyValueConfig) { c.TTL = 3600e9 })
	f.putMarker(t, kv, "claude", "team1", "m", "UKEY", store.RetiredFinal)
	r, errb, code := f.retiredAudit(t, "--strict")
	if code != 1 || r.Retired == nil || len(r.Problems) == 0 || r.Problems[0].What != "retired" || r.Problems[0].Item != store.RetiredBucket ||
		!strings.Contains(r.Problems[0].Error, "TTL") {
		t.Fatalf("bad bucket: exit %d %+v %+v err=%q", code, r.Retired, r.Problems, errb)
	}
}

func TestAuditRetiredUnreadableMarkerIsAProblem(t *testing.T) {
	f := newAuditEnv(t)
	kv := f.retiredKV(t)
	if _, err := kv.Put(context.Background(), "claude_team1_bad", []byte("not json")); err != nil {
		t.Fatal(err)
	}
	r, _, code := f.retiredAudit(t, "--strict")
	if code != 1 || len(r.Problems) != 1 || r.Problems[0].What != "retired" || r.Problems[0].Item != "claude_team1_bad" {
		t.Fatalf("bad marker: exit %d %+v", code, r.Problems)
	}
}

func TestAuditRetiredUnrecognizedStateIsAProblem(t *testing.T) {
	f := newAuditEnv(t)
	kv := f.retiredKV(t)
	f.putMarker(t, kv, "claude", "team1", "odd", "", "done")
	long := strings.Repeat("x", 500)
	f.putMarker(t, kv, "claude", "team1", "hostile", "", long)
	f.putMarker(t, kv, "claude", "team1", "plain", "", "")
	r, _, code := f.retiredAudit(t, "--strict")
	if code != 1 || len(r.Problems) != 2 {
		t.Fatalf("unrecognized state: exit %d %+v", code, r.Problems)
	}
	for _, p := range r.Problems {
		if p.What != "retired" || !strings.Contains(p.Error, "unrecognized state") || len(p.Error) > 300 {
			t.Errorf("problem %+v", p)
		}
	}
	if r.Problems[0].Item != "claude_team1_hostile" || r.Problems[1].Item != "claude_team1_odd" ||
		!strings.Contains(r.Problems[1].Error, `"done"`) || strings.Contains(r.Problems[0].Error, long) {
		t.Fatalf("problems %+v", r.Problems)
	}
	if r.Retired.Final != 3 || r.Retired.Pending != 0 {
		t.Fatalf("counts %+v", r.Retired)
	}
	for _, m := range r.Retired.Markers {
		if m.State != "final" {
			t.Errorf("marker %s state %q, want final", m.Key, m.State)
		}
	}
}

// --compare

func (f *auditFixture) compareRetired(t *testing.T, earlier string) (retiredOut, string, int) {
	t.Helper()
	return f.retiredAudit(t, "--strict", "--compare", earlier)
}

func TestAuditCompareRetiredNewMarkerIsFine(t *testing.T) {
	f := newAuditEnv(t)
	kv := f.retiredKV(t)
	f.putMarker(t, kv, "claude", "team1", "a", "UA", store.RetiredFinal)
	first := f.auditTo(t, "first.json")
	f.putMarker(t, kv, "claude", "team1", "b", "UB", store.RetiredFinal)
	r, errb, code := f.compareRetired(t, first)
	if code != 0 || r.Compare == nil || !r.Compare.RetiredCompared || len(r.Compare.RetiredAdded) != 1 || r.Compare.RetiredAdded[0] != "claude_team1_b" ||
		len(r.Compare.RetiredFinalLost) != 0 {
		t.Fatalf("new marker: exit %d %+v err=%q", code, r.Compare, errb)
	}
	if txt, _, _ := f.run(t, "--compare", first); !strings.Contains(txt, "compare retired marker added claude_team1_b") {
		t.Errorf("text form:\n%s", txt)
	}
}

func TestAuditCompareRetiredFinalDisappearedFailsStrict(t *testing.T) {
	f := newAuditEnv(t)
	kv := f.retiredKV(t)
	f.putMarker(t, kv, "claude", "team1", "a", "UA", store.RetiredFinal)
	first := f.auditTo(t, "first.json")
	if err := kv.Delete(context.Background(), "claude_team1_a"); err != nil {
		t.Fatal(err)
	}
	r, errb, code := f.compareRetired(t, first)
	if code != 1 || len(r.Compare.RetiredFinalLost) != 1 || r.Compare.RetiredFinalLost[0] != "claude_team1_a" || !strings.Contains(errb, "retired") {
		t.Fatalf("final gone: exit %d %+v err=%q", code, r.Compare, errb)
	}
	if txt, _, _ := f.run(t, "--compare", first); !strings.Contains(txt, "compare retired marker FINAL LOST claude_team1_a") {
		t.Errorf("text form:\n%s", txt)
	}
	// without --strict it is shown only
	if _, _, code := f.retiredAudit(t, "--compare", first); code != 0 {
		t.Errorf("without --strict exit %d", code)
	}
}

func TestAuditCompareRetiredFinalWholeBucketGoneFailsStrict(t *testing.T) {
	f := newAuditEnv(t)
	f.putMarker(t, f.retiredKV(t), "claude", "team1", "a", "UA", store.RetiredFinal)
	first := f.auditTo(t, "first.json")
	f.recreateRetired(t, nil)
	r, _, code := f.compareRetired(t, first)
	if code != 1 || len(r.Compare.RetiredFinalLost) != 1 {
		t.Fatalf("bucket gone: exit %d %+v", code, r.Compare)
	}
}

func TestAuditCompareRetiredFinalBackToPendingFails(t *testing.T) {
	f := newAuditEnv(t)
	kv := f.retiredKV(t)
	f.putMarker(t, kv, "claude", "team1", "a", "UA", store.RetiredFinal)
	first := f.auditTo(t, "first.json")
	f.putMarker(t, kv, "claude", "team1", "a", "UA", store.RetiredPending)
	if r, _, code := f.compareRetired(t, first); code != 1 || len(r.Compare.RetiredFinalLost) != 1 {
		t.Fatalf("final regressed: exit %d %+v", code, r.Compare)
	}
}

func TestAuditCompareRetiredPendingBecameFinalIsFine(t *testing.T) {
	f := newAuditEnv(t)
	kv := f.retiredKV(t)
	f.putMarker(t, kv, "claude", "team1", "a", "UA", store.RetiredPending)
	first := f.auditTo(t, "first.json")
	f.putMarker(t, kv, "claude", "team1", "a", "UA", store.RetiredFinal)
	r, _, code := f.compareRetired(t, first)
	if code != 0 || len(r.Compare.RetiredPendingGone) != 0 || len(r.Compare.RetiredFinalLost) != 0 {
		t.Fatalf("pending -> final: exit %d %+v", code, r.Compare)
	}
}

func TestAuditCompareRetiredPendingGoneIsReported(t *testing.T) {
	f := newAuditEnv(t)
	kv := f.retiredKV(t)
	f.putMarker(t, kv, "claude", "team1", "a", "UA", store.RetiredPending)
	first := f.auditTo(t, "first.json")
	if err := kv.Delete(context.Background(), "claude_team1_a"); err != nil {
		t.Fatal(err)
	}
	r, _, code := f.compareRetired(t, first)
	// reported, and not a strict failure: deprovision deletes a pending marker when the retirement did not happen
	if code != 0 || len(r.Compare.RetiredPendingGone) != 1 || r.Compare.RetiredPendingGone[0] != "claude_team1_a" || len(r.Compare.RetiredFinalLost) != 0 {
		t.Fatalf("pending gone: exit %d %+v", code, r.Compare)
	}
	if txt, _, _ := f.run(t, "--compare", first); !strings.Contains(txt, "compare retired marker pending gone claude_team1_a") {
		t.Errorf("text form:\n%s", txt)
	}
}

// An earlier report from before the retired section cannot show that a final marker was not deleted since. Without
// --strict it stays readable and says so; with --strict it is refused like any earlier report that cannot prove it.
func TestAuditCompareEarlierWithoutRetiredSection(t *testing.T) {
	f := newAuditEnv(t)
	first := f.auditTo(t, "first.json")
	old := writeEdited(t, first, func(m map[string]any) { delete(m, "retired") })
	f.putMarker(t, f.retiredKV(t), "claude", "team1", "a", "UA", store.RetiredFinal)
	const msg = "retired comparison unverified: the earlier report has no retired section"

	out, errb, code := f.run(t, "--strict", "--compare", old)
	if code != 3 || out != "" || !strings.Contains(errb, msg) {
		t.Fatalf("strict: exit %d out=%q err=%q", code, out, errb)
	}

	var r retiredOut
	jout, errb, code := f.run(t, "--json", "--compare", old)
	if code != 0 {
		t.Fatalf("non-strict json: exit %d err=%q", code, errb)
	}
	if err := json.Unmarshal([]byte(jout), &r); err != nil || r.Compare == nil || r.Compare.RetiredCompared || !r.Compare.RetiredUnverified || len(r.Compare.RetiredAdded) != 0 {
		t.Fatalf("non-strict json compare: %v %+v", err, r.Compare)
	}
	txt, _, code := f.run(t, "--compare", old)
	if code != 0 || !strings.Contains(txt, "WARNING "+msg) {
		t.Errorf("text form must warn (exit %d):\n%s", code, txt)
	}
	// a comparison against a report that has the section is not unverified
	if r2, _, _ := f.compareRetired(t, first); r2.Compare.RetiredUnverified {
		t.Errorf("a whole earlier report must not be unverified: %+v", r2.Compare)
	}
}

// The unverified failure does not depend on the current report: the bucket may be the thing that was deleted.
func TestAuditCompareEarlierWithoutRetiredSectionStrictEmptyNow(t *testing.T) {
	f := newAuditEnv(t)
	old := writeEdited(t, f.auditTo(t, "first.json"), func(m map[string]any) { delete(m, "retired") })
	out, errb, code := f.run(t, "--strict", "--compare", old)
	if code != 3 || out != "" || !strings.Contains(errb, "retired comparison unverified") {
		t.Fatalf("exit %d out=%q err=%q", code, out, errb)
	}
}

// A retired section that is there must be whole: a stripped one would hide a deleted final marker.
func TestAuditCompareEarlierIncompleteRetiredSectionIsRefused(t *testing.T) {
	f := newAuditEnv(t)
	f.putMarker(t, f.retiredKV(t), "claude", "team1", "a", "UA", store.RetiredFinal)
	first := f.auditTo(t, "first.json")
	cases := map[string]struct {
		edit func(m map[string]any)
		want string
	}{
		"no markers": {func(m map[string]any) { delete(m["retired"].(map[string]any), "markers") }, "retired.markers"},
		"no present": {func(m map[string]any) { delete(m["retired"].(map[string]any), "present") }, "retired.present"},
		"marker keyless": {func(m map[string]any) {
			delete(m["retired"].(map[string]any)["markers"].([]any)[0].(map[string]any), "key")
		}, "retired.markers[0].key"},
		"marker stateless": {func(m map[string]any) {
			delete(m["retired"].(map[string]any)["markers"].([]any)[0].(map[string]any), "state")
		}, "retired.markers[0].state"},
	}
	for name, tc := range cases {
		out, errb, code := f.run(t, "--strict", "--compare", writeEdited(t, first, tc.edit))
		if code != 3 || out != "" || !strings.Contains(errb, "lacks "+tc.want) {
			t.Errorf("%s: exit %d out=%q err=%q", name, code, out, errb)
		}
	}
	// a null section is no section: unverified, exit 3 under --strict, readable without it
	null := writeEdited(t, first, func(m map[string]any) { m["retired"] = nil })
	if out, errb, code := f.run(t, "--strict", "--compare", null); code != 3 || out != "" || !strings.Contains(errb, "retired comparison unverified") {
		t.Errorf("null retired strict: exit %d out=%q err=%q", code, out, errb)
	}
	if txt, errb, code := f.run(t, "--compare", null); code != 0 || !strings.Contains(txt, "WARNING retired comparison unverified") {
		t.Errorf("null retired non-strict: exit %d err=%q\n%s", code, errb, txt)
	}
}

// A marker state compareRetired does not know would let a deleted final marker escape --strict, and a duplicate key
// would let the last entry win and hide a lost final: both are refused.
func TestAuditCompareEarlierBadRetiredMarkersAreRefused(t *testing.T) {
	f := newAuditEnv(t)
	f.putMarker(t, f.retiredKV(t), "claude", "team1", "a", "UA", store.RetiredFinal)
	first := f.auditTo(t, "first.json")
	setState := func(st string) func(m map[string]any) {
		return func(m map[string]any) {
			m["retired"].(map[string]any)["markers"].([]any)[0].(map[string]any)["state"] = st
		}
	}
	cases := map[string]struct {
		edit func(m map[string]any)
		want string
	}{
		"state Final": {setState("Final"), "retired.markers[0].state"},
		"state done":  {setState("done"), "retired.markers[0].state"},
		"duplicate key": {func(m map[string]any) {
			r := m["retired"].(map[string]any)
			ms := r["markers"].([]any)
			dup := map[string]any{}
			for k, v := range ms[0].(map[string]any) {
				dup[k] = v
			}
			dup["state"] = store.RetiredPending
			r["markers"] = append(ms, dup)
		}, "retired.markers[1].key"},
	}
	for name, tc := range cases {
		out, errb, code := f.run(t, "--strict", "--compare", writeEdited(t, first, tc.edit))
		if code != 3 || out != "" || !strings.Contains(errb, tc.want) {
			t.Errorf("%s: exit %d out=%q err=%q", name, code, out, errb)
		}
	}
}

func TestAuditDocsDescribeRetired(t *testing.T) {
	raw, err := os.ReadFile("../../docs/audit.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"### `retired`", "`percent_used`", "`retired_final_lost`", "`retired_pending_gone`", "`retired_compared`", "`max_bytes`"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("docs/audit.md lacks %s", want)
		}
	}
}
