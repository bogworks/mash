package endpoint

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/sap"
)

// cfg.Identity validates the address first, so the builder's refusal is reached here, one layer below it, with an
// address no constructor made.
var noSubjectAddr = sap.Address{Owner: "acme", Group: "o.ps", Instance: "alpha"}

func TestEndpointStartNoSubject(t *testing.T) {
	s, err := subjectsOf(noSubjectAddr)
	if err == nil || s != (sap.Subjects{}) {
		t.Fatalf("got %+v, %v", s, err)
	}
	var out, errb bytes.Buffer
	code := cliframe.Report(&out, &errb, err, false)
	want := "error: the endpoint could not start: no subject can be built for acme.o.ps.alpha\n"
	if errb.String() != want || out.Len() != 0 || code != 1 {
		t.Fatalf("stderr %q stdout %q code %d, want %q", errb.String(), out.String(), code, want)
	}
}

func TestDiscoverNoSubject(t *testing.T) {
	// The connection is nil and never used: the subject is built before anything is asked of it, so nothing is published.
	subj, err := Discover(context.Background(), nil, noSubjectAddr)
	var ns *sap.NoSubjectError
	if !errors.As(err, &ns) || subj != "" {
		t.Fatalf("got %q, %v", subj, err)
	}
}
