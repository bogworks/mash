package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/policy"
)

// ErrServerSetRefused is wrapped by Servers when more than maxServers distinct servers answered or a server or
// cluster name is over maxNameBytes. The set it returns is partial; a caller must refuse and not act on it.
var ErrServerSetRefused = errors.New("server set refused")

// Caps on what a system-account peer can make mashctl hold or wait for. A server is not trusted to be small.
const (
	// maxServers is the most distinct server names Servers records.
	maxServers = 1024
	// maxReplies is the most replies LookupAll or Push collects.
	maxReplies = 4096
	// maxNameBytes is the longest server or cluster name taken as an identity. A longer one is refused, never cut:
	// a cut name would make two distinct servers one.
	maxNameBytes = 4096
)

var errTooManyReplies = fmt.Errorf("more than %d replies: refusing", maxReplies)

// checkClaimsReply refuses a claims reply larger than the account JWT limit; it is never truncated.
func checkClaimsReply(b []byte) error {
	if len(b) > policy.AccountJWTLimit {
		return fmt.Errorf("reply of %d bytes over the limit of %d bytes", len(b), policy.AccountJWTLimit)
	}
	return nil
}

// The subjects of a full resolver's system-account API, and the waits of the transport.
const (
	lookupSubject = "$SYS.REQ.ACCOUNT.%s.CLAIMS.LOOKUP"
	updateSubject = "$SYS.REQ.CLAIMS.UPDATE"
	pingSubject   = "$SYS.REQ.SERVER.PING"

	// pingFirst bounds the first server's answer to the ping; pingSettle ends the collection when no new server
	// answered for that long. ackWait bounds how long a known server has to acknowledge the push.
	pingFirst  = time.Second
	pingSettle = 300 * time.Millisecond
	// pingTotal is the longest the ping waits for the rest of a cluster that has not all replied yet.
	pingTotal = 3 * time.Second
	ackWait   = 2 * time.Second

	lookupTimeout = 5 * time.Second
	// pushTimeout bounds the first answer; after it, further servers have settleTimeout each to add theirs.
	pushTimeout   = 5 * time.Second
	settleTimeout = 300 * time.Millisecond
)

// NATS is a Resolver over a connection authenticated as a system account user.
type NATS struct {
	Conn *nats.Conn
	// Inventory is the operator's server count (--servers; 0 none): the ping stops waiting once that many replied.
	Inventory int
}

// Lookup asks the resolver for the account's JWT; an empty answer is "none".
func (n NATS) Lookup(ctx context.Context, account string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	msg, err := n.Conn.RequestWithContext(ctx, fmt.Sprintf(lookupSubject, account), nil)
	if err != nil {
		return "", hintNoAnswer(err)
	}
	if err := checkClaimsReply(msg.Data); err != nil {
		return "", err
	}
	return string(msg.Data), nil
}

// LookupAll asks every server's resolver for the account's JWT and collects the replies on a fresh inbox. It waits up
// to lookupTimeout for the first; then, with expect > 0, until expect replies arrived or ackWait has passed since the
// first; with expect <= 0 until settleTimeout passes without a new reply.
func (n NATS) LookupAll(ctx context.Context, account string, expect int) ([]string, error) {
	inbox := n.Conn.NewRespInbox()
	sub, err := n.Conn.SubscribeSync(inbox)
	if err != nil {
		return nil, err
	}
	defer sub.Unsubscribe()
	if err := n.Conn.PublishMsg(&nats.Msg{Subject: fmt.Sprintf(lookupSubject, account), Reply: inbox}); err != nil {
		return nil, err
	}
	if err := n.Conn.Flush(); err != nil {
		return nil, err
	}
	var out []string
	wait := lookupTimeout
	var deadline time.Time
	for ctx.Err() == nil && (expect <= 0 || len(out) < expect) {
		msg, err := sub.NextMsg(wait)
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) && len(out) > 0 {
				break
			}
			if errors.Is(err, nats.ErrTimeout) {
				return nil, hintNoAnswer(err)
			}
			return out, err
		}
		if err := checkClaimsReply(msg.Data); err != nil {
			return out, err
		}
		out = append(out, string(msg.Data))
		if len(out) >= maxReplies {
			return out, errTooManyReplies
		}
		if expect > 0 {
			if deadline.IsZero() {
				deadline = time.Now().Add(ackWait)
			}
			wait = max(time.Until(deadline), time.Millisecond)
		} else {
			wait = settleTimeout
		}
	}
	return out, ctx.Err()
}

// Servers pings the system account and collects the replies: it waits up to pingFirst for the first one and then
// until pingSettle passes without a new one. If the set is still not complete (see ServerSet.Complete) it keeps
// listening until pingTotal has passed since the ping, so a slow server is not left out of the expected set.
func (n NATS) Servers(ctx context.Context) (ServerSet, error) {
	var set ServerSet
	inbox := n.Conn.NewRespInbox()
	sub, err := n.Conn.SubscribeSync(inbox)
	if err != nil {
		return set, err
	}
	defer sub.Unsubscribe()
	if err := n.Conn.PublishMsg(&nats.Msg{Subject: pingSubject, Reply: inbox}); err != nil {
		return set, err
	}
	if err := n.Conn.Flush(); err != nil {
		return set, err
	}
	start := time.Now()
	seen := map[string]bool{}
	wait := pingFirst
	for ctx.Err() == nil {
		msg, err := sub.NextMsg(wait)
		if err != nil {
			// quiet for the settle window: done when complete, or when the total wait is used up.
			left := pingTotal - time.Since(start)
			if set.Complete(n.Inventory) || set.Gateways || set.ClusterConflict || left <= 0 || len(set.Names) == 0 {
				break
			}
			wait = min(pingSettle, left)
			continue
		}
		wait = pingSettle
		if addPingReply(&set, seen, msg.Data) {
			sort.Strings(set.Names)
			return set, fmt.Errorf("%w: more than %d servers answered, or a server or cluster name is over %d bytes", ErrServerSetRefused, maxServers, maxNameBytes)
		}
	}
	sort.Strings(set.Names)
	return set, nil
}

// Push sends the claims update and collects the answers. With a known server set it waits until every one of them
// has answered or ackWait has passed; with none it takes the first answer within pushTimeout and then every one that
// follows within settleTimeout of the last.
func (n NATS) Push(ctx context.Context, token string, expect []string) ([]ServerAck, error) {
	inbox := n.Conn.NewRespInbox()
	sub, err := n.Conn.SubscribeSync(inbox)
	if err != nil {
		return nil, err
	}
	defer sub.Unsubscribe()
	if err := n.Conn.PublishMsg(&nats.Msg{Subject: updateSubject, Reply: inbox, Data: []byte(token)}); err != nil {
		return nil, err
	}
	if err := n.Conn.Flush(); err != nil {
		return nil, err
	}
	var acks []ServerAck
	if len(expect) > 0 {
		pending := map[string]bool{}
		for _, e := range expect {
			pending[e] = true
		}
		deadline := time.Now().Add(ackWait)
		for len(pending) > 0 && ctx.Err() == nil {
			left := time.Until(deadline)
			if left <= 0 {
				break
			}
			msg, err := sub.NextMsg(left)
			if err != nil {
				break
			}
			a := parseAck(msg.Data)
			acks = append(acks, a)
			delete(pending, a.Server)
			if len(acks) >= maxReplies {
				return acks, errTooManyReplies
			}
		}
		return acks, ctx.Err()
	}
	wait := pushTimeout
	for {
		msg, err := sub.NextMsg(wait)
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) && len(acks) > 0 {
				return acks, nil
			}
			if errors.Is(err, nats.ErrTimeout) {
				return nil, errors.New("no server answered the claims update in time")
			}
			return acks, err
		}
		acks = append(acks, parseAck(msg.Data))
		if len(acks) >= maxReplies {
			return acks, errTooManyReplies
		}
		wait = settleTimeout
		if ctx.Err() != nil {
			return acks, ctx.Err()
		}
	}
}

func parseAck(b []byte) ServerAck {
	var r struct {
		Server struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"server"`
		Data *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"data"`
		Error *struct {
			Code        int    `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return ServerAck{Message: "unreadable answer"}
	}
	name := r.Server.Name
	if name == "" {
		name = r.Server.ID
	}
	if len(name) > maxNameBytes {
		name = "" // never cut: an over-long name matches no known server, so the push stays unconfirmed
	}
	switch {
	case r.Error != nil:
		return ServerAck{Server: name, Code: r.Error.Code, Message: config.Clean(r.Error.Description)}
	case r.Data != nil:
		return ServerAck{Server: name, Code: r.Data.Code, Message: config.Clean(r.Data.Message)}
	}
	return ServerAck{Server: name, Message: "empty answer"}
}

func hintNoAnswer(err error) error {
	if errors.Is(err, nats.ErrNoResponders) || errors.Is(err, nats.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: the system account answered nothing; are these the system account's credentials, and does the server run an operator-mode full resolver?", err)
	}
	return err
}

// addPingReply folds one $SYS.REQ.SERVER.PING reply into the set. seen holds the server names already counted;
// an unparsable reply, one naming no server, or a repeat is ignored. The cluster name is the first non-empty one
// seen; a reply naming a different cluster marks the set ClusterConflict, which keeps it from being complete.
//
// Names are kept raw: they are the identity that acknowledgements are matched on, and config.Clean (which cuts and
// escapes) is applied only where one is printed. A server or cluster name over maxNameBytes, or a name beyond
// maxServers distinct ones, is not recorded and the result is true: the caller must stop and refuse.
func addPingReply(set *ServerSet, seen map[string]bool, data []byte) (overflow bool) {
	var r struct {
		Server struct {
			Name    string `json:"name"`
			ID      string `json:"id"`
			Cluster string `json:"cluster"`
		} `json:"server"`
		Stats struct {
			Routes []struct {
				Name string `json:"name"`
			} `json:"routes"`
			Gateways []json.RawMessage `json:"gateways"`
		} `json:"statsz"`
	}
	if json.Unmarshal(data, &r) != nil {
		return false
	}
	name := r.Server.Name
	if name == "" {
		name = r.Server.ID
	}
	if len(name) > maxNameBytes || len(r.Server.Cluster) > maxNameBytes {
		return true
	}
	if name == "" || seen[name] {
		return false
	}
	if len(seen) >= maxServers {
		return true
	}
	seen[name] = true
	set.Names = append(set.Names, name)
	switch {
	case r.Server.Cluster == "":
	case set.Cluster == "":
		set.Cluster = r.Server.Cluster
	case set.Cluster != r.Server.Cluster:
		set.ClusterConflict = true
	}
	// One route connection per peer, but a pool holds several: count distinct peer names when they are
	// reported (an unnamed route counts for itself, which can only over-count and so can only say UNCONFIRMED).
	peers := map[string]bool{}
	unnamed := 0
	for _, rt := range r.Stats.Routes {
		if rt.Name == "" {
			unnamed++
		} else {
			peers[rt.Name] = true
		}
	}
	set.Expected = max(set.Expected, 1+len(peers)+unnamed)
	if len(r.Stats.Gateways) > 0 {
		set.Gateways = true
	}
	return false
}
