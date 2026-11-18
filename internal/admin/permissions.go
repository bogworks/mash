// Package admin provisions mash endpoints: the permission template a NATS
// user needs, and the streams, consumer and directory record behind it.
// mash never mints users or holds account seeds; the operator's own tooling
// issues the user with these permissions.
package admin

import (
	"fmt"
	"strings"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// Permissions is the complete permission set of the endpoint at a (ADR 0145, spec section 8). Anything not listed is
// denied, and there is no response permission: it would grant publishing to any reply subject the endpoint receives, so
// a request it sends itself with a reply subject of $JS.API.STREAM.DELETE.<victim> would let it delete another inbox.
//
// Publish: mail, prompt and status to any endpoint of the same owner (agents.<verb>.<owner>.*.*, the owner a literal
// token), and the fixed lines: its own exact heartbeat, replies to the inboxes of its own owner (_INBOX.<owner>.>), the service API, and the JetStream and KV subjects of
// its own inbox, the directory and its lease, all keyed by its own flat key. Subscribe: its private reply prefix (_INBOX.<owner>.<flat>.>), its
// own prompt and status subjects, $SRV.>, and the heartbeats of its own owner (agents.hb.<owner>.*.*).
//
// The owner is a literal in every peer line, never a wildcard, so no line reaches another owner's endpoints. a must be
// valid (sap.NewAddress); for one that is not, Permissions returns nil, because an empty permission set would allow
// everything.
func Permissions(a sap.Address) *jwt.Permissions {
	if a.Validate() != nil {
		return nil
	}
	s := a.Subjects()
	peer := a.Owner + ".*.*"
	var pub []string
	for _, verb := range []string{"mail", "prompt", "status"} {
		pub = append(pub, "agents."+verb+"."+peer)
	}
	pub = append(pub, fixedPub(a)...)
	sub := []string{
		a.InboxPrefix() + ".>",
		s.Prompt,
		s.Status,
		"$SRV.>",
		"agents.hb." + peer,
	}
	var p jwt.Permissions
	p.Pub.Allow.Add(pub...)
	p.Sub.Allow.Add(sub...)
	return &p
}

// fixedPub is the part of the publish allow list that names only the endpoint itself: its heartbeat, replies, the
// service API, and the JetStream and KV subjects of its own inbox, the directory and its lease. The stream is
// inbox-<flat>, the consumer is the shared consumer name, and the lease key is <flat>.
func fixedPub(a sap.Address) []string {
	flat := a.Flat()
	inbox := a.InboxStream()
	dir := "KV_" + store.DirectoryBucket
	lease := "KV_" + store.LeaseBucket
	return []string{
		a.Subjects().HB,
		"_INBOX." + a.Owner + ".>", // replies (SAP prompt chunks, $SRV answers) go to inboxes of its own owner only
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
		"$KV." + store.LeaseBucket + "." + flat,
		"$JS.API.DIRECT.GET." + lease + ".$KV." + store.LeaseBucket + "." + flat,
	}
}

// NSCFlags renders Permissions as `nsc add user` flags. An invalid address gives none.
func NSCFlags(a sap.Address) []string {
	p := Permissions(a)
	if p == nil {
		return nil
	}
	var f []string
	for _, s := range p.Pub.Allow {
		f = append(f, "--allow-pub", s)
	}
	for _, s := range p.Sub.Allow {
		f = append(f, "--allow-sub", s)
	}
	return f
}

// ValidateGrant checks one subject grant: a grant that applies to the agents. subject space must not carry '*' or '>'
// in the owner token position, which is token index 2 of agents.<verb>.<owner>.<group>.<instance>, or at or before it.
// A grant is accepted only if every token is a literal or a lone '*', '>' appears only as the last token, no token is
// empty or has whitespace, and no token mixes a wildcard character with other text. A grant that starts with a
// wildcard applies to the agents. space too, so it is held to the same rule. An agents. grant needs at least four
// tokens (verb and owner are named, and something follows). Subjects outside the agents. space pass the structural
// checks only.
func ValidateGrant(subject string) error {
	const rule = "a grant must not carry '*' or '>' in the owner token position (token 3) or before it"
	if subject == "" || strings.ContainsAny(subject, " \t\r\n") {
		return fmt.Errorf("grant %q refused (%s): empty or contains whitespace", subject, rule)
	}
	toks := strings.Split(subject, ".")
	for i, t := range toks {
		switch {
		case t == "":
			return fmt.Errorf("grant %q refused (%s): empty token", subject, rule)
		case t == ">" && i != len(toks)-1:
			return fmt.Errorf("grant %q refused (%s): '>' only as the last token", subject, rule)
		case t != "*" && t != ">" && strings.ContainsAny(t, "*>"):
			return fmt.Errorf("grant %q refused (%s): partial wildcard token %q", subject, rule, t)
		}
	}
	if toks[0] == "_INBOX" && len(toks) >= 2 && (toks[1] == "*" || toks[1] == ">") {
		return fmt.Errorf("grant %q refused (a reply-inbox grant must name the owner token, not a wildcard)", subject)
	}
	if toks[0] != "agents" && toks[0] != "*" && toks[0] != ">" {
		return nil
	}
	for i, t := range toks {
		if i > 2 {
			break
		}
		if t == "*" || t == ">" {
			return fmt.Errorf("grant %q refused (%s)", subject, rule)
		}
	}
	if len(toks) < 4 {
		return fmt.Errorf("grant %q refused (%s): an agents. grant needs at least the verb and owner tokens, and a token after them", subject, rule)
	}
	return nil
}
