package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
)

// AccountJWTLimit is the largest account claims nats-server accepts: 1 MiB. It is the jwt library's decoder cap
// (jwt.MaxTokenSize, a constant: no server setting lowers it), which the server applies to every account JWT. A
// server's max_payload can be smaller, and then it is the limit (see ServerLimits.AccountJWT).
const AccountJWTLimit = 1 << 20

// BudgetReport is the size of what a compile would need: the account JWT with one scoped signing key per zone and
// the biggest user JWT, each measured against every server's limit.
type BudgetReport struct {
	AccountJWTBytes int
	// AccountLimit is the smallest account JWT limit of any server.
	AccountLimit int
	Zones        int
	// WorstUserJWTBytes is the biggest user JWT, and WorstConnectBytes the biggest full connect line (ConnectLineBytes).
	WorstUserJWTBytes int
	WorstConnectBytes int
	WorstUser         string
	// UserLimit is the smallest connect-line limit (max_control_line) of any server.
	UserLimit int
	// Refusals lists every credential that does not fit a server, one entry per credential and server.
	Refusals []Refusal
	OK       bool
}

// Budget measures the compile against nats-server's default limits, as one server named DefaultServer: the offline
// measure of `policy check` and `policy show`. BudgetFor measures against the servers' own limits.
func Budget(c *Compiled) (BudgetReport, error) { return BudgetFor(c, DefaultServerLimits()) }

// BudgetFor measures the two kinds of JWT a compile has to fit and reports both against every server in limits.
// (1) The account JWT: one scoped signing key per zone, each carrying the zone's permission template, encoded as a
// real account claim with throwaway keys made inside the call (nothing is kept), against each server's account
// limit. (2) Every sender's user JWT: its name, its tags, signed by its zone's key on behalf of the account, inside
// the full CONNECT line (ConnectLineBytes, with the client name the endpoint connects with), against each server's
// max_control_line. When any does not fit on any server, OK is false and the error lists each sender or account, its
// size, the limit and the server. Nothing is dropped, merged or widened to make a credential fit.
func BudgetFor(c *Compiled, limits []ServerLimits) (BudgetReport, error) {
	return budget(c, limits)
}

func budget(c *Compiled, limits []ServerLimits) (BudgetReport, error) {
	rep := BudgetReport{Zones: len(c.Zones)}
	if len(limits) == 0 {
		return rep, errors.New("policy: budget: no server limits to measure against")
	}
	for i, l := range limits {
		if i == 0 || l.AccountJWT < rep.AccountLimit {
			rep.AccountLimit = l.AccountJWT
		}
		if i == 0 || l.MaxControlLine < rep.UserLimit {
			rep.UserLimit = l.MaxControlLine
		}
	}
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
	claims.Name = "account"
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
	for _, l := range limits {
		if rep.AccountJWTBytes > l.AccountJWT {
			rep.Refusals = append(rep.Refusals, Refusal{Kind: "account", Name: claims.Name, Bytes: rep.AccountJWTBytes,
				Limit: l.AccountJWT, Setting: accountSetting(l.AccountJWT), Server: l.Server,
				Note: fmt.Sprintf("%d zones; the biggest signing key is zone %s (%d bytes of JSON): fewer zones, or fewer zone pairs granted, shrink it", rep.Zones, biggest, biggestBytes)})
		}
	}

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
		line := ConnectLineBytes(tok, ConnectLineName(e.Name))
		if line > rep.WorstConnectBytes {
			rep.WorstUserJWTBytes, rep.WorstConnectBytes, rep.WorstUser = len(tok), line, e.Name
		}
		for _, l := range limits {
			if line > l.MaxControlLine {
				rep.Refusals = append(rep.Refusals, Refusal{Kind: "sender", Name: e.Name, Zone: e.Zone, Tags: len(e.Tags), Exceptions: exceptionCount(e.Tags), Bytes: line,
					Limit: l.MaxControlLine, Setting: "max_control_line", Server: l.Server})
			}
		}
	}

	rep.OK = len(rep.Refusals) == 0
	if rep.OK {
		return rep, nil
	}
	var lines []string
	hint := false
	for _, r := range rep.Refusals {
		lines = append(lines, r.Error())
		hint = hint || r.Kind == "sender"
	}
	msg := strings.Join(lines, "\n  ")
	if hint {
		msg += "\n" + RefusalHint
	}
	return rep, errors.New(msg)
}

// accountSetting names the server setting an account JWT limit comes from: max_payload when it is under the decoder
// cap, else the cap itself.
func accountSetting(limit int) string {
	if limit < AccountJWTLimit {
		return "max_payload"
	}
	return "the jwt decoder cap"
}

// exceptionCount is the number of exceptions among a sender's tags: every tag but the placeholder deny tag every
// sender carries (admin.NoDenyTag).
func exceptionCount(tags []string) int {
	n := 0
	for _, t := range tags {
		if t != admin.NoDenyTag {
			n++
		}
	}
	return n
}
