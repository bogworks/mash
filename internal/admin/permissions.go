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

// Tag names a user's JWT carries to widen or narrow what its zone's signing key grants, and the templates that expand
// them. A tag is "<name>:<zone>.<endpoint>" (or "<name>:<zone>.*"), lower case, and its value must be a valid subject
// tail: it is placed in a permission subject as it stands.
const (
	AllowTag = "allow"
	DenyTag  = "deny"
	// NoDenyTag is the placeholder deny tag EVERY user carries, whether or not its zone has a deny exception.
	// nats-server refuses to authenticate a user whose scoped key has a {{tag(deny)}} template in a deny list and
	// that has no deny tag ("generated invalid subject"; allow lists just drop the line), and every zone key carries
	// that template from its first compile, so every user needs one deny tag: this one. Its value "_none" is a valid
	// subject token, and no endpoint or zone name can be "_none" (names start with a letter), so
	// agents.mail.*.<owner>._none addresses nothing. Because every user carries it from the start, the first real deny
	// exception of a zone changes only the JWTs of the endpoints it names, never the zone key.
	NoDenyTag = DenyTag + ":_none"
)

// ZonePermissions is the permission template of the scoped signing key of zone under owner. Only MAIL carries the zone
// token (ADR 0054): agents.mail.<kind>.<owner>.<zone>.<name>. Prompt, status and heartbeat keep SAP's five-token
// subjects, so the key grants those as they are. Every endpoint of the zone is a user signed by this key and gets, by
// expansion, its own copy of the fixed per-endpoint lines ({{name()}} is the user's name) and these peer-addressing
// lines:
//
//   - one MAIL publish line per wildcard zone: agents.mail.*.<owner>.<zone>.*, so every endpoint of the zone may mail
//     every endpoint of each wildcardZones entry;
//   - the mail exception line agents.mail.*.<owner>.{{tag(allow)}}, one subject per "allow:" tag of the user; a user
//     with no allow tag gets none;
//   - the mail deny line agents.mail.*.<owner>.{{tag(deny)}}, one subject per "deny:" tag of the user: a deny beats
//     any allow, so a user's deny tags remove peers from what the mail wildcards grant. The key carries it always,
//     so adding a zone's first deny exception never changes the key. nats-server refuses a user with no deny tag
//     under this template, so every user carries NoDenyTag at least;
//   - two fixed, open lines, agents.prompt.*.<owner>.* and agents.status.*.<owner>.*: live prompts and status probes
//     to any endpoint of the owner are not policed until a later decision (ADR 0054).
//
// The endpoint's own prompt and status subscriptions are the five-token sap.For subjects with {{name()}}. Nothing else
// addresses a peer. The block is never empty. The templates need the
// scoped-key user to carry empty permissions of its own. zone names the key; no line spells it out except through
// the mail wildcards it is a recipient of.
func ZonePermissions(owner, zone string, wildcardZones []string) jwt.Permissions {
	const name = "{{name()}}"
	var pub []string
	for _, z := range slices.Sorted(slices.Values(wildcardZones)) {
		pub = append(pub, "agents.mail.*."+owner+"."+z+".*")
	}
	pub = append(pub, "agents.mail.*."+owner+".{{tag("+AllowTag+")}}")
	pub = append(pub, "agents.prompt.*."+owner+".*", "agents.status.*."+owner+".*")
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
	p.Pub.Deny.Add("agents.mail.*." + owner + ".{{tag(" + DenyTag + ")}}")
	p.Sub.Allow.Add(sub...)
	return p
}
