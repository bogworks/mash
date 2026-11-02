package policy

import (
	"encoding/json"
	"fmt"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// AccountJWTLimit is the largest account claims push the NATS server accepts: 1 MiB.
const AccountJWTLimit = 1 << 20

// BudgetReport is the size of the account JWT a compile would need.
type BudgetReport struct {
	AccountJWTBytes int
	Limit           int
	Endpoints       int
	OK              bool
}

// Budget measures the account JWT that holds the compile's scoped signing keys: one per endpoint, each carrying
// that endpoint's whole permission set as its template. It builds and encodes a real account claim with throwaway
// keys generated inside the call and nothing is kept. When the claim is over AccountJWTLimit the report has OK false
// and the error names the endpoint whose signing key is the biggest part of it.
func Budget(c *Compiled) (BudgetReport, error) { return budget(c, AccountJWTLimit) }

func budget(c *Compiled, limit int) (BudgetReport, error) {
	rep := BudgetReport{Limit: limit, Endpoints: len(c.Endpoints)}
	op, err := nkeys.CreateOperator()
	if err != nil {
		return rep, fmt.Errorf("policy: budget: %w", err)
	}
	acct, err := nkeys.CreateAccount()
	if err != nil {
		return rep, fmt.Errorf("policy: budget: %w", err)
	}
	apub, err := acct.PublicKey()
	if err != nil {
		return rep, fmt.Errorf("policy: budget: %w", err)
	}
	claims := jwt.NewAccountClaims(apub)
	claims.Name = "budget"
	biggest, biggestBytes := "", -1
	for _, e := range c.Endpoints {
		kp, err := nkeys.CreateAccount()
		if err != nil {
			return rep, fmt.Errorf("policy: budget: %w", err)
		}
		pub, err := kp.PublicKey()
		if err != nil {
			return rep, fmt.Errorf("policy: budget: %w", err)
		}
		scope := jwt.NewUserScope()
		scope.Key = pub
		scope.Role = e.Name
		scope.Template.Permissions = e.Perms
		claims.SigningKeys.AddScopedSigner(scope)
		if raw, err := json.Marshal(scope); err == nil && len(raw) > biggestBytes {
			biggest, biggestBytes = e.Name, len(raw)
		}
	}
	token, err := claims.Encode(op)
	if err != nil {
		return rep, fmt.Errorf("policy: budget: %w", err)
	}
	rep.AccountJWTBytes = len(token)
	rep.OK = rep.AccountJWTBytes <= limit
	if !rep.OK {
		return rep, fmt.Errorf("policy: the account JWT would be %d bytes, over the %d byte limit for %d endpoints; the biggest signing key is %s (%d bytes of JSON): fewer allowed recipients per sender shrink it",
			rep.AccountJWTBytes, limit, rep.Endpoints, biggest, biggestBytes)
	}
	return rep, nil
}
