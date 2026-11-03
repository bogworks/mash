package policy

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// Limits a compile must fit.
const (
	// AccountJWTLimit is the largest account claims nats-server accepts: 1 MiB. It is the jwt library's decoder cap
	// (jwt.MaxTokenSize), which the server applies to every account JWT whatever max_payload says.
	AccountJWTLimit = 1 << 20
	// UserJWTLimit is the largest user JWT mash allows: a user's JWT travels in the CONNECT line, which the default
	// server caps at 4096 bytes together with the signature, name and options. Measured: a 3,802 byte JWT
	// connects and a 3,838 byte one fails (#179), so the limit leaves real headroom below the failing size.
	UserJWTLimit = 3750
)

// BudgetReport is the size of what a compile would need: the account JWT with one scoped signing key per zone and
// the biggest user JWT.
type BudgetReport struct {
	AccountJWTBytes   int
	AccountLimit      int
	Zones             int
	WorstUserJWTBytes int
	WorstUser         string
	UserLimit         int
	OK                bool
}

// Budget measures the two JWTs a compile has to fit and reports both. (1) The account JWT: one scoped signing key per
// zone, each carrying the zone's permission template, encoded as a real account claim with throwaway keys made
// inside the call (nothing is kept), against AccountJWTLimit. (2) Every user JWT: its name, its tags, signed by its
// zone's key on behalf of the account, against UserJWTLimit; the report names the largest. When either is over, OK
// is false and the error names the biggest contributor: the zone whose key is the biggest part of the account JWT,
// or the endpoint with the most tags.
func Budget(c *Compiled) (BudgetReport, error) { return budget(c, AccountJWTLimit, UserJWTLimit) }

func budget(c *Compiled, accountLimit, userLimit int) (BudgetReport, error) {
	rep := BudgetReport{AccountLimit: accountLimit, UserLimit: userLimit, Zones: len(c.Zones)}
	fail := func(err error) (BudgetReport, error) { return rep, fmt.Errorf("policy: budget: %w", err) }
	op, err := nkeys.CreateOperator()
	if err != nil {
		return fail(err)
	}
	acct, err := nkeys.CreateAccount()
	if err != nil {
		return fail(err)
	}
	apub, err := acct.PublicKey()
	if err != nil {
		return fail(err)
	}
	claims := jwt.NewAccountClaims(apub)
	claims.Name = "budget"
	signers := make(map[string]nkeys.KeyPair, len(c.Zones))
	biggest, biggestBytes := "", -1
	for _, z := range c.Zones {
		kp, err := nkeys.CreateAccount()
		if err != nil {
			return fail(err)
		}
		pub, err := kp.PublicKey()
		if err != nil {
			return fail(err)
		}
		signers[z.Name] = kp
		scope := jwt.NewUserScope()
		scope.Key = pub
		scope.Role = z.Name
		scope.Template.Permissions = z.Perms
		claims.SigningKeys.AddScopedSigner(scope)
		if raw, err := json.Marshal(scope); err == nil && len(raw) > biggestBytes {
			biggest, biggestBytes = z.Name, len(raw)
		}
	}
	token, err := claims.Encode(op)
	if err != nil {
		return fail(err)
	}
	rep.AccountJWTBytes = len(token)

	worstTags := 0
	for _, e := range c.Endpoints {
		kp, ok := signers[e.Zone]
		if !ok {
			return fail(fmt.Errorf("endpoint %s is in zone %q, which has no key", e.Name, e.Zone))
		}
		ukp, err := nkeys.CreateUser()
		if err != nil {
			return fail(err)
		}
		upub, err := ukp.PublicKey()
		if err != nil {
			return fail(err)
		}
		uc := jwt.NewUserClaims(upub)
		uc.Name = e.Name
		uc.IssuerAccount = apub
		uc.UserPermissionLimits = jwt.UserPermissionLimits{} // a scoped key's users carry no permissions or limits
		uc.Tags.Add(e.Tags...)
		tok, err := uc.Encode(kp)
		if err != nil {
			return fail(err)
		}
		if len(tok) > rep.WorstUserJWTBytes {
			rep.WorstUserJWTBytes, rep.WorstUser, worstTags = len(tok), e.Name, len(e.Tags)
		}
	}

	rep.OK = rep.AccountJWTBytes <= accountLimit && rep.WorstUserJWTBytes <= userLimit
	var errs []error
	if rep.AccountJWTBytes > accountLimit {
		errs = append(errs, fmt.Errorf("the account JWT would be %d bytes, over the %d byte limit, for %d zones; the biggest signing key is zone %s (%d bytes of JSON): fewer zones, or fewer zone pairs granted, shrink it",
			rep.AccountJWTBytes, accountLimit, rep.Zones, biggest, biggestBytes))
	}
	if rep.WorstUserJWTBytes > userLimit {
		errs = append(errs, fmt.Errorf("the user JWT of %s would be %d bytes with %d tags, over the %d byte limit of the connect line: fewer exceptions for that endpoint (rules on labels other than %q compile to tags)",
			rep.WorstUser, rep.WorstUserJWTBytes, worstTags, userLimit, c.SubjectLabel))
	}
	if len(errs) > 0 {
		return rep, fmt.Errorf("policy: %w", errors.Join(errs...))
	}
	return rep, nil
}
