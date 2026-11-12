package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/config"
)

// The system account's CONNZ requests: the ping reaches every server, the direct subject one server by its id.
const (
	connzPingSubject   = "$SYS.REQ.SERVER.PING.CONNZ"
	connzDirectSubject = "$SYS.REQ.SERVER.%s.CONNZ"

	// DefaultConnzPage is how many connections one CONNZ reply carries (nats-server's own default).
	DefaultConnzPage = 1024

	// connzTotal bounds the wait for the servers a ping should reach; connzSettle ends the wait when no set of
	// expected servers is known; connzPageTimeout bounds one follow-up page request.
	connzTotal       = 3 * time.Second
	connzSettle      = 500 * time.Millisecond
	connzPageTimeout = 5 * time.Second

	// maxConnzConns is the most connections Connz collects from one server. A server is not trusted to be small.
	maxConnzConns = 1 << 20
)

// ConnzConn is one connection of one server as CONNZ reports it with auth on. Key is the connected user's public key
// (CONNZ's authorized_user: for a JWT user the JWT's subject). The user's JWT is never read.
type ConnzConn struct {
	Key       string   `json:"public_key"`
	Account   string   `json:"account"`
	Name      string   `json:"name,omitempty"`       // the client's connection name, as it sent it
	IssuerKey string   `json:"issuer_key,omitempty"` // the key that signed the user's JWT
	Tags      []string `json:"tags,omitempty"`
	CID       uint64   `json:"cid"`
}

// ConnzServer is what one server said: its name and id and every connection it reported for the account.
type ConnzServer struct {
	Name  string
	ID    string
	Conns []ConnzConn
	// Total is the server's own count of matching connections; len(Conns) equals it when every page was read.
	Total int
}

type connzReply struct {
	Server struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	} `json:"server"`
	Data *struct {
		Total  int `json:"total"`
		Offset int `json:"offset"`
		Conns  []struct {
			User      string   `json:"authorized_user"`
			Account   string   `json:"account"`
			Name      string   `json:"name"`
			IssuerKey string   `json:"issuer_key"`
			Tags      []string `json:"tags"`
			CID       uint64   `json:"cid"`
		} `json:"connections"`
	} `json:"data"`
	Error *struct {
		Code        int    `json:"code"`
		Description string `json:"description"`
	} `json:"error"`
}

func connzOptions(account string, offset, limit int) []byte {
	b, _ := json.Marshal(map[string]any{"auth": true, "acc": account, "offset": offset, "limit": limit})
	return b
}

func (r connzReply) name() string {
	if r.Server.Name != "" {
		return r.Server.Name
	}
	return r.Server.ID
}

// Connz asks every server for its open connections of account and returns one entry per server that answered, sorted
// by name. It waits for the servers named in expect (the ping's set) and stops when all have answered or connzTotal has
// passed; a server that never answers is simply absent, and the caller compares the names. A server that answers with an
// error, or whose answer cannot be read, is an error (the check must not pass over a server it could not read). Each
// server's further pages (a reply carries at most page connections, DefaultConnzPage when page <= 0) are asked for on
// that server's own subject, so a server with more connections than one page is read whole. The connections' JWTs, which
// CONNZ returns with auth on, are not kept.
func (n NATS) Connz(ctx context.Context, account string, expect []string, page int) ([]ConnzServer, error) {
	if page <= 0 {
		page = DefaultConnzPage
	}
	inbox := n.Conn.NewRespInbox()
	sub, err := n.Conn.SubscribeSync(inbox)
	if err != nil {
		return nil, err
	}
	defer sub.Unsubscribe()
	if err := n.Conn.PublishMsg(&nats.Msg{Subject: connzPingSubject, Reply: inbox, Data: connzOptions(account, 0, page)}); err != nil {
		return nil, err
	}
	if err := n.Conn.Flush(); err != nil {
		return nil, err
	}
	var out []ConnzServer
	seen := map[string]bool{}
	start := time.Now()
	wait := connzTotal
	for ctx.Err() == nil {
		msg, err := sub.NextMsg(wait)
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) {
				break
			}
			return out, err
		}
		var r connzReply
		if json.Unmarshal(msg.Data, &r) != nil {
			return out, errors.New("a server's CONNZ answer could not be read")
		}
		name := r.name()
		if name == "" || len(name) > maxNameBytes || (r.Server.ID != "" && len(r.Server.ID) > maxNameBytes) {
			return out, errors.New("a server's CONNZ answer names no usable server")
		}
		if r.Error != nil {
			return out, fmt.Errorf("server %s refused CONNZ: %s", config.Clean(name), config.Clean(r.Error.Description))
		}
		if r.Data == nil {
			return out, fmt.Errorf("server %s answered CONNZ with no data", config.Clean(name))
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		if len(seen) > maxServers {
			return out, fmt.Errorf("%w: more than %d servers answered CONNZ", ErrServerSetRefused, maxServers)
		}
		cs := ConnzServer{Name: name, ID: r.Server.ID, Total: r.Data.Total}
		cs.add(r)
		if err := n.connzPages(ctx, &cs, account, page); err != nil {
			return out, err
		}
		out = append(out, cs)
		if len(expect) > 0 {
			if allSeen(expect, seen) {
				break
			}
			wait = max(connzTotal-time.Since(start), time.Millisecond)
		} else {
			wait = connzSettle
		}
	}
	slices.SortFunc(out, func(a, b ConnzServer) int {
		switch {
		case a.Name < b.Name:
			return -1
		case a.Name > b.Name:
			return 1
		}
		return 0
	})
	return out, ctx.Err()
}

func allSeen(expect []string, seen map[string]bool) bool {
	for _, e := range expect {
		if !seen[e] {
			return false
		}
	}
	return true
}

func (cs *ConnzServer) add(r connzReply) {
	for _, c := range r.Data.Conns {
		cs.Conns = append(cs.Conns, ConnzConn{Key: c.User, Account: c.Account, Name: c.Name, IssuerKey: c.IssuerKey, Tags: c.Tags, CID: c.CID})
	}
}

// connzPages reads the pages after the first from the server's own subject until it has all of its Total.
func (n NATS) connzPages(ctx context.Context, cs *ConnzServer, account string, page int) error {
	for len(cs.Conns) < cs.Total {
		if cs.ID == "" {
			return fmt.Errorf("server %s has more connections than one CONNZ page and gave no id to ask again", config.Clean(cs.Name))
		}
		if cs.Total > maxConnzConns {
			return fmt.Errorf("server %s reports %d connections: refusing to read more than %d", config.Clean(cs.Name), cs.Total, maxConnzConns)
		}
		rctx, cancel := context.WithTimeout(ctx, connzPageTimeout)
		msg, err := n.Conn.RequestWithContext(rctx, fmt.Sprintf(connzDirectSubject, cs.ID), connzOptions(account, len(cs.Conns), page))
		cancel()
		if err != nil {
			return fmt.Errorf("server %s: the next CONNZ page: %w", config.Clean(cs.Name), err)
		}
		var r connzReply
		if json.Unmarshal(msg.Data, &r) != nil || r.Data == nil || r.Error != nil {
			return fmt.Errorf("server %s: the next CONNZ page could not be read", config.Clean(cs.Name))
		}
		before := len(cs.Conns)
		cs.add(r)
		if len(cs.Conns) == before {
			return fmt.Errorf("server %s: a CONNZ page came back empty before its %d connections were read", config.Clean(cs.Name), cs.Total)
		}
	}
	return nil
}
