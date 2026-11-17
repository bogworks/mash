// Characterisation tests (plan 2b, Phase 0): these pin today's identifiers; a change here is a change to an identifier and needs its own decision.

package mint

import (
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
)

// The NATS user's name is the endpoint's dotted address.
func TestGoldenMintedUserName(t *testing.T) {
	m, err := Mint(newFakeKeys(t), testAddr, nil)
	if err != nil {
		t.Fatal(err)
	}
	uc, err := jwt.DecodeUserClaims(m.JWT)
	if err != nil {
		t.Fatal(err)
	}
	if uc.Name != "o.g.a" || m.Name != "o.g.a" {
		t.Errorf("name %q minted name %q", uc.Name, m.Name)
	}
	if !strings.Contains(string(m.Creds), "BEGIN NATS USER JWT") {
		t.Error("creds are not a user creds file")
	}
}
