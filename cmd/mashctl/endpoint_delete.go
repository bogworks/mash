package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
)

const endpointDeleteUsage = `usage: mashctl endpoint delete <address> [--yes] [--dry-run] [--json]
  address    the endpoint, as owner.group.name
  --yes      do not ask
  --dry-run  print what would change, and change nothing
  --json     print the result as JSON
  Deletes the endpoint, its inbox and its credentials. The address can never be used again.
  Without --yes it asks you to type the address, and needs a terminal for that.
  --servers N and --sys-creds FILE override MASH_SERVERS and MASH_SYS_CREDS.
Example:
  mashctl endpoint delete acme.demo.bob
`

// verboseEnv is the environment name under which main tells a command that the global --verbose was given.
const verboseEnv = "MASH_CTL_VERBOSE_FLAG"

// stdinIsTerminal says whether standard input is a terminal; a test replaces it.
var stdinIsTerminal = func() bool {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(fi, null) {
		return false
	}
	return true
}

// confirmStdin is where the typed confirmation is read from; a test replaces it.
var confirmStdin = func() io.Reader { return os.Stdin }

var endpointDeleteSpec = epSpec{
	name: "delete", usage: endpointDeleteUsage, flags: []string{"--yes", "--dry-run"}, min: 1, max: 1,
	need: "the endpoint address (owner.group.name)",
	check: func(in *epInput) error {
		a, err := parseEndpointRef(in.pos[0])
		if err != nil {
			return cliframe.Usagef("%v", err).WithHint("mashctl endpoint delete --help")
		}
		in.addr = a
		return nil
	},
}

// slashForm is owner/group/instance.
func slashForm(a sap.Address) string { return a.Owner + "/" + a.Group + "/" + a.Instance }

// writeJSON prints v as indented JSON and a newline.
func writeJSON(w io.Writer, v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintf(w, "%s\n", b)
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
}

// deletePlanJSON is the JSON of a delete with --dry-run.
type deletePlanJSON struct {
	Address          string `json:"address"`
	Deleted          bool   `json:"deleted"`
	DryRun           bool   `json:"dry_run"`
	MessagesToDelete uint64 `json:"messages_to_delete"`
}

// confirmDelete asks the person to type the address. It runs after the endpoint is found and its messages counted, and
// before anything changes. The question goes to prompt (stderr with --json, so stdout stays one object).
func confirmDelete(prompt io.Writer, addr sap.Address, messages uint64) error {
	if !stdinIsTerminal() {
		return cliframe.Usagef("deleting needs a terminal to confirm, or --yes")
	}
	fmt.Fprintf(prompt, "This deletes %s and %s. It cannot be undone.\nType the address to confirm: ", addr, inboxWords(messages))
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
func runEndpointDelete(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
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
			writeJSON(stdout, deletePlanJSON{Address: name, DryRun: true, MessagesToDelete: res.messages})
			return 0
		}
		fmt.Fprintf(stdout, "Would delete endpoint %s and %s\nNothing was changed.\n", name, inboxWords(res.messages))
		return 0
	}
	if in.json {
		writeJSON(stdout, deleteJSON{Address: name, Deleted: true, MessagesDeleted: res.messages})
		return 0
	}
	fmt.Fprintf(stdout, "Deleted endpoint %s\n", name)
	if env(verboseEnv) != "" {
		if res.marker != "" {
			fmt.Fprintf(stdout, "retired marker: %s\n", res.marker)
		}
		if res.acked > 0 {
			fmt.Fprintf(stdout, "servers acknowledged: %d\n", res.acked)
		}
		if res.renamedTo != "" {
			fmt.Fprintf(stdout, "credentials file: renamed to %s\n", res.renamedTo)
		}
	}
	return 0
}
