package listset

import (
	"sort"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/store"
)

// Tag names, the deny sentinel and the template layout (chosen by the herd under contract section 7; ADR 0102).
//
// A tag value is the address without its range part, "owner.name", lower case: the range is the account's and sits as
// a literal in every template line, so no tag can name an address outside the range (INV-005) and a tag is as short as
// the naming grammar allows (REQ-008's size limit). One allow tag expands in every verb's line, so an exception costs
// one tag across mail, prompt, status and heartbeat.
const (
	// AllowTag is the tag name of an allow entry: allow:<owner>.<name>.
	AllowTag = "allow"
	// DenyTag is the tag name of a deny entry: deny:<owner>.<name>.
	DenyTag = "deny"
	// NoDenyValue is the deny sentinel's value. It expands to agents.<verb>.<range>._none, a four-token subject, never
	// the five tokens of an address; "_none" is also no part the naming grammar allows (a part starts with a letter),
	// so the sentinel addresses nothing and can never become an address.
	NoDenyValue = "_none"
	// NoDenyTag is the deny tag every user of a deny key carries when it has no real deny entry.
	NoDenyTag = DenyTag + ":" + NoDenyValue

	nameFn = "{{name()}}"
	allowT = "{{tag(" + AllowTag + ")}}"
	denyT  = "{{tag(" + DenyTag + ")}}"
)

// Compiled is the output of Compile: one scoped-key template per owner group that has a sender, and one user per
// sender. A user is modelled as its tags and nothing else: Permissions is the claims a user JWT must NOT carry (NATS
// refuses a permission list on a user signed by a scoped key), kept here so Check can refuse a user that has one.
type Compiled struct {
	Range     string
	Templates []Template // sorted by Group
	Users     []User     // sorted by Sender
}

// Template is the permission template on the scoped signing key of one owner group.
type Template struct {
	// Group is "range.owner".
	Group       string
	Permissions jwt.Permissions
}

// User is the claims of one sender's user JWT that mash sets.
type User struct {
	// Sender is the three-part address.
	Sender string
	// Name is the JWT name claim, the sender's name part. The template's {{name()}} expands to it.
	Name string
	// Tags are the lower-case tags, sorted.
	Tags []string
	// Permissions must stay empty: a user signed by a scoped key carries only tags.
	Permissions jwt.UserPermissionLimits
}

// Compile validates ls, derives the templates and tags, and checks its own output with Check. On any error it
// returns nil and the error: a *ValidationError, or a *CheckError.
func Compile(ls ListSet) (*Compiled, error) {
	m, err := validate(ls)
	if err != nil {
		return nil, err
	}
	c := &Compiled{Range: m.rng}

	denyKey := map[string]bool{}
	for _, s := range m.senders {
		if len(s.deny) > 0 {
			denyKey[s.addr.group()] = true
		}
	}
	groups := map[string]address{}
	for _, s := range m.senders {
		groups[s.addr.group()] = s.addr
	}
	names := make([]string, 0, len(groups))
	for g := range groups {
		names = append(names, g)
	}
	sort.Strings(names)
	for _, g := range names {
		a := groups[g]
		c.Templates = append(c.Templates, Template{Group: g, Permissions: template(m, a.rng, a.owner, denyKey[g])})
	}

	for _, s := range m.senders {
		var tags []string
		for _, a := range s.allow {
			tags = append(tags, AllowTag+":"+a.tail())
		}
		for _, d := range s.deny {
			tags = append(tags, DenyTag+":"+d.tail())
		}
		if denyKey[s.addr.group()] && len(s.deny) == 0 {
			tags = append(tags, NoDenyTag)
		}
		sort.Strings(tags)
		c.Users = append(c.Users, User{Sender: s.addr.String(), Name: s.addr.name, Tags: tags})
	}

	if err := Check(ls, c); err != nil {
		return nil, err
	}
	return c, nil
}

// template builds the permission template of owner group rng.owner.
//
// Publish allow: the owner line of each shared publish verb; one tag line per publish verb; the fixed lines. Subscribe
// allow: the owner line of heartbeat when shared; one tag line for heartbeat; the fixed lines. Deny lines exist only
// when the key has a deny entry (withDeny), only for shared verbs: a deny beats an allow, and a closed verb has no
// owner line to take an address from.
//
// The fixed lines name the sender through {{name()}} and the key's own range and owner, never through a tag: the
// sender's own heartbeat, its inbox stream and consumer, its reply prefix, its own prompt and status subjects, the
// directory and its lease.
func template(m *model, rng, owner string, withDeny bool) jwt.Permissions {
	group := rng + "." + owner
	selfSubj := func(v Verb) string { return "agents." + v.subject() + "." + group + "." + nameFn }
	var pub, sub, pubDeny, subDeny []string
	for _, v := range []Verb{Mail, Prompt, Status} {
		if m.shared(group, v) {
			pub = append(pub, "agents."+v.subject()+"."+group+".*")
		}
		pub = append(pub, "agents."+v.subject()+"."+rng+"."+allowT)
		if withDeny && m.shared(group, v) {
			pubDeny = append(pubDeny, "agents."+v.subject()+"."+rng+"."+denyT)
		}
	}
	if m.shared(group, Heartbeat) {
		sub = append(sub, "agents.hb."+group+".*")
		if withDeny {
			subDeny = append(subDeny, "agents.hb."+rng+"."+denyT)
		}
	}
	sub = append(sub, "agents.hb."+rng+"."+allowT)

	flat := rng + "_" + owner + "_" + nameFn
	pub = append(pub, fixedPub(selfSubj(Heartbeat), flat)...)
	sub = append(sub,
		"_INBOX."+flat+".>",
		selfSubj(Prompt),
		selfSubj(Status),
		"$SRV.>",
	)

	var p jwt.Permissions
	p.Pub.Allow.Add(pub...)
	p.Pub.Deny.Add(pubDeny...)
	p.Sub.Allow.Add(sub...)
	p.Sub.Deny.Add(subDeny...)
	return p
}

// fixedPub is the part of a sender's publish allow list that names only the sender itself and the shared plumbing:
// its heartbeat, replies, the service API, and the JetStream and KV subjects of its own inbox, the directory and its
// lease. hb is the sender's own heartbeat subject and flat its flat key (range_owner_name). The lines and their order
// are those of internal/admin fixedPubKeyed (the unexported source); this copy lets the template name the sender
// through {{name()}}. It never names another address's inbox, stream or consumer (REQ-001), and has no tag.
func fixedPub(hb, flat string) []string {
	inbox := "inbox-" + flat
	dir := "KV_" + store.DirectoryBucket
	lease := "KV_" + store.LeaseBucket
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
		"$KV." + store.LeaseBucket + "." + flat,
		"$JS.API.DIRECT.GET." + lease + ".$KV." + store.LeaseBucket + "." + flat,
	}
}
