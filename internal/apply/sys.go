package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

// The subjects of a full resolver's system-account API, and the waits of the transport.
const (
	lookupSubject = "$SYS.REQ.ACCOUNT.%s.CLAIMS.LOOKUP"
	updateSubject = "$SYS.REQ.CLAIMS.UPDATE"

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

// Push sends the claims update and collects the answers: the first within pushTimeout, then every one that follows
// within settleTimeout of the last.
func (n NATS) Push(ctx context.Context, token string) ([]ServerAck, error) {
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
	switch {
	case r.Error != nil:
		return ServerAck{Server: r.Server.Name, Code: r.Error.Code, Message: r.Error.Description}
	case r.Data != nil:
		return ServerAck{Server: r.Server.Name, Code: r.Data.Code, Message: r.Data.Message}
	}
	return ServerAck{Server: r.Server.Name, Message: "empty answer"}
}

func hintNoAnswer(err error) error {
	if errors.Is(err, nats.ErrNoResponders) || errors.Is(err, nats.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: the system account answered nothing; are these the system account's credentials, and does the server run an operator-mode full resolver?", err)
	}
	return err
}
