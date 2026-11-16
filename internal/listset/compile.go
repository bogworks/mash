package listset

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// Tag names, the deny sentinel and the template layout (ADR 0102, amended by ADR 0132).
//
// The scope of a list set is the NATS account and an owner group is the owner, so an address is "owner.name" and a
// tag value is that address, lower case. The third subject token of an address subject (the SAP agent token of the
// endpoint's program) is never named by a list set and is a wildcard in every template line, so one allow tag expands
// in every verb's line and an exception costs one tag across mail, prompt, status and heartbeat.
const (
	// AllowTag is the tag name of an allow entry: allow:<owner>.<name>.
	AllowTag = "allow"
	// DenyTag is the tag name of a deny entry: deny:<owner>.<name>.
	DenyTag = "deny"
	// NoDenyValue is the deny sentinel's value. It expands to agents.<verb>.*._none, a four-token subject, never
	// the five tokens of an address; "_none" is also no part the naming grammar allows (a part starts with a letter),
	// so the sentinel addresses nothing and can never become an address.
	NoDenyValue = "_none"
	// NoDenyTag is the deny tag every user of a deny key carries when it has no real deny entry.
	NoDenyTag = DenyTag + ":" + NoDenyValue
	// EndpointTagName is the tag name of the endpoint tag: ep:<flat key>. The ISSUER adds it to every user JWT it
	// signs under an owner group's scoped key (see EndpointTag); a list set and Compiled.Users never name it.
	EndpointTagName = "ep"

	nameFn = "{{name()}}"
	allowT = "{{tag(" + AllowTag + ")}}"
	denyT  = "{{tag(" + DenyTag + ")}}"
	epT    = "{{tag(" + EndpointTagName + ")}}"
	// anyAgent is the template's agent token position: a wildcard.
	anyAgent = "*"
)

// Compiled is the output of Compile: one scoped-key template per owner group that has a sender, and one user per
// sender. A user is modelled as its tags and nothing else: Permissions is the claims a user JWT must NOT carry (NATS
// refuses a permission list on a user signed by a scoped key), kept here so Check can refuse a user that has one.
type Compiled struct {
	Templates []Template // sorted by Group
	Users     []User     // sorted by Sender
}

// Template is the permission template on the scoped signing key of one owner group.
type Template struct {
	// Group is the owner group: the owner part of the addresses it holds.
	Group       string
	Permissions jwt.Permissions
}

// User is the claims of one sender's user JWT that mash sets.
type User struct {
	// Sender is the two-part address, "owner.name".
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
	c := build(m)
	if err := Check(ls, c); err != nil {
		return nil, err
	}
	return c, nil
}

// build derives the templates and tags of a validated model. It does not check them; DryRun uses it to collect the
// refusals of Check and SizeCheck together.
func build(m *model) *Compiled {
	c := &Compiled{}

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
		c.Templates = append(c.Templates, Template{Group: g, Permissions: template(m, a.owner, denyKey[g])})
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
	return c
}

// template builds the permission template of owner group owner.
//
// Publish allow: the owner line of each shared publish verb; one tag line per publish verb; the fixed lines. Subscribe
// allow: the owner line of heartbeat when shared; one tag line for heartbeat; the fixed lines. Deny lines exist only
// when the key has a deny entry (withDeny), only for shared verbs: a deny beats an allow, and a closed verb has no
// owner line to take an address from.
//
// The agent token position of every address subject is a wildcard: no line names an agent token. The fixed lines name
// the sender through {{name()}} and the key's own owner, and through the endpoint tag {{tag(ep)}} where the flat key
// of the sender's endpoint is one subject token with the agent token inside it (inbox-<flat>, the lease key and the
// reply prefix): the sender's own heartbeat, its inbox stream and consumer, its reply prefix, its own prompt and
// status subjects, the directory and its lease. A credential without the endpoint tag gets none of the lines that
// need it (the server drops a template line whose tag is missing).
func template(m *model, owner string, withDeny bool) jwt.Permissions {
	selfSubj := func(v Verb) string { return "agents." + v.subject() + "." + anyAgent + "." + owner + "." + nameFn }
	var pub, sub, pubDeny, subDeny []string
	for _, v := range []Verb{Mail, Prompt, Status} {
		if m.shared(owner, v) {
			pub = append(pub, "agents."+v.subject()+"."+anyAgent+"."+owner+".*")
		}
		pub = append(pub, "agents."+v.subject()+"."+anyAgent+"."+allowT)
		if withDeny && m.shared(owner, v) {
			pubDeny = append(pubDeny, "agents."+v.subject()+"."+anyAgent+"."+denyT)
		}
	}
	if m.shared(owner, Heartbeat) {
		sub = append(sub, "agents.hb."+anyAgent+"."+owner+".*")
		if withDeny {
			subDeny = append(subDeny, "agents.hb."+anyAgent+"."+denyT)
		}
	}
	sub = append(sub, "agents.hb."+anyAgent+"."+allowT)

	pub = append(pub, fixedPub(selfSubj(Heartbeat), PolicySubject(), epT)...)
	sub = append(sub,
		"_INBOX."+epT+".>",
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

// PolicySubject is the subject of the policy responder: "mash.policy". Every sender may publish to it, so `mash policy
// show` and `mash policy explain` can ask it; the answer returns on the sender's own _INBOX prefix. It has two tokens
// and does not begin with agents, so it is no address subject and no reach.
func PolicySubject() string { return "mash.policy" }

// EndpointTag is the endpoint tag of the endpoint id at address: "ep:<flat key>", the flat key being agent_owner_name
// for a new-form identity and the bare name for a legacy one (id.Flat(), lower case). It is the one function every issuer of a user JWT under an owner group's scoped key calls to build the
// tag, so the tag comes from mash's own table of the endpoint's identity and never from a request. It refuses an
// identity whose owner and name are not the address's (a credential must not point at another address's inbox). A
// legacy flat key has no owner and a legacy name belongs to one owner only through the directory, so this function
// cannot tell whether the name is the address's owner's: the issuer checks that against the directory before it signs
// a legacy tag (ADR 0138). A legacy flat key cannot equal a new-form one (a new-form flat key has underscores).
func EndpointTag(id sap.EndpointIdentity, address string) (string, error) {
	if err := id.Validate(); err != nil {
		return "", fmt.Errorf("endpoint tag: %w", err)
	}
	a, reason := parseAddress(address)
	if reason != "" {
		return "", fmt.Errorf("endpoint tag: address %q: %s", address, reason)
	}
	if id.Owner != a.owner || id.Name != a.name {
		return "", fmt.Errorf("endpoint tag: endpoint %s is not at address %s: a credential cannot point at another address's inbox", id, address)
	}
	return EndpointTagName + ":" + id.Flat(), nil
}

// WithEndpointTag returns tags (a lower-case, sorted set) with the endpoint tag of endpoint, the canonical
// agent.owner.name of the endpoint, added for address. Any ep tag already in tags is an error: a credential carries
// one, and it is the issuer's.
func WithEndpointTag(tags []string, endpoint, address string) ([]string, error) {
	var id sap.EndpointIdentity
	var err error
	if parts := strings.Split(endpoint, "."); len(parts) == 3 && parts[0] == sap.AgentToken {
		id, err = sap.Legacy(parts[1], parts[2]) // cc.<owner>.<name> is a legacy identity (ADR 0138)
	} else {
		id, err = sap.ParseCanonical(endpoint)
	}
	if err != nil {
		return nil, fmt.Errorf("endpoint tag: endpoint %q: %s", endpoint, strings.TrimPrefix(err.Error(), "sap: "))
	}
	t, err := EndpointTag(id, address)
	if err != nil {
		return nil, err
	}
	for _, x := range tags {
		if IsEndpointTag(x) {
			return nil, fmt.Errorf("endpoint tag: the tags already hold %q; the issuer supplies it", x)
		}
	}
	out := append(slices.Clone(tags), t)
	sort.Strings(out)
	return out, nil
}

// IsEndpointTag reports whether tag is an endpoint tag.
func IsEndpointTag(tag string) bool { return strings.HasPrefix(tag, EndpointTagName+":") }

// WithoutEndpointTag returns tags without the endpoint tag: the tags a list set compiles to.
func WithoutEndpointTag(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if !IsEndpointTag(t) {
			out = append(out, t)
		}
	}
	return out
}

// fixedPub is the part of a sender's publish allow list that names only the sender itself and the shared plumbing:
// its heartbeat, replies, the service API, and the JetStream and KV subjects of its own inbox, the directory and its
// lease, and the policy responder. hb is the sender's own heartbeat subject, policy the responder's subject and flat
// the sender's flat key (agent_owner_name) or, in a template, the endpoint tag expansion. The lines and their order
// are those of internal/admin fixedPubKeyed (the unexported source); this copy lets the template name the sender
// through {{name()}} and {{tag(ep)}}. It never names another address's inbox, stream or consumer (REQ-001).
func fixedPub(hb, policy, flat string) []string {
	inbox := "inbox-" + flat
	dir := "KV_" + store.DirectoryBucket
	lease := "KV_" + store.LeaseBucket
	return []string{
		hb,
		policy,
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
