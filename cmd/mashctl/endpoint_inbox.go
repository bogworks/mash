package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore/render"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/sapsdk"
)

const endpointInboxUsage = `Usage: mashctl endpoint inbox <address> [--after number] [--limit n] [--json]
  address  the endpoint, as owner.group.name
  --after  only messages after this message number, oldest first
  --limit  how many messages to list (default 20)
  --json   print the list as JSON
  Shows who sent each message, when, its kind and size. Never shows its text.
Example:
  mashctl endpoint inbox acme.demo.bob --limit 5
`

var endpointInboxSpec = epSpec{
	name: "inbox", usage: endpointInboxUsage, values: []string{"--limit", "--after"}, min: 1, max: 1,
	need: "the endpoint address (owner.group.name)",
	check: func(in *epInput) error {
		if err := checkAddress(in); err != nil {
			return err
		}
		hint := "mashctl endpoint inbox --help"
		if v, ok := in.values["--limit"]; ok {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return cliframe.Usagef("--limit needs a whole number of 1 or more").WithHint(hint)
			}
			in.limit = n
		}
		if v, ok := in.values["--after"]; ok {
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return cliframe.Usagef("--after must be a whole number").WithHint(hint)
			}
			in.after, in.afterSet = n, true
		}
		return nil
	},
}

// inboxNow is the clock the SENT column counts from; a test replaces it.
var inboxNow = time.Now

// inboxMessageJSON is one message of the JSON list. It carries no message text.
type inboxMessageJSON struct {
	Number    uint64 `json:"number"`
	From      string `json:"from"`
	Trust     string `json:"trust"`
	Delivered bool   `json:"delivered"`
	Kind      string `json:"kind"`
	Size      int    `json:"size"`
	When      string `json:"when"`
	// Reason is the class of a refusal (a frozen class string); present on refused entries only.
	Reason string `json:"reason,omitempty"`
}

// inboxJSON is the JSON of `mashctl endpoint inbox`.
type inboxJSON struct {
	Address     string             `json:"address"`
	Total       uint64             `json:"total"`
	Undelivered uint64             `json:"undelivered"`
	Refused     int                `json:"refused"`
	Messages    []inboxMessageJSON `json:"messages"`
}

// inboxTrust is a message's trust label as shown: a message with a header fault is "refused" (CHG-9; its class is the
// reason), and no label is "-".
func inboxTrust(t string) string {
	switch t {
	case "":
		return "-"
	case "malformed":
		return "refused"
	}
	return t
}

// inboxReason is the class shown as a message's reason: the one the inspection found, and for a header fault that gave
// none, the class for an unreadable header. Messages mash does not refuse have none.
func inboxReason(m admin.InspectedMessage) string {
	if m.Reason == "" && m.Trust == "malformed" {
		return sapsdk.ClassUnreadable
	}
	return m.Reason
}

// senderAddress is a message's sender as an address: the stored flat name read as owner.group.name, else as stored.
func senderAddress(sender string) string {
	if a, err := sap.AddressFromFlat(sender); err == nil {
		return a.String()
	}
	return sender
}

// ago is how long before now t was: 40s ago, 2m ago, 3h ago, 5d ago.
func ago(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		if d < 0 {
			d = 0
		}
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d.Hours() < 48:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// byteSize is a size as 48 B, 1.5 KiB, 2.0 MiB.
func byteSize(n int) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}

// buildInboxJSON is the list an inspection holds: the stream's message count, the messages the deliver consumer has not
// delivered (all of them when there is no consumer), and the messages inspected. It carries no message text.
func buildInboxJSON(addr sap.Address, in *admin.InboxInspection) inboxJSON {
	rep := inboxJSON{Address: addr.String(), Total: in.Stream.Messages, Undelivered: in.Stream.Messages, Refused: in.Refused, Messages: make([]inboxMessageJSON, 0, len(in.Messages))}
	if in.Consumer != nil {
		rep.Undelivered = in.Consumer.NumPending
	}
	for _, m := range in.Messages {
		rep.Messages = append(rep.Messages, inboxMessageJSON{Number: m.Seq, From: senderAddress(m.Sender), Trust: inboxTrust(m.Trust), Delivered: m.State != admin.StateUndelivered, Kind: m.Kind, Size: m.Size, When: m.StoredAt.UTC().Format(time.RFC3339Nano), Reason: inboxReason(m)})
	}
	return rep
}

// runEndpointInbox is `mashctl endpoint inbox`: a read-only view of one endpoint's stored messages and their delivery
// state. It reads stream info, messages by sequence and the existing consumer's info, and writes nothing.
func runEndpointInbox(args []string, g cliframe.Global, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	return runEp(endpointInboxSpec, g, args, env, lookup, stdout, stderr, func(rt *epRuntime) error {
		addr, o := rt.in.addr, rt.in
		if _, err := rt.adm.ResolveInbox(rt.ctx, addr.String()); err != nil {
			return failErr(err)
		}
		opts := admin.InspectOptions{Limit: o.limit}
		if o.afterSet {
			opts.From = o.after + 1
		}
		in, err := rt.adm.InspectInbox(rt.ctx, addr, opts)
		if err != nil {
			return failErr(err)
		}
		rep := buildInboxJSON(addr, in)
		total, undelivered, msgs := rep.Total, rep.Undelivered, rep.Messages
		if o.json {
			writeJSON(stdout, rep)
			return nil
		}
		if total == 0 && len(msgs) == 0 {
			fmt.Fprintf(stdout, "No messages for %s.\n", addr)
			return nil
		}
		fmt.Fprintf(stdout, "%s for %s, %d not delivered%s.\n", messages(total), addr, undelivered, malformedPart(in.Malformed))
		if len(msgs) == 0 {
			return nil
		}
		return failErr(writeInboxTable(stdout, addr, msgs, in.Messages, inboxNow()))
	})
}

// malformedPart is ", N refused as malformed" for the header line, nothing when none is. The header counts only the mail
// mashctl itself finds malformed, and says so: it cannot know the endpoint's refusal setting (ADR 0174, TO-6).
func malformedPart(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(", %d refused as malformed", n)
}

// writeInboxTable prints the message table: one row per message, FROM short inside the endpoint's own owner and group.
func writeInboxTable(stdout io.Writer, addr sap.Address, msgs []inboxMessageJSON, raw []admin.InspectedMessage, now time.Time) error {
	fmt.Fprintln(stdout)
	var buf strings.Builder
	tw := tabwriter.NewWriter(&buf, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NUMBER\tFROM\tSENT\tTRUST\tDELIVERED\tKIND\tSIZE\t")
	for i, m := range msgs {
		from := config.Clean(m.From)
		if a, err := sap.ParseAddress(m.From); err == nil && a.Owner == addr.Owner && a.Group == addr.Group {
			from = a.Instance
		}
		delivered := "no"
		switch {
		case m.Delivered:
			delivered = "yes"
		}
		kind := m.Kind
		if kind == "" {
			kind = "-"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t\n", m.Number, from, ago(now, raw[i].StoredAt), config.Clean(m.Trust), delivered, config.Clean(kind), byteSize(m.Size))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	// REASON is the last cell and the only one cut: with the ellipsis, at 79 columns, as a preview is. The header names it
	// only when some message has one.
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	anyReason := false
	for _, m := range msgs {
		anyReason = anyReason || m.Reason != ""
	}
	for i, l := range lines {
		switch {
		case i == 0 && anyReason:
			l += "REASON"
		case i > 0 && msgs[i-1].Reason != "":
			l += render.Cut(config.Clean(msgs[i-1].Reason), max(79-render.DisplayWidth(l), 1))
		}
		fmt.Fprintln(stdout, strings.TrimRight(l, " "))
	}
	return nil
}
