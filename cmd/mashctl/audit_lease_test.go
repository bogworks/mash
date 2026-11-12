package main

import (
	"context"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/store"
)

// leaseProblems returns the audit's problems about the lease bucket.
func leaseProblems(r retiredOut) []string {
	var out []string
	for _, p := range r.Problems {
		if p.What == "lease" {
			out = append(out, p.Item+": "+p.Error)
		}
	}
	return out
}

func TestAuditHardenedLeaseBucketIsClean(t *testing.T) {
	f := newAuditEnv(t)
	r, _, code := f.retiredAudit(t, "--strict")
	if code != 0 || len(r.Problems) != 0 {
		t.Fatalf("hardened bucket: exit %d %+v", code, r.Problems)
	}
}

func TestAuditLeaseBucketThatAllowsRollupIsAProblemUntilInit(t *testing.T) {
	f := newAuditEnv(t)
	ctx := context.Background()
	if err := f.js.DeleteKeyValue(ctx, store.LeaseBucket); err != nil {
		t.Fatal(err)
	}
	if _, err := f.js.CreateKeyValue(ctx, store.LeaseConfigFor(store.DefaultLimits())); err != nil { // as an older mash made it
		t.Fatal(err)
	}
	r, errb, code := f.retiredAudit(t, "--strict")
	lp := leaseProblems(r)
	if code != 1 || len(lp) != 1 || !strings.Contains(lp[0], `lease bucket allows rollup writes; run "mash admin init" to harden it`) ||
		!strings.Contains(errb, "--strict") {
		t.Fatalf("un-hardened bucket: exit %d %v err=%q", code, lp, errb)
	}
	if txt, _, _ := f.run(t); !strings.Contains(txt, "allows rollup writes") {
		t.Errorf("text form must report it:\n%s", txt)
	}
	if _, errb, code := ctlRun(t, f.e, "init"); code != 0 {
		t.Fatalf("init: %d %q", code, errb)
	}
	r, _, code = f.retiredAudit(t, "--strict")
	if code != 0 || len(r.Problems) != 0 {
		t.Fatalf("after init: exit %d %+v", code, r.Problems)
	}
}

func TestAuditNoLeaseBucketIsNotAProblem(t *testing.T) {
	f := newAuditEnv(t)
	if err := f.js.DeleteKeyValue(context.Background(), store.LeaseBucket); err != nil {
		t.Fatal(err)
	}
	r, _, code := f.retiredAudit(t, "--strict")
	if code != 0 || len(leaseProblems(r)) != 0 {
		t.Fatalf("no lease bucket: exit %d %+v", code, r.Problems)
	}
}
