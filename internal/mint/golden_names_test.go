// Characterisation tests (plan 2b, Phase 0): these pin today's identifiers and validity for legacy identities; a change here is a change to a legacy identifier and needs its own decision.

package mint

import (
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
)

// The NATS user's name is the endpoint's bare name.
func TestGoldenMintedUserName(t *testing.T) {
	c := compile(t, `
default: deny
endpoints:
  a: {labels: {team: red}}
rules: []
`)
	m, err := Mint(newFakeKeys(t), c, "a", nil)
	if err != nil {
		t.Fatal(err)
	}
	uc, err := jwt.DecodeUserClaims(m.JWT)
	if err != nil {
		t.Fatal(err)
	}
	if uc.Name != "a" || m.Name != "a" {
		t.Errorf("name %q minted name %q", uc.Name, m.Name)
	}
	if !strings.Contains(string(m.Creds), "BEGIN NATS USER JWT") {
		t.Error("creds are not a user creds file")
	}
}
