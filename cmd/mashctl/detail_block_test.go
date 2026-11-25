package main

import (
	"bytes"
	"testing"

	"github.com/bogworks/mash/internal/sap"
)

// The label:value lines after the usual line of reissue --detail are one block: labels padded to the longest label
// ("servers acknowledged:") plus one space (#1050). Without --detail the block is the one Join file line.
func TestReissueDetailBlockOneWidth(t *testing.T) {
	a, _ := sap.ParseAddress("acme.demo.bob")
	tg := epTarget{addr: a, join: true, outPath: "acme.demo.bob.join"}
	var w bytes.Buffer
	epPrint(&w, "reissue", tg, epResult{acked: 2}, true, false)
	if want := "Reissued credentials for acme.demo.bob\nJoin file:            acme.demo.bob.join\nservers acknowledged: 2\n"; w.String() != want {
		t.Errorf("detail %q", w.String())
	}
	w.Reset()
	epPrint(&w, "reissue", tg, epResult{acked: 2}, false, false)
	if want := "Reissued credentials for acme.demo.bob\nJoin file: acme.demo.bob.join\n"; w.String() != want {
		t.Errorf("plain %q", w.String())
	}
}
