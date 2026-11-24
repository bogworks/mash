package sap_test

import (
	"testing"

	agents "github.com/bogworks/synadia-agents.go"

	"github.com/bogworks/mash/internal/sap"
)

// This is the first import of the SAP Go SDK. It keeps the requirement in go.mod under `go mod tidy`, and it checks
// that the subjects mash builds are byte-for-byte the ones the SDK builds for the same identity. The SDK's third
// subject token is mash's owner, its owner token is mash's group and its name is mash's instance.
func TestAddressSubjectsMatchTheSDK(t *testing.T) {
	a, err := sap.NewAddress("acme", "ops", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	s, err := agents.NewAgentSubject("claude-code", a.Group, a.Instance, agents.WithSubjectToken(a.Owner))
	if err != nil {
		t.Fatal(err)
	}
	got := a.Subjects()
	for _, c := range []struct{ verb, mash, sdk string }{
		{"prompt", got.Prompt, s.Prompt()},
		{"status", got.Status, s.Status()},
		{"hb", got.HB, s.Heartbeat()},
	} {
		if c.mash != c.sdk {
			t.Errorf("%s subject: mash %q, SDK %q", c.verb, c.mash, c.sdk)
		}
	}
}
