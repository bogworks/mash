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
