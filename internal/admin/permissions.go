// Package admin provisions mash endpoints: the permission template a NATS
// user needs, and the streams, consumer and directory record behind it.
// mash never mints users or holds account seeds; the operator's own tooling
// issues the user with these permissions.
package admin

import (
	"slices"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// Permissions is the complete permission set of endpoint name under owner
// (spec section 8). Anything not listed is denied.
func Permissions(owner, name string) jwt.Permissions {
	return PermissionsTo(owner, name, nil)
}

// PermissionsTo is Permissions with the three peer-addressing publish lines (mail, prompt and status to any endpoint
// of the owner) narrowed to recipients: one line per recipient and verb, agents.<verb>.*.<owner>.<recipient>. A nil
// recipients keeps the wildcard lines, so PermissionsTo(owner, name, nil) is Permissions. The mail line is the five-token subject agents.mail.*.<owner>.* (ADR 0091). An empty non-nil slice
// grants no mail, prompt or status line at all; the fixed lines (own heartbeat, inbox, directory read, lease) remain,
// so the block is never empty. Recipients must be valid endpoint names: they are placed in subjects as they are.
func PermissionsTo(owner, name string, recipients []string) jwt.Permissions {
	var pub []string
	if recipients == nil {
		pub = append(pub,
			"agents.mail.*."+owner+".*",
			"agents.prompt.*."+owner+".*",
			"agents.status.*."+owner+".*")
	} else {
		for _, r := range slices.Sorted(slices.Values(recipients)) {
			for _, verb := range []string{"mail", "prompt", "status"} {
				pub = append(pub, "agents."+verb+".*."+owner+"."+r)
			}
		}
	}
	pub = append(pub, fixedPub(owner, name)...)
	subj := sap.For(owner, name)
	sub := []string{
		sap.InboxPrefix(name) + ".>",
		subj.Prompt,
		subj.Status,
		"$SRV.>",
		"agents.hb.*." + owner + ".*",
	}
	var p jwt.Permissions
	p.Pub.Allow.Add(pub...)
	p.Sub.Allow.Add(sub...)
	// No response permission: it grants publishing to any reply subject the
	// endpoint receives, so a request it sends itself with a reply subject of
	// $JS.API.STREAM.DELETE.<victim> would let it delete another inbox.
	return p
}

// fixedPub is the part of the publish allow list that is the same for every endpoint and names only the endpoint
// itself: its heartbeat, replies, the service API, and the JetStream and KV subjects of its own inbox, the directory
// and its lease. name may be the template {{name()}}.
func fixedPub(owner, name string) []string {
	return fixedPubKeyed(sap.For(owner, name).HB, name)
}

// fixedPubKeyed is fixedPub with the heartbeat subject and the flat key given. A legacy endpoint's flat key is its bare
// name (fixedPub); a new-form endpoint's is agent_owner_name (NewFormPermissions). The stream is inbox-<flat>, the
// consumer is the shared consumer name, and the lease key is <flat>.
func fixedPubKeyed(hb, flat string) []string {
	inbox := sap.InboxStream(flat)
	dir := "KV_" + store.DirectoryBucket
	lease := "KV_" + store.LeaseBucket
	name := flat
	return []string{
		hb,
		"_INBOX.>", // replies (SAP prompt chunks, $SRV answers) go to callers' inboxes only
		"$SRV.>",
		"$JS.API.INFO",
		"$JS.API.STREAM.INFO." + inbox,
		"$JS.API.CONSUMER.INFO." + inbox + "." + store.Consumer,
		"$JS.API.CONSUMER.MSG.NEXT." + inbox + "." + store.Consumer,
		"$JS.ACK." + inbox + "." + store.Consumer + ".>",
		"$JS.API.DIRECT.GET." + inbox,
		"$JS.API.DIRECT.GET." + inbox + ".>",
		"$JS.API.STREAM.INFO." + dir,
		"$JS.API.DIRECT.GET." + dir + ".>",
		"$JS.API.STREAM.INFO." + lease,
		"$KV." + store.LeaseBucket + "." + name,
		"$JS.API.DIRECT.GET." + lease + ".$KV." + store.LeaseBucket + "." + name,
	}
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
	return f
}
