// Package sap implements the parts of the Synadia Agent Protocol 0.3 that
// mash uses: subjects (§2), and sender identity (§13). mash adds one verb,
// "mail", which SAP does not reserve.
package sap

import "strings"

// Protocol constants (SAP §2, §3, Appendix C).
const (
	AgentToken      = "cc"
	AgentName       = "claude-code"
	ServiceName     = "agents"
	QueueGroup      = "agents"
	ProtocolVersion = "0.3"
)

// Subjects are one endpoint's SAP subjects.
type Subjects struct {
	Prompt, Status, HB, Mail string
}

// For returns the subjects of endpoint name under owner.
func For(owner, name string) Subjects {
	s := func(verb string) string {
		return "agents." + verb + "." + AgentToken + "." + owner + "." + name
	}
	return Subjects{Prompt: s("prompt"), Status: s("status"), HB: s("hb"), Mail: s("mail")}
}

// MailFilter is the subject filter of name's inbox stream: mail to name
// under owner, from any agent token.
func MailFilter(owner, name string) string {
	return "agents.mail.*." + owner + "." + name
}

// DefaultZone is the conventional name of a zone for operators who want one;
// an endpoint with no zone in its record has the legacy 5-token mail subject,
// it is not in this zone.
const DefaultZone = "default"

// MaxZoneLen is the longest zone token.
const MaxZoneLen = 32

// ValidZone reports whether z is a zone token: 1-32 lower-case letters, digits
// and hyphens. That excludes '.', '*', '>' and whitespace, so a zone is always
// exactly one subject token.
func ValidZone(z string) bool {
	if z == "" || len(z) > MaxZoneLen {
		return false
	}
	for i := 0; i < len(z); i++ {
		c := z[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// ForZone returns the subjects of endpoint name under owner in zone: the same
// as For, except that mail carries the zone token,
// agents.mail.cc.<owner>.<zone>.<name>.
func ForZone(owner, zone, name string) Subjects {
	s := For(owner, name)
	s.Mail = "agents.mail." + AgentToken + "." + owner + "." + zone + "." + name
	return s
}

// MailFilterZone is the subject filter that matches mail to name under owner
// in exactly zone (the 6-token form), from any agent token. A zoned endpoint's
// inbox takes this filter and never a wildcard zone: a sender gated into one
// zone must not reach an endpoint in another.
func MailFilterZone(owner, zone, name string) string {
	return "agents.mail.*." + owner + "." + zone + "." + name
}

// ZoneOfMailFilter returns the zone of s if s is an exact-zone mail filter of
// name under owner (6 tokens: agents, mail, *, owner, a valid zone, name).
func ZoneOfMailFilter(s, owner, name string) (zone string, ok bool) {
	t := strings.Split(s, ".")
	if len(t) != 6 || t[0] != "agents" || t[1] != "mail" || t[2] != "*" || t[3] != owner || t[5] != name || !ValidZone(t[4]) {
		return "", false
	}
	return t[4], true
}

// SplitMail splits a mail subject into owner, zone and name. It accepts the
// legacy 5-token form (zone is "") and the 6-token zoned form.
func SplitMail(subj string) (owner, zone, name string, ok bool) {
	t := strings.Split(subj, ".")
	if len(t) < 5 || len(t) > 6 || t[0] != "agents" || t[1] != "mail" {
		return "", "", "", false
	}
	for _, x := range t {
		if x == "" {
			return "", "", "", false
		}
	}
	if len(t) == 5 {
		return t[3], "", t[4], true
	}
	return t[3], t[4], t[5], true
}

// InboxPrefix is the reply-subject prefix of endpoint name's connections:
// _INBOX.<name>. Each endpoint may subscribe only under its own prefix, so
// replies meant for one endpoint (JetStream deliveries, KV reads, prompt
// answers) are private; it may publish replies only under _INBOX.>.
func InboxPrefix(name string) string { return "_INBOX." + name }

// InboxStream is the JetStream stream that holds name's mail.
func InboxStream(name string) string { return "inbox-" + name }

// NameFromSubject returns the instance name (5th token) of a 5-token
// agents.* subject.
func NameFromSubject(subj string) (string, bool) {
	t := strings.Split(subj, ".")
	if len(t) != 5 || t[0] != "agents" || t[4] == "" {
		return "", false
	}
	return t[4], true
}

// KeyFromSubject returns the directory key of the endpoint a five-token
// agents.<verb>.<agent>.<owner>.<name> subject addresses. A subject whose agent
// token is AgentToken is legacy and its key is the bare name token, as
// NameFromSubject gives. Any other agent token is a new-form identity and the
// key is its flat form (agent_owner_name); tokens that fail the new token rule
// give ok false, so no subject can name a key its tokens could not have made.
func KeyFromSubject(subj string) (string, bool) {
	t := strings.Split(subj, ".")
	if len(t) != 5 || t[0] != "agents" || t[4] == "" {
		return "", false
	}
	if t[2] == AgentToken {
		return t[4], true
	}
	id, err := New(t[2], t[3], t[4])
	if err != nil {
		return "", false
	}
	return id.Flat(), true
}
