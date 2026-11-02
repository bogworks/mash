package apply

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/ctlkeys"
)

// Trust is the set of public keys mashctl accepts as the issuer of the account JWT it fetches: its own operator key
// and, when an operator JWT is known, the operator's identity key and declared signing keys.
type Trust struct {
	issuers map[string]bool
}

// NewTrust builds the accepted issuers from operatorPub (the operator key mashctl holds) and operatorJWT (the operator
// JWT, "" when none is known). The operator JWT must carry a valid signature by its own subject or one of its signing
// keys, and its subject and signing keys must include operatorPub.
func NewTrust(operatorPub, operatorJWT string) (Trust, error) {
	t := Trust{issuers: map[string]bool{operatorPub: true}}
	if strings.TrimSpace(operatorJWT) == "" {
		return t, nil
	}
	oc, err := jwt.DecodeOperatorClaims(strings.TrimSpace(operatorJWT)) // verifies the signature against its issuer
	if err != nil {
		return Trust{}, fmt.Errorf("the operator JWT does not decode with a valid signature: %w", err)
	}
	declared := map[string]bool{oc.Subject: true}
	for _, k := range oc.SigningKeys {
		declared[k] = true
	}
	if oc.Issuer != oc.Subject && !declared[oc.Issuer] {
		return Trust{}, fmt.Errorf("the operator JWT is signed by %s, which is neither its own subject nor one of its signing keys", oc.Issuer)
	}
	if !declared[operatorPub] {
		return Trust{}, fmt.Errorf("the operator JWT (operator %s) does not list the operator key mashctl holds (%s) as its identity or signing key: the operator key and operator JWT do not belong together", oc.Subject, operatorPub)
	}
	for k := range declared {
		t.issuers[k] = true
	}
	return t, nil
}

// LoadTrust is NewTrust for the operator key and the operator.jwt of the key store.
func LoadTrust(ks *ctlkeys.Authority) (Trust, error) {
	kp, pub, err := ks.Operator()
	if err != nil {
		return Trust{}, err
	}
	kp.Wipe()
	tok, err := ks.OperatorJWT()
	if err != nil {
		return Trust{}, err
	}
	return NewTrust(pub, tok)
}

// Issuers lists the accepted issuer public keys, sorted.
func (t Trust) Issuers() []string {
	out := make([]string, 0, len(t.issuers))
	for k := range t.issuers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// VerifyAccountJWT decodes token as an account JWT and requires: a valid signature by its stated issuer, the subject
// accountPub, and an issuer that trust accepts. Anything else is an error that names public keys only.
func VerifyAccountJWT(token string, accountPub string, trust Trust) (*jwt.AccountClaims, error) {
	if len(trust.issuers) == 0 {
		return nil, errors.New("no accepted issuer is configured for the account JWT")
	}
	claims, err := jwt.DecodeAccountClaims(token) // checks the signature against claims.Issuer
	if err != nil {
		return nil, fmt.Errorf("the account JWT does not decode with a valid signature: %w", err)
	}
	if claims.Subject != accountPub {
		return nil, fmt.Errorf("the account JWT is for account %s, not %s", claims.Subject, accountPub)
	}
	if !trust.issuers[claims.Issuer] {
		return nil, fmt.Errorf("the account JWT was issued by %s, which is not an accepted issuer (accepted: %s): mashctl signs the account JWT with the operator key, so it requires the JWT to come from that key or from a signing key the operator JWT declares (operator.jwt in the key directory names the operator's signing keys)", claims.Issuer, strings.Join(trust.Issuers(), ", "))
	}
	return claims, nil
}
