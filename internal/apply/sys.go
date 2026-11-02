package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/nats-io/nats.go"
)

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
type NATS struct{ Conn *nats.Conn }

// Lookup asks the resolver for the account's JWT; an empty answer is "none".
func (n NATS) Lookup(ctx context.Context, account string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	msg, err := n.Conn.RequestWithContext(ctx, fmt.Sprintf(lookupSubject, account), nil)
	if err != nil {
		return "", hintNoAnswer(err)
	}
	return string(msg.Data), nil
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
			if set.Complete() || set.Gateways || left <= 0 || len(set.Names) == 0 {
				break
			}
			wait = min(pingSettle, left)
			continue
		}
		wait = pingSettle
		var r struct {
			Server struct {
				Name string `json:"name"`
				ID   string `json:"id"`
			} `json:"server"`
			Stats struct {
				Routes []struct {
					Name string `json:"name"`
				} `json:"routes"`
				Gateways []json.RawMessage `json:"gateways"`
			} `json:"statsz"`
		}
		if json.Unmarshal(msg.Data, &r) != nil {
			continue
		}
		name := r.Server.Name
		if name == "" {
			name = r.Server.ID
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		set.Names = append(set.Names, name)
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
	switch {
	case r.Error != nil:
		return ServerAck{Server: name, Code: r.Error.Code, Message: r.Error.Description}
	case r.Data != nil:
		return ServerAck{Server: name, Code: r.Data.Code, Message: r.Data.Message}
	}
	return ServerAck{Server: name, Message: "empty answer"}
}

func hintNoAnswer(err error) error {
	if errors.Is(err, nats.ErrNoResponders) || errors.Is(err, nats.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: the system account answered nothing; are these the system account's credentials, and does the server run an operator-mode full resolver?", err)
	}
	return err
}
