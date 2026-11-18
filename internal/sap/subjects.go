// Package sap implements the parts of the Synadia Agent Protocol 0.3 that
// mash uses: subjects (§2), and sender identity (§13). mash adds one verb,
// "mail", which SAP does not reserve.
package sap

// Protocol constants (SAP §2, §3, Appendix C).
const (
	AgentName       = "claude-code"
	ServiceName     = "agents"
	QueueGroup      = "agents"
	ProtocolVersion = "0.3"
)

// Subjects are one endpoint's SAP subjects.
type Subjects struct {
	Prompt, Status, HB, Mail string
}

// InboxStream is the JetStream stream that holds an endpoint's mail. The
// argument is the endpoint's flat key (Address.Flat).
func InboxStream(flat string) string { return "inbox-" + flat }

// InboxPrefix is the reply-subject prefix of a's connections: _INBOX.<owner>.<flat>, where the flat key is
// Address.Flat. The endpoint may subscribe only under its own prefix, so replies meant for it (JetStream deliveries,
// KV reads, prompt answers, discovery answers) are private; it may publish replies only under _INBOX.<owner>.>, the
// inboxes of its own owner, so an endpoint of another owner can neither answer nor forge into them.
func (a Address) InboxPrefix() string { return "_INBOX." + a.Owner + "." + a.Flat() }

// InboxStream is the JetStream stream that holds a's mail.
func (a Address) InboxStream() string { return InboxStream(a.Flat()) }
