package listset

import (
	"fmt"
	"sort"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// Size limits (REQ-007, REQ-008). They mirror internal/policy/budget.go, which this package does not import.
const (
	// DefaultMaxConnectLine is nats-server's default max_control_line, 4096 bytes. A user's JWT travels in the CONNECT
	// line together with the signature, name and options, so the whole line must fit.
	DefaultMaxConnectLine = 4096
	// ConnectLineOverhead is what the CONNECT line holds besides the user JWT: the signature, nkey, client name and
	// options. It is the headroom budget.go leaves between the server default and its UserJWTLimit (4096 - 3750 = 346
	// bytes), measured there against a 3,802 byte JWT that connects and a 3,838 byte one that fails (#179). A user
	// fits a server when its JWT size plus this overhead is at most the server's MaxConnectLine.
	ConnectLineOverhead = 346
	// DefaultMaxAccountJWT is the largest account claims nats-server accepts, 1 MiB: the jwt library's decoder cap
	// (jwt.MaxTokenSize), which the server applies to every account JWT whatever max_payload says. It is
	// budget.go's AccountJWTLimit.
	DefaultMaxAccountJWT = 1 << 20
)

// ServerLimits are the size limits of one NATS server. A zero limit means the default (DefaultMaxConnectLine,
// DefaultMaxAccountJWT).
type ServerLimits struct {
	Server         string
	MaxConnectLine int
	MaxAccountJWT  int
}

func (s ServerLimits) connectLine() int {
	if s.MaxConnectLine <= 0 {
		return DefaultMaxConnectLine
	}
	return s.MaxConnectLine
}

func (s ServerLimits) accountJWT() int {
	if s.MaxAccountJWT <= 0 {
		return DefaultMaxAccountJWT
	}
	return s.MaxAccountJWT
}

// SizeCheck measures what Compile's output needs on the wire and returns one Problem for each sender, and for the
// account, that does not fit a server, naming the size, the limit and the server (REQ-007, AT-006). With no servers
// it checks one server with the defaults, named "default". It never drops, merges or widens an entry to make
// anything fit, and the result is sorted by Entry.
//
// For each user it encodes a user JWT structurally identical to the one that would be issued (the name, the tags, the
// issuer account, no permissions or limits) signed by a scoped key, adds ConnectLineOverhead, and compares the sum
// with the server's MaxConnectLine. It also encodes an account JWT carrying every owner-group template as a scoped
// signing key and compares it with MaxAccountJWT.
//
// The measure uses throwaway nkeys generated inside the call and discarded: it never touches a real key, and a key's
// encoded length does not depend on its value. It is an estimate of the signed JWT, not a measure of it: the issue
// path must measure the JWT it really signed, and refuse it before sending, with the same limits.
func SizeCheck(c *Compiled, servers []ServerLimits) []Problem {
	if len(servers) == 0 {
		servers = []ServerLimits{{Server: "default"}}
	}
	userSizes, accountSize, err := measure(c)
	if err != nil {
		return []Problem{{"size measure", err.Error()}}
	}
	var ps []Problem
	for _, s := range servers {
		for _, u := range c.Users {
			line := userSizes[u.Sender] + ConnectLineOverhead
			if limit := s.connectLine(); line > limit {
				ps = append(ps, Problem{
					fmt.Sprintf("sender %s, server %s", u.Sender, quote(s.Server)),
					fmt.Sprintf("its connect line would be %d bytes (user JWT %d bytes with %d tags, plus %d bytes of connect options), over the %d byte limit of server %s: fewer exceptions for that sender, or a shared verb instead of a long list",
						line, userSizes[u.Sender], len(u.Tags), ConnectLineOverhead, limit, quote(s.Server)),
				})
			}
		}
		if limit := s.accountJWT(); accountSize > limit {
			ps = append(ps, Problem{
				fmt.Sprintf("account %s, server %s", quote(c.Range), quote(s.Server)),
				fmt.Sprintf("the account JWT with %d scoped signing keys would be %d bytes, over the %d byte limit of server %s",
					len(c.Templates), accountSize, limit, quote(s.Server)),
			})
		}
	}
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].Entry < ps[j].Entry })
	return ps
}

// measure returns the encoded size of each user's JWT, by sender, and of the account JWT.
func measure(c *Compiled) (map[string]int, int, error) {
	op, err := nkeys.CreateOperator()
	if err != nil {
		return nil, 0, err
	}
	acct, err := nkeys.CreateAccount()
	if err != nil {
		return nil, 0, err
	}
	apub, err := acct.PublicKey()
	if err != nil {
		return nil, 0, err
	}
	claims := jwt.NewAccountClaims(apub)
	claims.Name = "size"
	signers := map[string]nkeys.KeyPair{}
	for _, t := range c.Templates {
		kp, err := nkeys.CreateAccount()
		if err != nil {
			return nil, 0, err
		}
		pub, err := kp.PublicKey()
		if err != nil {
			return nil, 0, err
		}
		signers[t.Group] = kp
		scope := jwt.NewUserScope()
		scope.Key = pub
		scope.Role = t.Group
		scope.Template.Permissions = t.Permissions
		claims.SigningKeys.AddScopedSigner(scope)
	}
	tok, err := claims.Encode(op)
	if err != nil {
		return nil, 0, err
	}
	accountSize := len(tok)

	ukp, err := nkeys.CreateUser()
	if err != nil {
		return nil, 0, err
	}
	upub, err := ukp.PublicKey()
	if err != nil {
		return nil, 0, err
	}
	sizes := map[string]int{}
	for _, u := range c.Users {
		a, reason := parseAddress(u.Sender)
		if reason != "" {
			return nil, 0, fmt.Errorf("sender %q: %s", u.Sender, reason)
		}
		kp, ok := signers[a.group()]
		if !ok {
			return nil, 0, fmt.Errorf("sender %q is in owner group %s, which has no template", u.Sender, a.group())
		}
		uc := jwt.NewUserClaims(upub)
		uc.Name = u.Name
		uc.IssuerAccount = apub
		uc.UserPermissionLimits = jwt.UserPermissionLimits{}
		uc.Tags.Add(u.Tags...)
		tok, err := uc.Encode(kp)
		if err != nil {
			return nil, 0, err
		}
		sizes[u.Sender] = len(tok)
	}
	return sizes, accountSize, nil
}
