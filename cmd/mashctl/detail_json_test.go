package main

// --detail only adds: it adds nothing unless the specification writes the form out, and the only form it writes out is
// `endpoint reissue`'s: `servers acknowledged: N` in the text, and a `servers_acknowledged` key in --json. Every other
// command prints the same bytes, on stdout and on stderr, in text and in --json, with --detail or without.

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

func detailObj(t *testing.T, out string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("not a JSON object: %q", out)
	}
	return m
}

// sameBytes asserts two runs printed the same, after the names of the runs (subs, pairs of old, new) are made alike.
func sameBytes(t *testing.T, what, plain, detail string, subs []string) {
	t.Helper()
	r := strings.NewReplacer(subs...)
	if r.Replace(plain) != r.Replace(detail) {
		t.Errorf("%s differs with --detail:\nplain  %q\ndetail %q", what, plain, detail)
	}
}

func TestEndpointCreateDetailChangesNothing(t *testing.T) {
	for _, mode := range []string{"text", "--json"} {
		for _, join := range []bool{false, true} {
			t.Run(mode, func(t *testing.T) {
				h := newFleetEnv(t)
				dir := t.TempDir()
				a1, a2 := ad("c1"), ad("c2")
				run := func(a sap.Address, extra ...string) (string, string) {
					args := []string{"endpoint", "create", a.String()}
					if join {
						args = append(args, "--out", filepath.Join(dir, a.Instance+".join"))
					}
					if mode != "text" {
						args = append(args, mode)
					}
					so, se, code := h.run(append(args, extra...)...)
					if code != 0 {
						t.Fatalf("%v: %d %q %q", extra, code, so, se)
					}
					return so, se
				}
				po, pe := run(a1)
				do, de := run(a2, "--detail")
				subs := []string{a1.String(), "X", a2.String(), "X", filepath.Join(dir, "c1"), "X", filepath.Join(dir, "c2"), "X"}
				sameBytes(t, "stdout", po, do, subs)
				sameBytes(t, "stderr", pe, de, subs)
			})
		}
	}
}

func TestEndpointReissueDetail(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	setFakeAcked(t, 3)
	a := ad("re")
	if _, se, code := h.run("endpoint", "create", a.String()); code != 0 {
		t.Fatal(se)
	}
	run := func(extra ...string) string {
		so, se, code := h.run(append([]string{"endpoint", "reissue", a.String()}, extra...)...)
		if code != 0 {
			t.Fatalf("%v: %d %q %q", extra, code, so, se)
		}
		if se != "" {
			t.Errorf("%v: stderr %q", extra, se)
		}
		return so
	}
	// text: --detail adds exactly the one line
	plain, text := run(), run("--detail")
	if text != plain+"servers acknowledged: 3\n" {
		t.Errorf("text %q, plain %q", text, plain)
	}
	// --json: the usual object, plus servers_acknowledged only with --detail
	pj, fj := detailObj(t, run("--json")), detailObj(t, run("--json", "--detail"))
	if _, ok := pj["servers_acknowledged"]; ok {
		t.Errorf("plain --json has servers_acknowledged: %v", pj)
	}
	if fj["servers_acknowledged"] != float64(3) {
		t.Errorf("servers_acknowledged %v", fj["servers_acknowledged"])
	}
	delete(fj, "servers_acknowledged")
	if !reflect.DeepEqual(pj, fj) {
		t.Errorf("the usual object changed: %v vs %v", pj, fj)
	}
	// --dry-run prints only its own line or object
	if p, d := run("--json", "--dry-run"), run("--json", "--dry-run", "--detail"); p != d {
		t.Errorf("dry-run json: %q vs %q", p, d)
	}
	if p, d := run("--dry-run"), run("--dry-run", "--detail"); p != d {
		t.Errorf("dry-run text: %q vs %q", p, d)
	}
}

func TestEndpointReissueDetailJSONAlwaysHasTheCount(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	setFakeAcked(t, 0)
	a := ad("rz")
	if _, se, code := h.run("endpoint", "create", a.String()); code != 0 {
		t.Fatal(se)
	}
	so, se, code := h.run("endpoint", "reissue", a.String(), "--json", "--detail")
	if code != 0 {
		t.Fatalf("%d %q %q", code, so, se)
	}
	if v, ok := detailObj(t, so)["servers_acknowledged"]; !ok || v != float64(0) {
		t.Errorf("servers_acknowledged %v (present %v) in %q", v, ok, so)
	}
}

func TestEndpointDeleteDetailChangesNothing(t *testing.T) {
	for _, mode := range []string{"text", "--json"} {
		t.Run(mode, func(t *testing.T) {
			h := newFleetEnv(t)
			fakeRevoke(t, nil)
			setFakeAcked(t, 0)
			as := []sap.Address{ad("d1"), ad("d2")}
			for _, a := range as {
				h.seed(a)
				h.killedDelete(a, store.RetiredFinal)
			}
			run := func(a sap.Address, extra ...string) (string, string) {
				args := []string{"endpoint", "delete", a.String(), "--yes"}
				if mode != "text" {
					args = append(args, mode)
				}
				so, se, code := h.run(append(args, extra...)...)
				if code != 0 {
					t.Fatalf("%v: %d %q %q", extra, code, so, se)
				}
				return so, se
			}
			po, pe := run(as[0])
			do, de := run(as[1], "--detail")
			subs := []string{as[0].String(), "X", as[1].String(), "X"}
			sameBytes(t, "stdout", po, do, subs)
			sameBytes(t, "stderr", pe, de, subs)
		})
	}
}

func TestInitDetailChangesNothing(t *testing.T) {
	for _, mode := range []string{"text", "--json"} {
		for _, dry := range []bool{false, true} {
			t.Run(mode, func(t *testing.T) {
				e := newApplyEnv(t)
				one, two := newIniter(t, e), newIniter(t, e)
				args := []string{}
				if mode != "text" {
					args = append(args, mode)
				}
				if dry {
					args = append(args, "--dry-run")
				}
				po, pe, c1 := one.init(args...)
				do, de, c2 := two.init(append(append([]string{}, args...), "--detail")...)
				if c1 != 0 || c2 != 0 {
					t.Fatalf("exit %d %d: %q %q", c1, c2, po, do)
				}
				subs := []string{one.root, "X", two.root, "X"}
				sameBytes(t, "stdout", po, do, subs)
				sameBytes(t, "stderr", pe, de, subs)
			})
		}
	}
}

func TestLimitsApplyDetailChangesNothing(t *testing.T) {
	for _, mode := range []string{"text", "--json"} {
		t.Run(mode, func(t *testing.T) {
			h := newFleetEnv(t)
			as := []sap.Address{ad("l1"), ad("l2")}
			for _, a := range as {
				h.seed(a)
			}
			h.setLimits(`{"inbox_max_bytes": "32MiB"}`)
			run := func(a sap.Address, extra ...string) (string, string) {
				args := []string{"limits", "apply", a.String()}
				if mode != "text" {
					args = append(args, mode)
				}
				so, se, code := h.run(append(args, extra...)...)
				if code != 0 {
					t.Fatalf("%v: %d %q %q", extra, code, so, se)
				}
				return so, se
			}
			po, pe := run(as[0])
			do, de := run(as[1], "--detail")
			if !strings.Contains(po, "1") {
				t.Fatalf("nothing was raised: %q", po)
			}
			subs := []string{as[0].String(), "X", as[1].String(), "X"}
			sameBytes(t, "stdout", po, do, subs)
			sameBytes(t, "stderr", pe, de, subs)
		})
	}
}
