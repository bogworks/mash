package policyapply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/apply"
)

// Caps and waits of the connection query, in the style of the account JWT collection (natscluster.go).
const (
	connzSubject = "$SYS.REQ.SERVER.PING.CONNZ"
	// maxConnzReplies is the most replies Connected reads.
	maxConnzReplies = 4096
	// maxConnzReplyBytes is the largest reply taken. The request is filtered by user, so a reply is small; a bigger
	// one is refused, never cut.
	maxConnzReplyBytes = 1 << 20
	connzFirst         = 2 * time.Second
	connzSettle        = 500 * time.Millisecond
	connzTotal         = 4 * time.Second
)

// NATSConnections is the live Connections: a connection authenticated as a system account user.
type NATSConnections struct {
	Conn *nats.Conn
}

var _ Connections = NATSConnections{}

// Connected asks every server, with a $SYS.REQ.SERVER.PING.CONNZ request filtered by the user key, whether it shows an
// open connection authorized as userPub. It returns true as soon as any server does. It returns false, with no error,
// when every server (as many as the system account shows) or all that answered before a quiet period show none. A
// reply over maxConnzReplyBytes, a server name over maxServerNameBytes, or more than maxConnzReplies replies is an
// error.
func (c NATSConnections) Connected(ctx context.Context, userPub string) (bool, error) {
	if !nkeys.IsValidPublicUserKey(userPub) {
		return false, fmt.Errorf("%q is not a user public key", userPub)
	}
	set, err := apply.NATS{Conn: c.Conn}.Servers(ctx)
	if err != nil {
		return false, fmt.Errorf("server set: %w", err)
	}
	want := max(len(set.Names), 1)
	body, err := json.Marshal(struct {
		Auth bool   `json:"auth"`
		User string `json:"user"`
	}{true, userPub})
	if err != nil {
		return false, err
	}
	inbox := c.Conn.NewRespInbox()
	sub, err := c.Conn.SubscribeSync(inbox)
	if err != nil {
		return false, err
	}
	defer func() { _ = sub.Unsubscribe() }()
	if err := c.Conn.PublishMsg(&nats.Msg{Subject: connzSubject, Reply: inbox, Data: body}); err != nil {
		return false, err
	}
	if err := c.Conn.Flush(); err != nil {
		return false, err
	}
	answered := map[string]bool{}
	start := time.Now()
	wait := connzFirst
	replies := 0
	for ctx.Err() == nil && len(answered) < want {
		left := connzTotal - time.Since(start)
		if left <= 0 {
			break
		}
		msg, err := sub.NextMsg(min(wait, left))
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) {
				break
			}
			return false, err
		}
		wait = connzSettle
		if replies++; replies > maxConnzReplies {
			return false, fmt.Errorf("more than %d replies: refusing", maxConnzReplies)
		}
		seen, name, err := foldConnz(msg.Data, userPub)
		if err != nil {
			return false, err
		}
		if seen {
			return true, nil
		}
		if name != "" {
			answered[name] = true
		}
	}
	return false, ctx.Err()
}

// foldConnz reads one CONNZ reply: whether it lists an open connection authorized as userPub, and the server's name. An
// unreadable reply, or one with an error, counts as no connection and names no server.
func foldConnz(data []byte, userPub string) (seen bool, server string, err error) {
	if len(data) > maxConnzReplyBytes {
		return false, "", fmt.Errorf("reply of %d bytes over the limit of %d bytes: refusing", len(data), maxConnzReplyBytes)
	}
	var r struct {
		Server struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"server"`
		Data *struct {
			Conns []struct {
				AuthorizedUser string     `json:"authorized_user"`
				Stop           *time.Time `json:"stop"`
			} `json:"connections"`
		} `json:"data"`
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &r) != nil || r.Error != nil {
		return false, "", nil
	}
	server = r.Server.Name
	if server == "" {
		server = r.Server.ID
	}
	if len(server) > maxServerNameBytes {
		return false, "", fmt.Errorf("a server name over %d bytes: refusing", maxServerNameBytes)
	}
	if r.Data != nil {
		for _, c := range r.Data.Conns {
			if c.AuthorizedUser == userPub && c.Stop == nil {
				return true, server, nil
			}
		}
	}
	return false, server, nil
}
