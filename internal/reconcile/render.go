package reconcile

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bogworks/mash/internal/config"
)

// connText names one connection so a live connection stands out: the user's name, the client's name, the host and the server.
// Every word comes from the network or a record, so it is cleaned.
func connText(c Conn) string {
	parts := []string{}
	if c.Name != "" {
		parts = append(parts, fmt.Sprintf("user %q", config.Clean(c.Name)))
	}
	if c.Client != "" {
		parts = append(parts, fmt.Sprintf("client %q", config.Clean(c.Client)))
	}
	if c.Host != "" {
		parts = append(parts, "host "+config.Clean(c.Host))
	}
	parts = append(parts, "server "+config.Clean(c.Server), fmt.Sprintf("cid %d", c.CID))
	return strings.Join(parts, ", ")
}

// OneWayLine ends every text dry run that lists a revoke.
const OneWayLine = "a revoked credential cannot be reissued on an adopted account in this release: --yes is one-way"

// Text is the plan as the dry run prints it.
func (p Plan) Text() string {
	var b strings.Builder
	switch p.Source {
	case SourceListSetFile:
		fmt.Fprintf(&b, "account %s: preview against a list set from a file (not applied; nothing here can be executed)\n", p.Account)
	default:
		fmt.Fprintf(&b, "account %s: list set generation %d in force\n", p.Account, p.Generation)
	}
	for _, sk := range p.SigningKeys {
		if sk.State == StateStillPresent {
			fmt.Fprintf(&b, "signing key %s: still present\n", sk.Key)
		} else {
			fmt.Fprintf(&b, "signing key %s: removed by the first apply\n", sk.Key)
		}
		for _, u := range sk.Signed {
			fmt.Fprintf(&b, "  signed connected user %s (%s)\n", u.Key, connText(u.Conn))
		}
	}
	for _, r := range p.Revocations {
		line := fmt.Sprintf("revoke %s", r.Key)
		if r.Address != "" {
			line += " (sender " + config.Clean(r.Address) + ")"
		}
		line += ": " + r.Reason + " [at " + r.At + "]"
		if len(r.Connections) > 0 {
			cs := make([]string, 0, len(r.Connections))
			for _, c := range r.Connections {
				cs = append(cs, connText(c))
			}
			line += " connected: " + strings.Join(cs, "; ")
		}
		fmt.Fprintln(&b, line)
	}
	for _, f := range p.Findings {
		if f.Key == "" {
			fmt.Fprintf(&b, "finding: %s\n", f.Detail)
		} else {
			fmt.Fprintf(&b, "finding %s: %s\n", f.Key, f.Detail)
		}
	}
	for _, ip := range p.IssuerProblems {
		fmt.Fprintf(&b, "issuer check failed: %s user %s is signed by %s, which does not stay: reissue it under the account key first\n", ip.Role, ip.User, ip.Issuer)
	}
	if len(p.Revocations) > 0 {
		fmt.Fprintln(&b, OneWayLine)
		if p.PlanDigest != "" && len(p.IssuerProblems) == 0 {
			fmt.Fprintf(&b, "run again with --yes --plan %s to revoke\n", p.PlanDigest)
		}
	}
	if p.PlanDigest != "" {
		fmt.Fprintf(&b, "plan: %s\n", p.PlanDigest)
	}
	fmt.Fprintln(&b, p.Status)
	return b.String()
}

// JSON is the plan as the stable JSON the dry run prints with --json.
func (p Plan) JSON() ([]byte, error) {
	out := struct {
		Plan
		OneWay bool `json:"one_way,omitempty"`
	}{p, len(p.Revocations) > 0}
	return json.MarshalIndent(out, "", "  ")
}
