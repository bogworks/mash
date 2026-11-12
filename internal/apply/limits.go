package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/policy"
)

// varzSubject is the system account request every server answers with its running configuration (nats-server
// events.go: the VARZ monitoring request). Its data holds max_control_line and max_payload.
const varzSubject = "$SYS.REQ.SERVER.PING.VARZ"

// ServerReport is what one server said about its limits in answer to the VARZ request.
type ServerReport struct {
	// Server is the name the server gave, or its id when it has no name.
	Server string
	// MaxControlLine is max_control_line (0 when the reply did not carry it) and MaxPayload is max_payload.
	MaxControlLine int
	MaxPayload     int
}

// LimitsResolver is a Resolver that can also ask every server for its limits. NATS does; a Resolver that does not
// leaves every server's limit unknown, which LearnLimits refuses unless the operator gives one.
type LimitsResolver interface {
	// ServerLimits asks every server for its limits. expect is how many servers are known (0: unknown): a transport
	// may stop once that many answered.
	ServerLimits(ctx context.Context, expect int) ([]ServerReport, error)
}

// ServerLimits sends the VARZ request to the system account and collects one report per server: it waits up to
// pingFirst for the first, and then until pingSettle passes without a new one (or expect replies arrived, or
// pingTotal has passed since the request). A reply that is not a VARZ answer is ignored.
func (n NATS) ServerLimits(ctx context.Context, expect int) ([]ServerReport, error) {
	inbox := n.Conn.NewRespInbox()
	sub, err := n.Conn.SubscribeSync(inbox)
	if err != nil {
		return nil, err
	}
	defer sub.Unsubscribe()
	if err := n.Conn.PublishMsg(&nats.Msg{Subject: varzSubject, Reply: inbox}); err != nil {
		return nil, err
	}
	if err := n.Conn.Flush(); err != nil {
		return nil, err
	}
	var out []ServerReport
	seen := map[string]bool{}
	start := time.Now()
	wait := pingFirst
	for ctx.Err() == nil && (expect <= 0 || len(out) < expect) {
		msg, err := sub.NextMsg(wait)
		if err != nil {
			left := pingTotal - time.Now().Sub(start)
			if len(out) == 0 || left <= 0 || expect <= 0 {
				break
			}
			wait = min(pingSettle, left)
			continue
		}
		wait = pingSettle
		r, ok := parseVarz(msg.Data)
		if !ok || seen[r.Server] {
			continue
		}
		seen[r.Server] = true
		out = append(out, r)
		if len(out) >= maxServers {
			return out, fmt.Errorf("%w: more than %d servers answered", ErrServerSetRefused, maxServers)
		}
	}
	return out, ctx.Err()
}

// parseVarz reads one reply to the VARZ request: {"server":{"name","id"},"data":{"max_control_line","max_payload"}}.
func parseVarz(b []byte) (ServerReport, bool) {
	var r struct {
		Server struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"server"`
		Data *struct {
			MaxControlLine int `json:"max_control_line"`
			MaxPayload     int `json:"max_payload"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &r) != nil || r.Data == nil {
		return ServerReport{}, false
	}
	name := r.Server.Name
	if name == "" {
		name = r.Server.ID
	}
	if name == "" || len(name) > maxNameBytes {
		return ServerReport{}, false
	}
	return ServerReport{Server: name, MaxControlLine: r.Data.MaxControlLine, MaxPayload: r.Data.MaxPayload}, true
}

// ErrLimitUnknown is wrapped by LearnLimits when a server's limit cannot be learned and the operator gave none.
var ErrLimitUnknown = errors.New("server limit unknown")

// LearnLimits is the limits of every server in names (the servers the ping found; reports may name more), from the
// reports the servers gave. A server is never assumed small or large: one that gave no report gets override (the
// operator's --max-control-line) when that is above 0, and otherwise the result is an error wrapping ErrLimitUnknown
// that names it. An override also caps a server that did report: the smaller of the two is used, so an operator
// who says a server is smaller than it says it is, is believed. The account JWT limit of a server is the jwt decoder
// cap, lowered to its max_payload when that is smaller (the claims update is one message). The result is sorted by
// server name.
func LearnLimits(names []string, reports []ServerReport, override int) ([]policy.ServerLimits, error) {
	byName := map[string]ServerReport{}
	for _, r := range reports {
		if r.MaxControlLine > 0 {
			byName[r.Server] = r
		}
	}
	all := map[string]bool{}
	for _, n := range names {
		all[n] = true
	}
	for n := range byName {
		all[n] = true
	}
	var unknown []string
	var out []policy.ServerLimits
	for n := range all {
		l := policy.ServerLimits{Server: n, AccountJWT: policy.AccountJWTLimit}
		if r, ok := byName[n]; ok {
			l.MaxControlLine = r.MaxControlLine
			if r.MaxPayload > 0 {
				l.AccountJWT = min(l.AccountJWT, r.MaxPayload)
			}
			if override > 0 {
				l.MaxControlLine = min(l.MaxControlLine, override)
			}
		} else if override > 0 {
			l.MaxControlLine = override
		} else {
			unknown = append(unknown, config.Clean(n))
			continue
		}
		out = append(out, l)
	}
	if len(all) == 0 {
		if override <= 0 {
			return nil, fmt.Errorf("%w: no server answered the system account request for its limits; pass --max-control-line N (MASH_MAX_CONTROL_LINE) with the max_control_line your servers run", ErrLimitUnknown)
		}
		out = append(out, policy.ServerLimits{Server: "--max-control-line", MaxControlLine: override, AccountJWT: policy.AccountJWTLimit})
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("%w: the max_control_line of server %s is not known (it did not answer the system account request for its limits); mash does not assume it, so pass --max-control-line N (MASH_MAX_CONTROL_LINE) with the limit that server runs",
			ErrLimitUnknown, quoteList(unknown))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Server < out[j].Server })
	return out, nil
}

func quoteList(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(q, ", ")
}

// Limits asks the servers named in set (the ping's answer) for their limits through r and combines the answers with
// override as LearnLimits says. A Resolver that is not a LimitsResolver reports nothing.
func Limits(ctx context.Context, r Resolver, set ServerSet, override int) ([]policy.ServerLimits, error) {
	var reports []ServerReport
	if lr, ok := r.(LimitsResolver); ok {
		var err error
		reports, err = lr.ServerLimits(ctx, len(set.Names))
		if err != nil && errors.Is(err, ErrServerSetRefused) {
			return nil, err
		}
	}
	return LearnLimits(set.Names, reports, override)
}
