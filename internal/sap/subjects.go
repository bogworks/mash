// Package sap implements the parts of the Synadia Agent Protocol 0.3 that
// mash uses: subjects (§2), and sender identity (§13). mash adds one verb,
// "mail", which SAP does not reserve.
package sap

import (
	"strings"
	"sync/atomic"

	agents "github.com/bogworks/synadia-agents.go"
)

// Protocol constants. ServiceName, QueueGroup and ProtocolVersion are the
// SDK's (SAP §3, §11); AgentToken and AgentName are mash's own choices for its
// agent (SAP Appendix C), which the SDK has no counterpart for.
const (
	AgentToken      = "cc"
	AgentName       = "claude-code"
	ServiceName     = agents.ServiceName
	QueueGroup      = agents.QueueGroup
	ProtocolVersion = agents.ProtocolVersion
)

// Subjects are one endpoint's SAP subjects.
type Subjects struct {
	Prompt, Status, HB, Mail string
}

// subjectFallbacks counts how often endpointSubjects fell back to string
// construction; tests read it to show the SDK builder accepts every name mash does.
var subjectFallbacks atomic.Int64

// endpointSubjects builds the four subjects of an endpoint: prompt, status and
// heartbeat through the SDK's AgentSubject, mail (which SAP has no verb for) by
// hand. agent is the agent identifier and token the third subject token (equal
// to agent unless an abbreviation such as "cc" is in use). The SDK's token rule
// is looser than mash's, so it only errs for a value mash's validators already
// refuse (empty, or containing a dot); For and Subjects have no error return,
// so such a value falls back to the plain construction, which never panics.
func endpointSubjects(agent, token, owner, name string) Subjects {
	mail := "agents.mail." + token + "." + owner + "." + name
	var opts []agents.SubjectOption
	if token != agent {
		opts = append(opts, agents.WithSubjectToken(token))
	}
	if as, err := agents.NewAgentSubject(agent, owner, name, opts...); err == nil {
		return Subjects{Prompt: as.Prompt(), Status: as.Status(), HB: as.Heartbeat(), Mail: mail}
	}
	subjectFallbacks.Add(1)
	s := func(verb string) string { return "agents." + verb + "." + token + "." + owner + "." + name }
	return Subjects{Prompt: s("prompt"), Status: s("status"), HB: s("hb"), Mail: mail}
}

// For returns the subjects of endpoint name under owner.
func For(owner, name string) Subjects {
	return endpointSubjects(AgentName, AgentToken, owner, name)
}

// MailFilter is the subject filter of name's inbox stream: mail to name
// under owner, from any agent token.
func MailFilter(owner, name string) string {
	return "agents.mail.*." + owner + "." + name
}

// LegacyZoneOfMailFilter returns the zone token of s if s is a six-token zoned mail
// filter of name under owner (agents, mail, *, owner, a zone token, name). It is
// legacy parsing for "mashctl unzone" and the upgrade guards, which have to read
// an inbox stream a zoned release made; nothing builds a zoned filter.
func LegacyZoneOfMailFilter(s, owner, name string) (zone string, ok bool) {
	t := strings.Split(s, ".")
	if len(t) != 6 || t[0] != "agents" || t[1] != "mail" || t[2] != "*" || t[3] != owner || t[5] != name || !legacyZoneToken(t[4]) {
		return "", false
	}
	return t[4], true
}

// LegacyZoneOfMailSubject returns the zone token of s if s is the six-token zoned mail
// subject of name under owner (agents.mail.cc.<owner>.<zone>.<name>). It is legacy parsing
// for reading a directory record a zoned release wrote; nothing builds such a subject.
func LegacyZoneOfMailSubject(s, owner, name string) (zone string, ok bool) {
	t := strings.Split(s, ".")
	if len(t) != 6 || t[0] != "agents" || t[1] != "mail" || t[2] != AgentToken || t[3] != owner || t[5] != name || !legacyZoneToken(t[4]) {
		return "", false
	}
	return t[4], true
}

// legacyZoneToken reports whether z is a token a zoned release could have used:
// 1-32 lower-case letters, digits and hyphens.
func legacyZoneToken(z string) bool {
	if z == "" || len(z) > 32 {
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

// SplitMail splits a mail subject into owner and name. A mail subject has exactly
// five tokens, agents.mail.<agent>.<owner>.<name>; any other shape is not mail.
func SplitMail(subj string) (owner, name string, ok bool) {
	t := strings.Split(subj, ".")
	if len(t) != 5 || t[0] != "agents" || t[1] != "mail" {
		return "", "", false
	}
	for _, x := range t {
		if x == "" {
			return "", "", false
		}
	}
	return t[3], t[4], true
}

// InboxPrefix is the reply-subject prefix of endpoint name's connections:
// _INBOX.<name>. Each endpoint may subscribe only under its own prefix, so
// replies meant for one endpoint (JetStream deliveries, KV reads, prompt
// answers) are private; it may publish replies only under _INBOX.>.
func InboxPrefix(name string) string { return "_INBOX." + name }

// InboxStream is the JetStream stream that holds name's mail.
func InboxStream(name string) string { return "inbox-" + name }

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
