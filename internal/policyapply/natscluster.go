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
	"github.com/bogworks/mash/internal/policy"
)

// Caps and waits of the account JWT collection, in the style of internal/apply's system-account transport.
const (
	accountzSubject = "$SYS.REQ.SERVER.PING.ACCOUNTZ"
	// maxAccountzReplies is the most replies AccountJWTs collects.
	maxAccountzReplies = 4096
	// maxServerNameBytes is the longest server name taken as an identity: a longer one is refused, never cut, because
	// a cut name could make two servers one.
	maxServerNameBytes = 4096
	// accountzFirst bounds the first server's answer; accountzSettle ends the collection when no further server
	// answered for that long (it is skipped once every expected server has answered); accountzTotal bounds the whole
	// collection.
	accountzFirst  = 2 * time.Second
	accountzSettle = 500 * time.Millisecond
	accountzTotal  = 4 * time.Second
)

// NATSCluster is the live Cluster: a connection authenticated as a system account user. Servers and Push are
// internal/apply's transport; AccountJWTs asks each server for the account JWT it holds.
type NATSCluster struct {
	Conn *nats.Conn
	// Inventory is the operator's server count (0 when none was given), as for apply.NATS.
	Inventory int
}

var _ Cluster = NATSCluster{}

func (c NATSCluster) transport() apply.NATS { return apply.NATS{Conn: c.Conn, Inventory: c.Inventory} }

// Servers pings the system account (apply.NATS.Servers).
func (c NATSCluster) Servers(ctx context.Context) (apply.ServerSet, error) {
	return c.transport().Servers(ctx)
}

// Push sends the signed account JWT to every server (apply.NATS.Push).
func (c NATSCluster) Push(ctx context.Context, accountJWT string, expect []string) ([]apply.ServerAck, error) {
	return c.transport().Push(ctx, accountJWT, expect)
}

// AccountJWTs asks every server for the account JWT it holds, with a $SYS.REQ.SERVER.PING.ACCOUNTZ request, and
// returns it per server name. It stops when as many servers answered as Servers reports, or after a quiet period. A
// server that answered with an error (it has not loaded the account) maps to "". A server that did not answer is
// absent from the map, so the gate reads it as not proven. A reply over the account JWT limit, a server name over
// maxServerNameBytes, or more than maxAccountzReplies replies is refused with an error and nothing is returned: no
// reply is ever cut.
func (c NATSCluster) AccountJWTs(ctx context.Context, account string) (map[string]string, error) {
	if !nkeys.IsValidPublicAccountKey(account) {
		return nil, fmt.Errorf("%q is not an account public key", account)
	}
	set, err := c.Servers(ctx)
	if err != nil {
		return nil, fmt.Errorf("server set: %w", err)
	}
	want := len(set.Names)
	body, err := json.Marshal(struct {
		Account string `json:"account"`
	}{account})
	if err != nil {
		return nil, err
	}
	inbox := c.Conn.NewRespInbox()
	sub, err := c.Conn.SubscribeSync(inbox)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sub.Unsubscribe() }()
	if err := c.Conn.PublishMsg(&nats.Msg{Subject: accountzSubject, Reply: inbox, Data: body}); err != nil {
		return nil, err
	}
	if err := c.Conn.Flush(); err != nil {
		return nil, err
	}
	out := map[string]string{}
	start := time.Now()
	wait := accountzFirst
	replies := 0
	for ctx.Err() == nil && len(out) < max(want, 1) {
		left := accountzTotal - time.Since(start)
		if left <= 0 {
			break
		}
		msg, err := sub.NextMsg(min(wait, left))
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) {
				break
			}
			return nil, err
		}
		wait = accountzSettle
		if replies++; replies > maxAccountzReplies {
			return nil, fmt.Errorf("more than %d replies: refusing", maxAccountzReplies)
		}
		if err := foldAccountz(out, msg.Data); err != nil {
			return nil, err
		}
	}
	return out, ctx.Err()
}

// maxAccountzReply bounds a raw ACCOUNTZ reply. The reply carries the account JWT plus its decoded claims and server
// fields, so it is larger than the JWT; the JWT itself is held to policy.AccountJWTLimit separately.
const maxAccountzReply = 4 * policy.AccountJWTLimit

// foldAccountz adds one ACCOUNTZ reply to out. An unreadable reply, or one naming no server, is ignored (the server
// then counts as not answered); a repeat of a server already seen is ignored.
func foldAccountz(out map[string]string, data []byte) error {
	if len(data) > maxAccountzReply {
		return fmt.Errorf("reply of %d bytes over the limit of %d bytes: refusing", len(data), maxAccountzReply)
	}
	var r struct {
		Server struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"server"`
		Data *struct {
			Account *struct {
				JWT string `json:"jwt"`
			} `json:"account_detail"`
		} `json:"data"`
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &r) != nil {
		return nil
	}
	name := r.Server.Name
	if name == "" {
		name = r.Server.ID
	}
	if len(name) > maxServerNameBytes {
		return fmt.Errorf("a server name over %d bytes: refusing", maxServerNameBytes)
	}
	if name == "" {
		return nil
	}
	if _, seen := out[name]; seen {
		return nil
	}
	tok := ""
	if r.Error == nil && r.Data != nil && r.Data.Account != nil {
		tok = r.Data.Account.JWT
	}
	if len(tok) > policy.AccountJWTLimit {
		return fmt.Errorf("account JWT of %d bytes from %s over the limit of %d bytes: refusing", len(tok), name, policy.AccountJWTLimit)
	}
	out[name] = tok
	return nil
}
