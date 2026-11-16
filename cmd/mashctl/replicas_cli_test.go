package main

import (
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
)

// init takes --replicas, refuses a value that cannot be used with the allowed values named, and limits --show says the
// effective count.
func TestInitReplicasFlagAndLimitsShow(t *testing.T) {
	h := newAddEnv(t)
	if _, errb, code := h.run("init", "--replicas", "3"); code != 1 || !strings.Contains(errb, "replicas 3 needs at least 3 servers and none are declared") {
		t.Fatalf("init --replicas 3 on no declared servers: %d %q", code, errb)
	}
	if _, errb, code := h.run("init", "--replicas=5"); code != 1 || !strings.Contains(errb, "auto, 1 and 3") {
		t.Fatalf("init --replicas=5: %d %q", code, errb)
	}
	if _, errb, code := h.run("init", "--replicas"); code != 2 || !strings.Contains(errb, "usage:") || !strings.Contains(errb, "--replicas auto|1|3") {
		t.Fatalf("init --replicas without a value: %d %q", code, errb)
	}
	if _, errb, code := h.run("init", "--bogus"); code != 2 {
		t.Fatalf("init --bogus: %d %q", code, errb)
	}
	for _, v := range []string{"auto", "1"} {
		if out, errb, code := h.run("init", "--replicas", v); code != 0 {
			t.Fatalf("init --replicas %s: %d %q %q", v, code, out, errb)
		}
	}
	if out, _, code := h.run("limits", "--show"); code != 0 || !strings.Contains(out, "replicas 1 (setting auto, servers 0)\n") {
		t.Fatalf("limits --show: %d %q", code, out)
	}
	h.env["MASH_SERVERS"], h.env["MASH_REPLICAS"] = "3", "auto"
	if out, _, code := h.run("limits", "--show"); code != 0 || !strings.Contains(out, "replicas 3 (setting auto, servers 3)\n") {
		t.Fatalf("limits --show with 3 servers: %d %q", code, out)
	}
	h.env["MASH_REPLICAS"] = "5"
	if _, errb, code := h.run("limits", "--show"); code != 1 || !strings.Contains(errb, "auto, 1 and 3") {
		t.Fatalf("limits --show with replicas 5: %d %q", code, errb)
	}
}

func TestInitLocalRefusesMoreThanOneReplica(t *testing.T) {
	h := newAddEnv(t)
	if _, e, code := h.run("init", "--local", "--owner", "o", "--dir", t.TempDir(), "--replicas", "3"); code != 1 || !strings.Contains(e, "replicas 3 needs at least 3 servers and 1 are declared") {
		t.Fatalf("init --local --replicas 3: %d %q", code, e)
	}
}

// buildAudit turns the snapshot's replica finding into one problem, and no finding leaves the report as it was.
func TestAuditReplicaFinding(t *testing.T) {
	clean := buildAudit("o", admin.AuditSnapshot{}, nil, nil, nil)
	if len(clean.Problems) != 0 || clean.findings() != 0 {
		t.Fatalf("a snapshot with no finding: %+v", clean.Problems)
	}
	rep := buildAudit("o", admin.AuditSnapshot{ReplicaFinding: "2 mash-owned object(s) have fewer than 3 replicas (a, b): one server down would stop writes; raise the replica count of mash-owned state to 3"}, nil, nil, nil)
	if len(rep.Problems) != 1 || rep.Problems[0].What != "replicas" || rep.Summary.Problems != 1 || rep.findings() != 1 {
		t.Fatalf("%+v", rep.Problems)
	}
	var b strings.Builder
	writeAuditText(&b, rep)
	if !strings.Contains(b.String(), "raise the replica count of mash-owned state to 3") {
		t.Errorf("the finding is not in the text report:\n%s", b.String())
	}
}
