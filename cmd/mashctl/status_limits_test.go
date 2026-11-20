package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/testenv"
)

func TestStatusOneServerOK(t *testing.T) {
	h := newFleetEnv(t)
	h.seed(ad("a"))
	h.seed(ad("b"))
	h.seed(ad("c"))
	h.holdLease("b")
	out, errb, code := h.run("status")
	if want := h.e.URL + ": ok, 3 endpoints, 1 online\n"; code != 0 || out != want || errb != "" {
		t.Fatalf("status: %d %q %q", code, out, errb)
	}
	out, errb, code = h.run("status", "--json")
	var r struct {
		Server    string  `json:"server"`
		OK        bool    `json:"ok"`
		Endpoints int     `json:"endpoints"`
		Online    int     `json:"online"`
		Reason    *string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil || code != 0 || errb != "" || r.Server != h.e.URL || !r.OK || r.Endpoints != 3 || r.Online != 1 || r.Reason != nil {
		t.Fatalf("status --json: %d %v %q %q", code, err, out, errb)
	}
}

func TestStatusOneEndpointIsSingular(t *testing.T) {
	h := newFleetEnv(t)
	h.seed(ad("a"))
	if out, _, code := h.run("status"); code != 0 || out != h.e.URL+": ok, 1 endpoint, 0 online\n" {
		t.Fatalf("%d %q", code, out)
	}
}

func TestStatusServerDown(t *testing.T) {
	h := newFleetEnv(t)
	h.env["MASH_SERVER"] = "nats://127.0.0.1:1"
	out, errb, code := h.run("status")
	if want := "nats://127.0.0.1:1: not ok: the server did not answer\n"; code != 1 || out != want || errb != "hint: mashctl doctor\n" {
		t.Fatalf("status: %d %q %q", code, out, errb)
	}
	out, errb, code = h.run("status", "--json")
	var r struct {
		Server    string  `json:"server"`
		OK        bool    `json:"ok"`
		Endpoints *int    `json:"endpoints"`
		Reason    *string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil || code != 1 || errb != "" || r.OK || r.Endpoints != nil || r.Reason == nil || *r.Reason != "the server did not answer" {
		t.Fatalf("status --json: %d %v %q %q", code, err, out, errb)
	}
}

// Several servers: one line each; the exit is 1 when any is not ok, and the hint is said once, on stderr.
func TestStatusSeveralServers(t *testing.T) {
	h := newFleetEnv(t)
	h.seed(ad("a"))
	h.env["MASH_SERVER"] = h.e.URL + ",nats://127.0.0.1:1"
	out, errb, code := h.run("status")
	want := h.e.URL + ": ok, 1 endpoint, 0 online\nnats://127.0.0.1:1: not ok: the server did not answer\n"
	if code != 1 || out != want || errb != "hint: mashctl doctor\n" {
		t.Fatalf("status: %d %q %q", code, out, errb)
	}
	out, _, code = h.run("status", "--json")
	var r struct {
		Servers []struct {
			Server string `json:"server"`
			OK     bool   `json:"ok"`
		} `json:"servers"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil || code != 1 || len(r.Servers) != 2 || !r.Servers[0].OK || r.Servers[1].OK {
		t.Fatalf("status --json: %d %v %q", code, err, out)
	}
}

// A server whose storage was never made is not ok, with a reason; the verb reports and does not fail.
func TestStatusWithoutStorage(t *testing.T) {
	e := testenv.Start(t)
	out, errb, code := ctlRun(t, e, "status")
	if code != 1 || !strings.HasPrefix(out, e.URL+": not ok: ") || errb != "hint: mashctl doctor\n" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
}

func TestLimitsShow(t *testing.T) {
	h := newFleetEnv(t)
	out, errb, code := h.run("limits", "show")
	if code != 0 || out != "inbox size:  16 MiB\ninbox age:   30 days\n" || errb != "" {
		t.Fatalf("show: %d %q %q", code, out, errb)
	}
	out, _, code = h.run("limits", "show", "--json")
	var r struct {
		Bytes int64  `json:"inbox_bytes"`
		Age   string `json:"inbox_age"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil || code != 0 || r.Bytes != 16<<20 || r.Age != "720h" {
		t.Fatalf("show --json: %d %v %q", code, err, out)
	}
	h.setLimits(`{"inbox_max_bytes": "64MiB", "inbox_max_age": "36h"}`)
	if out, _, code = h.run("limits", "show"); code != 0 || out != "inbox size:  64 MiB\ninbox age:   36 hours\n" {
		t.Fatalf("show custom: %d %q", code, out)
	}
	// it reads no server
	h.env["MASH_SERVER"] = "nats://127.0.0.1:1"
	if _, _, code = h.run("limits", "show"); code != 0 {
		t.Fatalf("show with no server: %d", code)
	}
}

func TestLimitsNeedsASubVerb(t *testing.T) {
	h := newFleetEnv(t)
	out, errb, code := h.run("limits")
	if code != 2 || out != "" || !strings.Contains(errb, "show") || !strings.Contains(errb, "apply") {
		t.Fatalf("limits: %d %q %q", code, out, errb)
	}
	if out, errb, code = h.run("limits", "--show"); code != 2 || out != "" || errb != "error: unknown option --show\nhint: mashctl limits --help\n" {
		t.Fatalf("limits --show: %d %q %q", code, out, errb)
	}
	if _, errb, code = h.run("limits", "frob"); code != 2 || errb != "error: mashctl limits takes show or apply\nhint: mashctl limits --help\n" {
		t.Fatalf("limits frob: %d %q", code, errb)
	}
}

// setLimits writes the "limits" object of config.json in the fleet's config folder.
func (h *fleetEnv) setLimits(body string) {
	h.t.Helper()
	if err := os.WriteFile(filepath.Join(h.dir, "config.json"), []byte(`{"limits": `+body+`}`), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func TestLimitsApply(t *testing.T) {
	h := newFleetEnv(t)
	a, b := ad("a"), sap.Address{Owner: testenv.Owner, Group: "h", Instance: "b"}
	h.seed(a)
	h.seed(b)

	if out, errb, code := h.run("limits", "apply"); code != 0 || out != "Nothing to raise\n" || errb != "" {
		t.Fatalf("nothing: %d %q %q", code, out, errb)
	}
	h.setLimits(`{"inbox_max_bytes": "32MiB"}`)

	// --dry-run says what it would do and changes nothing
	out, errb, code := h.run("limits", "apply", "--dry-run")
	if code != 0 || out != "Would raise 2 inboxes\nNothing was changed.\n" || errb != "" {
		t.Fatalf("dry run: %d %q %q", code, out, errb)
	}
	if out, _, _ = h.run("limits", "apply", "--dry-run", "--json"); !strings.Contains(out, `"would_raise": 2`) || !strings.Contains(out, `"raised": 0`) {
		t.Fatalf("dry run json: %q", out)
	}

	// an owner.group prefix selects only that group
	if out, errb, code = h.run("limits", "apply", testenv.Owner+".g"); code != 0 || out != "Raised 1 inbox\n" || errb != "" {
		t.Fatalf("group prefix: %d %q %q", code, out, errb)
	}
	// one address, with --verbose naming it
	if out, _, code = h.run("limits", "apply", b.String(), "--detail"); code != 0 || !strings.HasPrefix(out, "Raised 1 inbox\n"+b.String()+": max_bytes 16777216 -> 33554432") {
		t.Fatalf("one address: %d %q", code, out)
	}
	// nothing left, by every prefix form
	for _, args := range [][]string{{"limits", "apply"}, {"limits", "apply", testenv.Owner}, {"limits", "apply", testenv.Owner + ".*"}, {"limits", "apply", a.String()}} {
		if out, errb, code = h.run(args...); code != 0 || out != "Nothing to raise\n" || errb != "" {
			t.Fatalf("%v: %d %q %q", args, code, out, errb)
		}
	}
	// an owner with no endpoints has nothing to raise
	if out, _, code = h.run("limits", "apply", "nobody"); code != 0 || out != "Nothing to raise\n" {
		t.Fatalf("empty owner: %d %q", code, out)
	}
	// JSON
	h.setLimits(`{"inbox_max_bytes": "48MiB"}`)
	out, _, code = h.run("limits", "apply", "--json")
	var r struct {
		Raised int `json:"raised"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil || code != 0 || r.Raised != 2 {
		t.Fatalf("json: %d %v %q", code, err, out)
	}
	// it never lowers: the refusal is an error, and the inboxes keep what they have
	h.setLimits(`{"inbox_max_bytes": "8MiB"}`)
	out, errb, code = h.run("limits", "apply")
	if code != 1 || out != "" || !strings.HasPrefix(errb, "error: ") || !strings.Contains(errb, "never lowers") {
		t.Fatalf("lower: %d %q %q", code, out, errb)
	}
	// an address with no record, and a bad prefix
	if _, errb, code = h.run("limits", "apply", testenv.Owner+".g.zz"); code != 1 || errb != "error: no endpoint "+testenv.Owner+".g.zz\nhint: mashctl endpoint list\n" {
		t.Fatalf("no record: %d %q", code, errb)
	}
	if out, errb, code = h.run("limits", "apply", "Not.A.Prefix"); code != 2 || out != "" || !strings.HasPrefix(errb, "error: not an owner") {
		t.Fatalf("bad prefix: %d %q %q", code, out, errb)
	}
}
