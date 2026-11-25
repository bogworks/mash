package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
)

const endpointDeleteUsage = `usage: mashctl endpoint delete <address> [--yes] [--dry-run] [--json]
  address    the endpoint, as owner.group.name
  --yes      do not ask
  --dry-run  print what would change, and change nothing
  --json     print the result as JSON
  Deletes the endpoint, its inbox and its credentials. The address can never be
  used again.
  Without --yes it asks you to type the address, and needs a terminal for that.
Example:
  mashctl endpoint delete acme.demo.bob
`

// stdinIsTerminal says whether standard input is a terminal; a test replaces it.
var stdinIsTerminal = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// confirmStdin is where the typed confirmation is read from; a test replaces it.
var confirmStdin = func() io.Reader { return os.Stdin }

var endpointDeleteSpec = epSpec{
	name: "delete", usage: endpointDeleteUsage, flags: []string{"--yes", "--dry-run"}, min: 1, max: 1,
	need:  "the endpoint address (owner.group.name)",
	check: checkAddress,
}

// writeJSON prints v as JSON and a newline: indented at a terminal, one compact line otherwise.
func writeJSON(w io.Writer, v any) {
	_ = cliframe.WriteJSON(w, v)
}

// inboxWords is how a message count is said: "the 3 messages in its inbox", "its empty inbox".
func inboxWords(n uint64) string {
	switch n {
	case 0:
		return "its empty inbox"
	case 1:
		return "the 1 message in its inbox"
	}
	return fmt.Sprintf("the %d messages in its inbox", n)
}

// deleteJSON is the JSON of a delete.
type deleteJSON struct {
	Address         string `json:"address"`
	Deleted         bool   `json:"deleted"`
	MessagesDeleted uint64 `json:"messages_deleted"`
	DryRun          bool   `json:"dry_run"` // false unless --dry-run
}

// confirmDelete asks the person to type the address. It runs after the endpoint is found and its messages counted, and
// before anything changes. The question goes to prompt (stderr with --json, so stdout stays one object).
func confirmDelete(prompt io.Writer, addr sap.Address, messages uint64) error {
	if !stdinIsTerminal() {
		return cliframe.Usagef("mashctl endpoint delete needs a terminal to type the address").WithHint("mashctl endpoint delete --help")
	}
	fmt.Fprintf(prompt, "This deletes %s and %s.\nMail that arrives before it is gone is lost too. It cannot be undone.\nType the address to confirm: ", addr, inboxWords(messages))
	line, err := bufio.NewReader(confirmStdin()).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(prompt)
		return cliframe.Failf("that was not the address; nothing was deleted")
	}
	if strings.TrimRight(line, "\r\n") != addr.String() {
		return cliframe.Failf("that was not the address; nothing was deleted")
	}
	return nil
}

// runEndpointDelete is `mashctl endpoint delete`.
func runEndpointDelete(args []string, g cliframe.Global, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	if helpAsked(args) {
		fmt.Fprint(stdout, endpointDeleteUsage)
		return 0
	}
	in, err := parseEpInput(endpointDeleteSpec, args)
	if err != nil {
		return cliframe.Report(stdout, stderr, err, jsonRequested(args))
	}
	opts := retireOpts{dryRun: in.flags["--dry-run"], yes: in.flags["--yes"], again: "mashctl endpoint delete " + in.addr.String()}
	prompt := stdout
	if in.json {
		prompt = stderr
	}
	opts.confirm = func(messages uint64) error { return confirmDelete(prompt, in.addr, messages) }
	res, err := retireAddress(in.addr, opts, env, lookup, stderr)
	if err != nil {
		return cliframe.Report(stdout, stderr, err, in.json)
	}
	name := in.addr.String()
	if res.planned {
		if in.json {
			writeJSON(stdout, deleteJSON{Address: name, Deleted: true, MessagesDeleted: res.messages, DryRun: true})
			return 0
		}
		fmt.Fprintf(stdout, "Would delete endpoint %s and %s\n", name, inboxWords(res.messages))
		return 0
	}
	if in.json {
		writeJSON(stdout, deleteJSON{Address: name, Deleted: true, MessagesDeleted: res.messages})
		return 0
	}
	fmt.Fprintf(stdout, "Deleted endpoint %s\n", name)
	return 0
}
