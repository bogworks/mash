// Package admin provisions mash endpoints: the permission template a NATS
// user needs, and the streams, consumer and directory record behind it.
// mash never mints users or holds account seeds; the operator's own tooling
// issues the user with these permissions.
package admin

import (
	"fmt"
	"time"

	"github.com/nats-io/jwt/v2"

	"github.com/randybias/mash/internal/sap"
	"github.com/randybias/mash/internal/store"
)

// Response limits: a SAP prompt reply is a stream of chunks (ack, "working"
// every 30 s for up to 10 minutes, response, terminator) to the caller's inbox.
const (
	RespMaxMsgs = 1000
	RespTTL     = 15 * time.Minute
)

// Permissions is the complete permission set of endpoint name under owner
// (spec section 8). Anything not listed is denied.
func Permissions(owner, name string) jwt.Permissions {
	subj := sap.For(owner, name)
	inbox := sap.InboxStream(name)
	dir := "KV_" + store.DirectoryBucket
	lease := "KV_" + store.LeaseBucket
	pub := []string{
		"agents.mail.*." + owner + ".*",
		"agents.prompt.*." + owner + ".*",
		"agents.status.*." + owner + ".*",
		subj.HB,
		"$SRV.>",
		"$JS.API.INFO",
		"$JS.API.STREAM.INFO." + inbox,
		"$JS.API.CONSUMER.INFO." + inbox + "." + store.Consumer,
		"$JS.API.CONSUMER.MSG.NEXT." + inbox + "." + store.Consumer,
		"$JS.ACK." + inbox + "." + store.Consumer + ".>",
		"$JS.API.DIRECT.GET." + inbox,
		"$JS.API.DIRECT.GET." + inbox + ".>",
		"$JS.API.STREAM.INFO." + dir,
		"$JS.API.CONSUMER.CREATE." + dir + ".>",
		"$JS.API.CONSUMER.DELETE." + dir + ".>",
		"$JS.API.DIRECT.GET." + dir + ".>",
		"$JS.FC." + dir + ".>",
		"$JS.API.STREAM.INFO." + lease,
		"$KV." + store.LeaseBucket + "." + name,
		"$JS.API.DIRECT.GET." + lease + ".$KV." + store.LeaseBucket + "." + name,
	}
	sub := []string{
		"_INBOX.>",
		subj.Prompt,
		subj.Status,
		"$SRV.>",
		"agents.hb.*." + owner + ".*",
	}
	var p jwt.Permissions
	p.Pub.Allow.Add(pub...)
	p.Sub.Allow.Add(sub...)
	p.Resp = &jwt.ResponsePermission{MaxMsgs: RespMaxMsgs, Expires: RespTTL}
	return p
}

// NSCFlags renders Permissions as `nsc add user` flags.
func NSCFlags(owner, name string) []string {
	p := Permissions(owner, name)
	var f []string
	for _, s := range p.Pub.Allow {
		f = append(f, "--allow-pub", s)
	}
	for _, s := range p.Sub.Allow {
		f = append(f, "--allow-sub", s)
	}
	f = append(f, fmt.Sprintf("--allow-pub-response=%d", RespMaxMsgs), "--response-ttl", "15m")
	return f
}
