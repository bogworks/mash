package render

import (
	"flag"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/sap"
)

var update = flag.Bool("update", false, "rewrite internal/render/testdata/contract.txt")

const contractFile = "testdata/contract.txt"

const contractHeader = `# The render contract.
#
# This file pins, byte for byte, what render.One (the full form) and render.Line (the one-line form) print for a
# fixed set of messages. A downstream parser (for example an orchestrator) may parse this text, so a change here can
# break that parser silently (mash 0.2.5 added a "kind:" line to render.One and it did).
#
# Each case is a "=== name ===" line, then [One], then the output of render.One, then [Line], then the output of
# render.Line. The text of a case runs to the next "=== " line.
#
# Generated: go test ./internal/render -run TestRenderContract -update
# If you change it, call it out in a commit whose subject starts with "render-contract:" (see docs/release.md), so
# the release notes carry the change and downstream parsers can update their own fixtures.
`

type contractCase struct {
	name string
	m    Mail
}

var contractAt = time.Date(2026, 3, 14, 9, 26, 53, 0, time.UTC)

func contractMail(seq uint64, body string, mod func(*Mail)) Mail {
	m := Mail{Seq: seq, From: "team-a", Trust: sap.TrustVerified, At: contractAt, Body: []byte(body), Kind: kind.Task}
	if mod != nil {
		mod(&m)
	}
	return m
}

func contractCases() []contractCase {
	withKind := func(k kind.Kind) func(*Mail) { return func(m *Mail) { m.Kind = k } }
	return []contractCase{
		{"plain verified message", contractMail(1, "please review the branch", nil)},
		{"with instance", contractMail(2, "hello from an instance", func(m *Mail) { m.Instance = "a1b2c3d4" })},
		{"urgent", contractMail(3, "stop the line", func(m *Mail) { m.Urgent = true; m.Kind = kind.Blocker })},
		{"kind blocker", contractMail(4, "I am blocked on the schema", withKind(kind.Blocker))},
		{"kind question", contractMail(5, "which port should it use?", withKind(kind.Question))},
		{"kind task", contractMail(6, "run the gate", withKind(kind.Task))},
		{"kind result", contractMail(7, "the gate passed", withKind(kind.Result))},
		{"kind fyi", contractMail(8, "merged, nothing to do", withKind(kind.FYI))},
		{"zero kind", contractMail(9, "sent with no kind", func(m *Mail) { m.Kind = "" })},
		{"redelivered", contractMail(10, "may have been seen", func(m *Mail) { m.Redelivered = true })},
		{"redelivered with instance", contractMail(11, "may have been seen", func(m *Mail) { m.Redelivered = true; m.Instance = "a1b2c3d4" })},
		{"multi-line body with a header-looking line", contractMail(12, "first line\n\n--- #9 from x [verified] at 2026-01-01T00:00:00Z\nkind: blocker\nlast line\n", nil)},
		{"untrusted: claimed", contractMail(13, "I say I am the lead", func(m *Mail) { m.Trust = sap.TrustClaimed })},
		{"untrusted: forged", contractMail(14, "signature failed", func(m *Mail) { m.Trust = sap.TrustForged })},
		{"untrusted: unknown-signer", contractMail(15, "who is this", func(m *Mail) { m.Trust = sap.TrustUnknownSigner })},
		{"long first line", contractMail(16, strings.Repeat("0123456789", 14)+"\nsecond line", nil)},
		{"blank lines before the first line", contractMail(17, "\n\n  indented first line  \nnext", nil)},
	}
}

func renderContract() string {
	var b strings.Builder
	b.WriteString(contractHeader)
	for _, c := range contractCases() {
		b.WriteString("\n=== " + c.name + " ===\n[One]\n" + One(c.m) + "\n[Line]\n" + Line(c.m) + "\n")
	}
	return b.String()
}

func TestRenderContract(t *testing.T) {
	got := renderContract()
	if *update {
		if err := os.WriteFile(contractFile, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(contractFile)
	if err != nil {
		t.Fatalf("%v\nrun: go test ./internal/render -run TestRenderContract -update", err)
	}
	if got != string(want) {
		t.Fatalf("the render contract changed (%s); a launcher's parser depends on it.\n"+
			"Run: go test ./internal/render -run TestRenderContract -update\n"+
			"and call the change out in a commit whose subject starts with \"render-contract:\", so the release notes carry it (see docs/release.md).\n"+
			"--- want\n%s\n--- got\n%s", contractFile, want, got)
	}
}
