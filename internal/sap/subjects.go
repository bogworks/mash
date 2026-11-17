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

// InboxPrefix is the reply-subject prefix of an endpoint's connections:
// _INBOX.<flat>. The argument is the endpoint's flat key (Address.Flat). Each
// endpoint may subscribe only under its own prefix, so replies meant for one
// endpoint (JetStream deliveries, KV reads, prompt answers) are private; it may
// publish replies only under _INBOX.>.
func InboxPrefix(flat string) string { return "_INBOX." + flat }

// InboxStream is the JetStream stream that holds an endpoint's mail. The
// argument is the endpoint's flat key (Address.Flat).
func InboxStream(flat string) string { return "inbox-" + flat }

// InboxPrefix is the reply-subject prefix of a's connections.
func (a Address) InboxPrefix() string { return InboxPrefix(a.Flat()) }

// InboxStream is the JetStream stream that holds a's mail.
func (a Address) InboxStream() string { return InboxStream(a.Flat()) }
