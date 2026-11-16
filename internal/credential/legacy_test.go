package credential

import (
	"context"
	"testing"
)

type legacyDir struct {
	owner         string
	legacy, found bool
}

func (d legacyDir) LegacyOwner(context.Context, string) (string, bool, bool, error) {
	return d.owner, d.legacy, d.found, nil
}

// A legacy endpoint (cc.<owner>.<name>) is issued through the primitive only with a directory that backs it (ADR 0138).
func TestIssueForALegacyEndpoint(t *testing.T) {
	f := newFixture(t)
	req := newReq(t, "c.core")
	req.Endpoint = "cc.c.core"
	res, err := Issue(context.Background(), f.cfg, req) // no Directory: fail closed
	if err != nil || res.Outcome != OutcomeRefused || refusalCode(res) != "endpoint_mismatch" {
		t.Fatalf("%+v %v", res, err)
	}
	f.cfg.Directory = legacyDir{owner: "d", legacy: true, found: true}
	if res, _ = Issue(context.Background(), f.cfg, req); refusalCode(res) != "endpoint_mismatch" {
		t.Fatalf("another owner: %+v", res)
	}
	f.cfg.Directory = legacyDir{owner: "c", legacy: true, found: true}
	res, err = Issue(context.Background(), f.cfg, req)
	if err != nil || res.Outcome != OutcomeIssued {
		t.Fatalf("%+v %v", res, err)
	}
	found := false
	for _, tag := range res.Tags {
		found = found || tag == "ep:core"
	}
	if !found || len(f.rec.recs) != 1 {
		t.Fatalf("tags %v, records %d", res.Tags, len(f.rec.recs))
	}
}
