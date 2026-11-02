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
// recipients keeps the wildcard lines, so PermissionsTo(owner, name, nil) is Permissions. The wildcard mail line comes twice, the legacy 5-token form and the zoned 6-token form (ADR 0054); the recipient form keeps its one legacy line per recipient. An empty non-nil slice
// grants no mail, prompt or status line at all; the fixed lines (own heartbeat, inbox, directory read, lease) remain,
// so the block is never empty. Recipients must be valid endpoint names: they are placed in subjects as they are.
func PermissionsTo(owner, name string, recipients []string) jwt.Permissions {
	var pub []string
	if recipients == nil {
		pub = append(pub,
			"agents.mail.*."+owner+".*",
			"agents.mail.*."+owner+".*.*", // the zoned mail subject (ADR 0054)
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
	subj := sap.For(owner, name)
	inbox := sap.InboxStream(name)
	dir := "KV_" + store.DirectoryBucket
	lease := "KV_" + store.LeaseBucket
	return []string{
		subj.HB,
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

// Tag names a user's JWT carries to widen or narrow what its zone's signing key grants, and the templates that expand
// them. A tag is "<name>:<zone>.<endpoint>" (or "<name>:<zone>.*"), lower case, and its value must be a valid subject
// tail: it is placed in a permission subject as it stands.
const (
	AllowTag = "allow"
	DenyTag  = "deny"
	// NoDenyTag is the harmless deny tag a user of a zone whose key has deny templates carries when it has no other.
	// nats-server refuses to authenticate a user whose scoped key has a {{tag(deny)}} template in a deny list and
	// that has no deny tag ("generated invalid subject"; allow lists just drop the line). No zone or endpoint name
	// starts with a digit, so agents.<verb>.*.<owner>.0 addresses nothing.
	NoDenyTag = DenyTag + ":0"
)

// ZonePermissions is the permission template of the scoped signing key of zone under owner, in the subject layout
// agents.<verb>.<kind>.<owner>.<zone>.<name> (heartbeats keep SAP's five tokens). Every endpoint of the zone is a
// user signed by this key and gets, by expansion, its own copy of the fixed per-endpoint lines ({{name()}} is the
// user's name) and these peer-addressing lines:
//
//   - one publish line per verb (mail, prompt, status) and wildcard zone: agents.<verb>.*.<owner>.<zone>.*, so
//     every endpoint of the zone may address every endpoint of each wildcardZones entry;
//   - the exception line agents.<verb>.*.<owner>.{{tag(allow)}}, one subject per "allow:" tag of the user; a user
//     with no allow tag gets none.
//
// Nothing else addresses a peer. The block is never empty. See ZonePermissionsWithDeny for deny exceptions. The
// templates need the scoped-key user to carry empty permissions of its own.
func ZonePermissions(owner, zone string, wildcardZones []string) jwt.Permissions {
	const name = "{{name()}}"
	var pub []string
	for _, z := range slices.Sorted(slices.Values(wildcardZones)) {
		for _, verb := range zoneVerbs {
			pub = append(pub, "agents."+verb+".*."+owner+"."+z+".*")
		}
	}
	for _, verb := range zoneVerbs {
		pub = append(pub, "agents."+verb+".*."+owner+".{{tag("+AllowTag+")}}")
	}
	pub = append(pub, fixedPub(owner, name)...)
	sub := []string{
		sap.InboxPrefix(name) + ".>",
		"agents.prompt." + sap.AgentToken + "." + owner + "." + zone + "." + name,
		"agents.status." + sap.AgentToken + "." + owner + "." + zone + "." + name,
		"$SRV.>",
		"agents.hb.*." + owner + ".*",
	}
	var p jwt.Permissions
	p.Pub.Allow.Add(pub...)
	p.Sub.Allow.Add(sub...)
	return p
}

// ZonePermissionsWithDeny is ZonePermissions plus the publish deny lines agents.<verb>.*.<owner>.{{tag(deny)}}: a
// user's "deny:" tags remove peers from what the zone's wildcards grant (a deny beats any allow). Every user of the
// zone must then carry at least one deny tag, NoDenyTag if it has no real one.
func ZonePermissionsWithDeny(owner, zone string, wildcardZones []string) jwt.Permissions {
	p := ZonePermissions(owner, zone, wildcardZones)
	for _, verb := range zoneVerbs {
		p.Pub.Deny.Add("agents." + verb + ".*." + owner + ".{{tag(" + DenyTag + ")}}")
	}
	return p
}

var zoneVerbs = []string{"mail", "prompt", "status"}
